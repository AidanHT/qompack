package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
)

// argsPreviewMax bounds ToolUseRecord.ArgsPreview.
const argsPreviewMax = 120 //nomagic:allow §5.8 specifies ToolUseRecord.ArgsPreview as ≤120 chars

// previewEllipsis terminates a truncated preview.
const previewEllipsis = "…"

// argsPreviewKeys are the tool_input keys ArgsDigest builds a human-readable preview from, in
// preference order. They are the arguments that actually identify what a tool call was about.
var argsPreviewKeys = []string{"file_path", "path", "pattern", "command", "url"}

// scanIndexJSONL calls fn for every non-empty line of p, in file order. A missing file is not an
// error — an index that has never been written to simply has no records yet. fn returning an
// error causes the line to be counted as malformed by the caller, never to abort the scan: a
// truncated final line from a crash must not make the store unopenable.
func scanIndexJSONL(p string, fn func(line []byte) (bool, error)) (bad int, err error) {
	f, err := os.Open(paths.Long(p))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, scannerInitialBuf), scannerMaxBuf)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		ok, ferr := fn(line)
		if ferr != nil || !ok {
			bad++
		}
	}
	if serr := sc.Err(); serr != nil {
		// A scan error (an over-long or truncated tail) is reported as malformed content rather
		// than propagated: the records already read are good and must stay usable.
		return bad + 1, nil
	}
	return bad, nil
}

// tuRec is one index/tool_use.jsonl content record, field for field in the order the record is
// written. This compact on-disk shape is deliberately NOT ToolUseRecord's own MarshalJSON, which
// is frozen to the long-key contract fixture testdata/golden/contracts/store/want/tool_use_line.jsonl
// and stays untouched: that fixture pins the TYPE's wire shape, this struct is the FILE's.
type tuRec struct {
	V      int              `json:"v"`
	ID     core.ToolUseID   `json:"id"`
	S      core.SessionID   `json:"s"`
	Turn   core.TurnIndex   `json:"turn"`
	TS     core.UnixMilli   `json:"ts"`
	Tool   string           `json:"tool"`
	ArgD   core.Hash        `json:"argd"`
	ArgP   string           `json:"argp"`
	Root   core.Hash        `json:"root"`
	Path   string           `json:"path"`
	Bytes  int64            `json:"bytes"`
	Tokens core.Tokens      `json:"tokens"`
	Sig    *signatureOnWire `json:"sig,omitempty"`
	St     Supersession     `json:"st"`
	By     core.ToolUseID   `json:"by"`
	Eph    bool             `json:"eph"`
	Sub    string           `json:"sub"`
}

// tuSupersedeRec is the append-only mutation record MarkSuperseded writes instead of rewriting an
// existing line (§7.4: nothing in the system rewrites an index from a summary).
type tuSupersedeRec struct {
	V  int            `json:"v"`
	Op string         `json:"op"`
	ID core.ToolUseID `json:"id"`
	By core.ToolUseID `json:"by"`
	TS core.UnixMilli `json:"ts"`
}

// opProbe reads only the discriminator of an index record, so the loader can tell a content
// record from a mutation record before committing to a full decode.
type opProbe struct {
	V  int    `json:"v"`
	Op string `json:"op"`
}

// tuSignature frames rec's MinHash signature for a tool_use line, reusing roots.go's
// encodeSignature so both index files agree on the wire form byte for byte — which
// TestSketchContract_ToolUseLineCarriesTheSameWireForm now checks against SP-03's frozen QPKS
// frame, because sharing a helper is not the same as staying in agreement. A signature that cannot
// be serialized is omitted, because near-duplicate detection is an optimization and must never
// fail a write; see encodeSignature for which signatures those are now that sketch is real.
func tuSignature(sig sketch.Signature) *signatureOnWire {
	m, ok := encodeSignature(sig)
	if !ok {
		return nil
	}
	return &signatureOnWire{P: sig.Perms, M: m}
}

// tuSignatureFrom is tuSignature's inverse, tolerating an absent signature.
func tuSignatureFrom(w *signatureOnWire) sketch.Signature {
	if w == nil {
		return sketch.Signature{}
	}
	return decodeSignature(*w)
}

// toolUsePath is index/tool_use.jsonl.
func (s *FSStore) toolUsePath() string { return filepath.Join(s.l.Index, toolUseFile) }

// loadToolUse replays index/tool_use.jsonl into the in-memory tool_use index.
//
// Records are applied in file order, last-wins, so a "supersede" mutation appended after a
// content record takes effect exactly as if the record had been rewritten — which is what makes
// the file append-only without losing the ability to change a record's status.
func (s *FSStore) loadToolUse() error {
	bad, err := scanIndexJSONL(s.toolUsePath(), func(line []byte) (bool, error) {
		var probe opProbe
		if err := json.Unmarshal(line, &probe); err != nil {
			return false, err
		}
		if probe.V != indexRecordVersion {
			return false, nil
		}
		switch probe.Op {
		case "":
			var r tuRec
			if err := json.Unmarshal(line, &r); err != nil {
				return false, err
			}
			rec := ToolUseRecord{
				ID: r.ID, Session: r.S, Turn: r.Turn, TS: r.TS, Tool: r.Tool,
				ArgsDigest: r.ArgD, ArgsPreview: r.ArgP, Root: r.Root, Path: r.Path,
				Bytes: r.Bytes, Tokens: r.Tokens, Signature: tuSignatureFrom(r.Sig),
				Status: r.St, SupersededBy: r.By, Ephemeral: r.Eph, Subagent: r.Sub,
			}
			s.putToolUseLocked(rec)
			return true, nil
		case "supersede":
			var r tuSupersedeRec
			if err := json.Unmarshal(line, &r); err != nil {
				return false, err
			}
			if cur, ok := s.toolUse[r.ID]; ok {
				cur.Status, cur.SupersededBy = StatusSuperseded, r.By
			}
			return true, nil
		default:
			return false, nil
		}
	})
	if err != nil {
		return err
	}
	if bad > 0 {
		s.count("store.index.badline", int64(bad))
		s.log.Warn("store: skipped malformed tool_use index lines", "file", s.toolUsePath(), "lines", bad)
	}
	return nil
}

// putToolUseLocked inserts or replaces rec in the in-memory index and keeps byPathTU ts-ordered.
// The caller holds s.mu for writing, or is Open, which runs before the store is shared.
func (s *FSStore) putToolUseLocked(rec ToolUseRecord) {
	_, existed := s.toolUse[rec.ID]
	stored := rec
	s.toolUse[rec.ID] = &stored
	if existed || rec.Path == "" {
		return
	}
	k := storeKey(rec.Path)
	ids := s.byPathTU[k]
	// Insert by TS so ToolUsesByPath can read the tail without re-sorting. Records normally
	// arrive in order, so this is an append in the common case.
	i := sort.Search(len(ids), func(i int) bool { return s.toolUse[ids[i]].TS > rec.TS })
	ids = append(ids, "")
	copy(ids[i+1:], ids[i:])
	ids[i] = rec.ID
	s.byPathTU[k] = ids
}

// RecordToolUse appends rec to index/tool_use.jsonl.
//
// It is safe to replay: re-recording an ID that is already present with the SAME Root is a silent
// no-op, which is what lets the daemon replay its WAL after a crash without producing duplicate
// index lines. Re-recording an ID with a DIFFERENT Root is an append-only violation — a tool_use
// id identifies one tool call, and one tool call has one result.
func (s *FSStore) RecordToolUse(ctx context.Context, rec ToolUseRecord) error {
	if err := s.use(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if rec.ID == "" {
		return fmt.Errorf("%w: tool_use record has no id", core.ErrNotFound)
	}
	// Section 13 invariant 7: the preview passes the same choke point as the bytes. Callers build
	// ArgsPreview from raw tool arguments, which can carry credentials, and index/tool_use.jsonl
	// is a plaintext file redaction otherwise never sees (V3-VERIFY ruling on the SP-08 parked
	// note). Re-normalize to the preview budget only when a match rewrote the string, because
	// replacement tokens may grow it past argsPreviewMax.
	if red, matches := s.deps.Redact.Redact([]byte(rec.ArgsPreview)); len(matches) > 0 {
		rec.ArgsPreview = previewString(string(red))
	}

	s.mu.Lock()
	if cur, ok := s.toolUse[rec.ID]; ok {
		if cur.Root != rec.Root {
			s.mu.Unlock()
			return fmt.Errorf("%w: tool_use %s already recorded", core.ErrAppendOnly, rec.ID)
		}
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	line, err := marshalLine(tuRec{
		V: indexRecordVersion, ID: rec.ID, S: rec.Session, Turn: rec.Turn, TS: rec.TS,
		Tool: rec.Tool, ArgD: rec.ArgsDigest, ArgP: rec.ArgsPreview, Root: rec.Root,
		Path: rec.Path, Bytes: rec.Bytes, Tokens: rec.Tokens,
		Sig: tuSignature(rec.Signature), St: rec.Status, By: rec.SupersededBy,
		Eph: rec.Ephemeral, Sub: rec.Subagent,
	})
	if err != nil {
		return err
	}
	if err := s.tuW.write(line); err != nil {
		return err
	}

	s.mu.Lock()
	s.putToolUseLocked(rec)
	s.mu.Unlock()
	return nil
}

// ToolUse looks up one tool_use record.
func (s *FSStore) ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error) {
	if err := s.use(); err != nil {
		return ToolUseRecord{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.toolUse[id]
	if !ok {
		return ToolUseRecord{}, fmt.Errorf("%w: tool_use %s", core.ErrNotFound, id)
	}
	return *rec, nil
}

// ToolUsesByPath returns the most recent limit records for path, newest first. A limit of zero or
// less means every record.
func (s *FSStore) ToolUsesByPath(ctx context.Context, path string, limit int) ([]ToolUseRecord, error) {
	if err := s.use(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := s.byPathTU[storeKey(path)]
	if len(ids) == 0 {
		return nil, nil
	}
	n := len(ids)
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]ToolUseRecord, 0, n)
	for i := len(ids) - 1; i >= 0 && len(out) < n; i-- {
		if rec, ok := s.toolUse[ids[i]]; ok {
			out = append(out, *rec)
		}
	}
	return out, nil
}

// MarkSuperseded flips older's Status to StatusSuperseded, pointing SupersededBy at by.
//
// It appends a mutation record rather than rewriting older's original line, which is what keeps
// index/tool_use.jsonl append-only (§7.4). Marking the same pair twice is a no-op that writes
// nothing, so a replayed hook cannot grow the file without bound.
func (s *FSStore) MarkSuperseded(ctx context.Context, older core.ToolUseID, by core.ToolUseID) error {
	if err := s.use(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.RLock()
	cur, haveOlder := s.toolUse[older]
	_, haveBy := s.toolUse[by]
	var already bool
	if haveOlder {
		already = cur.Status == StatusSuperseded && cur.SupersededBy == by
	}
	s.mu.RUnlock()

	if !haveOlder {
		return fmt.Errorf("%w: tool_use %s", core.ErrNotFound, older)
	}
	if !haveBy {
		return fmt.Errorf("%w: tool_use %s", core.ErrNotFound, by)
	}
	if already {
		return nil
	}

	line, err := marshalLine(tuSupersedeRec{
		V: indexRecordVersion, Op: "supersede", ID: older, By: by, TS: s.now(),
	})
	if err != nil {
		return err
	}
	if err := s.tuW.write(line); err != nil {
		return err
	}

	s.mu.Lock()
	if rec, ok := s.toolUse[older]; ok {
		rec.Status, rec.SupersededBy = StatusSuperseded, by
	}
	s.mu.Unlock()
	return nil
}

// ArgsDigest canonicalizes a tool_input JSON document and returns its digest plus the ≤120-char
// preview ToolUseRecord.ArgsPreview carries. SP-08 calls this; nothing else may re-derive it, so
// that two subplans can never disagree about what "the same tool arguments" means.
//
// Canonicalization sorts object keys recursively, preserves array order, drops insignificant
// whitespace, and re-emits every number VERBATIM as its json.Number literal. That last point is
// load-bearing: routing numbers through float64 would silently corrupt any argument above 2^53 —
// a byte offset into a large file, or an epoch-nanosecond timestamp — and two calls that differed
// only in such a value would collide on one digest.
func ArgsDigest(raw json.RawMessage) (core.Hash, string) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return core.HashBytes(core.DomainArgs, nil), ""
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		// Unparseable input is still digested, over its raw bytes, so a malformed tool_input is
		// stable and comparable rather than silently collapsing to the empty digest.
		trimmed := bytes.TrimSpace(raw)
		return core.HashBytes(core.DomainArgs, trimmed), previewString(string(trimmed))
	}

	var buf bytes.Buffer
	writeCanonJSON(&buf, v)
	canonical := buf.Bytes()
	return core.HashBytes(core.DomainArgs, canonical), argsPreview(v, canonical)
}

// writeCanonJSON writes v to buf in canonical form.
func writeCanonJSON(buf *bytes.Buffer, v any) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSONString(buf, k)
			buf.WriteByte(':')
			writeCanonJSON(buf, t[k])
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonJSON(buf, e)
		}
		buf.WriteByte(']')
	case string:
		writeJSONString(buf, t)
	case json.Number:
		buf.WriteString(t.String())
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case nil:
		buf.WriteString("null")
	default:
		// UseNumber means no float64 ever reaches here; anything else is encoded defensively so
		// canonicalization is total rather than silently dropping a value.
		writeJSONString(buf, fmt.Sprint(t))
	}
}

// writeJSONString writes s as a JSON string with HTML escaping disabled, matching every other
// JSON writer in this codebase.
func writeJSONString(buf *bytes.Buffer, s string) {
	var scratch bytes.Buffer
	enc := json.NewEncoder(&scratch)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		buf.WriteString(`""`)
		return
	}
	buf.Write(bytes.TrimRight(scratch.Bytes(), "\n"))
}

// argsPreview builds the human-readable preview: the values of the identifying argument keys when
// v is an object that has any, and the canonical JSON otherwise.
func argsPreview(v any, canonical []byte) string {
	obj, ok := v.(map[string]any)
	if !ok {
		return previewString(string(canonical))
	}
	var parts []string
	for _, k := range argsPreviewKeys {
		raw, present := obj[k]
		if !present {
			continue
		}
		if sv, isStr := raw.(string); isStr && sv != "" {
			parts = append(parts, sv)
		}
	}
	if len(parts) == 0 {
		return previewString(string(canonical))
	}
	return previewString(strings.Join(parts, " "))
}

// previewString strips control characters, collapses runs of whitespace, and truncates to
// argsPreviewMax BYTES on a rune boundary, with a trailing ellipsis when it had to cut.
func previewString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r' || r == ' ':
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			lastSpace = true
		case r < 0x20 || r == 0x7f:
			// A control character carries no information into a preview; drop it outright.
		default:
			b.WriteRune(r)
			lastSpace = false
		}
	}
	out := strings.TrimRight(b.String(), " ")
	if len(out) <= argsPreviewMax {
		return out
	}
	// Cut so that the ellipsis fits inside the byte budget, never splitting a rune.
	cut := argsPreviewMax - len(previewEllipsis)
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return out[:cut] + previewEllipsis
}
