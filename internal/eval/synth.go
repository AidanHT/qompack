package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// Generator identity and timing.
//
// The epoch and the two gap sizes are the whole idle-time model. SynthSpec (fixed by §5.18) has no
// idle field, so a long gap has to be derived from something it does have — and §6.6 lists
// inter-turn time gaps as a changepoint feature while §5.4 states cold-cache windows occur only in
// idle gaps. A long think-gap at a task boundary is therefore exactly where the design says one
// belongs, and the "long-idle" shape is simply the shape with the most changepoints.
const (
	synthGeneratorID = "eval.Synthesize/1"
	// synthEpochMillis is 2025-01-01T00:00:00Z.
	synthEpochMillis   = 1735689600000
	synthTurnGapMS     = 45_000
	synthIdleGapMS     = 3_600_000
	synthUserEvery     = 6
	synthDecisionEvery = 9
	// synthPCGStream is the second PCG seed word: the golden-ratio constant, chosen only because
	// it is a well-mixed fixed value.
	synthPCGStream = 0x9E3779B97F4A7C15
	// thrashRereadThreshold is the re-read rate above which the generator emits an explicit
	// read/edit/test repair loop rather than sampling tools independently. A high re-read rate IS
	// the thrash loop: the agent is re-reading, editing and re-testing the same file.
	thrashRereadThreshold = 0.6
	// synthRecencyWindow is how far back a re-read reaches. Temporal locality is a real property
	// of agent sessions and it is what any recency heuristic — including the host's own — is
	// betting on, so a corpus that ignored it would not be neutral, it would be adversarial.
	synthRecencyWindow  = 12
	synthDecisionDomain = "qompack.eval.decision"
)

// synthToolTokens is the token cost range of each tool's result.
var synthToolTokens = map[string][2]int{
	"FileRead": {1200, 4800},
	"Bash":     {300, 2000}, //nomagic:allow a synthetic tool-output size, not a config default
	"Grep":     {200, 900},
	"Edit":     {150, 400},
	"Write":    {150, 400},
	"Test":     {2000, 9000},
	"Task":     {800, 2400},
}

// synthApproaches are the eliminated approaches injected as negative knowledge.
var synthApproaches = [8]string{
	"widen pool timeout", "retry with backoff", "disable prepared statements",
	"bump connection limit", "switch to transaction mode", "cache the lookup",
	"batch the writes", "move the check upstream",
}

// synthDependencyFiles alternate at each DependencyChangeAt turn: the files SP-09's staleness path
// will later key on.
var synthDependencyFiles = [2]string{"docker-compose.yml", "package-lock.json"}

// thrashCycle is the repair loop a high re-read rate produces, and the loop SP-15's Sequitur
// detector is meant to find.
var thrashCycle = [3]string{"FileRead", "Edit", "Test"}

// fileToolNames is the set of tools whose paths become file blocks, in the generator's vocabulary.
var fileToolNames = map[string]bool{"FileRead": true, "Read": true, "Edit": true, "Write": true}

// Synthesize deterministically generates a synthetic Session from seed and spec.
//
// The same seed and spec always produce a byte-identical Session (00-ARCHITECTURE.md §6.3), which
// is what makes replay numbers comparable across commits. Two rules keep that true: the RNG is an
// explicitly seeded PCG rather than the package-level source, and every map is walked in sorted
// key order, so Go's randomized map iteration can never leak into the output.
//
// Synthesize cannot name its own shape — SynthSpec has no field for one — so it sets a
// seed-derived ID and leaves shape naming to SynthesizeNamed.
func Synthesize(seed int64, spec SynthSpec) Session {
	g := &synthGen{
		rng:  rand.New(rand.NewPCG(uint64(seed), synthPCGStream)), //nolint:gosec // deterministic fixtures, not cryptography
		seed: seed,
		spec: spec,
	}
	return g.run()
}

// SynthesizeNamed produces one committed corpus session: Synthesize, then the shape naming that
// SynthSpec has no room for.
//
// It is the SINGLE path by which a corpus session is produced — WriteCorpus, --regen-corpus and
// the golden stability test all go through it — so the committed bytes have exactly one producer
// and the golden test compares like with like.
func SynthesizeNamed(n NamedSpec) Session {
	s := Synthesize(n.Seed, n.Spec)
	s.ID = n.Shape + "-" + strconv.FormatInt(n.Seed, 10)
	s.Meta["shape"] = n.Shape
	return s
}

// EncodeSession renders a session exactly as the committed corpus files hold it: indented JSON
// with a trailing newline. The golden test compares these bytes, so the encoding is part of the
// contract rather than a detail of whoever wrote the file.
//
// HTML escaping is off, matching every other JSON writer in this codebase, so a "<" in a redacted
// span or a path appears literally rather than as <.
func EncodeSession(s Session) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, fmt.Errorf("eval: encoding session %s: %w", s.ID, err)
	}
	return buf.Bytes(), nil
}

// synthGen is one generation in progress.
type synthGen struct {
	rng  *rand.Rand
	seed int64
	spec SynthSpec

	turns     []Turn
	readPaths []string
	pkgIndex  int
	fileIndex int

	bag    []string
	bagPos int

	// rereadAt maps a forced-reread turn to how many paths had been read before its compaction, so
	// the re-read always names content that actually predates the boundary.
	rereadAt map[int]int
	// thrashPos advances only on thrash-cycle turns, so an interruption costs one trigram rather
	// than shifting the loop's phase.
	thrashPos int
}

func (g *synthGen) run() Session {
	spec := g.spec
	if spec.Turns <= 0 {
		return Session{ID: g.defaultID(), Synthetic: true, Meta: g.meta(nil)}
	}

	changepoints := evenlySpaced(spec.Changepoints, spec.Turns, spec.Turns)
	eliminations := evenlySpaced(spec.Eliminations, spec.Turns*6/10, spec.Turns)
	subagents := evenlySpaced(spec.SubagentCalls, spec.Turns, spec.Turns)

	cpSet := indexSet(changepoints)
	elimSet := indexSet(eliminations)
	subSet := indexSet(subagents)
	depSet := make(map[int]int, len(spec.DependencyChangeAt))
	for i, at := range spec.DependencyChangeAt {
		depSet[int(at)] = i
	}
	compactSet := make(map[int]bool, len(spec.CompactionAt))
	for _, at := range spec.CompactionAt {
		compactSet[int(at)] = true
	}

	// Every injected event — an elimination, a subagent call, a dependency write — is a tool
	// invocation, and only an assistant turn makes those. Without this override an event that
	// happened to land on a user turn would be silently dropped and the shape would quietly not be
	// the shape the corpus table asked for.
	forcedAssistant := func(i int) bool {
		if i == 0 {
			return false
		}
		_, isDep := depSet[i]
		return elimSet[i] || subSet[i] || isDep
	}

	roles := make([]string, spec.Turns)
	assistantCount := 0
	for i := range roles {
		if i%synthUserEvery == 0 && !forcedAssistant(i) {
			roles[i] = roleUser
			continue
		}
		roles[i] = roleAssistant
		assistantCount++
	}
	g.bag = toolBag(g.rng, spec.ToolMix, assistantCount)

	// Every compaction is followed by a re-read of content that predates it. That is not padding:
	// a compaction nobody demands anything back from scores every policy 1.0 and hides all signal,
	// so the corpus would measure nothing. Re-reading what the boundary just dropped is also
	// exactly what an agent does, and exactly what the harness exists to count.
	g.rereadAt = map[int]int{}
	rereadOwner := map[int]bool{}
	for _, at := range spec.CompactionAt {
		for t := int(at) + 1; t < spec.Turns; t++ {
			if roles[t] == roleAssistant {
				rereadOwner[t] = true
				break
			}
		}
	}

	thrashLo, thrashHi := -1, -1
	if spec.FileRereadRate >= thrashRereadThreshold {
		thrashLo, thrashHi = spec.Turns/5, spec.Turns*5/7
	}

	ts := int64(synthEpochMillis)
	assistantSeen := 0
	pendingSubagent := ""
	var thrashPath string

	for i := range spec.Turns {
		if i > 0 {
			gap := int64(synthTurnGapMS)
			if cpSet[i] {
				gap = synthIdleGapMS
			}
			ts += gap
		}
		if cpSet[i] {
			g.pkgIndex++
		}
		if compactSet[i] {
			g.rereadAt[i] = len(g.readPaths)
		}

		turn := Turn{Index: core.TurnIndex(i), Role: roles[i], TS: core.UnixMilli(ts)}

		if roles[i] == roleAssistant {
			assistantSeen++
			turn.Tokens = core.Tokens(400 + g.rng.IntN(400))
			if assistantSeen%synthDecisionEvery == 0 {
				turn.Text = "settled the approach [decision:" + g.decisionID(i) + "]"
			}
			if pendingSubagent != "" {
				turn.Text = strings.TrimSpace(turn.Text + " subagent " + pendingSubagent +
					" reported back: the migration path is viable")
				pendingSubagent = ""
			}
			tool, path := g.toolFor(i, rereadOwner[i], elimSet[i], subSet[i], depSet,
				thrashLo, thrashHi, &thrashPath)
			turn.ToolCalls = []ToolCall{g.callFor(i, tool, path)}
			// Only an INJECTED subagent reports back. A Task the tool mix happened to draw is an
			// ordinary call, and quoting its id too would make SubagentCalls describe something
			// other than the number of subagent round-trips in the session.
			if subSet[i] {
				pendingSubagent = string(turn.ToolCalls[0].ID)
			}
		} else {
			turn.Tokens = core.Tokens(60 + g.rng.IntN(140))
			turn.Text = "next: step " + strconv.Itoa(i)
		}

		g.turns = append(g.turns, turn)
	}

	return Session{
		ID:           g.defaultID(),
		Turns:        g.turns,
		CompactionAt: append([]core.TurnIndex(nil), spec.CompactionAt...),
		Meta:         g.meta(changepoints),
		Synthetic:    true,
	}
}

// toolFor decides one assistant turn's tool and the path it touches.
func (g *synthGen) toolFor(i int, forcedReread, isElim, isSub bool, depSet map[int]int,
	thrashLo, thrashHi int, thrashPath *string,
) (tool, path string) {
	switch {
	case forcedReread:
		return "FileRead", g.preCompactionPath(i)
	case isElim:
		return toolRecordEliminated, ""
	case isSub:
		return "Task", ""
	default:
		if idx, ok := depSet[i]; ok {
			return "Write", synthDependencyFiles[idx%len(synthDependencyFiles)]
		}
	}

	if thrashLo >= 0 && i >= thrashLo && i <= thrashHi {
		tool = thrashCycle[g.thrashPos%len(thrashCycle)]
		if g.thrashPos%len(thrashCycle) == 0 {
			*thrashPath = g.pickPath()
		}
		g.thrashPos++
		return tool, *thrashPath
	}

	tool = g.nextBagTool()
	if fileToolNames[tool] {
		return tool, g.pickPath()
	}
	return tool, ""
}

// preCompactionPath returns a path read shortly before the compaction this re-read answers to.
//
// It draws from the most RECENT pre-compaction files rather than uniformly over the session's
// whole history, and that is a fidelity choice, not a thumb on the scale. Real sessions have
// strong temporal locality: the first thing an agent does after a compaction is pick up the file
// it was working on, not a file it touched two hundred turns ago. A corpus that re-read uniformly
// would be adversarial to every recency heuristic at once, stock would score exactly 0.0 on every
// session, and the primary metric would become indistinguishable from the null floor — which
// measures nothing and would make the Phase 0 number a tautology rather than a finding.
func (g *synthGen) preCompactionPath(turn int) string {
	best, found := -1, false
	for at, count := range g.rereadAt {
		if at >= turn || count == 0 {
			continue
		}
		if !found || at > best {
			best, found = at, true
		}
	}
	if !found {
		return g.pickPath()
	}
	count := g.rereadAt[best]
	window := min(count, hostTopFiles)
	return g.readPaths[count-1-(turn*7+3)%window]
}

// nextBagTool draws the next tool from the shuffled exact-proportion bag.
func (g *synthGen) nextBagTool() string {
	if len(g.bag) == 0 {
		return "Bash"
	}
	tool := g.bag[g.bagPos%len(g.bag)]
	g.bagPos++
	return tool
}

// pickPath re-reads a previously read file at the spec's re-read rate, and mints a new one in the
// current changepoint namespace otherwise.
//
// A re-read draws from the most recent synthRecencyWindow files rather than uniformly over the
// whole history, because that is what real sessions look like: an agent works in a neighbourhood
// and moves on, it does not sample its own past uniformly.
func (g *synthGen) pickPath() string {
	if len(g.readPaths) > 0 && g.rng.Float64() < g.spec.FileRereadRate {
		window := min(len(g.readPaths), synthRecencyWindow)
		return g.readPaths[len(g.readPaths)-1-g.rng.IntN(window)]
	}
	g.fileIndex++
	p := "src/pkg" + strconv.Itoa(g.pkgIndex) + "/file" + strconv.Itoa(g.fileIndex) + ".go"
	g.readPaths = append(g.readPaths, p)
	return p
}

// callFor builds the tool call for one turn.
func (g *synthGen) callFor(i int, tool, path string) ToolCall {
	call := ToolCall{ID: core.ToolUseID(fmt.Sprintf("tu_%04d", i)), Name: tool}
	if tool == "Task" {
		call.ID = core.ToolUseID(fmt.Sprintf("tu_task_%04d", i))
	}
	if path != "" {
		call.Paths = []string{path}
		if !contains(g.readPaths, path) {
			g.readPaths = append(g.readPaths, path)
		}
	}

	if tool == toolRecordEliminated {
		approach := synthApproaches[(i/len(synthApproaches)+i)%len(synthApproaches)]
		target := "src/pkg" + strconv.Itoa(g.pkgIndex) + "/file1.go"
		if len(g.readPaths) > 0 {
			target = g.readPaths[i%len(g.readPaths)]
		}
		call.Paths = []string{target}
		call.Args = mustCompactJSON(map[string]string{
			"target":   target,
			"approach": approach,
			"reason":   approach + " fails under the pinned dependency constraint",
		})
		call.Result = mustCompactJSON(map[string]int{"tokens": 64})
		return call
	}

	lo, hi := 300, 900 //nomagic:allow the fallback synthetic tool-output size, not a config default
	if r, ok := synthToolTokens[tool]; ok {
		lo, hi = r[0], r[1]
	}
	tokens := lo + g.rng.IntN(hi-lo+1)
	if tool == "Test" {
		tokens = int(float64(tokens) * (1 + g.spec.TestOutputNoise))
	}
	call.Result = mustCompactJSON(map[string]int{"tokens": tokens})
	return call
}

// decisionID mints a decision id keyed on the SEED rather than on the session ID, precisely so
// that naming a session afterwards cannot perturb a single decision.
func (g *synthGen) decisionID(turn int) string {
	key := strconv.FormatInt(g.seed, 10) + "#" + strconv.Itoa(turn)
	return "dec_" + core.HashBytes(synthDecisionDomain, []byte(key)).Short()
}

func (g *synthGen) defaultID() string { return "synth-" + strconv.FormatInt(g.seed, 10) }

// meta records everything needed to understand a committed session without re-running the
// generator, including the changepoint turns the idle gaps sit at.
func (g *synthGen) meta(changepoints []int) map[string]string {
	spec, err := json.Marshal(g.spec)
	if err != nil {
		spec = []byte("{}")
	}
	cps := make([]string, len(changepoints))
	for i, c := range changepoints {
		cps[i] = strconv.Itoa(c)
	}
	return map[string]string{
		"generator":    synthGeneratorID,
		"seed":         strconv.FormatInt(g.seed, 10),
		"spec":         string(spec),
		"changepoints": strings.Join(cps, ","),
	}
}

// toolBag builds a multiset matching mix's proportions exactly (largest-remainder allocation) and
// shuffles it deterministically.
//
// Exact allocation rather than independent sampling is what makes the shape invariants robust: at
// 150 draws, i.i.d. sampling from a 0.60 weight lands below 0.55 often enough to make a corpus
// fixture flaky, and a flaky fixture is worse than no fixture.
func toolBag(rng *rand.Rand, mix map[string]float64, n int) []string {
	if n <= 0 || len(mix) == 0 {
		return nil
	}
	names := make([]string, 0, len(mix))
	total := 0.0
	for k, v := range mix {
		names = append(names, k)
		total += v
	}
	sort.Strings(names)
	if total <= 0 {
		total = float64(len(names))
		for _, k := range names {
			mix[k] = 1
		}
	}

	counts := make([]int, len(names))
	fracs := make([]float64, len(names))
	assigned := 0
	for i, k := range names {
		exact := float64(n) * mix[k] / total
		counts[i] = int(math.Floor(exact))
		fracs[i] = exact - float64(counts[i])
		assigned += counts[i]
	}

	order := make([]int, len(names))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		if fracs[order[a]] != fracs[order[b]] {
			return fracs[order[a]] > fracs[order[b]]
		}
		return names[order[a]] < names[order[b]]
	})
	for i := 0; assigned < n; i++ {
		counts[order[i%len(order)]]++
		assigned++
	}

	bag := make([]string, 0, n)
	for i, k := range names {
		for range counts[i] {
			bag = append(bag, k)
		}
	}
	rng.Shuffle(len(bag), func(i, j int) { bag[i], bag[j] = bag[j], bag[i] })
	return bag
}

// evenlySpaced returns count turn indices spread across [0, within), bounded by limit. The first
// index is never in the opening turns, which is what keeps a cold-cache idle gap at a task
// boundary rather than at startup.
func evenlySpaced(count, within, limit int) []int {
	if count <= 0 || within <= 0 {
		return nil
	}
	out := make([]int, 0, count)
	for i := 1; i <= count; i++ {
		at := i * within / (count + 1)
		if at >= limit {
			at = limit - 1
		}
		out = append(out, at)
	}
	return out
}

func indexSet(v []int) map[int]bool {
	out := make(map[int]bool, len(v))
	for _, i := range v {
		out[i] = true
	}
	return out
}

func contains(v []string, want string) bool {
	for _, s := range v {
		if s == want {
			return true
		}
	}
	return false
}

// mustCompactJSON marshals a small, statically-shaped value the generator controls entirely, so
// there is no reachable error to report.
func mustCompactJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("eval: synthesizing a tool payload: " + err.Error())
	}
	return b
}
