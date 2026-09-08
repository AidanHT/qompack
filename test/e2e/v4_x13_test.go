// V4 §4.13 — the hot path is unchanged with the full wave-3 resident set.
//
// TIMING ARM DEFERRED, DELIBERATELY. The reconciliation map homes this row in test/bench/hotpath
// and grades B-A…B-F against budgets. This machine measured the SAME hot-path row at 90.1 ms,
// 81.9 ms, 30.7 ms and 6.1 ms across four runs: the variance exceeds the effect, so a wall-clock
// assertion here would be noise wearing a gate's name. ADR 0010's co-load policy applies, and the
// measured arm belongs on a quiet runner. This row asserts the STRUCTURAL claim instead — what is
// resident, and what work the hot path does — which is the half that can be established here and
// is the half a regression would break first.
//
// It also lives in test/e2e rather than test/bench/hotpath: the bench package's budget machinery is
// owned elsewhere, and a structural A/B needs two DIFFERENT compositions of the same daemon, which
// only a composition root can build.
package e2e

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The two session identities: the full wave-3 arm and the observer-only reference arm.
const (
	x13v4Session    = core.SessionID("sess-e2e-v4-x13")
	x13v4RefSession = core.SessionID("sess-e2e-v4-x13-ref")
)

// x13v4Turns is the hook burst each arm replays. Identical on both sides, so the write sets are
// comparable by construction.
const x13v4Turns = 8

// x13v4WriteSet returns every path under .qompack/ whose existence or size changed between before
// and after, as slash-relative names with volatile per-run components normalized away.
//
// It is a SET of names, never sizes: the two arms store different session ids and different tool
// use ids, so byte counts legitimately differ while the SHAPE of what the hot path touches must not.
func x13v4WriteSet(t *testing.T, root string, before map[string]int64) []string {
	t.Helper()
	out := map[string]bool{}
	dot := paths.Of(root).Dot
	err := filepath.WalkDir(paths.Long(dot), func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return nil //nolint:nilerr // a vanished temp file is not this walk's concern
		}
		rel, relErr := filepath.Rel(paths.Long(dot), p)
		if relErr != nil {
			return nil
		}
		name := filepath.ToSlash(rel)
		fi, statErr := d.Info()
		if statErr != nil {
			return nil //nolint:nilerr // a vanished temp file is not this walk's concern
		}
		if was, ok := before[name]; ok && was == fi.Size() {
			return nil // present and unchanged: this burst did not touch it
		}
		out[x13v4Normalize(name)] = true
		return nil
	})
	require.NoError(t, err)

	names := make([]string, 0, len(out))
	for n := range out {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// x13v4Normalize erases the per-run components of a path so two arms are comparable: the session id
// in a state file name, the content-addressed object shards, and the dated day log.
func x13v4Normalize(name string) string {
	switch {
	case len(name) > 7 && name[:7] == "objects":
		return "objects/<shard>/<object>"
	case len(name) > 5 && name[:5] == "logs/":
		return "logs/<log>"
	case len(name) > 6 && name[:6] == "state/":
		base := filepath.Base(name)
		for _, pfx := range []string{"draft-", "rehydrate-"} {
			if len(base) > len(pfx) && base[:len(pfx)] == pfx {
				return "state/" + pfx + "<session>.json"
			}
		}
		return name
	case len(name) > 6 && name[:6] == "spool/":
		return "spool/<wal>"
	case len(name) > 4 && name[:4] == "tmp/":
		return "tmp/<staging>"
	default:
		return name
	}
}

// x13v4Existing lists what is already under .qompack/ so the write set is a DELTA.
func x13v4Existing(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	dot := paths.Of(root).Dot
	_ = filepath.WalkDir(paths.Long(dot), func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return nil //nolint:nilerr // the tree may not exist yet
		}
		fi, statErr := d.Info()
		if statErr != nil {
			return nil //nolint:nilerr // a vanished temp file is not this listing's concern
		}
		if rel, relErr := filepath.Rel(paths.Long(dot), p); relErr == nil {
			out[filepath.ToSlash(rel)] = fi.Size()
		}
		return nil
	})
	return out
}

// TestV4_HotPathUnchangedWithTheFullWave3ResidentSet is V4-VERIFY §4.13, structural arm.
//
// The negative control is the last arm: one idle pass with the same resident set MUST change the
// write set. That is what proves the comparison can see added work at all — without it, "the write
// sets are identical" would also hold for a comparison that could not distinguish anything.
func TestV4_HotPathUnchangedWithTheFullWave3ResidentSet(t *testing.T) {
	// ── Arm A: the full wave-3 resident set ──────────────────────────────────────────────────────
	p := v4Project(t)
	r := v4StartRig(t, p)
	env := e2eEnv(p)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x13v4Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, x13v4Session, "hold every wave-3 subsystem resident"), env)

	// The resident set this row names includes the negative-knowledge ledger, and that one is
	// opened lazily, on a compaction — so the row has to be a daemon that has compacted before it
	// can assert the ledger is resident. See OpenLedgerByCompacting.
	r.OpenLedgerByCompacting(t, x13v4Session)

	// Residency is ASSERTED, not assumed: without it the two arms could be the same daemon twice.
	require.NotNil(t, r.W, "the checkpoint writer must be resident")
	_, srcErr := r.Src()
	require.NoError(t, srcErr, "the full SourceSet must resolve: store, segments, ledger, pins, graph, grammar, tokens")
	require.NotNil(t, r.Opts.LedgerHandle(), "the negative-knowledge ledger must be open")
	require.NotNil(t, r.Opts.Store, "the store must be open")
	require.NotNil(t, r.Opts.Graph, "the dependence DAG must be open")
	registered := r.RunIdle(t)
	for _, want := range []string{"advance_frontier", "act.checkpoint_cadence", "materialize_pins"} {
		require.Contains(t, registered, want,
			"the wave-3 idle task %q must be registered, or 'the full resident set' is a claim about "+
				"nothing; ran=%v", want, registered)
	}

	// The hook burst, with NO idle pass inside it: this is the hot path and nothing else.
	beforeA := x13v4Existing(t, p.Root)
	for i := range x13v4Turns {
		obsRunHook(t, r.Bin, []string{"observe", "tool"},
			obsToolPayload(t, p.Root, x13v4Session, fmt.Sprintf("toolu_v4x13_%02d", i),
				fmt.Sprintf("src/x13_%02d.go", i),
				fmt.Sprintf("package x13\n\nfunc h%02d() error { return nil }\n", i)), env)
	}
	r.WaitIndexed(t, x13v4Turns)
	fullSet := x13v4WriteSet(t, p.Root, beforeA)
	require.NotEmpty(t, fullSet, "the hook burst must have written something")

	// ── Arm B: the SAME burst against an observer-only daemon ────────────────────────────────────
	pr := v4Project(t)
	rr := v4StartObserverOnly(t, pr)
	envr := e2eEnv(pr)

	obsRunHook(t, rr.Bin, []string{"session-start"}, sessionStartFor(t, pr.Root, x13v4RefSession), envr)
	obsRunHook(t, rr.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, pr.Root, x13v4RefSession, "hold every wave-3 subsystem resident"), envr)

	beforeB := x13v4Existing(t, pr.Root)
	for i := range x13v4Turns {
		obsRunHook(t, rr.Bin, []string{"observe", "tool"},
			obsToolPayload(t, pr.Root, x13v4RefSession, fmt.Sprintf("toolu_v4x13_%02d", i),
				fmt.Sprintf("src/x13_%02d.go", i),
				fmt.Sprintf("package x13\n\nfunc h%02d() error { return nil }\n", i)), envr)
	}
	rr.WaitIndexed(t, x13v4Turns)
	refSet := x13v4WriteSet(t, pr.Root, beforeB)

	// ── The claim: the wave-3 residents add NO work to the hot path ──────────────────────────────
	require.Equal(t, refSet, fullSet,
		"a hook burst must touch the same files whether or not the checkpoint writer, the frontier "+
			"advancer, the pin store, the ledger and the cadence task are resident. A difference here "+
			"is wave-3 work that migrated ONTO the hot path.\nwith wave 3: %v\nwithout:    %v",
		fullSet, refSet)

	// The scheduler and the checkpointer are off the hot path in the strongest observable sense:
	// no draft and no artifact exist after a burst with no idle pass.
	require.Empty(t, cpCheckpointArtifacts(t, p.Root),
		"no hook in the burst may seal a checkpoint — sealing is idle or PreCompact work")

	// ── NEGATIVE CONTROL: one idle pass MUST change the write set ────────────────────────────────
	cpCloseObserverSegment(t, r.Segs, x13v4Session)
	cpCloseSegment(t, r.Segs, x13v4Session, 0, 9)
	beforeIdle := x13v4Existing(t, p.Root)
	r.RunIdle(t)
	idleSet := x13v4WriteSet(t, p.Root, beforeIdle)
	require.NotEmpty(t, idleSet,
		"NEGATIVE CONTROL: an idle pass with the same resident set must write files the hook burst "+
			"did not. If it does not, the comparison above cannot see wave-3 work at all and its "+
			"passing means nothing")
	draft := filepath.Join(paths.Of(p.Root).State, "draft-"+string(x13v4Session)+".json")
	if _, err := os.Stat(paths.Long(draft)); err == nil {
		require.Contains(t, idleSet, "state/draft-<session>.json",
			"the idle pass's own draft write must show up in the delta; idle wrote %v", idleSet)
	}
}
