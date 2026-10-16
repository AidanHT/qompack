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
	renderLiveBriefs(b, s)
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
