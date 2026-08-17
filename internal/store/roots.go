package store

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/tokens"
)

// opGC is the "op" a garbage-collection tombstone line carries. A tombstone is a DIFFERENT line
// shape appended to the same file: index/roots.jsonl is append-only, so a collected root is
// retired by appending a record that retires it, never by rewriting the line that created it
// (Qompack.md §7.4).
const opGC = "gc"

// deltaToolName is the synthetic "tool" a volatile-delta side record is filed under, so the delta
// root is an ordinary, GC-visible root rather than an invisible orphan (Qompack.md §8.1).
const deltaToolName = "«deltas»"

// maxKnownClass is the highest tokens.Class ordinal this format knows. Ordinals are PINNED here
// because they are persisted: 0 prose, 1 code, 2 json, 3 diff, 4 image, 5 pdf, 6 binary, matching
// internal/tokens/class.go's declaration order.
const maxKnownClass = uint8(tokens.ClassBinary)

// The three escaping constants appendJSONString needs to reproduce encoding/json's own output
// under SetEscapeHTML(false).
const (
	// replacementRune is what invalid UTF-8 becomes. encoding/json emits the six-character ESCAPE
	// \ufffd rather than the U+FFFD rune's own bytes, and this matches it exactly so an index line
	// and a paths.AppendJSONL line are byte-identical for the same input.
	replacementRune = `\ufffd`
	// lineSeparator and paragraphSeparator are U+2028 and U+2029: valid JSON, but they terminate
	// a JavaScript string literal, so encoding/json escapes them unconditionally.
	lineSeparator      = '\u2028'
	paragraphSeparator = '\u2029'
	// escapedLineSep is the common prefix of both runes' \u escape; the final digit is '8' or '9'.
	escapedLineSep = `\u202`
)

// rootWire is the parse-side shape of every index/roots.jsonl line, content record and tombstone
// alike. Writing goes through appendRootLine's hand-written marshaller so field order is fixed and
// goldens are stable; reading has no such constraint, so it uses encoding/json.
type rootWire struct {
	V      int              `json:"v"`
	Op     string           `json:"op"`
	Root   string           `json:"root"`
	TS     core.UnixMilli   `json:"ts"`
	Tool   string           `json:"tool"`
	Path   string           `json:"path"`
	Raw    int64            `json:"raw"`
	Canon  int64            `json:"canon"`
	Tokens core.Tokens      `json:"tokens"`
	Class  *uint8           `json:"class"`
	Eph    bool             `json:"eph"`
	Chunks []chunkWire      `json:"chunks"`
	Sig    *signatureOnWire `json:"sig"`
	Deltas string           `json:"deltas"`
}

// chunkWire is one entry of a roots line's "chunks" array. The keys are deliberately short: this
// array dominates the file's size, and a store with a million chunk references pays for every
// byte of key name in it.
type chunkWire struct {
	H string `json:"h"`
	N int    `json:"n"`
}

// signatureOnWire is a MinHash signature as a roots line carries it: the permutation count plus
// the base64 of sketch.Signature's own binary form, so the sketch file format stays sketch's
// business and this package never reimplements it.
type signatureOnWire struct {
	P uint16 `json:"p"`
	M string `json:"m"`
}

// appendJSONString appends s to dst as a JSON string literal with HTML escaping DISABLED, so a
// "<" in a path or a diff survives literally.
//
// This reproduces encoding/json's own escaping under SetEscapeHTML(false), which is what every
// other JSON writer in this codebase uses (see paths.AppendJSONL): quote and backslash escaped,
// C0 controls escaped with the \n/\r/\t shortcuts where they exist and \u00XX otherwise, the two
// line-separator runes escaped because they are not valid in JavaScript string literals, and
// invalid UTF-8 replaced with U+FFFD rather than emitted raw.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '"':
				dst = append(dst, '\\', '"')
			case c == '\\':
				dst = append(dst, '\\', '\\')
			case c == '\n':
				dst = append(dst, '\\', 'n')
			case c == '\r':
				dst = append(dst, '\\', 'r')
			case c == '\t':
				dst = append(dst, '\\', 't')
			case c < 0x20:
				const hexDigits = "0123456789abcdef"
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
			default:
				dst = append(dst, c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, replacementRune...)
			i++
			continue
		}
		// U+2028/U+2029 are valid JSON but terminate a JavaScript string literal; encoding/json
		// escapes them unconditionally and so does this.
		if r == lineSeparator || r == paragraphSeparator {
			dst = append(dst, escapedLineSep...)
			dst = append(dst, byte('8'+(r-lineSeparator)))
			i += size
			continue
		}
		dst = append(dst, s[i:i+size]...)
		i += size
	}
	return append(dst, '"')
}

// appendKey appends `"name":` (with a leading comma when this is not the first field).
func appendKey(dst []byte, name string, first bool) []byte {
	if !first {
		dst = append(dst, ',')
	}
	dst = appendJSONString(dst, name)
	return append(dst, ':')
}

// marshalRootLine renders rl as one complete, newline-terminated index/roots.jsonl line.
//
// The marshaller is hand-written rather than encoding/json over a struct because field ORDER is
// part of this format: testdata/golden/store/roots.jsonl pins it byte-for-byte, and Go's struct
// field order is not a contract a golden should depend on. Optional fields — eph, sig, deltas —
// are omitted entirely rather than emitted with a zero value.
func marshalRootLine(rl rootEntry) []byte {
	dst := make([]byte, 0, 128+len(rl.Root.Chunks)*80)
	dst = append(dst, '{')
	dst = appendKey(dst, "v", true)
	dst = strconv.AppendInt(dst, indexRecordVersion, 10)
	dst = appendKey(dst, "root", false)
	dst = appendJSONString(dst, rl.Root.Hash.String())
	dst = appendKey(dst, "ts", false)
	dst = strconv.AppendInt(dst, int64(rl.TS), 10)
	dst = appendKey(dst, "tool", false)
	dst = appendJSONString(dst, rl.Tool)
	dst = appendKey(dst, "path", false)
	dst = appendJSONString(dst, rl.Path)
	dst = appendKey(dst, "raw", false)
	dst = strconv.AppendInt(dst, rl.Root.RawBytes, 10)
	dst = appendKey(dst, "canon", false)
	dst = strconv.AppendInt(dst, rl.Root.CanonBytes, 10)
	dst = appendKey(dst, "tokens", false)
	dst = strconv.AppendInt(dst, int64(rl.Root.Tokens), 10)
	dst = appendKey(dst, "class", false)
	dst = strconv.AppendUint(dst, uint64(rl.Class), 10)

	if rl.Eph {
		dst = appendKey(dst, "eph", false)
		dst = append(dst, "true"...)
	}

	dst = appendKey(dst, "chunks", false)
	dst = append(dst, '[')
	for i, c := range rl.Root.Chunks {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '{')
		dst = appendKey(dst, "h", true)
		dst = appendJSONString(dst, c.Hash.String())
		dst = appendKey(dst, "n", false)
		dst = strconv.AppendInt(dst, int64(c.Len), 10)
		dst = append(dst, '}')
	}
	dst = append(dst, ']')

	if enc, ok := encodeSignature(rl.Sig); ok {
		dst = appendKey(dst, "sig", false)
		dst = append(dst, '{')
		dst = appendKey(dst, "p", true)
		dst = strconv.AppendUint(dst, uint64(rl.Sig.Perms), 10)
		dst = appendKey(dst, "m", false)
		dst = appendJSONString(dst, enc)
		dst = append(dst, '}')
	}

	if !rl.Deltas.IsZero() {
		dst = appendKey(dst, "deltas", false)
		dst = appendJSONString(dst, rl.Deltas.String())
	}

	return append(dst, '}', '\n')
}

// encodeSignature renders sig's binary form as standard base64, reporting false when there is no
// signature to record.
//
// A MarshalBinary failure is deliberately NOT an error here: internal/sketch is still an SP-01
// stub whose MarshalBinary reports core.ErrNotImplemented on every call (Rule W-2), and a Put
// must not fail because near-duplicate detection is not available yet. The field is simply
// omitted, which is exactly what "no signature was computed" already means.
func encodeSignature(sig sketch.Signature) (string, bool) {
	if sig.Perms == 0 && len(sig.Mins) == 0 {
		return "", false
	}
	b, err := sig.MarshalBinary()
	if err != nil || len(b) == 0 {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(b), true
}

// marshalGCTombstone renders the retirement record for root.
func marshalGCTombstone(root core.Hash, ts core.UnixMilli) []byte {
	dst := make([]byte, 0, 128)
	dst = append(dst, '{')
	dst = appendKey(dst, "v", true)
	dst = strconv.AppendInt(dst, indexRecordVersion, 10)
	dst = appendKey(dst, "op", false)
	dst = appendJSONString(dst, opGC)
	dst = appendKey(dst, "root", false)
	dst = appendJSONString(dst, root.String())
	dst = appendKey(dst, "ts", false)
	dst = strconv.AppendInt(dst, int64(ts), 10)
	return append(dst, '}', '\n')
}

// appendRoot writes rl to index/roots.jsonl and publishes it to the in-memory index.
//
// The file write happens BEFORE the in-memory publish so a crash can only ever lose the publish,
// never invent one: an entry visible in memory but absent from disk would vanish on reopen, and a
// caller that already acted on the returned Root would be holding a dangling reference.
func (s *FSStore) appendRoot(rl rootEntry) error {
	if err := s.rootsW.write(marshalRootLine(rl)); err != nil {
		return fmt.Errorf("store: appending to %s: %w", rootsFile, err)
	}
	s.mu.Lock()
	s.indexRootLocked(rl)
	s.mu.Unlock()
	return nil
}

// appendGCTombstone retires root: it appends the tombstone line and drops the root from the
// in-memory index, decrementing each of its chunks' refcount. It is the only way a root leaves
// service, and it never rewrites an existing line.
func (s *FSStore) appendGCTombstone(root core.Hash) error {
	if err := s.rootsW.write(marshalGCTombstone(root, s.now())); err != nil {
		return fmt.Errorf("store: appending a gc tombstone to %s: %w", rootsFile, err)
	}
	s.mu.Lock()
	s.retireRootLocked(root)
	s.mu.Unlock()
	return nil
}

// indexRootLocked publishes one content record into the in-memory index. s.mu must be held.
func (s *FSStore) indexRootLocked(rl rootEntry) {
	entry := rl
	s.rootIndex[rl.Root.Hash] = &entry
	for _, c := range rl.Root.Chunks {
		s.chunkSet[c.Hash] = chunkLenOrUnknown(c.Len)
		s.refs[c.Hash]++
	}
	if rl.Path != "" {
		k := storeKey(rl.Path)
		s.byPath[k] = append(s.byPath[k], rl.Root.Hash)
	}
}

// chunkLenOrUnknown narrows a chunk length to the int32 the chunk set stores, reporting -1 —
// "present, length unknown" — for anything that cannot be one.
//
// The width matters because chunkSet holds one entry per chunk in the whole store, so int32 rather
// than int is what keeps a million-chunk index affordable. The GUARD matters because c.Len does not
// only come from the chunker: on the read path it comes from a parsed index line, where a corrupt
// or hostile "n" can be any int64 JSON admits. A bare int32() conversion would wrap 3e9 to a
// NEGATIVE length, and GetChunk passes that straight to getObject as the expected size — so a
// single bad digit in one index line would turn every read of that chunk into either a spurious
// integrity failure or, at exactly -1, a silently disabled length check. Reporting -1 deliberately
// is the honest version of that: the checksum still stands alone, which is the same degradation an
// object with no index entry already gets.
func chunkLenOrUnknown(n int) int32 {
	if n < 0 || n > MaxPutBytes {
		return -1
	}
	return int32(n)
}

// retireRootLocked removes a tombstoned root from the in-memory index and decrements each of its
// chunks' refcount, dropping a chunk from chunkSet once nothing references it. s.mu must be held.
func (s *FSStore) retireRootLocked(root core.Hash) {
	entry, ok := s.rootIndex[root]
	if !ok {
		return
	}
	for _, c := range entry.Root.Chunks {
		if n := s.refs[c.Hash]; n > 1 {
			s.refs[c.Hash] = n - 1
		} else {
			delete(s.refs, c.Hash)
			delete(s.chunkSet, c.Hash)
		}
	}
	if entry.Path != "" {
		k := storeKey(entry.Path)
		kept := s.byPath[k][:0]
		for _, h := range s.byPath[k] {
			if h != root {
				kept = append(kept, h)
			}
		}
		if len(kept) == 0 {
			delete(s.byPath, k)
		} else {
			s.byPath[k] = kept
		}
	}
	delete(s.rootIndex, root)
}

// loadRoots scans index/roots.jsonl once and rebuilds every in-memory root index from it.
//
// A malformed line is counted, logged once for the whole file, and skipped rather than failing the
// open: a truncated final line is the normal shape of a crash mid-append, and a store that refuses
// to open because of it would turn a recoverable interruption into total data loss.
func (s *FSStore) loadRoots() error {
	p := filepath.Join(s.l.Index, rootsFile)
	f, err := os.Open(paths.Long(p))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("store: opening %s: %w", rootsFile, err)
	}
	defer func() { _ = f.Close() }()

	s.mu.Lock()
	defer s.mu.Unlock()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, scannerInitialBuf), scannerMaxBuf)

	bad := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		rl, tombstone, root, perr := parseRootLine(line)
		if perr != nil {
			bad++
			continue
		}
		switch {
		case tombstone:
			s.retireRootLocked(root)
		default:
			if rl.Class > maxKnownClass {
				rl.Class = uint8(tokens.ClassProse)
				s.count("store.index.badclass", 1)
			}
			s.indexRootLocked(rl)
		}
	}
	if err := sc.Err(); err != nil {
		bad++
	}
	if bad > 0 {
		s.count("store.index.badline", int64(bad))
		s.log.Warn("store: skipped malformed index lines", "file", rootsFile, "lines", bad)
	}

	s.sortByPathLocked()
	return nil
}

// sortByPathLocked orders every per-path root list by the timestamp of the record that produced
// it, so "the most recent root for this path" is always the last element. s.mu must be held.
func (s *FSStore) sortByPathLocked() {
	for k, hashes := range s.byPath {
		sort.SliceStable(hashes, func(i, j int) bool {
			a, aok := s.rootIndex[hashes[i]]
			b, bok := s.rootIndex[hashes[j]]
			if !aok || !bok {
				return false
			}
			return a.TS < b.TS
		})
		s.byPath[k] = hashes
	}
}

// errUnknownRecord marks a roots line this build cannot interpret: a record version it does not
// speak, or an "op" a later wave introduced.
//
// Both are skipped rather than guessed at. Falling through to the content-record path — which is
// what happens to any line that is merely not "gc" — would publish a PHANTOM root: an entry with no
// chunks and zero bytes that Stats counts, Search ranks and GC reasons about, built entirely from a
// line whose meaning this build does not know. loadToolUse and segLog.load already skip on both
// axes; this is roots.jsonl agreeing with them.
var errUnknownRecord = errors.New("qompack: unrecognized roots index record")

// parseRootLine decodes one index/roots.jsonl line into either a content record or a tombstone.
func parseRootLine(line []byte) (rl rootEntry, tombstone bool, root core.Hash, err error) {
	var w rootWire
	if err = json.Unmarshal(line, &w); err != nil {
		return rootEntry{}, false, core.Hash{}, err
	}
	if w.V != indexRecordVersion {
		return rootEntry{}, false, core.Hash{}, errUnknownRecord
	}
	h, err := core.ParseHash(w.Root)
	if err != nil {
		return rootEntry{}, false, core.Hash{}, err
	}
	if w.Op == opGC {
		return rootEntry{}, true, h, nil
	}
	if w.Op != "" {
		return rootEntry{}, false, core.Hash{}, errUnknownRecord
	}

	rl.Root.Hash = h
	rl.TS, rl.Tool, rl.Path = w.TS, w.Tool, w.Path
	rl.Root.RawBytes, rl.Root.CanonBytes, rl.Root.Tokens = w.Raw, w.Canon, w.Tokens
	rl.Eph = w.Eph
	if w.Class != nil {
		rl.Class = *w.Class
	}
	rl.Root.Chunks = make([]ChunkRef, 0, len(w.Chunks))
	for _, c := range w.Chunks {
		ch, cerr := core.ParseHash(c.H)
		if cerr != nil {
			return rootEntry{}, false, core.Hash{}, cerr
		}
		rl.Root.Chunks = append(rl.Root.Chunks, ChunkRef{Hash: ch, Len: c.N})
	}
	if w.Sig != nil {
		rl.Sig = decodeSignature(*w.Sig)
	}
	if w.Deltas != "" {
		if d, derr := core.ParseHash(w.Deltas); derr == nil {
			rl.Deltas = d
		}
	}
	return rl, false, h, nil
}

// decodeSignature rebuilds a MinHash signature from its wire form. A signature that fails to
// decode — which every signature does while internal/sketch is still a stub — degrades to the
// permutation count alone rather than failing the whole line.
func decodeSignature(w signatureOnWire) sketch.Signature {
	sig := sketch.Signature{Perms: w.P}
	raw, err := base64.StdEncoding.DecodeString(w.M)
	if err != nil {
		return sig
	}
	var decoded sketch.Signature
	if err := decoded.UnmarshalBinary(raw); err != nil {
		return sig
	}
	return decoded
}
