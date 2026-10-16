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

// runLiveSizing prints the sizing of the main run of o's task set from the pilot summary o.sizeFrom.
func runLiveSizing(o liveOptions, w io.Writer) error {
	ts, _, err := eval.LoadLiveTaskSet(o.tasksFile)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(o.sizeFrom)
	if err != nil {
		return fmt.Errorf("live-eval: --size-from: reading the pilot summary: %w", err)
	}
	var pilot eval.LiveSummary
	if err := json.Unmarshal(raw, &pilot); err != nil {
		return fmt.Errorf("live-eval: --size-from: parsing %s: %w", o.sizeFrom, err)
	}
	pace := eval.LiveSizingPacing{FiveHourGate: o.fiveHourGate, SevenDayCeiling: o.sevenDayCeiling, NightHours: o.night.hours()}
	sz, err := eval.SizeLive(ts.Analysis, len(ts.Tasks), pilot, pace)
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
		rates := fmt.Sprintf("p₀ %.3f → p₁ %.3f", t.P0, t.P1)
		if t.Test == "S" {
			rates = fmt.Sprintf("p %.3f", t.P0)
		}
		fmt.Fprintf(w, "  %s (%s): %s (the pilot's 80 %% Wilson bound nearer 0.5): n₀ %d, after u %d, after the design "+
			"effect %d\n", t.Test, strings.Join(t.Arms, ", "), rates, t.N0, t.NU, t.N)
	}
	var parts []string
	for _, arm := range eval.LiveArms {
		if n, ok := sz.ArmN[arm]; ok {
			parts = append(parts, fmt.Sprintf("%s %d", arm, n))
		}
	}
	fmt.Fprintf(w, "  per-arm n, each the largest of its tests rounded up to a multiple of %d: %s = %d trial(s)\n", k,
		strings.Join(parts, ", "), sz.Calendar.Trials)
	for _, t := range sz.Tests {
		if t.Test == "H1" {
			fmt.Fprintf(w, "  Sonnet check (plan §9.8), sized from a Sonnet pilot's rates: classic and steer at n_H1 = %d each\n",
				eval.RoundToTasks(t.N, k))
		}
	}
	c := sz.Calendar
	fmt.Fprintf(w, "  calendar (planning figure): about %.0f h of sessions, %.1f night(s) of %.1f h; %.1f five-hour window(s) at the "+
		"gate %.2f; %.1f week(s) of seven-day allowance at the ceiling %.2f; about %.1f week(s) in all (four weeks is 28 nights; "+
		"above it, plan §9.3 item 3)\n", c.Hours, c.Nights, nightHoursOr(o.night), c.FiveHourWindows, o.fiveHourGate,
		c.SevenDayWeeks, o.sevenDayCeiling, c.Weeks)
	if len(sz.Efficiency) > 0 {
		fmt.Fprintf(w, "  efficiency power at the planned steer and classic n (SE ≈ √(2σ_w²/(K·m) + τ²/K), τ %.2f; it adds no "+
			"trials):\n", eval.LiveSizingTau)
		for _, e := range sz.Efficiency {
			ni := "H3 passes"
			if e.Metric == eval.LiveMetricActiveTime {
				ni = "time non-inferiority holds"
			}
			fmt.Fprintf(w, "    %s: σ_w %.3f, SE %.4f; a true +25 %% is caught as harm with P ≈ %.3f; a false harm at 1.0 "+
				"≈ %.3f; %s at 1.0 with P ≈ %.3f\n", e.Metric, e.SigmaW, e.SE, e.HarmAt125, e.FalseHarmAt1, ni, e.NIAt1)
		}
	}
	for _, n := range sz.Notes {
		fmt.Fprintf(w, "  note: %s\n", n)
	}
	return nil
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
