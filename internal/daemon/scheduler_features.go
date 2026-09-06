package daemon

import (
	"strings"
	"unicode"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/scheduler"
)

// observer must not import scheduler (00-ARCHITECTURE.md §3.2), so the translation from
// observer.Signals to scheduler.Features lives in this composition root and is owned by SP-12.

// defaultFeatureWindow is the number of turns per sliding window when NewFeatureHistory is
// given no positive window.
const defaultFeatureWindow = 8 // turns per sliding window

// maxShingles bounds the lexical-cohesion allocation. 4096 is in §11.6's forbidden integer
// set, so the annotation is mandatory — it is a memory bound, not a config default.
const maxShingles = 4096 //nomagic:allow bounded-allocation cap, not an Appendix C default

// msPerSecond converts a core.UnixMilli delta into GapSeconds.
const msPerSecond = 1_000

// FeatureHistory is the sliding-window state FeaturesFrom computes against: the most recent
// `window` turns and the `window` turns before those, plus the previous observation's
// timestamp. It is not safe for concurrent use; the Runtime holds the lock.
type FeatureHistory struct {
	window int
	recent turnWindow // most recent `window` turns
	prior  turnWindow // the `window` turns before those
	lastTS core.UnixMilli
	seen   int // observations so far; GapSeconds is 0 until the second one

	// pendingText is the ArgsPreview-style text staged by observeText for the next
	// FeaturesFrom call, which consumes and clears it. FeaturesFrom's signature carries no text
	// parameter, so this is how the tap hands the lexical channel across.
	pendingText string
}

// turnObs is one turn's observation as the windows store it.
type turnObs struct {
	paths    map[string]struct{} // paths.Key form
	tool     string
	text     string
	shingles []string // distinct lowercased bigram shingles of text, ≤ maxShingles, computed once
}

// turnWindow is one window of per-turn observations. All four slices share one length and are
// pre-allocated to the window size, so a push never reallocates: when full, the oldest entry is
// shifted out and returned to the caller.
type turnWindow struct {
	paths    []map[string]struct{} // paths.Key form, one set per turn
	tools    []string
	text     []string
	shingles [][]string
}

func newTurnWindow(n int) turnWindow {
	return turnWindow{
		paths:    make([]map[string]struct{}, 0, n),
		tools:    make([]string, 0, n),
		text:     make([]string, 0, n),
		shingles: make([][]string, 0, n),
	}
}

// push appends one turn. When the window is full it first evicts its oldest turn and reports
// it, so the caller can roll it into the next window.
func (w *turnWindow) push(o turnObs) (evicted turnObs, ok bool) {
	if n := len(w.paths); n > 0 && n == cap(w.paths) {
		evicted = turnObs{paths: w.paths[0], tool: w.tools[0], text: w.text[0], shingles: w.shingles[0]}
		ok = true
		copy(w.paths, w.paths[1:])
		copy(w.tools, w.tools[1:])
		copy(w.text, w.text[1:])
		copy(w.shingles, w.shingles[1:])
		w.paths, w.tools, w.text, w.shingles = w.paths[:n-1], w.tools[:n-1], w.text[:n-1], w.shingles[:n-1]
	}
	w.paths = append(w.paths, o.paths)
	w.tools = append(w.tools, o.tool)
	w.text = append(w.text, o.text)
	w.shingles = append(w.shingles, o.shingles)
	return evicted, ok
}

// NewFeatureHistory returns an empty history holding two windows of `window` turns each.
// window <= 0 selects defaultFeatureWindow.
func NewFeatureHistory(window int) *FeatureHistory {
	if window <= 0 {
		window = defaultFeatureWindow
	}
	return &FeatureHistory{window: window, recent: newTurnWindow(window), prior: newTurnWindow(window)}
}

// init makes a zero-value FeatureHistory usable, so a struct literal behaves like
// NewFeatureHistory(0) rather than indexing an empty window.
func (h *FeatureHistory) init() {
	if h.window <= 0 {
		h.window = defaultFeatureWindow
		h.recent = newTurnWindow(h.window)
		h.prior = newTurnWindow(h.window)
	}
}

// observeText stages the ArgsPreview-style text of the observation the next FeaturesFrom call
// will append. It is the lexical channel: FeaturesFrom's signature carries paths, tool and time
// but no text, so the tap calls observeText(h, rec.ArgsPreview) immediately before FeaturesFrom.
// Calling it twice before a FeaturesFrom keeps the later text; FeaturesFrom clears it.
func observeText(h *FeatureHistory, text string) {
	if h == nil {
		return
	}
	h.pendingText = text
}

// FeaturesFrom appends one turn's observation and returns the current feature vector.
// It mutates h and is not safe for concurrent use; the Runtime holds the lock.
//
// A nil h is treated as a fresh, single-use history: the vector of one observation against an
// empty prior window.
func FeaturesFrom(h *FeatureHistory, sig observer.Signals, tool string, ts core.UnixMilli) scheduler.Features {
	if h == nil {
		h = NewFeatureHistory(0)
	}
	h.init()

	// sig.Paths are RAW (observer §5.21): normalize to paths.Key form before set membership.
	pset := make(map[string]struct{}, len(sig.Paths))
	for _, p := range sig.Paths {
		if p == "" {
			continue
		}
		pset[paths.Key(p)] = struct{}{}
	}
	text := h.pendingText
	h.pendingText = ""

	// Window roll: the oldest entry of recent moves into prior, and prior drops its oldest, so
	// both windows hold at most `window` turns.
	obs := turnObs{paths: pset, tool: tool, text: text, shingles: shingleList(text, maxShingles)}
	if evicted, ok := h.recent.push(obs); ok {
		h.prior.push(evicted)
	}

	var gap float64
	if h.seen > 0 && ts > h.lastTS {
		gap = float64(ts-h.lastTS) / msPerSecond
	}
	h.lastTS = ts
	h.seen++

	var todo float64
	if sig.TodoCompleted || sig.TestPassed || sig.GitCommit {
		todo = 1
	}

	return scheduler.Features{
		PathJaccard:     pathJaccard(h.recent, h.prior),
		ToolShift:       toolShift(h.recent, h.prior),
		LexicalCohesion: lexicalCohesion(h.recent, h.prior),
		GapSeconds:      gap,
		TodoTransition:  todo,
	}
}

// pathJaccard is |R ∩ P| / |R ∪ P| over the unions of the two windows' path sets. An empty
// union is 1.0: no evidence of a shift, and a changepoint must not be manufactured from silence.
func pathJaccard(recent, prior turnWindow) float64 {
	np := 0
	for _, set := range prior.paths {
		np += len(set)
	}
	priorUnion := make(map[string]struct{}, np)
	for _, set := range prior.paths {
		for k := range set {
			priorUnion[k] = struct{}{}
		}
	}
	nr := 0
	for _, set := range recent.paths {
		nr += len(set)
	}
	recentUnion := make(map[string]struct{}, nr)
	inter := 0
	for _, set := range recent.paths {
		for k := range set {
			if _, dup := recentUnion[k]; dup {
				continue
			}
			recentUnion[k] = struct{}{}
			if _, ok := priorUnion[k]; ok {
				inter++
			}
		}
	}
	union := len(priorUnion) + len(recentUnion) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

// toolShift is the total-variation distance 0.5·Σ_t |p_t − q_t| between the two windows'
// tool-name distributions. A turn with no tool name carries no mass; either window without
// mass is 0.0. The result is in [0, 1] by construction.
func toolShift(recent, prior turnWindow) float64 {
	counts := make(map[string][2]int, len(recent.tools)+len(prior.tools))
	nr, np := 0, 0
	for _, t := range recent.tools {
		if t == "" {
			continue
		}
		c := counts[t]
		c[0]++
		counts[t] = c
		nr++
	}
	for _, t := range prior.tools {
		if t == "" {
			continue
		}
		c := counts[t]
		c[1]++
		counts[t] = c
		np++
	}
	if nr == 0 || np == 0 {
		return 0
	}
	var sum float64
	for _, c := range counts {
		d := float64(c[0])/float64(nr) - float64(c[1])/float64(np)
		if d < 0 {
			d = -d
		}
		sum += d
	}
	tv := sum / 2
	return min(max(tv, 0), 1)
}

// lexicalCohesion is the Jaccard similarity over lowercased whitespace-token bigram shingles of
// the two windows' text, each window's shingle set capped at maxShingles. Either side empty is
// 1.0. It is computed on every call but only enters the detector when an operator adds
// "lexical" to changepoint.features (Appendix C's default set omits it).
func lexicalCohesion(recent, prior turnWindow) float64 {
	rs := windowShingles(recent, maxShingles)
	ps := windowShingles(prior, maxShingles)
	if len(rs) == 0 || len(ps) == 0 {
		return 1
	}
	inter := 0
	for k := range rs {
		if _, ok := ps[k]; ok {
			inter++
		}
	}
	union := len(rs) + len(ps) - inter
	return float64(inter) / float64(union)
}

// windowShingles is the union of a window's per-turn shingle lists, capped at limit entries so
// the transient set is bounded however many turns the window holds.
func windowShingles(w turnWindow, limit int) map[string]struct{} {
	n := 0
	for _, s := range w.shingles {
		n += len(s)
	}
	set := make(map[string]struct{}, min(n, limit))
	for _, s := range w.shingles {
		for _, k := range s {
			if len(set) >= limit {
				return set
			}
			set[k] = struct{}{}
		}
	}
	return set
}

// shingleList returns the distinct lowercased whitespace-token bigrams of text in first-seen
// order. Scanning stops after limit bigrams have been produced, which bounds both the work and
// the allocation regardless of the input size; the result therefore never holds more than limit
// shingles. Empty text yields nil.
func shingleList(text string, limit int) []string {
	if limit <= 0 || text == "" {
		return nil
	}
	var (
		out      []string
		seen     map[string]struct{}
		prev     string
		start    = -1
		produced = 0
	)
	for i := 0; i <= len(text); i++ {
		atSpace := i == len(text) || isSpaceByte(text[i])
		if !atSpace {
			if start < 0 {
				start = i
			}
			continue
		}
		if start < 0 {
			continue
		}
		tok := strings.ToLower(text[start:i])
		start = -1
		if prev != "" {
			key := prev + " " + tok
			if seen == nil {
				seen = make(map[string]struct{})
			}
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				out = append(out, key)
			}
			produced++
			if produced >= limit {
				return out
			}
		}
		prev = tok
	}
	return out
}

// isSpaceByte reports whether b is ASCII whitespace. A byte of a non-ASCII rune is treated as a
// token byte, so multi-byte runes stay inside their token.
func isSpaceByte(b byte) bool {
	return b < unicode.MaxASCII && unicode.IsSpace(rune(b))
}
