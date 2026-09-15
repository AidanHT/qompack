package security

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
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
)

// Retention of EVIDENCE, which is a different rule from retention of data.
//
// §12.3's recovery for a corrupt object or a corrupt sketch is "quarantine the bytes, Loud,
// continue" — and the whole value of that is an operator being able to look at the bytes
// afterwards. A garbage collection that swept the quarantine, or the capture sidecars that join a
// stored root to the delivery that produced it, would turn a recoverable degradation into an
// unexplainable one. Plan §3 says it in the fsck row: repairs "preserve diagnostic/quarantine
// evidence within retention policy; no destructive default cleanup".
//
// The pass below is deliberately the most destructive one the API can express — negative retention
// on both axes, which is what `qompack fsck --gc-all` uses to force collection — because anything
// weaker would leave "GC did not collect it" and "GC never considered it" indistinguishable.

// retentionSession is the session this file's captures belong to.
const retentionSession = core.SessionID("sess-security-retention-0001")

// TestSecurity_ForcedGCPreservesQuarantineAndCaptureEvidence.
func TestSecurity_ForcedGCPreservesQuarantineAndCaptureEvidence(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	rec := newRecord(t, "retention_forced_gc_preserves_evidence")
	rec.Capability = CapRetention

	// A real capture, so records/captures/** has a sidecar to preserve.
	body := deniedBodyText()
	writeProjectFile(t, p, deniedPath, body)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, retentionSession))
	require.True(t, waitDaemonUp(t, p.Root), "session-start must bring a daemon up")
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, retentionSession, deniedToolUseID, deniedPath, body))
	requireIndexed(t, p.Root, deniedToolUseID)
	runHook(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, retentionSession))
	shutdownIfReachable(t, p.Root)

	// A quarantined object, produced the way §12.3 produces one: a damaged object refused on read.
	s := openStoreAt(t, p.Root)
	ctx := context.Background()
	res, err := s.PutBytes(ctx, []byte("// the object that will be damaged\n"+strings.Repeat("x = 1;\n", 64)),
		store.PutOptions{Tool: "Read", Path: "src/doomed.ts"})
	require.NoError(t, err)
	require.NotEmpty(t, res.Root.Chunks, "the seeded object must name its chunks")
	require.NoError(t, s.Flush(ctx))
	require.NoError(t, s.Close())

	chunk := res.Root.Chunks[0].Hash
	raw, err := os.ReadFile(paths.Long(objectFilePath(p.Root, chunk)))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(objectFilePath(p.Root, chunk)), raw[:len(raw)/2], 0o600))

	reader := openStoreAt(t, p.Root)
	_, err = reader.GetChunk(ctx, chunk)
	require.Error(t, err, "the damaged object must be refused")
	require.True(t, quarantineHolds(t, p.Root, chunk), "the damaged object must be quarantined first")

	// A quarantined SKETCH, produced through internal/sketch's own Quarantine.
	sketchPath := filepath.Join(paths.Of(p.Root).Sketches, "tried.bloom")
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(sketchPath)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(sketchPath), []byte("not a valid bloom filter"), 0o600))
	movedSketch, err := sketch.Quarantine(sketchPath)
	require.NoError(t, err, "internal/sketch must be able to quarantine a corrupt filter")
	require.Contains(t, filepath.Base(movedSketch), ".corrupt.",
		"a quarantined sketch keeps the .corrupt.<ms> shape §12.3 names")

	before := evidenceInventory(t, p.Root)
	require.NotEmpty(t, before.Quarantine, "the quarantine must hold something, or this proves nothing")
	require.NotEmpty(t, before.Captures, "records/captures/** must hold something, or this proves nothing")

	// The most destructive pass the API can express.
	report, err := reader.GC(ctx, store.GCPolicy{RetainDays: -1, RetainSessions: -1})
	require.NoError(t, err, "a forced GC pass must complete")
	require.NoError(t, reader.Close())

	after := evidenceInventory(t, p.Root)

	var lost []string
	for _, name := range before.Quarantine {
		if !contains(after.Quarantine, name) {
			lost = append(lost, "quarantine/"+name)
		}
	}
	for _, name := range before.Captures {
		if !contains(after.Captures, name) {
			lost = append(lost, "records/captures/"+name)
		}
	}
	if _, statErr := os.Stat(paths.Long(movedSketch)); statErr != nil {
		lost = append(lost, filepath.Base(movedSketch))
	}

	rec.Detail = fmt.Sprintf("GC deleted %d object(s); quarantine %d→%d, captures %d→%d",
		report.DeletedObjects, len(before.Quarantine), len(after.Quarantine),
		len(before.Captures), len(after.Captures))

	if len(lost) == 0 {
		rec.Outcome = OutcomeVerified
		rec.Reason = "a GC pass with negative retention on both axes — the forcing form — collected " +
			"objects and left every piece of diagnostic evidence in place: the quarantined object, " +
			"the .corrupt.<ms> sketch, and every records/captures/** sidecar."
	} else {
		rec.Outcome = OutcomeFailed
		rec.Reason = fmt.Sprintf("a forced GC pass removed diagnostic evidence: %v. Owner: "+
			"internal/store.", lost)
		t.Errorf("plan §3: a forced GC pass removed diagnostic evidence: %v", lost)
	}
	writeRecord(t, rec)
}

// evidence is the inventory of what a GC pass must not touch.
type evidence struct {
	Quarantine []string
	Captures   []string
}

// evidenceInventory lists, by path relative to its own root, everything under .qompack/tmp/
// quarantine/ and .qompack/records/captures/.
func evidenceInventory(t *testing.T, root string) evidence {
	t.Helper()
	l := paths.Of(root)
	return evidence{
		Quarantine: relativeFiles(t, filepath.Join(l.Tmp, "quarantine")),
		Captures:   relativeFiles(t, filepath.Join(l.Records, "captures")),
	}
}
