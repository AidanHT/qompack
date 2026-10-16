package main

// The Stage A sections of summary.md (V7 W6 task 2): the steer arm's brief outcomes, and, once the
// decisions exist, the pre-registered tests and the reported rows.

import (
	"fmt"
	"strings"

	"github.com/qompack/qompack/internal/eval"
)

// renderLiveStageA writes the Stage A sections; nothing for a run that has none of them.
func renderLiveStageA(b *strings.Builder, s eval.LiveSummary) {
	if st := s.StageA; st != nil {
		fmt.Fprintf(b, "\n## Stage A decisions\n\nPre-registered (plan §9.4): every interval is the paired task bootstrap's "+
			"%.0f %% two-sided percentile interval, B = %d resamples of %d task(s) from seed %d, tasks resampled jointly "+
			"across arms. Newcombe intervals are reported beside, never decided.\n\n", 100*st.Confidence, st.B, st.Tasks, st.Seed)
		fmt.Fprintf(b, "**Outcome (§9.6):** %s.\n\n", st.Outcome)
		renderLiveTests(b, st.Tests, true)
		for _, n := range st.Notes {
			fmt.Fprintf(b, "\n- %s\n", n)
		}
		if len(st.Reported) > 0 {
			b.WriteString("\n## Reported, never decided\n\n")
			renderLiveTests(b, st.Reported, false)
		}
		if len(st.UtilizationFits) > 0 {
			b.WriteString("\nSubscription utilization delta against cost C, a least-squares line over trials with a complete C " +
				"(it tests C as the allowance's proxy; a loose fit means the docs say so):\n\n")
			b.WriteString("| window | trials | slope per USD | intercept | R² |\n|---|---|---|---|---|\n")
			for _, f := range st.UtilizationFits {
				fmt.Fprintf(b, "| %s | %d | %.4f | %.4f | %.3f |\n", f.Window, f.N, f.Slope, f.Intercept, f.R2)
			}
		}
	}
	renderLiveEfficiency(b, s)
	renderLiveBriefs(b, s)
}

// renderLiveTests writes a table of Stage A tests; decided tables carry the rule and whether the test
// is gated.
func renderLiveTests(b *strings.Builder, tests []eval.LiveTest, decided bool) {
	if decided {
		b.WriteString("| test | comparison | metric | estimate [interval] | Newcombe | rule | gated | label | why |\n" +
			"|---|---|---|---|---|---|---|---|---|\n")
	} else {
		b.WriteString("| row | comparison | metric | estimate [interval] | Newcombe | note |\n|---|---|---|---|---|---|\n")
	}
	for _, t := range tests {
		iv := "n/a"
		if t.Interval != nil {
			iv = fmt.Sprintf("%.3f [%.3f, %.3f]", t.Interval.Estimate, t.Interval.Low, t.Interval.High)
		}
		nc := "—"
		if t.Newcombe != nil {
			nc = fmt.Sprintf("[%.3f, %.3f]", t.Newcombe.Low, t.Newcombe.High)
		}
		cmp := t.Treated + " vs " + t.Comparator
		if decided {
			fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %t | %s | %s |\n", t.ID, cmp, t.Metric, iv, nc, t.Rule, t.Gated,
				t.Label, t.Reason)
			continue
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n", t.ID, cmp, t.Metric, iv, nc, t.Reason)
	}
}

// renderLiveEfficiency writes each arm's cost C and active time T (amendment 4, reported): the count
// of complete, partial and unknown costs, the mean C over the known ones, T split into API and local
// time, the steps that ended with no result, and the compactions.
func renderLiveEfficiency(b *strings.Builder, s eval.LiveSummary) {
	have := false
	for _, as := range s.Arms {
		have = have || (as.Efficiency != nil && s.StageA != nil)
	}
	if !have {
		return
	}
	b.WriteString("\n## Cost and active time per arm\n\nC is the host's list-price estimate: complete, a lower bound " +
		"(partial) or unknown, and an unknown C is never counted as zero. T sums each reached step's duration_ms, a " +
		"reached step with no result at the pinned step timeout. Reported here; the decisions above compare the arms.\n\n")
	b.WriteString("| arm | trials | C complete / partial / unknown | mean C USD | mean T s | API s | local s | timed-out steps | " +
		"compactions | compaction ms p50 / p95 | post_tokens p50 |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, arm := range liveSortedKeys(s.Arms) {
		e := s.Arms[arm].Efficiency
		if e == nil {
			continue
		}
		fmt.Fprintf(b, "| %s | %d | %d / %d / %d | %.4f | %.1f | %.1f | %.1f | %d | %d | %.0f / %.0f | %.0f |\n", arm, e.Trials,
			e.CostComplete, e.CostPartial, e.CostUnknown, e.MeanCostUSD, e.MeanActiveMS/1000, e.MeanAPIMS/1000,
			e.MeanLocalMS/1000, e.TimedOutSteps, e.Compactions, e.CompactionMS.P50, e.CompactionMS.P95, e.PostTokens.P50)
	}
}

// renderLiveBriefs writes each arm's brief outcomes (W6 item 5): compactions by class, the unserved
// share with served-empty beside it, and the trials with no served brief.
func renderLiveBriefs(b *strings.Builder, s eval.LiveSummary) {
	have := false
	for _, as := range s.Arms {
		have = have || as.Brief != nil
	}
	if !have {
		return
	}
	b.WriteString("\n## Brief outcomes\n\nEach compaction of a steer trial matched to its brief record: unserved is cold, " +
		"busy, timeout, error or missing (no record); served-empty steered nothing and is its own class. Every trial " +
		"counts as assigned (intention to treat); reported, not decided.\n\n")
	b.WriteString("| arm | trials | compactions | unserved share | served-empty share | no-served-brief trials | by class | " +
		"served chars mean / p50 |\n|---|---|---|---|---|---|---|---|\n")
	for _, arm := range liveSortedKeys(s.Arms) {
		st := s.Arms[arm].Brief
		if st == nil {
			continue
		}
		var classes []string
		for _, c := range eval.LiveBriefClasses {
			if n := st.Outcomes[c]; n > 0 {
				classes = append(classes, fmt.Sprintf("%s %d", c, n))
			}
		}
		fmt.Fprintf(b, "| %s | %d | %d | %.2f | %.2f | %d | %s | %.0f / %.0f |\n", arm, st.Trials, st.Compactions,
			st.UnservedShare, st.ServedEmptyShare, st.NoServedTrials, orNone(strings.Join(classes, ", ")),
			st.ServedChars.Mean, st.ServedChars.P50)
	}
}

// orNone renders an empty list as "none".
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
