# Linux runtime and product-child race corrections

2026-09-22; verify/v6. This is corrective evidence, not release acceptance.

The first instrumented product-child run (`linux-product-child-race`) found a
race in the MCP test harness: `os/exec` wrote stderr to a plain `bytes.Buffer`
while assertions read it. The captured detector report remains in
`linux-product-child-race-artifacts/race.8310`. A mutex-protected writer/string
buffer fixes the shared access. The focused concurrent helper test passed on
Windows under the race detector. The real MCP session passed in the corrected
Linux run; the original failure is retained.

That corrected run (`linux-product-child-race-corrected`) passed seven selected
e2e cases, including the executable instrumentation assertion, but failed X10
crash recovery. An exited daemon was retained as a zombie by the container's
PID 1. `kill(pid, 0)` still succeeded, so the daemon lock refused recovery.
The earlier cleanup helper had the same mistaken liveness assumption.

Linux production and test helpers now inspect `/proc/PID/stat` after a successful
signal-zero probe. Exited Z/X states are dead. Inaccessible or ambiguous state
remains conservatively alive; the lock's successful IPC probe still takes
precedence. Other platforms retain their existing implementations. The regression
uses a real exited child before `Wait`, confirms signal-zero still succeeds, and
requires reclaim even with a fresh heartbeat. Existing live-lock, exclusive-lock,
reclaim, and initial-publication-window assertions are preserved.

`linux-lock-recovery-race` passed all six selected lock cases and the real X10
crash-recovery case. The test inspected the actual child and confirmed `-race=true`,
Go 1.26.6, SHA256
`6001ce742deff34dcd8f8f1870e9e8be50c0e94368ab901c3ae45a280b67874b`.
X10 took 120.74 seconds; this is functional instrumentation evidence, not a quiet
latency measurement. No detector artifact was emitted in that run. The new
nightly lane captures both harness and child race reports and fails on either.

The B and C executed-source records identify immutable Linux snapshots and verify
every source hash after execution. The Windows run wrapper's mutable-worktree
hash is not substituted for those actual Linux sources. Docker/WSL is qualified;
these runs have no installed Claude host and do not certify native macOS, an
installed plugin bundle, human UAT, or all races in every product path.

Scoped Windows and Linux lint passed. The first lint command used an absent v2
package path; the first Linux lint launch mistakenly built a Linux tool executable
on Windows. Both invocation failures are retained. The corrected Linux run uses
the pinned native Windows lint executable with Linux target package selection.

## Historical CI evidence

GitHub nightly run `35704111100` used historical source
`9c84e31d596ff2357cceeed42af240c8b6642dd5`: 26 jobs succeeded and three failed.
The saved job list and downloaded failure logs disprove the earlier inventory's
claim that no workflow had ever run. They do not certify this candidate.
Linux B-B reported p99 36.864 ms against 15 ms; Windows B-B reported p99
65.536 ms against 50 ms. Windows race reported two permission-fixture failures
and one concurrent-write request deadline. No current billing blockage is inferred.
Recorded replay job success alone does not prove a corpus was configured.
