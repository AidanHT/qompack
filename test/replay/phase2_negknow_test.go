package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// This file operationalizes Qompack.md §10 Phase 2's exit criterion — "measurable reduction in
// repeated-elimination events in replay, with zero stale-block incidents (a viable approach
// wrongly refused) on sessions that include a dependency change" — against the committed
// 24-session synthetic corpus, and §11.2's redundant-work-rate metric with it.
//
// ── THE MODELLING RULE (controller ruling R4) ────────────────────────────────────────────────
//
// The committed corpus contains ZERO OBSERVED RE-ATTEMPTS: no session records the same
// (target, approach) twice, and no non-elimination tool call carries an approach argument. A
// re-attempt is therefore not something this file can count off the tape. It is MODELLED, by the
// rule below, and every number this test prints is a number about that model plus the real
// ledger's real answers inside it. Read the rule before reading the numbers.
//
//   - Walk each session's turns in order, maintaining the set of eliminations recorded so far.
//     Each record_eliminated tool call is one elimination E = (turn, target, approach, reason).
//   - STOCK BRANCH (no ledger): at each compaction turn c in s.CompactionAt, every elimination
//     recorded before c is FORGOTTEN — a stock summary has no eliminated[] slot (§6.1) — and is
//     re-attempted exactly once at the first assistant tool-call turn after c (§6.2's "re-attempts
//     an approach it eliminated forty minutes ago"). Each such re-attempt is one
//     repeated-elimination event. An elimination forgotten at c1 and re-attempted is then
//     re-recorded — the agent eliminates it again — so it is forgotten again at c2.
//   - NEGKNOW BRANCH: the same walk, against a real negknow.Ledger opened on its own
//     testutil.NewProject root. Every record_eliminated call goes through IngestMCP, which mints
//     evidence through the store and resolves depends_on from the store's file history. At each
//     modelled re-attempt point the agent first calls Query(target, approach, ScopeSession):
//     AnswerActive means the re-attempt is AVOIDED and is not counted; AnswerStale or AnswerAbsent
//     means it happens and counts — and, as in the stock branch, the agent then re-records the
//     elimination it has just re-eliminated, which is precisely the re-verification §8.3 asks for
//     after a staleness flip.
//   - DEPENDENCIES: before turn 0 the negknow branch seeds the store's file history for
//     docker-compose.yml and package-lock.json with one deterministic root each, so every
//     IngestMCP resolves both as depends_on. At each corpus Write to one of them it appends a NEW
//     version (a different root) and then calls RefreshStaleness(ctx, store).
//   - A dependency-change session is one carrying at least one such Write. That derivation is
//     used here rather than SynthSpec.DependencyChangeAt — the per-session spec is not stored in
//     the session file — and it is cross-checked against eval.CorpusSpecs() below so the two can
//     never silently disagree.
//
// The walk is a TURN WALK and not a Replay: eval.Harness.Replay in Deterministic mode calls a
// Policy only at compaction turns, and this metric is about what happens at every turn between
// them. eval.Harness.Load is what this file consumes the harness for, exactly as SP-02's driver
// constructs it; no eval.Policy and no eval.Replay call is involved, and internal/eval gains no
// constructor, method or field for this file (Rule W-3).

// p2ToolRecordEliminated is the tool name internal/eval/synth.go injects an elimination under. It
// is transcribed rather than imported because eval keeps its own copy unexported and Rule W-3
// forbids exporting one for this file's benefit.
const p2ToolRecordEliminated = "record_eliminated"

// p2ToolWrite is the tool name a dependency change arrives under. An Edit or a FileRead touching
// one of the two dependency files is NOT a dependency change: §8.3's staleness compares recorded
// content hashes, and only a write produces a new one.
const p2ToolWrite = "Write"

// p2DependencyFiles are internal/eval/synth.go's two synthDependencyFiles, in that file's order.
var p2DependencyFiles = [2]string{"docker-compose.yml", "package-lock.json"}

// p2Elimination is one record_eliminated call, decoded.
type p2Elimination struct {
	turn                     core.TurnIndex
	target, approach, reason string
}

// p2Args is the {"target","approach","reason"} payload a record_eliminated call carries.
type p2Args struct {
	Target   string `json:"target"`
	Approach string `json:"approach"`
	Reason   string `json:"reason"`
}

// p2Walk is one session's modelled event schedule: what happens at which turn, and how many
// repeated-elimination events the stock branch therefore suffers.
//
// It is computed ONCE per session and shared by both branches, which is what makes "the same walk"
// a property of the code rather than a claim in a comment: the two branches differ only in whether
// a ledger is consulted at a re-attempt point, never in where those points are.
type p2Walk struct {
	elims     []p2Elimination
	elimAt    map[core.TurnIndex][]int
	depWrite  map[core.TurnIndex][]string
	reattempt map[core.TurnIndex][]int
	stock     int
	depChange bool
}

// p2PlanWalk extracts the schedule from one session.
func p2PlanWalk(t *testing.T, s eval.Session) p2Walk {
	t.Helper()

	w := p2Walk{
		elimAt:    map[core.TurnIndex][]int{},
		depWrite:  map[core.TurnIndex][]string{},
		reattempt: map[core.TurnIndex][]int{},
	}
	for _, tn := range s.Turns {
		for _, c := range tn.ToolCalls {
			switch c.Name {
			case p2ToolRecordEliminated:
				var a p2Args
				require.NoError(t, json.Unmarshal(c.Args, &a),
					"%s: turn %d carries an undecodable record_eliminated payload", s.ID, tn.Index)
				w.elimAt[tn.Index] = append(w.elimAt[tn.Index], len(w.elims))
				w.elims = append(w.elims, p2Elimination{
					turn: tn.Index, target: a.Target, approach: a.Approach, reason: a.Reason,
				})
			case p2ToolWrite:
				for _, p := range c.Paths {
					if p == p2DependencyFiles[0] || p == p2DependencyFiles[1] {
						w.depWrite[tn.Index] = append(w.depWrite[tn.Index], p)
						w.depChange = true
					}
				}
			}
		}
	}

	compact := make(map[core.TurnIndex]bool, len(s.CompactionAt))
	for _, c := range s.CompactionAt {
		compact[c] = true
	}

	// known is R4's "set of eliminations recorded so far", in first-record order. It never
	// shrinks, which is how "re-recorded, so it is forgotten again at c2" is modelled.
	var known []int
	seen := map[string]bool{}
	for _, tn := range s.Turns {
		// The compaction snapshot is taken BEFORE this turn's own eliminations are added, because
		// R4 forgets what was recorded strictly before c.
		if compact[tn.Index] && len(known) > 0 {
			if r, ok := p2FirstToolTurnAfter(s, tn.Index); ok {
				w.reattempt[r] = append(w.reattempt[r], known...)
				w.stock += len(known)
			}
		}
		for _, i := range w.elimAt[tn.Index] {
			k := w.elims[i].target + "\x00" + w.elims[i].approach
			if seen[k] {
				continue
			}
			seen[k] = true
			known = append(known, i)
		}
	}
	return w
}

// p2FirstToolTurnAfter is R4's re-attempt point: the first assistant turn after c that makes a
// tool call. An agent re-attempts an approach by calling a tool, so a turn with none is not a
// place a re-attempt can land.
func p2FirstToolTurnAfter(s eval.Session, c core.TurnIndex) (core.TurnIndex, bool) {
	for _, tn := range s.Turns {
		if tn.Index > c && tn.Role == "assistant" && len(tn.ToolCalls) > 0 {
			return tn.Index, true
		}
	}
	return 0, false
}

// p2AppendDepVersion records a NEW version of one dependency file, so that every record minted
// before this call now holds a hash store.ChangedSince reports as changed.
//
// The root is deterministic and synthetic: AppendFileVersion requires a non-zero root and nothing
// in the staleness path dereferences it, so a per-version digest is both sufficient and stable
// across runs.
func p2AppendDepVersion(ctx context.Context, t *testing.T, p *testutil.Project, s store.Store,
	file string, version map[string]int, turn core.TurnIndex,
) {
	t.Helper()
	version[file]++
	seed := fmt.Sprintf("%s@v%d", file, version[file])
	require.NoError(t, s.AppendFileVersion(ctx, file, store.FileVersion{
		TS:    core.UnixMilli(p.Clock.Now().UnixMilli()),
		Root:  core.HashBytes(core.DomainRoot, []byte(seed)),
		Turn:  turn,
		Bytes: int64(len(seed)),
	}))
}

// p2Result is one session's contribution to the metric.
type p2Result struct {
	Session     string
	Stock       int
	Negknow     int
	StaleBlocks int
	DepChange   bool
}

// p2Report is the plan's six-key report, written to t.TempDir() and logged so a CI run surfaces
// the numbers.
type p2Report struct {
	StockRepeats             int     `json:"stock_repeats"`
	NegknowRepeats           int     `json:"negknow_repeats"`
	ReductionPct             float64 `json:"reduction_pct"`
	StaleBlocks              int     `json:"stale_blocks"`
	Sessions                 int     `json:"sessions"`
	DependencyChangeSessions int     `json:"dependency_change_sessions"`
}

// p2NegknowSession runs the negknow branch of R4's walk over one session against a real ledger,
// returning the re-attempts that still happened and the stale-block incidents observed.
func p2NegknowSession(ctx context.Context, t *testing.T, s eval.Session, w p2Walk) (repeats, staleBlocks int) {
	t.Helper()

	p := testutil.NewProject(t)
	st := p.Store(t)
	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store:   st,
		Session: core.SessionID(s.ID),
		Clock:   p.Clock,
		Log:     p.Log,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })

	m, ok := led.(negknow.Maintainer)
	require.True(t, ok, "negknow.Open must return a Maintainer: IngestMCP is how a record_eliminated "+
		"MCP call reaches the ledger, and this walk models nothing else")

	// recordedAt maps a record id to the turn it was minted at, and writtenAt maps a dependency
	// path to the last turn it was written. Together they decide what "changed SINCE this record
	// was made" means, which is the only reading of a stale-block that is not trivially false: a
	// write that happened BEFORE a record is already baked into that record's depends_on hash and
	// has changed nothing about it.
	//
	// The comparison is at >= made, not at > made, and the walk order below is why: a re-attempt
	// re-records at step 1 and a dependency write lands at step 3, so a write at the SAME turn is
	// still a write that came after the mint and did change that record's evidence. The relaxed
	// predicate cannot false-positive either, because it is only ever reached for a record Query
	// answered AnswerActive with — and a record whose dependency really did change has been
	// flipped stale by the RefreshStaleness that follows the write, so Query cannot answer active
	// for it at all. No corpus session currently has an elimination turn coinciding with a
	// dependency write, so this is a correctness fix ahead of the corpus rather than a change to
	// today's numbers.
	recordedAt := map[string]core.TurnIndex{}
	writtenAt := map[string]core.TurnIndex{}
	version := map[string]int{}

	ingest := func(e p2Elimination, turn core.TurnIndex) {
		rec, warnings, ierr := m.IngestMCP(ctx, negknow.MCPArgs{
			Target:   e.target,
			Approach: e.approach,
			Reason:   e.reason,
			Scope:    string(negknow.ScopeSession),
		})
		require.NoError(t, ierr)
		require.Empty(t, warnings, "%s: an unresolved scope or dependency would change what was recorded", s.ID)
		require.Len(t, rec.DependsOn, len(p2DependencyFiles),
			"%s: R4 seeds both dependency files before turn 0, so every record must be guarded by both", s.ID)
		recordedAt[rec.ID] = turn
	}

	// Seed the two dependency files before turn 0, so every IngestMCP below resolves both.
	for _, f := range p2DependencyFiles {
		p2AppendDepVersion(ctx, t, p, st, f, version, 0)
	}

	for _, tn := range s.Turns {
		// A distinct timestamp per turn, so two records minted in one session are separately
		// addressable and pick() has a real ordering to work with.
		p.Clock.Advance(time.Second)

		// 1. The modelled re-attempts scheduled for this turn. A compaction's re-attempt point is
		//    strictly after the compaction, so this never races with step 2 for the same event.
		for _, i := range w.reattempt[tn.Index] {
			e := w.elims[i]
			ans, qerr := led.Query(ctx, e.target, e.approach, negknow.ScopeSession)
			require.NoError(t, qerr)

			if ans.State == negknow.AnswerActive {
				require.NotNil(t, ans.Record,
					"an active answer without a record is not an answer (§13 invariant 3)")
				made, minted := recordedAt[ans.Record.ID]
				require.True(t, minted, "%s: Query answered with a record this walk never minted", s.ID)
				for _, d := range ans.Record.DependsOn {
					if at, written := writtenAt[d.Path]; written && at >= made {
						staleBlocks++
						break
					}
				}
				continue
			}

			repeats++
			ingest(e, tn.Index)
		}

		// 2. The eliminations this turn records.
		for _, i := range w.elimAt[tn.Index] {
			ingest(w.elims[i], tn.Index)
		}

		// 3. The dependency writes this turn makes, each followed by a refresh.
		for _, f := range w.depWrite[tn.Index] {
			p2AppendDepVersion(ctx, t, p, st, f, version, tn.Index)
			writtenAt[paths.Key(f)] = tn.Index
			_, rerr := led.RefreshStaleness(ctx, st)
			require.NoError(t, rerr)
		}
	}
	return repeats, staleBlocks
}

// TestPhase2ExitCriterion is Qompack.md §10 Phase 2's exit criterion, made mechanical.
func TestPhase2ExitCriterion(t *testing.T) {
	ctx := context.Background()

	// The harness is constructed exactly as SP-02's driver constructs it in replayCorpus.
	base := testutil.NewProject(t)
	cfg := base.Cfg
	h := eval.New(eval.Options{Cfg: cfg, Log: logging.Nop()})

	sessions, err := h.Load(repoPath(t, "testdata/sessions/synthetic"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(sessions), cfg.Eval.MinSessions,
		"Appendix C's eval.minSessions is the floor below which a phase-gate replay is not credible")

	// The dependency-change derivation, cross-checked against the generator's own specs.
	specDepChange := map[string]bool{}
	for _, n := range eval.CorpusSpecs() {
		specDepChange[n.Shape+"-"+strconv.FormatInt(n.Seed, 10)] = len(n.Spec.DependencyChangeAt) > 0
	}

	var (
		results                  []p2Result
		stockRepeats             int
		negknowRepeats           int
		staleBlocks              int
		dependencyChangeSessions int
		improvedSessions         int
	)
	for _, s := range sessions {
		w := p2PlanWalk(t, s)

		want, known := specDepChange[s.ID]
		require.True(t, known, "session %s is not in eval.CorpusSpecs()", s.ID)
		require.Equal(t, want, w.depChange,
			"%s: a Write to a dependency file and SynthSpec.DependencyChangeAt must agree", s.ID)

		got, blocks := p2NegknowSession(ctx, t, s, w)

		stockRepeats += w.stock
		negknowRepeats += got
		if got < w.stock {
			improvedSessions++
		}
		if w.depChange {
			dependencyChangeSessions++
			// §10 Phase 2 restricts the stale-block criterion to sessions that include a
			// dependency change; a session with none cannot produce one by construction.
			staleBlocks += blocks
		}
		results = append(results, p2Result{
			Session: s.ID, Stock: w.stock, Negknow: got, StaleBlocks: blocks, DepChange: w.depChange,
		})
	}

	var lines strings.Builder
	for _, r := range results {
		fmt.Fprintf(&lines, "\n  %-26s stock=%3d negknow=%3d staleBlocks=%d depChange=%t",
			r.Session, r.Stock, r.Negknow, r.StaleBlocks, r.DepChange)
	}
	t.Logf("per-session repeated-elimination events:%s", lines.String())

	report := p2Report{
		StockRepeats:             stockRepeats,
		NegknowRepeats:           negknowRepeats,
		ReductionPct:             100 * float64(stockRepeats-negknowRepeats) / float64(stockRepeats),
		StaleBlocks:              staleBlocks,
		Sessions:                 len(sessions),
		DependencyChangeSessions: dependencyChangeSessions,
	}
	body, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "phase2-negknow.json")
	require.NoError(t, os.WriteFile(out, append(body, '\n'), 0o600))
	t.Logf("phase 2 negative-knowledge report (%s):\n%s", out, body)

	// Metric 1 — repeated-elimination events (§11.2's redundant work rate, the re-attempt half).
	require.Greater(t, stockRepeats, 0,
		"a corpus with no modelled re-attempts would make every assertion below vacuous")
	require.LessOrEqual(t, negknowRepeats, int(0.75*float64(stockRepeats)),
		"§10 Phase 2's \"measurable reduction\" is operationalized as at least 25%%")
	require.GreaterOrEqual(t, improvedSessions, 8,
		"a reduction carried by two outlier sessions is not a reduction in replay")

	// Metric 2 — stale-block incidents.
	//
	// WHAT THIS ZERO CLAIMS, AND WHAT IT DOES NOT (inherited constraint 2, SP05-D1). It is a
	// statement about THE LEDGER GIVEN THE EVENTS IT RECEIVED, not about the world. SP-05's IPC
	// drain is not lossless on abort: a drain stopped by idle-budget expiry consumes the line it
	// interrupted, so a PostToolUse event can be lost. A lost event never manufactures a FALSE
	// staleness flip, but it can produce a MISSED one — leaving a record active whose evidence did
	// in fact change, which is §12's High-severity direction and the one case §8.3's "false
	// positives are the safe direction" does not cover, because that claim is scoped to WHILE
	// EVIDENCE IS CURRENT. The replay corpus is deterministic and drops nothing, so this assertion
	// is structurally incapable of seeing that case and must not be read as excluding it.
	require.Zero(t, staleBlocks,
		"a viable approach wrongly refused: Query answered active for a record one of whose "+
			"depends_on files was written after the record was minted")
	require.Greater(t, dependencyChangeSessions, 0,
		"the stale-block criterion is asserted over dependency-change sessions, so there must be one")
}
