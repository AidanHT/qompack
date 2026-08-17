# SP-05 Final Whole-Branch Review — feat/sp05-daemon-ipc-and-hot-path

Reviewer: final branch reviewer (read-only). Branch: develop..b6181b7a (7 commits), worktree
C:/Users/Quant/Documents/Programming/Projects/qompack-sp05. Authority order applied: ledger
(progress.md, rulings #1–#30) > plan text.

**Verdict: Needs fixes (0 Critical, 2 Important).** Both Importants are small, mechanical fixes;
one fix wave closes everything below. The branch is otherwise coherent, honest, and well past its
exit criteria. Two pre-merge CI conditions from the ledger remain outstanding (they are not code
defects and are listed at the end).

Verification actually run (Windows dev host): `go build ./...`; `go test ./...` (all green);
`go test -race -count=1` on internal/ipc, internal/daemon, internal/contract, internal/cli +
ipctest + contracttest (green, ipc 29.2s); `go vet ./...` clean; `GOOS=linux` and `GOOS=darwin`
`go build ./...` clean; `go run ./tools/devtool fmt lint vet` (gofumpt, golangci-lint, nomagic,
importrules, sleepcheck, vet) exit 0; per-package coverage; the constraint greps of section B.

---

## A. Cross-task coherence

**Hook client ↔ daemon route contracts — coherent.** The six cli hook bodies (cli/hooks.go,
hookclient.go) use exactly the shipped op spellings; `ipc.KnownOps` (13 ops) and
`daemon.defaultRoutes` (daemon.go:225-241) cover the identical set, so no op can dispatch to the
unknown-op refusal by construction, and `TestServicesAllNil` pins it. The deadline table is
consistent end to end: observe.tool/stop fire-and-forget on `State.AckDeadlineMs` (floor 8ms);
observe.prompt Reply at cli's 250ms mirror of the daemon's `promptReplyDeadline`; session-start/
checkpoint/flush Reply at 10/15/15s with the `hookConnectDeadlineFloor` (250ms) widening applied
to exactly the three non-hot-path reply ops (`hookConnectDeadline`, hookclient.go:156-162 — the
fix-round-2 predicate correctly excludes observe.prompt). One margin note in section E (E-5).

**ACK/NAK under HotSpool — coherent.** dispatchOp (handlers.go:143-148) forces `OK:false` only
for non-Reply hot-path requests once `registry.HotMode()==HotSpool`; the request is already WAL'd
by the route's own `ingest.Accept`, the client's NAK path flips its in-process submode and spools
the duplicate, and `seenSet` (shared between ingest workers and the drainer, keyed on the exact
WAL/spool line bytes hashed under `qompack.wal.v1`) collapses it to one dispatch. Verified the key
is computed pre-blob-resolution on both paths (ingest.dispatch and drainer.drainFile), so the
resolved/unresolved forms cannot double-dispatch.

**blobRef round trip — coherent, one hardening minor.** client.externalize (client.go:393-422)
writes `blob-<pid>-<n>.bin` under the spool dir and preserves a pre-existing `Raw` under the
descriptor's `x` key; daemon/blob.go `resolveBlob` is the single shared restorer for both the live
ingest path and the drain path, restores `Raw = ref.X`, and deletes the blob. The descriptor shape
is respelled in daemon (documented, unexported in ipc) — acceptable, both sides pin
`"e.tool_response"`. See FR-3 for the unvalidated `ref.Blob` join.

**Contract history flow — coherent.** All history access is serialized by the daemon's single
`historyMu` exactly as ruling #25 / the Task-5 brief mandated: handleSessionStart and
handleCheckpoint use a three-phase pattern (locked observe+save → unlocked seam call → re-locked
re-load+merge+save) that prevents both the fix-round I-5 deadlock and lost updates; the
observe.prompt sentinel scan (scanSentinelForPrompt) takes the same mutex from the worker pool,
off the reply path. The binding Task-4 constraint holds: `LastSessionID` is written nowhere in
internal/daemon (grep clean) — only `checkSessionStartFires` assigns it. `SessionHistory`'s wire
shape is frozen by the golden fixture and the daemon adds no fields. self-test only ever
LoadHistory's (never saves) and uses a persistence-disabled throwaway monitor — no cross-process
mutation hazard against a live daemon (WriteAtomic keeps reads consistent).

**state.bin lifecycle — coherent.** Writers: Run (post-listen, pre-drain), applyHotPathTransition
(both directions, flip gated on SpoolOnBreach but logging unconditional), handleSessionStart
phase 1 (after RunAll — so a mode transition and a new-session HotSync reset both reach disk),
reloadConfig on any applied change (the fix-round I-4 rewrite is present, reload.go:100-107).
Remover: Stop (before server.Close — correct order; no window where a removed record coexists
with a live listener the other way round). Readers: every hook's single 32-byte read;
`NewClientWithOptions` trusts a caller-supplied non-zero State and never double-reads (I-1 fix
verified). The CRC + magic guard makes torn/corrupt records fall back to `StateFromConfig
(config.Defaults())`, and the `runtime.mode` vocabulary mapping (auto/passive → contract modes)
is implemented once in state.go with clear reasoning.

**Breach detector → SetHotMode → NAK hint → client submode — end-to-end sound.** Samples are
TS-validated (validHotPathTS rejects absent/zero/stale/future TS and counts
`hotpath_sample_invalid`), observed+`hotPathTailAllowance` feeds both the `hook_controlled`
histogram (the ruling-#29 gate source) and the breach worker via a non-blocking channel; window
closure runs off the ACK path; `CheckBudgets` refreshes once per closed window (I-3 fix). ToSpool
logs WARN+Loud+counter and (gated) flips registry + rewrites state.bin. Reversion is delivered as
designed by `registry.Ensure`'s new-session reset to HotSync plus `breach.Reset()` keyed on
pre-Ensure existence (handlers.go:426-430), then persisted by phase 1's WriteState. In-session
ToSync is effectively unreachable once clients stop connecting — that is the documented
"reverts in a subsequent session" semantics, not a bug.

**Spawn / lock / Probe liveness triangle — sound.** Ruling #22 is implemented consistently:
`ipc.Probe` (dial+close, no bytes) is the decisive positive in both lock staleness step 2 (against
the lock file's recorded address) and EnsureRunning; negatives fall through to POSIX kill(0) and
the heartbeat/Started-anchored mtime check (the CreateNew→touchFile race is closed by the Started
fallback, and `owned()` prevents a reclaimed-from daemon from heartbeating or releasing a lock it
lost — the I-2 two-daemons race fix is present). `handleAdminPing` pins OK:true unconditionally as
ruling #22 requires. The ledgered EnsureRunning readiness-contract weakness (Probe consumes the
accept slot it uses as proof) is **properly deferred, not silently load-bearing**: the cli-layer
mitigation (`hookConnectDeadlineFloor` on the three non-hot-path reply ops, plus self-test's
admin.ping using the same floor) covers every path that dials immediately after EnsureRunning;
hot-path ops never call EnsureRunning at all. The client-side spawn.lock is stale-self-clearing
and additionally cleaned by the daemon after listen (task-2 M-8 is thereby RESOLVED).

**Stop/shutdown ordering — one gap (FR-4).** Stop's sequence (close stopped → runCancel →
bounded Drain → ing.Wait/Close → sketches → metrics Persist → RemoveState → server.Close →
lock.Release) is correct and idempotent; runCancel triggers server Close early via
context.AfterFunc, and the second explicit Close is a safe replay. The ctx.Done arm's I-7 fix
(Stop with context.Background()) is present. The remaining gap: the **genuine Serve-failure arm**
(daemon.go:399-409) returns the error without ever calling Stop — lock left held (reclaimable
only after process death), state.bin left stale, WAL handles unflushed, sketches/metrics
unpersisted. Cheap to close; see the fix wave.

## B. Global constraints (verified over the whole branch)

| Constraint | Result |
|---|---|
| Hook subcommands exit 0 on every path | PASS — Dispatch/runGuarded framework + `TestHooksExitZeroUnderFaults` (6×11=66 green), `TestE2E_AllSixHooksExitZero` restored under its exact name, `TestSelfTestIsTheOnlyNonZeroExit` present |
| No `time.Sleep` outside test/bench | PASS — grep finds comments only; devtool sleepcheck clean |
| `net` only in internal/ipc, `"unix"` only | PASS — importers: client/dial_other/dial_windows/listen_unix/listen_windows/server.go; only `Dial("unix")`/`Listen("unix")` |
| `os/exec` only in daemon/cli/tools | PASS — daemon/spawn.go, cli/fault_lockdir_windows.go, tools; internal/testutil hits are pre-existing SP-01 files untouched by this branch |
| Nothing imports cli/daemon | PASS — cli imported by cmd/qompack, testutil (SP-01 composition root, pre-existing), test/guards; daemon imported by cli/test roots only; devtool importrules clean; shipped binary links neither testutil nor testing (`go list -deps ./cmd/qompack` clean) |
| Qompack.md untouched | PASS — `git diff develop..HEAD --stat` has no Qompack.md |
| Underscore counter idiom | PASS — l0_*, ipc_*, contract_fail_*, hotpath_*, idle_task_*; the dotted `contract.degrade`/`contract.mode` names in monitor.record are pre-existing SP-01 code, out of SP-05 scope |
| Frozen fixtures additive-only | PASS — contract MANIFEST rows appended, ipc MANIFEST new, testutil frozen count 23→28 with per-task provenance comment; no existing row edited |
| QOMPACK_FAULT literal placement | PASS — non-test literal in exactly cli/fault.go + daemon/spawn.go; noinject twin never spells it; spawn env-strip is case-insensitive (Windows-correct). One doc contradiction: FR-6 |
| No attribution trailers | PASS — `git log --format=%B develop..HEAD | grep -iE 'co-authored|generated with|signed-off'` empty |
| No new go.mod deps | PASS — go.mod/go.sum byte-identical to develop (go-winio was already SP-01's) |
| No out-of-scope package touched | PASS — empty diff for observer/store/sketch/chunk/canon/symbols/dag/scheduler/checkpoint/rehydrate/mcp/negknow/eval/commands/obs/hookio/config/paths/core/logging |
| Placeholder scan | PASS — only hit is a bench payload's literal `"pattern": "TODO"` grep-payload fixture, not a placeholder |

## C. Exit criteria

| Criterion | Result |
|---|---|
| Exactly 7 commits, mandated subjects | **PASS** — `git rev-list --count` = 7; subjects byte-match the plan as amended by ledger #18 (commit 4/5 swap) and #20 (64-char hook trims: "the hot-path"→"hot-path", "detached spawn seam"→"spawn seam", "latency budgets" dropped, "assertion set"→"assertions", "daemon start" dropped) — all controller-sanctioned |
| ≥75% statement coverage (4 packages) | **PASS** — ipc **84.2%**, daemon **82.0%**, contract **83.9%**, cli **81.5%** (`go test -count=1 -cover`, package-level; the SP-05-delivered files dominate all four packages, so package-level is a faithful proxy) |
| Zero Rule W-1 conformance skips | **PASS** — ipctest: 0 SKIP; contracttest: the 4 SKIPs are the deliberate `*_StubIsSkipped` / shape-against-stub meta-tests that PROVE the skip mechanism; every `*_AgainstQompack*` real-implementation suite runs skip-free |
| Full `go test ./...` green + `-race` on 4 core pkgs | **PASS** — all packages ok; race run green (-count=1) incl. ipctest/contracttest |
| GOOS=linux + darwin builds clean | **PASS** — both `go build ./...` clean; `go vet` clean; devtool fmt/lint/vet (nomagic, importrules, sleepcheck) exit 0 |
| No trailers / commit hygiene | **PASS** — see section B; every commit carries a Refs footer |
| Bench gate B-A/B-B/B-E on ubuntu/macos/windows CI | **NOT VERIFIABLE LOCALLY — pre-merge condition** (ledger: local product-side numbers healthy — daemon p99 ≤2.048ms, B-B 0.64ms, B-E 136.7ms; the wall-clock estimator's sandbox spawn jitter is what ruling #29 re-sourced the gate around; CI adjudicates) |

## D. Deferred-minors adjudication

FIX-NOW = goes into the single fix wave. ACCEPT = stays deferred with justification.

| Item | Ruling | Justification |
|---|---|---|
| Task-1: WriteState permission-retry not GOOS-gated | ACCEPT | os.IsPermission on a POSIX atomic rename is effectively unreachable; 64 Gosched retries are harmless there |
| Task-1: LineReader doc memory-bound overclaim | ACCEPT | doc nit, no behavior |
| Task-1: decodeState mode/hot bytes unrange-checked (fails open to MayAct) | ACCEPT | unreachable from any shipped writer (daemon writes monitor.Mode() ∈ {0,1,2}) and CRC-guarded against corruption; defense-in-depth only |
| Task-1: Op.Valid re-sorts per call | RESOLVED | precomputed knownOpSet landed (op.go:19-53) |
| Task-2 M-1: ACL cross-principal test gate decorative | ACCEPT | needs a CI second-account job (infrastructure, not code); TestWindowsSDDL_Shape carries the substance — keep the follow-up ticket |
| Task-2 M-2: BenchmarkServerRoundTrip asserts no budget | ACCEPT | the budget is now genuinely gated in test/bench/hotpath (B-B via daemon histogram) |
| Task-2 M-3: property-test bounds looser than spec | ACCEPT | test-quality nit; the property itself (Send never errors) is intact |
| Task-2 M-5: NAK overloaded five ways, client latches HotSpool on all | ACCEPT | hook clients are one-shot processes, so the latch scope is a single request in practice; the daemon-side submode is set only by the breach detector |
| Task-2 M-8: spawn.lock never cleaned up | RESOLVED | daemon.Run deletes it after listen (spawn.go removeSpawnLockFile, daemon.go:362) |
| Task-2 M-12: op.go ride-along scope note | ACCEPT | recorded; behavior-preserving improvement |
| Task-2 M-13: threshold zero-fallback vs literal min() | ACCEPT | documented, only observable for a hand-built zero State |
| paths.IsProtected omits the spool tier | ACCEPT | pre-existing SP-01 scope gap (appendonly.go:23-39 protects sketches/checkpoints/pins only); belongs to SP-06/SP-17; carry forward |
| connIdleTimeout (10min) unconfigurable | ACCEPT | right backstop at this layer; revisit when SP-13 lands MCP-over-daemon |
| go-winio v0.6.2 Close-vs-Accept 20s+ hang | ACCEPT | bounded by closeListenerBounded; Windows-only, upstream; keep the ledgered follow-up ticket (go-winio bump or cancellable-Accept redesign) |
| Task-3: ipctest suiteWait guards (unnamed transient) | ACCEPT | eyeballed: suiteWait=10s guards are t.Fatal liveness deadlines, not assertions — a load-induced miss cannot mask a correctness failure; the N-2a race they once flagged is fixed and 10/10-clean |
| Task-4 NEW-5: test comment credits wrong variant | ACCEPT | fold into any later touch |
| Task-4 M7-behavioral: LOUD restore line during a force | ACCEPT | documented wrinkle; force semantics are in-memory by design (ruling #24) |
| Task-5: hotPathSampleMaxAge discards (not clamps) extreme samples | ACCEPT | a >10s "live" sample can only be a broken TS, not a latency measurement; documented at the constant (handlers.go:199-204) |
| Task-5: ctx.Done Stop-error vs idle-exit ignore asymmetry | ACCEPT | cosmetic; both paths run the same Stop |
| Task-5 M-8: handleMCP never calls the seam | ACCEPT | SP-13 replaces the route wholesale |
| Task-5 M-9 Loud-line half + M-11 Touch-no-op | ACCEPT | test nit on a process-wide ring; M-11 is an architectural "consider" |
| Task-6 N-6: fault_noinject.go "exactly one non-test file" claim | **FIX-NOW** | 2-line doc fix; it flatly contradicts fault.go's own (correct) two-file contract and would misdirect whoever maintains the security grep |
| Task-6: EnsureRunning readiness-contract weakness | ACCEPT | properly deferred; mitigated at cli by hookConnectDeadlineFloor (verified not silently load-bearing — section A); ipc/daemon-layer fix candidate for a later subplan |
| Task-6: drain cadence (spooled line can sit on a continuously-active project) | ACCEPT | real but bounded (flush/idle/startup all drain; spool capped at 64MiB); note for SP-08/SP-12 idle-work design |
| Task-7 R2-2: unconditional "hook-spawn-dominated" note sentence | **FIX-NOW** | one fmt change: print the actual tranche/iterations proportion (or make the dominance claim conditional on it) — honesty-of-measurement is this plan's own doctrine |
| Task-7 R2-3: small-n percentile-collapse warning | ACCEPT | optional enhancement; CI/nightly always run n=2000/5000 |
| p999>max snapshot observation | ACCEPT | pre-existing obs behavior, deliberate |

## E. Fresh findings (missed by task-scoped reviews)

**FR-1 (Important) — data race on SessionState.Live in `sessionIsLive`.**
internal/daemon/daemon.go:446-449: `registry.Get` returns the live `*SessionState` under RLock,
but `s.Live` is then read after the lock is dropped, racing `Ensure`/`End`/`Touch` writes made
under the registry mutex. The drainer's IsLive callback runs on idle-tick/admin.drain/flush
goroutines concurrently with routes ending sessions — a real data race that -race has not flagged
only because no test overlaps Drain with a concurrent End. Fix: add
`SessionRegistry.IsLive(id core.SessionID) bool` that reads under RLock (and prefer it over
exposing the mutable pointer), then use it in daemon.go. (`handleSessionStart`'s
`_, existedBefore := Get(...)` use is safe — it reads only the ok bool.)

**FR-2 (Important) — handleObservePrompt swallows WAL-append failures silently.**
internal/daemon/handlers.go:351-353: `if line, err := ipc.EncodeRequest(req); err == nil { _ =
d.ing.Accept(req, line) }` discards both errors with no log, no counter, and an OK reply. On the
same failure (e.g. WAL open/write error, disk full), observe.tool/stop return OK:false → NAK →
the client spools, so the event stays durable; a prompt event is the one hot-path event that can
vanish with zero observability — against §12.1's "nothing fails silently" and the WAL-is-the-
durability-boundary doctrine. Fix: on either error, `log.Warn` + increment a counter (e.g.
`l0_accept_error`); keep the reply's Output flow unchanged.

**FR-3 (Minor, fix-now) — blobRef.Blob is joined under spool/ unvalidated.**
internal/daemon/blob.go:56: a hostile or corrupt spool/WAL line can carry
`{"blob":"..\\..\\<anything>","field":"e.tool_response"}`; resolveBlob will read that file into
ToolResponse and, on success, delete it. Same-user trust boundary (spool and socket are 0700/
SID-ACL'd), so not exploitable cross-user — but the shipped client only ever writes
`blob-<pid>-<n>.bin`, so rejecting any name where `filepath.Base(ref.Blob) != ref.Blob` (or
without the `blob-` prefix) is a two-line hardening with zero legitimate loss.

**FR-4 (Minor, fix-now) — Run's genuine Serve-failure arm skips Stop.**
internal/daemon/daemon.go:399-409: on an unexpected transport failure Run returns the error
directly — daemon.lock stays held until process death, state.bin keeps advertising a dead daemon,
ingest WAL handles are never flushed/closed, sketches/metrics never persist. The ctx.Done arm got
exactly this fix (I-7); this arm should call `_ = d.Stop(context.Background())` before returning
the error.

**FR-5 (Minor, accepted) — observe.prompt has zero transport margin.** cli's reply deadline and
the daemon's seam budget are the same 250ms constant, so a seam that uses its full budget loses
the reply race and the client returns empty output (freshness-only loss; seam is nil in wave 1).
Both constants are plan-pinned (§2.4's 250ms), so no change now — note for SP-08: budget the
ObservePrompt seam strictly inside the wire deadline (e.g. 250ms − ~50ms).

**FR-6 (Minor, fix-now) — runtime.daemon.enabled=false does not prevent the session-start spawn.**
cli/sessionstart.go:34-42 + hookclient.go:253-255: `preSend` (daemon.EnsureRunning) runs before
and independently of the `st.DaemonEnabled` check that Send applies, so an operator who disables
the daemon still gets a resident process spawned at every session start (it then serves nothing —
all clients spool — and idle-exits after 30min; meanwhile its idle drain quietly processes the
spool, which "disabled" operators likely do not expect). The plan gives `enabled` no prose
semantics, but spawning a process the operator turned off is the wrong default reading. Fix: pass
`st` (or just `st.DaemonEnabled`) into `spec.preSend` and skip EnsureRunning when disabled. If the
drain-while-disabled behavior is actually intended, document that instead — but decide, don't
leave it emergent.

**FR-7 (Minor, accepted) — admin.shutdown reply can lose to its own shutdown.**
handlers.go:805-808 comments "replies first, then stops asynchronously", but nothing sequences
handleConn's reply write ahead of Stop→server.Close's conn sweep; on an adverse schedule the
caller sees a reset instead of OK. Callers (cli/e2e) already tolerate a failed reply, and a
correct fix needs reply-write/close sequencing in the server — not worth the surgery now; fix the
overclaiming comment when the file is next touched.

Also checked and clean: fault-injection cannot leak into production spawns (case-insensitive env
strip, noinject twin compiles the seam out, spawned daemons never inherit QOMPACK_FAULT); SDDL/
socket permissions per §2.4 (0700 dir/0600 socket, per-user SID on Windows); no assertion-free
tests found among the mandated suites (the one "cannot fail" case, task-2 M-1, is adjudicated
above); bench harness matches rulings #29/#30 exactly (gate reads the daemon's TS-anchored
hook_controlled p99; wall-clock survives as the ungated B-A_spawn_estimate; warm-up =
64 hot tranche + admin.ping bulk; delivery-integrity check requires exact l0_ingest count);
`hookEventKnownFields` is reflection-derived so it cannot drift from hookio; percentile math is
nearest-rank on both sides.

---

## FIX-WAVE LIST (one dispatch)

1. **[Important, FR-1]** internal/daemon/registry.go + daemon.go:446 — add `SessionRegistry.IsLive(id) bool` (read `Live` under RLock) and use it in `sessionIsLive`; stop reading the shared `*SessionState` outside the registry mutex.
2. **[Important, FR-2]** internal/daemon/handlers.go:351-353 — handleObservePrompt: on `EncodeRequest`/`ing.Accept` error, `log.Warn` + increment an `l0_accept_error`-style counter instead of silently discarding; add a test that a failed prompt WAL append is observable.
3. **[Minor, FR-3]** internal/daemon/blob.go:56 — refuse a `blobRef.Blob` whose `filepath.Base` differs from itself (or lacking the `blob-` prefix) before joining under spool/; blob file is read-and-deleted on an attacker-controlled path otherwise.
4. **[Minor, FR-4]** internal/daemon/daemon.go:399-409 — genuine Serve-failure arm: call `_ = d.Stop(context.Background())` before `return err` so lock/state.bin/WAL handles/sketches/metrics are released like every other exit path.
5. **[Minor, FR-6]** internal/cli/hookclient.go + sessionstart.go — gate `spec.preSend` (EnsureRunning) on `st.DaemonEnabled` so `runtime.daemon.enabled=false` stops session-start from spawning a daemon (or explicitly document drain-while-disabled as intended).
6. **[Minor, N-6]** internal/cli/fault_noinject.go:10-12 — correct the "exactly one non-test file" claim to fault.go's actual two-file contract (fault.go + internal/daemon/spawn.go).
7. **[Minor, R2-2]** test/bench/hotpath/measure.go buildNotes — replace the unconditional "stays hook-spawn-dominated" sentence with the computed warmHotTranche/(iterations+warmHotTranche) proportion, or make the claim conditional on it.

## Pre-merge conditions (ledger, not code — restated for the finisher)

- Observe ubuntu + macos CI legs green before merge: internal/ipc/listen_unix.go and
  server_unix_test.go have never executed on any machine (Task-2 merge condition).
- bench-gate must run green on all three platforms; only AFTER the first green run, mark it a
  required check on develop/main in GitHub branch protection (Task-7 escalation #1). Leave
  replay-gate's continue-on-error alone (SP-02-owned).

**Verdict: Needs fixes (0 Critical, 2 Important).** With the seven-line fix wave applied and the
two CI conditions observed, this branch is mergeable.

---

# Re-review (fix wave)

Scoped re-review of review-fixwave.diff (old HEAD b6181b7a → new HEAD 93f71262). Verified on the
new HEAD: exactly 7 commits, C1–C4 SHAs unchanged (4dc422b5/4e91fcac/d0e9f006/98c4fef7), replayed
C5''=3bd35994, C6''=d040fc07, C7''=93f71262 with subjects byte-identical to the pre-wave branch;
`git log --format=%B develop..HEAD` carries zero attribution trailers. `go build ./...` clean;
`go test -race -count=1` green on internal/daemon, internal/cli, test/bench/hotpath;
`go test -count=1` green on test/e2e (66-fault matrix included), internal/ipc(+ipctest),
internal/contract(+contracttest); `devtool fmt lint vet` exit 0.

## Per-item status

| Item | Status | Notes |
|---|---|---|
| FR-1 (Important) — SessionState.Live race | **FIXED** — `SessionRegistry.IsLive` reads under RLock (registry.go:556-561), `sessionIsLive` rewired to it; `TestSessionRegistry_IsLive` pins semantics and `TestSessionRegistry_IsLiveConcurrentWithEndIsRaceFree` reproduces the exact diagnosed interleaving — and passed under `-race`, which is the load-bearing proof |
| FR-2 (Important) — observe.prompt silent WAL-failure swallow | **FIXED** — `EncodeRequest`/`ing.Accept` errors now each Warn + increment new `counterL0AcceptError` ("l0_accept_error", underscore idiom, documented); ACK/Output flow deliberately unchanged, exactly as prescribed. `TestObservePromptWALFailureIsObservable` forces a deterministic MkdirAll failure (file where .qompack must be a dir) and asserts counter==1 with resp.OK still true |
| FR-3 (Minor) — blobRef.Blob traversal | **FIXED** — refuses any name where `filepath.Base(ref.Blob) != ref.Blob` or lacking the `blob-` prefix (respelled constant, documented), Warn-logged, descriptor left in place. `TestResolveBlob_RefusesPathTraversal` covers parent/nested/absolute/missing-prefix and asserts the target file is neither read into ToolResponse nor deleted. (The added `log != nil` guard is redundant — resolveBlob already Nop-defaults log — harmless) |
| FR-4 (Minor) — Serve-failure arm skips Stop | **FIXED** — `_ = d.Stop(context.Background())` before `return err`, with the Stop error correctly not allowed to shadow the triggering failure; comment updated. Test concern adjudicated below |
| FR-6 (Minor) — enabled=false still spawns | **FIXED** — `preSend` signature now carries the already-read `ipc.State` (no second disk read), `ensureDaemonRunning` gates on `st.DaemonEnabled`; `TestEnsureDaemonRunning_GatedOnDaemonEnabled` pins both arms via the deterministic "spawn failed" Warn side channel. One residual noted below |
| N-6 (Minor) — fault_noinject doc contradiction | **FIXED** — comment now states the correct two-file contract (fault.go + daemon/spawn.go) with the fix provenance |
| R2-2 (Minor) — unconditional dominance claim | **FIXED** — the note now prints the computed warmHotTranche/(iterations+warmHotTranche) percentage for the actual run; `TestBuildNotes_WarmUpProportionIsComputedNotAsserted` pins both n=2000 (~3.1%) and the small-n case the old sentence could not honestly cover, plus the no-warm-up negative case |

## FR-4 test-gap adjudication

**Accepted.** The implementer is right that a deterministic *non-nil* Serve failure (stopped still
open, err != nil) cannot be forced without ipc-layer fault injection, which is out of scope for
this wave. The one-line fix invokes the identical idempotent Stop that the ctx.Done, idle-exit and
admin.shutdown arms already exercise with cleanup assertions, so the shared path is covered; the
untested delta is only "does this arm call it", a risk not worth new transport seams. Optional
future note (not required): a test in package daemon could close `dd.server` directly — Serve then
returns nil with `d.stopped` still open, driving this same arm — and assert lock release +
state.bin removal; it exercises the arm's Stop call, though with a nil err rather than a genuine
transport failure.

## Residuals (no action required)

- FR-6 first-run window: before any daemon has ever written state.bin, ReadState falls back to
  `config.Defaults()` (Enabled=true), so the very first session-start on a config-disabled project
  still spawns one daemon; that daemon writes state.bin with Enabled=false and idle-exits, and
  every later session-start is gated. Unavoidable under the plan's "no config.Load on the hot
  path" rule; the daemon-side write closes it after one cycle.
- Post-wave bench (implementer-reported): B-A 4.096ms / B-B 0.704ms / B-E 195ms — all PASS;
  consistent with the pre-wave product-side numbers. CI three-platform run remains the standing
  pre-merge condition, unchanged.

## Re-review verdict

**Approved.** All seven fix-wave items are genuinely fixed per the suggested fixes, each with a
regression test where one was feasible; no new defect found in the delta; commit count, subjects,
and trailer hygiene intact. The two ledgered pre-merge CI conditions (ubuntu/macos legs green;
bench-gate green on all three platforms before flipping branch protection) still apply.

---

# Re-review (shutdown fix)

Scoped re-review of `git diff 3bd35994..30a04e20` (stopDone completion signal), replayed as
C5'''=30a04e20, C6'''=5288c2e9, C7'''=b57df25e, HEAD=b57df25e.

**(1) Ordering correct for all exit arms.** `stopDone` is closed by a `defer` INSIDE the
`stopOnce.Do` closure — so it fires after every cleanup step (and even if one panics), exactly
once. The only waiter in the tree is the test, and it waits only after Run has returned via the
`stopped` path — which by construction means Stop's Do closure is already executing (`stopped`
closes as that closure's first statement), so the deferred `close(stopDone)` is guaranteed to
fire: no waiter can hang. The early-return arms that never call Stop (ErrLockHeld,
ErrAddrTooLong, lock-acquire/NewServer failures) leave `stopDone` unclosed, and correctly so —
nothing waits on it there. ctx.Done, idle-exit, Serve-failure and admin.shutdown all funnel into
the same Once-guarded Stop; a second concurrent Stop call still returns immediately without
waiting (pre-existing Once semantics, unchanged — and `stopDone` is now precisely the primitive a
caller needing completion can use). Idempotency intact.

**(2) Test now deterministic.** After the existing Run-returned wait, the test waits on
`dd.stopDone` (8s t.Fatal guard) — no sleeps, no test-side deletion; the comment correctly
explains the .qompack/tmp WriteAtomic vs TempDir-cleanup race. Verified locally:
`-race -count=10 -run TestAdminShutdownStopsTheDaemon` clean (implementer additionally reports
-count=50 clean where pre-fix failed within 10–13); full daemon + cli packages green.

**(3) No new defect.** The change is additive (one field, one defer, doc comments); no production
code path blocks on the new channel.

**(4) Hygiene intact.** Exactly 7 commits; C1–C4 SHAs unchanged; subjects byte-identical; zero
attribution trailers.

**Residual (recorded, no action — pre-existing, out of this delta's scope):** the same
Run-returns-before-cleanup-finishes shape exists one level up: `qompack daemon` (cli/daemon.go)
returns when Run returns, so an admin.shutdown-driven process exit can still truncate the async
Stop goroutine mid-cleanup (WriteAtomic keeps files untorn; a skipped lock.Release is recovered by
dead-pid staleness). `stopDone` is unexported and the Daemon interface is frozen, so plumbing a
completion wait into cli needs an interface seam — candidate for SP-17 hardening, alongside the
already-accepted FR-7 reply/close sequencing note.

**Verdict: Approved.**
