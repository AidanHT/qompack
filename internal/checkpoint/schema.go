package checkpoint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
)

// createdLayout is the time layout of Checkpoint.Created: RFC 3339 UTC with millisecond
// precision, exactly as Qompack.md §8.5 spells the artifact's own timestamp. The trailing Z is a
// literal, not a numeric-zone directive, because CreatedNow always formats a UTC instant.
//
// It applies to Checkpoint.Created and to nothing else. paths.ManifestEntry.Created is a
// core.UnixMilli NUMBER on a different file, and conflating the two makes every manifest this
// package writes unreadable by the package that owns it (00-ARCHITECTURE.md §3.3).
const createdLayout = "2006-01-02T15:04:05.000Z"

// defaultSketchRefs is the sketch_refs value every checkpoint carries: exactly the two keys
// Qompack.md §8.5 shows, no more. It is returned by value from ensureNonNil so no caller can
// mutate a shared map.
func defaultSketchRefs() map[string]string {
	return map[string]string{"tried": "tried.bloom", "touch": "touch.cms"}
}

// ensureNonNil initializes every nil slice to an empty slice and SketchRefs to the two-key
// constant, so that encoded_segments, invariants, eliminated, decisions, open_questions, dropped,
// pointers.files, pointers.tools and every decision's alternatives_rejected serialize as [] and
// never as null.
//
// A null where an array belongs is not cosmetic: SP-11's rehydrator and SP-13's retrieval tools
// range over these fields, and a JSON null that decodes to a nil slice in Go decodes to something
// far less forgiving in the jq expressions /qompack:status and the e2e suite use.
//
// It descends into Decisions because that is where the nil actually comes from: two of
// ExtractDecisions' three sources mint a Decision without ever touching AlternativesRejected, and
// the clones on the way to Marshal (mergeDecisionsLocked, snapshotLocked) are slices.Clone, which
// returns nil for nil. eliminated[].depends_on needs no such pass — negknow.Record marshals
// through a wire projection that materializes deps non-nil — and that asymmetry is exactly why the
// decision-side nil is easy to miss.
func (c *Checkpoint) ensureNonNil() {
	if c.EncodedSegments == nil {
		c.EncodedSegments = []core.SegmentID{}
	}
	if c.Invariants == nil {
		c.Invariants = []Invariant{}
	}
	if c.Eliminated == nil {
		c.Eliminated = []negknow.Record{}
	}
	if c.Decisions == nil {
		c.Decisions = []Decision{}
	}
	// Copy on write. Marshal takes its Checkpoint by value, but a value copy shares the DECISION
	// ELEMENTS through the slice's backing array, so writing a field of c.Decisions[i] in place
	// would reach back into the caller's checkpoint. Marshal is called from inside Truncate's
	// sizeOf, and Truncate is required to return a checkpoint that fits its budget unchanged, so
	// that write would turn a size measurement into a mutation of the thing being measured.
	cloned := false
	for i := range c.Decisions {
		if c.Decisions[i].AlternativesRejected != nil {
			continue
		}
		if !cloned {
			c.Decisions, cloned = slices.Clone(c.Decisions), true
		}
		c.Decisions[i].AlternativesRejected = []string{}
	}
	if c.OpenQuestions == nil {
		c.OpenQuestions = []string{}
	}
	if c.UserIntent.Evolution == nil {
		c.UserIntent.Evolution = []string{}
	}
	if c.Pointers.Files == nil {
		c.Pointers.Files = []FilePointer{}
	}
	if c.Pointers.Tools == nil {
		c.Pointers.Tools = []ToolPointer{}
	}
	if c.Dropped == nil {
		c.Dropped = []DropEntry{}
	}
	if len(c.SketchRefs) == 0 {
		c.SketchRefs = defaultSketchRefs()
	}
}

// Marshal renders c as the canonical checkpoint bytes: two-space indent, HTML escaping off, one
// trailing newline. Identical input yields byte-identical output, which is what lets the manifest
// digest mean anything and what TestGoldenCheckpointRoundTrip asserts.
//
// HTML escaping is off because a checkpoint carries user intent and error text verbatim, and
// rewriting the angle brackets and ampersands in an error message into their escape sequences
// would make the artifact diverge from the original it exists to preserve.
//
// Marshal never runs Migrate: it writes at SchemaVersion by construction. Unmarshal is the
// direction migration belongs in.
func Marshal(c Checkpoint) ([]byte, error) {
	c.ensureNonNil()
	c.Version = SchemaVersion

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("checkpoint: marshal: %w", err)
	}
	// Encoder.Encode already terminates with exactly one newline.
	return buf.Bytes(), nil
}

// Unmarshal decodes raw into a Checkpoint, running Migrate first so that an artifact written by an
// older schema version is read at the current one.
//
// DisallowUnknownFields is deliberately OFF. A checkpoint written by a newer plugin is rejected by
// Migrate on its version field, which is the honest signal; an unknown field on a document that
// passed the version check is forward compatibility working as intended, so it is dropped and
// named in a Warn rather than failing the read.
func Unmarshal(raw []byte) (Checkpoint, error) {
	migrated, err := Migrate(raw)
	if err != nil {
		return Checkpoint{}, err
	}

	var c Checkpoint
	if err := json.Unmarshal(migrated, &c); err != nil {
		return Checkpoint{}, fmt.Errorf("checkpoint: unmarshal: %w", err)
	}
	warnUnknownFields(migrated)
	c.ensureNonNil()
	return c, nil
}

// warnUnknownFields decodes raw a second time with DisallowUnknownFields to name any field the
// current schema does not know, at Warn. It never fails the read — see Unmarshal.
func warnUnknownFields(raw []byte) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var probe Checkpoint
	if err := dec.Decode(&probe); err != nil {
		pkgLog().Warn("checkpoint: unknown field dropped on read", "detail", err.Error())
	}
}

// CreatedNow renders clk's current instant in Checkpoint.Created's layout. It populates that field
// and nothing else.
func CreatedNow(clk core.Clock) string {
	return clk.Now().UTC().Format(createdLayout)
}
