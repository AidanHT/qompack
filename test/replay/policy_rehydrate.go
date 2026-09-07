package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
	"github.com/qompack/qompack/internal/tokens"
)

// SP-11's replay arm: the L5 rehydrator as an eval.Policy, plus §10 Phase 3's exit criterion.
//
// WHY THIS IS NOT A _test.go FILE. `devtool replay` shells out to `go run ./test/replay`, and
// `go run` does not compile test files. A policy that lived only in a _test.go file would be
// invisible to the gate: the driver would report "no policy named qompack-rehydrate", the phase
// check would never run, and the suite would still be green. The policy and its phase check
// therefore live in the driver binary itself, and only their unit coverage lives beside them in
// phase3_rehydrate_test.go.
//
// NO SECOND STOCK POLICY IS REGISTERED HERE. internal/eval/policy.go already registers
// "stock" -> eval.NewStockPolicy, and that is the policy testdata/baseline/phase0.json describes.
// A second modelled host would fork the baseline's meaning.

// policyName is the registered name, written once.
const policyName = "qompack-rehydrate"

// checkpointSeqForReplay is the sequence number every synthesized checkpoint carries. A replay has
// one checkpoint per compaction event and no chain, so the number is a constant rather than a
// counter — nothing in Build reads it except the injection tag.
const checkpointSeqForReplay = core.CheckpointSeq(1)

// replayCheckpointCreated is the synthetic checkpoint's `created` stamp. It is fixed, not read off
// a clock: two runs of the same corpus must produce byte-identical output (§10 Phase 0's
// reproducibility criterion), and a wall-clock timestamp inside a rehydrated digest would defeat
// that on the first run.
const replayCheckpointCreated = "2026-01-01T00:00:00.000Z"

// pointerCap bounds how many file and tool pointers a synthesized checkpoint carries. §4.4's
// pointers-never-contents rule makes each one cheap, but an unbounded pointer list would make the
// tier-3 truncation the thing under measurement rather than the tier-1 guarantee A4 grades.
const pointerCap = 32

// residualReductionFloor is A3's second threshold: at least half the stock span must be gone.
const residualReductionFloor = 0.5

// multiCompactionMin is how many compaction events a session needs before A3's reduction ratio is
// asserted over it. §8.5's incrementally-advancing frontier only has anything to advance FROM once
// a second compaction happens; a single-compaction session's frontier is its first, so the ratio
// there measures the definition rather than the mechanism.
const multiCompactionMin = 2

// decisionMarkerRe matches the [decision:<id>] marker a synthesized turn's text mints. It is a
// local copy of internal/eval/blocks.go's own unexported decisionMarker: Rule W-3 forbids
// exporting one for this file's benefit, and the two are pinned together by
// TestBlockIndex_MatchesEvalBlockIDs, which fails the moment they disagree.
var decisionMarkerRe = regexp.MustCompile(`\[decision:([A-Za-z0-9_-]+)\]`)

// toolRecordEliminated is the tool name an elimination arrives under, transcribed for the same
// reason decisionMarkerRe is.
const toolRecordEliminated = "record_eliminated"

// roleUser is eval's own spelling of a user turn's Role.
const roleUser = "user"

// The drop-report Kinds rehydrate.Build names when it could not fit something. They are the join
// between "what Build dropped" and "which harness block id that was".
const (
	dropKindPointer     = "pointer"
	dropKindDecision    = "decision"
	dropKindElimination = "elimination"
)

// ── run-scoped accumulators ─────────────────────────────────────────────────────────────────
//
// Package-level state, deliberately. eval.Report sums rehydration_tokens ACROSS sessions, so the
// per-session maximum — which is what A1's second half and A4 are actually about — exists nowhere
// in the report and cannot be recovered from the sum. The driver runs the corpus twice through two
// freshly constructed harnesses but one process, so these accumulate over both passes; every
// assertion below is a max or a ratio, both of which are stable under that.
var (
	// maxRehydrationTokens is the largest Result.Tokens any single build produced.
	maxRehydrationTokens core.Tokens
	// builds counts every KeepSet call that reached rehydrate.Build.
	builds int
	// tier1Drops counts builds that lost a tier-1 item: no affordance line, or no user intent when
	// the checkpoint carried one.
	tier1Drops int
	// maxResidualTokens is the largest §8.5 residual span — Σ tokens over (F, at] — any build saw.
	maxResidualTokens core.Tokens
	// residualReductions holds 1 − residual/stockSpan for every MULTI-compaction session's events.
	residualReductions []float64
)

// resetRehydrateCounters clears the accumulators. It exists for the unit tests, which build several
// independent runs in one process; the driver never calls it.
func resetRehydrateCounters() {
	maxRehydrationTokens, builds, tier1Drops, maxResidualTokens = 0, 0, 0, 0
	residualReductions = nil
}

// ── the policy ──────────────────────────────────────────────────────────────────────────────

// qompackRehydratePolicy replays the L5 rehydrator as a keep-set decision: what survives a
// compaction under Qompack is exactly what the rehydrated digest re-injects.
type qompackRehydratePolicy struct {
	cfg  config.Config
	deps rehydrate.Deps
}

func (qompackRehydratePolicy) Name() string { return policyName }

func init() {
	eval.RegisterPolicy(policyName, func(c config.Config) eval.Policy {
		return qompackRehydratePolicy{
			cfg: c,
			// Graph, Rules and Skills are nil ON PURPOSE, and the choice is load-bearing rather
			// than convenient. A synthesized session has no dependence DAG, no .claude/ tree and no
			// skills directory, so any of the three would have to be faked — and a faked slice
			// score would decide item 3's ranking. A nil graph makes every slice score 0, which
			// fully determines §8.6's documented tiebreak (TS desc, then ID asc) and is what lets
			// KeepSet recompute the kept subset exactly rather than guessing at it.
			deps: rehydrate.Deps{
				Tokens: tokens.New(c, ""),
				Log:    logging.Nop(),
			},
		}
	})
}

// KeepSet is the four-step mapping from a logged session to a rehydrated keep-set.
func (p qompackRehydratePolicy) KeepSet(ctx context.Context, s eval.Session, at core.TurnIndex,
	budget core.Tokens,
) (eval.KeepSet, error) {
	if err := ctx.Err(); err != nil {
		return eval.KeepSet{}, err
	}

	idx := newBlockIndex(s, at)
	cp, fed := sessionCheckpoint(s, at, idx)

	res, err := rehydrate.Build(ctx, rehydrate.Request{
		Session:    core.SessionID(s.ID),
		Source:     "compact",
		Budget:     budget,
		Checkpoint: cp,
		Ref:        checkpoint.Ref{Seq: checkpointSeqForReplay, Frontier: frontierOf(s, at)},
		Cfg:        p.cfg,
	}, p.deps)
	if err != nil {
		return eval.KeepSet{}, fmt.Errorf("replay: %s at turn %d: %w", policyName, at, err)
	}

	recordBuild(cp, res)
	recordResidual(s, at)

	return eval.KeepSet{
		IDs:    survivors(p.cfg, fed, res.Dropped),
		Tokens: res.Tokens,
		// P is 0, and that is the same value eval.stockPolicy returns, for the same reason: a Full
		// Compact rewrites the whole message array, so p_min is 0 and the rewrite cost is w·n.
		// Holding P equal across both arms keeps the comparison about rehydration budget and
		// divergence rather than about a rewrite-cost artifact neither arm is being graded on.
		P: 0,
	}, nil
}

// recordBuild folds one build into the run-scoped accumulators.
//
// tier1Drops is the §8.6 tier-1 guarantee made countable: item 8 (the affordance line) must always
// be present, and item 2 (the verbatim user intent) must be present whenever the checkpoint
// carried one. Both are tier 1, which Truncate is forbidden to touch — so either one missing is a
// budget squeeze that reached content it may never reach.
func recordBuild(cp checkpoint.Checkpoint, res rehydrate.Result) {
	builds++
	if res.Tokens > maxRehydrationTokens {
		maxRehydrationTokens = res.Tokens
	}

	emitted := make(map[rehydrate.ItemKind]bool, len(res.Items))
	for _, it := range res.Items {
		emitted[it.Kind] = true
	}
	switch {
	case !emitted[rehydrate.ItemAffordance]:
		tier1Drops++
	case !emitted[rehydrate.ItemUserIntent] && cp.UserIntent.Original != "":
		tier1Drops++
	}
}

// recordResidual measures the synthetic replay model's §8.5 O1 span for one compaction event.
//
// residual is Σ tokens over the turns in (F, at] for frontierOf's model boundary. stockSpan is Σ
// tokens over the whole prefix, which is what a Full Compact rewrites. The ratio between them is
// replay arithmetic, not evidence that a durable writer committed F.
func recordResidual(s eval.Session, at core.TurnIndex) {
	residual, stockSpan := spans(s, at)
	if residual > maxResidualTokens {
		maxResidualTokens = residual
	}
	if len(s.CompactionAt) >= multiCompactionMin && stockSpan > 0 {
		residualReductions = append(residualReductions, 1-float64(residual)/float64(stockSpan))
	}
}

// spans returns the synthetic model's (residual, stockSpan) for a compaction at turn at.
func spans(s eval.Session, at core.TurnIndex) (residual, stockSpan core.Tokens) {
	f := frontierOf(s, at)
	for i := range s.Turns {
		turn := s.Turns[i]
		if turn.Index > at {
			break
		}
		stockSpan += turn.Tokens
		if turn.Index > f {
			residual += turn.Tokens
		}
	}
	return residual, stockSpan
}

// frontierOf is the synthetic replay model's frontier: the index of the last user turn at or
// before at.
//
// This is not a durable checkpoint frontier. checkpoint.Reader leaves Ref.Frontier zero because
// finalized checkpoint bytes and the frozen manifest do not record it. The model's last-user-turn
// boundary is only the deterministic input for this replay's A3 arithmetic: it represents closed
// work before an in-flight exchange, without claiming that a writer committed that boundary.
//
// It is deliberately NOT `at`. Defining F = at would make the residual identically zero, which
// would make A3 unfailable — a gate that cannot fail is not a gate. A LATER VERIFICATION ROUND
// may consume a real frontier only after a versioned durable seq-to-frontier record is introduced
// and Reader can recover it. That recovery is outside this synthetic replay model.
func frontierOf(s eval.Session, at core.TurnIndex) core.TurnIndex {
	f := core.TurnIndex(-1)
	for i := range s.Turns {
		turn := s.Turns[i]
		if turn.Index > at {
			break
		}
		if turn.Role == roleUser {
			f = turn.Index
		}
	}
	return f
}

// ── block-id resolution ─────────────────────────────────────────────────────────────────────

// blockIndex resolves the four identifier vocabularies a checkpoint speaks — path keys, tool_use
// ids, decision ids and elimination targets — into the harness's own block ids.
//
// eval.Replay satisfies a demand only when the keep-set literally contains that demand's BlockID,
// and those ids are prefixed forms (`file:<pathKey>`, `tu:<id>`, `dec:<id>`, `elim:<12 hex>`) minted
// by constructors internal/eval keeps unexported. elim: in particular is a domain-separated digest
// of (target, approach-class), which cannot be reconstructed from outside the package at all. So
// the ids are RESOLVED through the exported eval.Blocks, zipped against the session by the
// per-turn emission order Blocks documents, rather than re-derived.
type blockIndex struct {
	file map[string]string         // paths.Key(path)   -> file block ID
	tool map[core.ToolUseID]string // tool_use_id       -> tool block ID
	dec  map[string]string         // decision id       -> decision block ID
	elim map[string]string         // paths.Key(target) -> elimination block ID
}

// newBlockIndex builds the index from eval.Blocks(s, at).
//
// The zip is per (turn, kind), in emission order, which is exactly the order Blocks documents: for
// each turn it emits the turn's own message block, then one block per tool result in ToolCalls
// order, then one file block per path key first seen there, then one elimination block per
// record_eliminated call in ToolCalls order, then one decision block per [decision:] marker in
// text order. Two of the four are additionally self-identifying — a BlockFile carries
// Paths[0] already in paths.Key form, and a BlockElimination with a non-empty target does too — so
// those are read off the block and the zip only has to carry the other two.
func newBlockIndex(s eval.Session, at core.TurnIndex) blockIndex {
	idx := blockIndex{
		file: map[string]string{},
		tool: map[core.ToolUseID]string{},
		dec:  map[string]string{},
		elim: map[string]string{},
	}

	byTurn := map[core.TurnIndex]eval.Turn{}
	for _, turn := range s.Turns {
		byTurn[turn.Index] = turn
	}

	// Per-turn cursors into the session-side sequences the two non-self-identifying kinds zip
	// against.
	toolSeen := map[core.TurnIndex]int{}
	decSeen := map[core.TurnIndex]int{}
	elimSeen := map[core.TurnIndex]int{}

	for _, b := range eval.Blocks(s, at) {
		turn := byTurn[b.Turn]
		switch b.Kind {
		case eval.BlockToolResult:
			k := toolSeen[b.Turn]
			toolSeen[b.Turn] = k + 1
			if k < len(turn.ToolCalls) {
				idx.tool[turn.ToolCalls[k].ID] = b.ID
			}

		case eval.BlockFile:
			if len(b.Paths) > 0 {
				idx.file[b.Paths[0]] = b.ID
			}

		case eval.BlockElimination:
			k := elimSeen[b.Turn]
			elimSeen[b.Turn] = k + 1
			if len(b.Paths) > 0 {
				idx.elim[b.Paths[0]] = b.ID
				continue
			}
			// A record_eliminated call with an empty target mints a block with no Paths; the zip
			// is what identifies it.
			if tc, ok := nthEliminationCall(turn, k); ok {
				idx.elim[paths.Key(eliminationTarget(tc))] = b.ID
			}

		case eval.BlockDecision:
			ids := decisionIDsOf(turn.Text)
			k := decSeen[b.Turn]
			decSeen[b.Turn] = k + 1
			if k < len(ids) {
				idx.dec[ids[k]] = b.ID
			}

		case eval.BlockUserPrompt, eval.BlockAssistant:
			// Turn blocks are harness block ids too, but eval.Demands never emits one, so keeping
			// or dropping them cannot change what a keep-set satisfies. They are left out so
			// KeepSet.IDs is exactly the set the drop report can speak about.
		}
	}
	return idx
}

// nthEliminationCall returns the k-th record_eliminated call of turn.
func nthEliminationCall(turn eval.Turn, k int) (eval.ToolCall, bool) {
	n := 0
	for _, tc := range turn.ToolCalls {
		if tc.Name != toolRecordEliminated {
			continue
		}
		if n == k {
			return tc, true
		}
		n++
	}
	return eval.ToolCall{}, false
}

// elimArgs is the {"target","approach","reason"} payload a record_eliminated call carries.
type elimArgs struct {
	Target   string `json:"target"`
	Approach string `json:"approach"`
	Reason   string `json:"reason"`
}

// eliminationTarget reads a record_eliminated call's target.
func eliminationTarget(tc eval.ToolCall) string { return decodeElimArgs(tc).Target }

// decodeElimArgs decodes a record_eliminated call's arguments, tolerating an absent or malformed
// payload the way internal/eval does: an unreadable argument blob yields empty strings, never an
// error, because a corpus fixture that cannot be parsed must not take the whole run down.
func decodeElimArgs(tc eval.ToolCall) elimArgs {
	var a elimArgs
	if len(tc.Args) == 0 {
		return a
	}
	_ = json.Unmarshal(tc.Args, &a)
	return a
}

// decisionIDsOf returns every decision id minted in text, in order of appearance.
func decisionIDsOf(text string) []string {
	if !strings.Contains(text, "[decision:") {
		return nil
	}
	m := decisionMarkerRe.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, g[1])
	}
	return out
}

// ── the synthesized checkpoint ──────────────────────────────────────────────────────────────

// fedIn is everything the checkpoint handed to Build, paired with the block id each item resolves
// to. It is what step 3 subtracts the drop report from.
//
// elims is ORDERED, and the order is the one Build's item 3 ranks under: with a nil graph every
// slice score is 0, so the documented tiebreak (TS desc, then ID asc) fully determines it. That is
// what makes the single collapsed elimination drop entry — which names no id at all — recoverable.
type fedIn struct {
	pointers  map[string]string // pointer id (path key or tool_use_id) -> block ID
	decisions map[string]string // decision id -> block ID
	elims     []fedElimination
}

// fedElimination is one elimination in item 3's ranking order.
type fedElimination struct {
	id    string         // the negknow record id, as the drop report would name it
	block string         // the harness's elimination block ID
	ts    core.UnixMilli // the recording turn's timestamp, which is the primary ranking key
}

// sessionCheckpoint builds the checkpoint a compaction at turn at would have written.
//
// It is assembled BY FIELD ASSIGNMENT, never through checkpoint.Writer: Begin/Advance/Finalize
// belong to a same-wave sibling (Rule W-2) and calling them from here would couple this gate to an
// implementation that does not exist yet. Everything it carries comes from the logged session.
//
// THE PREFIX IS [0, at), NOT [0, at]. eval.Blocks(s, at) emits blocks for turns strictly before at
// — clampTurn(upTo) then `for i := range end` — and eval.stockPolicy walks the same half-open
// range. A checkpoint carrying content from turn at itself would therefore feed Build items no
// block id exists for, which survivors() would silently drop and which would read as a rehydration
// that preserved less than it did. The residual span is the one quantity measured over (F, at]
// inclusive, because that is the span a summarizer at turn at would actually have to read.
func sessionCheckpoint(s eval.Session, at core.TurnIndex, idx blockIndex) (checkpoint.Checkpoint, fedIn) {
	fed := fedIn{pointers: map[string]string{}, decisions: map[string]string{}}

	cp := checkpoint.Checkpoint{
		Version: checkpoint.SchemaVersion,
		Session: core.SessionID(s.ID),
		Seq:     checkpointSeqForReplay,
		Created: replayCheckpointCreated,
		// Invariants are empty: a synthesized session pins nothing, and inventing a pin would put
		// tier-1 content into the measurement that no logged turn produced.
		Invariants: nil,
	}

	cp.UserIntent = checkpoint.UserIntent{Original: firstUserText(s)}
	cp.Eliminated, fed.elims = eliminationsUpTo(s, at, idx)
	cp.Decisions = decisionsUpTo(s, at, idx, fed.decisions)
	cp.CurrentWork = currentWork(s, at)
	cp.Pointers = pointersUpTo(s, at, idx, fed.pointers)
	cp.SketchRefs = map[string]string{}

	return cp, fed
}

// firstUserText is the verbatim original intent: §8.6 item 2, which G2.3 forbids regenerating from
// a summary. In a logged session it is simply the first user turn.
func firstUserText(s eval.Session) string {
	for _, turn := range s.Turns {
		if turn.Role == roleUser && turn.Text != "" {
			return turn.Text
		}
	}
	return ""
}

// eliminationsUpTo collects every record_eliminated call BEFORE at (the half-open prefix
// eval.Blocks mints ids over) and pairs each with the harness block id it resolves to.
func eliminationsUpTo(s eval.Session, at core.TurnIndex, idx blockIndex) ([]negknow.Record, []fedElimination) {
	var recs []negknow.Record
	var fed []fedElimination

	for _, turn := range s.Turns {
		if turn.Index >= at {
			break
		}
		for _, tc := range turn.ToolCalls {
			if tc.Name != toolRecordEliminated {
				continue
			}
			a := decodeElimArgs(tc)
			id := string(tc.ID)
			if id == "" {
				id = fmt.Sprintf("elim_%d_%s", int(turn.Index), paths.Key(a.Target))
			}
			recs = append(recs, negknow.Record{
				ID:       id,
				Session:  core.SessionID(s.ID),
				TS:       turn.TS,
				Target:   a.Target,
				Approach: a.Approach,
				Reason:   a.Reason,
				Scope:    negknow.ScopeSession,
				Status:   negknow.StatusActive,
			})
			fed = append(fed, fedElimination{id: id, block: idx.elim[paths.Key(a.Target)], ts: turn.TS})
		}
	}

	// Item 3's ranking order, reproduced: slice score first, then TS descending, then ID ascending.
	// With a nil Deps.Graph every slice score is 0, so the first key is constant and the documented
	// tiebreak is the WHOLE order — which is exactly why the graph is nil (see init). Reproducing
	// it here is what makes the single collapsed elimination drop entry, which names no id at all,
	// mean a determinate set rather than a guess.
	sort.SliceStable(fed, func(i, j int) bool {
		if fed[i].ts != fed[j].ts {
			return fed[i].ts > fed[j].ts
		}
		return fed[i].id < fed[j].id
	})
	return recs, fed
}

// decisionsUpTo collects every [decision:<id>] marker in the half-open prefix [0, at).
func decisionsUpTo(s eval.Session, at core.TurnIndex, idx blockIndex, into map[string]string) []checkpoint.Decision {
	var out []checkpoint.Decision
	for _, turn := range s.Turns {
		if turn.Index >= at {
			break
		}
		for _, id := range decisionIDsOf(turn.Text) {
			out = append(out, checkpoint.Decision{
				ID:   core.DecisionID(id),
				What: turn.Text,
				Why:  turn.Text,
				Turn: turn.Index,
			})
			if block, ok := idx.dec[id]; ok {
				into[id] = block
			}
		}
	}
	return out
}

// currentWork is §8.6 item 5, taken from the last user turn before at: what the session was asked
// to do most recently is what it is still doing.
func currentWork(s eval.Session, at core.TurnIndex) checkpoint.CurrentWork {
	goal := ""
	for _, turn := range s.Turns {
		if turn.Index >= at {
			break
		}
		if turn.Role == roleUser && turn.Text != "" {
			goal = turn.Text
		}
	}
	return checkpoint.CurrentWork{Goal: goal}
}

// pointersUpTo builds §4.4's pointers over the half-open prefix [0, at) — paths and tool_use ids
// with a one-line reason, never content — most recent first, capped at pointerCap per kind.
func pointersUpTo(s eval.Session, at core.TurnIndex, idx blockIndex, into map[string]string) checkpoint.Pointers {
	var ptr checkpoint.Pointers

	// Files, most recent first: the block index already holds exactly the path keys eval.Blocks
	// minted a file block for, so iterating it cannot invent a pointer the harness has no id for.
	fileKeys := make([]string, 0, len(idx.file))
	for key := range idx.file {
		fileKeys = append(fileKeys, key)
	}
	sort.Strings(fileKeys)
	for _, key := range fileKeys {
		if len(ptr.Files) == pointerCap {
			break
		}
		ptr.Files = append(ptr.Files, checkpoint.FilePointer{
			Path: key,
			Why:  "read during this session; re-read rather than restore (§4.4)",
		})
		into[key] = idx.file[key]
	}

	// Tools, most recent first: the newest results are the ones a continuation is most likely to
	// reach back for, which is the whole reason the pointer tier is ordered at all.
	for i := len(s.Turns) - 1; i >= 0 && len(ptr.Tools) < pointerCap; i-- {
		turn := s.Turns[i]
		if turn.Index >= at {
			continue
		}
		for k := len(turn.ToolCalls) - 1; k >= 0 && len(ptr.Tools) < pointerCap; k-- {
			tc := turn.ToolCalls[k]
			block, ok := idx.tool[tc.ID]
			if !ok {
				continue
			}
			ptr.Tools = append(ptr.Tools, checkpoint.ToolPointer{
				ToolUseID: tc.ID,
				Summary:   tc.Name + " result; expand to retrieve",
			})
			into[string(tc.ID)] = block
		}
	}
	return ptr
}

// ── step 3: what survived ───────────────────────────────────────────────────────────────────

// survivors maps what Build emitted back onto harness block ids: everything fed in, minus what the
// drop report names.
//
// The three drop shapes are not symmetric, and the asymmetry is the whole reason this function
// exists rather than a set intersection. A pointer or a decision is dropped BY ID. Eliminations
// are dropped as ONE collapsed entry naming no id at all — item 3 renders the top-N and a single
// "and the rest are in already_tried" line — so the collapsed entry means "everything after the
// first eliminationsTopN, in the order step 2 fixed".
func survivors(cfg config.Config, fed fedIn, dropped []checkpoint.DropEntry) []string {
	droppedPointer := map[string]bool{}
	droppedDecision := map[string]bool{}
	droppedElim := map[string]bool{}
	elimsCollapsed := false

	for _, d := range dropped {
		switch d.Kind {
		case dropKindPointer:
			droppedPointer[d.ID] = true
		case dropKindDecision:
			droppedDecision[d.ID] = true
		case dropKindElimination:
			if d.ID == "" {
				elimsCollapsed = true
				continue
			}
			// A named elimination drop is an ordinary by-id removal.
			droppedElim[d.ID] = true
		}
	}

	kept := map[string]bool{}
	for id, block := range fed.pointers {
		if block != "" && !droppedPointer[id] {
			kept[block] = true
		}
	}
	for id, block := range fed.decisions {
		if block != "" && !droppedDecision[id] {
			kept[block] = true
		}
	}

	topN := cfg.Runtime.Rehydrate.EliminationsTopN
	for i, e := range fed.elims {
		if e.block == "" || droppedElim[e.id] {
			continue
		}
		if elimsCollapsed && i >= topN {
			continue
		}
		kept[e.block] = true
	}

	out := make([]string, 0, len(kept))
	for id := range kept {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ── §10 Phase 3's exit criterion ────────────────────────────────────────────────────────────

// phase3 is Qompack.md §10 Phase 3's exit criterion, made mechanical: the rehydrated context is
// cheaper than the stock summary, it diverges later, its residual span is what O1 promised, and it
// never drops a tier-1 item to get there.
//
// A NOTE ON THE SPAN INSTRUCTION. §8.5's focus instruction is advisory: the host may ignore it,
// and there is no observable that says whether it did. Compliance is therefore MODELLED, by
// eval.ReplayOptions{Deterministic: true} — every replay in this gate assumes the logged action
// sequence, which is the compliant case. Non-compliance is explicitly out of scope, and it is out
// of scope for a reason rather than by omission: the checkpoint remains authoritative whether or
// not the summarizer cooperated, which is exactly what
// TestE2E_SessionStartCompactAfterFailedSummary asserts against the real binary.
func phase3(c Context) error {
	q, ok := c.Driver.Policies[policyName]
	if !ok {
		return fmt.Errorf(
			"phase 3: no %q policy in the report; add it to --policies, since the phase-3 number "+
				"IS the rehydrator's behaviour by definition", policyName)
	}
	stock, ok := c.Driver.Policies[baselinePolicyName]
	if !ok {
		return fmt.Errorf("phase 3: no %q policy in the report to compare against", baselinePolicyName)
	}

	// A4 is reported on PASS as well as failure: a tier-1 guarantee whose margin is invisible is a
	// guarantee nobody can see eroding.
	fmt.Printf("phase 3: tier-1 drops %d/%d builds; max rehydration %d tokens; max residual %d tokens\n",
		tier1Drops, builds, int(maxRehydrationTokens), int(maxResidualTokens))

	if err := phase3Budget(c, q, stock); err != nil {
		return err
	}
	if err := phase3Divergence(c, q, stock); err != nil {
		return err
	}
	if err := phase3Residual(c); err != nil {
		return err
	}
	return phase3Tier1()
}

// phase3Budget is A1: the rehydrated context costs fewer tokens than the stock summary, and no
// single build exceeded runtime.rehydrate.maxTokens.
//
// It reads maxRehydrationTokens rather than c.Driver.BudgetViolations deliberately: that field
// fires on eval.DefaultKeepBudget (40 000) and already fails the whole run elsewhere, so it can
// never make A1 fail independently — a gate whose condition is subsumed by another gate is
// decoration.
func phase3Budget(c Context, q, stock map[string]float64) error {
	const metric = "rehydration_tokens"
	if q[metric] >= stock[metric] {
		return fmt.Errorf(
			"phase 3 (A1 budget): %s spent %.0f rehydration tokens against %s's %.0f; "+
				"the rehydrated context has to be CHEAPER than the summary it replaces",
			policyName, q[metric], baselinePolicyName, stock[metric])
	}

	hardCap := core.Tokens(c.Cfg.Runtime.Rehydrate.MaxTokens)
	if maxRehydrationTokens > hardCap {
		return fmt.Errorf(
			"phase 3 (A1 budget): one rehydration produced %d tokens, over "+
				"runtime.rehydrate.maxTokens (%d)", int(maxRehydrationTokens), int(hardCap))
	}
	return nil
}

// phase3Divergence is A2: the rehydrated arm diverges from the uncompacted baseline LATER than the
// stock arm, and the stock arm itself has not moved against the committed Phase 0 number.
//
// The conjunction is the point. "qompack diverges later than stock" is satisfiable by making stock
// worse, so the same invocation's stock number is judged against testdata/baseline/phase0.json
// under the §11.3 2% rule before the comparison is allowed to count.
func phase3Divergence(c Context, q, stock map[string]float64) error {
	const metric = "first_divergence_turn"
	if q[metric] <= stock[metric] {
		return fmt.Errorf(
			"phase 3 (A2 divergence): %s first diverged at turn %.0f, no later than %s's %.0f; "+
				"a rehydration that does not delay divergence has not preserved anything",
			policyName, q[metric], baselinePolicyName, stock[metric])
	}

	// The gate's own compare step runs AFTER the phase checks (main.go), so c.Driver.Regressions is
	// empty here by construction and the baseline is re-read rather than borrowed.
	root, err := repoRoot()
	if err != nil {
		return fmt.Errorf("phase 3 (A2 divergence): %w", err)
	}
	base, err := loadBaseline(resolve(root, defaultBaselinePath), root)
	if err != nil {
		return fmt.Errorf(
			"phase 3 (A2 divergence): the stock arm must be shown not to have regressed, and the "+
				"committed baseline could not be read: %w", err)
	}
	if r, regressed := judge(baselinePolicyName, metric, base.Policies[baselinePolicyName][metric],
		stock[metric], ""); regressed {
		return fmt.Errorf(
			"phase 3 (A2 divergence): %s.%s moved from %.6f to %.6f (%+.2f%%), past the §11.3 2%% "+
				"rule; %s cannot be judged against a baseline arm that has itself moved",
			baselinePolicyName, metric, r.Baseline, r.Observed, r.DeltaPct, policyName)
	}
	return nil
}

// phase3Residual is A3: §8.5's O1 span.
//
// THE NUMBERS ARE THE POLICY'S OWN, not the report's residual_span_p95, and the reason is
// arithmetic rather than preference. eval.Replay derives Run.ResidualSpan as n − KeepSet.P, and
// both arms return P = 0 (see KeepSet), so residual_span_p95 is n for both and carries no
// information about the frontier at all. The residual A3 is about is Σ tokens over (F, at], which
// only the policy is in a position to measure, so it measures it there and this reads it back.
func phase3Residual(c Context) error {
	limit := core.Tokens(c.Cfg.Checkpoint.Frontier.MaxResidualTokens)
	if maxResidualTokens > limit {
		return fmt.Errorf(
			"phase 3 (A3 O1 span): the largest residual span was %d tokens, over "+
				"checkpoint.frontier.maxResidualTokens (%d); past that the frontier has not "+
				"advanced and a full pass is forced",
			int(maxResidualTokens), int(limit))
	}
	for i, got := range residualReductions {
		if got < residualReductionFloor {
			return fmt.Errorf(
				"phase 3 (A3 O1 span): compaction event %d of a multi-compaction session reduced "+
					"the rewritten span by %.3f, under the %.2f floor",
				i, got, residualReductionFloor)
		}
	}
	return nil
}

// phase3Tier1 is A4: tier 1 is never truncated. §6.9 orders truncation tier 3, then tier 2, never
// tier 1 — so a single tier-1 drop at the default budget is a budgeting bug, not a tight fit.
func phase3Tier1() error {
	if tier1Drops != 0 {
		return fmt.Errorf(
			"phase 3 (A4 tier-1 intact): %d of %d builds dropped a tier-1 item; §6.9 truncates "+
				"tier 3 first, then tier 2, and never tier 1", tier1Drops, builds)
	}
	return nil
}
