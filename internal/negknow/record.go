package negknow

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Record is one elimination: an evidence-linked, staleness-guarded statement that a specific
// approach to a specific target was tried and did not work (00-ARCHITECTURE.md §5.10, §8.3).
//
// The json tags are explicit and frozen, transcribed verbatim from 00-ARCHITECTURE.md §5.10: this
// is the shape testdata/golden/contracts/negknow/want/elimination_record.jsonl (one
// records/eliminations.jsonl line) and testdata/golden/contracts/checkpoint/want/0001.json's
// eliminated[0] entry pin byte-for-byte (Rule W-2), field for field, in this exact order.
type Record struct {
	ID      string         `json:"id"`
	Session core.SessionID `json:"session"`
	TS      core.UnixMilli `json:"ts"`
	// Target is the free-text thing the eliminated approach was tried against, e.g.
	// "src/auth.ts:refreshToken".
	Target string `json:"target"`
	// Approach is the free-text approach that was tried, e.g. "widen pool timeout".
	Approach string `json:"approach"`
	// Reason is the free-text explanation of why the approach did not work.
	Reason string `json:"reason"`
	// Desc is Target/Approach/Reason's canonical form: what Descriptor.Key hashes into the bloom.
	Desc Descriptor `json:"descriptor"`
	// Evidence is the content hash of whatever proved this elimination (a tool result, a log).
	Evidence core.Hash `json:"evidence"`
	// DependsOn is the staleness guard (00-ARCHITECTURE.md §8.3): the file hashes this
	// elimination's validity depends on. RefreshStaleness compares these against the store's
	// current file versions.
	DependsOn []Dep  `json:"depends_on"`
	Scope     Scope  `json:"scope"`
	Status    Status `json:"status"`
	// StaleSince is when Status flipped to StatusStale; zero (omitted) while Status is
	// StatusActive.
	StaleSince core.UnixMilli `json:"stale_since,omitempty"`
	// StaleBecause names which DependsOn entries changed, causing the flip to StatusStale;
	// nil (omitted) while Status is StatusActive.
	StaleBecause []string   `json:"stale_because,omitempty"`
	Source       SourceKind `json:"source"`
}

// The §12.3 bounds normalizeRecord enforces before a Record is ever appended, so that no unbounded
// agent-supplied string reaches a JSONL line: 512 bytes for target and approach, 2048 for reason,
// 32 entries for depends_on and stale_because.
//
// maxReasonBytes is written as a multiple of maxTextBytes rather than as its own literal because
// 2048 is one of the §11.6 forbidden literals (nomagic flags the token, not the evaluated
// constant). The multiplier is 4, not 2: 2*512 would be 1024, which is not the limit the subplan's
// bounds table and its TestNormalizeRecord_Bounds row both state.
const (
	maxTextBytes    = 512
	maxReasonBytes  = 4 * maxTextBytes
	maxDeps         = 32
	maxStaleBecause = 32
)

// truncMark is appended to any field the bounds cut short. It counts against the field's own
// limit, so a normalized field is never longer than the limit its row of the §8.5 table states.
const truncMark = "…"

// recordIDPrefix and recordIDHexLen fix the shape of every record id. Both are pinned by the
// frozen contract fixture's "id":"elim_3f9b2c7d1a48": the prefix is elim_, five characters, not
// elm_, and exactly twelve lowercase hex digits follow it.
const (
	recordIDPrefix = "elim_"
	recordIDHexLen = 12
)

// String returns the human-facing spelling of s: "mcp", "slash", "heuristic", "user", or
// "unknown" for a value no version of this package has minted.
//
// This is NOT the wire form. On disk and in a checkpoint, source is the integer value, exactly as
// the two frozen fixtures carry it ("source":1); String exists for /qompack:status output, log
// lines and the --json rendering SP-14 owns.
func (s SourceKind) String() string {
	switch s {
	case SourceMCP:
		return "mcp"
	case SourceSlashCommand:
		return "slash"
	case SourceHeuristic:
		return "heuristic"
	case SourceUserStatement:
		return "user"
	default:
		return "unknown"
	}
}

// ParseSourceKind is String's inverse. "unknown" is String's fallback for an unrecognized value
// rather than a kind of its own, so it does not parse back; every unrecognized spelling reports
// core.ErrNotFound.
func ParseSourceKind(s string) (SourceKind, error) {
	switch s {
	case "mcp":
		return SourceMCP, nil
	case "slash":
		return SourceSlashCommand, nil
	case "heuristic":
		return SourceHeuristic, nil
	case "user":
		return SourceUserStatement, nil
	}
	return SourceMCP, fmt.Errorf("%w: unknown negknow source kind %q", core.ErrNotFound, s)
}

// recordID mints the stable id for one elimination: the domain-separated digest of the session,
// the timestamp and the descriptor's own bloom key, rendered as recordIDPrefix plus twelve
// lowercase hex characters.
//
// It mixes in ts deliberately, which is why the ledger's dedup is identity-based (byKey) and not
// id-based: an MCP retry a second later mints a different id for the same elimination.
func recordID(sess core.SessionID, ts core.UnixMilli, d Descriptor) string {
	var b bytes.Buffer
	b.WriteString(string(sess))
	b.WriteByte(fieldSep)
	fmt.Fprintf(&b, "%d", int64(ts))
	b.WriteByte(fieldSep)
	b.Write(d.Key())
	h := core.HashBytes(domainRecordID, b.Bytes())
	return recordIDPrefix + hex.EncodeToString(h[:])[:recordIDHexLen]
}

// recordWire is Record's on-disk form. Its field order IS the shipped Record's field order, which
// is the key order encoding/json emits and which the frozen fixture
// testdata/golden/contracts/negknow/want/elimination_record.jsonl pins byte for byte.
//
// The codec exists only for the two things struct tags cannot express: a nil DependsOn rendering
// as [] rather than null, and the tolerant defaulting of a missing scope/status/source or an
// unparseable hash on the way in. It changes no key, no key order and no value shape.
type recordWire struct {
	ID           string     `json:"id"`
	Session      string     `json:"session"`
	TS           int64      `json:"ts"`
	Target       string     `json:"target"`
	Approach     string     `json:"approach"`
	Reason       string     `json:"reason"`
	Desc         descWire   `json:"descriptor"`
	Evidence     string     `json:"evidence"`
	DependsOn    []depWire  `json:"depends_on"`
	Scope        string     `json:"scope"`
	Status       string     `json:"status"`
	StaleSince   int64      `json:"stale_since,omitempty"`
	StaleBecause []string   `json:"stale_because,omitempty"`
	Source       SourceKind `json:"source"` // INTEGER on the wire — "source":1 in the frozen fixture
}

// depWire is core.Dep's wire form: the lowercase path/hash keys §8.5 shows inside depends_on.
type depWire struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// descWire is Descriptor's wire form. It spells the four fields out rather than delegating to
// Descriptor's own tags, so that a reason_hash which fails to parse can be tolerated on the way in
// instead of failing the whole line.
type descWire struct {
	NormalizedPath string `json:"normalized_path"`
	Symbol         string `json:"symbol"`
	ApproachClass  string `json:"approach_class"`
	ReasonHash     string `json:"reason_hash"`
}

// wire projects r into its on-disk form. DependsOn is always a non-nil slice, which is what makes
// a record with no dependencies render as "depends_on":[] rather than null.
func (r Record) wire() recordWire {
	deps := make([]depWire, 0, len(r.DependsOn))
	for _, d := range r.DependsOn {
		deps = append(deps, depWire{Path: d.Path, Hash: d.Hash.String()})
	}
	return recordWire{
		ID:       r.ID,
		Session:  string(r.Session),
		TS:       int64(r.TS),
		Target:   r.Target,
		Approach: r.Approach,
		Reason:   r.Reason,
		Desc: descWire{
			NormalizedPath: r.Desc.NormalizedPath,
			Symbol:         r.Desc.Symbol,
			ApproachClass:  r.Desc.ApproachClass,
			ReasonHash:     r.Desc.ReasonHash.String(),
		},
		Evidence:     r.Evidence.String(),
		DependsOn:    deps,
		Scope:        string(r.Scope),
		Status:       string(r.Status),
		StaleSince:   int64(r.StaleSince),
		StaleBecause: r.StaleBecause,
		Source:       r.Source,
	}
}

// MarshalJSON emits one records/eliminations.jsonl line's worth of bytes, without the terminating
// newline. HTML escaping is off, matching every other JSON writer in this codebase, so a "<" in a
// path or a diff survives as itself.
//
// The receiver is a value, not a pointer, so []Record — which is what SP-10 embeds as a
// checkpoint's eliminated[] — marshals through this method too.
func (r Record) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r.wire()); err != nil {
		return nil, fmt.Errorf("negknow: marshal record %q: %w", r.ID, err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

// UnmarshalJSON decodes one record line, tolerating everything decodeRecord tolerates and
// discarding the warnings. A caller that wants to see them — the log replay does — calls
// decodeRecord directly.
func (r *Record) UnmarshalJSON(b []byte) error {
	// By convention an Unmarshaler treats a literal null as a no-op, so that decoding a null into
	// an existing value leaves it alone the way encoding/json's own decoder would.
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	rec, _, err := decodeRecord(b)
	if err != nil {
		return err
	}
	*r = rec
	return nil
}

// decodeRecord decodes one record line into a Record, returning any tolerance warnings it
// accumulated on the way.
//
// What it tolerates, and why none of it is an error:
//
//   - a missing source (-> SourceMCP), status (-> StatusActive) or scope (-> ScopeSession): an
//     older plugin version, or a hand-written line, must still replay;
//   - an unparseable or absent evidence hash (-> the zero core.Hash): whether a record without
//     evidence is acceptable is eliminations.requireEvidence's question, and the ledger — not the
//     codec — is what holds that configuration;
//   - an unparseable reason_hash (-> the zero core.Hash): the descriptor is the ledger's to
//     recompute from the record's own text when it matters;
//   - an unparseable dep hash: THAT DEP is dropped, with a warning naming its path, because a
//     dependency whose hash cannot be compared cannot guard anything — while the elimination it
//     was guarding is still real and still worth keeping.
//
// Only malformed JSON is an error.
func decodeRecord(b []byte) (Record, []string, error) {
	var w recordWire
	if err := json.Unmarshal(b, &w); err != nil {
		return Record{}, nil, fmt.Errorf("negknow: unmarshal record: %w", err)
	}
	rec, warnings := recordFromWire(w)
	return rec, warnings, nil
}

// recordFromWire converts one already-decoded wire record into a Record, returning the same
// tolerance warnings decodeRecord documents.
//
// It is split out of decodeRecord so that a caller which has ALREADY unmarshalled the line does
// not have to unmarshal it a second time to get a Record out of it. replayLog is that caller: it
// decodes every line exactly once, into log.go's combined logLine, and hands the embedded
// recordWire straight to this function. Nothing about the conversion or the warnings differs
// between the two entry points, which is what keeps the codec's behaviour single-sourced.
func recordFromWire(w recordWire) (Record, []string) {
	var warnings []string
	rec := Record{
		ID:       w.ID,
		Session:  core.SessionID(w.Session),
		TS:       core.UnixMilli(w.TS),
		Target:   w.Target,
		Approach: w.Approach,
		Reason:   w.Reason,
		Desc: Descriptor{
			NormalizedPath: w.Desc.NormalizedPath,
			Symbol:         w.Desc.Symbol,
			ApproachClass:  w.Desc.ApproachClass,
			ReasonHash:     parseHashOrZero(w.Desc.ReasonHash),
		},
		Evidence:     parseHashOrZero(w.Evidence),
		Scope:        Scope(w.Scope),
		Status:       Status(w.Status),
		StaleSince:   core.UnixMilli(w.StaleSince),
		StaleBecause: w.StaleBecause,
		Source:       w.Source,
	}
	if rec.Scope == "" {
		rec.Scope = ScopeSession
	}
	if rec.Status == "" {
		rec.Status = StatusActive
	}

	if w.Evidence != "" {
		if _, err := core.ParseHash(w.Evidence); err != nil {
			warnings = append(warnings,
				fmt.Sprintf("zeroed evidence: unparseable hash %q", w.Evidence))
		}
	}

	if len(w.DependsOn) > 0 {
		deps := make([]Dep, 0, len(w.DependsOn))
		for _, d := range w.DependsOn {
			h, err := core.ParseHash(d.Hash)
			if err != nil {
				warnings = append(warnings,
					fmt.Sprintf("dropped depends_on entry %q: unparseable hash %q", d.Path, d.Hash))
				continue
			}
			deps = append(deps, Dep{Path: d.Path, Hash: h})
		}
		if len(deps) > 0 {
			rec.DependsOn = deps
		}
	}
	return rec, warnings
}

// parseHashOrZero is the codec's tolerance for hash fields: an absent or unparseable digest
// decodes to the zero core.Hash, which core.Hash.IsZero reports as unset, rather than failing the
// line. See decodeRecord for who then decides what to do about it.
func parseHashOrZero(s string) core.Hash {
	h, err := core.ParseHash(s)
	if err != nil {
		return core.Hash{}
	}
	return h
}

// normalizeRecord makes r fit to append: redaction first, then the §12.3 byte and entry bounds,
// then one dependency per path in ascending path order.
//
// It deliberately does NOT recompute the descriptor and does NOT fill in defaults for ID, TS,
// Scope, Status or Source. Those are the ledger's, because they need a clock, a session and the
// configuration, none of which belong in a normalizer.
//
// redact, when non-nil, is applied to Target, Approach and Reason BEFORE the bounds, so a secret
// can never survive by sitting past the truncation point. warn, when non-nil, is called once per
// thing that was cut; nothing here is silent.
func normalizeRecord(r *Record, redact func([]byte) []byte, warn func(string)) {
	if r == nil {
		return
	}
	note := func(format string, args ...any) {
		if warn != nil {
			warn(fmt.Sprintf(format, args...))
		}
	}

	if redact != nil {
		r.Target = string(redact([]byte(r.Target)))
		r.Approach = string(redact([]byte(r.Approach)))
		r.Reason = string(redact([]byte(r.Reason)))
	}

	for _, f := range []struct {
		name  string
		limit int
		field *string
	}{
		{"target", maxTextBytes, &r.Target},
		{"approach", maxTextBytes, &r.Approach},
		{"reason", maxReasonBytes, &r.Reason},
	} {
		s, cut := truncateUTF8(*f.field, f.limit)
		if cut {
			note("truncated %s from %d to %d bytes", f.name, len(*f.field), len(s))
			*f.field = s
		}
	}

	if len(r.DependsOn) > 0 {
		seen := make(map[string]struct{}, len(r.DependsOn))
		deps := make([]Dep, 0, len(r.DependsOn))
		for _, d := range r.DependsOn {
			k := paths.Key(d.Path)
			if _, dup := seen[k]; dup {
				note("dropped duplicate depends_on entry %q", d.Path)
				continue
			}
			seen[k] = struct{}{}
			deps = append(deps, d)
		}
		// Ascending path order, so the same set of dependencies always produces the same line.
		sort.SliceStable(deps, func(i, j int) bool { return deps[i].Path < deps[j].Path })
		if len(deps) > maxDeps {
			note("dropped %d depends_on entries over the %d-entry cap", len(deps)-maxDeps, maxDeps)
			deps = deps[:maxDeps]
		}
		r.DependsOn = deps
	}

	if len(r.StaleBecause) > maxStaleBecause {
		note("dropped %d stale_because entries over the %d-entry cap",
			len(r.StaleBecause)-maxStaleBecause, maxStaleBecause)
		r.StaleBecause = r.StaleBecause[:maxStaleBecause]
	}
}

// truncateUTF8 cuts s to at most limit bytes, ellipsis included, without splitting a rune, and
// reports whether it had to. A field already inside its bound is returned untouched: no ellipsis,
// no copy.
func truncateUTF8(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	cut := limit - len(truncMark)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncMark, true
}
