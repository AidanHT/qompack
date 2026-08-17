package eval

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// The registry is what turns G8.3 from a complaint into a measurable question: any steering
// strategy — including a /compact-instruction variant — becomes a named policy with a comparable
// number, bounded below by null and above by oracle.
var (
	registryMu sync.Mutex
	registry   = map[string]func(config.Config) Policy{}
)

// RegisterPolicy adds a named policy constructor. A duplicate name panics: registration happens
// at init, so a collision is a programming error that must be loud immediately rather than
// silently shadowing whichever policy lost the race.
func RegisterPolicy(name string, ctor func(config.Config) Policy) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("eval: policy %q registered twice", name))
	}
	registry[name] = ctor
}

// PolicyByName constructs the named policy, reporting whether it is registered.
func PolicyByName(name string, cfg config.Config) (Policy, bool) {
	registryMu.Lock()
	ctor, ok := registry[name]
	registryMu.Unlock()
	if !ok {
		return nil, false
	}
	return ctor(cfg), true
}

// PolicyNames returns every registered name, sorted, so a report's column order never depends on
// init order or on map iteration.
func PolicyNames() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func init() {
	RegisterPolicy("stock", NewStockPolicy)
	RegisterPolicy("null", NewNullPolicy)
}

// ── block helpers shared by the policies ────────────────────────────────────────────────────

// isPositionAdvancing reports whether a block is prefix content rather than a derived retention
// unit — the same split Blocks uses to decide whether a block advances Pos.
func isPositionAdvancing(k BlockKind) bool {
	return k == BlockUserPrompt || k == BlockAssistant || k == BlockToolResult
}

// blocksOfTurn returns a turn's position-advancing blocks: its own message block and its tool
// results. Derived blocks ride along via the file-restore stage instead.
func blocksOfTurn(blocks []Block, turn core.TurnIndex) []Block {
	var out []Block
	for _, b := range blocks {
		if b.Turn == turn && isPositionAdvancing(b.Kind) {
			out = append(out, b)
		}
	}
	return out
}

// derivedBlocksOfTurn returns a turn's file, elimination and decision blocks.
func derivedBlocksOfTurn(blocks []Block, turn core.TurnIndex) []Block {
	var out []Block
	for _, b := range blocks {
		if b.Turn == turn && !isPositionAdvancing(b.Kind) {
			out = append(out, b)
		}
	}
	return out
}

// lastNDistinctFiles returns the n most recent file blocks, one per distinct path key, re-sorted
// ascending by turn so the result is deterministic and reads in session order.
func lastNDistinctFiles(blocks []Block, n int) []Block {
	if n <= 0 {
		return nil
	}
	var files []Block
	for _, b := range blocks {
		if b.Kind == BlockFile {
			files = append(files, b)
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].Turn > files[j].Turn })

	seen := make(map[string]bool, n)
	out := make([]Block, 0, n)
	for _, b := range files {
		if seen[b.ID] {
			continue
		}
		seen[b.ID] = true
		out = append(out, b)
		if len(out) == n {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Turn < out[j].Turn })
	return out
}

// sumTokens totals a block slice's weights.
func sumTokens(bs []Block) core.Tokens {
	var n core.Tokens
	for _, b := range bs {
		n += b.Tokens
	}
	return n
}

// hasTextBlock reports whether a turn contributed a message block, which is what §2.3's
// minTextBlockMessages counts.
func hasTextBlock(bs []Block) bool {
	for _, b := range bs {
		if b.Kind == BlockUserPrompt || b.Kind == BlockAssistant {
			return true
		}
	}
	return false
}

// oldestKeptTurn returns the earliest turn any kept block came from, and false when nothing is
// kept — which terminates the eviction loop even when the budget is smaller than a single block.
func oldestKeptTurn(contrib map[string]core.Tokens, blocks []Block) (core.TurnIndex, bool) {
	var oldest core.TurnIndex
	found := false
	for _, b := range blocks {
		if _, kept := contrib[b.ID]; !kept {
			continue
		}
		if !found || b.Turn < oldest {
			oldest, found = b.Turn, true
		}
	}
	return oldest, found
}

// sortedKeys returns a contribution map's keys in lexicographic order.
func sortedKeys(contrib map[string]core.Tokens) []string {
	if len(contrib) == 0 {
		return nil
	}
	out := make([]string, 0, len(contrib))
	for id := range contrib {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ── stock ───────────────────────────────────────────────────────────────────────────────────

// stockPolicy models Claude Code's Full Compact per §2.4 and §2.3 exactly. It is the policy the
// Phase 0 baseline number describes.
type stockPolicy struct{}

// NewStockPolicy returns the modelled host behaviour. It reads no configuration: stock behaviour
// is a property of the host, not of Qompack's settings.
func NewStockPolicy(config.Config) Policy { return stockPolicy{} }

func (stockPolicy) Name() string { return "stock" }

// KeepSet reproduces the host's three-stage decision.
//
// Every stage records what each kept block contributes to the running total in contrib, because
// stage (a) caps a file block at 5K while stage (b) would count that same block in full. Without
// the map, stage (c)'s removals subtract a number that was never added and the total drifts — it
// can go negative on a session with large file reads. used() is by construction Σ contrib.
func (stockPolicy) KeepSet(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error) {
	if err := ctx.Err(); err != nil {
		return KeepSet{}, err
	}
	blocks := Blocks(s, at)
	contrib := map[string]core.Tokens{}
	add := func(id string, t core.Tokens) {
		if _, ok := contrib[id]; !ok {
			contrib[id] = t
		}
	}
	used := func() core.Tokens {
		var n core.Tokens
		for _, t := range contrib {
			n += t
		}
		return n
	}

	// (a) §2.4 step 7: top 5 recently-read files, 5K/file, 50K budget.
	fileUsed := core.Tokens(0)
	for _, b := range lastNDistinctFiles(blocks, hostTopFiles) {
		t := min(b.Tokens, hostPerFileTokens)
		if fileUsed+t > hostRestoreBudget {
			break
		}
		add(b.ID, t)
		fileUsed += t
	}

	// (b) §2.3 preservation: walk backwards from `at`, keeping whole turns until BOTH
	// minTokens=10_000 and minTextBlockMessages=5 are met, capped at maxTokens=40_000.
	msgs, msgTokens := 0, core.Tokens(0)
	for t := int(at) - 1; t >= 0; t-- {
		turnBlocks := blocksOfTurn(blocks, core.TurnIndex(t))
		tt := sumTokens(turnBlocks)
		if msgTokens+tt > hostPreserveMaxTokens {
			break
		}
		for _, b := range turnBlocks {
			add(b.ID, b.Tokens)
		}
		msgTokens += tt
		if hasTextBlock(turnBlocks) {
			msgs++
		}
		if msgTokens >= hostPreserveMinTokens && msgs >= hostPreserveMinMessages {
			break
		}
	}

	// (c) The harness imposes one equal budget on every policy (§11.1, "under the same token
	// budget"), and the host's own two budgets — 50K of file restore plus 40K of preservation —
	// do not fit inside it. Stock has no smarter rule than dropping its oldest kept turn, so that
	// is exactly what it does, and the resulting squeeze is a real property of stock behaviour
	// under an equal budget rather than an artifact to be tuned away.
	for used() > budget {
		oldest, ok := oldestKeptTurn(contrib, blocks)
		if !ok {
			break
		}
		for _, b := range blocksOfTurn(blocks, oldest) {
			delete(contrib, b.ID)
		}
		for _, b := range derivedBlocksOfTurn(blocks, oldest) {
			delete(contrib, b.ID)
		}
	}

	// P is 0, and that is not an approximation: a Full Compact rewrites the whole message array,
	// so p_min is 0 and the rewrite cost is w·n. That is the fact §5.2 is complaining about, and
	// the baseline has to show it.
	return KeepSet{IDs: sortedKeys(contrib), Tokens: used(), P: 0}, nil
}

// ── null ────────────────────────────────────────────────────────────────────────────────────

// nullPolicy keeps nothing. Its fraction-of-OPT is the floor: a policy scoring below null is a
// bug in that policy, not a finding.
type nullPolicy struct{}

// NewNullPolicy returns the floor policy.
func NewNullPolicy(config.Config) Policy { return nullPolicy{} }

func (nullPolicy) Name() string { return "null" }

func (nullPolicy) KeepSet(ctx context.Context, _ Session, _ core.TurnIndex, _ core.Tokens) (KeepSet, error) {
	if err := ctx.Err(); err != nil {
		return KeepSet{}, err
	}
	return KeepSet{}, nil
}
