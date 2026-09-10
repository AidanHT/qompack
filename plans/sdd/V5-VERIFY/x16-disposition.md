# V5-VERIFY §4.16 disposition — `TestV5_NoPackageWritesOutsideDotQompack`

| Field | Value |
|---|---|
| Retained identifier | `TestV5_NoPackageWritesOutsideDotQompack` |
| Current criterion (plan §4 row) | SP-17 trust/retention/write-set boundaries with explicit allowed data writes and no project mutation. |
| Disposition | **authored** |
| Level and file | e2e — `test/e2e/v5_x16_test.go` (real binary; one arm over the in-process composed daemon, one over the shipped daemon the binary spawns) |
| Base | `verify/v5` @ 87c0c1d |

## Why e2e

The seam crosses processes: every hook and every SP-14 command is a spawned `qompack` child, the
second arm's daemon is the shipped `qompack daemon` process, and the negative control is a
spawned `qompack eval import`. The historical seam (v3_x09 → v4_x11) was e2e; it is followed.

## Test names and what each asserts

`TestV5_NoPackageWritesOutsideDotQompack` with four subtests. Every arm snapshots THREE roots
before and after (x9Snapshot: FNV-64a of every regular file): the project, HOME, and the built
binary's directory — which harness.go's `Run` makes every child's working directory, so a
cwd-relative write (the shape a plugin-install-directory write takes) is now visible.

| Subtest | Drives | Asserts |
|---|---|---|
| `ComposedDaemon` | v4StartRig (checkpoint writer, pins, ledger, frontier) + real binary: session-start, observe prompt, 2×8 observe tool, `status`, `status --json`, `pin <text>`, `recall handler`, closed segments, idle pass, `checkpoint` (PreCompact, sealed), SessionStart(source=compact) (rehydrate), `flush` | (1) invariant 7: every created/modified/deleted path is under `<project>/.qompack/` or `<home>/.qompack/`; nothing in the binary's directory changed. (2) explicit allowed data writes: every path created under `.qompack/` is `.gitignore` or lives under a directory `paths.Layout` names (allowlist built from `paths.Of`, so it cannot drift); every HOME write is `calibration.json`. (3) the writers really wrote: `index/tool_use.jsonl`, `checkpoints/`, `pins/`, `state/` present in the delta. (4) `recall` without the MCP layer reports "retrieval is temporarily offline" at non-zero exit — unavailability, never absence. (5) no project mutation: the four fixture files byte-identical, `.qompack/.gitignore == "*\n"`, `tmp/` empty. (6) append-only growth: mid-session logs are a byte prefix of the end-of-session logs. |
| `ShippedDaemon` | real binary only: session-start (lazy-spawns the shipped daemon), observe prompt, 8× observe tool, `observe stop`, `status`, `status --json`, `recall --k 5 handler`, `dropped`, `flush`, then shutdown | (1)(2)(5)(6) as above over the composition `internal/cli.runDaemon` actually ships; (3) the writers the rig does not transcribe: `state/scheduler.json`, `state/bocd.json` (SP-12), `state/observations.json`, `run/marker.json` (SP-19); (7) `p.AssertAppendOnly` — the §7.4 conformance list (O_TRUNC on a checkpoint, in-place pins rewrite, WriteAtomic onto tried.bloom, CreateNew twice) each refused; (8) the built binary links no net/http, net/url, crypto/tls symbol. |
| `NegativeControl_DirectedImportIsOutsideAndSeen` | `qompack eval import --from <transcripts> --to <dir outside project and HOME>`, then the same with `--to <project>/sessions` | see below |
| `NegativeControl_SeveredWritersStayConfined` | `QOMPACK_FAULT=daemon-down,disk-full` + one `observe tool` | hook exits 0 with a valid hookio.Output; the delta is confined and Layout-allowed (`logs/LOUD.log`, day log, `run/spawn.lock`); nothing under `index/` (the event was never durably accepted, so it is not claimed); fixtures untouched. |

## Negative control and how it was proven

**Real switch, real write.** `qompack eval import --to` is the one product command whose
destination is user-directed and outside `.qompack/` by design. The arm runs it against a fourth
snapshot root, then runs the SAME confinement check (`x16v5AssertConfined`, which takes a
reporter interface) against a recorder instead of `*testing.T`, and requires:

- the import exited 0 and wrote at least one real session file under `--to`;
- the recorder holds exactly one `CREATED outside .qompack/` line per file written, each naming
  the destination, and nothing else — so the check both sees a stray write and does not
  misreport the project, HOME or the binary's directory.

The refusal half: the fixture project's `go.mod` carries `module github.com/qompack/qompack`, so
`--to <project>/sessions` is refused by `eval.insideRepository` — non-zero exit, "refusing to
import" on stdout, the destination never created, confinement still holding — on a temp tree,
never the real checkout.

The second control severs the transport and the spool at once (`daemon-down` installs a no-op
spawn; `disk-full` fails the first spool append) and asserts the honest failure path: exit 0, no
index claim, no stray write.

No production source was edited to turn the test red; the recorder-based control is the proof of
non-vacuity. During authoring the test WAS red three times for real reasons, each recorded in the
commit body (recall offline in the rig; `AssertAppendOnly` on a project that had sealed
`0001.json`; recall without `--k`).

## Old-to-new assertion map (historical §4.16 text → this row)

| Historical expectation | Status | Note |
|---|---|---|
| Run the full 4.1 + 4.4 + 4.8 + 4.12 sequence under a filesystem-write recorder | **corrected** | No write recorder exists; the before/after content-hash snapshot (v3_x09's) is the recorder. 4.1 = `status` (both arms); 4.4 = PreCompact + SessionStart(compact) (composed arm); 4.8's finalize runs inside the sealed checkpoint; 4.12's warm start does not exist (below). |
| Every write path is under `<root>/.qompack/` or `~/.qompack/` | **kept, strengthened** | Plus a third root (binary directory) and a per-path Layout allowlist. |
| `~/.qompack/` (calibration only) | **kept** | HOME allowlist is exactly `calibration.json`. Observed: zero HOME writes in every arm — nothing calls `Calibrate` in a session without provider-reported usage. |
| No write to `plugin/` | **corrected** | The plugin bundle is not on disk in a test; `pluginmanifest.Write` is called only by `tools/devtool`. The binary's own directory (every child's cwd) stands in for the install directory and must not change. |
| No write to `testdata/`, the repo | **kept in spirit** | The fixture tree is the "repo": byte-identical fixtures, and the `eval import` refusal proves the one user-directed writer refuses a tree carrying the qompack module line. |
| No absolute path outside the project | **partially retired** | Not assertable without a system-wide write tracer. POSIX socket locations 3–5 of §13 invariant 7 are legitimate writes outside both roots on Linux; on Windows the endpoint is a named pipe. Snapshot roots cover locations 1–2 plus the cwd. |
| `sketches/segments/` | **retired (name)** | Shipped as `sketches/seg-NNNN.bloom` (`store.SegmentFilterRef`) behind the default-off `segmentBloom` switch; not flipped, recorded as off. |
| `state/warmstart.json` | **retired (name)** | Never existed. The scheduler persists `state/bocd.json` and `state/scheduler.json`; the shipped arm requires both. `warmPrior` is a default-off refused switch; recorded as off, not passed. |
| `state/promotions.json` | **unverified** | Written by `internal/mcp/promote.go` on an MCP `expand`; no arm drives an expansion (`demandPromotion` is default-off). Path is Layout-allowed if it appears. |
| `testdata`-free command output | **kept** | `status`/`recall`/`dropped` produce stdout only; the deltas contain no command output file. |
| `.gitignore` self-ignoring still holds | **kept** | `.qompack/.gitignore == "*\n"` after every arm; the project's own `.gitignore` byte-identical. |

## Observed write sets (run 3; `objects/**` elided)

Composed arm (53 files): checkpoints/0001.json, checkpoints/MANIFEST.jsonl, dag/deps.jsonl,
index/{files.json,files.jsonl,roots.jsonl,segments.jsonl,sessions.jsonl,tool_use.jsonl},
logs/qompack-20260909.log, metrics/latency.json, pins/{invariants.json,invariants.jsonl},
records/eliminations.jsonl, run/{daemon.hb,daemon.lock,marker.json,state.bin},
sketches/{explore.hll,touch.cms,tried.bloom}, state/{chunktokens.bin, delivery-ack-position.json,
delivery-acks.jsonl, delivery-lease-position.json, delivery-leases.jsonl, draft-<sess>.json,
drain.json, gc-live.bin, history.json, observations.json, observer.json, precompact.json,
rehydrate-<sess>.json, session-recovery.json, store.json}. HOME: none.

Shipped arm (38 files): as above minus checkpoints/pins/records/tried.bloom/draft/rehydrate/
precompact and the daemon lock (released on shutdown), plus spool/client-<pid>.ndjson,
state/bocd.json, state/scheduler.json. HOME: none.

Severed arm (3 files): logs/LOUD.log, logs/qompack-20260909.log, run/spawn.lock. HOME: none.

## Unverified remainder

- `state/promotions.json` and `sketches/seg-NNNN.bloom`: their writers are behind default-off
  switches or an undriven MCP `expand`; not asserted, recorded as off/undriven.
- Calibration writes to `~/.qompack/calibration.json` never occurred (no provider usage report in
  a session); the HOME allowlist is exercised only in the negative direction.
- POSIX socket locations 3–5 of invariant 7: not observable on this Windows host.
- Retention policy (quota/expiry/GC deletion) is not this row's assertion; x9's flush-time GC row
  remains the evidence.
- **Defect observed (SP-14, not fixed here):** `qompack recall <query>` without `--k` sends
  `k=0` (`internal/commands/cmd_recall.go` leaves `RecallArgs.K` unset) and the daemon's schema
  (`minimum:1`) refuses it: `invalid arguments for recall: /k: below minimum`, exit 1. The
  shipped arm passes `--k 5` and records this rather than working around it silently.
- Observation: the shipped arm leaves `spool/client-<pid>.ndjson` behind after flush + shutdown
  (Layout-allowed; noted for the SP-20 drain owner, not asserted).

## Run command and result

```
cd <worktree> && go test ./test/e2e -run '^TestV5_NoPackageWritesOutsideDotQompack$' -count=1 -v -timeout=10m
```

`go test ./test/e2e -list 'TestV5_NoPackageWritesOutsideDotQompack'` prints the one name.
Result: PASS twice consecutively (8.68 s, 9.25 s), all four subtests. `gofmt -l ./test ./internal`
empty; `go vet ./test/e2e ./test/integration` clean;
`go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns` PASS.
