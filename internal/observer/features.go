package observer

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// The five cheap features of §6.6, computed from sessionState.Recent and delivered through
// Options.OnFeatures for the daemon to translate into scheduler.Features. Every one of them is
// O(window) with a fixed window, which is what keeps BOCD's input off the latency budget.

// bytesPerCohesionToken is how many bytes of a result are retained per token the lexical-cohesion
// feature will score. It bounds the memory one feature window pins; four or five bytes is a
// typical token, and eight leaves headroom without pinning a whole session's output.
const bytesPerCohesionToken = 8

// millisPerSecond converts the package's UnixMilli timestamps to the seconds GapSeconds reports.
const millisPerSecond = 1000

// todoWriteTool is the display name whose newest appearance turns the todo-transition feature on.
const todoWriteTool = "TodoWrite"

// reCohesionToken is the token shape the lexical-cohesion feature scores: identifiers and words,
// which is what TextTiling-style cohesion is actually about. Numbers and punctuation runs carry no
// topic signal and would make two unrelated stack traces look cohesive.
var reCohesionToken = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// recordRecent appends one event to the BOCD feature window, evicting from the front at
// 2*featureWindow entries.
//
// It deliberately does NOT touch st.LastTS. LastTS is the timestamp of the PREVIOUS event and is
// assigned as the last bookkeeping statement of each entry point, after features has been
// consulted; assigning it here would make GapSeconds identically zero, which is the single easiest
// way to silently disable BOCD's time feature.
func (o *observer) recordRecent(st *sessionState, tool string, paths []string, body []byte, ts core.UnixMilli) {
	text := body
	if len(text) > cohesionTokenCap*bytesPerCohesionToken {
		text = text[:cohesionTokenCap*bytesPerCohesionToken]
	}

	st.Recent = append(st.Recent, recentEvent{
		Tool:  tool,
		Paths: append([]string(nil), paths...),
		Text:  append([]byte(nil), text...),
		TS:    ts,
	})
	if k := len(st.Recent) - 2*featureWindow; k > 0 {
		st.Recent = st.Recent[k:]
	}
}

// features computes the §6.6 sample, reporting false until two full windows are in hand — there is
// nothing to compare a single window against.
//
// Every returned value is finite: each divisor below is guarded, so NaN and ±Inf are impossible.
func (o *observer) features(st *sessionState, now core.UnixMilli) (FeatureSample, bool) {
	n := len(st.Recent)
	if n < 2*featureWindow {
		return FeatureSample{}, false
	}
	cur := st.Recent[n-featureWindow:]
	prev := st.Recent[n-2*featureWindow : n-featureWindow]

	f := FeatureSample{
		Turn:            st.Turn,
		TS:              now,
		PathJaccard:     jaccard(pathSet(cur), pathSet(prev)),
		ToolShift:       totalVariation(toolDistribution(cur), toolDistribution(prev)),
		LexicalCohesion: cosineTokens(concatText(cur), concatText(prev)),
	}
	if st.LastTS != 0 {
		f.GapSeconds = float64(now-st.LastTS) / millisPerSecond
		if f.GapSeconds < 0 {
			f.GapSeconds = 0
		}
	}
	if st.Recent[n-1].Tool == todoWriteTool && st.TodoTransitioned {
		f.TodoTransition = 1
	}
	return f, true
}

// pathSet is the set of paths a window touched.
func pathSet(window []recentEvent) map[string]struct{} {
	set := make(map[string]struct{}, len(window))
	for _, e := range window {
		for _, p := range e.Paths {
			set[p] = struct{}{}
		}
	}
	return set
}

// toolDistribution is a window's normalized tool-name distribution.
func toolDistribution(window []recentEvent) map[string]float64 {
	if len(window) == 0 {
		return nil
	}
	counts := make(map[string]float64, len(window))
	for _, e := range window {
		counts[e.Tool]++
	}
	total := float64(len(window))
	for k := range counts {
		counts[k] /= total
	}
	return counts
}

// concatText joins a window's retained result bytes.
func concatText(window []recentEvent) []byte {
	var out []byte
	for _, e := range window {
		out = append(out, e.Text...)
		out = append(out, '\n')
	}
	return out
}

// jaccard is |a ∩ b| / |a ∪ b|. Two EMPTY sets score 1 — no locality shift happened — and one
// empty set against a non-empty one scores 0, which is a complete shift.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

// totalVariation is 0.5 · Σ|p_i − q_i| over the union of keys: 0 for identical distributions and 1
// for disjoint ones.
func totalVariation(a, b map[string]float64) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	sum := 0.0
	for k, p := range a {
		sum += math.Abs(p - b[k])
	}
	for k, q := range b {
		if _, ok := a[k]; !ok {
			sum += q
		}
	}
	return 0.5 * sum
}

// cosineTokens is the cosine similarity of two term-frequency vectors over lowercased identifier
// tokens, capped at cohesionTokenCap tokens per side. Either side having no tokens scores 0.
func cosineTokens(a, b []byte) float64 {
	va, vb := termFrequencies(a), termFrequencies(b)
	if len(va) == 0 || len(vb) == 0 {
		return 0
	}

	var dot, na, nb float64
	for term, ca := range va {
		na += ca * ca
		if cb, ok := vb[term]; ok {
			dot += ca * cb
		}
	}
	for _, cb := range vb {
		nb += cb * cb
	}
	if na == 0 || nb == 0 {
		return 0
	}

	cos := dot / (math.Sqrt(na) * math.Sqrt(nb))
	switch {
	case cos < 0:
		return 0
	case cos > 1:
		return 1
	default:
		return cos
	}
}

// termFrequencies counts the first cohesionTokenCap lowercased tokens of b.
func termFrequencies(b []byte) map[string]float64 {
	if len(b) == 0 {
		return nil
	}
	matches := reCohesionToken.FindAll(b, cohesionTokenCap)
	if len(matches) == 0 {
		return nil
	}
	tf := make(map[string]float64, len(matches))
	for _, m := range matches {
		tf[strings.ToLower(string(m))]++
	}
	return tf
}

// todoTransitionInput is the tolerant shape newlyCompletedTodos reads. It carries content as well
// as status, unlike signals.go's todoInput, because a TRANSITION is a comparison against the
// contents already known to be done.
type todoTransitionInput struct {
	Todos []struct {
		Content string `json:"content"`
		Status  string `json:"status"`
	} `json:"todos"`
}

// newlyCompletedTodos reports whether e completed a todo that was not already complete, and
// records every completed item it saw.
//
// This is the transition detector that lets ExtractSignals stay pure: §5.21 requires that function
// to be a function of the payload alone, and "newly completed" is a fact about the session.
func (o *observer) newlyCompletedTodos(st *sessionState, e Event) bool {
	st.TodoTransitioned = false
	if NormalizeToolName(e.ToolName) != todoWriteTool {
		return false
	}

	var in todoTransitionInput
	if err := json.Unmarshal(e.ToolInput, &in); err != nil {
		return false
	}

	fired := false
	for _, todo := range in.Todos {
		if todo.Status != todoStatusCompleted || todo.Content == "" {
			continue
		}
		if !st.TodoDone[todo.Content] {
			fired = true
		}
		st.TodoDone[todo.Content] = true
	}
	st.TodoTransitioned = fired
	return fired
}
