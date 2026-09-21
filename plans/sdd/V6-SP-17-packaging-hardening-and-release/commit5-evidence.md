# Commit 5 — `qompack fsck` and `qompack doctor`

Plan row: "Implement compatible fsck/doctor contracts, error/unknown status and explicit repair
behavior; test rollback-safe diagnostics." (plans/V6-SP-17-packaging-hardening-and-release.md §3,
SP17-M7-04.)

Platform: windows/amd64, Go 1.26.6, branch `feat/sp17-packaging-hardening-and-release`.

## 1. What shipped

| file | what it is |
|---|---|
| `internal/store/readonly.go` | `OpenReadOnly` + `ReadOnlyStore`: the read-only store seam both commands use (ruling R5-A, finding R5-6) |
| `internal/store/readonly_test.go` | it creates nothing on a bare directory, and a rejected object stays where it was |
| `internal/store/fsstore.go` :217-223 / `objects.go` :319-328 | the `readOnly` flag and the `quarantine` branch it suppresses |
| `internal/cli/fsck.go` | the integrity command: 17 check rows, the five explicit repairs, the JSON/table renderers |
| `internal/cli/doctor.go` | the diagnostics report: 8 sections, capability/evidence join, control and gap rows |
| `internal/cli/fsck_test.go` | fsck's tests, written against the `--json` DOCUMENT rather than this package's structs |
| `internal/cli/doctor_test.go` | doctor's tests, same discipline |
| `internal/cli/commands.go:35-37, :50-52` | registration: `fsckCmds()`/`doctorCmds()` appended; only `bench` is left in `notImplemented` |
| `internal/cli/dispatch_test.go:205-220` | `TestDispatch_NonHookErrorExitsOne` moved from `fsck` to `bench` |

Entry points: `fsckCmds` fsck.go:126, `runFsck` fsck.go:163, `fsckScanProject` fsck.go:400,
`performFsckRepairs` fsck.go:2021, the repair seam `var runFsckRepairs` fsck.go:156;
`doctorCmds` doctor.go:99, `runDoctor` doctor.go:108, `collectDoctorReport` doctor.go:163.

Nothing under `plugin/`, `docs/commands.md`, `internal/commands` or `plans/00-ARCHITECTURE.md` was
touched. `tools/devtool/fsck.go`'s comment is still true (it degrades only when `cmd/qompack` is
absent, which is not this change) and was left alone.

## 2. The fsck check-class table

Each row is `{id, ok, severity, scanned?, count, detail[]}`; `scanned` is the population the check
walked, so a zero-defect answer over a zero-sized store is distinguishable from a vacuous one.
Exit 0 = no defect; 1 = a defect or an I/O failure that prevented a check (reported as a row);
2 = usage.

| id | what it resolves | repair |
|---|---|---|
| `store` | `.qompack` present at all; an absent one is "no store" and exit 0 | none (never creates one) |
| `daemon` | `ReadLock` + `ipc.Probe` (ruling R5-C): a live holder makes the answer "a snapshot of a moving target"; a lock nothing answers behind is reported STALE and disables no check | none |
| `objects` | every file under `objects/**`: name is a content address, regular file, a BARE object within `store.MaxPutBytes` (the limit `readObjectFile` itself refuses) and any object within this build's read bound, decodes, plaintext re-hashes to the name; both the `.zst` and bare spellings | move to `tmp/quarantine/` through the store's own path |
| `index.roots` | line-by-line: record version, unknown `op`, unparseable hashes, chunk lengths the store coerces to unknown, every chunk of every live root held ON DISK, `deltas`/`base`/`orig` resolve, tombstoned-but-referenced roots reported as accounted-for | none |
| `index.tool_use` | every record's root by finalize.go's root-or-chunks rule, supersession links resolve, a session's turns never go backwards | none |
| `index.files` | `index/files.jsonl` parses; `index/files.json` agrees with it (DERIVED-VIEW defect) | regenerate `index/files.json` |
| `index.segments` | record version and op; every `encode` record's seq names a MANIFEST entry (finalize.go's seq-reference-drift) | none |
| `captures` | sidecar version (newer ⇒ support gap, not damage); a TOOL delivery at stage 1 with outcome ok and durable bytes; a published sidecar whose root no longer resolves | none — sidecars are evidence |
| `checkpoints` | MANIFEST lines parse and their digests parse; `Reader.Verify` mismatches; artifacts with no MANIFEST line (orphans, re-hashed and named); a NEWER schema reported as written by a newer plugin; `Reader.Chain` from the newest verifying checkpoint | append a MANIFEST line for a clean orphan |
| `pins` | `invariants.jsonl` parses, `MintID(text) == id`, tombstones name a recorded add; `invariants.json` agrees with the log | regenerate `pins/invariants.json` |
| `negknow` | `records/eliminations.jsonl` parses (unreadable ⇒ BLIND); record/active/stale counts; `tried.bloom` classified absent / corrupt / truncated / unsupported by `errors.Is` on sketch's sentinels; surviving `tried.bloom.<n>.bak` generation; `*.corrupt.<ms>` listed as preserved evidence | rebuild `tried.bloom` from ACTIVE records |
| `delivery` | both journals within their loader's byte and entry bounds and every line parses; each position seal is a v1 JSON document or the v2 binary image. The full dual-reader check ACQUIRES the daemon lock, so it sits behind `--seal-check` and the default row names the question it did not ask | none — it never edits a journal |
| `spool` | `state/drain.json` against the spool in `DrainGapKind`'s vocabulary: an offset past size, a record claiming bytes of a vanished file, a spool below its durable bound. Unreplayed bytes are an observation, not a defect | none |
| `retention` | every `retention-roots.jsonl` line parses and every hash is held, with the `evidence`-class distinction (an evidence root names a capture sidecar's `bytes_hash` under `records/captures/`, never an object); `state/pending/*.json` parse | none |
| `migrate` | each backup's `manifest.json` version and every file re-hashed against it; the six migrate records' versions. It SAYS which path ran, because `NewMigrator` refuses while the legacy-import build gate is closed | none |
| `fidelity` | `RestoreOriginal`'s status for every live root, counted VERBATIM, through `store.OpenReadOnly`; never promotes canonical or unavailable to exact, and never relocates a damaged object by asking about it | none |
| `quarantine` | count and bytes under `tmp/quarantine/**` | none — evidence, never touched |

## 3. The five explicit repairs (`--repair --yes`)

`--repair` without `--yes` is a usage error with the both-halves shape `admin delivery-seal`
established (its own message, exit 2). `--repair --yes` takes `daemon.AcquireLock` FIRST and refuses
a project a daemon owns with that tool's message shape ("a daemon is running in <root> and owns the
project; stop it first"), writing nothing.

| kind | target | why it is safe |
|---|---|---|
| `files.view` | `index/files.json` | regenerated from `index/files.jsonl`, which is the truth |
| `pins.view` | `pins/invariants.json` | regenerated through `pins.Store.Materialize`, the only writer of that projection |
| `checkpoint.manifest` | `checkpoints/MANIFEST.jsonl` | ONE appended line per orphan artifact that re-hashes cleanly and parses at a known schema version, through `paths.AppendManifest` — the only legal writer. The artifact itself is never rewritten |
| `negknow.bloom` | `sketches/tried.bloom` | rebuilt from ACTIVE elimination records through the ledger's own `RebuildBloom` (generational replace, bumps the seq); it runs only when the filter is broken or absent while active records exist |
| `object.quarantine` | `tmp/quarantine/` | a failing object is moved by the STORE's own quarantine path (`GetChunk` → `getObject`), so the bytes are kept as evidence where every other rejected object lands |

It never deletes, never edits a journal, never rewrites a roots/tool_use/segments line, never touches
a capture sidecar, a checkpoint artifact or a backup, and **never runs GC**: `--gc-all` is not
implemented by this command and no flag here reaches a `GCPolicy`. `internal/store/gcrun.go:193`'s
comment mentioning `qompack fsck --gc-all` was left alone; it names a flag that does not exist yet.

## 4. The doctor row table

Every section is `{id, title, rows:[{id, status, observed, detail, coverage?, gate?}]}` with
`"schema": 1`. Status is a closed four-value vocabulary: `ok`, `degraded`, `unknown`, `disabled`.
doctor ALWAYS exits 0 except for a usage error.

| section | rows |
|---|---|
| `version` | `version.plugin` (core.Version), `version.buildinfo` (main module + GOOS/GOARCH/CGO/trimpath/vcs), `version.bundle` (BUNDLE.json version/target/source commit/dirty when the executable sits at `<bundle>/bin/qompack[.exe]`; `unknown` otherwise), `version.pluginRoot` (the contract assertion's three outcomes: unset / set-and-resolves / set-but-no-binary) |
| `host` | `host.platform`, `host.go`, `host.claudeCLI` ("not probed: … test/canary"), `host.target` (`unknown target` unless the observation ledger carries one) |
| `scope` | `scope.root` (and how it resolved), `scope.established`, `scope.daemon` (no lock / held+reachable / held+unreachable), `scope.mode` (effective value + provenance layer and location), `scope.contractMode` (Mode, SessionCount, CleanRuns), `scope.degradedReason` (+ DegradedSince) when there is one |
| `capabilities` | eight rows from `DefaultCapabilityRegister().Get(c)` in ledger order, two-value form: class, mechanism, evidence status, enabled, target (`unknown target`), fallback, joined to the NEWEST matching observation-ledger entry (outcome, scope, ts, coverage rendered exactly as stored) or "no observation" |
| `controls` | `config.violations` (this run's tolerant load plus the persisted `state/config-violations.json` list); the ten `runtime.migration.*`/`runtime.phase7.*` gated leaves, each with its `{owner, gate, passed}` row and labelled **refused-by-validate** rather than "disables"; `runtime.mode`, both `settingsVersion` leaves, `blockManualCompact`, `runtime.telemetry.enabled` (hardwired off), `runtime.redact.enabled`, `runtime.daemon.enabled`, both `runtime.selection.*` leaves; `store.migrate.legacyImportCutover` (a BUILD gate with no config leaf); `admission` ("disabled: mirrors runtime.migration.replacement.newResult; host allowlist empty"); `runtime.recording` (recording has no switch of its own — `runtime.mode` is it) |
| `recording` | `store.writable` (the probe Task 2's F-2 asked for), `spool.pending`, `drain.progress`, `negknow.ledger` (blind?), `captures.unpublished`, one `assertion.<id>` row per failed assertion in `history.Last` |
| `retrieval` | `store.open` (Stats as OBSERVATIONS, explicitly not health; skipped while a daemon owns the writer), `checkpoint.latest`, `mcp.tools` (count + names, and whether config disables them), `integrity` (points at `qompack fsck`) |
| `status` | `status.primary`, `status.mode`, `status.schema`, from `commands.CollectStatus` over the SAME `commandStatusSources` the `status` command wires |

No dedup ratio, storage ratio or latency figure is presented as a health verdict anywhere; the two
places numbers appear (`store.open`, `spool.pending`) say in their detail that they are observations.

## 5. RED, then GREEN

RED was taken against the branch tip with both test files written and neither command implemented,
so every failure is BEHAVIOURAL rather than a compile error:

```
$ go test -count=1 -run 'TestFsck_|TestDoctor_' ./internal/cli/
--- FAIL: TestFsck_CleanProjectFindsNoDefect (0.26s)
    Messages: stdout= stderr=qompack fsck: not implemented in this build
--- FAIL: TestDoctor_AlwaysExitsZeroAndNeverCreatesAProject (0.00s)
    Messages: stdout= stderr=qompack doctor: not implemented in this build
… 16 top-level tests, 36 cases in all
FAIL  github.com/qompack/qompack/internal/cli  1.243s   (exit 1)
```

GREEN, after `fsck.go`, `doctor.go` and the registration:

```
$ go test -count=1 -run 'TestFsck_|TestDoctor_' ./internal/cli/
ok    github.com/qompack/qompack/internal/cli  1.336s   (exit 0)

$ go test -count=1 ./internal/cli/
ok    github.com/qompack/qompack/internal/cli  9.744s   (exit 0)
```

The whole package is run rather than a `-run` pattern alone, both because the dispatch-table tests
live there and because `go test -run` prints `ok` for a pattern that matches nothing.

Cases covered (all in `internal/cli/fsck_test.go` and `internal/cli/doctor_test.go`):

- a clean project reports no defect AND a non-nil population for objects/roots/tool_use/checkpoints/pins
- an empty directory reports "no store", exits 0 and leaves no `.qompack` behind
- fourteen seeded defect classes, each asserting the RIGHT check row, that its detail names the
  CLASS, that `repairs` is empty and that the damaged file is byte-identical afterwards
- a newer checkpoint schema is reported as "written by a newer plugin", the row stays OK, and the
  word "corrupt" does not appear
- a canonical-only root is counted canonical and `exact` stays zero
- `--repair` without `--yes` ⇒ exit 2, nothing on stdout
- `--repair --yes` with a daemon holding the lock ⇒ exit 1, the seal tool's message shape, and a
  `.qompack` snapshot identical before and after
- `--repair --yes` on a stopped project ⇒ only the five repair kinds; a snapshot diff proves no other
  file changed and none was deleted; the orphan artifact's own bytes are unchanged; a second fsck run
  then reports every live reference resolving
- a read-only run with a live daemon lock reports `daemon_running` and "snapshot of a moving target"
- the repair seam: four rows compare the WHOLE `fsckRepairOptions` struct, prove the repair is not
  reached at all without `--repair`, and pin that `Clock` is the one Dispatch was handed
- `fsckKnownRecordVersion` is pinned against the version internal/store's own writer puts in
  `index/files.json`
- both commands are in `All()` and neither is a Hook
- doctor exits 0 on an empty directory, a seeded project and a damaged project, and creates nothing
- doctor's eight capability rows, each with `unknown target` and `no observation`, coverage never
  `complete`
- every migration gate row carries `passed:false`, and there are exactly `len(config.MigrationGates())`
  of them
- a gated switch set true in `config.json` is shown as a violation and reported as `false`, and
  doctor still exits 0
- the `store.writable` probe row
- doctor and `status` report the same primary provenance and the same snapshot mode, with and
  without a daemon holding the lock

## 6. Manual smoke (`go run ./tools/devtool fsck --json`)

Against a temp project with no `.qompack` at all (exit 0, two rows, nothing created):

```
{"schema":1,"project":"…/fsck-smoke","daemon_running":false,"read_only":true,
 "checks":[{"id":"store","ok":true,"severity":0,"count":0,
            "detail":["no store: … has never been used with Qompack, and fsck does not create one"]},
           {"id":"daemon","ok":true,"severity":0,"count":0,
            "detail":["no daemon holds the lock; this project is quiet"]}],
 "repairs":[],"fidelity":{},"quarantine":{"files":0,"bytes":0},"exit":0}
```

Against a laid-out project with the daemon stopped (all seventeen rows, exit 0) — the full document
is `commit5-fsck-smoke.json`:

```
store        ok  —  store present at …/.qompack
daemon       ok  —  no daemon holds the lock; this project is quiet
objects      ok  0
index.roots  ok  0
index.tool_use ok 0
index.files  ok  0
index.segments ok 0
captures     ok  0
checkpoints  ok  0  no checkpoint has been written yet
pins         ok  0  pins/invariants.jsonl does not exist yet
negknow      ok  0  records/eliminations.jsonl does not exist yet: nothing has been eliminated
delivery     ok  0  the full dual-reader seal check passed
spool        ok  0
retention    ok  0  state/retention-roots.jsonl does not exist yet
migrate      ok  0  the legacy-import gate "store.migrate.legacyImportCutover" is closed (owner SP-20 M1-04) …
fidelity     ok  0  no live root to restore
quarantine   ok  0  0 quarantined file(s), 0 byte(s), preserved as evidence and never swept
exit 0
```

The same run with a daemon holding the lock reported `daemon_running: true`, the "snapshot of a
moving target" line, and named the two questions it did NOT ask (the full dual-reader seal check and
the fidelity pass) rather than asking them unsafely. `qompack doctor`'s table output for the same
project is `commit5-doctor-smoke.txt`.

## 7. Validation

| command | exit |
|---|---|
| `go run ./tools/devtool fmt-check` | 0 |
| `go vet ./internal/cli/ ./internal/store/` | 0 |
| `go test -count=1 ./internal/cli/` | 0 — `ok … 9.03s coverage: 78.2% of statements` (OWNERS floor for `cli` is 75) |
| `go test -count=1 ./internal/store/ -run TestOpenReadOnly -v` | 0 — 3 tests, 2 subtests, all PASS |
| `go test -count=1 ./internal/store/` | 0 — `ok … 148.3s coverage: 89.1% of statements`; the 90 floor was already breached before this change (finding R5-7) |
| `go test -count=1 ./internal/commands/` | 0 |
| `go test -count=1 ./tools/devtool/` | 0 |
| `go run ./tools/devtool lint --only=nomagic,importgraph,testdeps,bindeps,sleepcheck,stubskips` | 0 — `PASS nomagic`, `importgraph: OK (69)`, `testdeps: OK (71)`, `bindeps: OK (6 targets)`, `sleepcheck: OK`, `stubskips: OK` |
| `go run ./tools/devtool plugin-validate` | 0 — `OK (10 file(s), 7 command(s), 7 hook event(s))` |
| `go build ./...` | 0 |
| `go run ./tools/devtool fsck --json` (temp project, twice) | 0 |

`go test -count=1 -timeout=30m ./test/guards/` is run in full because this commit changes
internal/cli's command table. It exits 1 on exactly ONE test, and that failure is PRE-EXISTING at the
branch tip and belongs to Task 6:

```
--- FAIL: TestGuard_FaultEnvIsConfinedToTwoFiles (0.22s)
    Messages: test/security/security.go names QOMPACK_FAULT. The fault switch has exactly one
              reader (internal/cli/fault.go) and one stripper (internal/daemon/spawn.go) …
FAIL  github.com/qompack/qompack/test/guards  108.578s
```

`grep -rln QOMPACK_FAULT --include=*.go internal cmd tools test | grep -v _test.go` names four files:
`internal/cli/fault.go`, `internal/daemon/spawn.go` (both allowed) and `test/platform/platform.go`,
`test/security/security.go` (Tasks 2 and 3's, and Task 6's to settle). Nothing in this commit names
that variable. Every other guard in the package passes.

`stubskips` runs `go test -json ./...` over the whole tree, so it is the one check here that is not
focused; it was started before the last two coverage tests were added and this commit introduces no
`t.Skip` at all, so its answer stands.

`internal/store` was not touched, so its quarantine/getObject tests were not re-run.

## 8. Deviations from the brief, with the measurement behind each

0. **Neither command calls `store.Open` (ruling R5-A).** See §11.
1. **`checkNegativeKnowledge` does not open `negknow.Ledger`.** The brief asks for "`Health`
   fields"; opening the ledger to get them is a WRITE. `negknow.Open` (ledger.go:411, :426) calls
   `paths.EnsureLayout` and then takes an `O_CREATE` append handle on
   `records/eliminations.jsonl` before it reads anything, so a read-only integrity scan that used it
   would create files in the project it is inspecting. The row therefore reports the same facts from
   the files directly: record/active/stale counts from the log, blind mode as "the log exists and
   cannot be read" (negknow's own `reasonBlind`), and the filter's bits/fill/estimated-fp/saturation
   from `sketch.LoadWithLog` into a `*sketch.Bloom`. The ledger's own `RebuildBloom` IS used by
   `--repair`, which holds the lock. `FilterGeneration` comes from `paths.HighestBloomBackupSeq`.
2. **No check is skipped because a daemon holds the lock.** A read-only store takes no handles and
   no lock, so the fidelity pass runs beside a live daemon; the report says the answer is a snapshot
   of a moving target. And "a daemon is running" is now decided by `ReadLock` + `ipc.Probe`
   (ruling R5-C), so a stale lock file disables nothing.
3. **The fidelity pass asks about every live root through a READ-ONLY store.** An earlier version
   drove `RestoreOriginal` over every root through the ordinary `store.Open`, and the
   seeded-corruption test failed because the OBJECT HAD MOVED — `getObject` quarantines what it
   rejects, so the pass was changing the project it was inspecting. `store.OpenReadOnly` (R5-6)
   removes the class outright: it takes no writers and suppresses the quarantine MOVE, so a damaged
   root is reported as `corrupt` rather than relocated, which is also the more informative answer.
4. **`index/files.json`'s repair reproduces a shape whose writer is unexported.**
   `internal/store`'s `materializeFilesJSON` and `indexRecordVersion` are both unexported, so
   `fsckKnownRecordVersion` (fsck.go:78) carries a copy of the number.
   `TestFsck_TheViewVersionMatchesTheStoresOwn` pins that copy against what the store actually writes,
   so a bump cannot pass silently. See finding R5-1.
5. **`--repair` re-reads the project under the lock** rather than carrying the read-only scan's
   findings across. The scan ran before the lock was taken, so a finding from before a lock is a
   finding about a project somebody else may still have been writing. This is why
   `fsckRepairOptions` carries no findings and the seam test can compare the whole struct.
6. **`--gc-all` is not implemented**, as the brief instructs. `internal/store/gcrun.go:193`'s comment
   naming it was left alone; it currently names a flag that does not exist.

## 9. Findings returned, with owning package

- **R5-1 (owner `internal/store`).** `index/files.json` has no exported regenerator. The view is
  derived state that `internal/checkpoint`'s finalize treats as legitimately stale
  (finalize.go:126-130) and that plan §3 names as a legal repair target, but the only writer is
  `(*FSStore).materializeFilesJSON`, unexported, and `Flush` calls it only when `filesDirty` — which
  is false for a freshly opened store, so no exported call can regenerate it. `internal/cli` now
  carries its own copy of the view's shape and version. A one-line exported
  `(*FSStore).MaterializeFilesView() error` would delete that copy. Pinned meanwhile by
  `TestFsck_TheViewVersionMatchesTheStoresOwn`.
- **R5-6 (owner `internal/store`, LANDED here under ruling R5-A).** `store.OpenReadOnly` +
  `ReadOnlyStore` (`internal/store/readonly.go`). It is the sanctioned cross-owner addition the fsck
  contract depends on: no `EnsureLayout`, no `ensureStoreDirs`, no append handles, a segment log
  replayed read-only with no append handle of its own (§14), a `Close` that does not `Flush` and
  degrades that log, a token estimator with no persistence path, and an `FSStore.readOnly`
  flag that suppresses `quarantine`'s MOVE so a rejected object is reported and left where it is.
  §5.8's `Store` interface is unchanged; the new type is a narrow read half so the mutating methods
  are unreachable through it. Tested by `TestOpenReadOnly_CreatesNothing`,
  `TestOpenReadOnly_RejectsAnObjectWithoutMovingIt` and `TestOpenReadOnly_LoadsARealStoreUnchanged`.
- **R5-7 (owner `internal/store`, PRE-EXISTING).** `internal/store` measures **89.1 %** line
  coverage against the 90 floor `plans/OWNERS.tsv` sets for it, so `devtool cover` fails on it. It is
  not this commit's doing: `readonly.go` measures 18/19 statements (94.7 %) and the package WITHOUT
  it measures 89.10 %, so this change moved the figure up by 0.03 pp. Reported rather than fixed —
  raising another owner's package over its floor is not in this task's scope.
- **R5-2 (owner `internal/store`).** There is still no exported way to quarantine one object
  DELIBERATELY. The repair reaches the store's own quarantine path by calling `GetChunk` on an object
  it already knows fails, and relies on `getObject`'s side effect — which `readOnly` now suppresses
  on the read path, so the two behaviours are the same call distinguished by which opener built the
  store. It works and it lands the bytes exactly where §12.3 puts
  every other rejected object, but a repair whose mechanism is another function's side effect is
  fragile: a future `getObject` that stopped quarantining would silently turn this repair into a
  no-op, and no test outside `internal/cli` would notice.
- **R5-3 (owner `internal/negknow`).** Blind mode is not observable through the seam. `blind` is
  unexported, `Health` does not carry it, and the only external signals are the counter
  `negknow.bloom.blind_mode` on a registry the caller supplies and an `AnswerUnavailable` from a
  Query. A `Health.Blind bool` would let `doctor` and `fsck` report the ledger's own verdict instead
  of re-deriving it from a failed read.
- **R5-4 (owner `internal/daemon`).** `daemon.ReadLock` answers "a lock file exists and parses", not
  "a daemon is alive". `pidAlive` is unexported and returns `(false, false)` on Windows
  (lock_windows.go:9), so `doctor` falls back to `ipc.Probe` against the recorded address to tell a
  live holder from a stale one, and `fsck` treats any parseable lock as live. An exported
  `daemon.LockLiveness(root) (LockInfo, Liveness)` would give every diagnostic the staleness
  protocol's own answer rather than a second approximation of it.
- **R5-5 (owner `internal/checkpoint`, carried from Task 4's F4-8).** `toolResultResolvable`
  (finalize.go:230-264) still asks `store.Has`, which never stats a file. fsck deliberately does not
  use it; Task 6 owns the fix.

## 10. What stayed unverified

- **No fsck run against a project a real session produced.** Every fixture here is built through the
  product's own writers from a test, and the manual smoke ran against a laid-out but empty project.
  What is not exercised is a store a real Claude Code session filled — many roots, real delta side
  records, a cadence-sealed checkpoint's own pointer set. Task 4's `test/fault` audit is the closest
  thing that exists and it runs against the bundled binary, not against `fsck`.
- **No fsck run against a LARGE store.** The fidelity pass reads and decodes every chunk of every
  live root with no bound, deliberately (an operator command is not on a latency budget), but nothing
  here says what that costs on a store with a hundred thousand objects.
- **The `--repair` path has never run against a store a daemon was recently writing.** Both repair
  tests use a project this process built and then closed. The refusal path (a held lock) is covered;
  what is not is a repair on a project a daemon left mid-write, which is
  `internal/daemon`'s fixture.
- **One platform.** windows/amd64 only. Nothing here claims anything about the other five targets.
  `doctorExeSuffix` has a `.exe` branch and a bare branch and only one of them was ever taken.
- **The bundle identity row was exercised only through its "not running from a bundle" answers.**
  `Env.Self` is set by `cmd/qompack/main.go` alone, so a test reaches the BUNDLE.json parse only by
  constructing an `Env` by hand, which no row here does — `version.bundle` is covered for the unset
  and the not-under-bin cases and not for a real bundle. Task 1's bundle tests own that shape.
- **`doctor`'s agreement with `status` was asserted with no daemon ANSWERING.** Both rows of
  `TestDoctor_AgreesWithStatusOnModeAndProvenance` run against a project with no listener: the
  "daemon present" half holds the LOCK without serving, which is the state a diagnostic most often
  meets and is not the same as a daemon that answers `ipc.OpStatus`. The agreement is structural —
  both commands call `commands.CollectStatus` over `commandStatusSources` — so a live daemon cannot
  make them disagree, but that has not been run.
- **`store.migrate.legacyImportCutover` is closed in every build**, so `Migrator.VerifyBackup` has
  never been the path that ran. fsck's backup verification is its own re-hash of the same rule, and
  the row says which path ran; the two have not been compared against one another on a real backup.

## 11. Read-only, enforced (rulings R5-A, R5-B, R5-C, R5-D, R5-E)

The first version of these two commands claimed to be read-only and was not. The claim is now a
checked property, in three places.

**No store handle that writes.** `store.OpenReadOnly` (`internal/store/readonly.go`) is the
sanctioned cross-owner addition. `store.Open` runs `paths.EnsureLayout` and `ensureStoreDirs` and
then takes `O_APPEND|O_CREATE` handles on all five index files, so merely ASKING a project a
question manufactured the `.qompack` tree inside it. The read-only opener has none of that: no
layout creation, no append handles, a segment log replayed by `openSegLogReadOnly` because
`openSegLog` creates its file — loaded, so a diagnostic can count what the project holds, and
refusing every write with `ErrReadOnly` (§14) — a `Close` that does not `Flush` and degrades that
log, and a token estimator with no calibration or chunk-cache path. It also carries
`FSStore.readOnly`, which suppresses `quarantine`'s MOVE — verifying an object is a read, relocating
it is not — so the read path reports a rejected object and leaves the bytes where they are.

**No daemon lock on the default run.** `daemon.RepairDeliverySeal` acquires the lock for its whole
run, so the seal check moved behind `--seal-check`; the default row names the question it did not
ask. `--seal-check` and `--repair` are mutually exclusive because both want the lock and the repair
holds it.

**No probe file.** Doctor's writability row opens an EXISTING file for writing and closes it
untouched (`.qompack/.gitignore`, `state/store.json` or `index/roots.jsonl`, whichever is there) and
reports "not probed" when none is — instead of creating and deleting one under `tmp/`, which created
`.qompack/tmp/` on a project that had none.

### The proof

`internal/cli/fsck_test.go`'s `TestFsck_AndDoctor_CreateNothingOnAnyProjectShape` snapshots the WHOLE
`.qompack` tree — directories included, skipping nothing — before and after running each command on
four project shapes, and asserts byte-identical. `snapshotQompack` used to skip `run/`, `tmp/`,
`logs/` and `metrics/`, which is exactly what let twenty-odd created files go unnoticed.

By hand with a freshly built binary (`commit5-readonly-proof.txt`):

| scenario | paths before | paths after | fsck | doctor | tree |
|---|---|---|---|---|---|
| a directory holding only `.qompack/` | 1 | 1 | 0 | 0 | identical |
| a laid-out project (EnsureLayout + self-test) | 30 | 30 | 0 | 0 | identical |
| one `roots.jsonl` line and the one object it names | 7 | 7 | 0 | 0 | identical |

The third is the one that matters most: it has a live root, so the fidelity pass actually runs, and
it reports `fidelity: {"canonical": 1}` over a seven-path tree it did not touch.

### Liveness, and what a stale lock may not do

`daemon.ReadLock` answers "a lock file exists and parses". Taking that for "a daemon is running" let
a lock naming pid 999999 make the fidelity pass and the seal check skip themselves and still report
`ok`, so `fsck` exited 0 on a project it had barely looked at. Both commands now use
`fsckDaemonLiveness` — `ReadLock` plus the staleness protocol's own `ipc.Probe` dial — and a stale
lock is reported as STALE and disables nothing (`TestFsck_AStaleLockDisablesNoCheck`). The live case
is tested against a REAL bound listener (`serveFakeDaemon`), not against a lock file.

### Every I/O failure is a row

`checkMigrateRecords`, `checkDeliveryPositions`, `checkDelivery`, `checkPendingMarkers`,
`fsckCheckpointArtifactsOnDisk`, `checkMigrateAndBackups`, `fsckSketchEvidence` and
`fsckReadPinsView` reported `ok` on an unreadable artifact. They now report a defect naming the path
and the error. `fsckReadDir` is what makes the distinction reliable: on Windows, `os.ReadDir` on a
path that is a regular FILE comes back as `ERROR_PATH_NOT_FOUND`, which Go maps to `fs.ErrNotExist`,
so an `errors.Is(err, fs.ErrNotExist)` guard reported a clean row for a directory that had been
replaced by a file. It `Lstat`s first and only treats a genuinely absent path as a state.
`TestFsck_AnUnreadableArtifactIsAReportedRow` covers seven sites.

## 12. The rest of fix round 1

**`checkpoint_pointer` (commit4-evidence.md §8's missing ABSORB class).**
`checkCheckpointPointers` resolves every file and tool pointer a VERIFYING checkpoint carries into
the store, by finalize.go's root-or-chunks rule and on disk. A pointer the writer already dropped is
not here to be found — `ValidatePointers`' `DropEntry` is the explicit report for that — so what this
catches is the pointer that survived into the artifact and stopped resolving afterwards, which is
the state rehydration meets and nothing else names.
`TestFsck_ReportsEveryDanglingReferenceClass` seeds it by deleting the object under live index
lines, which is also the only seed that tells an on-disk resolver apart from one that asks
`store.Has`.

**The object size window.** A bare object past `store.MaxPutBytes` is now its own defect: that is the
limit `readObjectFile` itself refuses, so the store cannot read it either. The window that used to
drive `getObject` into a quarantine move is moot — the read-only store does not move anything — and
`fsckFailingObjects` applies the same bound before reading a file it is deciding whether to move.

**Repair only what the scan reported (M-1).** An absent `index/files.json` over an empty log, and an
absent `pins/invariants.json` over a log with no live invariant, are not defects — `checkFiles` and
`checkPins` do not report them — so `--repair --yes` no longer writes either.
`TestFsck_RepairOnACleanProjectWritesNothing` pins that a clean project comes back with an empty
`repairs` list and an unchanged tree.

**The derived view's keys (M-3).** `fsckStoreKey` normalizes a path exactly as
`internal/store/fsstore.go`'s `storeKey` does, so the view fsck compares against and the one
`--repair` regenerates are keyed the way the store keys them.

**The table (M-4).** `writeFsckTable` prints a `SCANNED` column; a row with no population prints `-`
rather than `0`, because "it scanned nothing" and "it has nothing to scan" are different answers.

**Doctor's clock (M-7).** `statusRows` collects at `s.clk.Now()` rather than `time.Now()`, so a
fixture's clock is what dates the report.

**Doctor's agreement with status is stated, not assumed (R5-E).** The `status.primary` row says that
doctor never SPAWNS a daemon — it strips `Env.Self` before building the client — so it agrees with
`qompack status` for the disk and probe sources and for a daemon that is already running, and a
`status` run that lazily spawns one can report `source=daemon` where doctor reports `disk`. The test
asserts the row carries that sentence rather than pretending the agreement is unconditional.

**Test hygiene (M-5).** `isRepairablePath` matched `quarantine/` while the relative path is
`tmp/quarantine/...` (a dead branch); it now matches the real prefix. The unknown-op row asserted the
two-letter substring `op`, which matches almost any message; it now asserts the full class sentence.

## 13. Fix round 2 — the read-only store now refuses writes (N-1 .. N-5)

**N-1, the one that mattered.** `readOnlyStore` EMBEDDED `*FSStore`. An embedded pointer promotes
every method, so the value `OpenReadOnly` returned still satisfied `store.Store` and one type
assertion reached the whole mutating half. Measured through that assertion on a seeded project:
`GC(ctx, GCPolicy{})` deleted an unreferenced object file, `PutBytes` created five directories and
two files before panicking on the nil `rootsW`, and `Flush` returned nil. The doc comment claiming
"a caller cannot reach them at all" was false.

It is now enforced twice, because either alone can be undone:

1. `readOnlyStore struct{ fs *FSStore }` — an unexported field and six explicit delegating methods.
   Nothing is promoted, so the assertion to `store.Store` FAILS, and a method added to `Store` later
   cannot silently widen this value.
2. `FSStore.mutate()` (`internal/store/fsstore.go`), `use()`'s sibling, returns the new sentinel
   `store.ErrReadOnly` on a read-only store. Eight mutating methods call it at their head: `Put`,
   `PutBytes`, `Flush`, `GC`, `RecordToolUse`, `RecordToolUseSuperseding`, `MarkSuperseded`,
   `AppendFileVersion`. That is the guard for the store BEHIND the value.

`closeBody` tolerates `ErrReadOnly` from its own `Flush`, because closing is not a write.
`Segments()` used to return a nil `*segLog` on a read-only store, against its own documented
"never nil" contract — `Segments().Open` would have panicked on a nil mutex rather than refusing —
so `OpenReadOnly` now installs a DEGRADED log: every `segLog` method checks `degraded` first, so
reads and writes alike report `core.ErrDegraded` instead of crashing or answering from a log that
was never loaded. `WriteCaptureSidecar` and `LinkCaptureReference` are package-level functions
taking a project root, not store methods, so no value hands them out and `mutate()` does not apply.

Tests (`internal/store/readonly_test.go`): `TestOpenReadOnly_TheValueIsNotAStore` asserts the
assertion to `Store` (and to `RefCounter`, and to a one-method `PutBytes` interface) fails;
`TestOpenReadOnly_EveryMutatingMethodRefuses` reaches past the interface to the `*FSStore`, calls all
eight, and requires `ErrReadOnly` from each AND a byte-identical tree after each — a guard that
errored after creating a directory passes the first assertion and fails the second;
`TestReadOnly_TheRefusalSweepCoversEveryGuardedMethod` parses this package's own non-test files and
requires the set of methods calling `mutate()` to EQUAL the set the sweep exercises, so a new writer
that takes the guard but is never exercised fails here rather than in a project.

**N-2.** `read_only` was `!repairing`, so `--seal-check` — which acquires the daemon lock, and
acquiring it creates `.qompack/run/` — called itself a run that wrote nothing. It is now
`!repairing && !sealCheck`, with new `repairing` and `attempts_daemon_lock` fields naming the reason
separately, and a human line per reason (`--repair` leads with the writes; `--seal-check` with the
lock). Measured on a copy of scenario C: default run 7 paths → 7 paths, `read_only: true`;
`--seal-check` 7 paths → 8 paths (`d - run`), `read_only: false`, `attempts_daemon_lock: true`.

**N-3.** `cp.Pointers.Tools` is a second loop over a second field that no seeded test reached.
`seedDanglingFixtures` now seeds a `ToolPointer` alongside the file pointer, and
`TestFsck_ReportsEveryDanglingReferenceClass` has a fifth case asserting the `points at tool result`
row.

**N-4.** `spoolRow` collapsed every `os.ReadDir` error into "no spool directory / nothing has been
spooled in this project" — reporting a count of zero for a spool nobody could read. It now checks
absence explicitly, routes the read through `fsckReadDir` (which `Lstat`s first, so a directory
replaced by a file is not Windows' `fs.ErrNotExist`), and reports `unknown` / `unreadable` naming
the path and the error. `TestDoctor_AnUnreadableSpoolIsItsOwnRow` covers both halves.

**N-5.** `fsckObjectFileBound = 2 * store.MaxPutBytes` was a bound fsck invented, while
`readObjectFile` refuses a `.zst` candidate past the encoder's worst-case encoded size — so every
compressed object between those two figures was one fsck called acceptable and the store then
refused, a defect the report did not have. `store.EncodedObjectLimit()` is now exported and
`fsckObjectSizeLimit(name)` applies exactly `readObjectFile`'s split: the encoded limit for `.zst`,
`MaxPutBytes` for a bare object. The old constant survives only as `fsckMaxLineBytes`, which is what
it was actually still used for. `TestFsck_TheObjectSizeLimitIsTheStoresOwn` asserts the relation
rather than materializing a 64 MiB file.

## 14. Fix round 3 — the segment log, the sweep's blind spot, and the lock field's tense

**1, the one that mattered.** `qompack doctor` printed `0 segment(s)` with status `ok` for a project
whose `index/segments.jsonl` held three records. `OpenReadOnly` never loaded the segment log —
`openSegLog` opens the file `O_CREATE`, and asking a project a question may not create one — and
`segmentCount` (`stats.go`) reads `len(byID)` without consulting `degraded`, so BOTH earlier shapes
(a nil log, then a degraded one) produced a confident zero. That is the class of answer the spool row
was corrected for in round 2, and it is worse here because the row reads `ok` rather than `unknown`.

**Option (a) was taken, not (b).** `openSegLogReadOnly` (`segments.go`) replays the log with no
append handle: `scanIndexJSONL` already treats a missing file as an empty one, so it creates nothing.
Option (b) — report `segments: unknown` — would have been honest and useless: the file is right
there, `fsck`'s own `index.segments` row already parses it, and a diagnostic that can say "three"
should not say "unknown". A loaded log also removes the special case rather than adding one: reads
answer, writes refuse, exactly as the store itself behaves.

The log is therefore NOT degraded, so it needed a write guard of its own. `segLog.readOnly` plus
`writable()` at the head of `Open`, `Close` and `MarkEncoded`, the same check in `PublishFilter`, and
`append` — the single choke point every record passes through — refusing again behind them. The
guard is consulted BEFORE any validation, which is what the first attempt got wrong: guarding only
`append` let `MarkEncoded` answer "segment not closed" from a log that would have refused anyway.

Measured on a fresh scratch project (scenario D: scenario C plus three `open` lines), with a binary
built from the folded commit: `doctor --json` store row
`{"status":"ok","observed":"1 object(s), 0 tool use(s), 0 file(s), 3 segment(s)"}`, and the tree
8 paths → 8 paths, byte-identical. Tests: `TestOpenReadOnly_LoadsTheSegmentLog` (three segments in,
three out, tree unchanged) and `TestOpenReadOnly_TheSegmentLogRefusesWrites` (all four writers
report `ErrReadOnly`, the reads still answer, and the refused `Close` left the segment open).

**2, the sweep's blind spot.** The round-2 test compared {methods calling `mutate()`} with {methods
`readOnlyRefusals` sweeps}, which is a tautology for the one mistake that matters: a new writer that
never calls `mutate()` — a `Prune` that opens with `s.use()` and calls `os.Remove` — is absent from
BOTH sets and the sweep stays green. `TestReadOnly_EveryExportedMethodIsClassified` now partitions
every EXPORTED `*FSStore` method into three buckets and fails on anything in none of them: delegated
by `readOnlyStore` (parsed off `readOnlyStore`'s own methods, so a new delegation needs no test
edit), swept by `readOnlyRefusals`, or named in `readOnlyReadsAllowlist` with a one-line reason. The
allowlist is checked in the other direction too: a name that no longer exists, or that has since
become delegated or guarded, is a stale justification and fails.

**3, the lock field's tense.** `TookDaemonLock` was set before the lock was attempted, and the
attempt can fail — `--seal-check` yields with a reported row when a daemon owns the lock. It is now
`AttemptsDaemonLock` / `attempts_daemon_lock`, alongside a new `Repairing` / `repairing`, because
"this run writes repairs" and "this run reaches for the lock" are different facts and an empty
`repairs` list cannot tell a clean repair run from a scan. The human line branches accordingly:
under `--repair` it leads with the writes (the lock is the smaller half of what that run does), under
`--seal-check` with the lock.
