package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
)

// This file is SP-16 §3's per-segment filter: the LSM-style acceleration 00-ARCHITECTURE.md §6.8
// describes and §5.8 reserved, whose Segment.BloomRef has read `"" until SP-16` since SP-06.
//
// A per-segment filter can only ever SKIP work. It never answers a query, and §3 is explicit
// about the two rules that keep that true:
//
// A POSITIVE is confirmed exactly. A bloom hit means "maybe", and the exact index is what decides.
// This is the same rule §13 invariant 3 already imposes on tried.bloom, and it is why Lookup
// returns a verdict rather than a bool — there is no spelling of "yes" here for a caller to
// mistake for an answer.
//
// A NEGATIVE is only trusted when coverage is COMPLETE and CURRENT. A filter built over a subset
// of a segment's keys, or built against an older generation of it, can say "definitely not
// present" about something that is. Both cases return FilterBypass, which costs a scan the caller
// would have paid anyway; the alternative costs a wrong answer.
//
// The second rule is what makes bounded capacity honest. §3 and ledger row A06 both say a fixed
// filter cannot hold an unbounded insertion stream at a fixed error rate, so this file does not
// pretend otherwise: a segment with more keys than the build cap is filtered INCOMPLETELY and
// every negative over it is bypassed. Nothing degrades silently and no false-positive-rate promise
// is made that the mechanism cannot keep.
//
// Coverage and generation live in the FILTER FILE, not in the Segment record. Segment's json tags
// are frozen by testdata/golden/contracts/store/want/segments_line.jsonl (Rule W-2), and a
// coverage field that lived there could be published a moment apart from the bits it describes.
// One file, written atomically, is what "publish generation and coverage atomically with its
// index" has to mean.

// segmentFilterVersion is the on-disk version of a segment filter envelope. A file declaring
// anything else is unreadable to this build and is treated as no filter at all — never as an
// empty one, which would be a filter that says "definitely not present" about everything.
const segmentFilterVersion = 1

// segmentFilterPerm is the mode a filter file is written with: owner-only, like everything else
// under .qompack/.
const segmentFilterPerm = 0o600

// maxSegmentFilterKeys is the largest number of keys one segment filter will size itself for.
//
// It is a COST bound, not an accuracy one. Beyond it the filter is published incomplete rather
// than grown, because growing without bound is how a fixed-false-positive-rate promise turns into
// a memory leak, and because a segment with this many distinct keys is one a scan can afford
// relative to what a filter over it would cost to rebuild.
const maxSegmentFilterKeys = 65536

// segmentFilterFPRate is the false-positive rate a segment filter is sized for.
//
// It is not configurable and it is not a guarantee. sketch.NewBloom sizes m and k for this rate AT
// the declared capacity; the realised rate rises with the fill, which is exactly why Coverage
// carries the key count and why an over-capacity segment is published incomplete instead of being
// squeezed into the same bits.
const segmentFilterFPRate = 0.01

// SegmentFilterCoverage is what a filter file says about itself: which generation of a segment it
// was built from, how much of it the filter actually holds, and whether that is all of it.
//
// Complete is the field every negative depends on. It is false whenever the builder could not
// promise the filter saw every key — an over-capacity segment, an interrupted build — and Lookup
// then refuses to report a miss.
type SegmentFilterCoverage struct {
	// Version is segmentFilterVersion.
	Version int `json:"v"`
	// Segment is the segment this filter covers.
	Segment core.SegmentID `json:"segment"`
	// Generation is the segment's rebuild sequence: the value a reader compares against the
	// segment's current generation to decide whether this filter is still current. A filter whose
	// generation is behind is stale, and its negatives are bypassed.
	Generation int `json:"generation"`
	// Keys is how many keys were inserted.
	Keys int `json:"keys"`
	// Complete reports that every key in the segment was inserted. False means the filter is
	// partial and its negatives mean nothing.
	Complete bool `json:"complete"`
	// StartTurn and EndTurn are the segment's turn range as it stood when the filter was built,
	// so a reader can see at a glance what span a stale filter was describing.
	StartTurn core.TurnIndex `json:"start_turn"`
	EndTurn   core.TurnIndex `json:"end_turn"`
	// BuiltAt is when the build finished.
	BuiltAt core.UnixMilli `json:"built_at"`
	// Incomplete, when Complete is false, names why in the closed vocabulary
	// SegmentFilterIncomplete defines.
	Incomplete SegmentFilterIncomplete `json:"incomplete,omitempty"`
}

// SegmentFilterIncomplete names why a filter does not cover its whole segment. It is a closed set
// so a reader can act on the reason rather than only on the flag.
type SegmentFilterIncomplete string

const (
	// IncompleteNone means the filter is complete.
	IncompleteNone SegmentFilterIncomplete = ""
	// IncompleteOverCapacity means the segment had more distinct keys than maxSegmentFilterKeys.
	IncompleteOverCapacity SegmentFilterIncomplete = "over_capacity"
	// IncompleteSourceError means the key source failed part-way through the build.
	IncompleteSourceError SegmentFilterIncomplete = "source_error"
	// IncompleteCancelled means the build was cancelled before it finished.
	IncompleteCancelled SegmentFilterIncomplete = "cancelled"
)

// SegmentFilter is one segment's bloom filter together with the coverage that qualifies it.
type SegmentFilter struct {
	// Coverage qualifies every answer Lookup gives.
	Coverage SegmentFilterCoverage
	// bloom holds the bits. It is unexported so a caller cannot Test it directly and skip the
	// coverage rules Lookup enforces.
	bloom *sketch.Bloom
}

// FilterVerdict is Lookup's answer. There is deliberately no value meaning "present": a filter
// cannot establish presence, and offering a spelling of it would invite exactly the mistake §13
// invariant 3 forbids.
type FilterVerdict uint8

const (
	// FilterBypass means the filter cannot be trusted for this query — it is stale, incomplete,
	// or absent — and the caller must consult the exact index. It is the ZERO VALUE, so a verdict
	// nobody computed costs a scan rather than skipping one.
	FilterBypass FilterVerdict = iota
	// FilterMaybe means the filter matched. The caller must confirm against the exact index; a
	// bloom match is a candidate, never a result.
	FilterMaybe
	// FilterMiss means the filter is complete and current and does not hold the key, so the
	// segment can be skipped. It is the only verdict that saves work, and the only one with
	// preconditions.
	FilterMiss
)

// String returns the human-facing spelling of v.
func (v FilterVerdict) String() string {
	switch v {
	case FilterBypass:
		return "bypass"
	case FilterMaybe:
		return "maybe"
	case FilterMiss:
		return "miss"
	}
	return "bypass"
}

// SkipsSegment reports whether v permits the caller to skip this segment entirely. Only
// FilterMiss does.
func (v FilterVerdict) SkipsSegment() bool { return v == FilterMiss }

// BuildSegmentFilter builds a filter for seg over the keys yielded by next.
//
// next returns one key at a time and reports ok=false when it is done; an error ends the build
// with whatever was inserted so far, marked incomplete. That is deliberate: a filter built from
// half a segment is still useful — its positives still narrow the search — as long as nobody
// reads its negatives, and Complete=false is what stops them.
//
// The three ways a build ends short (over capacity, source error, cancellation) each set their own
// Incomplete reason, so a maintenance report can tell a segment that is too big from a disk that
// is failing.
func BuildSegmentFilter(ctx context.Context, seg Segment, generation int, now core.UnixMilli,
	next func() (key []byte, ok bool, err error),
) (*SegmentFilter, error) {
	if next == nil {
		return nil, errors.New("store: BuildSegmentFilter needs a key source")
	}
	cov := SegmentFilterCoverage{
		Version:    segmentFilterVersion,
		Segment:    seg.ID,
		Generation: generation,
		Complete:   true,
		StartTurn:  seg.StartTurn,
		EndTurn:    seg.EndTurn,
		BuiltAt:    now,
	}
	b := sketch.NewBloom(maxSegmentFilterKeys, segmentFilterFPRate)

	for {
		if err := ctx.Err(); err != nil {
			cov.Complete, cov.Incomplete = false, IncompleteCancelled
			break
		}
		key, ok, err := next()
		if err != nil {
			cov.Complete, cov.Incomplete = false, IncompleteSourceError
			break
		}
		if !ok {
			break
		}
		if cov.Keys >= maxSegmentFilterKeys {
			cov.Complete, cov.Incomplete = false, IncompleteOverCapacity
			break
		}
		b.Add(key)
		cov.Keys++
	}

	b.SetCreated(now)
	return &SegmentFilter{Coverage: cov, bloom: b}, nil
}

// Lookup reports what f establishes about key at the caller's current generation.
//
// currentGeneration is the generation the CALLER believes the segment is at. Passing a value ahead
// of f.Coverage.Generation is how a reader says "this segment has been rewritten since"; the
// filter then bypasses rather than answering from bits that describe an older shape.
//
// A nil filter bypasses. So does an incomplete one, and so does a stale one — but only for the
// MISS half: a positive from a stale or partial filter is still only a candidate, and reporting it
// as FilterMaybe costs the same exact confirmation a bypass would have, with no risk either way.
// Collapsing both halves into bypass would be safe too, and slower for no gain.
func (f *SegmentFilter) Lookup(key []byte, currentGeneration int) FilterVerdict {
	if f == nil || f.bloom == nil {
		return FilterBypass
	}
	hit := f.bloom.Test(key)
	if hit {
		return FilterMaybe
	}
	// From here the filter says "not present", which is only actionable with complete, current
	// coverage.
	if !f.Coverage.Complete || f.Coverage.Generation != currentGeneration {
		return FilterBypass
	}
	return FilterMiss
}

// Stats exposes the underlying bloom's saturation for a maintenance report. It returns the zero
// BloomStats for a nil or empty filter rather than panicking, because a report must be able to
// describe a filter that failed to load.
func (f *SegmentFilter) Stats() sketch.BloomStats {
	if f == nil || f.bloom == nil {
		return sketch.BloomStats{}
	}
	return f.bloom.Stats()
}

// MarshalBinary encodes f as one JSON coverage line, a newline, then the bloom's own binary
// encoding.
//
// The header is JSON and comes first so that a reader — or an operator with `head -1` — can learn
// what a filter covers without decoding its bits, and so that a coverage record can never be
// separated from the bits it describes. The bloom's encoding is opaque bytes after the first
// newline, which is why the header is written compactly on exactly one line.
func (f *SegmentFilter) MarshalBinary() ([]byte, error) {
	if f == nil || f.bloom == nil {
		return nil, errors.New("store: marshalling an empty segment filter")
	}
	head, err := json.Marshal(f.Coverage)
	if err != nil {
		return nil, fmt.Errorf("store: marshalling segment filter coverage: %w", err)
	}
	body, err := f.bloom.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("store: marshalling segment filter bits: %w", err)
	}
	out := make([]byte, 0, len(head)+1+len(body))
	out = append(out, head...)
	out = append(out, '\n')
	return append(out, body...), nil
}

// UnmarshalBinary decodes what MarshalBinary wrote.
//
// A file this build cannot read — a bad version, a truncated body, a header that will not parse —
// is an ERROR, and the caller treats an error as "no filter". It is never decoded into an empty
// filter: an empty bloom answers "definitely not present" to everything, which would turn an
// unreadable file into a silent, total false negative.
func (f *SegmentFilter) UnmarshalBinary(data []byte) error {
	nl := bytes.IndexByte(data, '\n')
	if nl < 0 {
		return fmt.Errorf("%w: segment filter has no coverage header", sketch.ErrMalformed)
	}
	var cov SegmentFilterCoverage
	if err := json.Unmarshal(data[:nl], &cov); err != nil {
		return fmt.Errorf("%w: segment filter coverage: %v", sketch.ErrMalformed, err)
	}
	if cov.Version != segmentFilterVersion {
		return fmt.Errorf("%w: segment filter version %d, want %d",
			sketch.ErrMalformed, cov.Version, segmentFilterVersion)
	}
	b := &sketch.Bloom{}
	if err := b.UnmarshalBinary(data[nl+1:]); err != nil {
		return fmt.Errorf("%w: segment filter bits: %v", sketch.ErrMalformed, err)
	}
	f.Coverage, f.bloom = cov, b
	return nil
}

// SegmentFilterRef returns the store-relative path of a segment's filter file, which is what goes
// into Segment.BloomRef.
//
// It is store-relative rather than absolute because BloomRef is written into an append-only log
// that outlives any particular checkout: an absolute path recorded on one machine is meaningless
// on the next, and a store that is copied elsewhere must still resolve its own filters.
func SegmentFilterRef(id core.SegmentID) string {
	return fmt.Sprintf("sketches/seg-%04d.bloom", int(id))
}

// SegmentFilterPath resolves SegmentFilterRef against a project root.
func SegmentFilterPath(root string, id core.SegmentID) string {
	return filepath.Join(paths.Of(root).Dot, filepath.FromSlash(SegmentFilterRef(id)))
}

// WriteSegmentFilter writes f to the path for its segment under root, atomically.
//
// It writes the FILE only. Appending the segment log's bloom record is a separate, later step
// (segLog.PublishFilter), and the order matters: a log record naming a file that does not exist
// yet is a dangling reference every reader has to defend against, while a file no record names is
// simply an unused file the next publication overwrites.
func WriteSegmentFilter(root string, f *SegmentFilter) (string, error) {
	if f == nil {
		return "", errors.New("store: writing a nil segment filter")
	}
	b, err := f.MarshalBinary()
	if err != nil {
		return "", err
	}
	p := SegmentFilterPath(root, f.Coverage.Segment)
	if err := paths.WriteAtomic(p, b, segmentFilterPerm); err != nil {
		return "", fmt.Errorf("store: writing segment filter %s: %w", p, err)
	}
	return SegmentFilterRef(f.Coverage.Segment), nil
}

// ReadSegmentFilter reads the filter for id under root.
//
// A missing file reports core.ErrNotFound and a corrupt one reports sketch.ErrMalformed; both mean
// the same thing to a caller — there is no usable filter, so scan — and both are distinguished so
// a maintenance report can tell "never built" from "built and broken".
func ReadSegmentFilter(root string, id core.SegmentID) (*SegmentFilter, error) {
	p := SegmentFilterPath(root, id)
	b, err := paths.ReadFileShared(p)
	if err != nil {
		return nil, fmt.Errorf("%w: segment filter %s: %v", core.ErrNotFound, p, err)
	}
	f := &SegmentFilter{}
	if err := f.UnmarshalBinary(b); err != nil {
		return nil, err
	}
	if f.Coverage.Segment != id {
		return nil, fmt.Errorf("%w: segment filter %s covers segment %d, not %d",
			sketch.ErrMalformed, p, int(f.Coverage.Segment), int(id))
	}
	return f, nil
}

// SegmentFilterPublisher is the segment-log surface beyond §5.8's SegmentLog: the per-segment
// filter publication SP-16 owns.
//
// It is a separate interface reached by type assertion — `w, ok := st.Segments().(store.
// SegmentFilterPublisher)` — rather than a new SegmentLog method, for the same reason
// negknow.Maintainer is separate from negknow.Ledger: SegmentLog is a §5.8 contract several
// packages already satisfy and assert against, and widening it would make every one of them
// implement a capability only this subplan needs.
type SegmentFilterPublisher interface {
	// PublishFilter records that id's filter now lives at ref, which must already be on disk.
	PublishFilter(ctx context.Context, id core.SegmentID, ref string) error
}

// PublishFilter appends the segment log's bloom record for id.
//
// The file must already have been written by WriteSegmentFilter; this call is the second half of
// the publication and is what makes the pair atomic FROM A READER'S POINT OF VIEW. A reader learns
// about a filter only through BloomRef, so until this record lands there is no filter as far as
// the system is concerned, and once it lands the bits are already there to be read.
func (l *segLog) PublishFilter(ctx context.Context, id core.SegmentID, ref string) error {
	if ref == "" {
		return fmt.Errorf("store: publishing an empty filter reference for segment %d", int(id))
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.degraded {
		return fmt.Errorf("%w: store: the segment log is closed", core.ErrDegraded)
	}
	seg, ok := l.byID[id]
	if !ok {
		return fmt.Errorf("%w: store: no segment %d", core.ErrNotFound, int(id))
	}
	if err := l.append(segBloomRec{V: indexRecordVersion, Op: segOpBloom, ID: id, Ref: ref}); err != nil {
		return fmt.Errorf("store: appending the bloom record for segment %d: %w", int(id), err)
	}
	seg.BloomRef = ref
	return nil
}
