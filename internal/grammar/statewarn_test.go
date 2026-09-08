package grammar_test

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/stretchr/testify/require"
)

// This file tests the state-aware warning layer against plans/sdd/V5-SP-15/contract.md §5 and the
// M6-G15-A gate. Contract §5 lists six bounds and calls them non-negotiable; each one has its own
// test below, named after the bound, so a future change that relaxes one fails a test whose name
// says which promise was broken:
//
//	1. TestStateWarning_IsAdvisoryOnly                     - never a constraint
//	2. TestDetector_ProgressObservedSuppressesEntirely     - the false-positive guard
//	3. TestDetector_ProgressUnknownIsOnlyEverUncertain     - no confident warning without coverage
//	4. TestDetector_DeduplicatesAndCapsPerSession          - bounded delivery
//	5. TestDetector_SelfOriginatedNeverWarns               - no feedback loop
//	6. TestDetectorStats_SeparatesUsefulnessFromFrequency  - honest accounting
//
// Several of them assert a NEGATIVE ("no warning is produced"), which is only meaningful next to a
// control showing the same stream warns when the guard is removed. Every such test carries one.

// The fixture state. It is an ordinary edit-test signature rather than anything contrived, because
// the question these tests ask is whether ordinary work gets warned about.
func fixtureSignature() grammar.StateSignature {
	return grammar.StateSignature{
		Goal:    "make the auth tests pass",
		Target:  "src/auth.ts",
		Action:  "Bash",
		Failure: "TestRefreshToken: expected 200, got 401",
	}
}

// observe is a terse constructor for the common case: one occurrence of the fixture state at a turn,
// with a given progress reading.
func observe(turn core.TurnIndex, p grammar.Progress) grammar.Observation {
	return grammar.Observation{Signature: fixtureSignature(), Turn: turn, Progress: p}
}

// feed replays observations into d and returns every warning it produced, in order.
func feed(d *grammar.Detector, obs ...grammar.Observation) []grammar.StateWarning {
	var out []grammar.StateWarning
	for _, o := range obs {
		if w, ok := d.Observe(o); ok {
			out = append(out, w)
		}
	}
	return out
}

// repeatedStream is the smallest stream that warns under the default bounds: MinRepeats occurrences
// of one signature, on consecutive turns inside one window, with nothing observed to change.
func repeatedStream(p grammar.Progress) []grammar.Observation {
	cfg := grammar.DefaultDetectorConfig()
	out := make([]grammar.Observation, 0, cfg.MinRepeats)
	for i := 0; i < cfg.MinRepeats; i++ {
		out = append(out, observe(core.TurnIndex(i), p))
	}
	return out
}

// TestStateSignature_Key pins Key's three properties: it is deterministic, it separates its fields,
// and it is domain-separated under a domain this test spells itself.
//
// The expected digest is recomputed here with crypto/sha256 rather than by calling core.HashBytes,
// for the reason internal/canon/sketchwire_test.go gives: a consumer has to be able to DISAGREE with
// the producer. Key's output is carried in StateWarning.DedupKey and is what a session's dedup state
// is keyed by, so a silent change to the domain string or the preimage layout would re-key every
// warning a running session is suppressing — this test is what makes that change loud.
func TestStateSignature_Key(t *testing.T) {
	// Transcribed from statewarn.go's documented construction, not imported from it.
	const domain = "qompack.grammar.state.v1"
	const sep = "\x1f"
	const prefix = "gstate_"

	sig := fixtureSignature()
	preimage := domain + "\x00" + sig.Goal + sep + sig.Target + sep + sig.Action + sep + sig.Failure
	sum := sha256.Sum256([]byte(preimage))
	want := prefix + hex.EncodeToString(sum[:])[:12]

	require.Equal(t, want, sig.Key(),
		"Key must be sha256(domain || 0x00 || goal 0x1f target 0x1f action 0x1f failure), short-form")
	require.Equal(t, sig.Key(), fixtureSignature().Key(), "equal signatures must produce equal keys")

	t.Run("every_field_participates", func(t *testing.T) {
		base := grammar.StateSignature{Goal: "g", Target: "t", Action: "a", Failure: "f"}
		for name, changed := range map[string]grammar.StateSignature{
			"goal":    {Goal: "G", Target: "t", Action: "a", Failure: "f"},
			"target":  {Goal: "g", Target: "T", Action: "a", Failure: "f"},
			"action":  {Goal: "g", Target: "t", Action: "A", Failure: "f"},
			"failure": {Goal: "g", Target: "t", Action: "a", Failure: "F"},
		} {
			require.NotEqual(t, base.Key(), changed.Key(), "%s does not affect the key", name)
		}
	})

	t.Run("fields_are_separated", func(t *testing.T) {
		// Without a separator, ("ab","c") and ("a","bc") would concatenate to the same preimage.
		// That is the exact collision domain separation and field separation exist to prevent.
		ab := grammar.StateSignature{Goal: "ab", Target: "c"}
		abc := grammar.StateSignature{Goal: "a", Target: "bc"}
		require.NotEqual(t, ab.Key(), abc.Key())
	})

	t.Run("empty_signature_still_keys", func(t *testing.T) {
		require.NotEmpty(t, grammar.StateSignature{}.Key())
		require.True(t, strings.HasPrefix(grammar.StateSignature{}.Key(), prefix))
	})
}

// TestStateWarning_IsAdvisoryOnly is contract §5's bound 1: a StateWarning never creates an
// elimination, a prohibition or a binding constraint, and there must be no API on it that could
// become one.
//
// "No API that could become one" is testable only structurally, so this asserts the two structural
// facts that make it true: StateWarning has NO methods at all (nothing to call that could act), and
// its fields are exactly the eight the contract froze, all of them plain data owned by this package
// or by core. A future change adding, say, an Enforce() method or a Prohibits field fails here — and
// failing here is the point, because such a change would have to be argued rather than merged.
//
// The absence of a method is also why Detector.Deliverable exists as a detector method rather than
// as StateWarning.Deliverable: expiry is a delivery decision, and keeping it off the record keeps the
// record inert.
func TestStateWarning_IsAdvisoryOnly(t *testing.T) {
	value := reflect.TypeOf(grammar.StateWarning{})
	require.Zero(t, value.NumMethod(), "StateWarning must expose no methods; it is a record, not an actor")
	require.Zero(t, reflect.PointerTo(value).NumMethod(), "*StateWarning must expose no methods either")

	wantFields := []struct{ name, typ string }{
		{"Signature", "grammar.StateSignature"},
		{"Repeats", "int"},
		{"Turns", "[]core.TurnIndex"},
		{"Progress", "grammar.Progress"},
		{"Uncertain", "bool"},
		{"DedupKey", "string"},
		{"ExpiresAt", "core.TurnIndex"},
		{"Warning", "grammar.Warning"},
	}
	require.Equal(t, len(wantFields), value.NumField(),
		"StateWarning's field set is frozen by contract §5; a new field is a contract change")
	for i, want := range wantFields {
		got := value.Field(i)
		require.Equal(t, want.name, got.Name, "field %d", i)
		require.Equal(t, want.typ, got.Type.String(), "field %s", want.name)
	}
}

// TestDetector_ProgressObservedSuppressesEntirely is bound 2, and it is the most important test in
// this file: an edit-test-edit loop that is changing files or failure signatures is not a loop.
//
// It is the difference between a feature and a liability. A detector that fires on ordinary
// iteration would interrupt the exact behaviour it should leave alone, and the user would rightly
// turn it off — after which none of the other five bounds would matter.
func TestDetector_ProgressObservedSuppressesEntirely(t *testing.T) {
	t.Run("control_the_same_stream_warns_without_progress", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		require.Len(t, feed(d, repeatedStream(grammar.ProgressNone)...), 1,
			"fixture sanity: this stream must warn, or the suppression tests below prove nothing")
	})

	t.Run("every_observation_reports_progress", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		var obs []grammar.Observation
		for i := 0; i < 12; i++ {
			obs = append(obs, observe(core.TurnIndex(i), grammar.ProgressObserved))
		}
		require.Empty(t, feed(d, obs...))
		require.Zero(t, d.Stats().Delivered)
		require.Equal(t, 12, d.Stats().ProgressSuppressed)
	})

	t.Run("one_observation_of_progress_suppresses_the_whole_window", func(t *testing.T) {
		// The latch is deliberate: a run that moved once is a run that is moving, and re-arming on
		// the next unchanged observation would report the tail of ordinary work as a loop.
		d := grammar.NewDetector(grammar.DetectorConfig{})
		obs := []grammar.Observation{
			observe(0, grammar.ProgressNone),
			observe(1, grammar.ProgressObserved),
			observe(2, grammar.ProgressNone),
			observe(3, grammar.ProgressNone),
			observe(4, grammar.ProgressNone),
			observe(5, grammar.ProgressNone),
		}
		require.Empty(t, feed(d, obs...))
	})

	t.Run("a_later_window_may_warn_again", func(t *testing.T) {
		// Suppression is scoped to the window it was observed in, not to the session: a session that
		// made progress and then genuinely got stuck must still be able to hear about it.
		d := grammar.NewDetector(grammar.DetectorConfig{})
		width := grammar.DefaultDetectorConfig().WindowTurns
		require.Empty(t, feed(d,
			observe(0, grammar.ProgressObserved),
			observe(1, grammar.ProgressNone),
			observe(2, grammar.ProgressNone)))
		require.Len(t, feed(d,
			observe(width, grammar.ProgressNone),
			observe(width+1, grammar.ProgressNone),
			observe(width+2, grammar.ProgressNone)), 1)
	})
}

// TestDetector_ProgressUnknownIsOnlyEverUncertain is bound 3. Missing observation coverage cannot
// establish that nothing changed, so a warning built on it must be readable as a question — in the
// record (Uncertain) and in the rendered text (the message), because a flag only a program can see
// is not a qualification the user ever receives.
func TestDetector_ProgressUnknownIsOnlyEverUncertain(t *testing.T) {
	confident := func(t *testing.T) grammar.StateWarning {
		t.Helper()
		got := feed(grammar.NewDetector(grammar.DetectorConfig{}), repeatedStream(grammar.ProgressNone)...)
		require.Len(t, got, 1)
		return got[0]
	}

	t.Run("all_unknown", func(t *testing.T) {
		got := feed(grammar.NewDetector(grammar.DetectorConfig{}), repeatedStream(grammar.ProgressUnknown)...)
		require.Len(t, got, 1)
		require.True(t, got[0].Uncertain)
		require.Equal(t, grammar.ProgressUnknown, got[0].Progress)
		require.NotEqual(t, confident(t).Warning.Message, got[0].Warning.Message,
			"an uncertain warning must not render identically to a confident one")
	})

	t.Run("one_unknown_taints_the_window", func(t *testing.T) {
		// The confident observation arrives LAST on purpose: bound 3 is a property of the run, and a
		// single well-observed turn at the end must not launder a partially unobserved run into a
		// confident finding.
		d := grammar.NewDetector(grammar.DetectorConfig{})
		got := feed(d,
			observe(0, grammar.ProgressUnknown),
			observe(1, grammar.ProgressNone),
			observe(2, grammar.ProgressNone))
		require.Len(t, got, 1)
		require.True(t, got[0].Uncertain)
		require.Equal(t, grammar.ProgressUnknown, got[0].Progress)
	})

	t.Run("fully_observed_runs_stay_confident", func(t *testing.T) {
		// The discriminating half: if Uncertain were always true it would carry no information.
		got := confident(t)
		require.False(t, got.Uncertain)
		require.Equal(t, grammar.ProgressNone, got.Progress)
	})

	t.Run("partial_dependency_coverage_is_also_uncertain", func(t *testing.T) {
		// Contract §5 names partial dependency coverage as a second, independent source of
		// uncertainty: progress was readable, but not everything it depends on was watched.
		d := grammar.NewDetector(grammar.DetectorConfig{})
		var obs []grammar.Observation
		for i := 0; i < 3; i++ {
			o := observe(core.TurnIndex(i), grammar.ProgressNone)
			o.PartialCoverage = true
			obs = append(obs, o)
		}
		got := feed(d, obs...)
		require.Len(t, got, 1)
		require.True(t, got[0].Uncertain)
		require.Equal(t, grammar.ProgressNone, got[0].Progress,
			"partial coverage qualifies the warning without claiming coverage was missing entirely")
	})

	t.Run("an_unrecognized_progress_value_is_treated_as_unknown", func(t *testing.T) {
		// A Progress value outside the declared three can only mean the producer knows something
		// this build does not, which is missing coverage, not an absence of change.
		d := grammar.NewDetector(grammar.DetectorConfig{})
		got := feed(d, repeatedStream(grammar.Progress(99))...)
		require.Len(t, got, 1)
		require.True(t, got[0].Uncertain)
	})
}

// TestDetector_DeduplicatesAndCapsPerSession is bound 4. An unbounded warning stream is itself a way
// to spend the context this system exists to save, so the same loop is reported once per window and
// a session hears at most MaxPerSession warnings however wrong the detector is.
func TestDetector_DeduplicatesAndCapsPerSession(t *testing.T) {
	cfg := grammar.DefaultDetectorConfig()

	t.Run("one_warning_per_window_however_many_repeats", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		var obs []grammar.Observation
		for i := 0; i < 10; i++ {
			obs = append(obs, observe(core.TurnIndex(i), grammar.ProgressNone))
		}
		got := feed(d, obs...)
		require.Len(t, got, 1)
		require.Equal(t, 1, d.Stats().Delivered)
		// Ten observations: the first two are below the threshold, the third warns, and the last
		// seven are deduplicated against it.
		require.Equal(t, 7, d.Stats().Deduplicated)
		require.Equal(t, 9, d.Stats().RepeatedStates)
	})

	t.Run("dedup_key_is_the_signature_key_plus_the_window", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		first := feed(d, repeatedStream(grammar.ProgressNone)...)
		require.Len(t, first, 1)

		var second []grammar.StateWarning
		for i := 0; i < cfg.MinRepeats; i++ {
			second = append(second, feed(d, observe(cfg.WindowTurns+core.TurnIndex(i), grammar.ProgressNone))...)
		}
		require.Len(t, second, 1)

		key := fixtureSignature().Key()
		require.True(t, strings.HasPrefix(first[0].DedupKey, key+"#"), "got %q", first[0].DedupKey)
		require.True(t, strings.HasPrefix(second[0].DedupKey, key+"#"), "got %q", second[0].DedupKey)
		require.NotEqual(t, first[0].DedupKey, second[0].DedupKey,
			"a later window is a new dedup key, or a long session could never be warned twice")
	})

	t.Run("max_per_session_is_a_hard_cap", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		var got []grammar.StateWarning
		for window := 0; window < cfg.MaxPerSession+3; window++ {
			base := cfg.WindowTurns * core.TurnIndex(window)
			for i := 0; i < cfg.MinRepeats; i++ {
				got = append(got, feed(d, observe(base+core.TurnIndex(i), grammar.ProgressNone))...)
			}
		}
		require.Len(t, got, cfg.MaxPerSession)
		require.Equal(t, cfg.MaxPerSession, d.Stats().Delivered)
		require.Positive(t, d.Stats().CapSuppressed)
	})

	t.Run("a_negative_cap_means_silence", func(t *testing.T) {
		// The in-code equivalent of contract §7's loopWarningsEnabled being off.
		d := grammar.NewDetector(grammar.DetectorConfig{MaxPerSession: -1})
		require.Empty(t, feed(d, repeatedStream(grammar.ProgressNone)...))
		require.Positive(t, d.Stats().CapSuppressed)
	})

	t.Run("a_pruned_window_is_never_rewarned", func(t *testing.T) {
		// Window state is pruned so a long session cannot leak one live entry per signature. The
		// record of what was already delivered is NOT pruned — it is bounded by MaxPerSession — so a
		// late, out-of-order observation for an old window cannot produce a second copy of a warning
		// the user has already seen.
		d := grammar.NewDetector(grammar.DetectorConfig{})
		require.Len(t, feed(d, repeatedStream(grammar.ProgressNone)...), 1)

		far := cfg.WindowTurns * 5
		require.Empty(t, feed(d, observe(far, grammar.ProgressNone)))
		require.Empty(t, feed(d,
			observe(0, grammar.ProgressNone),
			observe(1, grammar.ProgressNone),
			observe(2, grammar.ProgressNone)))
		require.Equal(t, 1, d.Stats().Delivered)
	})
}

// TestDetector_SelfOriginatedNeverWarns is bound 5, and it is the bound whose failure mode is a
// runaway rather than a nuisance: a warning that is injected, observed, and turned into the next
// warning is a feedback loop inside the component whose job is to notice feedback loops.
func TestDetector_SelfOriginatedNeverWarns(t *testing.T) {
	t.Run("an_explicitly_marked_stream_produces_nothing", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		var obs []grammar.Observation
		for i := 0; i < 25; i++ {
			o := observe(core.TurnIndex(i), grammar.ProgressNone)
			o.SelfOriginated = true
			obs = append(obs, o)
		}
		require.Empty(t, feed(d, obs...))
		require.Equal(t, 25, d.Stats().SelfSuppressed)
		require.Zero(t, d.Stats().Delivered)
		require.Zero(t, d.Stats().RepeatedStates,
			"self-originated records are excluded from the input, so they must not even inflate the "+
				"repeated-action frequency the M6-G15-A report reads")
	})

	t.Run("the_classifier_catches_an_unflagged_stream", func(t *testing.T) {
		// The explicit flag is the correct mechanism; this is the backstop for the wiring change that
		// forgets to set it, which would be an ordinary oversight anywhere else and self-amplifying
		// here.
		cases := map[string]grammar.StateSignature{
			"retrieval_tool":   {Action: "mcp__qompack__qompack_context", Target: "src/auth.ts"},
			"slash_command":    {Action: "Bash", Goal: "run /qompack:status and read the output"},
			"plugin_state_dir": {Action: "Read", Target: ".qompack/checkpoints/0001.json"},
		}
		for name, sig := range cases {
			t.Run(name, func(t *testing.T) {
				d := grammar.NewDetector(grammar.DetectorConfig{})
				var obs []grammar.Observation
				for i := 0; i < 10; i++ {
					obs = append(obs, grammar.Observation{Signature: sig, Turn: core.TurnIndex(i)})
				}
				require.Empty(t, feed(d, obs...))
				require.Equal(t, 10, d.Stats().SelfSuppressed)
			})
		}
	})

	t.Run("a_warning_cannot_cause_the_next_warning", func(t *testing.T) {
		// The closed loop, end to end: produce a real warning, render it exactly as SP-08 injects it,
		// and feed that text back as the next turn's goal. The detector must be silent, forever.
		d := grammar.NewDetector(grammar.DetectorConfig{})
		got := feed(d, repeatedStream(grammar.ProgressNone)...)
		require.Len(t, got, 1)

		injected := grammar.FormatWarning(got[0].Warning)
		require.Contains(t, injected, "[qompack]", "fixture sanity: this is the text SP-08 injects")

		echoed := fixtureSignature()
		echoed.Goal = "the assistant was told: " + injected
		var obs []grammar.Observation
		for i := 0; i < 30; i++ {
			obs = append(obs, grammar.Observation{Signature: echoed, Turn: core.TurnIndex(10 + i)})
		}
		require.Empty(t, feed(d, obs...))
		require.Equal(t, 1, d.Stats().Delivered, "the warning stream must not be self-sustaining")
	})
}

// TestIsSelfOriginated pins the classifier's marked surfaces and, just as importantly, its negative
// cases: a path or tool that merely mentions the project name is the session's own work.
func TestIsSelfOriginated(t *testing.T) {
	self := map[string]grammar.StateSignature{
		"mcp_retrieval_action":  {Action: "mcp__qompack__qompack_context"},
		"slash_command_in_goal": {Goal: "check /qompack:dropped"},
		"injected_warning_text": {Failure: "[qompack] possible loop: Read→Edit repeated 3× (turns 1–4) — x"},
		"state_dir_target":      {Target: ".qompack/sketches/tried.bloom"},
		"state_dir_itself":      {Target: ".qompack"},
	}
	for name, sig := range self {
		t.Run("self/"+name, func(t *testing.T) {
			require.True(t, grammar.IsSelfOriginated(sig))
		})
	}

	notSelf := map[string]grammar.StateSignature{
		"ordinary_tool":       {Action: "Bash", Target: "src/auth.ts"},
		"project_source_file": {Action: "Read", Target: "internal/qompack/main.go"},
		"similarly_named_dir": {Action: "Read", Target: "qompack-docs/README.md"},
		"empty":               {},
	}
	for name, sig := range notSelf {
		t.Run("not_self/"+name, func(t *testing.T) {
			require.False(t, grammar.IsSelfOriginated(sig))
		})
	}
}

// TestDetectorStats_SeparatesUsefulnessFromFrequency is bound 6. A component reported on by its own
// activity level always looks successful; M6-G15-A exists to stop that argument being made here, so
// repeated-action frequency, delivery and judged outcomes are three different numbers.
func TestDetectorStats_SeparatesUsefulnessFromFrequency(t *testing.T) {
	t.Run("frequency_rises_without_any_warning", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		var obs []grammar.Observation
		for i := 0; i < 15; i++ {
			obs = append(obs, observe(core.TurnIndex(i), grammar.ProgressObserved))
		}
		require.Empty(t, feed(d, obs...))
		s := d.Stats()
		require.Equal(t, 15, s.Observations)
		require.Equal(t, 14, s.RepeatedStates, "the session repeated a state; that is not a finding")
		require.Zero(t, s.Delivered)
		require.Zero(t, s.Useful)
		require.Zero(t, s.FalseAlarms)
	})

	t.Run("outcomes_are_recorded_once_and_only_for_delivered_keys", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		got := feed(d, repeatedStream(grammar.ProgressNone)...)
		require.Len(t, got, 1)

		require.True(t, d.RecordOutcome(got[0].DedupKey, grammar.OutcomeUseful))
		require.Equal(t, 1, d.Stats().Useful)

		require.False(t, d.RecordOutcome(got[0].DedupKey, grammar.OutcomeFalseAlarm),
			"an outcome that could be revised would make the false-alarm rate a function of who "+
				"filed the last report")
		require.Equal(t, 1, d.Stats().Useful)
		require.Zero(t, d.Stats().FalseAlarms)

		require.False(t, d.RecordOutcome("gstate_deadbeef0000#0", grammar.OutcomeUseful),
			"only a key this detector delivered may be judged")
		require.False(t, d.RecordOutcome(got[0].DedupKey, grammar.OutcomeUnknown),
			"OutcomeUnknown is the unjudged state, not a judgement")

		s := d.Stats()
		require.LessOrEqual(t, s.Useful+s.FalseAlarms, s.Delivered)
	})

	t.Run("false_alarms_are_counted_in_their_own_right", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		got := feed(d, repeatedStream(grammar.ProgressNone)...)
		require.Len(t, got, 1)
		require.True(t, d.RecordOutcome(got[0].DedupKey, grammar.OutcomeFalseAlarm))
		require.Equal(t, 1, d.Stats().FalseAlarms)
		require.Zero(t, d.Stats().Useful)
	})

	t.Run("stats_is_a_copy", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		before := d.Stats()
		feed(d, repeatedStream(grammar.ProgressNone)...)
		require.Zero(t, before.Delivered, "a held Stats value must not change underneath its holder")
		require.Equal(t, 1, d.Stats().Delivered)
	})

	t.Run("an_empty_signature_is_ignored_not_warned_about", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		var obs []grammar.Observation
		for i := 0; i < 6; i++ {
			obs = append(obs, grammar.Observation{Turn: core.TurnIndex(i)})
		}
		require.Empty(t, feed(d, obs...))
		require.Equal(t, 6, d.Stats().Ignored)
	})
}

// TestDetector_ExpiryBoundsDelivery covers StateWarning.ExpiresAt. Production and delivery are
// different moments — the detector runs on PostToolUse and the warning is injected on the next
// UserPromptSubmit — and a warning that arrives long after the session moved on is not merely stale,
// it reads as the plugin being confused about the present.
func TestDetector_ExpiryBoundsDelivery(t *testing.T) {
	cfg := grammar.DefaultDetectorConfig()
	d := grammar.NewDetector(grammar.DetectorConfig{})
	got := feed(d, repeatedStream(grammar.ProgressNone)...)
	require.Len(t, got, 1)

	produced := got[0].Turns[len(got[0].Turns)-1]
	require.Equal(t, produced+cfg.ExpiryTurns, got[0].ExpiresAt)

	require.True(t, d.Deliverable(got[0], produced), "deliverable at the turn it was produced")
	require.True(t, d.Deliverable(got[0], got[0].ExpiresAt), "ExpiresAt itself is still deliverable")
	require.Zero(t, d.Stats().Expired)

	require.False(t, d.Deliverable(got[0], got[0].ExpiresAt+1))
	require.Equal(t, 1, d.Stats().Expired)
}

// TestDetector_RendersThroughFormatWarning is contract §5's rendering requirement: a StateWarning
// renders through the FROZEN FormatWarning and invents no user-facing wording of its own.
//
// The expected strings below are the frozen template (formatwarning_test.go pins it byte-for-byte,
// and SP-08 injects it) filled with this layer's two Message values. Pinning them here means a
// change to either one is a visible change to what a user reads, which is exactly the review it
// deserves.
func TestDetector_RendersThroughFormatWarning(t *testing.T) {
	t.Run("with_an_action_sequence", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		var got []grammar.StateWarning
		for i := 0; i < 3; i++ {
			o := observe(core.TurnIndex(i), grammar.ProgressNone)
			o.Symbols = []grammar.Symbol{"Read", "Edit", "Bash"}
			got = append(got, feed(d, o)...)
		}
		require.Len(t, got, 1)
		require.Equal(t,
			"[qompack] possible loop: Read→Edit→Bash repeated 3× (turns 0–2) — consider a different approach",
			grammar.FormatWarning(got[0].Warning))
		require.Equal(t, got[0].Repeats, got[0].Warning.Repeats)
		require.Equal(t, got[0].Turns, got[0].Warning.Turns)
	})

	t.Run("without_one_it_renders_the_signature", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		got := feed(d, repeatedStream(grammar.ProgressNone)...)
		require.Len(t, got, 1)
		require.Equal(t,
			"[qompack] possible loop: Bash repeated 3× (turns 0–2) — consider a different approach",
			grammar.FormatWarning(got[0].Warning))
	})

	t.Run("uncertain_warnings_read_as_questions", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		got := feed(d, repeatedStream(grammar.ProgressUnknown)...)
		require.Len(t, got, 1)
		require.Equal(t,
			"[qompack] possible loop: Bash repeated 3× (turns 0–2) — observation coverage is "+
				"incomplete, so this may not be a loop",
			grammar.FormatWarning(got[0].Warning))
	})

	t.Run("turns_are_sorted_regardless_of_arrival_order", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		got := feed(d,
			observe(5, grammar.ProgressNone),
			observe(1, grammar.ProgressNone),
			observe(9, grammar.ProgressNone))
		require.Len(t, got, 1)
		require.Equal(t, []core.TurnIndex{1, 5, 9}, got[0].Turns)
		require.Contains(t, grammar.FormatWarning(got[0].Warning), "(turns 1–9)")
	})

	t.Run("the_returned_warning_owns_its_slices", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{})
		symbols := []grammar.Symbol{"Read", "Edit"}
		var got []grammar.StateWarning
		for i := 0; i < 3; i++ {
			o := observe(core.TurnIndex(i), grammar.ProgressNone)
			o.Symbols = symbols
			got = append(got, feed(d, o)...)
		}
		require.Len(t, got, 1)

		// The detector copied the caller's symbols rather than retaining the array, so a caller that
		// reuses one scratch slice across observations cannot rewrite a warning it already received.
		symbols[0] = "Bash"
		require.Equal(t, []grammar.Symbol{"Read", "Edit"}, got[0].Warning.Rule.Expansion)

		// The record's Turns and the rendered Warning's Turns are separate arrays, so a consumer that
		// sorts or trims one cannot scramble the other.
		got[0].Turns[0] = 999
		require.Equal(t, []core.TurnIndex{0, 1, 2}, got[0].Warning.Turns)
	})
}

// TestDetector_ZeroConfigTakesDefaults asserts the zero DetectorConfig is a valid, shipped-default
// detector rather than a trap. A zero WindowTurns would otherwise be a division by zero on the first
// observation, which is a poor way for an advisory feature to fail.
func TestDetector_ZeroConfigTakesDefaults(t *testing.T) {
	require.Equal(t, grammar.DefaultDetectorConfig(), grammar.NewDetector(grammar.DetectorConfig{}).Config())

	// Individually nonsensical values fall back field by field, so a caller overriding one bound does
	// not silently lose the others.
	partial := grammar.NewDetector(grammar.DetectorConfig{MinRepeats: 5}).Config()
	require.Equal(t, 5, partial.MinRepeats)
	require.Equal(t, grammar.DefaultDetectorConfig().WindowTurns, partial.WindowTurns)

	nonsense := grammar.NewDetector(grammar.DetectorConfig{MinRepeats: 1, WindowTurns: -3, ExpiryTurns: -1}).Config()
	require.Equal(t, grammar.DefaultDetectorConfig().MinRepeats, nonsense.MinRepeats,
		"a loop of one occurrence is a category error, not a threshold")
	require.Equal(t, grammar.DefaultDetectorConfig().WindowTurns, nonsense.WindowTurns)
	require.Equal(t, grammar.DefaultDetectorConfig().ExpiryTurns, nonsense.ExpiryTurns)

	t.Run("a_higher_threshold_delays_the_warning", func(t *testing.T) {
		d := grammar.NewDetector(grammar.DetectorConfig{MinRepeats: 5})
		require.Empty(t, feed(d,
			observe(0, grammar.ProgressNone),
			observe(1, grammar.ProgressNone),
			observe(2, grammar.ProgressNone),
			observe(3, grammar.ProgressNone)))
		require.Len(t, feed(d, observe(4, grammar.ProgressNone)), 1)
	})
}

// TestDetector_IsDeterministic asserts that two detectors fed the same observations in the same
// order produce identical warnings and identical statistics.
//
// It is not a formality. The M6-G15-A false-alarm and usefulness report is only meaningful if it
// describes the detector rather than one afternoon's scheduling, and the detector holds no clock and
// no map-ordered output precisely so that a replay can reproduce it exactly.
func TestDetector_IsDeterministic(t *testing.T) {
	stream := func() []grammar.Observation {
		var out []grammar.Observation
		for window := 0; window < 4; window++ {
			base := grammar.DefaultDetectorConfig().WindowTurns * core.TurnIndex(window)
			for i := 0; i < 5; i++ {
				o := observe(base+core.TurnIndex(i), grammar.Progress(i%3))
				o.Symbols = []grammar.Symbol{"Read", "Bash"}
				out = append(out, o)
			}
			other := fixtureSignature()
			other.Action = "Grep"
			out = append(out, grammar.Observation{Signature: other, Turn: base, Progress: grammar.ProgressNone})
		}
		return out
	}

	first := grammar.NewDetector(grammar.DetectorConfig{})
	second := grammar.NewDetector(grammar.DetectorConfig{})
	require.Equal(t, feed(first, stream()...), feed(second, stream()...))
	require.Equal(t, first.Stats(), second.Stats())
}
