package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/stretchr/testify/require"
)

// This file is gates M6-G15-A and M6-G15-B: the state-aware loop warnings measured on the thirteen
// held-out streams in testdata/phase6, and the ablation of Sequitur against the simpler state
// signature on exactly the same input.
//
// ── WHAT THESE ROWS PROVE, AND WHAT THEY DO NOT ────────────────────────────────────────────
//
// They prove the SIX BOUNDS of contract §5 on this corpus, and each of the six is a property of
// the output that can be checked exactly: no stream produces more than MaxPerSession warnings, no
// dedup key is delivered twice, a warning produced from unknown coverage is always marked
// uncertain and always says so in the line the user reads, a warning is refused at delivery once
// the session has moved past its expiry, Qompack's own traffic never reaches the detector's input
// OR its repeat counter, and — the one with the runaway failure mode — feeding every delivered
// warning back in as the session's next thirty turns produces exactly zero further warnings.
//
// They do NOT establish a false-alarm rate. THIRTEEN HAND-AUTHORED STREAMS ARE A SAMPLE SIZE, NOT
// A POPULATION. The counts below are exact statements about this corpus and nothing more: the
// streams were written by role E after roles A-D landed, from what ordinary and stuck sessions in
// this repository look like, and no implementation file was changed to make any of them come out a
// particular way. A false-alarm count of zero here does not mean the detector does not cry wolf in
// the field; it means it did not cry wolf on seven ordinary streams that were built to make it.
//
// Nor do they establish that a warning HELPS. Usefulness here is the held-out label fed back
// through RecordOutcome — a warning on a stuck stream is counted useful and a warning on a
// progress stream is counted a false alarm — which is a statement about whether the detector
// agreed with the label, not about whether the session that received the warning did better. The
// separation from raw repeated-action frequency, which is what contract §5.6 actually requires, is
// asserted; the value of speaking is not, and this corpus cannot establish it.
//
// THE ABLATION IS ALLOWED TO COME OUT AGAINST SEQUITUR, and does. Blocker M6-U15-sequitur-value
// says Sequitur stays optional and disabled until it demonstrably earns its place, so nothing here
// asserts anything about it: the arms are measured, the verdict is computed from the measurement
// by p6Verdict, and it is recorded rather than gated. The fixtures were not adjusted afterwards.
//
// There is NO SEED. The detector holds no clock, does no I/O and draws no random number, and the
// grammar arms are equally deterministic; reproducibility is asserted directly by running every
// arm twice and comparing its decision vector.

// p6UncertainMessage is amendment A7's uncertain wording, spelled out here rather than read from
// the package that produces it.
//
// The duplication is the point, and it is internal/canon/sketchwire_test.go's "a consumer must be
// able to disagree with the producer" pattern: a test that asked grammar for the string it was
// about to assert would pass no matter what that string became, including if it silently became
// the confident one. Bound 3 says an uncertain warning may never read like a confident finding, so
// the sentence the user actually sees is pinned here, in the file that checks it.
const p6UncertainMessage = "observation coverage is incomplete, so this may not be a loop"

// p6ConfidentMessage is the confident wording, which is 00-ARCHITECTURE.md §14.1's own example
// string verbatim. It is pinned for the same reason and against the opposite failure: a detector
// that hedged every warning would make the qualification meaningless in the other direction.
const p6ConfidentMessage = "consider a different approach"

// TestPhase6_HeldOutFalseAlarmAndUsefulnessReport is gate M6-G15-A's report half: every held-out
// stream, what the shipped detector said about it, and the two counts that matter.
//
// FALSE ALARMS ARE REPORTED, NOT HIDDEN. The test logs the confusion matrix in full — including
// any stream the detector missed and any ordinary stream it fired on — and asserts only that every
// delivered warning carried a held-out verdict, so the two columns add up to the deliveries. It
// does not assert a detection rate or a false-alarm rate: a threshold on either would turn thirteen
// synthetic streams into a specification, which is precisely the move the plan forbids when it says
// no default may be flipped by a synthetic score.
func TestPhase6_HeldOutFalseAlarmAndUsefulnessReport(t *testing.T) {
	t.Parallel()

	streams := p6Streams(t)
	run := runSignatureArm(streams)
	bd := measureBoundedDelivery(streams, run)

	var lines strings.Builder
	for _, o := range run.Result.Streams {
		fmt.Fprintf(&lines, "\n  %-38s label=%-8s verdict=%-12s delivered=%d uncertain=%d first-turn=%d",
			o.Stream, o.Label, o.Verdict, o.Delivered, o.Uncertain, o.FirstTurn)
	}
	t.Logf("M6-G15-A held-out report over %d streams (%d stuck, %d progress); "+
		"minRepeats=%d window=%d maxPerSession=%d expiry=%d:%s\n"+
		"  detected=%d/%d  MISSED=%d  FALSE ALARMS=%d/%d  delivered=%d\n"+
		"  separated counters: repeated-states=%d (raw frequency) vs useful=%d false-alarms=%d "+
		"(judged); suppressed by progress=%d self=%d dedup=%d cap=%d; expired at delivery=%d",
		len(streams), run.Result.Stuck, run.Result.Progress,
		run.Config.MinRepeats, int(run.Config.WindowTurns), run.Config.MaxPerSession,
		int(run.Config.ExpiryTurns), lines.String(),
		run.Result.Detected, run.Result.Stuck, run.Result.Missed,
		run.Result.FalseAlarms, run.Result.Progress, run.Result.Delivered,
		bd.RepeatedStates, bd.Useful, bd.FalseAlarms,
		bd.ProgressSuppressed, bd.SelfSuppressed, bd.DedupCollapsed, bd.CapSuppressed,
		bd.ExpiredAtDelivery)
	writeSP15Artifact(t, "phase6-heldout.json", struct {
		Config grammar.DetectorConfig `json:"config"`
		Arm    p6ArmResult            `json:"arm"`
		Bounds p6BoundedDelivery      `json:"bounds"`
	}{run.Config, run.Result, bd})

	require.Equal(t, run.Result.Delivered, bd.Useful+bd.FalseAlarms,
		"every delivered warning carries a held-out verdict, or the report is describing a "+
			"different set of warnings than the one that was delivered")
	require.NotEqual(t, bd.RepeatedStates, bd.Useful+bd.FalseAlarms,
		"contract §5.6 requires usefulness to be counted SEPARATELY from raw repeated-action "+
			"frequency; two counters that always agree are one counter with two names")
}

// TestPhase6_BoundedDeliveryUnderLoad is gate M6-G15-A's delivery half: the four bounds that keep a
// warning stream from becoming the thing filling the context window.
//
// It runs on the held-out corpus rather than on a synthetic burst on purpose. A burst of ten
// thousand identical observations would demonstrate the cap and nothing else; the two-interleaved
// stream demonstrates that two genuinely distinct loops each get one warning and neither gets two,
// which is the behaviour a real session depends on.
func TestPhase6_BoundedDeliveryUnderLoad(t *testing.T) {
	t.Parallel()

	streams := p6Streams(t)
	run := runSignatureArm(streams)
	bd := measureBoundedDelivery(streams, run)
	cfg := run.Config

	for _, s := range streams {
		produced := run.Warnings[s.Name]
		require.LessOrEqual(t, len(produced), cfg.MaxPerSession,
			"%s: %d warnings delivered against MaxPerSession=%d", s.Name, len(produced), cfg.MaxPerSession)

		seen := make(map[string]bool, len(produced))
		for _, w := range produced {
			require.False(t, seen[w.DedupKey], "%s: dedup key %s delivered twice", s.Name, w.DedupKey)
			seen[w.DedupKey] = true
			require.GreaterOrEqual(t, w.Repeats, cfg.MinRepeats,
				"%s: a warning fired at %d repeats", s.Name, w.Repeats)

			// Expiry is enforced at DELIVERY, not at production: the detector runs on PostToolUse
			// and the warning is injected on the next UserPromptSubmit, and the session may have
			// gone somewhere else in between.
			d := run.Detectors[s.Name]
			require.True(t, d.Deliverable(w, w.ExpiresAt), "%s: a warning must survive to its expiry", s.Name)
			require.False(t, d.Deliverable(w, w.ExpiresAt+1),
				"%s: a warning past ExpiresAt must be refused at delivery", s.Name)
		}
	}
	require.Positive(t, bd.ExpiredAtDelivery, "the expiry bound must actually have been exercised")

	// The interleaved stream is the load case, and its numbers are named rather than inferred:
	// eight observations cross the repeat threshold across two distinct signatures, and exactly
	// two warnings — one per signature — may be delivered.
	interleaved := run.Warnings["two-interleaved-stuck-states"]
	require.Len(t, interleaved, 2,
		"two distinct stuck states in one window are two warnings, not twelve and not one")
	require.NotEqual(t, interleaved[0].DedupKey, interleaved[1].DedupKey)
	require.Positive(t, bd.DedupCollapsed,
		"the later repeats of an already-delivered warning must be collapsed, not delivered")

	t.Logf("M6-G15-A bounded delivery: maxPerSession=%d observed-max=%d dedup-collapsed=%d "+
		"cap-suppressed=%d progress-suppressed=%d self-suppressed=%d expired-at-delivery=%d",
		bd.MaxPerSession, bd.MaxDelivered, bd.DedupCollapsed, bd.CapSuppressed,
		bd.ProgressSuppressed, bd.SelfSuppressed, bd.ExpiredAtDelivery)
}

// TestPhase6_SelfSuppressionIsAClosedLoop is contract §5.5 measured as a loop rather than asserted
// as a property: every warning the held-out corpus produced is rendered through the frozen
// FormatWarning and fed back in as the session's next thirty turns, in all four signature fields.
//
// Thirty turns is far past every threshold the detector has, so a detector that counted its own
// output would cross MinRepeats ten times over and hit its per-session cap. The required answer is
// zero — not "few", not "bounded" — because a warning that can cause the next warning is a feedback
// loop inside the component whose entire job is to notice feedback loops.
func TestPhase6_SelfSuppressionIsAClosedLoop(t *testing.T) {
	t.Parallel()

	streams := p6Streams(t)
	run := runSignatureArm(streams)
	bd := measureBoundedDelivery(streams, run)

	require.Positive(t, run.Result.Delivered, "a closed loop over no warnings would prove nothing")
	require.Zero(t, bd.AmplificationWarnings,
		"%d warnings were produced by feeding warnings back in; bound 5 forbids amplification "+
			"outright", bd.AmplificationWarnings)

	// The other half of bound 5: self-originated traffic is excluded BEFORE the frequency counter,
	// so a burst of retrieval results cannot show up in the report as session repetition.
	selfStream := p6StreamNamed(t, streams, "qompack-self-traffic")
	require.Empty(t, run.Warnings[selfStream.Name],
		"Qompack's own traffic must not produce a warning about Qompack")
	selfOriginated := 0
	for _, o := range selfStream.Observations {
		if grammar.IsSelfOriginated(selfStream.signature(o)) {
			selfOriginated++
		}
	}
	require.Positive(t, selfOriginated, "the self-traffic stream must actually contain self-traffic")
	require.Equal(t, selfOriginated, run.Detectors[selfStream.Name].Stats().SelfSuppressed,
		"every self-originated observation must be excluded, and counted as excluded")

	t.Logf("M6-G15-A self-suppression: %d warnings fed back over %d turns each produced %d further "+
		"warnings; %d of %s's %d observations were excluded as self-originated",
		run.Result.Delivered, p6SelfSuppressionTurns, bd.AmplificationWarnings,
		selfOriginated, selfStream.Name, len(selfStream.Observations))
}

// TestPhase6_UncertainWarningsSayThatTheyAreUncertain is amendment A7: the qualification bound 3
// puts in the type has to be visible in the line the user reads, or it exists only where a program
// can see it.
func TestPhase6_UncertainWarningsSayThatTheyAreUncertain(t *testing.T) {
	t.Parallel()

	streams := p6Streams(t)
	run := runSignatureArm(streams)

	uncertain, confident := 0, 0
	for _, s := range streams {
		for _, w := range run.Warnings[s.Name] {
			line := grammar.FormatWarning(w.Warning)
			if w.Uncertain {
				uncertain++
				require.Contains(t, line, p6UncertainMessage,
					"%s: an uncertain warning that reads like a confident one makes bound 3 invisible "+
						"exactly where it matters", s.Name)
				continue
			}
			confident++
			require.Contains(t, line, p6ConfidentMessage, "%s", s.Name)
			require.NotContains(t, line, p6UncertainMessage, "%s", s.Name)
		}
	}
	require.Positive(t, uncertain, "the held-out set must contain a warning produced from incomplete "+
		"coverage, or the uncertain path is untested")
	require.Positive(t, confident, "and one produced from complete coverage, or the confident path is")

	// The partial-coverage stream is the named case: a real loop seen through a hole in the
	// observation record. The verdict is still "stuck"; only the confidence changes.
	for _, w := range run.Warnings["stuck-with-partial-coverage"] {
		require.True(t, w.Uncertain)
		require.Equal(t, grammar.ProgressUnknown, w.Progress)
	}
	t.Logf("M6-G15-A qualification: %d uncertain warnings, %d confident", uncertain, confident)
}

// TestPhase6_AblationSequiturAgainstStateSignatures is gate M6-G15-B, and it is the row that is
// allowed to fail Sequitur.
//
// Three arms on identical input under identical delivery bounds: the shipped state-signature
// detector, Sequitur alone, and Sequitur given the same progress signal the signature arm has. The
// third arm exists because the obvious objection to the second is that Sequitur was handicapped,
// and the report says plainly where its progress latch is more generous than the signature arm's
// (window-wide rather than per-signature, which helps it on false alarms and can only cost it
// detections).
//
// NOTHING ABOUT SEQUITUR IS ASSERTED. Blocker M6-U15-sequitur-value leaves it optional and
// disabled until it demonstrably earns its place, so this test records the three confusion
// matrices, records the resource cost, computes the disposition with p6Verdict, and asserts only
// that the ablation actually ran on a corpus that can separate the arms. If the simpler signature
// does as well or better, that is the finding.
func TestPhase6_AblationSequiturAgainstStateSignatures(t *testing.T) {
	// Deliberately NOT parallel. The three arms are measured for allocation cost inside one
	// process, and running them alongside seven other parallel cases measures the test runner
	// rather than the detectors: the same signature arm reads 149 KB in a sequential pass and
	// 439 KB with the rest of this file running beside it. The decisions are unaffected either
	// way — TestPhase6_ArmsAreReproducible asserts that — but a cost column nobody can compare is
	// a cost column nobody should quote.
	streams := p6Streams(t)
	sig, seq, seqProg := runP6Arms(streams)

	var perStream strings.Builder
	for i, o := range sig.Result.Streams {
		fmt.Fprintf(&perStream, "\n  %-38s label=%-8s  %-14s %-14s %-14s",
			o.Stream, o.Label, o.Verdict, seq.Streams[i].Verdict, seqProg.Streams[i].Verdict)
	}
	verdict := p6Verdict(sig.Result, seq, seqProg)
	t.Logf("M6-G15-B ablation over %d held-out streams (%d stuck, %d progress), "+
		"same repeat threshold (minRepeats=%d / Thrash(%d)) and same cap (%d) on every arm.\n"+
		"  per stream: %-38s %-8s  %-14s %-14s %-14s%s\n\n%s\n%s\n%s\n\n  DISPOSITION: %s",
		len(streams), sig.Result.Stuck, sig.Result.Progress,
		sig.Config.MinRepeats, p6ThrashMinUses, sig.Config.MaxPerSession,
		"stream", "label", p6ArmSignature, p6ArmSequitur, p6ArmSequiturProgress, perStream.String(),
		p6ArmLine(sig.Result), p6ArmLine(seq), p6ArmLine(seqProg), verdict)
	writeSP15Artifact(t, "phase6-ablation.json", struct {
		Streams  int           `json:"streams"`
		Arms     []p6ArmResult `json:"arms"`
		Verdict  string        `json:"verdict"`
		Blocker  string        `json:"blocker"`
		CostNote string        `json:"costNote"`
	}{
		Streams: len(streams),
		Arms:    []p6ArmResult{sig.Result, seq, seqProg},
		Verdict: verdict,
		Blocker: "M6-U15-sequitur-value: Sequitur stays OPTIONAL AND DISABLED until it demonstrably " +
			"earns its place; this ablation is evidence toward that decision, not the decision",
		CostNote: "allocBytes/mallocs/wallMicros are RECORDED, never asserted: wall time on a loaded " +
			"machine is not a property of the code, and the allocation figures include the runtime's " +
			"own accounting. rules/compressedSymbols are exactly reproducible and are the structural " +
			"cost the grammar arms pay whether or not they fire",
	})

	require.Equal(t, len(streams), len(seq.Streams))
	require.Equal(t, len(streams), len(seqProg.Streams))
	require.Positive(t, sig.Result.Stuck, "an ablation with nothing to detect measures nothing")
	require.Positive(t, sig.Result.Progress, "an ablation with nothing to get wrong measures half of it")
	require.Positive(t, seq.Rules,
		"the Sequitur arm must actually induce a grammar, or it is not the thing being ablated")
}

// TestPhase6_ArmsAreReproducible is the determinism requirement, asserted where the alternative
// would be to quote a seed there is none of.
//
// It compares DECISION vectors and deliberately not costs: which streams fired, when and how many
// times must reproduce exactly, while allocation counts and wall time are measurements of the
// machine and are recorded rather than required.
func TestPhase6_ArmsAreReproducible(t *testing.T) {
	t.Parallel()

	streams := p6Streams(t)
	first, firstSeq, firstSeqProg := runP6Arms(streams)
	second, secondSeq, secondSeqProg := runP6Arms(streams)

	require.Equal(t, p6DecisionKey(first.Result), p6DecisionKey(second.Result))
	require.Equal(t, p6DecisionKey(firstSeq), p6DecisionKey(secondSeq))
	require.Equal(t, p6DecisionKey(firstSeqProg), p6DecisionKey(secondSeqProg))
	require.Equal(t, firstSeq.Rules, secondSeq.Rules,
		"the induced grammar is a deterministic function of the stream; a differing rule count "+
			"would mean the induction depends on map iteration order")

	// The rendered text has to reproduce too. It is the thing that reaches a prompt, and a warning
	// whose wording varied between runs would make SP-08's byte-for-byte injection assertion a
	// coin flip.
	for _, s := range streams {
		a, b := first.Warnings[s.Name], second.Warnings[s.Name]
		require.Len(t, b, len(a), "%s", s.Name)
		for i := range a {
			require.Equal(t, grammar.FormatWarning(a[i].Warning), grammar.FormatWarning(b[i].Warning),
				"%s warning %d", s.Name, i)
			require.Equal(t, a[i].DedupKey, b[i].DedupKey, "%s warning %d", s.Name, i)
		}
	}
}

// TestPhase6_ProgressSuppressionIsAWindowBoundAndNotAGag checks the two halves of bound 2 against
// each other, because either one alone is a bug.
//
// A stream that is making progress must produce nothing — that is the false alarm the whole feature
// would otherwise be. And a session that made progress and then got stuck must still be able to be
// told, or the bound is a permanent gag earned by one productive minute.
func TestPhase6_ProgressSuppressionIsAWindowBoundAndNotAGag(t *testing.T) {
	t.Parallel()

	streams := p6Streams(t)
	run := runSignatureArm(streams)

	require.Empty(t, run.Warnings["edit-test-edit-progress"],
		"an edit-test-edit loop that is changing files and failure signatures is not a loop")
	require.Empty(t, run.Warnings["refactor-rename-sweep"])

	later := run.Warnings["stuck-after-progress-later-window"]
	require.NotEmpty(t, later,
		"progress observed in one window must not silence the next one; a bound that did would "+
			"make the detector permanently mute after the first productive minute")
	for _, w := range later {
		require.GreaterOrEqual(t, w.Turns[0], core.TurnIndex(int(run.Config.WindowTurns)),
			"the warning must come from the LATER window, not from the productive one")
	}
}

// TestPhase6_PhaseCheckIsSelfContained runs the registered phase-6 check against a Context that
// carries no report at all, for the reason phase 5's twin gives: runPhaseChecks runs every landed
// phase on every pull request, and a check that needed a particular policy in --policies would fail
// on every invocation that did not include one.
func TestPhase6_PhaseCheckIsSelfContained(t *testing.T) {
	t.Parallel()
	require.NoError(t, phase6(Context{}))
}

// p6Streams loads the committed held-out set.
func p6Streams(t *testing.T) []p6Stream {
	t.Helper()
	streams, err := loadP6Streams(repoRootOf(t))
	require.NoError(t, err)
	require.NotEmpty(t, streams)
	return streams
}

// p6StreamNamed returns the named stream, failing the test if the held-out set no longer has it.
func p6StreamNamed(t *testing.T, streams []p6Stream, name string) p6Stream {
	t.Helper()
	for _, s := range streams {
		if s.Name == name {
			return s
		}
	}
	require.FailNowf(t, "missing stream", "the held-out set has no %q stream", name)
	return p6Stream{}
}
