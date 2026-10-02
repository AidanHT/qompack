package obs

import "os"

// NonReferenceDiskEnv is the environment variable through which a hosted CI job declares that the
// run's store sits on a NON-REFERENCE DISK: a GitHub-hosted runner's, whose fsync latency carries a
// tail no wall-clock budget priced on a reference host survives. It is the owner's Q1 ruling, third
// option (plans/V6-CLOSEOUT-CHECKLIST.md: "C7.2 hosted runners report-only for fsync-bound rows"),
// applied by coordinator decision D53(e): ci.yml reports hosted fsync-bound rows without gating
// them, keeps the structural ledger checks gated, and names a reason other than co-load. D55
// extended it: the declaration reports B-A as well as B-B and B-E's wall row (B-A contains B-B's
// ingest, D41), and nightly.yml's bench-deep declares it too, so no hosted job gates those three
// rows (ADR 0010 Addendum 2); they are judged on a reference disk, the owner's quiet runs.
//
// The evidence is CI's own. Alone on their runners in run 36816905394's bench-gate, ubuntu-latest
// measured B-B at p50 0.576 ms but p99 40.960 ms against 15, and windows-latest at p50 45.056 ms
// against 50, while the quiet Windows reference run (C5.1, D53(b)) read B-B p99 22.5 ms. The first
// three ci.yml runs (2026-09-14) put the same row on one windows-latest runner class at 73.7 ms in
// bench-gate, 98.3 ms in the timing job and 2 359 ms in test-e2e: a thirtyfold spread in one run is
// a throttled disk, not a delivery path. Hosted figures never become constants (Q1), and the
// runner is not co-loaded either, so UnderColoadEnv would name the wrong cause.
//
// What it licenses is exactly this, and nothing more:
//
//   - test/bench/hotpath REPORTS, instead of gating, the wall-clock rows whose region holds the
//     durable path's fsyncs — B-A, B-B and B-E's wall row — each with a note naming this variable
//     as the reason, in the printed summary and in the JSON artifact;
//   - the hot-path tests that drive the harness (test/integration's
//     TestIntegration_HotPathWarmWithRealResidentState, test/e2e's X11) REPORT the §12.2 spool
//     submode transition, and the hook deferrals that follow it, instead of forbidding them, and
//     still require a loud, named transition and a delivery ledger that adds up with 0 lost;
//   - test/e2e's X10 (TestV5_ThrashWarningVisibleInStatusAndCheckpoint) answers a prompt reply that
//     was observably late (l0_prompt_reply_late) or deferred to the hook's client spool with a
//     proof of RECOVERY — the warning re-armed, and carried by the prompt after one more loop
//     cycle — instead of failing on first-prompt delivery. A reply that was on time and carried
//     nothing still fails, every other property of the warning is asserted as before, and the
//     branch is written to the job summary, so a green run still says it took it (w16d-warnlate).
//
// B-E_cpu, the delivery-ledger identity check, 0 lost, the population census and every structural
// check stay gated exactly as before. The reference verdict on those wall rows is the owner's quiet
// local runs (quiet.sh's C5.1, the isolated D28 rows), which never set this variable: test/guards'
// TestNonReferenceDisk_IsHostedCIOnly keeps them that way.
//
// It is honoured only where GitHubActionsEnv is "true" — the variable GitHub sets on every hosted
// runner — so a local run cannot waive a gate with it by accident or on purpose; outside GitHub
// Actions it is ignored, and the harness says so in its notes. Unset by default, so forgetting it
// can only ever make a run STRICTER.
const NonReferenceDiskEnv = "QOMPACK_NONREFERENCE_DISK"

// GitHubActionsEnv is the variable GitHub Actions sets to "true" on every runner it starts.
const GitHubActionsEnv = "GITHUB_ACTIONS"

// NonReferenceDiskDeclared reports whether NonReferenceDiskEnv is set to anything non-empty,
// honoured or not: the harness uses it to say that a declaration was IGNORED.
func NonReferenceDiskDeclared() bool {
	return os.Getenv(NonReferenceDiskEnv) != ""
}

// NonReferenceDisk reports whether the run has declared a non-reference disk AND is a GitHub
// Actions run, the only place the declaration is honoured. See NonReferenceDiskEnv.
func NonReferenceDisk() bool {
	return NonReferenceDiskDeclared() && os.Getenv(GitHubActionsEnv) == "true"
}
