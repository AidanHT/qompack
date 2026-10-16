package main

// `devtool live-eval --describe` (V7 W6 item 10): the run as a reader should see it before it is
// started, written as Markdown from the same files and settings the run itself reads, so the
// description cannot drift from the run. It starts nothing and writes no run directory.

import (
	"fmt"
	"strings"

	"github.com/qompack/qompack/internal/eval"
)

// liveDescription is what --describe renders.
type liveDescription struct {
	opts        liveOptions
	plan        eval.LivePlan
	set         eval.LiveTaskSet
	tasks       []eval.LiveTask
	hostVersion string
	departures  []string
}

// render writes the description.
func (d liveDescription) render() string {
	var b strings.Builder
	p, o, ts := d.plan, d.opts, d.set
	fmt.Fprintf(&b, "# Live evaluation `%s`\n\n", ts.ID)
	if ts.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", ts.Description)
	}
	b.WriteString("Written by `devtool live-eval --describe` from the files and settings a run with these flags reads. " +
		"It started nothing.\n\n")

	b.WriteString("## Run\n\n")
	fmt.Fprintf(&b, "- Task set: `%s` (sha256 `%s`), %d task(s) declared, %d selected; held-out tasks %s.\n",
		p.TaskSetFile, p.TaskSetSHA256, p.TaskSetTasks, len(d.tasks), map[bool]string{true: "included", false: "left out"}[p.HeldOutIncluded])
	fmt.Fprintf(&b, "- Fixture tree %v, manifest sha256 `%s`.\n", p.FixtureTreeDirs, p.FixtureTreeSHA256)
	fmt.Fprintf(&b, "- Model: `%s` (pre-registered `%s`).\n", p.Model, p.PreregisteredModel)
	fmt.Fprintf(&b, "- Host: Claude Code %s at `%s`, pinned: before every trial the host's version is compared with "+
		"this one and a difference stops the run; every session and every `claude` command runs with `%s=1`.\n",
		d.hostVersion, orUnknown(p.ClaudeCLI), liveEnvAutoUpdater)
	fmt.Fprintf(&b, "- Plugin install: `%s`.\n", p.Install)
	fmt.Fprintf(&b, "- Daemon idle exit: %d s; step timeout %s; session timeout %s; max sessions %s.\n",
		p.DaemonIdleExitSeconds, o.stepTimeout, o.sessionTimeout, orNoCap(o.maxSessions))
	fmt.Fprintf(&b, "- Rate table for the list-price estimate: %s.\n", p.RateTableDate)
	if p.KnownDefects != nil {
		fmt.Fprintf(&b, "- Known open defects in the bundles: %s (the operator's statement, not machine-checked).\n",
			knownDefectsText(p.KnownDefects))
	}
	if len(d.departures) == 0 {
		b.WriteString("- Pre-registered confirmatory design: met at plan time.\n")
	} else {
		fmt.Fprintf(&b, "- Pre-registered confirmatory design: not met (%s).\n", strings.Join(d.departures, "; "))
	}

	renderLiveDescribeDecisions(&b, ts.Analysis)

	pace := o.livePacing()
	b.WriteString("\n## Pacing\n\n")
	fmt.Fprintf(&b, "- Five-hour gate %.2f: no trial starts while the newest `rate_limit_event` puts the five-hour window "+
		"at or above it, or rejected; the run waits for its reset (plus %s) when that falls inside the night window, "+
		"and pauses otherwise.\n", pace.FiveHourGate, liveResetMargin)
	fmt.Fprintf(&b, "- Seven-day ceiling %.2f: while any seven-day window is at or above it, or rejected, the run pauses "+
		"until it resets (the owner keeps %.0f %%).\n", pace.SevenDayCeiling, 100*(1-pace.SevenDayCeiling))
	fmt.Fprintf(&b, "- Night window: %s; outside it no trial starts and the run pauses.\n", o.night)
	b.WriteString("- Paid overage: any event with `isUsingOverage` or `overageInUse` true stops the run.\n")
	b.WriteString("- A trial a limit cuts short (a rejected event, or a usage-limit result) is set aside as " +
		"`trial.limited-<n>.json`, excluded from the analysis and run again in its slot; a pause is a clean exit, " +
		"and `devtool live-eval --resume <run-dir>` continues the plan.\n")

	b.WriteString("\n## Arms\n\n")
	b.WriteString("| arm | trials per task | plugin | bundle | mod enabled | mod steering | CLAUDE.md compact section | store before the session |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	yn := map[bool]string{true: "yes", false: "no"}
	for _, s := range p.ArmStates {
		bundle := "none"
		if s.Bundle != "" {
			bundle = "`--" + s.Bundle + "`"
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s | %s | %s |\n", s.Arm, p.ArmTrials[s.Arm], yn[s.Plugin], bundle,
			yn[s.ModEnabled], yn[s.ModSteering], yn[s.ClaudeMDCompactSection], yn[s.StorePrecreated])
	}
	b.WriteString("\nEach arm's session environment, set by the harness:\n\n")
	for _, s := range p.ArmStates {
		fmt.Fprintf(&b, "- %s: `%s`\n", s.Arm, strings.Join(liveSessionEnv(s, p.DaemonIdleExitSeconds), "` `"))
	}
	for _, id := range []struct {
		flag string
		pi   *eval.LivePluginIdentity
	}{{eval.LiveBundleClassic, p.Plugin}, {eval.LiveBundleMods, p.PluginMods}} {
		if id.pi == nil {
			continue
		}
		fmt.Fprintf(&b, "\n`--%s` (%s): Qompack %s at commit `%s` (dirty %t), mod sources %s, %s sha256 `%s`, at `%s`.\n",
			id.flag, strings.Join(liveArmsUsing(p.ArmStates, id.flag), ", "), id.pi.Version, id.pi.Commit, id.pi.Dirty,
			map[bool]string{true: "present", false: "absent"}[id.pi.Mods], identityFileName, id.pi.BundleSHA256, id.pi.BundleDir)
	}
	for _, s := range p.ArmStates {
		if s.ClaudeMDCompactSection {
			fmt.Fprintf(&b, "\nThe %s arm's project root `CLAUDE.md` gains this section (created when the fixture has "+
				"none, appended after a blank line otherwise), text G from the hashed task set:\n\n````markdown\n%s````\n",
				s.Arm, liveGenericClaudeMD(nil, ts.Analysis.GenericCompactInstructions))
		}
	}

	b.WriteString("\n## Tasks\n")
	for _, t := range d.tasks {
		fmt.Fprintf(&b, "\n### `%s`: %s\n\n", t.ID, t.Title)
		fmt.Fprintf(&b, "Category `%s`, variant `%s`", t.Category, t.Variant)
		if t.Domain != "" {
			fmt.Fprintf(&b, ", domain `%s`", t.Domain)
		}
		fmt.Fprintf(&b, ", held out %s. Fixture `%s`", yn[t.HeldOut], t.Fixture)
		if t.HiddenFixture != "" {
			fmt.Fprintf(&b, "; hidden fixture `%s`, copied over the project after the session and before any check", t.HiddenFixture)
		}
		fmt.Fprintf(&b, ". At most %d turns per message; allowed tools: `%s`.\n\nSteps, each sent by every arm:\n\n",
			t.EffectiveMaxTurns(ts.Defaults), strings.Join(t.EffectiveAllowedTools(ts.Defaults), "`, `"))
		for i, s := range t.Steps {
			if s.Compact {
				fmt.Fprintf(&b, "%d. `%s`: a compaction, sent as `%s`\n", i+1, s.ID, s.Message())
				continue
			}
			fmt.Fprintf(&b, "%d. `%s`: the prompt\n\n   ```text\n%s\n   ```\n", i+1, s.ID, indentLines(s.Prompt, "   "))
		}
		b.WriteString("\nChecks, graded by a program after the session:\n\n")
		for _, c := range t.Checks {
			fmt.Fprintf(&b, "- `%s` (%s): %s", c.ID, c.Outcome, describeLiveCheck(c))
			if c.Description != "" {
				fmt.Fprintf(&b, " — %s", c.Description)
			}
			b.WriteString(".\n")
		}
	}

	b.WriteString("\n## Trial order\n\n")
	if o.orderSeed == 0 {
		fmt.Fprintf(&b, "%d trial(s). The order seed derives from the run id, which is drawn when the run starts, so "+
			"the order is not known yet: pass `--order-seed <n>` to fix it and see it here.\n", len(p.Trials))
	} else {
		fmt.Fprintf(&b, "%d trial(s) in balanced blocks of rounds, shuffled within each block from order seed %d:\n\n",
			len(p.Trials), p.OrderSeed)
		for i, t := range p.Trials {
			fmt.Fprintf(&b, "%d. %s / %s / %d\n", i+1, t.Task, t.Arm, t.Trial)
		}
	}
	return b.String()
}

// renderLiveDescribeDecisions writes the pre-registered decision rules a four-arm set carries (W6
// item 2, amendment 4): the interval, S's margin, the sizing's Δs and ICC_int, and the efficiency
// block. A set without them gets nothing.
func renderLiveDescribeDecisions(b *strings.Builder, a eval.LiveAnalysis) {
	if a.Efficiency == nil {
		return
	}
	b.WriteString("\n## Pre-registered decisions\n\n")
	fmt.Fprintf(b, "- Every interval is the paired task bootstrap's %.0f %% two-sided percentile interval: B = %d resamples, "+
		"seed %d, tasks resampled jointly across arms.\n", 100*a.Confidence, eval.LiveBootstrapB, eval.LiveBootstrapSeed)
	b.WriteString("- Fixed sequence: H1 steer > classic on recovery, then H2 steer > generic, then H3 (below); S: steer " +
		"non-inferior to classic on task success, ")
	if a.SFallback {
		fmt.Fprintf(b, "the 20th percentile above -%.2f (the pre-registered fallback).\n", a.NonInferiorityMargin)
	} else {
		fmt.Fprintf(b, "the lower bound above -%.2f.\n", a.NonInferiorityMargin)
	}
	fmt.Fprintf(b, "- Sizing: Δ_H1 %.2f, Δ_H2 %.2f, ICC_int %.2f.\n", a.DeltaH1, a.DeltaH2, a.ICCInt)
	e := a.Efficiency
	fmt.Fprintf(b, "- Efficiency (amendment 4): margin %.2f on %s; cost C is the host's list-price estimate with "+
		"missing costs imputed adversely, active time T caps a step with no result at %d ms.\n", e.Margin,
		strings.Join(e.Metrics, " and "), e.StepTimeoutMS)
	for _, g := range e.Guards {
		kind := "a label for the docs, not gating"
		if g.Gated {
			kind = "gated: harm shown, or indeterminate, blocks shipping"
		}
		fmt.Fprintf(b, "  - %s vs %s: harm shown when the ratio's lower bound exceeds %.2f (%s).\n", g.Treated, g.Comparator,
			1+e.Margin, kind)
	}
	fmt.Fprintf(b, "- H3, steering's cost non-inferior to classic's (upper bound below %.2f): %s. Pilot brief redesign "+
		"ratio %.2f.\n", 1+e.Margin, map[bool]string{true: "a ship condition", false: "a claim, not a ship condition"}[e.H3ShipCondition],
		e.PilotRedesignRatio)
}

// describeLiveCheck says in plain words what a check looks for.
func describeLiveCheck(c eval.LiveCheck) string {
	switch c.Kind {
	case eval.CheckFileMatches:
		return fmt.Sprintf("the file `%s` matches `%s`", c.Path, c.Pattern)
	case eval.CheckFileNotMatches:
		return fmt.Sprintf("the file `%s` does not match `%s`", c.Path, c.Pattern)
	case eval.CheckFileExists:
		return fmt.Sprintf("the file `%s` exists", c.Path)
	case eval.CheckFileAbsent:
		return fmt.Sprintf("the file `%s` does not exist", c.Path)
	case eval.CheckFileUnchanged:
		return fmt.Sprintf("the file `%s` is byte-identical to the fixture's", c.Path)
	case eval.CheckFileLines:
		n := 0
		if c.Lines != nil {
			n = *c.Lines
		}
		return fmt.Sprintf("the file `%s` has exactly %d non-empty lines", c.Path, n)
	case eval.CheckAnswerMatches:
		return fmt.Sprintf("the answer to step `%s` matches `%s`", c.Step, c.Pattern)
	case eval.CheckAnswerNotMatches:
		return fmt.Sprintf("the answer to step `%s` does not match `%s`", c.Step, c.Pattern)
	case eval.CheckCommand:
		return fmt.Sprintf("`%s` exits 0 in the project root", strings.Join(c.Argv, " "))
	case eval.CheckToolNotUsedAfter:
		return fmt.Sprintf("no tool call after step `%s` matches `%s` (matched against the tool's name and input JSON; "+
			"Qompack's own lookups excepted)", c.Step, c.Pattern)
	}
	return "kind `" + c.Kind + "`"
}

func indentLines(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func orNoCap(n int) string {
	if n == 0 {
		return "no cap"
	}
	return fmt.Sprint(n)
}
