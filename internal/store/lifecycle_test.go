package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// ── doubles ──────────────────────────────────────────────────────────────────────────────────

// canonLossyDeltas strips the same lines canonStripTimestamp does but records each Delta WITHOUT
// its line terminator, so canon.Restore reconstructs something that is one byte per delta shorter
// than the input. It is the "declared base does not round-trip exactly" case: SP-20 invariant 6
// forbids persisting a delta-only representation from it.
func canonLossyDeltas() canon.Registry {
	return canonFunc(func(_, _ string, in []byte, o canon.Options) (canon.Result, error) {
		var out bytes.Buffer
		var deltas []canon.Delta
		for _, line := range strings.SplitAfter(string(in), "\n") {
			if strings.HasPrefix(line, timestampPrefix) {
				if o.KeepDeltas {
					deltas = append(deltas, canon.Delta{
						Offset:   out.Len(),
						Original: strings.TrimRight(line, "\n"), // deliberately lossy
						Class:    canon.ClassTimestamps,
					})
				}
				continue
			}
			out.WriteString(line)
		}
		return canon.Result{Canonical: out.Bytes(), Deltas: deltas}, nil
	})
}

// fixedRetentionRoots is a RetentionRootSource double: it reports exactly the roots it was built
// with, standing in for the daemon's delivery-lease journal or a rollback manifest.
type fixedRetentionRoots struct {
	roots []RetentionRoot
	err   error
}

func (f fixedRetentionRoots) RetentionRoots(context.Context) ([]RetentionRoot, error) {
	return f.roots, f.err
}

// withRetentionRoots injects one RetentionRootSource.
func withRetentionRoots(src RetentionRootSource) storeOpt {
	return func(_ *config.Config, d *Deps) { d.RetentionRoots = append(d.RetentionRoots, src) }
}

// ── T20-M1-06: exact delta bases ─────────────────────────────────────────────────────────────

// TestPutBytes_DeltaRecordDeclaresItsBase is invariant 6's first half: the delta record must
// carry its own declared base, not merely be pointed AT by one.
func TestPutBytes_DeltaRecordDeclaresItsBase(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	input := []byte(timestampPrefix + "09:11:04\nfirst body\n" + timestampPrefix + "09:11:05\nsecond\n")

	res, err := tp.Store.PutBytes(context.Background(), input, PutOptions{Path: "src/log.txt", KeepRaw: true})
	require.NoError(t, err)

	lines := tp.indexLines(t, rootsFile)
	require.Len(t, lines, 2, "KeepRaw must still append a delta root line")

	var delta map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &delta))
	require.Equal(t, deltaToolName, delta["tool"])
	require.Equal(t, res.Root.Hash.String(), delta["base"],
		"the delta record must declare the base it reconstructs")
	require.EqualValues(t, indexRecordVersionBase, delta["v"],
		"a record carrying a declared base announces the newer record version")

	tp.Store.mu.RLock()
	deltaEntry := tp.Store.rootIndex[mustHash(t, delta["root"])]
	tp.Store.mu.RUnlock()
	require.NotNil(t, deltaEntry)
	require.Equal(t, res.Root.Hash, deltaEntry.Base, "the base must survive into the in-memory index")
	require.Equal(t, FidelityExact, res.Fidelity, "a proven round-trip is exact fidelity")
}

// TestPutBytes_UnprovenDeltaFallsBackToAFullObject is invariant 6's second half: no base means no
// delta-only recovery claim. A delta list that does not reproduce the input exactly must not be
// persisted as the recovery record; the redacted original is retained as a full object instead.
func TestPutBytes_UnprovenDeltaFallsBackToAFullObject(t *testing.T) {
	tp := newTestStore(t, withCanon(canonLossyDeltas()))
	ctx := context.Background()
	input := []byte(timestampPrefix + "09:11:04\nfirst body\n")

	res, err := tp.Store.PutBytes(ctx, input, PutOptions{Path: "src/log.txt", KeepRaw: true})
	require.NoError(t, err)
	require.Equal(t, FidelityFull, res.Fidelity,
		"an unproven round-trip must degrade to a retained full object, never to a delta")

	for _, l := range tp.indexLines(t, rootsFile) {
		require.NotContains(t, l, `"tool":"`+deltaToolName+`"`,
			"no delta record may be persisted when its round-trip is unproven")
	}

	tp.Store.mu.RLock()
	entry := tp.Store.rootIndex[res.Root.Hash]
	tp.Store.mu.RUnlock()
	require.NotNil(t, entry)
	require.True(t, entry.Deltas.IsZero(), "the content record must not point at a delta")
	require.False(t, entry.Orig.IsZero(), "the content record must point at its retained full object")

	got, fid, err := tp.Store.RestoreOriginal(ctx, res.Root.Hash)
	require.NoError(t, err)
	require.Equal(t, FidelityFull, fid)
	require.Equal(t, input, got, "the retained full object must reproduce the input exactly")
}

// TestRestoreOriginal_ExactThroughTheDeltaRecord asserts the read side of a proven delta.
func TestRestoreOriginal_ExactThroughTheDeltaRecord(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()
	input := []byte(timestampPrefix + "09:11:04\nfirst body\nsecond body\n")

	res, err := tp.Store.PutBytes(ctx, input, PutOptions{Path: "src/log.txt", KeepRaw: true})
	require.NoError(t, err)

	got, fid, err := tp.Store.RestoreOriginal(ctx, res.Root.Hash)
	require.NoError(t, err)
	require.Equal(t, FidelityExact, fid)
	require.Equal(t, input, got)
}

// TestRestoreOriginal_NoRecordIsCanonicalOnly asserts a root stored without KeepRaw reports
// canonical fidelity rather than claiming its canonical bytes are the original.
func TestRestoreOriginal_NoRecordIsCanonicalOnly(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()

	res, err := tp.Store.PutBytes(ctx, []byte(timestampPrefix+"09:11:04\nbody\n"), PutOptions{Path: "src/log.txt"})
	require.NoError(t, err)

	_, fid, err := tp.Store.RestoreOriginal(ctx, res.Root.Hash)
	require.NoError(t, err)
	require.Equal(t, FidelityCanonical, fid, "canonical bytes are never an exactness claim")
}

// TestReadDelta_AbsentBaseIsUnavailable asserts reading a delta whose declared base is gone
// produces an explicit unavailable outcome that still carries the declared base — never a partial
// or guessed reconstruction.
func TestReadDelta_AbsentBaseIsUnavailable(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()
	input := []byte(timestampPrefix + "09:11:04\nfirst body\n")

	res, err := tp.Store.PutBytes(ctx, input, PutOptions{Path: "src/log.txt", KeepRaw: true})
	require.NoError(t, err)

	tp.Store.mu.RLock()
	deltaRoot := tp.Store.rootIndex[res.Root.Hash].Deltas
	tp.Store.mu.RUnlock()
	require.False(t, deltaRoot.IsZero())

	require.NoError(t, tp.Store.appendGCTombstone(res.Root.Hash)) // the base leaves service

	rec, fid, err := tp.Store.ReadDelta(ctx, deltaRoot)
	require.Error(t, err)
	require.Equal(t, FidelityUnavailable, fid)
	require.Equal(t, res.Root.Hash, rec.Base, "the declared base is reported even when it is gone")

	_, fid, err = tp.Store.RestoreOriginal(ctx, res.Root.Hash)
	require.Error(t, err)
	require.Equal(t, FidelityUnavailable, fid)
}

// TestReadDelta_CorruptRecordIsCorrupt asserts an unparseable delta payload is reported as corrupt
// rather than silently reconstructing something.
func TestReadDelta_CorruptRecordIsCorrupt(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()

	res, err := tp.Store.PutBytes(ctx, []byte(timestampPrefix+"09:11:04\nbody\n"),
		PutOptions{Path: "src/log.txt", KeepRaw: true})
	require.NoError(t, err)

	tp.Store.mu.RLock()
	entry := tp.Store.rootIndex[res.Root.Hash]
	deltaRoot := entry.Deltas
	deltaEntry := tp.Store.rootIndex[deltaRoot]
	chunks := append([]ChunkRef(nil), deltaEntry.Root.Chunks...)
	tp.Store.mu.RUnlock()

	for _, c := range chunks {
		for _, p := range tp.Store.objectCandidates(c.Hash) {
			_ = os.Remove(paths.Long(p))
		}
	}

	_, fid, err := tp.Store.ReadDelta(ctx, deltaRoot)
	require.Error(t, err)
	require.Contains(t, []Fidelity{FidelityCorrupt, FidelityUnavailable}, fid)
}

// ── T20-M1-07: retention roots ───────────────────────────────────────────────────────────────

// TestGC_RetainsADeltaWithItsBase asserts a delta root is never collectable independently of its
// base: pinning either one retains both.
func TestGC_RetainsADeltaWithItsBase(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()

	res, err := tp.Store.PutBytes(ctx, []byte(timestampPrefix+"09:11:04\nkept body\n"),
		PutOptions{Path: "src/log.txt", KeepRaw: true})
	require.NoError(t, err)

	tp.Store.mu.RLock()
	deltaRoot := tp.Store.rootIndex[res.Root.Hash].Deltas
	tp.Store.mu.RUnlock()
	require.False(t, deltaRoot.IsZero())

	// Only the BASE is referenced; the delta record must ride along.
	writeCheckpointJSON(t, tp, "0001.json", res.Root.Hash.String())
	_, err = tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)

	_, err = tp.Store.GetRoot(ctx, res.Root.Hash)
	require.NoError(t, err, "the referenced base must survive")
	_, err = tp.Store.GetRoot(ctx, deltaRoot)
	require.NoError(t, err, "a base's delta record must be retained with it")
}

// TestGC_RetainsADeltaBaseFromTheDeltaSide is the same coupling in the other direction: a
// reference that names only the delta record must keep the base it declares.
func TestGC_RetainsADeltaBaseFromTheDeltaSide(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()

	res, err := tp.Store.PutBytes(ctx, []byte(timestampPrefix+"09:11:04\nkept body\n"),
		PutOptions{Path: "src/log.txt", KeepRaw: true})
	require.NoError(t, err)

	tp.Store.mu.RLock()
	deltaRoot := tp.Store.rootIndex[res.Root.Hash].Deltas
	tp.Store.mu.RUnlock()

	writeCheckpointJSON(t, tp, "0001.json", deltaRoot.String())
	_, err = tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)

	_, err = tp.Store.GetRoot(ctx, res.Root.Hash)
	require.NoError(t, err, "a retained delta must retain its declared base")
}

// TestGC_RetainsPendingWritesAcrossACrashedRootAppend is the durable pending-write registry: an
// object written by putObject whose root append never landed must survive the next pass, even when
// its file is older than the pass.
func TestGC_RetainsPendingWritesAcrossACrashedRootAppend(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	gcSeed(t, tp, "src/other.txt", "an ordinary collectable root\n")

	plain := []byte("content whose root append never landed\n")
	h := core.HashBytes(core.DomainChunk, plain)
	pw, err := tp.Store.beginPendingWrite(h, []ChunkRef{{Hash: h, Len: len(plain)}})
	require.NoError(t, err)
	require.NotNil(t, pw)
	_, _, err = tp.Store.putObject(h, plain)
	require.NoError(t, err)
	// The crash: pw.done() is never called.

	aged := tp.Clock.Now().Add(-time.Hour)
	for _, p := range gcObjectPaths(t, tp) {
		require.NoError(t, os.Chtimes(p, aged, aged))
	}

	_, err = tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, tp.Store.Has(h), "a registered pending write must survive a full collection")
}

// TestGC_ExpiresAbandonedPendingMarkers asserts a pending marker that never completed is expired
// as a visible outcome rather than retained forever or dropped silently.
func TestGC_ExpiresAbandonedPendingMarkers(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	plain := []byte("abandoned pending write\n")
	h := core.HashBytes(core.DomainChunk, plain)
	pw, err := tp.Store.beginPendingWrite(h, []ChunkRef{{Hash: h, Len: len(plain)}})
	require.NoError(t, err)
	aged := tp.Clock.Now().Add(-90 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(paths.Long(pw.path), aged, aged))

	rep, err := tp.Store.GC(ctx, GCPolicy{RetainDays: 30, RetainSessions: -1})
	require.NoError(t, err)
	require.Equal(t, 1, rep.PendingExpired, "an abandoned marker past the window expires")
	require.NotEmpty(t, outcomesWithResult(rep, RootExpired))
	_, statErr := os.Stat(paths.Long(pw.path))
	require.True(t, os.IsNotExist(statErr), "an expired marker is removed")
}

// TestGC_ReadsTheDeliveryLeaseJournalAsARetentionRoot asserts the on-disk root-file convention:
// the daemon's open-lease journal and the generic retention-roots file both hold content live.
func TestGC_ReadsTheDeliveryLeaseJournalAsARetentionRoot(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)

	leased := gcSeed(t, tp, "src/leased.txt", "held by an open delivery lease\n")
	declared := gcSeed(t, tp, "src/declared.txt", "held by a declared retention root\n")
	doomed := gcSeed(t, tp, "src/doomed.txt", "held by nothing at all\n")

	writeJSONLWithHash(t, l.State, deliveryLeaseFile, "request", leased.Hash.String())
	require.NoError(t, AppendRetentionRoot(tp.Root, RetentionRoot{
		Hash: declared.Hash, Class: RetentionRollback, Reason: "pre-cutover rollback material",
	}))

	_, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)

	_, err = tp.Store.GetRoot(ctx, leased.Hash)
	require.NoError(t, err, "an open delivery lease must retain what it names")
	_, err = tp.Store.GetRoot(ctx, declared.Hash)
	require.NoError(t, err, "a declared retention root must retain what it names")
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestGC_RetentionRootSourceHoldsObjectsLive asserts the in-process seam: a producer that cannot
// be imported by this package supplies its roots through Deps.RetentionRoots.
func TestGC_RetentionRootSourceHoldsObjectsLive(t *testing.T) {
	p := newProject(t)
	tp := openOver(t, p)
	ctx := context.Background()
	kept := gcSeed(t, tp, "src/kept.txt", "held by an injected lease source\n")
	require.NoError(t, tp.Store.Close())

	src := fixedRetentionRoots{roots: []RetentionRoot{{
		Hash: kept.Hash, Class: RetentionLease, Reason: "delivery lease 7 is open",
	}}}
	reopened := openOver(t, p, withRetentionRoots(src))
	gcSeed(t, reopened, "src/gone.txt", "held by nothing\n")

	rep, err := reopened.Store.GC(ctx, forceCollect)
	require.NoError(t, err)

	_, err = reopened.Store.GetRoot(ctx, kept.Hash)
	require.NoError(t, err, "an injected retention root must keep its content alive")
	require.NotEmpty(t, outcomesWithResult(rep, RootRetained))
}

// ── quota and explicit expiry outcomes ───────────────────────────────────────────────────────

// TestGC_ReportsPerRootOutcomes asserts every root a pass decided about is reported with an
// explicit result and a reason, never as an aggregate count alone.
func TestGC_ReportsPerRootOutcomes(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	kept := gcSeed(t, tp, "src/kept.txt", "referenced by a checkpoint\n")
	gone := gcSeed(t, tp, "src/gone.txt", "referenced by nothing\n")
	writeCheckpointJSON(t, tp, "0001.json", kept.Hash.String())

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.Equal(t, 1, rep.Retained)
	require.Equal(t, 1, rep.Expired)

	byRoot := map[core.Hash]RootOutcome{}
	for _, o := range rep.Outcomes {
		byRoot[o.Root] = o
	}
	require.Equal(t, RootRetained, byRoot[kept.Hash].Result)
	require.NotEmpty(t, byRoot[kept.Hash].Reason, "a retained root must say why")
	require.Equal(t, RootExpired, byRoot[gone.Hash].Result)
	require.NotEmpty(t, byRoot[gone.Hash].Reason, "an expired root must say why")
}

// TestGC_QuotaEvictsInWindowRootsAndReportsThem asserts a store over its quota sheds in-window
// content as an explicit quota_evicted outcome rather than growing without bound or dropping
// silently.
func TestGC_QuotaEvictsInWindowRootsAndReportsThem(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/f%02d.txt", i), fmt.Sprintf("quota body %d, unique content\n", i))
		tp.Clock.Advance(time.Minute)
	}

	tp.Store.mu.RLock()
	onDisk := tp.Store.bytesOnDisk
	tp.Store.mu.RUnlock()
	require.Positive(t, onDisk)

	rep, err := tp.Store.GC(ctx, GCPolicy{QuotaBytes: onDisk / 2})
	require.NoError(t, err)
	require.Positive(t, rep.QuotaEvicted, "a store over quota must evict")
	require.Equal(t, onDisk, rep.QuotaBytesBefore)
	evicted := outcomesWithResult(rep, RootQuotaEvicted)
	require.NotEmpty(t, evicted)
	require.NotEmpty(t, evicted[0].Reason)
	require.Positive(t, rep.DeletedObjects, "quota evictions actually free objects")
}

// TestGC_QuotaNeverEvictsAHardRetentionRoot asserts the quota cannot override a lease, checkpoint,
// pin, evidence, delta-base or rollback root: the shortfall is reported instead.
func TestGC_QuotaNeverEvictsAHardRetentionRoot(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	var refs []string
	for i := 0; i < 4; i++ {
		r := gcSeed(t, tp, fmt.Sprintf("src/f%02d.txt", i), fmt.Sprintf("pinned body %d, unique\n", i))
		refs = append(refs, r.Hash.String())
	}
	writeCheckpointJSON(t, tp, "0001.json", refs...)

	rep, err := tp.Store.GC(ctx, GCPolicy{QuotaBytes: 1})
	require.NoError(t, err)
	require.Zero(t, rep.QuotaEvicted, "a checkpoint-referenced root is not quota-evictable")
	require.True(t, rep.QuotaExceeded, "a quota that cannot be met must be reported, never ignored")
	require.NotEmpty(t, outcomesWithResult(rep, RootUnsafeToCollect))
	require.Zero(t, rep.DeletedObjects)
}

// TestGC_OutcomesAreBoundedAndSayWhenTheyWereTruncated asserts the per-root outcome list cannot
// grow without bound, and that a truncated list says so rather than looking complete.
func TestGC_OutcomesAreBoundedAndSayWhenTheyWereTruncated(t *testing.T) {
	tp := newTestStore(t)
	for i := 0; i < 6; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/f%02d.txt", i), fmt.Sprintf("bounded body %d\n", i))
	}

	rep, err := tp.Store.GC(context.Background(), GCPolicy{RetainDays: -1, RetainSessions: -1, MaxOutcomes: 2})
	require.NoError(t, err)
	require.Len(t, rep.Outcomes, 2)
	require.True(t, rep.OutcomesTruncated)
	require.Equal(t, 6, rep.Expired, "the counters stay exact even when the list is capped")
}

// ── compatibility ────────────────────────────────────────────────────────────────────────────

// TestLoadRoots_ReadsBothRecordVersions asserts a roots.jsonl written before the declared-base
// field still loads in full, alongside a record carrying one.
func TestLoadRoots_ReadsBothRecordVersions(t *testing.T) {
	p := newProject(t)
	l := paths.Of(p.Root)
	base := core.HashBytes(core.DomainChunk, []byte("legacy base"))
	chunkHash := core.HashBytes(core.DomainChunk, []byte("legacy chunk"))
	delta := core.HashBytes(core.DomainChunk, []byte("legacy delta"))

	v1 := fmt.Sprintf(`{"v":1,"root":%q,"ts":1718269864000,"tool":"Bash","path":"src/a.ts",`+
		`"raw":11,"canon":11,"tokens":3,"class":0,"chunks":[{"h":%q,"n":11}]}`,
		base.String(), chunkHash.String())
	v2 := fmt.Sprintf(`{"v":%d,"root":%q,"ts":1718269864000,"tool":%q,"path":"",`+
		`"raw":9,"canon":9,"tokens":2,"class":2,"chunks":[],"base":%q}`,
		indexRecordVersionBase, delta.String(), deltaToolName, base.String())
	require.NoError(t, os.MkdirAll(paths.Long(l.Index), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(l.Index, rootsFile)),
		[]byte(v1+"\n"+v2+"\n"), 0o600))

	tp := openOver(t, p)
	tp.Store.mu.RLock()
	defer tp.Store.mu.RUnlock()
	legacy := tp.Store.rootIndex[base]
	require.NotNil(t, legacy, "a version-1 record must still load")
	require.Equal(t, "src/a.ts", legacy.Path)
	require.True(t, legacy.Base.IsZero(), "a version-1 record declares no base")

	withBase := tp.Store.rootIndex[delta]
	require.NotNil(t, withBase, "a version-2 record must load")
	require.Equal(t, base, withBase.Base)
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────

// outcomesWithResult filters a report's per-root outcomes by result.
func outcomesWithResult(rep GCReport, want RootResult) []RootOutcome {
	var out []RootOutcome
	for _, o := range rep.Outcomes {
		if o.Result == want {
			out = append(out, o)
		}
	}
	return out
}

// mustHash parses a hash out of a decoded JSON field.
func mustHash(t *testing.T, v any) core.Hash {
	t.Helper()
	s, ok := v.(string)
	require.True(t, ok, "expected a hash string, got %T", v)
	h, err := core.ParseHash(s)
	require.NoError(t, err)
	return h
}

// ── acknowledged delivery leases stop retaining (V4 fix O-1) ─────────────────────────────────

// writeJSONLLines plants a multi-record JSONL document, one record per line.
func writeJSONLLines(t *testing.T, dir, name string, records ...map[string]any) {
	t.Helper()
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	var buf bytes.Buffer
	for _, r := range records {
		b, err := json.Marshal(r)
		require.NoError(t, err)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, name)), buf.Bytes(), 0o600))
}

// deliveryNonce builds a 64-hex delivery token shaped like the daemon's, from a single filler
// character, so a test's nonces are distinct and none of them collides with a real root hash.
func deliveryNonce(fill string) string { return strings.Repeat(fill, 64) }

// TestGC_AcknowledgedDeliveryLeaseStopsRetaining is both directions of fix O-1 in one pass. The
// lease journal is append-only and never records a release, so a delivery that was fully published
// and acknowledged used to pin its references forever — nothing a daemon had ever handled could be
// collected. An OPEN lease must still retain; an ACKNOWLEDGED one must retain only what something
// else roots, and its published evidence is rooted by the evidence-class declaration the capture
// sidecar makes for it.
func TestGC_AcknowledgedDeliveryLeaseStopsRetaining(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)

	open := gcSeed(t, tp, "src/open.txt", "held by an OPEN delivery lease\n")
	done := gcSeed(t, tp, "src/done.txt", "held only by an ACKNOWLEDGED lease\n")
	evidence := gcSeed(t, tp, "src/evidence.txt", "acknowledged, and published as evidence\n")

	openNonce, doneNonce, evidenceNonce := deliveryNonce("1"), deliveryNonce("2"), deliveryNonce("3")
	writeJSONLLines(t, l.State, deliveryLeaseFile,
		map[string]any{"v": 1, "delivery": openNonce, "request": open.Hash.String()},
		map[string]any{"v": 1, "delivery": doneNonce, "request": done.Hash.String()},
		map[string]any{"v": 1, "delivery": evidenceNonce, "request": evidence.Hash.String()},
	)
	writeJSONLLines(t, l.State, deliveryAckFile,
		map[string]any{"v": 1, "delivery": doneNonce, "observation_id": "sha256:" + deliveryNonce("a")},
		map[string]any{"v": 1, "delivery": evidenceNonce, "observation_id": "sha256:" + deliveryNonce("b")},
	)
	require.NoError(t, AppendRetentionRoot(tp.Root, RetentionRoot{
		Hash: evidence.Hash, Class: RetentionEvidence, Reason: "capture sidecar sha256:beef",
	}))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)

	_, err = tp.Store.GetRoot(ctx, open.Hash)
	require.NoError(t, err, "an OPEN delivery lease must still retain what it names")
	_, err = tp.Store.GetRoot(ctx, evidence.Hash)
	require.NoError(t, err, "an acknowledged lease's published evidence keeps its own evidence root")
	_, err = tp.Store.GetRoot(ctx, done.Hash)
	require.ErrorIs(t, err, core.ErrNotFound,
		"an acknowledged lease nothing else roots must become collectable")

	byRoot := map[core.Hash]RootOutcome{}
	for _, o := range rep.Outcomes {
		byRoot[o.Root] = o
	}
	require.Equal(t, RetentionLease, byRoot[open.Hash].Class, "an open lease is reported as a lease")
	require.Equal(t, RetentionEvidence, byRoot[evidence.Hash].Class,
		"a declared retention root is reported under the class its producer wrote")
}

// TestGC_UnreadableAcknowledgementKeepsItsLeaseOpen pins the safe direction. A frontier record that
// GC cannot read is not an acknowledgement: the delivery it was about stays open and stays
// retained, because a torn ack line and a delivery still in flight look the same from here and
// only one of them is safe to collect through.
func TestGC_UnreadableAcknowledgementKeepsItsLeaseOpen(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)

	leased := gcSeed(t, tp, "src/leased.txt", "named by a lease whose ack is torn\n")
	nonce := deliveryNonce("7")
	writeJSONLLines(t, l.State, deliveryLeaseFile,
		map[string]any{"v": 1, "delivery": nonce, "request": leased.Hash.String()})
	// A crash mid-append leaves exactly this: a prefix of one acknowledgement, no terminator.
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(l.State, deliveryAckFile)),
		[]byte(`{"v":1,"delivery":"`+nonce+`","observation_i`), 0o600))

	_, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	_, err = tp.Store.GetRoot(ctx, leased.Hash)
	require.NoError(t, err, "a torn acknowledgement must not close its lease")
}
