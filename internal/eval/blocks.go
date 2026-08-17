package eval

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Block ID prefixes. The grammar is hash-free wherever a stable natural key exists, so a golden
// keep-set stays readable and a failing diff names the thing that changed rather than a digest.
const (
	toolBlockPrefix     = "tu:"
	turnBlockPrefix     = "turn:"
	fileBlockPrefix     = "file:"
	elimBlockPrefix     = "elim:"
	decisionBlockPrefix = "dec:"
)

// elimHashDomain keys the elimination block ID. An elimination has no natural short identifier —
// it is a (target, approach-class) pair — so it is the one block kind whose ID is a digest.
const elimHashDomain = "qompack.eval.elim.v1"

// toolRecordEliminated is the tool call that records a negative-knowledge elimination.
const toolRecordEliminated = "record_eliminated"

// Fixed weights for the two derived block kinds extracted from text. Neither is a Qompack
// tunable: they are the modelled cost of restoring one elimination or one decision into a
// rehydrated context, and changing them changes what every historical baseline means.
const (
	eliminationBlockTokens core.Tokens = 64
	decisionBlockTokens    core.Tokens = 128
)

// bytesPerTokenEstimate is the fallback used when a tool result carries no explicit token count.
// It is deliberately the same crude divisor the host itself uses, because Blocks models the
// session as it was actually logged, not as a better estimator would have counted it.
const bytesPerTokenEstimate = 4

// fileBlockTools is the set of tool names whose paths become file blocks: the tools that put file
// content into the transcript.
var fileBlockTools = map[string]bool{
	"FileRead": true,
	"Read":     true,
	"Edit":     true,
	"Write":    true,
}

// decisionMarker matches the [decision:<id>] marker a turn's text uses to mint a decision.
var decisionMarker = regexp.MustCompile(`\[decision:([A-Za-z0-9_-]+)\]`)

func toolBlockID(id core.ToolUseID) string { return toolBlockPrefix + string(id) }
func turnBlockID(i core.TurnIndex) string  { return turnBlockPrefix + strconv.Itoa(int(i)) }
func fileBlockID(key string) string        { return fileBlockPrefix + key }
func decisionBlockID(id string) string     { return decisionBlockPrefix + id }

// elimBlockID hashes the (target, approach-class) pair an elimination is identified by, so a
// re-attempt phrased differently still resolves to the same block.
func elimBlockID(target, approach string) string {
	key := paths.Key(target) + "\x00" + approachClass(approach)
	return elimBlockPrefix + core.HashBytes(elimHashDomain, []byte(key)).Short()
}

// approachClass is the normal form an approach is compared under: lowercased, whitespace runs
// collapsed to a single '-', everything outside [a-z0-9-] stripped. "Widen  Pool Timeout!"
// becomes "widen-pool-timeout". It is idempotent, which is what lets it be used as a map key on
// both sides of the elimination match.
func approachClass(approach string) string {
	var b strings.Builder
	b.Grow(len(approach))
	pendingSep := false
	for _, r := range strings.ToLower(approach) {
		if unicode.IsSpace(r) {
			pendingSep = true
			continue
		}
		if pendingSep {
			b.WriteByte('-')
			pendingSep = false
		}
		if r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// toolResultTokens weighs one tool result: the payload's own "tokens" field when it carries one,
// and len/4 rounded up otherwise.
func toolResultTokens(raw json.RawMessage) core.Tokens {
	if len(raw) == 0 {
		return 0
	}
	var probe struct {
		Tokens *int `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && probe.Tokens != nil && *probe.Tokens > 0 {
		return core.Tokens(*probe.Tokens)
	}
	return core.Tokens((len(raw) + bytesPerTokenEstimate - 1) / bytesPerTokenEstimate)
}

// eliminationArgs reads the target and approach out of a record_eliminated call's arguments.
func eliminationArgs(raw json.RawMessage) (target, approach string) {
	var a struct {
		Target   string `json:"target"`
		Approach string `json:"approach"`
	}
	if len(raw) == 0 {
		return "", ""
	}
	_ = json.Unmarshal(raw, &a)
	return a.Target, a.Approach
}

// decisionIDs returns every decision id minted in text, in order of appearance.
func decisionIDs(text string) []string {
	if !strings.Contains(text, "[decision:") {
		return nil
	}
	m := decisionMarker.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, g[1])
	}
	return out
}

// clampTurn bounds i to [0, len(turns)].
func clampTurn(i core.TurnIndex, n int) int {
	switch {
	case int(i) < 0:
		return 0
	case int(i) > n:
		return n
	default:
		return int(i)
	}
}

// Blocks derives every keepable unit of s's prefix over turns [0, upTo).
//
// Per turn it emits, in order: the turn's own message block, one block per tool result, one file
// block per path key first seen here, one block per recorded elimination, and one block per
// decision minted in the turn's text.
//
// Pos advances only on the first two kinds — the bytes that actually occupy the context window.
// A derived block takes the position of the block it came from, so nothing is counted into a
// position twice and p_min is always a real offset into the original prefix. Tokens, by contrast,
// is what restoring the block costs and is carried by derived blocks too: the budget a policy
// decides under is a retention budget, not a prefix measurement.
func Blocks(s Session, upTo core.TurnIndex) []Block {
	end := clampTurn(upTo, len(s.Turns))

	out := make([]Block, 0, end*2)
	seenFile := make(map[string]bool)
	pos := 0

	// toolPos and toolTokens carry each tool call's position and weight from step 2 to steps 3
	// and 4, which derive from it.
	var toolPos []int
	var toolTokens []core.Tokens

	for i := range end {
		turn := s.Turns[i]
		turnPos := pos

		kind := BlockAssistant
		if turn.Role == roleUser {
			kind = BlockUserPrompt
		}
		out = append(out, Block{
			ID: turnBlockID(turn.Index), Kind: kind, Turn: turn.Index,
			Pos: turnPos, Tokens: turn.Tokens,
		})
		pos += int(turn.Tokens)

		toolPos = toolPos[:0]
		toolTokens = toolTokens[:0]
		for _, tc := range turn.ToolCalls {
			tk := toolResultTokens(tc.Result)
			toolPos = append(toolPos, pos)
			toolTokens = append(toolTokens, tk)
			out = append(out, Block{
				ID: toolBlockID(tc.ID), Kind: BlockToolResult, Turn: turn.Index,
				Pos: pos, Tokens: tk,
			})
			pos += int(tk)
		}

		for j, tc := range turn.ToolCalls {
			if !fileBlockTools[tc.Name] {
				continue
			}
			for _, p := range tc.Paths {
				key := paths.Key(p)
				if key == "" || seenFile[key] {
					continue
				}
				seenFile[key] = true
				out = append(out, Block{
					ID: fileBlockID(key), Kind: BlockFile, Turn: turn.Index,
					Pos: toolPos[j], Tokens: toolTokens[j], Paths: []string{key},
				})
			}
		}

		for j, tc := range turn.ToolCalls {
			if tc.Name != toolRecordEliminated {
				continue
			}
			target, approach := eliminationArgs(tc.Args)
			b := Block{
				ID: elimBlockID(target, approach), Kind: BlockElimination, Turn: turn.Index,
				Pos: toolPos[j], Tokens: eliminationBlockTokens,
			}
			if target != "" {
				b.Paths = []string{paths.Key(target)}
			}
			out = append(out, b)
		}

		for _, id := range decisionIDs(turn.Text) {
			out = append(out, Block{
				ID: decisionBlockID(id), Kind: BlockDecision, Turn: turn.Index,
				Pos: turnPos, Tokens: decisionBlockTokens,
			})
		}
	}
	return out
}

// priorKnowledge is what existed before a compaction: everything a later turn is allowed to
// demand back.
type priorKnowledge struct {
	fileKeys  map[string]bool
	toolIDs   []core.ToolUseID
	decisions []string
	elims     map[string]string // (target key, approach class) -> elimination block ID
}

// knowledgeUpTo indexes every block produced at a turn <= from.
func knowledgeUpTo(s Session, from core.TurnIndex) priorKnowledge {
	k := priorKnowledge{fileKeys: map[string]bool{}, elims: map[string]string{}}
	end := clampTurn(from+1, len(s.Turns))
	for i := range end {
		turn := s.Turns[i]
		for _, tc := range turn.ToolCalls {
			if tc.ID != "" {
				k.toolIDs = append(k.toolIDs, tc.ID)
			}
			if fileBlockTools[tc.Name] {
				for _, p := range tc.Paths {
					if key := paths.Key(p); key != "" {
						k.fileKeys[key] = true
					}
				}
			}
			if tc.Name == toolRecordEliminated {
				target, approach := eliminationArgs(tc.Args)
				k.elims[elimMatchKey(target, approach)] = elimBlockID(target, approach)
			}
		}
		k.decisions = append(k.decisions, decisionIDs(turn.Text)...)
	}
	return k
}

// elimMatchKey is the pair an elimination and a later re-attempt are matched on.
func elimMatchKey(target, approach string) string {
	return paths.Key(target) + "\x00" + approachClass(approach)
}

// Demands returns everything the session went on to need over turns (from, to), restricted to
// content that already existed at turn from.
//
// Demands are the only notion of "was needed after the compaction" in this package: FractionOfOPT
// counts them, and both the policy's keep-set and OPT's are scored against the same set. The
// window opens at from+1 because turn from is the compaction turn itself.
//
// The result is deduplicated on (Turn, BlockID) and ordered by it, so a turn that touches the
// same file twice cannot inflate a score and two runs always produce the identical slice.
func Demands(s Session, from, to core.TurnIndex) []Demand {
	start := clampTurn(from+1, len(s.Turns))
	end := clampTurn(to, len(s.Turns))
	if start >= end {
		return nil
	}
	k := knowledgeUpTo(s, from)

	seen := make(map[string]bool)
	var out []Demand
	add := func(turn core.TurnIndex, id string, kind DemandKind) {
		key := strconv.Itoa(int(turn)) + "\x00" + id
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Demand{Turn: turn, BlockID: id, Kind: kind})
	}

	for i := start; i < end; i++ {
		turn := s.Turns[i]

		for _, tc := range turn.ToolCalls {
			for _, p := range tc.Paths {
				if key := paths.Key(p); k.fileKeys[key] {
					add(turn.Index, fileBlockID(key), DemandFileContent)
				}
			}
		}

		if turn.Text != "" {
			for _, id := range k.toolIDs {
				if strings.Contains(turn.Text, string(id)) {
					add(turn.Index, toolBlockID(id), DemandToolResult)
				}
			}
			for _, id := range k.decisions {
				if strings.Contains(turn.Text, "[decision:"+id+"]") {
					add(turn.Index, decisionBlockID(id), DemandDecision)
				}
			}
		}

		for _, tc := range turn.ToolCalls {
			if tc.Name == toolRecordEliminated {
				continue
			}
			target := ""
			if len(tc.Paths) > 0 {
				target = tc.Paths[0]
			}
			_, approach := eliminationArgs(tc.Args)
			if approach == "" {
				continue
			}
			if id, ok := k.elims[elimMatchKey(target, approach)]; ok {
				add(turn.Index, id, DemandElimination)
			}
		}
	}

	sort.Slice(out, func(a, b int) bool {
		if out[a].Turn != out[b].Turn {
			return out[a].Turn < out[b].Turn
		}
		return out[a].BlockID < out[b].BlockID
	})
	return out
}
