// Package l3policy is the eval.Policy named "qompack-l3": Qompack's L3 scheduler replayed through
// SP-02's Phase 4 harness with no daemon, no store and no I/O.
//
// Imports: internal/core, internal/config, internal/eval, internal/paths, internal/scheduler — and
// NOT internal/daemon, which is a composition root nothing may import (00-ARCHITECTURE §3.2).
// That is precisely why the drop classification lives in scheduler as DropClassOf, and why the
// policy re-derives from the logged session everything the daemon would read off its store.
//
// # What is modelled, and how
//
// Positions. Every token position — the candidates' Pos, the chosen P, the context size n — is
// read from eval.Blocks: a turn's cut point is the Pos of its message block, and n is the sum of
// the position-advancing blocks, which is exactly the prefixTokens the harness subtracts P from.
// The plan's shorthand "running sum of Turn.Tokens" is not that coordinate (Blocks also advances
// on every tool result's own weight), and a P measured in a different coordinate from n would
// make residual = n − P a fiction.
//
// Droppable results. A logged tool result is droppable when scheduler.DropClassOf says so for its
// tool name, its Ephemeral flag, and a supersession derived from the log: a result is superseded
// when every path it touched is touched again by a later call in the same prefix (§8.1 item 3).
// eval.Blocks never sets Block.Ephemeral today, so DropEphemeral is unreachable in replay and
// "the daemon and the policy classify identically" is exercised on the tool-name and
// supersession inputs alone.
// reclaimable(p) is the sum of the droppable results' weights at or after p, using the harness's
// own per-result weight (the result's tokens field, else len/4) rather than splitting a turn's
// tokens across its calls — the harness exposes the weight directly, so approximating it would
// only put KeepSet.Tokens and reclaimable(p) in two coordinates.
//
// Coupling. distortion(p) needs segment_coupling(p); with no dependence DAG it is approximated by
// the number of logged path-sharing call pairs that straddle p — for each path key, the calls
// before p times the calls at or after it.
//
// Features. The same scheduler.NewBOCD detector the daemon uses is fed ONE observation per
// tool-bearing turn: PathJaccard and ToolShift over two featureWindow-turn windows of the
// turns' ToolCall.Paths/.Name (the plan's FeaturesFrom definitions), GapSeconds from Turn.TS
// since the previous tool-bearing turn, TodoTransition from a logged TodoWrite completion or a
// test:pass action, LexicalCohesion 0 (not in the default feature list). That is NOT the
// daemon's cadence, and the difference is deliberate (ruling R57). The daemon observes once per
// PostToolUse record (the ObserveTool tap) AND once per main-agent Stop (the ObserveStop tap),
// the Stop pushing an EMPTY observation — no paths, no tool, no text — into the same windows
// (observeStop, scheduler_runtime.go; the Stop observation is plan-mandated by the tap table),
// so a live stream carries about twice the observations of this one, its windows of eight hold
// four tool observations in a one-call-per-turn session, and its `time` stream alternates
// between the think gap and the generation gap. Mirroring that stream here — one observation
// per ToolCall, then one empty observation per non-user turn, both stamped Turn.TS because the
// log has no per-hook clock — was tried in the final fix round and REVERTED under R57's revert
// clause: on this corpus it moved the Phase 4 rewrite ratio from 0.7059 to 0.8227 (bar 0.80),
// redundant_reads from 7 to 27, and hit 3 of the corpus's 105 generator boundaries where this
// cadence hits 24 (ADR 0012, "Phase 4 measurement"). Every Phase 4 figure is therefore a
// REPLAY-CADENCE figure: what the detector does under the daemon's real cadence is unmeasured
// on this branch and is V4-VERIFY §4.4's live test to measure. The detector is never Reset at a
// logged compaction.
//
// Idle-gap anchors. The policy measures idle gaps in two places, and they are different
// quantities on purpose: the detector's GapSeconds is the time since the previous TOOL-BEARING
// turn (a user turn, or an assistant turn without a call, is not an observation), while
// Evaluate's TTL gap is Now − LastAPICallTS, the compaction turn's stamp minus the immediately
// previous turn's whatever its role, and LastRequestStartTS stays 0 so ClassifyTTL anchors on
// exactly that. The daemon anchors both on hook events — rec.TS for the detector, NotifyActivity
// from every seam for the API-call clock — so a user prompt moves its TTL anchor and not its
// detector gap, which is the same split. It is what keeps the shape session's 3 600 s gap
// visible to the detector at the first call after it.
//
// Candidates. §8.4: changepoint boundaries ∩ API-round boundaries. Every turn the detector
// declares a changepoint at is a boundary, every non-user turn is an API round (§2.6), and the
// highest-Pos maxCandidates of the intersection are kept, as the daemon keeps them. Candidates
// exposes the set so the harness can check the cut against it. With no candidate there is no cut:
// Qompack does nothing and the host's own compaction runs, so the keep-set is stock's.
//
// Cache state. Deterministic replay has no environment, so the regime is the KNOWN five-minute
// one — the ladder run under FORCE_PROMPT_CACHING_5M alone, every number from cfg.Cache. Now is
// the timestamp of the turn the compaction precedes (the host compacts on submit), LastAPICallTS
// is the previous turn's, LastCompactionTS is the previous compaction point's or the session
// start's, and the Young–Daly δ stays nil: replay measures no compaction cost.
//
// The trigger. A logged compaction is the host's own auto-compact, and by Qompack.md §2.5 the host
// fires at effectiveContextWindow − HostAutoCompactBuffer; the policy therefore models the window
// as n + HostAutoCompactBuffer, so Evaluate sees the context the host saw and hard_ceiling fires
// where the host fired. Whether to compact is the log's decision; where to cut is Evaluate's.
//
// The keep-set. Everything before P is kept at no cost — it is the cached prefix, which is the
// whole point of p-selection (§5.2). After P the tail is what the checkpoint replaces, and it
// retains what Qompack.md says a checkpoint carries: every result §2.2 preserves (DropNone) with
// the file block it produced, every decision and elimination block (§8.5 tiers 1–2), and a §8.5
// tier-3 pointer tier — the file blocks of the tail's most recently touched paths, most recent
// first, within the budget the harness hands every policy (the budget bounds this tier; the
// preserved results are not a choice it can take back). Droppable results are dropped: that is
// what reclaimable(p) reclaims. Message blocks after P are what the summary replaces. Tokens is
// the retained tail — what the next turn rehydrates — which is the quantity the harness prices.
//
// l3policy owns NO pause model and declares NO latency constants. Deterministic replay makes no
// model call, so a wall-clock pause cannot be measured; §8.5 says decode dominates and decode
// length tracks the residual span, so the harness MODELS the pause linearly from the residual and
// labels it modelled everywhere it is reported (eval.LatencyModel.Modelled). A second copy of
// those coefficients in a package that cannot apply them is exactly the drifting duplicate this
// plan forbids elsewhere.
package l3policy

import (
	"bytes"
	"context"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/scheduler"
)

const (
	// policyName is the name the eval registry and the driver's --policies flag know this by.
	policyName = "qompack-l3"

	// featureWindow is the plan's window: two 8-turn windows per feature vector.
	featureWindow = 8
	// maxCandidates mirrors the daemon's cap (Qompack.md §5.4: "Twenty candidates, not 167,000"):
	// the highest-Pos boundaries are kept, because an early boundary can only win when the cache
	// is cold, and then even the 32nd-latest one is a deep cut relative to n.
	maxCandidates = 32

	// roleUser is the one eval.Turn role that is not an assistant round (eval treats every other
	// role as occupying the assistant side).
	roleUser = "user"

	// The two G1.5 safe-point signals TodoTransition is derived from in a log.
	toolTodoWrite       = "TodoWrite"
	todoCompletedMarker = `"completed"`
	testPassMarker      = "test:pass"

	// forceFiveMinuteEnv is the one documented host variable deterministic replay sets.
	forceFiveMinuteEnv = "FORCE_PROMPT_CACHING_5M"

	millisPerSecond = 1000.0
	millisPerMinute = 60_000.0
)

func init() { eval.RegisterPolicy(policyName, New) }

// policy is the eval.Policy. It is immutable after New: KeepSet reads cfg, regime and the stock
// fallback and keeps no per-call state, so the same session and turn always produce the same
// KeepSet.
type policy struct {
	cfg    config.Config
	regime scheduler.CacheRegime
	stock  eval.Policy
}

// New returns the eval.Policy named "qompack-l3". It replays a logged session through
// scheduler.Evaluate with no daemon, no store and no I/O: candidates and reclaimable tokens are
// derived from the session's own logged tool calls via scheduler.DropClassOf, and segment coupling
// is approximated by counting logged path-sharing pairs that straddle p.
func New(cfg config.Config) eval.Policy {
	return &policy{
		cfg: cfg,
		regime: scheduler.ResolveCacheRegime(replayEnv, cfg.Scheduler, "", false,
			cfg.Runtime.Scheduler.Cache.AssumeMaxTTLSeconds),
		stock: eval.NewStockPolicy(cfg),
	}
}

// replayEnv is the environment deterministic replay runs under: nothing but the documented
// FORCE_PROMPT_CACHING_5M, so the ladder resolves the KNOWN five-minute regime from cfg.Cache.
func replayEnv(name string) string {
	if name == forceFiveMinuteEnv {
		return "1"
	}
	return ""
}

func (p *policy) Name() string { return policyName }

// KeepSet chooses P through scheduler.Evaluate and keeps the prefix before it plus what the
// checkpoint retains after it. With no eligible candidate, or no cut, it answers as stock does.
// Deterministic: no clock, no randomness.
func (p *policy) KeepSet(ctx context.Context, s eval.Session, at core.TurnIndex, budget core.Tokens) (eval.KeepSet, error) {
	if err := ctx.Err(); err != nil {
		return eval.KeepSet{}, err
	}
	end := clampTurn(at, len(s.Turns))
	pf := indexPrefix(s, end)
	det := p.detect(s, end)
	cands := pf.candidates(s, det.turns)
	if len(cands) == 0 {
		return p.stock.KeepSet(ctx, s, at, budget)
	}
	d := scheduler.Evaluate(p.inputs(s, end, pf, det.state, cands))
	if !d.ShouldCompact {
		return p.stock.KeepSet(ctx, s, at, budget)
	}
	ids, tokens := pf.keep(d.P.Pos, budget)
	return eval.KeepSet{IDs: ids, Tokens: tokens, P: d.P.Pos}, nil
}

// Candidates returns the eligible candidate set the policy hands scheduler.Evaluate at the
// compaction preceding turn at: the detector's declared changepoint turns that are API-round
// boundaries, on their message blocks' Pos, capped to the highest-Pos maxCandidates. Empty means
// the policy makes no cut there.
func Candidates(cfg config.Config, s eval.Session, at core.TurnIndex) []scheduler.Candidate {
	p, ok := New(cfg).(*policy)
	if !ok {
		return nil
	}
	end := clampTurn(at, len(s.Turns))
	pf := indexPrefix(s, end)
	return pf.candidates(s, p.detect(s, end).turns)
}

// clampTurn bounds at to [0, n].
func clampTurn(at core.TurnIndex, n int) int {
	return min(max(int(at), 0), n)
}

// ── the prefix index ────────────────────────────────────────────────────────────────────────

// toolEntry is one logged tool result in the prefix, joined to the ToolCall it came from.
type toolEntry struct {
	block     eval.Block
	name      string   // the ToolCall's Name
	keys      []string // paths.Key of every path the call touched
	droppable bool
}

// prefixIndex is everything KeepSet needs about Blocks(s, at): the harness's n, each turn's cut
// point, and the droppable classification of every tool result.
type prefixIndex struct {
	blocks    []eval.Block
	n         int
	turnStart []int // turnStart[t] is the Pos of turn t's message block
	tools     []toolEntry
	droppable map[string]bool       // tool-result block ID → droppable
	fileBlock map[string]eval.Block // path key → the file block Blocks minted for it
	// lastTouch[key] is the highest Pos of a tool result touching key: the recency the pointer
	// tier orders by.
	lastTouch map[string]int
}

// indexPrefix builds the index over turns [0, end).
func indexPrefix(s eval.Session, end int) *prefixIndex {
	blocks := eval.Blocks(s, core.TurnIndex(end))
	pf := &prefixIndex{
		blocks:    blocks,
		turnStart: make([]int, end),
		droppable: make(map[string]bool),
		fileBlock: make(map[string]eval.Block),
		lastTouch: make(map[string]int),
	}

	// Blocks emits one BlockToolResult per ToolCall, in ToolCalls order, so the k-th tool block of
	// turn t is s.Turns[t].ToolCalls[k]: no knowledge of eval's ID grammar is needed.
	nextCall := make([]int, end)
	for _, b := range blocks {
		switch b.Kind {
		case eval.BlockUserPrompt, eval.BlockAssistant:
			pf.n += int(b.Tokens)
			pf.turnStart[int(b.Turn)] = b.Pos
		case eval.BlockToolResult:
			pf.n += int(b.Tokens)
			t := int(b.Turn)
			calls := s.Turns[t].ToolCalls
			k := nextCall[t]
			nextCall[t]++
			e := toolEntry{block: b}
			if k < len(calls) {
				e.name = calls[k].Name
				e.keys = pathKeys(calls[k].Paths)
			}
			for _, key := range e.keys {
				pf.lastTouch[key] = max(pf.lastTouch[key], b.Pos)
			}
			pf.tools = append(pf.tools, e)
		case eval.BlockFile:
			if len(b.Paths) > 0 {
				pf.fileBlock[b.Paths[0]] = b
			}
		}
	}

	// Supersession from the log: a result is superseded when every path it touched is touched
	// again by a later call in the same prefix (§8.1 item 3).
	lastIndex := make(map[string]int)
	for i, e := range pf.tools {
		for _, k := range e.keys {
			lastIndex[k] = i
		}
	}
	for i := range pf.tools {
		e := &pf.tools[i]
		superseded := len(e.keys) > 0
		for _, k := range e.keys {
			if lastIndex[k] <= i {
				superseded = false
				break
			}
		}
		e.droppable = scheduler.DropClassOf(e.name, e.block.Ephemeral, superseded) != scheduler.DropNone
		pf.droppable[e.block.ID] = e.droppable
	}
	return pf
}

// pathKeys normalizes a call's paths the way Blocks keys its file blocks, dropping empties.
func pathKeys(ps []string) []string {
	if len(ps) == 0 {
		return nil
	}
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if k := paths.Key(p); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// reclaimAfter is reclaimable(p): the droppable results' weights at or after pos.
func (pf *prefixIndex) reclaimAfter(pos int) core.Tokens {
	var sum core.Tokens
	for _, e := range pf.tools {
		if e.droppable && e.block.Pos >= pos {
			sum += e.block.Tokens
		}
	}
	return sum
}

// couplingAt approximates segment_coupling(p) by the logged path-sharing call pairs straddling
// pos: for each path key, the calls before pos times the calls at or after it.
func (pf *prefixIndex) couplingAt(pos int) int {
	before := make(map[string]int)
	after := make(map[string]int)
	for _, e := range pf.tools {
		for _, k := range e.keys {
			if e.block.Pos < pos {
				before[k]++
			} else {
				after[k]++
			}
		}
	}
	total := 0
	for k, b := range before {
		total += b * after[k]
	}
	return total
}

// candidates builds Inputs.Candidates: the declared changepoint turns that are API-round
// boundaries (§8.4's intersection), each on its message block's Pos, capped to the highest-Pos
// maxCandidates.
func (pf *prefixIndex) candidates(s eval.Session, cpTurns []core.TurnIndex) []scheduler.Candidate {
	end := len(pf.turnStart)
	seen := make(map[core.TurnIndex]bool, len(cpTurns))
	turns := make([]core.TurnIndex, 0, len(cpTurns))
	for _, t := range cpTurns {
		if int(t) < 0 || int(t) >= end || seen[t] || s.Turns[int(t)].Role == roleUser {
			continue
		}
		seen[t] = true
		turns = append(turns, t)
	}
	if len(turns) == 0 {
		return nil
	}
	sort.Slice(turns, func(i, j int) bool { return turns[i] < turns[j] })
	if len(turns) > maxCandidates {
		turns = turns[len(turns)-maxCandidates:]
	}

	out := make([]scheduler.Candidate, 0, len(turns))
	for _, t := range turns {
		pos := pf.turnStart[int(t)]
		out = append(out, scheduler.Candidate{
			Pos:               pos,
			Turn:              t,
			RoundBoundary:     true,
			ReclaimableTokens: pf.reclaimAfter(pos),
			Coupling:          pf.couplingAt(pos),
		})
	}
	return out
}

// keep applies the keep rule at cut and returns the kept IDs, sorted, with the retained tail's
// token sum.
func (pf *prefixIndex) keep(cut int, budget core.Tokens) ([]string, core.Tokens) {
	kept := make(map[string]bool, len(pf.blocks))
	var tail core.Tokens
	admit := func(b eval.Block) {
		if !kept[b.ID] {
			kept[b.ID] = true
			tail += b.Tokens
		}
	}

	// The cached prefix, at no cost.
	for _, b := range pf.blocks {
		if b.Pos < cut {
			kept[b.ID] = true
		}
	}
	// The tail's unconditional retention: preserved results with their file blocks, decisions,
	// eliminations.
	for _, e := range pf.tools {
		if e.block.Pos < cut || e.droppable {
			continue
		}
		admit(e.block)
		for _, key := range e.keys {
			if fb, ok := pf.fileBlock[key]; ok && fb.Pos >= cut {
				admit(fb)
			}
		}
	}
	for _, b := range pf.blocks {
		if b.Pos >= cut && (b.Kind == eval.BlockElimination || b.Kind == eval.BlockDecision) {
			admit(b)
		}
	}
	// The pointer tier: the tail's file blocks, most recently touched path first, within budget.
	// The budget bounds this tier alone (ruling R54); the unconditional retention above is what
	// §2.2 preserves and is not a choice the budget can take back. Ruling R56 keeps this shape: the
	// driver's keep-budget FAIL on --policies qompack-l3 is §2.2's retention, not the tier's — see the
	// ADR's "keep budget" paragraph. A block that does not fit is SKIPPED (`continue`), not the
	// tier ended (`break`): the walk goes on to less recent, possibly smaller blocks, so the tier
	// is a budget-bounded set filled in preference order, not a prefix of the recency order — a
	// large recent file never empties the tier for the small ones behind it.
	pointers := make([]eval.Block, 0, len(pf.fileBlock))
	for _, b := range pf.blocks {
		if b.Kind == eval.BlockFile && b.Pos >= cut && !kept[b.ID] {
			pointers = append(pointers, b)
		}
	}
	sort.SliceStable(pointers, func(i, j int) bool {
		ri, rj := pf.lastTouch[pointers[i].Paths[0]], pf.lastTouch[pointers[j].Paths[0]]
		if ri != rj {
			return ri > rj
		}
		return pointers[i].Pos > pointers[j].Pos
	})
	var pointerTokens core.Tokens
	for _, b := range pointers {
		if pointerTokens+b.Tokens > budget {
			continue
		}
		pointerTokens += b.Tokens
		admit(b)
	}

	ids := make([]string, 0, len(kept))
	for id := range kept {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, tail
}

// ── feature synthesis and detection ─────────────────────────────────────────────────────────

// detection is what one pass of the detector over the prefix yields.
type detection struct {
	turns []core.TurnIndex
	state scheduler.ChangepointState
}

// detect runs the detector over the tool-bearing turns of [0, end) and records every turn it
// declares a changepoint at. The detector is online, so the state after turn i is the state the
// daemon would hold at that turn.
func (p *policy) detect(s eval.Session, end int) detection {
	det := scheduler.NewBOCD(p.cfg.Scheduler.Changepoint.HazardRate, p.cfg.Scheduler.Changepoint.Features)
	var out detection
	observed := make([]int, 0, end) // indices of the tool-bearing turns seen so far
	for i := range end {
		if len(s.Turns[i].ToolCalls) == 0 {
			continue
		}
		f := featuresAt(s, observed, i)
		observed = append(observed, i)
		st := det.Observe(f)
		if st.AtChangepoint {
			out.turns = append(out.turns, core.TurnIndex(i))
		}
		out.state = st
	}
	return out
}

// featuresAt computes turn i's feature vector from the observed turns before it: the recent
// window is the last featureWindow−1 observed turns plus i, the prior window the featureWindow
// observed turns before those.
func featuresAt(s eval.Session, observed []int, i int) scheduler.Features {
	recent := append(tail(observed, featureWindow-1), i)
	prior := tail(observed[:max(len(observed)-(featureWindow-1), 0)], featureWindow)

	var f scheduler.Features
	f.PathJaccard = pathJaccard(s, recent, prior)
	f.ToolShift = toolShift(s, recent, prior)
	if len(observed) > 0 {
		last := s.Turns[observed[len(observed)-1]].TS
		f.GapSeconds = max(float64(s.Turns[i].TS-last)/millisPerSecond, 0)
	}
	if todoTransition(s.Turns[i]) {
		f.TodoTransition = 1
	}
	return f
}

// tail returns the last n elements of xs.
func tail(xs []int, n int) []int {
	if len(xs) <= n {
		return xs
	}
	return xs[len(xs)-n:]
}

// pathJaccard is |R ∩ P| / |R ∪ P| over the path keys of the two windows; an empty union is 1.0
// (no evidence of a shift; do not manufacture a changepoint from silence).
func pathJaccard(s eval.Session, recent, prior []int) float64 {
	r := windowPaths(s, recent)
	p := windowPaths(s, prior)
	inter := 0
	for k := range r {
		if p[k] {
			inter++
		}
	}
	union := len(r) + len(p) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

func windowPaths(s eval.Session, turns []int) map[string]bool {
	out := make(map[string]bool)
	for _, t := range turns {
		for _, tc := range s.Turns[t].ToolCalls {
			for _, k := range pathKeys(tc.Paths) {
				out[k] = true
			}
		}
	}
	return out
}

// toolShift is the total-variation distance between the two windows' tool-name distributions,
// 0.5 · Σ_t |p_t − q_t|; either window empty is 0.0.
func toolShift(s eval.Session, recent, prior []int) float64 {
	r, rn := windowTools(s, recent)
	p, pn := windowTools(s, prior)
	if rn == 0 || pn == 0 {
		return 0
	}
	names := make(map[string]bool, len(r)+len(p))
	for k := range r {
		names[k] = true
	}
	for k := range p {
		names[k] = true
	}
	// Sum in a fixed key order: float addition is not associative, and the result feeds the
	// detector that decides the candidate set, so map order would make replay non-deterministic.
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var tv float64
	for _, k := range keys {
		tv += abs(float64(r[k])/float64(rn) - float64(p[k])/float64(pn))
	}
	return tv / 2
}

func windowTools(s eval.Session, turns []int) (map[string]int, int) {
	out := make(map[string]int)
	n := 0
	for _, t := range turns {
		for _, tc := range s.Turns[t].ToolCalls {
			out[tc.Name]++
			n++
		}
	}
	return out, n
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// todoTransition is the G1.5 safe-point signal read off a log: a TodoWrite call marking an item
// completed, or a test:pass action in the turn's text, arguments or results.
func todoTransition(t eval.Turn) bool {
	if strings.Contains(t.Text, testPassMarker) {
		return true
	}
	for _, tc := range t.ToolCalls {
		if tc.Name == toolTodoWrite && bytes.Contains(tc.Args, []byte(todoCompletedMarker)) {
			return true
		}
		if bytes.Contains(tc.Args, []byte(testPassMarker)) || bytes.Contains(tc.Result, []byte(testPassMarker)) {
			return true
		}
	}
	return false
}

// ── Evaluate's inputs ───────────────────────────────────────────────────────────────────────

// inputs assembles the explicit snapshot Evaluate decides from at the compaction preceding turn
// end (see the package comment for every convention).
func (p *policy) inputs(s eval.Session, end int, pf *prefixIndex, state scheduler.ChangepointState,
	cands []scheduler.Candidate,
) scheduler.Inputs {
	var now, prev core.UnixMilli
	switch {
	case end < len(s.Turns):
		now = s.Turns[end].TS
	case end > 0:
		now = s.Turns[end-1].TS
	}
	if end > 0 {
		prev = s.Turns[end-1].TS
	}
	n := core.Tokens(pf.n)
	cache := p.cfg.Runtime.Scheduler.Cache
	return scheduler.Inputs{
		Now:                     now,
		ContextTokens:           n,
		EffectiveWindow:         n + scheduler.HostAutoCompactBuffer,
		MaxOutputTokens:         scheduler.HostDefaultMaxOutput,
		LastAPICallTS:           prev,
		LastCacheWriteTS:        prev,
		BurnRateTokensPerMin:    burnRate(s, pf, end),
		Changepoint:             state,
		Candidates:              cands,
		ResidualTokens:          n, // no frontier in replay: the whole prefix is residual
		Cfg:                     p.cfg.Scheduler,
		LastCompactionTS:        lastCompactionTS(s, end),
		CouplingLambda:          p.cfg.Selection.Submodular.Lambda,
		Regime:                  p.regime,
		ExpiringTriggerFraction: cache.ExpiringTriggerFraction,
		AssumeMaxTTLSeconds:     cache.AssumeMaxTTLSeconds,
	}
}

// burnRate is the recent token consumption: the position-advancing tokens of the last
// featureWindow turns of the prefix over the minutes they spanned; 0 when they spanned none.
func burnRate(s eval.Session, pf *prefixIndex, end int) float64 {
	if end < 2 {
		return 0
	}
	start := max(end-featureWindow, 0)
	minutes := float64(s.Turns[end-1].TS-s.Turns[start].TS) / millisPerMinute
	if minutes <= 0 {
		return 0
	}
	var tokens core.Tokens
	for _, b := range pf.blocks {
		if int(b.Turn) >= start {
			switch b.Kind {
			case eval.BlockUserPrompt, eval.BlockAssistant, eval.BlockToolResult:
				tokens += b.Tokens
			}
		}
	}
	return float64(tokens) / minutes
}

// lastCompactionTS is the previous compaction point's timestamp, or the session start's.
func lastCompactionTS(s eval.Session, end int) core.UnixMilli {
	if len(s.Turns) == 0 {
		return 0
	}
	ts := s.Turns[0].TS
	for _, c := range s.CompactionAt {
		if int(c) < end && int(c) < len(s.Turns) && s.Turns[c].TS > ts {
			ts = s.Turns[c].TS
		}
	}
	return ts
}
