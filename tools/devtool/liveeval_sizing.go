package main

// `devtool live-eval --size-from <pilot summary.json>` (V7 W6 item 2, coordinator decision D-d): the
// sizing step. It starts nothing: from a pilot's summary and the task set's pre-registered Δs and
// ICC_int (its analysis block) it prints each arm's n for the main run, the calendar estimate, and
// each efficiency test's approximate power at that n (amendment 4 item 8).

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/qompack/qompack/internal/eval"
)

// readLiveSummary reads a summary.json the sizing step takes, naming flag in its errors.
func readLiveSummary(flag, path string) (eval.LiveSummary, error) {
	var s eval.LiveSummary
	raw, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("live-eval: --%s: reading the pilot summary: %w", flag, err)
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("live-eval: --%s: parsing %s: %w", flag, path, err)
	}
	return s, nil
}

// runLiveSizing prints the sizing of the main run of o's task set from the pilot summary o.sizeFrom, and
// of the §9.8 Sonnet check from o.sizeSonnet when given.
func runLiveSizing(o liveOptions, w io.Writer) error {
	ts, _, err := eval.LoadLiveTaskSet(o.tasksFile)
	if err != nil {
		return err
	}
	pilot, err := readLiveSummary("size-from", o.sizeFrom)
	if err != nil {
		return err
	}
	var sonnet *eval.LiveSummary
	if o.sizeSonnet != "" {
		s, err := readLiveSummary("size-sonnet", o.sizeSonnet)
		if err != nil {
			return err
		}
		sonnet = &s
	}
	pace := eval.LiveSizingPacing{FiveHourGate: o.fiveHourGate, SevenDayCeiling: o.sevenDayCeiling, NightHours: o.night.hours()}
	sz, err := eval.SizeLive(ts.Analysis, len(ts.Tasks), pilot, sonnet, pace)
	if err != nil {
		return fmt.Errorf("live-eval: --size-from: %w", err)
	}
	k := len(ts.Tasks)
	fmt.Fprintf(w, "live-eval: --size-from %s: sizing the main run of task set %s (%d task(s), held-out included) from pilot "+
		"run %s; nothing is started\n", o.sizeFrom, ts.ID, k, orUnknown(pilot.RunID))
	fmt.Fprintf(w, "  pre-registered: Δ_H1 %.2f, Δ_H2 %.2f, S margin %.2f, ICC_int %.2f; two-sided α 0.05, power 0.8; the "+
		"pilot's unserved share u %.3f (served-empty included)\n", ts.Analysis.DeltaH1, ts.Analysis.DeltaH2,
		ts.Analysis.NonInferiorityMargin, sz.ICCInt, sz.U)
	for _, t := range sz.Tests {
		printLiveSizingTest(w, t)
	}
	var parts []string
	main := 0
	for _, arm := range eval.LiveArms {
		if n, ok := sz.ArmN[arm]; ok {
			parts = append(parts, fmt.Sprintf("%s %d", arm, n))
			main += n
		}
	}
	fmt.Fprintf(w, "  per-arm n, each the largest of its tests rounded up to a multiple of %d: %s = %d trial(s)\n", k,
		strings.Join(parts, ", "), main)
	if s := sz.Sonnet; s != nil {
		from := "from the Sonnet pilot " + o.sizeSonnet
		if !s.FromSonnetPilot {
			from = "from this pilot's rates, a planning stand-in (pass --size-sonnet <Sonnet pilot summary.json>)"
		}
		fmt.Fprintf(w, "  Sonnet check (plan §9.8), %s: classic and steer at n_H1 = %d each, %d trial(s)\n", from, s.N, 2*s.N)
		printLiveSizingTest(w, s.H1)
	}
	if r := sz.Redesign; r != nil {
		fmt.Fprintf(w, "  pilot steer/classic ratio of mean C %.3f and of mean T %.3f (mean C over trials with a known C, a "+
			"partial one at its lower bound); the brief redesign rule (plan §9.3 item 1) fires above %.2f: %s\n",
			r.CostRatio, r.TimeRatio, r.Threshold, map[bool]string{true: "FIRES", false: "does not fire"}[r.Fires])
	}
	c := sz.Calendar
	fmt.Fprintf(w, "  calendar (planning figure), %d trial(s) with the Sonnet check's %d: about %.0f h of sessions, %.1f "+
		"night(s) of %.1f h; %.1f five-hour window(s) at the gate %.2f; %.1f week(s) of seven-day allowance at the ceiling "+
		"%.2f; about %.1f week(s) in all (four weeks is 28 nights; above it, plan §9.3 item 3)\n", c.Trials, c.SonnetTrials,
		c.Hours, c.Nights, nightHoursOr(o.night), c.FiveHourWindows, o.fiveHourGate, c.SevenDayWeeks, o.sevenDayCeiling, c.Weeks)
	printLiveEfficiencyPower(w, "at the planned steer and classic n", sz.Efficiency)
	if s := sz.Sonnet; s != nil {
		printLiveEfficiencyPower(w, "of the Sonnet check's guards, at its n", s.Efficiency)
	}
	for _, n := range sz.Notes {
		fmt.Fprintf(w, "  note: %s\n", n)
	}
	return nil
}

// printLiveSizingTest prints one test's size.
func printLiveSizingTest(w io.Writer, t eval.LiveSizingTest) {
	rates := fmt.Sprintf("p₀ %.3f → p₁ %.3f", t.P0, t.P1)
	if t.Test == "S" {
		rates = fmt.Sprintf("p %.3f", t.P0)
	}
	fmt.Fprintf(w, "  %s (%s): %s (the pilot's 80 %% Wilson bound nearer 0.5): n₀ %d, after u %d, after the design "+
		"effect %d\n", t.Test, strings.Join(t.Arms, ", "), rates, t.N0, t.NU, t.N)
}

// printLiveEfficiencyPower prints each metric's power, the pilot's σ_w always with its df (review I-4).
func printLiveEfficiencyPower(w io.Writer, what string, power []eval.LiveEfficiencyPower) {
	if len(power) == 0 {
		return
	}
	fmt.Fprintf(w, "  efficiency power %s (SE ≈ √(2σ_w²/(K·m) + τ²/K), τ %.2f; it adds no trials):\n", what,
		eval.LiveSizingTau)
	for _, e := range power {
		ni := "H3 passes"
		if e.Metric == eval.LiveMetricActiveTime {
			ni = "time non-inferiority holds"
		}
		basis := "the pilot's σ_w"
		if e.Planning {
			basis = fmt.Sprintf("the planning band (the pilot's df %d < %d)", e.PilotDF, eval.LiveSizingMinDF)
		}
		fmt.Fprintf(w, "    %s at n = %d: the pilot's σ_w %.3f on %d df; power from %s:\n", e.Metric, e.N, e.PilotSigmaW,
			e.PilotDF, basis)
		for _, r := range e.Rows {
			fmt.Fprintf(w, "      σ_w %.3f, SE %.4f: a true +25 %% is caught as harm with P ≈ %.3f; a false harm at 1.0 ≈ "+
				"%.3f; %s at 1.0 with P ≈ %.3f\n", r.SigmaW, r.SE, r.HarmAt125, r.FalseHarmAt1, ni, r.NIAt1)
		}
	}
}

// hours is the night window's length, 0 when there is none.
func (n liveNightWindow) hours() float64 {
	if !n.set {
		return 0
	}
	mins := n.endMin - n.startMin
	if mins <= 0 {
		mins += 24 * 60
	}
	return float64(mins) / 60
}

// nightHoursOr is the night window's length, or the plan's planning figure when none is named.
func nightHoursOr(n liveNightWindow) float64 {
	if h := n.hours(); h > 0 {
		return h
	}
	return eval.LiveSizingNightHours
}
