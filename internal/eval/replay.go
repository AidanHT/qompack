package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// corpusManifestName is the one filename Load skips. It lives beside the sessions and is a
// manifest, not a session; skipping it by name rather than by a heuristic is what stops a real
// fixture from ever being swallowed by the same rule.
const corpusManifestName = "CORPUS.json"

// liveModeEnvVar gates live mode. CI never sets it.
const liveModeEnvVar = "QOMPACK_EVAL_LIVE"

// Repair tool names. FileRead and ReAttempt are what the deterministic estimator inserts when a
// keep-set failed to satisfy a demand: the work the agent has to redo because the content is gone.
const (
	toolFileRead  = "FileRead"
	toolRead      = "Read"
	toolBash      = "Bash"
	toolReAttempt = "ReAttempt"
)

// millisPerK converts a token count into thousands, the unit the latency coefficients are in.
const millisPerK = 1000.0

// Load reads every session file under dir, in filename order.
//
// Order is part of the contract: the corpus is a set, but it decides the order scores accumulate
// in, and Report's pooled percentiles are a function of that sequence. A file that fails to parse
// is a hard error naming the file — a silently skipped fixture is a silently wrong baseline — and
// unknown fields are rejected so schema drift cannot quietly zero a field the number came from.
func (h *harness) Load(dir string) ([]Session, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("eval: reading corpus %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	sawManifest := false
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			continue
		}
		if e.Name() == corpusManifestName {
			sawManifest = true
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	sessions := make([]Session, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path) //nolint:gosec // an explicitly named corpus directory
		if err != nil {
			return nil, fmt.Errorf("eval: reading %s: %w", path, err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var s Session
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("eval: parsing %s: %w", path, err)
		}
		sessions = append(sessions, s)
	}

	if len(sessions) == 0 {
		return nil, fmt.Errorf("eval: no sessions in %s: %w", dir, core.ErrNotFound)
	}
	if !sawManifest {
		// An imported recorded corpus legitimately has no manifest, so this is a notice rather
		// than a failure.
		h.log.Warn("eval: corpus has no manifest", "dir", dir, "file", corpusManifestName)
	}
	return sessions, nil
}

// BaselineRun builds the uncompacted branch straight from the logged turns.
//
// It is a package function rather than a Harness method because the uncompacted branch is not a
// replay at all — it is the logged ground truth — and because §5.18's Harness interface is
// reproduced exactly as specified, with no method added to it.
func BaselineRun(s Session) Run {
	actions := make([]Action, 0, len(s.Turns))
	for _, turn := range s.Turns {
		actions = append(actions, actionOfTurn(turn))
	}
	first := core.TurnIndex(-1)
	if len(s.CompactionAt) > 0 {
		first = s.CompactionAt[0]
	}
	return Run{
		Session:             s.ID,
		Branch:              branchUncompacted,
		Actions:             actions,
		FirstCompactionTurn: first,
		Horizon:             DefaultHorizonK,
	}
}

// actionOfTurn projects one logged turn onto the action the branches are compared over.
func actionOfTurn(turn Turn) Action {
	a := Action{Turn: turn.Index}
	if len(turn.ToolCalls) > 0 {
		a.Tool = turn.ToolCalls[0].Name
	}
	seen := make(map[string]bool)
	for _, tc := range turn.ToolCalls {
		for _, p := range tc.Paths {
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			a.Paths = append(a.Paths, p)
		}
	}
	if ids := decisionIDs(turn.Text); len(ids) > 0 {
		a.Decision = ids[0]
	}
	return a
}

// Replay applies p's keep-set decisions to the logged action sequence and repairs every demand
// they failed to satisfy, giving a reproducible, model-free estimate of D (§4.2).
//
// Deterministic mode is the only mode CI ever runs. Live mode is a seam: it refuses rather than
// degrading, first because the environment gate is shut and then because no LiveRunner is
// installed, so a deterministic number can never be mistaken for a model-backed one.
func (h *harness) Replay(ctx context.Context, s Session, p Policy, o ReplayOptions) (Run, error) {
	defer h.observe("eval.replay.ms", h.clock.Now())
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	if o.K <= 0 {
		o.K = DefaultHorizonK
	}
	if o.Budget <= 0 {
		o.Budget = DefaultKeepBudget
	}
	if !o.Deterministic {
		if os.Getenv(liveModeEnvVar) != "1" {
			return Run{}, fmt.Errorf(
				"eval: live mode requires %s=1, and CI runs deterministic mode only: %w",
				liveModeEnvVar, core.ErrNotImplemented)
		}
		if h.live == nil {
			return Run{}, fmt.Errorf(
				"eval: live mode has no LiveRunner installed in this build; SP-17's pre-release "+
					"run supplies one: %w", core.ErrNotImplemented)
		}
	}

	run := BaselineRun(s)
	run.Branch = branchCompacted
	run.Policy = p.Name()
	run.Horizon = o.K

	blockIndex := make(map[string]Block)
	toolPaths := make(map[core.ToolUseID][]string)
	for _, turn := range s.Turns {
		for _, tc := range turn.ToolCalls {
			toolPaths[tc.ID] = tc.Paths
		}
	}

	repairs := make(map[core.TurnIndex][]Action)
	lostDecision := make(map[core.TurnIndex]bool)

	for _, at := range s.CompactionAt {
		if err := ctx.Err(); err != nil {
			return Run{}, err
		}
		keep, err := p.KeepSet(ctx, s, at, o.Budget)
		if err != nil {
			return Run{}, fmt.Errorf("eval: policy %s at turn %d: %w", p.Name(), at, err)
		}
		kept := make(map[string]bool, len(keep.IDs))
		for _, id := range keep.IDs {
			kept[id] = true
		}

		blocks := Blocks(s, at)
		for _, b := range blocks {
			if _, ok := blockIndex[b.ID]; !ok {
				blockIndex[b.ID] = b
			}
		}

		to := at + core.TurnIndex(o.K)
		if int(to) > len(s.Turns) {
			to = core.TurnIndex(len(s.Turns))
		}
		demands := Demands(s, at, to)
		for _, d := range demands {
			if kept[d.BlockID] {
				continue
			}
			if a, ok := repairFor(d, blockIndex, toolPaths); ok {
				repairs[d.Turn] = append(repairs[d.Turn], a)
			} else {
				lostDecision[d.Turn] = true
			}
		}

		n := prefixTokens(blocks)
		residual := max(n-keep.P, 0)
		run.At = append(run.At, at)
		run.Demands = append(run.Demands, demands)
		run.PrefixTokens = append(run.PrefixTokens, core.Tokens(n))
		run.Keeps = append(run.Keeps, keep)
		run.ResidualSpan = append(run.ResidualSpan, core.Tokens(residual))
		run.PauseMS = append(run.PauseMS, modelledPauseMS(h.lat, core.Tokens(residual)))
		run.FirstTurnAfterMS = append(run.FirstTurnAfterMS, modelledFirstTurnMS(h.lat, keep.Tokens))
	}

	run.Actions = applyRepairs(run.Actions, repairs, lostDecision)
	return run, nil
}

// repairFor turns one unsatisfied demand into the work the agent has to redo. A lost decision has
// no repair — nothing brings it back — so it reports false and the demanding turn simply forgets
// what it had decided.
func repairFor(d Demand, blocks map[string]Block, toolPaths map[core.ToolUseID][]string) (Action, bool) {
	switch d.Kind {
	case DemandFileContent:
		return Action{
			Turn: d.Turn, Tool: toolFileRead,
			Paths: []string{strings.TrimPrefix(d.BlockID, fileBlockPrefix)},
		}, true

	case DemandToolResult:
		id := core.ToolUseID(strings.TrimPrefix(d.BlockID, toolBlockPrefix))
		if paths := toolPaths[id]; len(paths) > 0 {
			return Action{Turn: d.Turn, Tool: toolFileRead, Paths: []string{paths[0]}}, true
		}
		// A tool use with no path (a Bash command, a subagent Task) can only be re-run.
		return Action{Turn: d.Turn, Tool: toolBash}, true

	case DemandElimination:
		a := Action{Turn: d.Turn, Tool: toolReAttempt}
		if b, ok := blocks[d.BlockID]; ok && len(b.Paths) > 0 {
			a.Paths = []string{b.Paths[0]}
		}
		return a, true

	case DemandDecision:
		return Action{}, false

	default:
		return Action{}, false
	}
}

// applyRepairs splices repair actions in immediately before the turn that needed them, and blanks
// the decision of any turn whose decision the compaction dropped.
func applyRepairs(actions []Action, repairs map[core.TurnIndex][]Action, lost map[core.TurnIndex]bool) []Action {
	if len(repairs) == 0 && len(lost) == 0 {
		return actions
	}
	out := make([]Action, 0, len(actions)+len(repairs))
	for _, a := range actions {
		out = append(out, repairs[a.Turn]...)
		if lost[a.Turn] {
			a.Decision = ""
		}
		out = append(out, a)
	}
	return out
}

// modelledPauseMS is §11.2's compaction pause: the wall-clock of the summarization call, driven by
// the residual span between the frontier and the compaction point.
func modelledPauseMS(lat LatencyModel, residual core.Tokens) int {
	return roundNonNegative(lat.PauseBaseMS + lat.PausePerKResidualMS*float64(max(residual, 0))/millisPerK)
}

// modelledFirstTurnMS is §11.2's first-turn-after latency: the turn following a compaction pays to
// rebuild and re-write the cache, so it scales with what was rehydrated rather than with the
// residual.
func modelledFirstTurnMS(lat LatencyModel, rehydrated core.Tokens) int {
	return roundNonNegative(lat.FirstTurnBaseMS + lat.FirstTurnPerKRehydrMS*float64(max(rehydrated, 0))/millisPerK)
}

// roundNonNegative rounds to the nearest millisecond and floors at zero, because a negative
// millisecond count would poison a percentile.
func roundNonNegative(v float64) int {
	return max(int(math.Round(v)), 0)
}
