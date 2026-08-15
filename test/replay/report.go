package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/qompack/qompack/internal/eval"
)

// generatorID identifies the driver that produced a report or a baseline.
const generatorID = "test/replay/1"

// cacheControlMarkers is the Messages API's cache_control marker budget, and the only place that
// number is written. The breakpoint analysis is measurement-and-port material (§5.6, §12), so this
// is what an optimal placement would be scored against if Qompack ever ran on that API — not
// something the plugin does.
const cacheControlMarkers = 4

// Corpus tiers, per 00-ARCHITECTURE.md §6.3. The committed Phase 0 number is computed over the
// synthetic corpus and says so, because reporting a synthetic number as if it were a real-session
// number is exactly the dishonest measurement §1.3 RC-3 indicts.
const (
	tierSynthetic = "synthetic"
	tierRecorded  = "recorded"
)

// latencyModelled is what every report says about its latency numbers in deterministic mode. They
// are computed from a model, never observed, and the tag exists so nobody can mistake one for a
// wall-clock measurement.
const latencyModelled = "modelled"

// DriverReport is the envelope around eval.Report.
//
// eval.Report carries exactly 00-ARCHITECTURE.md §5.18's five fields, so everything else this
// driver is obliged to disclose — the corpus tier and hash, the modelled-latency tag, budget
// violations, sessions where nothing was demanded, the breakpoint plan, the growth verdict — lives
// here instead of being bolted onto a type the architecture froze.
type DriverReport struct {
	Generator        string                        `json:"generator"`
	Corpus           string                        `json:"corpus"`
	CorpusTier       string                        `json:"corpusTier"`
	CorpusSHA256     string                        `json:"corpusSHA256"`
	Sessions         int                           `json:"sessions"`
	Latency          string                        `json:"latency"`
	Policies         map[string]map[string]float64 `json:"policies"`
	WatchFor         map[string]float64            `json:"watchFor"`
	RetrievalActions map[string]int                `json:"retrievalActions"`
	BudgetViolations []string                      `json:"budgetViolations"`
	NoDemandSessions []string                      `json:"noDemandSessions"`
	Breakpoint       eval.BreakpointPlan           `json:"breakpoint"`
	Growth           eval.GrowthResult             `json:"growth"`
	PhaseChecked     int                           `json:"phaseChecked"`

	PhaseChecksSkipped bool              `json:"phaseChecksSkipped"`
	Report             eval.Report       `json:"report"`
	Regressions        []eval.Regression `json:"regressions"`
	Sketch             eval.SketchHealth `json:"sketch"`
}

// baselineFile is the committed Phase 0 answer, and the subset of a DriverReport that has to be
// byte-identical between two runs.
//
// It deliberately omits GeneratedAt, Regressions and anything else wall-clock dependent: a
// baseline that changed on every run could not be compared to anything.
type baselineFile struct {
	Generator    string                        `json:"generator"`
	Corpus       string                        `json:"corpus"`
	CorpusTier   string                        `json:"corpusTier"`
	CorpusSHA256 string                        `json:"corpusSHA256"`
	Sessions     int                           `json:"sessions"`
	Latency      string                        `json:"latency"`
	Policies     map[string]map[string]float64 `json:"policies"`
	WatchFor     map[string]float64            `json:"watchFor"`
}

// toBaseline projects a run onto the committed form.
func (d DriverReport) toBaseline() baselineFile {
	return baselineFile{
		Generator:    d.Generator,
		Corpus:       d.Corpus,
		CorpusTier:   d.CorpusTier,
		CorpusSHA256: d.CorpusSHA256,
		Sessions:     d.Sessions,
		Latency:      d.Latency,
		Policies:     d.Policies,
		WatchFor:     d.WatchFor,
	}
}

// encodeJSON renders a value the way every JSON writer in this codebase does.
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("replay: encoding the report: %w", err)
	}
	return buf.Bytes(), nil
}

// canonicalMetrics renders the per-policy metric map in a stable, wall-clock-free form. Two full
// replays of the same corpus must produce byte-identical output, which is how the Phase 0
// reproducibility criterion is actually checked rather than asserted.
func canonicalMetrics(policies map[string]map[string]float64) []byte {
	names := make([]string, 0, len(policies))
	for name := range policies {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, "%q:{", name)
		for j, metric := range eval.MetricNames() {
			if j > 0 {
				buf.WriteByte(',')
			}
			fmt.Fprintf(&buf, "%q:%.6f", metric, policies[name][metric])
		}
		buf.WriteByte('}')
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// firstDiffLine reports where two canonical renderings first disagree, so a reproducibility
// failure names the metric rather than dumping two long strings.
func firstDiffLine(a, b []byte) string {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			lo := max(i-60, 0)
			hiA := min(i+60, len(a))
			hiB := min(i+60, len(b))
			return fmt.Sprintf("  first run: …%s…\n  second run: …%s…", a[lo:hiA], b[lo:hiB])
		}
	}
	if len(a) != len(b) {
		return fmt.Sprintf("  lengths differ: %d vs %d bytes", len(a), len(b))
	}
	return "  (identical)"
}
