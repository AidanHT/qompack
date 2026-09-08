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
	// synthGeneratorID is bumped whenever the generator's OUTPUT SHAPE changes, because
	// eval.Comparable refuses to compare two baselines whose corpora were produced by different
	// generators. /2 is the SP02-D1/D3 recall rework: sessions now recall tool results,
	// decisions and eliminations across a compaction cut, not only file content.
	synthGeneratorID = "eval.Synthesize/2"
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

	// The recall window is SP02-D1 and SP02-D3's repair, and it is one mechanism, not two.
	//
	// Before it, exactly ONE assistant turn after each compaction re-read one pre-compaction file.
	// That made every demand in the committed corpus a DemandFileContent (177 of 177 across 39
	// events), so tool_edit_distance, re_attempts and decision_preservation were computed over an
	// input that could not move them; and it capped Σ tokens(candidates) at 14 412 against a
	// 40 000 keep budget, so the Belady budget never bound and fraction_of_opt graded against a
	// trivial ceiling.
	//
	// A real agent does not resume with one read. It spends its first several turns rebuilding the
	// context the summary dropped: re-reading the files it was in, re-checking a tool result it
	// had quoted, restating what it had decided, and — when the elimination went with the summary
	// — re-attempting an approach it had already ruled out. That last one is the failure the whole
	// negative-knowledge subsystem exists to prevent, so a corpus that could not produce it was
	// measuring around the thing under test.
	//
	// The window's LENGTH is derived from FileRereadRate rather than from a new SynthSpec field:
	// SynthSpec is fixed by §5.18 and gains no fields here, and the re-read rate is already the
	X
	synthRecallBase  = 9
	synthRecallScale = 10
	// The three fixed recall slots, in the order an agent actually rebuilds: pick the file back up,
	// re-check the tool result it quoted, restate the decision, re-try the ruled-out approach.
	// Slot 0 and every slot at or above synthRecallFileFrom are file re-reads.
	synthRecallToolSlot     = 1
	synthRecallDecisionSlot = 2
	synthRecallElimSlot     = 3
	synthRecallFileFrom     = 4
	// synthRecallPathsPerTurn is the working set one recall turn picks back up. The recall window
	// cannot simply be made longer: DefaultHorizonK bounds it at 20 turns, and a demand raised
	// past at+K is outside the horizon and counts for nothing. Widening each turn is the only
	// axis left, and it is the faithful one — an agent resuming a task opens the two or three
	// files it was working across, not one.
	synthRecallPathsPerTurn = 3
	// synthSubagentDelay is how many assistant turns a subagent's report lands after its Task
	// call. SP02-D1's other half: at 0 the pair was adjacent, so it could never straddle a cut and
	// DemandToolResult could never fire from it.
	synthSubagentDelay = 2
)

// recallTurns is how many assistant turns after a compaction are spent recalling what it dropped.
func recallTurns(rereadRate float64) int {
	n := synthRecallBase + int(rereadRate*synthRecallScale)
	return max(n, synthRecallFileFrom+1)
}

// recallSlots is how many DISTINCT pre-compaction files a recall window can name, and therefore
// how wide preCompactionPath's recency window has to be. Narrower and the later slots wrap onto
// paths the window already named, which is what silently capped Σ tokens(candidates).
func recallSlots(rereadRate float64) int {
	return (recallTurns(rereadRate) - synthRecallFileFrom + 1) * synthRecallPathsPerTurn
}

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

	// rereadAt maps a compaction turn to how many paths had been read before it, so a recall
	// always names content that actually predates the boundary.
	rereadAt map[int]int
	// The three parallel snapshots the non-file recall slots draw from: how many tool ids,
	// decisions and eliminations existed before each compaction. Without them a recall could quote
	// something minted AFTER the cut, which raises no demand at all and would make the corpus
	// assertion pass while measuring nothing — the exact failure SP02-D1 records.
	toolIDs     []string
	toolIDsAt   map[int]int
	decisions   []string
	decisionsAt map[int]int
	elims       []synthElim
	elimsAt     map[int]int
	// thrashPos advances only on thrash-cycle turns, so an interruption costs one trigram rather
	// than shifting the loop's phase.
	thrashPos int
}

// synthElim is one recorded elimination, kept so a later turn can re-attempt exactly it.
type synthElim struct{ target, approach string }

// recallSlot names the compaction an assistant turn is recalling and its position in that
// compaction's recall window.
type recallSlot struct{ at, slot int }

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

	// Every compaction is followed by a RECALL WINDOW: the first recallTurns assistant turns after
	// it rebuild what the boundary dropped. That is not padding — a compaction nobody demands
	// anything back from scores every policy 1.0 and hides all signal — and it is what an agent
	// actually does. See the synthRecall* constants for why one re-read was not enough.
	g.rereadAt = map[int]int{}
	g.toolIDsAt = map[int]int{}
	g.decisionsAt = map[int]int{}
	g.elimsAt = map[int]int{}
	recall := make(map[int]recallSlot, len(spec.CompactionAt)*synthRecallBase)
	want := recallTurns(spec.FileRereadRate)
	for _, at := range spec.CompactionAt {
		n := 0
		for t := int(at) + 1; t < spec.Turns && n < want; t++ {
			if roles[t] != roleAssistant {
				continue
			}
			// A turn already claimed by an EARLIER compaction keeps that claim: two windows
			// overlapping means the second compaction lands mid-recovery, and the first
			// compaction's recall is the one that predates it.
			if _, taken := recall[t]; taken {
				continue
			}
			recall[t] = recallSlot{at: int(at), slot: n}
			n++
		}
	}

	thrashLo, thrashHi := -1, -1
	if spec.FileRereadRate >= thrashRereadThreshold {
		thrashLo, thrashHi = spec.Turns/5, spec.Turns*5/7
	}

	ts := int64(synthEpochMillis)
	assistantSeen := 0
	pendingSubagent := ""
	pendingSubagentIn := 0
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
			g.toolIDsAt[i] = len(g.toolIDs)
			g.decisionsAt[i] = len(g.decisions)
			g.elimsAt[i] = len(g.elims)
		}

		turn := Turn{Index: core.TurnIndex(i), Role: roles[i], TS: core.UnixMilli(ts)}

		if roles[i] == roleAssistant {
			assistantSeen++
			turn.Tokens = core.Tokens(400 + g.rng.IntN(400))
			if assistantSeen%synthDecisionEvery == 0 {
				id := g.decisionID(i)
				g.decisions = append(g.decisions, id)
				turn.Text = "settled the approach [decision:" + id + "]"
			}
			if pendingSubagent != "" {
				if pendingSubagentIn > 0 {
					pendingSubagentIn--
				} else {
					turn.Text = strings.TrimSpace(turn.Text + " subagent " + pendingSubagent +
						" reported back: the migration path is viable")
					pendingSubagent = ""
				}
			}
			slot, recalling := recall[i]
			turn.Text = strings.TrimSpace(turn.Text + g.recallText(slot, recalling))
			tool, path := g.toolFor(i, slot, recalling, elimSet[i], subSet[i], depSet,
				thrashLo, thrashHi, &thrashPath)
			call := g.callFor(i, tool, path)
			switch {
			case recalling && slot.slot == synthRecallElimSlot:
				call = g.reAttemptCall(i, slot.at, call)
			case recalling && slot.slot >= synthRecallFileFrom:
				// A resuming agent picks up a WORKING SET, not one file, and the recall window is
				// bounded by DefaultHorizonK — a demand raised after turn at+K is outside the
				// horizon and counts for nothing. Two paths per recall turn is how the candidate
				// set clears DefaultKeepBudget without the window running past K. It inflates no
				// token count: every path here was already read before the cut, so its block's
				// weight was fixed by that first read, not by this call.
				call = g.addRecallPath(call, slot)
			}
			turn.ToolCalls = []ToolCall{call}
			if call.ID != "" && len(call.Paths) > 0 {
				g.toolIDs = append(g.toolIDs, string(call.ID))
			}
			if call.Name == toolRecordEliminated {
				target, approach := eliminationArgs(call.Args)
				g.elims = append(g.elims, synthElim{target: target, approach: approach})
			}
			// Only an INJECTED subagent reports back. A Task the tool mix happened to draw is an
			// ordinary call, and quoting its id too would make SubagentCalls describe something
			// other than the number of subagent round-trips in the session.
			if subSet[i] {
				pendingSubagent = string(call.ID)
				pendingSubagentIn = synthSubagentDelay
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
//
// A recall slot outranks everything: the window exists to make the demand fire, and a turn the
// tool mix happened to want for something else would silently drop it.
func (g *synthGen) toolFor(i int, slot recallSlot, recalling, isElim, isSub bool, depSet map[int]int,
	thrashLo, thrashHi int, thrashPath *string,
) (tool, path string) {
	if recalling {
		switch slot.slot {
		case synthRecallToolSlot, synthRecallDecisionSlot:
			// The recall is in the TEXT, so the turn still does ordinary work. Bash touches no
			// path, which keeps the quoted-id demand the only one this turn raises.
			return toolBash, ""
		case synthRecallElimSlot:
			// reAttemptCall rewrites this call's args; the tool has to be one whose paths become
			// a file block, so the re-attempt is visible as work and not only as a demand.
			return "Edit", g.preCompactionPath(slot.at, slot.slot)
		default:
			return "FileRead", g.preCompactionPath(slot.at, synthRecallPathsPerTurn*slot.slot)
		}
	}
	switch {
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

// preCompactionPath returns the slot'th most recent path read before compaction at.
//
// It draws from the most RECENT pre-compaction files rather than uniformly over the session's
// whole history, and that is a fidelity choice, not a thumb on the scale. Real sessions have
// strong temporal locality: the first thing an agent does after a compaction is pick up the file
// it was working on, not a file it touched two hundred turns ago. A corpus that re-read uniformly
// would be adversarial to every recency heuristic at once, stock would score exactly 0.0 on every
// session, and the primary metric would become indistinguishable from the null floor — which
// measures nothing and would make the Phase 0 number a tautology rather than a finding.
//
// The window is exactly as wide as the recall window is long, so every slot names a DIFFERENT
// file. Distinctness is what SP02-D3 needs: one file re-read ten times is one candidate block, and
// Σ tokens(candidates) has to clear DefaultKeepBudget for the Belady budget to bind at all.
func (g *synthGen) preCompactionPath(at, slot int) string {
	count := g.rereadAt[at]
	if count == 0 {
		return g.pickPath()
	}
	window := min(count, max(recallSlots(g.spec.FileRereadRate), hostTopFiles))
	return g.readPaths[count-1-slot%window]
}

// addRecallPath gives a file-recall turn the rest of its working set: the next pre-compaction
// files in recency order, skipping any that would repeat a path this call already names.
func (g *synthGen) addRecallPath(call ToolCall, slot recallSlot) ToolCall {
	for n := 1; n < synthRecallPathsPerTurn; n++ {
		p := g.preCompactionPath(slot.at, synthRecallPathsPerTurn*slot.slot+n)
		if p == "" || contains(call.Paths, p) {
			continue
		}
		call.Paths = append(call.Paths, p)
		if !contains(g.readPaths, p) {
			g.readPaths = append(g.readPaths, p)
		}
	}
	return call
}

// recallText is the half of the recall window that lives in a turn's prose: a quoted tool_use_id
// and a quoted decision id, both minted before the cut, which is what raises DemandToolResult and
// DemandDecision. Blocks.Demands matches them by substring, exactly as it does for a real
// transcript.
func (g *synthGen) recallText(slot recallSlot, recalling bool) string {
	if !recalling {
		return ""
	}
	switch slot.slot {
	case synthRecallToolSlot:
		// Reach PAST the working set the file slots re-read. A tool result whose file the recall
		// window was going to re-open anyway is repaired by a read that was already happening, so
		// the compacted branch's file set stays identical to the uncompacted one and
		// file_set_jaccard is 1.0 by construction — SP02-D2. The result an agent has to go back
		// for is the one that fell out of the working set, and its repair is a genuinely extra
		// read.
		if n := g.toolIDsAt[slot.at]; n > 0 {
			return " re-checking what " + g.toolIDs[max(n-1-recallSlots(g.spec.FileRereadRate), 0)] +
				" returned before the summary"
		}
	case synthRecallDecisionSlot:
		if n := g.decisionsAt[slot.at]; n > 0 {
			return " as already settled in [decision:" + g.decisions[n-1] + "]"
		}
	}
	return ""
}

// reAttemptCall turns the elimination slot's call into a re-attempt of an approach that was
// recorded as eliminated BEFORE the cut.
//
// Blocks.Demands matches a re-attempt on (target key, approach class) against the eliminations in
// the prefix, and skips RecordEliminated calls themselves — so this has to be an ordinary tool
// call carrying the same target and approach. When the session recorded no elimination before this
// compaction the call is left exactly as it was: an unmatched re-attempt would raise no demand and
// would only add noise.
func (g *synthGen) reAttemptCall(i, at int, call ToolCall) ToolCall {
	n := g.elimsAt[at]
	if n == 0 {
		return call
	}
	e := g.elims[(i+n-1)%n]
	if e.target == "" || e.approach == "" {
		return call
	}
	call.Paths = []string{e.target}
	call.Args = mustCompactJSON(map[string]string{
		"target":   e.target,
		"approach": e.approach,
		"reason":   "the summary dropped the elimination, so this approach looks untried",
	})
	return call
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
