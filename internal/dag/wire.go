package dag

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// This file is dag/deps.jsonl's record codec — the file 00-ARCHITECTURE.md §7.4 names in the
// directory layout as "dag/deps.jsonl # dependence edges for slicing". One record per line,
// newline-terminated, UTF-8, discriminated by a "type" field a reader can see before it decodes
// anything else.
//
// # The node and edge shapes are frozen, not chosen
//
// testdata/golden/contracts/dag/want/node_line.jsonl and edge_line.jsonl are frozen under Rule W-2
// and MANIFEST.json declares them to BE this file's line format. So the node and edge records are
// not an envelope this codec invents: they are the bytes Node.MarshalJSON and Edge.MarshalJSON
// already produce, marshalled from the Node and Edge values themselves. An earlier draft of this
// subplan specified a {"v":1,"r":"n",…} envelope with short keys; it is superseded, because it
// would contradict a frozen fixture.
//
//	{"type":"node","id":"file:src/auth.ts","kind":4,…,"ephemeral":false}
//	{"type":"edge","from":"tooluse:toolu_01A","to":"file:src/auth.ts","kind":2,"weight":1,"turn":61}
//	{"type":"tombstone","id":"file:src/auth.ts","ts":1767225480000}
//	{"type":"generation","v":1,"gen":1,"ts":1767225480000,"nodes":812,"edges":2104}
//
// The tombstone and generation shapes are this subplan's own; no fixture freezes them. "kind" is
// always the NUMERIC kind — see kinds.go for why NodeKind and EdgeKind must never grow a
// MarshalText method.
//
// # Where the version lives
//
// Only the generation line carries a schema version, and that is deliberate. Versioning every line
// would spend bytes on every one of a session's thousands of records to answer a question that is
// asked once per file; putting it on the header a compaction writes puts it exactly where a reader
// meets it first. A generation line from a newer schema is skipped in silence, which leaves the
// node and edge records — whose shapes are frozen and therefore cannot drift — readable by an
// older build.

// wireVersion is the schema version the generation header carries. It moves only if a record shape
// changes incompatibly, which for node and edge lines Rule W-2 forbids outright.
const wireVersion = 1

// maxLineBytes is the ceiling on one encoded record. A line longer than this is refused at write
// time (wrapping ErrInvalidNode or ErrInvalidEdge) and dropped at read time with LoadErrors++.
//
// The bound exists because a single pathological record must not be able to make the whole log
// unreadable: bufio.Scanner needs a maximum token size up front, and without an agreed one a
// writer could emit a line no reader could ever buffer. 1 MiB is far beyond any legitimate record —
// a NodeID's key is capped at 384 bytes by sanitizeKey and every other field is a small scalar.
const maxLineBytes = 1 << 20

// recordNewline terminates every record. It is also the byte that must never appear INSIDE one: a
// raw newline mid-record would split a single line into two unparseable fragments, which is why
// sanitizeKey strips control bytes from every NodeID and why encoding/json escapes them in every
// string field it writes.
const recordNewline = '\n'

// tombstoneLine is the on-disk shape of a Tombstone call: the id the store's GC collected and when.
// It carries no kind, because a tombstone is a statement about an id, and an id whose node record
// never arrived is still legitimately tombstoned (replaying it must be a no-op, not an error).
type tombstoneLine struct {
	Type string         `json:"type"`
	ID   NodeID         `json:"id"`
	TS   core.UnixMilli `json:"ts"`
}

// generationLine is the header Compact writes as the first record of every rewritten log. Nodes
// and Edges are advisory: they let a human reading the file — or a triage script — see what the
// rewrite claimed to contain without parsing the rest of it. The loader does not trust them; the
// authoritative counts are the records that follow.
type generationLine struct {
	Type  string         `json:"type"`
	V     int            `json:"v"`
	Gen   int            `json:"gen"`
	TS    core.UnixMilli `json:"ts"`
	Nodes int            `json:"nodes"`
	Edges int            `json:"edges"`
}

// wireProbe reads a line's discriminator and nothing else, so the decoder can pick a concrete type
// before committing to one. Decoding straight into Node and falling back on failure would be
// wrong: an edge line unmarshals into a Node without error (no field matches, so every field stays
// zero) and would be applied as an empty node.
type wireProbe struct {
	Type string `json:"type"`
}

// encodeLine renders v as one compact JSON line with HTML escaping DISABLED, and without the
// trailing newline json.Encoder appends.
//
// Escaping is the whole reason this is not a bare json.Marshal. Marshal rewrites "<", ">" and "&"
// as their six-character backslash-u escapes, so a file path containing any of them
// would land in deps.jsonl in a form no `grep 'src/<a>&b.ts'` could find — and an operator reading
// a log they cannot grep is an operator who cannot debug the session. Every other JSON writer in
// this codebase disables it too; see paths.AppendJSONL.
func encodeLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{recordNewline}), nil
}

// encodeGeneration renders the header Compact writes at the top of a rewritten log.
func encodeGeneration(gen int, ts core.UnixMilli, nodes, edges int) ([]byte, error) {
	return encodeLine(generationLine{
		Type: string(recGen), V: wireVersion, Gen: gen, TS: ts, Nodes: nodes, Edges: edges,
	})
}

// encodeRecord renders one pending record as its deps.jsonl line, without the terminating newline.
//
// A line over maxLineBytes is refused here rather than truncated or dropped, and the refusal wraps
// ErrInvalidNode or ErrInvalidEdge to say WHICH record cannot be written: silently discarding it
// would leave the graph in memory describing relationships the log does not, and truncating it
// would put a fragment on disk that the loader would then count as corruption forever.
func encodeRecord(rec record) ([]byte, error) {
	var (
		line     []byte
		err      error
		overflow error // the sentinel an over-long line of this kind wraps
	)
	switch rec.kind {
	case recNode:
		line, err = encodeLine(rec.node)
		overflow = ErrInvalidNode
	case recEdge:
		line, err = encodeLine(rec.edge)
		overflow = ErrInvalidEdge
	case recTomb:
		line, err = encodeLine(tombstoneLine{Type: string(recTomb), ID: rec.id, TS: rec.ts})
		overflow = ErrInvalidNode
	case recGen:
		// No pending queue ever holds a generation record — Compact writes its header straight
		// into the rewrite buffer with the real counts — but the union is kept total so that a
		// future producer cannot be silently dropped by an unhandled case.
		line, err = encodeGeneration(rec.gen, rec.ts, 0, 0)
		overflow = ErrInvalidNode
	default:
		return nil, fmt.Errorf("dag: cannot encode a record of unknown kind %q", string(rec.kind))
	}
	if err != nil {
		return nil, fmt.Errorf("dag: encode %s record: %w", string(rec.kind), err)
	}
	if len(line) > maxLineBytes {
		return nil, fmt.Errorf("%w: encoded %s line is %d bytes, over the %d-byte record limit",
			overflow, string(rec.kind), len(line), maxLineBytes)
	}
	return line, nil
}

// decodeRecord parses one deps.jsonl line.
//
// The three return values separate three genuinely different outcomes, which the loader treats
// three different ways:
//
//   - (rec, true, nil): a record this build understands. Apply it.
//   - (_, false, nil): nothing to apply, and nothing wrong. A blank line, a "type" this build has
//     never heard of, or a generation header written to a newer schema version. All three mean a
//     NEWER WRITER, not damage, so they are skipped in silence — counting them would make every
//     forward-compatible addition look like corruption to the version that predates it.
//   - (_, _, err): the line is damaged. The caller counts it (LoadErrors++), skips it, and reports
//     the total once per Open — degradation is loud (§13 invariant 10) but never fatal.
//
// "Damaged" covers malformed JSON, a known "type" whose payload will not unmarshal, a numeric kind
// outside the declared range, and an id that does not parse under the D-2 scheme. Those four are
// all cases where the line names a shape this build owns and then fails to be one.
func decodeRecord(line []byte) (rec record, recognised bool, err error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return record{}, false, nil // a blank line carries no record and is not damage
	}

	var probe wireProbe
	if probeErr := json.Unmarshal(line, &probe); probeErr != nil {
		return record{}, false, fmt.Errorf("dag: malformed record: %w", probeErr)
	}

	switch recKind(probe.Type) {
	case recNode:
		var n Node
		if unmarshalErr := json.Unmarshal(line, &n); unmarshalErr != nil {
			return record{}, false, fmt.Errorf("dag: malformed node record: %w", unmarshalErr)
		}
		if n.Kind >= KindInvalid {
			return record{}, false, fmt.Errorf("dag: node record carries kind %d, which is not a node kind", uint8(n.Kind))
		}
		if _, _, ok := ParseNodeID(n.ID); !ok {
			return record{}, false, fmt.Errorf("dag: node record id %q does not parse", string(n.ID))
		}
		return record{kind: recNode, node: n}, true, nil

	case recEdge:
		var e Edge
		if unmarshalErr := json.Unmarshal(line, &e); unmarshalErr != nil {
			return record{}, false, fmt.Errorf("dag: malformed edge record: %w", unmarshalErr)
		}
		if e.Kind >= EdgeInvalid {
			return record{}, false, fmt.Errorf("dag: edge record carries kind %d, which is not an edge kind", uint8(e.Kind))
		}
		if _, _, ok := ParseNodeID(e.From); !ok {
			return record{}, false, fmt.Errorf("dag: edge record from %q does not parse", string(e.From))
		}
		if _, _, ok := ParseNodeID(e.To); !ok {
			return record{}, false, fmt.Errorf("dag: edge record to %q does not parse", string(e.To))
		}
		return record{kind: recEdge, edge: e}, true, nil

	case recTomb:
		var tomb tombstoneLine
		if unmarshalErr := json.Unmarshal(line, &tomb); unmarshalErr != nil {
			return record{}, false, fmt.Errorf("dag: malformed tombstone record: %w", unmarshalErr)
		}
		if tomb.ID == "" {
			return record{}, false, fmt.Errorf("dag: tombstone record names no id")
		}
		return record{kind: recTomb, id: tomb.ID, ts: tomb.TS}, true, nil

	case recGen:
		var gen generationLine
		if unmarshalErr := json.Unmarshal(line, &gen); unmarshalErr != nil {
			return record{}, false, fmt.Errorf("dag: malformed generation record: %w", unmarshalErr)
		}
		if gen.V > wireVersion {
			return record{}, false, nil // written by a newer build; the records after it still read
		}
		if gen.V < wireVersion {
			// Every writer of this format stamps the version. A header without one is not an older
			// schema — there has never been one — it is a damaged line.
			return record{}, false, fmt.Errorf("dag: generation record carries schema version %d, want %d", gen.V, wireVersion)
		}
		return record{kind: recGen, gen: gen.Gen, ts: gen.TS}, true, nil

	default:
		return record{}, false, nil // a record kind from a newer writer, not corruption
	}
}
