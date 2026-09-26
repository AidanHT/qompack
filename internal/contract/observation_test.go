package contract

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// The observation ledger is 00-ARCHITECTURE.md §12.1's revised requirement: a PER-CAPABILITY record
// beside the frozen Result set, in which an absent producer, an unsupported mechanism and an
// unobserved-but-supported mechanism are three different outcomes rather than three spellings of
// "OK". Result itself does not change — testdata/golden/contracts/contract/want/result_set.json is
// a frozen wire format two other components parse — so everything below sits next to it.

// obsTarget is the target every test here attributes its observations to.
var obsTarget = Target{Provider: "claude-code", Version: "2.1.263", Platform: "windows/amd64", Date: "2026-09-07"}

// TestCapabilityOf_MapsEveryStandardAssertion pins the assertion → capability map. Seven of the
// nine §5.19 assertions observe the host; one is the injection probe; one is the PreCompact
// custom_instructions probe, whose mechanism Qompack.md v1.5 §7.3 declares unsupported.
func TestCapabilityOf_MapsEveryStandardAssertion(t *testing.T) {
	t.Parallel()

	want := map[ID]Capability{
		CSessionStartFires:         CapObservation,
		CSessionStartSourceCompact: CapObservation,
		CHookPayloadShape:          CapObservation,
		CTranscriptReadable:        CapObservation,
		CPluginRootResolves:        CapObservation,
		CPreCompactTiming:          CapObservation,
		CMCPRegistered:             CapObservation,
		CAdditionalContext:         CapInjection,
		CPreCompactCustomInstr:     CapCompactionRequest,
	}

	for _, a := range StandardAssertions() {
		got := CapabilityOf(a.ID)
		require.NotEmpty(t, string(got), "%s maps to no capability; a new assertion needs a decision here", a.ID)
		require.Equal(t, want[a.ID], got, "%s", a.ID)
	}
	require.Len(t, want, len(StandardAssertions()))

	require.Empty(t, string(CapabilityOf("something.new")),
		"an assertion this build does not know maps to no capability, not to a plausible one")
}

// TestClassifyResult_Table is the whole classification contract in one place: every Observed
// spelling assertions.go can emit, and the outcome it means.
func TestClassifyResult_Table(t *testing.T) {
	t.Parallel()

	reg := DefaultCapabilityRegister()

	cases := []struct {
		name string
		r    Result
		want Outcome
	}{
		// The producer is absent from the build. §12.1 reports this as OK/SevInfo so a wave-1
		// build is not degraded by a wave-4 subsystem — which is precisely why it must not read
		// as evidence the capability works.
		{"not-yet-implemented is unavailable", Result{ID: CSessionStartFires, OK: true, Observed: "not-yet-implemented"}, OutcomeUnavailable},
		{"not-yet-implemented on the injection probe", Result{ID: CAdditionalContext, OK: true, Observed: "not-yet-implemented"}, OutcomeUnavailable},

		// The register says the mechanism is unsupported here. No Result about it can mean
		// anything else, including a passing one: Qompack.md §12.1 "do not search a summary for
		// evidence an invented setter worked".
		{"unsupported mechanism, phrase found", Result{ID: CPreCompactCustomInstr, OK: true, Observed: "instruction phrase found in transcript tail"}, OutcomeUnsupported},
		{"unsupported mechanism, phrase absent", Result{ID: CPreCompactCustomInstr, OK: false, Observed: "instruction phrase not found in transcript tail"}, OutcomeUnsupported},
		{"unsupported mechanism, nothing emitted", Result{ID: CPreCompactCustomInstr, OK: true, Observed: "no-instructions-emitted"}, OutcomeUnsupported},

		// A real, observed failure.
		{"failed marker", Result{ID: CSessionStartFires, OK: false, Observed: "no marker from a prior terminal hook across two consecutive sessions"}, OutcomeFailed},
		{"failed source", Result{ID: CSessionStartSourceCompact, OK: false, Observed: "startup"}, OutcomeFailed},
		{"failed sentinel", Result{ID: CAdditionalContext, OK: false, Observed: "sentinel not found after two chances"}, OutcomeFailed},
		{"failed payload", Result{ID: CHookPayloadShape, OK: false, Observed: "missing hook_event_name"}, OutcomeFailed},
		{"failed mcp", Result{ID: CMCPRegistered, OK: false, Observed: "initialize-not-received"}, OutcomeFailed},
		{"failed transcript", Result{ID: CTranscriptReadable, OK: false, Observed: "transcript_path does not exist"}, OutcomeFailed},
		{"failed plugin root", Result{ID: CPluginRootResolves, OK: false, Observed: "CLAUDE_PLUGIN_ROOT set but no plugin binary found beneath it"}, OutcomeFailed},
		{"failed timing", Result{ID: CPreCompactTiming, OK: false, Observed: "p99=19000ms timeout=20000ms"}, OutcomeFailed},

		// OK, but nothing was actually observed. These are the spellings that used to be
		// indistinguishable from success.
		{"no observation yet", Result{ID: CSessionStartFires, OK: true, Observed: "no observation yet"}, OutcomeNotObserved},
		{"first-session", Result{ID: CSessionStartFires, OK: true, Observed: "first-session"}, OutcomeNotObserved},
		{"marker-absent-once", Result{ID: CSessionStartFires, OK: true, Observed: "marker-absent-once"}, OutcomeNotObserved},
		{"no-precompact-pending", Result{ID: CSessionStartSourceCompact, OK: true, Observed: "no-precompact-pending"}, OutcomeNotObserved},
		{"pending for another session", Result{ID: CSessionStartSourceCompact, OK: true, Observed: "precompact-pending-for-another-session"}, OutcomeNotObserved},
		{"not-yet-observed", Result{ID: CAdditionalContext, OK: true, Observed: "not-yet-observed"}, OutcomeNotObserved},
		{"timeout-unknown", Result{ID: CPreCompactTiming, OK: true, Observed: "timeout-unknown"}, OutcomeNotObserved},
		{"no-samples", Result{ID: CPreCompactTiming, OK: true, Observed: "no-samples"}, OutcomeNotObserved},
		{"no-transcript-path", Result{ID: CTranscriptReadable, OK: true, Observed: "no-transcript-path"}, OutcomeNotObserved},
		{"plugin root unset", Result{ID: CPluginRootResolves, OK: true, Observed: "unset"}, OutcomeNotObserved},

		// OK, and something really was observed.
		{"marker-found", Result{ID: CSessionStartFires, OK: true, Observed: "marker-found"}, OutcomeObserved},
		{"source compact", Result{ID: CSessionStartSourceCompact, OK: true, Expected: "compact", Observed: "compact"}, OutcomeObserved},
		{"sentinel-observed", Result{ID: CAdditionalContext, OK: true, Observed: "sentinel-observed"}, OutcomeObserved},
		{"payload shape valid", Result{ID: CHookPayloadShape, OK: true, Observed: "payload shape valid"}, OutcomeObserved},
		{"initialize-received", Result{ID: CMCPRegistered, OK: true, Observed: "initialize-received"}, OutcomeObserved},
		{"transcript readable", Result{ID: CTranscriptReadable, OK: true, Observed: "transcript readable"}, OutcomeObserved},
		{"plugin root resolved", Result{ID: CPluginRootResolves, OK: true, Observed: "resolved"}, OutcomeObserved},
		{"timing within budget", Result{ID: CPreCompactTiming, OK: true, Observed: "p99=200ms timeout=20000ms"}, OutcomeObserved},

		// An assertion this build cannot attribute to a capability is unknown, never observed.
		{"unmapped assertion", Result{ID: "something.new", OK: true, Observed: "fine"}, OutcomeUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, ClassifyResult(tc.r, reg))
		})
	}
}

// TestClassifyResult_UnsupportedBeatsEveryOtherReading is the rule with the sharpest edge, given
// its own test because it is the one an optimiser under deadline pressure would be tempted to
// relax: a mechanism the register calls unsupported yields `unsupported` no matter how convincing
// the Result looks.
func TestClassifyResult_UnsupportedBeatsEveryOtherReading(t *testing.T) {
	t.Parallel()

	reg := DefaultCapabilityRegister()
	rec, ok := reg.Get(CapCompactionRequest)
	require.True(t, ok)
	require.Equal(t, StatusUnsupported, rec.Status,
		"precondition: PreCompact custom_instructions has no supported output mechanism here")

	found := Result{ID: CPreCompactCustomInstr, OK: true, Observed: "instruction phrase found in transcript tail"}
	require.Equal(t, OutcomeUnsupported, ClassifyResult(found, reg))

	// ... and the classification is a property of the REGISTER, not of a constant: a future build
	// that verified the mechanism against a real target reads the same Result as an observation.
	verified := reg
	verified.Records = append([]CapabilityRecord(nil), reg.Records...)
	for i := range verified.Records {
		if verified.Records[i].Capability == CapCompactionRequest {
			verified.Records[i].Status = StatusVerifiedInTarget
		}
	}
	require.Equal(t, OutcomeObserved, ClassifyResult(found, verified))
}

// TestClassifyResult_FreshBuildIsEntirelyUnavailable walks the real StandardAssertions on a build
// with no declared producers — the state golden_test.go freezes — and asserts that what §12.1
// reports as nine OK results is nine UNAVAILABLE observations. This is the whole point of the
// ledger: the frozen fixture stays byte-identical and stops being readable as evidence.
func TestClassifyResult_FreshBuildIsEntirelyUnavailable(t *testing.T) {
	t.Cleanup(ResetProducers)
	ResetProducers()

	reg := DefaultCapabilityRegister()
	for _, a := range StandardAssertions() {
		r := a.Check(context.Background(), Env{})
		require.True(t, r.OK, "%s: precondition — §12.1 reports an absent producer as OK", a.ID)
		require.Equal(t, OutcomeUnavailable, ClassifyResult(r, reg),
			"%s: an absent producer is unavailable, never a passing capability", a.ID)
	}
}

// TestNoObservationSpellings_AreWhatTheChecksActuallyEmit drives the real Check bodies with
// synthetic Envs and asserts the classification of what comes back. The spelling list is a literal
// table, so this is the test that stops it drifting away from the code it describes.
func TestNoObservationSpellings_AreWhatTheChecksActuallyEmit(t *testing.T) {
	t.Cleanup(ResetProducers)
	ResetProducers()
	for _, a := range StandardAssertions() {
		DeclareProducer(a.ID)
	}
	// An empty value is what checkPluginRootResolves reads as "unset"; t.Setenv restores whatever
	// the surrounding environment had.
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")

	reg := DefaultCapabilityRegister()

	// A zero Env carries no History, no event and no transcript: every check that consults one
	// must report an absence of evidence, and none may report success.
	for _, a := range StandardAssertions() {
		r := a.Check(context.Background(), Env{})
		got := ClassifyResult(r, reg)
		require.NotEqual(t, OutcomeObserved, got,
			"%s observed something from an empty Env (%q)", a.ID, r.Observed)
		require.NotEqual(t, OutcomeUnavailable, got,
			"%s reported not-yet-implemented although its producer is declared", a.ID)
	}

	// And the specific spellings, so a renamed one shows up here rather than as a silent
	// reclassification to `observed`.
	h := &SessionHistory{Version: 1}
	spellings := map[ID]string{
		CSessionStartFires:         "no observation yet",
		CSessionStartSourceCompact: "no-precompact-pending",
		CAdditionalContext:         "not-yet-observed",
		CPreCompactTiming:          "timeout-unknown",
		CPreCompactCustomInstr:     "retired", // C1.18: the row is retired and observes nothing
		CTranscriptReadable:        "no-transcript-path",
		CPluginRootResolves:        "unset",
	}
	for _, a := range StandardAssertions() {
		want, ok := spellings[a.ID]
		if !ok {
			continue
		}
		e := Env{History: h}
		if a.ID == CSessionStartFires {
			e.History = nil // the only route to "no observation yet" is an absent history
		}
		r := a.Check(context.Background(), e)
		require.Equal(t, want, r.Observed, "%s", a.ID)
		require.True(t, noObservationSpellings[r.Observed],
			"%s emits %q, which the no-observation table does not list", a.ID, r.Observed)
	}
}

// TestObservedVocabulary_CoversEveryLiteralInAssertionsGo is the anti-drift guard the design asks
// for: it parses assertions.go and requires every literal Observed string it can emit to be
// accounted for by one of the two tables in observation.go. A new spelling added to a Check
// without a decision here fails this test instead of silently classifying as `observed`.
//
// Only string LITERALS are checkable this way; the three computed values (the sorted-extra-key
// message, the p99 line and e.Event.Source) are expressions, and each is on an OK=false path or a
// genuine observation, both of which classify before the spelling table is consulted.
func TestObservedVocabulary_CoversEveryLiteralInAssertionsGo(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "assertions.go", nil, 0)
	require.NoError(t, err, "the guard cannot run if it cannot parse the file it guards")

	var (
		seen    int
		unknown []string
	)
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Observed" {
			return true
		}
		lit, ok := kv.Value.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true // a computed value; see this test's doc comment
		}
		s, uerr := strconv.Unquote(lit.Value)
		require.NoError(t, uerr)
		seen++
		if !noObservationSpellings[s] && !observedSpellings[s] {
			unknown = append(unknown, s)
		}
		return true
	})

	require.Positive(t, seen, "the AST walk found no Observed literals at all — the guard is vacuous")
	require.Empty(t, unknown,
		"assertions.go emits Observed strings observation.go does not classify: %q.\n"+
			"Add each to noObservationSpellings or observedSpellings — an unclassified spelling "+
			"falls through to `observed`, which is the exact reading §12.1 forbids.", unknown)
}

// TestObservationsOf_AttributesAndDatesEveryResult: an observation nobody can date, scope or
// attribute to a host is not evidence. §12.1 lists the fields; this asserts they are filled.
func TestObservationsOf_AttributesAndDatesEveryResult(t *testing.T) {
	t.Parallel()

	reg := DefaultCapabilityRegister()
	results := []Result{
		{ID: CAdditionalContext, OK: true, Observed: "sentinel-observed", TS: core.UnixMilli(1767225510000)},
		{ID: CSessionStartFires, OK: true, Observed: "marker-found", TS: core.UnixMilli(1767225510001)},
		{ID: CPreCompactCustomInstr, OK: true, Observed: "instruction phrase found in transcript tail", TS: core.UnixMilli(1767225510002)},
	}

	got := ObservationsOf(results, reg, obsTarget, "session:abc")
	require.Len(t, got, len(results))

	for i, o := range got {
		require.Equal(t, results[i].ID, o.ID)
		require.Equal(t, results[i].TS, o.TS, "the observation keeps the Result's own timestamp")
		require.Equal(t, obsTarget, o.Target, "provider/version/platform/date travel with the record")
		require.Equal(t, "session:abc", o.Scope)
		require.Equal(t, results[i].Observed, o.Observed)
		require.NotEmpty(t, string(o.Capability))
		require.NotEmpty(t, string(o.Outcome))
		require.NotEmpty(t, o.Mechanism, "%s: the register's mechanism is what was actually attempted", o.ID)
	}

	require.Equal(t, OutcomeObserved, got[0].Outcome)
	require.Equal(t, OutcomeObserved, got[1].Outcome)
	require.Equal(t, OutcomeUnsupported, got[2].Outcome,
		"the custom_instructions setter is unsupported however convincing its Result looks")
}

// TestObservationsOf_CoverageIsNeverComplete is Qompack.md §12.1's sentence rendered as a test: a
// transcript sentinel documents ONE delivery under the tested contract. It does not document
// complete context, model compliance, or anything else, and the vocabulary must make the
// overstatement unspellable.
func TestObservationsOf_CoverageIsNeverComplete(t *testing.T) {
	t.Parallel()

	reg := DefaultCapabilityRegister()
	results := []Result{
		{ID: CAdditionalContext, OK: true, Observed: "sentinel-observed", TS: 1},
		{ID: CAdditionalContext, OK: true, Observed: "not-yet-observed", TS: 2},
		{ID: CAdditionalContext, OK: false, Observed: "sentinel not found after two chances", TS: 3},
		{ID: CSessionStartFires, OK: true, Observed: "marker-found", TS: 4},
	}

	got := ObservationsOf(results, reg, obsTarget, "session:abc")

	require.Equal(t, CoverageDeliveryUnderTestedContract, got[0].Coverage,
		"an observed sentinel is a delivery under the tested contract")
	require.Equal(t, CoverageNone, got[1].Coverage, "an unobserved sentinel covers nothing")
	require.Equal(t, CoverageNone, got[2].Coverage, "a failed sentinel covers nothing")
	require.Equal(t, CoverageNone, got[3].Coverage,
		"a marker observation is not a context-delivery claim")

	for _, o := range got {
		require.NotContains(t, o.Coverage, "complete")
		require.Contains(t, []string{CoverageDeliveryUnderTestedContract, CoverageNone}, o.Coverage,
			"%s: the coverage vocabulary is closed", o.ID)
	}
}

// TestObservationLedgerPath_IsItsOwnFile: two schemas sharing one path corrupt each other the first
// time both are written — the reason HistoryPath is not the monitor's state file, and the same
// reason this is neither.
func TestObservationLedgerPath_IsItsOwnFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	p := ObservationLedgerPath(root)
	require.Equal(t, "observations.json", filepath.Base(p))
	require.NotEqual(t, HistoryPath(root), p)
	require.Equal(t, filepath.Dir(HistoryPath(root)), filepath.Dir(p),
		"it belongs beside the other state files, in .qompack/state")
}

// TestObservationLedger_RoundTrips is the compatibility half: what Save writes, Load must return.
func TestObservationLedger_RoundTrips(t *testing.T) {
	t.Parallel()

	path := ObservationLedgerPath(t.TempDir())

	fresh := LoadObservationLedger(path)
	require.NotNil(t, fresh, "a missing ledger loads as fresh, never as nil")
	require.Empty(t, fresh.Observations)
	require.Equal(t, 1, fresh.Version)

	l := &ObservationLedger{Version: 1, Target: obsTarget}
	l.Append(ObservationsOf([]Result{
		{ID: CAdditionalContext, OK: true, Observed: "sentinel-observed", TS: 7},
	}, DefaultCapabilityRegister(), obsTarget, "session:abc")...)

	require.NoError(t, SaveObservationLedger(path, l))

	back := LoadObservationLedger(path)
	require.Equal(t, l.Target, back.Target)
	require.Equal(t, l.Observations, back.Observations)

	// Owner-only, like every other file this package writes.
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm()&0o600)

	// A corrupt or future-schema ledger fails toward a fresh one (§12.3), never toward a stale
	// reading of fields this build does not understand.
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
	require.Empty(t, LoadObservationLedger(path).Observations)

	future, err := json.Marshal(map[string]any{"version": 99, "observations": []any{map[string]any{"id": "x"}}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, future, 0o600))
	require.Empty(t, LoadObservationLedger(path).Observations)
}

// TestObservationLedger_CapsPerID keeps an always-on ledger bounded without letting a chatty
// assertion evict a quiet one: the cap is PER ID, newest last.
func TestObservationLedger_CapsPerID(t *testing.T) {
	t.Parallel()

	l := &ObservationLedger{Version: 1, Target: obsTarget}
	for i := range 200 {
		l.Append(Observation{ID: CSessionStartFires, Capability: CapObservation, Outcome: OutcomeObserved, TS: core.UnixMilli(i)})
	}
	l.Append(Observation{ID: CMCPRegistered, Capability: CapObservation, Outcome: OutcomeFailed, TS: 999})

	var fires []Observation
	var mcp []Observation
	for _, o := range l.Observations {
		switch o.ID {
		case CSessionStartFires:
			fires = append(fires, o)
		case CMCPRegistered:
			mcp = append(mcp, o)
		}
	}

	require.Len(t, fires, maxObservationsPerID, "the cap is per ID")
	require.Len(t, mcp, 1, "a quiet assertion is not evicted by a chatty one")
	require.Equal(t, core.UnixMilli(199), fires[len(fires)-1].TS, "newest last")
	require.Equal(t, core.UnixMilli(200-maxObservationsPerID), fires[0].TS, "the oldest are the ones dropped")

	// The cap is re-applied on load, so a hand-edited file cannot smuggle an unbounded ledger past
	// it — the same rule LoadHistory follows.
	path := ObservationLedgerPath(t.TempDir())
	big := &ObservationLedger{Version: 1}
	for i := range 500 {
		big.Observations = append(big.Observations, Observation{ID: CSessionStartFires, TS: core.UnixMilli(i)})
	}
	b, err := json.Marshal(big)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, b, 0o600))
	require.Len(t, LoadObservationLedger(path).Observations, maxObservationsPerID)
}

// TestSaveObservationLedger_NilIsAFreshLedger mirrors SaveHistory's own nil tolerance: a caller
// with nothing to record writes an empty ledger rather than an error nobody handles.
func TestSaveObservationLedger_NilIsAFreshLedger(t *testing.T) {
	t.Parallel()

	path := ObservationLedgerPath(t.TempDir())
	require.NoError(t, SaveObservationLedger(path, nil))
	require.Empty(t, LoadObservationLedger(path).Observations)
}
