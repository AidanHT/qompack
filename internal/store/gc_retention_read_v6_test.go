package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Retention-read safety (main's durable-data adjudication). GC must never read UNREADABLE or
// INCOMPLETE retention evidence as a proven absence and then sweep against it. These tests build a
// real store and a real GC pass, plant a root that ONLY a damaged retention file names, and prove the
// pass collects nothing and says so (RetentionRootsError) rather than deleting the root. Run as a
// negative control against the unfixed reader first: each one deleted the root it now protects.

// obsIDText builds a valid, canonical observation-id string (sha256:<64hex>) distinct per label, so a
// lease/ack pair can carry an exact observation identity the way the daemon's producer does.
func obsIDText(label string) string {
	return core.HashBytes(core.DomainChunk, []byte("obs:"+label)).String()
}

// TestGCRetention_UnreadableLeaseRootHaltsCollection: a retention-root path that exists but is not a
// readable regular file (here a directory planted where delivery-leases.jsonl belongs) is an
// unreadable retention source, not an empty one. The pass must halt with RetentionRootsError and
// sweep nothing — otherwise a doomed root held by no other reference is collected while the collector
// is blind to what the damaged source named.
func TestGCRetention_UnreadableLeaseRootHaltsCollection(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)

	doomed := gcSeed(t, tp, "src/doomed.txt", "held by nothing, collectable only if the pass runs\n")
	// A directory where the lease journal must be: unreadable as a token stream, but present.
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(l.State, deliveryLeaseFile)), 0o700))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err, "an unreadable retention root is a nothing-collected pass, not a hard error")
	require.True(t, rep.RetentionRootsError, "an unreadable retention source must be reported, never read as empty")
	require.Zero(t, rep.DeletedObjects, "a pass that could not read a retention source must collect nothing")
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.NoError(t, err, "nothing may be swept while a retention source is unreadable")
}

// TestGCRetention_TornCheckpointHidesLaterReferenceHaltsCollection: the "hash beyond first valid
// prefix" case. A checkpoint document names an EARLY hash, then breaks with malformed JSON, then names
// a LATE hash the token walk never reaches. The unfixed reader harvested EARLY, swallowed the parse
// error, and swept LATE. A torn known-retention document must halt the pass so BOTH survive.
func TestGCRetention_TornCheckpointHidesLaterReferenceHaltsCollection(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)

	early := gcSeed(t, tp, "src/early.txt", "named before the corruption\n")
	late := gcSeed(t, tp, "src/late.txt", "named AFTER the corruption, dropped by a lenient reader\n")

	// Valid up to and including EARLY, then a stray token where a separator must be; LATE appears in
	// the bytes but past the break, so a walk that stops at the error never sees it.
	torn := `{"pointers":["` + early.Hash.String() + `"] xx "late":"` + late.Hash.String() + `"}`
	require.NoError(t, os.MkdirAll(paths.Long(l.Checkpoints), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(l.Checkpoints, "0001.json")), []byte(torn), 0o600))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.RetentionRootsError, "a torn retention document must be reported, not silently truncated")
	require.Zero(t, rep.DeletedObjects, "a torn retention document collects nothing this pass")
	_, err = tp.Store.GetRoot(ctx, early.Hash)
	require.NoError(t, err, "the reference before the corruption survives")
	_, err = tp.Store.GetRoot(ctx, late.Hash)
	require.NoError(t, err, "the reference PAST the corruption must not be swept as if it were absent")
}

// TestGCRetention_OverlongLeaseLineHidesLaterLiveReferenceHaltsCollection: a first lease line past the
// scanner's line bound stops the scan where it stands; a later line naming a live root is never read.
// The unfixed reader ignored bufio's error and harvested nothing, sweeping the later root. The scan
// error must halt the pass.
func TestGCRetention_OverlongLeaseLineHidesLaterLiveReferenceHaltsCollection(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)

	late := gcSeed(t, tp, "src/late.txt", "named by a lease line after an overlong one\n")
	nonce := deliveryNonce("9")
	overlong := `{"v":1,"delivery":"` + deliveryNonce("8") + `","pad":"` + strings.Repeat("x", scannerMaxBuf+1) + `"}`
	second := `{"v":1,"delivery":"` + nonce + `","request":"` + late.Hash.String() + `"}`
	require.NoError(t, os.MkdirAll(paths.Long(l.State), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(l.State, deliveryLeaseFile)),
		[]byte(overlong+"\n"+second+"\n"), 0o600))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.RetentionRootsError, "an overlong line that stops the scan must be reported")
	require.Zero(t, rep.DeletedObjects)
	_, err = tp.Store.GetRoot(ctx, late.Hash)
	require.NoError(t, err, "a live reference hidden behind an overlong line must not be swept")
}

// TestGCRetention_AckReleaseRequiresExactObservationAndVersion: an acknowledgement releases a lease
// only when it joins the lease on the exact observation identity AND a known version, not on the
// delivery nonce alone. A matching ack releases; an ack for a DIFFERENT observation, or one carrying
// an unknown version, must keep the lease-held root retained (the unfixed reader released all three).
func TestGCRetention_AckReleaseRequiresExactObservationAndVersion(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)

	match := gcSeed(t, tp, "src/match.txt", "lease acknowledged under its exact observation\n")
	wrongObs := gcSeed(t, tp, "src/wrongobs.txt", "lease ack names a DIFFERENT observation\n")
	badVer := gcSeed(t, tp, "src/badver.txt", "lease ack carries an unknown version\n")
	open := gcSeed(t, tp, "src/open.txt", "lease with no acknowledgement at all\n")

	nMatch, nWrong, nBad, nOpen := deliveryNonce("1"), deliveryNonce("2"), deliveryNonce("3"), deliveryNonce("4")
	oMatch, oWrong, oBad, oOpen := obsIDText("match"), obsIDText("wrong-lease"), obsIDText("bad"), obsIDText("open")

	writeJSONLLines(t, l.State, deliveryLeaseFile,
		map[string]any{"v": 1, "delivery": nMatch, "request": match.Hash.String(), "observation_id": oMatch},
		map[string]any{"v": 1, "delivery": nWrong, "request": wrongObs.Hash.String(), "observation_id": oWrong},
		map[string]any{"v": 1, "delivery": nBad, "request": badVer.Hash.String(), "observation_id": oBad},
		map[string]any{"v": 1, "delivery": nOpen, "request": open.Hash.String(), "observation_id": oOpen},
	)
	writeJSONLLines(t, l.State, deliveryAckFile,
		// Exact match: nonce + observation + known version → releases.
		map[string]any{"v": 1, "delivery": nMatch, "observation_id": oMatch},
		// Same nonce, DIFFERENT observation identity → must not release nWrong's lease.
		map[string]any{"v": 1, "delivery": nWrong, "observation_id": obsIDText("some-other-observation")},
		// Known nonce+observation but an unknown version → an ack this build cannot vouch for.
		map[string]any{"v": 999, "delivery": nBad, "observation_id": oBad},
	)

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError, "well-formed lease and ack journals are readable")

	_, err = tp.Store.GetRoot(ctx, match.Hash)
	require.ErrorIs(t, err, core.ErrNotFound, "an exactly-acknowledged lease releases its root")
	_, err = tp.Store.GetRoot(ctx, wrongObs.Hash)
	require.NoError(t, err, "an ack for a different observation must not release this lease")
	_, err = tp.Store.GetRoot(ctx, badVer.Hash)
	require.NoError(t, err, "an ack of an unknown version must not release a lease")
	_, err = tp.Store.GetRoot(ctx, open.Hash)
	require.NoError(t, err, "a lease with no acknowledgement stays open")
}

// TestGCRetention_LegacyLeaseWithoutObservationStillReleasesOnNonce pins the compatibility direction
// the brief calls out: a lease written before the observation_id field existed carries no identity to
// match, so a valid acknowledgement for its unique nonce still releases it. (This is the exact shape
// TestGC_AcknowledgedDeliveryLeaseStopsRetaining depends on; it must keep working.)
func TestGCRetention_LegacyLeaseWithoutObservationStillReleasesOnNonce(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)

	legacy := gcSeed(t, tp, "src/legacy.txt", "legacy lease, acknowledged\n")
	nonce := deliveryNonce("5")
	writeJSONLLines(t, l.State, deliveryLeaseFile,
		map[string]any{"v": 1, "delivery": nonce, "request": legacy.Hash.String()}) // no observation_id
	writeJSONLLines(t, l.State, deliveryAckFile,
		map[string]any{"v": 1, "delivery": nonce, "observation_id": obsIDText("legacy")})

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError)
	_, err = tp.Store.GetRoot(ctx, legacy.Hash)
	require.ErrorIs(t, err, core.ErrNotFound,
		"a legacy lease with no observation_id is released by a valid ack for its unique nonce")
}

// TestGCRetention_UnreadableCheckpointDirHaltsCollection: the checkpoints/ directory replaced by a
// regular file (an aliased / non-directory retention source) must halt the pass. GC is blind to what
// committed checkpoints would name, so it may not sweep. The unfixed gcRootFiles swallowed the
// os.ReadDir error and swept a doomed root while reporting success.
func TestGCRetention_UnreadableCheckpointDirHaltsCollection(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)
	doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")
	require.NoError(t, os.RemoveAll(paths.Long(l.Checkpoints)))
	require.NoError(t, os.WriteFile(paths.Long(l.Checkpoints), []byte("not a directory\n"), 0o600))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.RetentionRootsError, "a non-directory checkpoints source must be reported, not read as empty")
	require.Zero(t, rep.DeletedObjects, "nothing may be swept while a retention source is unreadable")
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.NoError(t, err, "the doomed root must survive a pass that could not read the checkpoints dir")
}

// TestGCRetention_UnreadablePendingDirHaltsCollection: the pending-write registry directory replaced
// by a regular file must halt. The unfixed pendingRootFiles returned nil on any ReadDir error, so it
// dropped the whole registry — every written-but-not-yet-rooted object — and swept a doomed root.
func TestGCRetention_UnreadablePendingDirHaltsCollection(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)
	doomed := gcSeed(t, tp, "src/doomed.txt", "collectable only if the pass runs\n")
	// PutBytes leaves the pending-write directory behind (empty, its marker removed on completion);
	// replace that directory with a regular file so the registry path is a non-directory.
	pend := filepath.Join(l.State, pendingWriteDir)
	require.NoError(t, os.RemoveAll(paths.Long(pend)))
	require.NoError(t, os.WriteFile(paths.Long(pend), []byte("not a directory\n"), 0o600))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.True(t, rep.RetentionRootsError)
	require.Zero(t, rep.DeletedObjects)
	_, err = tp.Store.GetRoot(ctx, doomed.Hash)
	require.NoError(t, err, "nothing may be swept while the pending-write registry is unreadable")
}

// TestGCRetention_NonRegularAckSourceRetainsWithoutHaltingOrHanging: a delivery-acks path that is not
// a regular file (here a directory) is not an acknowledgement journal. GC must not treat it as one, and
// — because failing to read acks only RETAINS more — must keep the leased root and finish without
// RetentionRootsError, never opening it (a FIFO would hang os.Open; that Unix case is the sibling
// build-tagged test).
func TestGCRetention_NonRegularAckSourceRetainsWithoutHaltingOrHanging(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	l := paths.Of(tp.Root)
	leased := gcSeed(t, tp, "src/leased.txt", "held by an open lease; the ack journal is unreadable\n")
	nonce := deliveryNonce("6")
	writeJSONLLines(t, l.State, deliveryLeaseFile,
		map[string]any{"v": 1, "delivery": nonce, "request": leased.Hash.String(), "observation_id": obsIDText("leased")})
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Join(l.State, deliveryAckFile)), 0o700))

	rep, err := tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	require.False(t, rep.RetentionRootsError, "an unreadable ACK journal retains more, it does not halt")
	_, err = tp.Store.GetRoot(ctx, leased.Hash)
	require.NoError(t, err, "a lease whose acknowledgements cannot be read stays open and retained")
}

// TestGCRetention_CancelledContextIsNotARetentionError guards the explicit-cancellation direction: a
// cancelled pass reports context.Canceled, never a nil error and never RetentionRootsError. The
// retention-read halt path must not swallow or reclassify cancellation.
func TestGCRetention_CancelledContextIsNotARetentionError(t *testing.T) {
	tp := newTestStore(t)
	for i := 0; i < 4; i++ {
		gcSeed(t, tp, fmt.Sprintf("src/f%02d.txt", i), fmt.Sprintf("cancel body %d\n", i))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep, err := tp.Store.GC(ctx, forceCollect)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, rep.RetentionRootsError, "a cancellation is not a retention-source failure")
}
