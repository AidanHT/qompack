// V5 §4.7 — SP-15's selected complete records reach the actual SP-11 consumer without native cuts.
//
// The retained identifier, TestV5_ScheduledCutFeedsSelectorFeedsCheckpoint, names the historical
// seam: scheduler p-selection → analyzer suffix selection → checkpoint content. That seam was never
// built. What shipped (plans/sdd/V5-SP-15/contract.md §1, report-main.md §1) is a different
// consumer: the daemon — the one composition root allowed to hold a negknow.Record and an
// analyzer.Candidate at once — runs analyzer.Propose over the eliminations a rehydration could
// carry, maps the Proposal onto rehydrate.SelectionOutcome, and hands it to rehydrate.Build as
// request data. Item 3 of the SessionStart(source=compact) payload honours the selection; its
// archive-only and overflow outcomes reach item 7's drop report. The current §4 criterion is that
// seam, and "without native cuts" is the register's word for it: the scheduler exposes no native
// cut (scheduler.CapNativeCut is ClassUnsupported), and the guard that lets the selector construct
// is the LOCAL capability — a real daemon scheduler.Runtime over the real store and DAG — never a
// host-side cut position. Propose is called at p = 0; nothing here is scattered before any p.
//
// Wired: the real observer over real hook processes, the real negative-knowledge ledger opened the
// way a first compaction opens it, the real MCP record_eliminated handler, the shipped
// NewSchedulerRuntime over the rig's real store and DAG, the daemon's real rehydrate service, and
// the real drop reporter read back through its own reader.
//
// The old-to-new assertion map is in plans/sdd/V5-VERIFY/x07-disposition.md.
package e2e

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/scheduler"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/qompack/qompack/internal/tokens"
)

// x7v5Session is this row's session identity.
const x7v5Session = core.SessionID("sess-e2e-v5-x07")

// x7v5CompleteRecords is how many eliminations the complete-record arm carries: more than one, so
// the selector's ordering is observable, and far below runtime.rehydrate.eliminationsTopN (8, the
// Appendix C default), so every one of them must render whole.
const x7v5CompleteRecords = 3

// x7v5PressureRecords is how many eliminations the pressure arms record. Every one is active and
// therefore Mandatory to the selector, whose cheapest way to carry a record is its pointer — the
// record id, "elim_" plus twelve hex characters. Forty-eight of those price above
// x7v5PressureBudget under any estimator this tree ships (the bare fallback alone charges five
// tokens per id), and the arm ASSERTS that ordering with the daemon's own estimator rather than
// assuming it.
const x7v5PressureRecords = 48

// x7v5PressureBudget is runtime.rehydrate.maxTokens for the pressure arms. It has to sit ABOVE the
// injection wrapper — the open tag, the document header and the close tag, roughly forty tokens,
// which rehydrate.Build charges before it builds any item and past which it returns without
// running the selection at all — and BELOW the summed pointer prices of x7v5PressureRecords
// records, so the selector's reserve step must overflow. Both halves are asserted in the arm.
const x7v5PressureBudget = 120

// x7v5PressureMinTokens is runtime.rehydrate.minTokens for the pressure arms: the validated floor
// (config/validate.go: minTokens ≥ 1, minTokens ≤ maxTokens), so maxTokens may be lowered.
const x7v5PressureMinTokens = 1

// x7v5Item3Heading is the heading rehydrate renders for item 3.
const x7v5Item3Heading = "## 3. Approaches already eliminated"

// x7v5ArchiveKind and x7v5OverflowKind are the drop kinds the selection path mints: archive_only
// exists nowhere but internal/rehydrate/selection.go, and the overflow entry there is the only
// overflow whose ID column names an elimination record rather than "payload".
const (
	x7v5ArchiveKind     = "archive_only"
	x7v5OverflowKind    = "overflow"
	x7v5EliminationKind = "elimination"
)

// x7v5SelectionDetail is the substring every selection-path drop carries in its detail column —
// "representation selection" appears in both archiveRecoveryDetail and the overflow detail, and in
// nothing the pre-SP-15 path writes.
const x7v5SelectionDetail = "representation selection"

// x7v5Config renders the project config for one arm.
func x7v5Config(selectionEnabled bool, pressure bool) string {
	sel := fmt.Sprintf(`"selection":{"submodularEnabled":%t}`, selectionEnabled)
	if !pressure {
		return `{"runtime":{` + sel + `}}`
	}
	return fmt.Sprintf(`{"runtime":{%s,"rehydrate":{"minTokens":%d,"maxTokens":%d}}}`,
		sel, x7v5PressureMinTokens, x7v5PressureBudget)
}

// x7v5Project is testutil.NewProject with an arm's config and the e2e shutdown cleanup.
func x7v5Project(t *testing.T, cfgJSON string) *testutil.Project {
	t.Helper()
	p := testutil.NewProject(t, testutil.WithConfig(cfgJSON))
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	return p
}

// x7v5Elimination is one recorded elimination as the row knows it: the id the ledger minted and
// the three free-text fields item 3 must render whole.
type x7v5Elimination struct {
	ID       string
	Target   string
	Approach string
	Reason   string
}

// x7v5Line is the body of the line rehydrate.eliminationLine renders for e — target, approach and
// reason, in that shape, tagged active. A payload that contains it carried the COMPLETE record;
// a pointer, a truncated reason or a capsule would not match.
func (e x7v5Elimination) line() string {
	return e.Target + " — \"" + e.Approach + "\" — " + e.Reason + " [active]"
}

// x7v5Record drives the real record_eliminated handler once and returns what the ledger minted.
// The fields are distinct per i because the ledger deduplicates by descriptor identity.
func x7v5Record(t *testing.T, srv mcp.Server, i int) x7v5Elimination {
	t.Helper()
	e := x7v5Elimination{
		Target:   fmt.Sprintf("src/api/handler_%02d.go:serve", i),
		Approach: fmt.Sprintf("retry the upstream call %d times before failing", i+1),
		Reason:   fmt.Sprintf("the upstream rejects a retry storm from handler %02d within its window", i),
	}
	var recorded struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	res := v4Call(t, srv, x7v5Session, mcp.ToolRecordEliminated, map[string]any{
		"target": e.Target, "approach": e.Approach, "reason": e.Reason,
		"scope": "session", "depends_on": []string{"docker-compose.yml"},
	}, &recorded)
	require.False(t, res.IsError, "the real handler must record elimination %d: %+v", i, res)
	require.NotEmpty(t, recorded.ID, "a recorded elimination carries the id the ledger minted")
	// Active is what the daemon maps onto Mandatory: every record here must bind the selector.
	require.Equal(t, "active", recorded.Status, "elimination %d must be recorded active", i)
	e.ID = recorded.ID
	return e
}

// x7v5Start composes the rig, registers the session, opens the ledger the way a first compaction
// does, and records n eliminations through the real MCP handler.
func x7v5Start(t *testing.T, p *testutil.Project, n int) (*v4Rig, []x7v5Elimination) {
	t.Helper()
	r := v4StartRig(t, p)
	env := e2eEnv(p)
	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x7v5Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, x7v5Session, "stop retrying the upstream; find what rejects it"), env)
	r.SeedTurns(t, x7v5Session, "v5x07", x7v5CompleteRecords)
	v4EnsureLedger(t, r, x7v5Session)

	srv := v4Server(t, v4ToolDeps(t, r))
	recs := make([]x7v5Elimination, 0, n)
	for i := range n {
		recs = append(recs, x7v5Record(t, srv, i))
	}
	return r, recs
}

// x7v5OpenGate constructs the shipped scheduler Runtime over the rig's real store and DAG — the
// local capability that opens the closing-note-3 gate — and returns the closer that releases it.
func x7v5OpenGate(t *testing.T, r *v4Rig) func() {
	t.Helper()
	rt, err := daemon.NewSchedulerRuntime(daemon.SchedulerRuntimeOptions{
		ProjectRoot: r.P.Root,
		Session:     x7v5Session,
		Cfg:         r.P.Cfg,
		Clock:       core.SystemClock(),
		Log:         r.P.Log,
		Metrics:     r.Opts.Metrics,
		Store:       r.Opts.Store,
		Graph:       r.Opts.Graph,
		LedgerFn:    r.LedgerFn(),
		Checkpoints: r.W,
		Sources:     r.Src,
		Getenv:      func(string) string { return "" },
	})
	require.NoError(t, err, "the shipped scheduler Runtime must compose over the real store and DAG")
	require.True(t, scheduler.PSelectionAvailable(),
		"constructing the real Runtime is what opens the p-selection gate")
	closed := false
	closer := func() {
		if closed {
			return
		}
		closed = true
		require.NoError(t, daemon.CloseSchedulerRuntime(rt))
		require.False(t, scheduler.PSelectionAvailable(),
			"closing the real Runtime is what releases the p-selection gate")
	}
	t.Cleanup(closer)
	return closer
}

// x7v5Drops reads the drop report the daemon's rehydrate service wrote, through the real reader.
func x7v5Drops(t *testing.T, r *v4Rig) []checkpoint.DropEntry {
	t.Helper()
	drops, err := r.DropReporter().CurrentDrops(context.Background(), x7v5Session)
	require.NoError(t, err, "the rehydration must have written a readable drop report")
	return drops
}

// x7v5Count tallies the drop entries of one kind whose ID names a recorded elimination, and
// separately those whose detail carries the selection path's wording.
func x7v5Count(drops []checkpoint.DropEntry, recs []x7v5Elimination, kind string) (named, selectionWorded int) {
	ids := make(map[string]bool, len(recs))
	for _, e := range recs {
		ids[e.ID] = true
	}
	for _, d := range drops {
		if d.Kind != kind {
			continue
		}
		if ids[d.ID] {
			named++
		}
		if strings.Contains(d.Detail, x7v5SelectionDetail) {
			selectionWorded++
		}
	}
	return named, selectionWorded
}

// x7v5RequireNoSelectionSignature is the shape of a rehydration the selector did NOT feed: item 3
// went through the pre-SP-15 ranking, so under pressure its records are reported as elimination
// drops, and nothing anywhere in the report is an archive_only entry or wears the selection path's
// wording.
func x7v5RequireNoSelectionSignature(t *testing.T, drops []checkpoint.DropEntry, recs []x7v5Elimination, why string) {
	t.Helper()
	archived, archiveWorded := x7v5Count(drops, recs, x7v5ArchiveKind)
	require.Zero(t, archived, "%s: no record may be archived by a selection that did not run: %+v", why, drops)
	require.Zero(t, archiveWorded, "%s: no archive_only wording may appear: %+v", why, drops)
	overflowed, overflowWorded := x7v5Count(drops, recs, x7v5OverflowKind)
	require.Zero(t, overflowed, "%s: no overflow may name a record: %+v", why, drops)
	require.Zero(t, overflowWorded, "%s: no overflow may carry the selection wording: %+v", why, drops)
	eliminated, _ := x7v5Count(drops, recs, x7v5EliminationKind)
	require.Positive(t, eliminated,
		"%s: under pressure the shipped ranking reports the records it could not fit as elimination "+
			"drops — their absence would mean the records never reached the consumer at all: %+v", why, drops)
}

// x7v5PressurePreconditions proves the pressure arm is in the regime it claims: the injection
// wrapper fitted (otherwise Build returns before any item is built and the selector never runs),
// and the records' pointer prices, under the daemon's own estimator, exceed the budget.
func x7v5PressurePreconditions(t *testing.T, p *testutil.Project, drops []checkpoint.DropEntry, recs []x7v5Elimination) {
	t.Helper()
	for _, d := range drops {
		require.False(t, d.Kind == x7v5OverflowKind && d.ID == "payload",
			"the wrapper must fit at %d tokens or the selection is never consulted: %+v",
			x7v5PressureBudget, d)
	}
	est := tokens.NewForProject(p.Cfg, tokens.DefaultCalibPath(), p.Root)
	var pointers core.Tokens
	for _, e := range recs {
		pointers += est.EstimateString(e.ID, tokens.ClassProse)
	}
	require.Greater(t, int(pointers), x7v5PressureBudget,
		"the pressure arm is meaningful only when even the pointers do not fit: %d records price "+
			"their ids at %d tokens against a %d-token budget", len(recs), int(pointers), x7v5PressureBudget)
}

// TestV5_ScheduledCutFeedsSelectorFeedsCheckpoint is V5-VERIFY §4.7 under its retained
// identifier. The current criterion: SP-15's selected complete records reach the actual SP-11
// consumer without native cuts.
//
// The negative controls are the two real switches the shipped wiring checks before it will run the
// selector: the operator switch runtime.selection.submodularEnabled (checked first, in
// rehydrateService.selectionFor), and the closing-note-3 gate that only a constructed
// scheduler.Runtime opens (checked inside analyzer.NewSelector). Each is severed in its own arm
// under the same pressure that makes the selector's decision visible, and the consumer's own drop
// report is what tells the two paths apart.
func TestV5_ScheduledCutFeedsSelectorFeedsCheckpoint(t *testing.T) {
	// ── Arm 1: selected complete records reach the consumer ─────────────────────────────────────
	t.Run("selected_complete_records_reach_item3", func(t *testing.T) {
		p := x7v5Project(t, x7v5Config(true, false))
		require.True(t, p.Cfg.Runtime.Selection.SubmodularEnabled, "the operator switch must be on")
		r, recs := x7v5Start(t, p, x7v5CompleteRecords)
		closeGate := x7v5OpenGate(t, r)

		// "Without native cuts": the register the scheduler answers capability questions from says
		// the host exposes no cut, and no configuration changes that. What opened the gate above
		// was the local Runtime, not a cut position.
		native := scheduler.Supports(scheduler.CapNativeCut, p.Cfg.Scheduler)
		require.False(t, native.Available, "the installed host exposes no native cut: %+v", native)
		require.Equal(t, scheduler.ClassUnsupported, native.Class)

		ac := r.CompactStart(t, x7v5Session)
		require.NotEmpty(t, ac, "a compact SessionStart must inject a rehydrated context")
		require.Contains(t, ac, x7v5Item3Heading, "item 3 is where the selection reaches the model: %s", ac)

		// Every record, COMPLETE: target, approach and reason as the ledger holds them, tagged
		// active. This is the consumer serializing what the selector admitted, not a unit
		// selector's return value.
		for _, e := range recs {
			require.Contains(t, ac, e.line(),
				"record %s must reach the payload whole, in the consumer's own rendering", e.ID)
		}

		// In the SELECTOR's order. Proposal.Chosen is assembled by walking candidates in Item
		// order, and SelectionOutcome.Keep replaces the slice-score ranking that shipped, so the
		// rendered order is record id ascending. A necessary condition, asserted as such; the
		// pressure arms below are what make the selection's presence sufficient.
		byID := append([]x7v5Elimination(nil), recs...)
		sort.Slice(byID, func(i, j int) bool { return byID[i].ID < byID[j].ID })
		last := -1
		for _, e := range byID {
			at := strings.Index(ac, e.line())
			require.Greater(t, at, last,
				"item 3 must render in the selector's own order (record id ascending); %s is out of place:\n%s",
				e.ID, ac)
			last = at
		}

		// Nothing was archived and nothing overflowed: under the shipped budget every complete
		// record fitted, so the report carries no selection outcome to explain.
		drops := x7v5Drops(t, r)
		archived, _ := x7v5Count(drops, recs, x7v5ArchiveKind)
		require.Zero(t, archived, "no record may be archived when every record fitted: %+v", drops)
		overflowed, _ := x7v5Count(drops, recs, x7v5OverflowKind)
		require.Zero(t, overflowed, "no overflow may be reported when every record fitted: %+v", drops)

		closeGate()
		p.AssertAppendOnly(t)
	})

	// ── Arm 2: the selector's decision is visible in the consumer's own drop report ─────────────
	//
	// Under a budget the pointers alone exceed, contract §3's reserve step overflows BEFORE anything
	// optional is bought, Proposal.Chosen is nil, and applySelection reports every record as
	// archive_only with a recovery path plus one overflow entry whose ID column is the offending
	// record's id and whose detail is a sentence — the two never share a column (defect 6 of the
	// SP-15 integration). None of that shape exists on the pre-SP-15 path.
	t.Run("explicit_overflow_reaches_item7", func(t *testing.T) {
		p := x7v5Project(t, x7v5Config(true, true))
		require.True(t, p.Cfg.Runtime.Selection.SubmodularEnabled, "the operator switch must be on")
		require.Equal(t, x7v5PressureBudget, p.Cfg.Runtime.Rehydrate.MaxTokens, "the pressure budget must be in force")
		r, recs := x7v5Start(t, p, x7v5PressureRecords)
		closeGate := x7v5OpenGate(t, r)

		r.CompactStart(t, x7v5Session)
		drops := x7v5Drops(t, r)
		x7v5PressurePreconditions(t, p, drops, recs)

		archived, archiveWorded := x7v5Count(drops, recs, x7v5ArchiveKind)
		require.Equal(t, len(recs), archived,
			"every record the selection could not carry must be reported archive_only, by id: %+v", drops)
		require.Equal(t, archived, archiveWorded,
			"every archive_only entry must carry the recovery path — an archive without a way back is a drop")
		overflowed, overflowWorded := x7v5Count(drops, recs, x7v5OverflowKind)
		require.Equal(t, 1, overflowed,
			"exactly one overflow entry must name the mandatory record that could not be carried: %+v", drops)
		require.Equal(t, 1, overflowWorded, "the overflow entry must say it came from representation selection")
		eliminated, _ := x7v5Count(drops, recs, x7v5EliminationKind)
		require.Zero(t, eliminated,
			"an overflowed selection admits nothing, so item 3 has no units to drop as eliminations — "+
				"their presence would mean the consumer took the shipped ranking instead: %+v", drops)

		closeGate()
		p.AssertAppendOnly(t)
	})

	// ── NEGATIVE CONTROL 1: the operator switch off, gate open ──────────────────────────────────
	t.Run("negative_control_operator_switch_off", func(t *testing.T) {
		p := x7v5Project(t, x7v5Config(false, true))
		require.False(t, p.Cfg.Runtime.Selection.SubmodularEnabled, "the operator switch must be off")
		r, recs := x7v5Start(t, p, x7v5PressureRecords)
		closeGate := x7v5OpenGate(t, r)

		r.CompactStart(t, x7v5Session)
		drops := x7v5Drops(t, r)
		x7v5PressurePreconditions(t, p, drops, recs)
		x7v5RequireNoSelectionSignature(t, drops, recs,
			"NEGATIVE CONTROL (runtime.selection.submodularEnabled=false with the gate open)")

		closeGate()
		p.AssertAppendOnly(t)
	})

	// ── NEGATIVE CONTROL 2: the operator switch on, no local capability ─────────────────────────
	//
	// No scheduler.Runtime is constructed, so scheduler.PSelectionAvailable() is false and
	// analyzer.NewSelector refuses with core.ErrNotImplemented; selectionFor degrades to nil and the
	// consumer takes the shipped path. This is §6's replacement guard: the selector's prerequisite
	// is an actual local capability, and its absence is honest degradation, never a failed session.
	t.Run("negative_control_gate_closed", func(t *testing.T) {
		// The gate is process-wide, and a row elsewhere in this package may construct a Runtime it
		// never closes. Releasing it here is the same store CloseSchedulerRuntime performs, and the
		// closed state is the process default this arm needs — nothing to restore afterwards.
		scheduler.DisablePSelection()
		require.False(t, scheduler.PSelectionAvailable(), "the gate must be closed before this arm drives a compaction")
		p := x7v5Project(t, x7v5Config(true, true))
		require.True(t, p.Cfg.Runtime.Selection.SubmodularEnabled, "the operator switch must be on")
		r, recs := x7v5Start(t, p, x7v5PressureRecords)

		// At this budget the payload is empty on both paths; the drop report is the evidence.
		r.CompactStart(t, x7v5Session)
		require.False(t, scheduler.PSelectionAvailable(), "nothing in a compaction may open the gate")
		drops := x7v5Drops(t, r)
		x7v5PressurePreconditions(t, p, drops, recs)
		x7v5RequireNoSelectionSignature(t, drops, recs,
			"NEGATIVE CONTROL (submodularEnabled=true, no scheduler.Runtime constructed)")

		p.AssertAppendOnly(t)
	})
}
