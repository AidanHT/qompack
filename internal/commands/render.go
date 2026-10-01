package commands

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/obs"
)

// Rendering widths. They are layout constants, not tunable values: the columns are sized to the
// longest name each can hold ("UserPromptSubmit", "observe stop --subagent", "checkpoint_finalize")
// so no row ever wraps and every golden file diffs cleanly.
const (
	colEvent = 18
	// colSubcommand fits "qompack observe stop --subagent", the longest installed invocation.
	colSubcommand = 32
	colBudget     = 7
	colBudgetID   = 5
	colHist       = 21
	colMeasure    = 11
)

// RenderStatus writes the human-readable status report.
//
// The output carries no ANSI escapes and no wall-clock reads: everything it prints comes from rep,
// which was collected once against an injected clock. That is what lets the same function produce
// a terminal page and a committed golden file.
//
// Every number it prints is accompanied by where it came from, how old it is, and whether it was
// measured or derived. A status page that shows a figure without those is not a smaller version of
// this one — it is a different and less supportable claim.
func RenderStatus(w io.Writer, rep StatusReport) error {
	rw := &errWriter{w: w}

	rw.printf("qompack status — collected %s\n\n",
		time.UnixMilli(rep.CollectedAtMS).UTC().Format(time.RFC3339))
	renderPrimary(rw, rep.Primary)

	if rep.Snapshot != nil {
		renderSnapshot(rw, *rep.Snapshot)
	}

	renderHooks(rw, rep.Hooks)
	renderBudgets(rw, rep.Budgets)

	return rw.err
}

// renderPrimary prints where the whole report came from, which qualifies everything below it.
func renderPrimary(rw *errWriter, p Provenance) {
	rw.printf("source: %s (%s%s)\n", p.Source, p.Status, ageSuffix(p.AgeMS))
	if p.Reason != "" {
		for _, line := range wrapReason(p.Reason) {
			rw.printf("  %s\n", line)
		}
	}
	rw.printf("\n")
}

// renderSnapshot prints the parts of the daemon's own payload status has always shown.
func renderSnapshot(rw *errWriter, s DaemonStatus) {
	renderContract(rw, s.Contract)

	rw.printf("mode:        %s\n", orUnknown(s.Mode))
	rw.printf("hot path:    %s\n", orUnknown(s.Hot))
	if s.Hot == hotSpool {
		renderSpoolSubmode(rw)
	}
	rw.printf("sessions:    %d\n", len(s.Sessions))
	rw.printf("spool files: %d\n", s.SpoolFiles)

	if len(s.Counters) > 0 {
		rw.printf("\ncounters\n")
		for _, k := range sortedKeys(s.Counters) {
			rw.printf("  %-40s %d\n", k, s.Counters[k])
		}
	}

	if len(s.Budgets) > 0 {
		rw.printf("\nbudget breaches\n")
		for _, b := range s.Budgets {
			rw.printf("  %-*s observed %s over limit %s for %d window(s)\n",
				colBudgetID, b.Budget, dur(b.Observed), dur(b.Limit), b.Windows)
		}
	}

	if len(s.LoudTail) > 0 {
		rw.printf("\nrecent loud lines\n")
		for _, l := range s.LoudTail {
			rw.printf("  %s\n", l)
		}
	}
	rw.printf("\n")
}

// hotSpool is the daemon's status word for spool submode (internal/daemon hotModeString).
const hotSpool = "spool"

// renderSpoolSubmode explains a `hot path: spool` line where the user reads it (D53(c)): on a slow
// disk it is the designed behaviour of a long session and loses nothing, so the page says what
// happened, what ends it and what tunes it, rather than leaving a bare word next to the loud line.
func renderSpoolSubmode(rw *errWriter) {
	for _, part := range []string{
		obs.SpoolSubmodeWhat + ".",
		"It lasts until " + obs.SpoolSubmodeUntil + ".",
		"To tune it: " + obs.SpoolSubmodeTune + ".",
	} {
		for _, line := range wrapWords(part, reasonWidth) {
			rw.printf("  %s\n", line)
		}
	}
}

// wrapWords splits text into lines of at most width bytes at spaces; a word longer than width gets a
// line of its own.
func wrapWords(text string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		switch {
		case line == "":
			line = w
		case len(line)+1+len(w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// renderContract prints the host-contract banner.
//
// contract.Result carries Expected and Observed precisely so a degraded session can lead with what
// the host promised next to what was actually seen (internal/contract's own doc comment says so).
// A count of failures without that pair tells a user something is wrong and nothing about what.
//
// A row that is not failing is not therefore holding (contract.StandingOf): one still waiting for
// its observation is pending, and one with nothing to judge is neither. "all holding" is printed
// only when every row reports something actually seen; otherwise the banner counts the three and
// names each pending row with what it is waiting on. The candidate 4 live re-run read "9
// assertion(s), all holding" beside two rows still pending (UAT-01, D50).
func renderContract(rw *errWriter, results []contract.Result) {
	if len(results) == 0 {
		rw.printf("host contract: no assertions reported\n\n")
		return
	}

	var failed, pending []contract.Result
	holding, idle := 0, 0
	for _, r := range results {
		switch contract.StandingOf(r) {
		case contract.StandingFailing:
			failed = append(failed, r)
		case contract.StandingPending:
			pending = append(pending, r)
		case contract.StandingIdle:
			idle++
		default:
			holding++
		}
	}
	if holding == len(results) {
		rw.printf("host contract: %d assertion(s), all holding\n\n", len(results))
		return
	}

	counts := fmt.Sprintf("%d holding, %d pending, %d with nothing to judge", holding, len(pending), idle)
	if len(failed) == 0 {
		rw.printf("host contract: %d assertion(s), none failing: %s\n", len(results), counts)
	} else {
		rw.printf("host contract: %d of %d assertion(s) FAILING; %s\n", len(failed), len(results), counts)
	}
	for _, r := range failed {
		rw.printf("  %s (%s)\n", r.ID, severityText(r.Severity))
		rw.printf("    expected: %s\n", orUnknown(r.Expected))
		rw.printf("    observed: %s\n", orUnknown(r.Observed))
		if r.Detail != "" {
			for _, line := range wrapReason(r.Detail) {
				rw.printf("    %s\n", line)
			}
		}
	}
	for _, r := range pending {
		rw.printf("  pending: %s (%s)\n", r.ID, r.Observed)
	}
	rw.printf("\n")
}

// severityText renders a contract severity for a person.
//
// internal/contract deliberately ships no String method — its own severity.go says §5.19 leaves
// the user-facing spelling to /qompack:status rather than pre-empting it — so the mapping lives
// here, in the package that does the rendering.
//
// The words matter more than they look. Only an observed critical failure degrades the session
// (§12.1); a warn is surfaced and changes nothing. Printing them identically would make a
// cosmetic finding read like a degraded session, and the numeric value would be worse than either.
func severityText(s contract.Severity) string {
	switch s {
	case contract.SevInfo:
		return "info"
	case contract.SevWarn:
		return "warn — surfaced, session not degraded"
	case contract.SevCritical:
		return "critical — degrades the session when observed"
	}
	return "unknown severity"
}

// renderHooks prints one row per installed hook entry point.
func renderHooks(rw *errWriter, rows []HookRow) {
	rw.printf("hooks — per-entry-point latency\n")
	if len(rows) == 0 {
		rw.printf("  (no hook entry points installed)\n\n")
		return
	}
	rw.printf("  %-*s %-*s %-*s %s\n",
		colEvent, "EVENT", colSubcommand, "SUBCOMMAND", colBudget, "BUDGET", "LATENCY")

	for _, r := range rows {
		rw.printf("  %-*s %-*s %-*s %s\n",
			colEvent, r.Event,
			colSubcommand, "qompack "+r.Subcommand,
			colBudget, orDash(r.Budget),
			latencyText(r.Latency, r.Provenance))
		if r.Latency == nil && r.Provenance.Reason != "" {
			for _, line := range wrapReason(r.Provenance.Reason) {
				rw.printf("  %-*s   %s\n", colEvent, "", line)
			}
		}
	}
	rw.printf("\n")
}

// renderBudgets prints one row per §2.4 budget, with the scope of what each one mixes.
func renderBudgets(rw *errWriter, rows []BudgetRow) {
	rw.printf("budgets — §2.4\n")
	if len(rows) == 0 {
		rw.printf("  (no budgets declared)\n")
		return
	}
	rw.printf("  %-*s %-*s %-*s %s\n",
		colBudgetID, "ID", colHist, "HISTOGRAM", colMeasure, "MEASURE", "LATENCY")

	for _, r := range rows {
		rw.printf("  %-*s %-*s %-*s %s\n",
			colBudgetID, string(r.ID),
			colHist, r.Hist,
			colMeasure, measureText(r.Measure, r.Latency),
			latencyText(r.Latency, r.Provenance))

		if r.Aggregate {
			rw.printf("  %-*s   scope: %s\n", colBudgetID, "", scopeText(r.Covers))
		}
		if !r.Gated {
			rw.printf("  %-*s   reported only, never gated\n", colBudgetID, "")
		}
		if r.Latency == nil && r.Provenance.Reason != "" {
			for _, line := range wrapReason(r.Provenance.Reason) {
				rw.printf("  %-*s   %s\n", colBudgetID, "", line)
			}
		}
	}
}

// latencyText renders one latency cell, or the reason there is no number in it.
//
// An absent measurement prints the availability word, never a zero. "p99 0µs" and "nothing
// measured this" are different claims, and a reader cannot tell them apart once the second has
// been rendered as the first.
func latencyText(l *Latency, p Provenance) string {
	if l == nil {
		return string(p.Status)
	}
	return fmt.Sprintf("p50 %s  p95 %s  p99 %s  max %s  n=%d  (%s from %s%s)",
		durUS(l.P50US), durUS(l.P95US), durUS(l.P99US), durUS(l.MaxUS), l.N,
		l.Measure, p.Source, ageSuffix(p.AgeMS))
}

// measureText renders how a row's number was arrived at, or a dash when there is no number.
//
// "observed" against an empty latency cell is the wrong claim in a subtle way: it describes how a
// value WOULD have been arrived at, next to the statement that none was. B-D reads that way — the
// budget table declares it observed, and nothing in the tree records it — so the column is blanked
// whenever the row has no reading to characterize.
func measureText(m Measure, l *Latency) string {
	if l == nil {
		return "—"
	}
	return string(m)
}

// scopeText names what an aggregate row mixes.
func scopeText(covers []string) string {
	if len(covers) == 0 {
		return "aggregate; not attributable to a single hook entry point"
	}
	return fmt.Sprintf("aggregate over %d entry point(s): %s", len(covers), strings.Join(covers, ", "))
}

// ageSuffix renders an observation's age, or says the age is unknown.
func ageSuffix(ms *int64) string {
	if ms == nil {
		return ", age unknown"
	}
	return ", " + dur(time.Duration(*ms)*time.Millisecond) + " old"
}

// durUS renders a microsecond count.
func durUS(us int64) string { return dur(time.Duration(us) * time.Microsecond) }

// dur renders a duration at a fixed resolution so two runs produce identical bytes.
func dur(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%.2fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.2fm", d.Minutes())
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
}

// reasonWidth is how wide a wrapped explanatory line may be. It is a layout constant.
const reasonWidth = 84

// wrapReason splits a reason into lines short enough to read next to a table.
func wrapReason(reason string) []string {
	var lines []string
	for _, part := range strings.Split(reason, "; ") {
		for len(part) > reasonWidth {
			cut := strings.LastIndex(part[:reasonWidth], " ")
			if cut <= 0 {
				cut = reasonWidth
			}
			lines = append(lines, part[:cut])
			part = strings.TrimSpace(part[cut:])
		}
		if part != "" {
			lines = append(lines, part)
		}
	}
	return lines
}

// orUnknown renders an empty string as an explicit unknown rather than as blank space.
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// orDash renders an empty string as a dash.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// sortedKeys returns m's keys in sorted order, so a map never reorders the output between runs.
func sortedKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// errWriter collects the first write error so the renderer reads as straight-line code instead of
// checking every Fprintf.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, args...)
}
