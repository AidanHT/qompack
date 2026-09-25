// V5 §4.14 — every contract assertion's evidence is attributed per capability, and an absent
// producer is recorded as `unavailable`, never as success.
//
// Current criterion (plans/V5-VERIFY §4 row 4.14): "SP-19 per-capability observed/unknown evidence,
// no fixed producer count or setter claim."
//
// Wired: the real binary's hook subcommands, the daemon composed the way internal/cli composes it
// (v4_harness_test.go), the §12.1 monitor's StandardAssertions with their real Checks, the
// daemon's SessionStart route (which runs the monitor and appends its results to
// state/observations.json through contract.ObservationsOf), the worker-pool sentinel scan the
// UserPromptSubmit route triggers, the PreCompact route's marker/history writes, and the status
// surface's Contract results. Nothing here is stubbed.
//
// Retired clauses this row must NOT assert (reconciliation map §4.14): the historical text required
// "all nine" assertions to carry a real observation and forbade `not-yet-implemented` on four named
// ids. Both are retired. The producer set is a property of the composition, read live through
// contract.HasProducer, so the row asserts the EQUIVALENCE "unavailable iff undeclared" rather than
// any count; and precompact.custom_instructions_accepted is attributed to compaction_request, which
// the register calls unsupported — Qompack.md §7.3: custom_instructions is PreCompact INPUT, and
// a phrase found in a transcript is not evidence an invented setter worked. That assertion lands as
// `unsupported` however its Result reads.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
)

// The session identities, distinct from every other file's so the package can run in one process
// without two rows sharing a draft, a registry entry or a segment range.
const (
	// x14v5SessionA is the session the full arm drives through injection, PreCompact and the
	// compact restart.
	x14v5SessionA = core.SessionID("sess-e2e-v5-x14-a")
	// x14v5SessionB is the NEXT session of the same project: it is what makes session_start.fires
	// observable (the marker names a session other than the one starting).
	x14v5SessionB = core.SessionID("sess-e2e-v5-x14-b")
	// x14v5CtlSession is the negative control's session: its transcript never carries the probe.
	x14v5CtlSession = core.SessionID("sess-e2e-v5-x14-ctl")
	// x14v5ObsSession is the observer-only composition's session.
	x14v5ObsSession = core.SessionID("sess-e2e-v5-x14-obs")
)

// x14v5NotYetImplemented is §12.1's exact Observed string for an assertion whose producer is
// absent (internal/contract/standard.go, unexported there and frozen by the golden fixture).
const x14v5NotYetImplemented = "not-yet-implemented"

// x14v5HostProvider is the provider the daemon stamps on the ledger's Target
// (internal/daemon/observations.go hostProvider; Qompack.md §7.1). A hook payload carries no host
// version, so Version stays empty rather than being invented.
const x14v5HostProvider = "claude-code"

// x14v5ScanBound / x14v5ScanTick bound and pace the wait for the worker-pool sentinel scan to
// land in state/history.json. Generous on purpose — the machine may be under parallel load — and
// a bound, never a latency assertion.
const (
	x14v5ScanBound = 30 * time.Second
	x14v5ScanTick  = 25 * time.Millisecond
)

// x14v5SeedTurns is how many tool uses the full arm feeds the observer before its PreCompact, so
// the checkpoint has pointers to seal (v4_x08 seeds the same three).
const x14v5SeedTurns = 3

// x14v5SentinelChances is §12.1's "two chances": the number of UserPromptSubmit scans that must
// miss the probe before hook.additional_context_delivered fails (contract.checkAdditionalContext-
// Delivered's `Chances < 2`).
const x14v5SentinelChances = 2

// x14v5Transcript writes a real JSONL transcript whose last line is valid JSON — what
// transcript.readable probes — and returns its path. It is the file cpPreCompactPayload names.
func x14v5Transcript(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "transcript.jsonl")
	line := `{"type":"user","message":{"role":"user","content":"read the auth module"}}` + "\n"
	require.NoError(t, os.WriteFile(paths.Long(path), []byte(line), 0o600))
	return path
}

// x14v5HostFolds plays the host: it appends the injected additionalContext to the transcript as
// one JSON line, which is exactly what the UserPromptSubmit sentinel scan reads back.
func x14v5HostFolds(t *testing.T, transcript, ac string) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"type": "system", "content": ac})
	require.NoError(t, err)
	f, err := os.OpenFile(paths.Long(transcript), os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	_, werr := f.Write(append(b, '\n'))
	require.NoError(t, f.Close())
	require.NoError(t, werr)
}

// x14v5StartPayload builds a SessionStart payload that names the transcript, so the assertions
// that read transcript_path have something real to read.
func x14v5StartPayload(t *testing.T, root string, sess core.SessionID, source, transcript string) []byte {
	t.Helper()
	b, err := json.Marshal(hookio.Event{
		HookEventName: "SessionStart", SessionID: sess, CWD: root, Source: source, TranscriptPath: transcript,
	})
	require.NoError(t, err)
	return b
}

// x14v5PromptPayload builds a UserPromptSubmit payload that names the transcript, which is what
// the daemon's sentinel scan (internal/daemon/handlers.go scanSentinelForPrompt) reads.
func x14v5PromptPayload(t *testing.T, root string, sess core.SessionID, transcript, prompt string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "UserPromptSubmit", "session_id": sess, "cwd": root,
		"transcript_path": transcript, "prompt": prompt,
	})
	require.NoError(t, err)
	return b
}

// x14v5Start runs `qompack session-start` through the real binary against the rig's daemon.
func x14v5Start(t *testing.T, r *v4Rig, payload []byte) hookio.Output {
	t.Helper()
	return r.Hook(t, []string{"session-start"}, payload)
}

// x14v5AdditionalContext returns the additionalContext a SessionStart emitted, failing if none.
func x14v5AdditionalContext(t *testing.T, out hookio.Output) string {
	t.Helper()
	require.NotNil(t, out.HookSpecificOutput, "a full-mode SessionStart must emit hookSpecificOutput")
	return out.HookSpecificOutput.AdditionalContext
}

// x14v5WaitSentinel drives Drain and polls state/history.json until pred holds of the sentinel
// state, bounded by x14v5ScanBound. The scan runs on the ingest worker pool, off the reply path,
// so the hook's exit is not the scan's completion.
func x14v5WaitSentinel(t *testing.T, r *v4Rig, what string, pred func(contract.SentinelState) bool) {
	t.Helper()
	ctx := context.Background()
	ticker := time.NewTicker(x14v5ScanTick)
	defer ticker.Stop()
	timeout := time.NewTimer(x14v5ScanBound)
	defer timeout.Stop()
	for {
		_, _ = r.D.Drain(ctx)
		h := contract.LoadHistory(contract.HistoryPath(r.P.Root))
		if pred(h.Sentinel) {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			require.FailNowf(t, "the sentinel scan never landed",
				"waiting for %s: sentinel state after %s is %+v; LOUD: %v",
				what, x14v5ScanBound, h.Sentinel, loudLines(t, r.P.Root))
		}
	}
}

// x14v5Ledger loads state/observations.json, first requiring that the file exists:
// LoadObservationLedger returns a fresh ledger for a missing file, and a fresh ledger would make
// every "no observation is X" assertion below vacuous.
func x14v5Ledger(t *testing.T, root string) *contract.ObservationLedger {
	t.Helper()
	path := contract.ObservationLedgerPath(root)
	_, err := os.Stat(paths.Long(path))
	require.NoError(t, err, "the daemon must have written %s on SessionStart", path)
	return contract.LoadObservationLedger(path)
}

// x14v5Runs returns scope's observations grouped into RunAll runs, in arrival order. One run is
// one observation per standard assertion, so the count must divide exactly.
func x14v5Runs(t *testing.T, led *contract.ObservationLedger, scope core.SessionID) [][]contract.Observation {
	t.Helper()
	n := len(contract.StandardAssertions())
	var mine []contract.Observation
	for _, o := range led.Observations {
		if o.Scope == string(scope) {
			mine = append(mine, o)
		}
	}
	require.NotEmpty(t, mine, "no observation carries scope %s", scope)
	require.Zero(t, len(mine)%n, "scope %s holds %d observations, not a whole number of %d-assertion runs", scope, len(mine), n)
	runs := make([][]contract.Observation, 0, len(mine)/n)
	for i := 0; i < len(mine); i += n {
		runs = append(runs, mine[i:i+n])
	}
	return runs
}

// x14v5ByID indexes one run by assertion id, requiring every standard assertion exactly once.
func x14v5ByID(t *testing.T, run []contract.Observation) map[contract.ID]contract.Observation {
	t.Helper()
	by := make(map[contract.ID]contract.Observation, len(run))
	for _, o := range run {
		_, dup := by[o.ID]
		require.False(t, dup, "assertion %s appears twice in one run", o.ID)
		by[o.ID] = o
	}
	for _, a := range contract.StandardAssertions() {
		require.Contains(t, by, a.ID, "one run must carry every standard assertion")
	}
	return by
}

// x14v5AssertLedgerInvariants is the §12.1 (v1.5) reading of the ledger, checked on EVERY
// observation regardless of which arm produced it:
//
//   - the target is the host this binary runs under, with no invented version;
//   - every observation is attributed to one of the register's capabilities, carries that
//     capability's own mechanism, and one of the six closed outcomes;
//   - coverage is never "complete", and the one narrow claim is made only by an observed injection;
//   - `unavailable` holds exactly when the producer is undeclared IN THIS PROCESS — read live
//     through contract.HasProducer, never as a count — and exactly when the Result said
//     not-yet-implemented;
//   - a mechanism the register calls unsupported is `unsupported` whenever it was attempted at all;
//   - the outcome is re-derivable from the observation's own verbatim Observed string, which is
//     what lets it be disputed rather than merely trusted.
func x14v5AssertLedgerInvariants(t *testing.T, led *contract.ObservationLedger, reg contract.CapabilityRegister) {
	t.Helper()

	require.Equal(t, x14v5HostProvider, led.Target.Provider)
	require.Empty(t, led.Target.Version, "a hook payload carries no host version; none may be invented")
	require.Equal(t, runtime.GOOS+"/"+runtime.GOARCH, led.Target.Platform)
	require.NotEmpty(t, led.Target.Date, "evidence without a date cannot be aged out")

	known := map[contract.Capability]bool{}
	for _, c := range contract.Capabilities() {
		known[c] = true
	}
	outcomes := map[contract.Outcome]bool{
		contract.OutcomeObserved: true, contract.OutcomeNotObserved: true, contract.OutcomeUnavailable: true,
		contract.OutcomeUnsupported: true, contract.OutcomeUnknown: true, contract.OutcomeFailed: true,
	}

	for _, o := range led.Observations {
		require.True(t, known[o.Capability], "%s is attributed to %q, which is not a register capability", o.ID, o.Capability)
		require.Equal(t, contract.CapabilityOf(o.ID), o.Capability, "%s: attribution must be the package's own", o.ID)
		rec, ok := reg.Get(o.Capability)
		require.True(t, ok, "%s: the register has no record for %s", o.ID, o.Capability)
		require.Equal(t, rec.Mechanism, o.Mechanism, "%s: the observation carries its capability's mechanism", o.ID)
		require.True(t, outcomes[o.Outcome], "%s: outcome %q is not one of the six", o.ID, o.Outcome)
		require.NotEqual(t, contract.OutcomeUnknown, o.Outcome, "%s: a standard assertion is never unattributable", o.ID)
		require.Equal(t, led.Target, o.Target, "%s: every observation names the ledger's target", o.ID)
		require.NotZero(t, o.TS, "%s: an observation keeps its Result's own timestamp", o.ID)
		require.NotEmpty(t, o.Observed, "%s: the verbatim Observed string is the disputable record", o.ID)

		require.NotEqual(t, "complete", o.Coverage, "%s: complete coverage is unspellable", o.ID)
		wantCoverage := contract.CoverageNone
		if o.Capability == contract.CapInjection && o.Outcome == contract.OutcomeObserved {
			wantCoverage = contract.CoverageDeliveryUnderTestedContract
		}
		require.Equal(t, wantCoverage, o.Coverage, "%s: only an observed injection supports the narrow delivery claim", o.ID)

		undeclared := !contract.HasProducer(o.ID)
		require.Equal(t, undeclared, o.Outcome == contract.OutcomeUnavailable,
			"%s: unavailable must hold exactly when the producer is undeclared in this process (declared=%v, outcome=%s)",
			o.ID, !undeclared, o.Outcome)
		require.Equal(t, o.Observed == x14v5NotYetImplemented, o.Outcome == contract.OutcomeUnavailable,
			"%s: unavailable must hold exactly when the Result said %q (observed=%q, outcome=%s)",
			o.ID, x14v5NotYetImplemented, o.Observed, o.Outcome)

		if rec.Status == contract.StatusUnsupported && o.Outcome != contract.OutcomeUnavailable {
			require.Equal(t, contract.OutcomeUnsupported, o.Outcome,
				"%s: the register calls %s unsupported, so an attempted observation is never evidence (observed=%q)",
				o.ID, o.Capability, o.Observed)
		}

		rederived := contract.ClassifyResult(contract.Result{
			ID: o.ID, OK: o.Outcome != contract.OutcomeFailed, Observed: o.Observed,
		}, reg)
		require.Equal(t, o.Outcome, rederived, "%s: the outcome must re-derive from its own Observed %q", o.ID, o.Observed)
	}
}

// TestV5_EveryContractAssertionHasARealProducer is V5-VERIFY §4.14.
//
// Three compositions, each a real one:
//
//  1. the full wave-3 composition, driven through injection, PreCompact and a compact restart, so
//     the capabilities whose producers this composition declares land as `observed`, the ones
//     nothing drove land as `not_observed`, the undeclared one lands as `unavailable`, and the
//     unsupported one lands as `unsupported` — then the same run read back through the status
//     surface and `qompack self-test`;
//  2. the NEGATIVE CONTROL: the same composition with the delivery severed — the host never folds
//     the injected context into the transcript — so the same assertion lands as `failed`, LOUD,
//     and the daemon degrades. This is what proves arm 1's `observed` came from the transcript
//     carrying the probe and not from the producer merely being declared;
//  3. the observer-only composition, whose producer set differs, so the SAME assertion that arm 1
//     observed is `unavailable` here. The producer set is the composition's, not a number.
func TestV5_EveryContractAssertionHasARealProducer(t *testing.T) {
	reg := contract.DefaultCapabilityRegister()
	require.NoError(t, reg.Validate(), "the shipped register must be one this build may act on")

	t.Run("full_composition_records_per_capability_evidence", func(t *testing.T) {
		p := v4Project(t)
		r := v4StartRig(t, p)
		env := e2eEnv(p)
		transcript := x14v5Transcript(t, p.Root)

		// ── Session A, first start: the probe is minted and emitted ─────────────────────────
		out1 := x14v5Start(t, r, x14v5StartPayload(t, p.Root, x14v5SessionA, "startup", transcript))
		require.Empty(t, out1.SystemMessage, "a clean first start carries no degrade banner")
		ac1 := x14v5AdditionalContext(t, out1)
		require.Contains(t, ac1, scProbePrefix, "the session.start route appends its §12.1 probe")

		// The host folds the injected context into the transcript; the next prompt's scan finds it.
		x14v5HostFolds(t, transcript, ac1)
		obsRunHook(t, r.Bin, []string{"observe", "prompt"},
			x14v5PromptPayload(t, p.Root, x14v5SessionA, transcript, "first prompt of session A"), env)
		x14v5WaitSentinel(t, r, "the probe to be observed", func(s contract.SentinelState) bool { return s.Observed })

		// ── PreCompact: marker and timing sample recorded by the real route, and a real seal ──────
		// Criterion change (C1.18): this used to prove the full-mode route ran by the instruction it
		// recorded into the contract history. That instruction is retired — no host accepts one —
		// so nothing is recorded, and the seal is proven by the artifact instead.
		r.SeedTurns(t, x14v5SessionA, "v5x14", x14v5SeedTurns)
		r.PreCompact(t, x14v5SessionA)
		require.NotEmpty(t, cpCheckpointArtifacts(t, p.Root), "a full-mode PreCompact route seals a checkpoint")
		require.Empty(t, contract.LoadHistory(contract.HistoryPath(p.Root)).PrecompactInstr,
			"no route records a retired instruction into the contract history")

		// ── Session A restarts from the compaction; then session B starts ─────────────────────
		out2 := x14v5Start(t, r, x14v5StartPayload(t, p.Root, x14v5SessionA, "compact", transcript))
		require.Empty(t, out2.SystemMessage)
		out3 := x14v5Start(t, r, x14v5StartPayload(t, p.Root, x14v5SessionB, "startup", transcript))
		require.Empty(t, out3.SystemMessage)

		snap := e2eStatus(t, p.Root)
		require.Equal(t, contract.ModeFull.String(), snap.Mode, "nothing in this arm breaks a contract")

		// ── The ledger ────────────────────────────────────────────────────────────────────────
		led := x14v5Ledger(t, p.Root)
		x14v5AssertLedgerInvariants(t, led, reg)
		runsA := x14v5Runs(t, led, x14v5SessionA)
		runsB := x14v5Runs(t, led, x14v5SessionB)
		require.Len(t, runsA, 2, "session A started twice")
		require.Len(t, runsB, 1, "session B started once")
		require.Len(t, led.Observations, 3*len(contract.StandardAssertions()),
			"three SessionStarts, one observation per standard assertion each, nothing else")
		first, compact, next := x14v5ByID(t, runsA[0]), x14v5ByID(t, runsA[1]), x14v5ByID(t, runsB[0])

		// Run 1: nothing had a chance to be delivered yet, and there was no prior session.
		// Absence of evidence is recorded as absence, not as success and not as failure.
		require.Equal(t, contract.OutcomeNotObserved, first[contract.CAdditionalContext].Outcome)
		require.Equal(t, "not-yet-observed", first[contract.CAdditionalContext].Observed)
		require.Equal(t, contract.CoverageNone, first[contract.CAdditionalContext].Coverage)
		require.Equal(t, contract.OutcomeNotObserved, first[contract.CSessionStartFires].Outcome)
		require.Equal(t, "first-session", first[contract.CSessionStartFires].Observed)

		// Run 2 (the compact restart): the injection was delivered under the tested contract, the
		// restart carried source=compact, and the PreCompact left a timing sample.
		require.Equal(t, contract.OutcomeObserved, compact[contract.CAdditionalContext].Outcome)
		require.Equal(t, "sentinel-observed", compact[contract.CAdditionalContext].Observed)
		require.Equal(t, contract.CoverageDeliveryUnderTestedContract, compact[contract.CAdditionalContext].Coverage,
			"one delivery under the tested contract — the only coverage an injection may claim")
		require.Equal(t, contract.OutcomeObserved, compact[contract.CSessionStartSourceCompact].Outcome)
		require.Equal(t, "compact", compact[contract.CSessionStartSourceCompact].Observed)
		require.Equal(t, contract.OutcomeObserved, compact[contract.CPreCompactTiming].Outcome)
		require.True(t, strings.HasPrefix(compact[contract.CPreCompactTiming].Observed, "p99="),
			"a real wall-time sample, not a placeholder: %q", compact[contract.CPreCompactTiming].Observed)

		// The setter claim, retired: the producer IS declared and its Check DID run (its Observed
		// is not the not-yet-implemented placeholder), and whatever the transcript scan reported
		// the ledger attributes it to compaction_request and records `unsupported`.
		require.True(t, contract.HasProducer(contract.CPreCompactCustomInstr))
		require.NotEqual(t, x14v5NotYetImplemented, compact[contract.CPreCompactCustomInstr].Observed)
		require.Equal(t, contract.CapCompactionRequest, compact[contract.CPreCompactCustomInstr].Capability)
		require.Equal(t, contract.OutcomeUnsupported, compact[contract.CPreCompactCustomInstr].Outcome,
			"a phrase found (or not) in a transcript is not evidence an invented setter exists")

		// Run 3 (session B): the marker A's PreCompact wrote is found by a DIFFERENT session's
		// start; the compact obligation was already resolved; payload and transcript observed.
		require.Equal(t, contract.OutcomeObserved, next[contract.CSessionStartFires].Outcome)
		require.Equal(t, "marker-found", next[contract.CSessionStartFires].Observed)
		require.Equal(t, contract.OutcomeNotObserved, next[contract.CSessionStartSourceCompact].Outcome)
		require.Equal(t, "no-precompact-pending", next[contract.CSessionStartSourceCompact].Observed)
		require.Equal(t, contract.OutcomeObserved, next[contract.CHookPayloadShape].Outcome)
		require.Equal(t, contract.OutcomeObserved, next[contract.CTranscriptReadable].Outcome)
		require.Equal(t, "transcript readable", next[contract.CTranscriptReadable].Observed)
		// No MCP initialize ever arrived in this arm, so under no composition may it be observed;
		// which of unavailable/failed it is belongs to the composition, and the invariants above
		// already tie that to HasProducer.
		require.NotEqual(t, contract.OutcomeObserved, next[contract.CMCPRegistered].Outcome)

		// ── The status surface agrees, id by id, with the ledger's last run ───────────────────
		require.Len(t, snap.Contract, len(contract.StandardAssertions()))
		for _, res := range snap.Contract {
			o, ok := next[res.ID]
			require.True(t, ok, "status reports %s, which the ledger's last run lacks", res.ID)
			require.Equal(t, res.Observed, o.Observed, "%s: the ledger keeps the status Result's Observed verbatim", res.ID)
			require.Equal(t, contract.ClassifyResult(res, reg), o.Outcome,
				"%s: classifying the status Result must give the ledger's outcome", res.ID)
		}

		t.Run("self_test_reads_the_same_ids_and_writes_no_evidence", func(t *testing.T) {
			ledgerPath := contract.ObservationLedgerPath(p.Root)
			before, err := os.ReadFile(paths.Long(ledgerPath))
			require.NoError(t, err)

			stdout, stderr, code := Run(t, r.Bin, []string{"self-test", "--json"}, nil, env)
			require.Equal(t, 0, code, "self-test against a healthy project\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			var report struct {
				Checks []struct {
					ID       string `json:"id"`
					OK       bool   `json:"ok"`
					Observed string `json:"observed"`
				} `json:"checks"`
				Mode string `json:"mode"`
				Exit int    `json:"exit"`
			}
			require.NoError(t, json.Unmarshal(stdout, &report), "self-test --json must be one document:\n%s", stdout)
			require.Equal(t, contract.ModeFull.String(), report.Mode)
			require.Zero(t, report.Exit)

			byID := map[string]contract.Result{}
			for _, c := range report.Checks {
				byID[c.ID] = contract.Result{ID: contract.ID(c.ID), OK: c.OK, Observed: c.Observed}
			}
			for _, a := range contract.StandardAssertions() {
				res, ok := byID[string(a.ID)]
				require.True(t, ok, "self-test must report %s", a.ID)
				o := contract.ClassifyResult(res, reg)
				require.NotEqual(t, contract.OutcomeUnknown, o, "%s attributes to a capability", a.ID)
				if res.Observed == x14v5NotYetImplemented {
					// self-test's monitor is throwaway and declares only what its own process
					// declares; a placeholder there is `unavailable`, never a working capability.
					require.Equal(t, contract.OutcomeUnavailable, o, "%s", a.ID)
				}
				if a.ID == contract.CPreCompactCustomInstr {
					require.NotEqual(t, contract.OutcomeObserved, o, "no setter claim from self-test either")
				}
			}

			after, err := os.ReadFile(paths.Long(ledgerPath))
			require.NoError(t, err)
			require.Equal(t, before, after, "self-test is diagnostic: it appends no observation")
		})
	})

	t.Run("negative_control_severed_delivery_is_failed_and_loud", func(t *testing.T) {
		p := v4Project(t)
		r := v4StartRig(t, p)
		env := e2eEnv(p)
		transcript := x14v5Transcript(t, p.Root)

		out1 := x14v5Start(t, r, x14v5StartPayload(t, p.Root, x14v5CtlSession, "startup", transcript))
		require.Empty(t, out1.SystemMessage)
		ac := x14v5AdditionalContext(t, out1)
		require.Contains(t, ac, scProbePrefix, "the probe was minted and emitted — the producer is present")
		require.True(t, contract.HasProducer(contract.CAdditionalContext), "this composition declares the injection producer")

		// The host drops the injected context: the transcript never carries the probe. Two
		// prompts spend the two chances.
		for i := 1; i <= x14v5SentinelChances; i++ {
			obsRunHook(t, r.Bin, []string{"observe", "prompt"},
				x14v5PromptPayload(t, p.Root, x14v5CtlSession, transcript, fmt.Sprintf("prompt %d, probe never delivered", i)), env)
			want := i
			x14v5WaitSentinel(t, r, fmt.Sprintf("chance %d to be spent", i),
				func(s contract.SentinelState) bool { return !s.Observed && s.Chances >= want })
		}
		raw, err := os.ReadFile(paths.Long(transcript))
		require.NoError(t, err)
		require.NotContains(t, string(raw), scProbePrefix, "fixture: the transcript must not carry the probe")

		out2 := x14v5Start(t, r, x14v5StartPayload(t, p.Root, x14v5CtlSession, "startup", transcript))
		require.NotEmpty(t, out2.SystemMessage,
			"NEGATIVE CONTROL: a critical contract failure is never silent — the SessionStart carries the degrade banner")
		snap := e2eStatus(t, p.Root)
		require.Equal(t, contract.ModeDegradedPassive.String(), snap.Mode, "the daemon degraded to passive recording")

		led := x14v5Ledger(t, p.Root)
		x14v5AssertLedgerInvariants(t, led, reg)
		runs := x14v5Runs(t, led, x14v5CtlSession)
		require.Len(t, runs, 2)
		second := x14v5ByID(t, runs[1])
		require.Equal(t, contract.OutcomeFailed, second[contract.CAdditionalContext].Outcome,
			"NEGATIVE CONTROL: with delivery severed the same assertion arm 1 observed must land as failed — "+
				"attempted and not honoured, which is neither unavailable nor success")
		require.Equal(t, "sentinel not found after two chances", second[contract.CAdditionalContext].Observed)
		require.Equal(t, contract.CoverageNone, second[contract.CAdditionalContext].Coverage)

		// Nothing was sealed in this arm, so the append-only conformance list can be run whole.
		p.AssertAppendOnly(t)
	})

	t.Run("observer_only_composition_declares_a_different_producer_set", func(t *testing.T) {
		// The producer set is process-wide and monotonic (contract.DeclareProducer is additive);
		// the previous arms' daemons are torn down, so resetting here is what lets THIS
		// composition's declarations be read on their own. The same reset the existing
		// TestE2E_AdditionalContextProducerIsDeclared performs.
		contract.ResetProducers()
		t.Cleanup(contract.ResetProducers)

		p := v4Project(t)
		r := v4StartObserverOnly(t, p)
		transcript := x14v5Transcript(t, p.Root)

		// What this composition declares, read live rather than counted: the observer's wiring
		// reaches the rehydrator, so injection is declared; no checkpoint writer or PreCompact
		// seam is bound, so the PreCompact pair is not; no MCP op is installed.
		require.True(t, contract.HasProducer(contract.CAdditionalContext))
		require.False(t, contract.HasProducer(contract.CPreCompactTiming))
		require.False(t, contract.HasProducer(contract.CPreCompactCustomInstr))
		require.False(t, contract.HasProducer(contract.CMCPRegistered))

		out := x14v5Start(t, r, x14v5StartPayload(t, p.Root, x14v5ObsSession, "startup", transcript))
		require.Empty(t, out.SystemMessage, "an absent producer never degrades a session")

		led := x14v5Ledger(t, p.Root)
		x14v5AssertLedgerInvariants(t, led, reg)
		runs := x14v5Runs(t, led, x14v5ObsSession)
		require.Len(t, runs, 1)
		run := x14v5ByID(t, runs[0])

		// The assertion arm 1 OBSERVED is unavailable here — same binary, different composition.
		require.Equal(t, contract.OutcomeUnavailable, run[contract.CPreCompactTiming].Outcome)
		require.Equal(t, x14v5NotYetImplemented, run[contract.CPreCompactTiming].Observed)
		require.Equal(t, contract.CoverageNone, run[contract.CPreCompactTiming].Coverage)
		// Undeclared wins over unsupported: nothing was attempted, so nothing is attributed.
		require.Equal(t, contract.OutcomeUnavailable, run[contract.CPreCompactCustomInstr].Outcome)
		require.Equal(t, contract.OutcomeUnavailable, run[contract.CMCPRegistered].Outcome)
		// The declared-but-undriven injection is absence of evidence, not unavailability.
		require.Equal(t, contract.OutcomeNotObserved, run[contract.CAdditionalContext].Outcome)
		require.Equal(t, "not-yet-observed", run[contract.CAdditionalContext].Observed)
	})
}
