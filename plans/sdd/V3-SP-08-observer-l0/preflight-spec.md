# V3-SP-08 preflight: Implementation-spec / Test-plan / Commit-plan claim verification

Scope: every claim in `plans/V3-SP-08-observer-l0.md` lines 640–2716 about code, fixtures, tooling
and docs that **already exist** on this branch (`arch/sp08-observer-seams`, worktree
`C:/Users/Quant/Documents/Programming/Projects/qompack-sp08`). Claims about files SP-08 *creates*
are out of scope except where the plan asserts something about their environment.

Counts: **OK 73 · MISMATCH 16 · MISSING 3** (92 rows).

> **State note.** The `arch/sp08-observer-seams` amendment (Commit 0's pre-steps a/b/c) landed in
> this worktree as **uncommitted working-tree changes** partway through this verification:
> `internal/store/put.go`, `internal/cli/hookclient.go`, `internal/daemon/handlers.go`,
> `internal/daemon/options.go`, `internal/daemon/daemon.go`, `plans/00-ARCHITECTURE.md` and five
> test files. Rows 6, 7 and 72 below describe the **post-amendment** tree and say so; every other
> row was re-checked against it or is untouched by it. `internal/cli/daemon.go` is **not** among
> the changed files, so row 1 stands unaffected.

MISMATCH / MISSING rows first.

| # | Claim (short) | Verdict | Actual (file:line + real shape/quote) |
|---|---|---|---|
| 1 | Commit 6: adding `WireObserver(&opts)` + `RegisterObserverIdleWork(d, obsv)` to `runDaemon` is enough to make the observer live end-to-end ("Prove the block is load-bearing… `TestE2E_ObserverThroughDaemon`… confirm it **fails**") | **MISMATCH** | `internal/cli/daemon.go:81-86` assigns **only** `opts.Log`, `opts.Metrics`, `opts.Clock` onto `daemon.NewOptions(root, cfg)`. `opts.Store`, `opts.Graph`, `opts.Ledger`, `opts.Grammar`, `opts.Sched`, `opts.Checkpoints` are all **nil**, and no production code anywhere opens a store or a DAG: `git grep 'store\.Open(\|dag\.Open(' -- internal cmd test tools ':!*_test.go'` returns only `internal/testutil/project.go:272` and `test/guards/probes.go:58`. Plan decision 8 makes `observer.New` error when `Store == nil \|\| Graph == nil`, so `WireObserver` returns an error, `runDaemon` logs Loud and carries on, all five seams stay nil, and every e2e row that asserts store/DAG/sketch artifacts cannot pass. Commit 6 must **also** construct `store.Open(root, cfg, store.Deps{…})` and `dag.Open(root, cfg, log)` in `runDaemon` — which the Done checklist ("`git diff develop..HEAD -- internal/cli/daemon.go` shows exactly the two wiring blocks and no other change") forbids. |
| 2 | Commit 7 / exit criteria: "`go run ./tools/devtool ci-local` — `verify`, `test`, `cover`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs` all green"; Commit 6 "the import-graph check in `verify`" | **MISMATCH** | `tools/devtool/main.go:26-48` registers 21 tasks and **none** is named `verify`, `bench-gate`, `replay-gate`, `security`, `docs`, `crossbuild`, `nomagic`, `import-graph` or `test-only-dep`. `tools/devtool/cilocal.go:14-23`: `ciLocalSteps = {fmt-check, lint, vet, build, test, cover, plugin-validate, gen-config-docs --check}` — no race run, no bench, no replay, no security, no crossbuild. `verify`/`bench-gate`/`replay-gate`/`security`/`docs`/`cover`/`crossbuild`/`lint-windows`/`test`/`plugin-validate` are **CI jobs** in `.github/workflows/ci.yml:14,75,96,126,137,147,190,224,233,254`. The local equivalent of `verify` is `fmt-check` + `lint` + `go vet ./...` + `go build ./...`; gofumpt/nomagic/import-graph/test-only-dep all live under `lint` (`tools/devtool/lint.go:26-35`). |
| 3 | Commit 1 "`go run ./tools/devtool fmt lint test`"; Commit 6 "`go run ./tools/devtool build test test-race`"; Commit 4 "`go run ./tools/devtool test cover`" | **MISMATCH** | `tools/devtool/main.go:62`: `task, rest := args[0], args[1:]` — **one task per invocation**. `taskFmt(args []string)` (`fmt.go:57`), `taskTest(args)` (`test.go:22`), `taskBuild`, `taskTestRace` (`test.go:40`) all ignore `args`. So `devtool fmt lint test` runs **only** `fmt` and exits 0; `devtool build test test-race` runs **only** `build`; `devtool test cover` runs **only** `test`. Each must be a separate `go run ./tools/devtool <task>` line. |
| 4 | The eight commit subjects as written in the Commit plan | **MISMATCH** | Both `tools/devtool/checkcommitmsg.go:21` and `.github/workflows/ci.yml:44` enforce `^(feat\|fix\|docs\|test\|refactor\|perf\|build\|ci\|chore\|revert)(\(scope\))?: .{1,64}$` — at most **64 characters after `": "`**. Four subjects exceed it and would be rejected by the installed commit-msg hook and by CI's `verify` job: Commit 0 `…empty Canon.Strip and a caller MinHash opt-out` (65), Commit 1 `addressable tombstones, tool classification, and task-boundary signals` (70), Commit 2 `PostToolUse pipeline with store, index, sketches and DAG emission` (65), Commit 6 `SessionStart startup/resume, SessionEnd flush, session index and GC` (67). Commits 3/4/5/7 pass (52/59/62/54). |
| 5 | Commit 4 "`internal/observer` at or above the 75% floor (§6.4)"; Done checklist "the only file **modified** outside [internal/observer and the test trees] is `internal/cli/daemon.go`" | **MISMATCH** | The 75% floor is real (`plans/OWNERS.tsv:45` → `observer\tSP-08\t75\tOnToolUse`; `plans/00-ARCHITECTURE.md:2294` "everything else 75%"), but it is **inert** until SP-08 is added to `tools/devtool/cover.go:40-48` `landedSubplans` — `floorApplies` (`cover.go:177`) exempts an unlanded subplan "at any coverage, including 0%". And `tools/devtool/cover_test.go:263-267` **fails the build** if `landedSubplans["SP-08"]` is true ("SP-08 is listed as landed, but wave 2 has not been cut yet"). So SP-08 must edit **two more files outside its file-map** (`cover.go` and `cover_test.go`), or its own coverage checklist item asserts nothing. |
| 6 | Pre-step (a): the store must honour an explicit empty `Canon.Strip` and a caller MinHash opt-out; `canonOptions()` passes `canon.Options{Strip: cls, KeepDeltas: true, MinHash: sketch.MinHashOptions{…}}` | **OK — now landed (uncommitted)** | Before the amendment `internal/store/put.go`'s `canonOptions` merged **only** `if len(o.Canon.Strip) > 0 { … }`, ignoring an empty non-nil `Strip` and the caller's `MinHash` entirely. It now reads: `if o.Canon.Strip != nil { opts.Strip = o.Canon.Strip; if !o.Canon.MinHash.Enabled { opts.MinHash.Enabled = false } }`. Three consequences for `tooluse.go`/`prompt.go`: (i) **nil-ness, not length**, is the override switch — an empty non-nil `Strip` now means "no optional class"; (ii) the MinHash opt-out is **gated on `Strip` being non-nil**, so `verbatimOptions()` must set both (`Strip: []canon.Class{}` **and** `MinHash.Enabled: false`) or the opt-out silently does nothing; (iii) **`Canon.KeepDeltas` is still not read** — `opts.KeepDeltas = o.KeepRaw` — so the plan's `KeepDeltas: true` in `canonOptions()` is inert and `KeepRaw: true` is what matters (the plan already passes it). MinHash `Permutations`/`NearDupThreshold` remain config-only: a caller may turn the signature off, never on. The rule is now written into `plans/00-ARCHITECTURE.md` §5.8 (added by the same change, just below the `PutOptions` block). |
| 7 | Pre-step (c) / Commit 0: the amendment adds "the §5.4 declaration in `plans/00-ARCHITECTURE.md`" for `Services.Mode` | **MISMATCH** | The doc half was **already committed** on this branch before the amendment work started — `git diff plans/00-ARCHITECTURE.md` does **not** touch §5.4. `plans/00-ARCHITECTURE.md:917-932` already declared `Mode func() contract.Mode // SP-05 → SP-08` and the hoist rationale ("New therefore constructs the monitor and assigns `svc.Mode = monitor.Mode` BEFORE it runs the bind loop…"). Do not re-add that text. The **code** half has now landed uncommitted: `internal/daemon/options.go` gains `Mode func() contract.Mode` on `Services` with a doc comment, and `internal/daemon/daemon.go` hoists `statePath` / `contract.NewMonitor` / `monitor.Register` above the bind loop and assigns `svc.Mode = monitor.Mode` immediately before `for _, bind := range o.binds`. So `WireObserver`'s `modeSrc = s.Mode` capture now works as the plan describes — but keep the plan's nil guard: `modeSrc` is still nil in any test that never calls `daemon.New`. |
| 8 | Decision 10 / §12.1: "L0 and L1 keep running… verbatim capture"; `o.mode()` never guards a write; `TestModePassiveStillWrites` proves it | **MISMATCH** | True in-process, false through the daemon. `internal/daemon/handlers.go:402`: `if d.svc.ObservePrompt != nil && mode.MayAct() { out = d.callObservePromptWithDeadline(ctx, ev) }`, and `internal/contract/mode.go:53-54`: `func (m Mode) MayAct() bool { return m != ModeDegradedPassive && m != ModeOff }`. So in `ModeDegradedPassive` the observer's `OnUserPrompt` is **never invoked** — no `PutBytes`, no `RecordToolUse`, no `userprompt:` node — which is exactly the "verbatim capture keeps running" §12.1 promises. (`observe.tool`/`observe.stop` use `MayRecord()` (`handlers.go:353`, `mode.go:60-61`: `m != ModeOff`) and are unaffected; `handleFlush`'s `SessionEnd` seam is also `MayRecord()`-gated at `handlers.go:723`, so `ModeOff` skips GC, the sketch write and the state file.) `TestModePassiveStillWrites` drives the observer directly and cannot see this. |
| 9 | Exit criterion: "`internal/observer` imports exactly: `core paths config logging obs hookio store canon sketch dag grammar tokens`… No `scheduler`, `checkpoint`, `symbols`, `contract`, `negknow`, `analyzer`, or `daemon`", checked by the import-graph check | **MISMATCH** | The allow-table is a **Go table**, `tools/devtool/importrules.go:90`: `"observer": {"hookio", "store", "chunk", "canon", "sketch", "dag", "grammar", "negknow", "tokens"}` (plus `foundation = {core, paths, config, logging, obs}` at `:6`), transcribed from `plans/00-ARCHITECTURE.md:409` §3.2 — it is **not** read from the Markdown at run time. `negknow` is **permitted** and `chunk` is permitted but absent from the plan's list, so the "no negknow" half of the exit criterion is not machine-enforced and must be checked by `git grep -n 'negknow' -- internal/observer` (which the Done checklist already does). `scheduler`, `checkpoint`, `symbols`, `contract`, `analyzer`, `daemon` **are** correctly excluded by the table. |
| 10 | Test rows: "`store.Open(root)` returns the prompt byte for byte"; "`Open(root)` then JSON round-trips" | **MISMATCH** | Two different symbols. Package-level `internal/store/store.go:94`: `func Open(root string, cfg config.Config, deps Deps) (Store, error)` — **three** args, returns a `Store`. Reading content by root hash is the **method** `internal/store/read.go:82`: `func (s *FSStore) Open(ctx context.Context, root core.Hash) (io.ReadCloser, error)` (on the `Store` interface, `store.go:29`), with `GetRoot(ctx, root) (Root, error)` at `read.go:46` and `OpenSpan` at `read.go:104`. So the assertion is `rc, err := st.Open(ctx, rec.Root); b, _ := io.ReadAll(rc)`. |
| 11 | Done checklist: `nomagic` int set is `{20000, 12000, 10000, 2048, 1024, 4096, 16384, 300, 120, 450}` | **MISMATCH** | `tools/lint/nomagic/literals.go:14`: `var forbiddenInts = []int64{20000, 12000, 10000, 8000, 2048, 1024, 4096, 16384, 300, 120, 450}` — **8000 is also forbidden** (the §11.6 extension, `plans/00-ARCHITECTURE.md:2673`). The float set matches exactly (`literals.go:12`: `{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}`). None of SP-08's declared constants (8, 64, 256<<10, 32, 256, 3, 4000, 8s, 4<<20, 512<<10, 48, 5) hits either set. |
| 12 | "`internal/observer/tombstone_test.go` — it already ships with four passing tests" | **MISMATCH** | Five: `TestTombstone_RendersTheSection81Form:24`, `TestTombstone_IsAddressable:43`, `TestTombstone_HandlesAnEmptyRecord:55`, `TestHumanBytes_UsesTheBinaryDivisor:64`, **`TestHumanBytes_NeverEmitsASpaceBeforeTheUnit:91`**. The fifth also pins renderer output and must survive commit 1. |
| 13 | `ExtractSignals` stays pure and returns **raw** paths; "the caller normalizes with `paths.Norm`/`paths.Key`" | **MISMATCH** | The shipped doc comment says the opposite: `internal/observer/signals.go:14-15` — "`// Paths lists the file paths this event touched, in paths.Key form.`". The plan's design is fine, but that comment must be corrected in commit 1 or the package documents a contract it does not keep. |
| 14 | (not claimed) `observertest`'s `extract_signals_detects_todo_test_and_git` constrains `Signals.Paths`' zero value | **MISSING** from plan | `internal/observer/observertest/behaviour.go:289-292`: `require.Equal(t, observer.Signals{}, got, "an empty payload must not manufacture a boundary")` — so for a zero `Event`, `Paths` must be **nil**, never `[]string{}`. `behaviour.go:268-271` also requires `got.Paths` to *contain* `"src/auth.ts"` from `{"file_path":"src/auth.ts"}` (raw == key here, so raw is compatible). |
| 15 | `testdata/golden/observer/tombstones.txt` (fixture created in commit 1) | **MISSING** (expected) | `testdata/golden/` holds `canon chunk config contracts eval plugin store` — no `observer/`. Consistent with the plan; recorded so the implementer knows the directory must be created. |
| 16 | "`dagtest.Synth`'s `ControlOnlyFraction: 0.35`" | **MISMATCH** (spelling) | `Synth` is a function: `internal/dag/dagtest/synth.go:124` `func Synth(seed int64, s SynthSpec) (nodes []dag.Node, edges []dag.Edge, criteria []dag.NodeID, truth map[dag.NodeID]bool)`. `ControlOnlyFraction` is a field of **`dagtest.SynthSpec`** (`synth.go:59`), used at `0.35` in `internal/dag/bench_test.go:30`, `slice_compare_test.go:40`, `dagtest/synth_test.go:24,40`. |
| 17 | `plans/00-ARCHITECTURE.md` §10 "subject: imperative mood, ≤ 72 chars" | **MISMATCH** (doc vs tool) | `plans/00-ARCHITECTURE.md:2477` says ≤ 72; the enforced rule everywhere is 64 (`checkcommitmsg.go:21`, `ci.yml:44`). Use 64. Body wrap is 100 runes, enforced (`checkcommitmsg.go:32` `maxBodyLineLen = 100`, `:84`), and `Refs:` is **required** on `feat`/`fix` only (`checkcommitmsg.go:90`). |
| 18 | Plan references `observer.Persister`, `observer.Mode`/`ModeFull`/`ModePassive`, `FeatureSample`, `TestOutcome`/`TestPass`/`TestFail`/`TestUnknown`, `Rehydrator`, `SubagentCapture`, `SubagentToolRef`, `Options.{Touch,Explore,Hot,Symbols,Rehydrate,Mode,OnSignals,OnFeatures}` | **MISSING** (in scope) | Today `internal/observer` declares exactly: `Event = hookio.Event` (`observer.go:29`), `Output = hookio.Output` (`:31`), `Observer` (`:37`), `Options` (`:62`, nine fields: ProjectRoot, Cfg, Store, Graph, Grammar, Tokens, Log, Metrics, Clock), `stubObserver` (`:95`), `Signals` (`signals.go:7`), plus `Tombstone`/`humanBytes`/`ExtractSignals`. Every other name above must be **declared** by SP-08. `plans/00-ARCHITECTURE.md:2124-2145` §5.21 does not list them; adding them is legal (SP-08 owns the package; Rule W-3 governs *other* subplans' interfaces) but the Done checklist's "match §5.21 character for character" applies only to `Observer`, `Tombstone`, `Signals`, `ExtractSignals`, which do match. |
| 19 | `hookio.HSO{HookEventName: "UserPromptSubmit", AdditionalContext: …}` | **MISMATCH** (minor) | `internal/hookio/output.go:29-32` declares only `hookEventSessionStart = "SessionStart"` and `hookEventPreCompact = "PreCompact"`, with constructors `SessionStartOutput`/`PreCompactOutput` (`:51`, `:59`) and the stated intent "that exact string appears exactly once in the codebase for each". There is **no** `UserPromptSubmitOutput`; the plan's hand-built literal is legal (nothing lints it) but introduces the string outside `hookio`. |
| 20 | `RegisterObserverIdleWork` does `if p, ok := obsv.(observer.Persister); ok { d.Idle().Register("observer.persist", 50, p.Persist) }` | **MISMATCH** (follows #18) | `observer.Persister` does not exist yet. `IdleController.Register` itself matches: `internal/daemon/idle.go:33` `Register(name string, prio int, fn func(ctx context.Context) error)`, and `Persist(ctx) error` fits. Note `internal/daemon/idle.go:61`: `RunOnce` skips tasks whose name carries the `"act."` prefix when `!mode().MayAct()` — `"observer.persist"` is unprefixed and so keeps running in degraded-passive, which is correct. SP-05's own priorities are drain 10, sketches 20, metrics 30 (`daemon.go:283-291`), so 50 is genuinely below nothing that exists yet. |
| 21 | A: `BuildToolUse` emits tool-use node → tool-result node → `EdgeProduces` → assistant node | **OK** | `internal/dag/builders.go:164-178`, in that exact order. |
| 22 | A: `EdgeConsumes` `toolresult:<prev> → assistant` only when `PrevToolUseID != "" && PrevTurn < Turn` | **OK** | `builders.go:180-185`. Invalid input `PrevTurn > Turn` returns `ErrInvalidEdge` with nothing mutated (`:150-153`). |
| 23 | A: `turnLinkKind` → `EdgeSequence` vs `EdgeControlOnly` via `sharesState` reading `Out`/`In` | **OK** | `builders.go:237-245`, and `sharesState` at `:258-292` (`collect(g.Out(prevUse), false)`, `collect(g.In(prevUse), true)`, filtering to `EdgeSharedFile`/`EdgeSharedSymbol`). First tool use of a session → `EdgeSequence` (`:238-240`), matching `TestGraph_FirstToolUseOfASessionIsSequence`. |
| 24 | A: file node + `EdgeSharedFile` oriented by `Writes` (file→tooluse read, tooluse→file write) | **OK** | `builders.go:191-199` + `sharedStateEdge` at `:297-303`: `from, to := anchor, use; if o.Writes { from, to = use, anchor }`. |
| 25 | A: symbol nodes sorted ascending, one `EdgeSharedSymbol` each | **OK** | `builders.go:201-209` over `sortedUniqueSymbols(o.Symbols)`. |
| 26 | A: `BuildToolUse` sorts **and dedupes** `Symbols` | **OK** | `builders.go:313-331` — copies, drops `""` and duplicates, `sort.Strings`, returns nil when nothing survives. |
| 27 | A: `EdgeSupersedes` runs older → newer, from `ObservedTool.Supersedes` | **OK** | `builders.go:211-219`: `From: ToolUseNode(o.Supersedes), To: use`. Doc at `:24`: "EdgeSupersedes runs superseded → superseding". |
| 28 | A: `Node.Ref` for a tool-use node is the tool name | **OK** | `builders.go:166-167`: `Ref: o.Tool`. (The tool-**result** node's `Ref` is `string(o.ToolUseID)`, `:171`.) |
| 29 | A: `BuildUserPrompt` emits `userprompt:<turn>` + `EdgeConsumes` to `assistant:<turn>` | **OK** | `builders.go:355-367`: `AddNode{ID: UserPromptNode(o.Turn), Kind: KindUserPrompt…}` then `AddEdge{From: prompt, To: AssistantNode(o.Turn), Kind: EdgeConsumes}`. |
| 30 | A: `BuildSegment(Members: nil)` mints the segment node with `Ref == "<start>-<end>"` and the `previous → current` chain edge | **OK** | `builders.go:518-544`: `ref := strconv.Itoa(int(s.StartTurn)) + "-" + strconv.Itoa(int(s.EndTurn))`; the member loop is a no-op on nil; the chain edge is emitted only when `s.PrevID != 0` (`:537-542`). `s.ID == 0` returns `ErrInvalidNode`. |
| 31 | A: `traverse.go`'s thin branch drops `EdgeControlOnly` **and nothing else** | **OK** | `internal/dag/traverse.go:220`: `if o.Thin && e.Kind == EdgeControlOnly { continue }` — the only `o.Thin` reference in the file. |
| 32 | A: `EdgeSupersedes` multiplier is 0.30 | **OK** | `internal/dag/kinds.go:88`: `EdgeSupersedes: 0.30` in `edgeKindMultipliers`; `Multiplier()` at `:163`. |
| 33 | A: `ParseNodeID` splits on the **first** colon | **OK** | `internal/dag/nodeid.go:185`: `strings.Cut(string(id), nodeIDSep)` with `nodeIDSep = ":"` (`:26`). Doc at `:11`: "split on the FIRST colon". |
| 34 | A: `nodeid.go` quote — "Every consumer builds IDs through the constructors below rather than concatenating strings … it silently produces two disconnected halves of the same graph." | **OK** | `internal/dag/nodeid.go:13-16`, verbatim. |
| 35 | A: `SymbolNode` keys `"<pathKey>#<name>"` | **OK** | `nodeid.go:156-158`, `symbolSep = "#"` (`:31`). |
| 36 | A: golden carries `"id":"assistant:1"`, `"id":"symbol:src/auth.ts#refreshToken"`, `{"from":"tooluse:toolu_01ABCdef","to":"tooluse:toolu_02GHIjkl","kind":5}` | **OK** | `testdata/golden/contracts/dag/graph-basic.jsonl:3`, `:6`, `:22` (the edge line's full text is `{"type":"edge","from":"tooluse:toolu_01ABCdef","to":"tooluse:toolu_02GHIjkl","kind":5,"weight":1,"turn":2}` — the plan quotes a prefix). 24 lines total. |
| 37 | A: its `MANIFEST.json` says SP-08/09/12 assert against it | **OK** | `testdata/golden/contracts/dag/MANIFEST.json:8`: `"note": "…internal/dag's TestGraphBasicGolden reproduces it byte for byte through Flush; SP-08, SP-09 and SP-12 assert against these bytes"`. |
| 38 | A: `dag.TestBuilderOutputIsAcyclic` exists | **OK** | `internal/dag/builders_test.go:621`. |
| 39 | A: `dag.Open(t.TempDir(), config.Defaults(), logging.Nop())` is the reference `Graph` | **OK** | `internal/dag/log.go:50`: `func Open(root string, cfg config.Config, log logging.Logger) (Graph, error)`. Note the `Graph` interface has **12** methods a `fakeGraph` must implement: `AddNode, AddEdge, Node, Out, In, BackwardSlice, ForwardSlice, CrossingEdges, NodesAfter, Flush, Compact, Stats` (`internal/dag/graph.go:35-59`). |
| 40 | B: `ArgsDigest` doc comment "SP-08 calls this; nothing else may re-derive it, so that two subplans can never disagree about what 'the same tool arguments' means." | **OK** | `internal/store/tooluseindex.go:339-341`, verbatim. |
| 41 | B: preview keys, in order `file_path, path, pattern, command, url` | **OK** | `tooluseindex.go:28`: `var argsPreviewKeys = []string{"file_path", "path", "pattern", "command", "url"}`, consumed in order at `:437`. |
| 42 | B: ≤120-**byte** cap with a `…` suffix | **OK** | `tooluseindex.go:21` `const argsPreviewMax = 120 //nomagic:allow §5.8 specifies ToolUseRecord.ArgsPreview as ≤120 chars`; `:24` `previewEllipsis = "…"`; `previewString` (`:453-481`) truncates on a **byte** budget on a rune boundary ("truncates to argsPreviewMax BYTES"), collapsing whitespace runs and dropping control bytes. (The `ToolUseRecord.ArgsPreview` field comment at `tooluse.go:41-42` still says "characters" — cosmetic.) |
| 43 | B: `TestArgsDigest_KeyOrderInvariant` in `tooluse_test.go` | **OK** | `internal/store/tooluse_test.go:236`. `tooluse_test.go:258-259` also pins `len(preview) <= argsPreviewMax` and the ellipsis suffix. |
| 44 | B: `ArgsDigest` sorts keys recursively, preserves array order, re-emits numbers verbatim, digests under `core.DomainArgs` | **OK** | `tooluseindex.go:348-366` (`dec.UseNumber()`, `writeCanonJSON` at `:369-410`, `core.HashBytes(core.DomainArgs, canonical)`); `core.DomainArgs = "qompack.args.v1"` at `internal/core/hash.go:48`. Unparseable input is digested over its trimmed raw bytes, not collapsed to empty (`:358-362`). |
| 45 | B: `FSStore.ToolUsesByPath` walks the per-path id list backwards (newest first) | **OK** | `internal/store/tooluseindex.go:281`: `for i := len(ids) - 1; i >= 0 && len(out) < n; i--`. `limit <= 0` means no cap (`:277`). |
| 46 | B: `Stats().DedupRatio == RawBytes / Bytes` | **OK** | `internal/store/stats.go:40-42`; doc at `:17-20`. `Stats` struct at `internal/store/gc.go:36-45`. |
| 47 | B: `Stats().RawBytes` accumulates pre-dedup bytes | **OK** | `internal/store/put.go:222` `s.rawBytes += raw`; recovery re-accumulates at `flush.go:343`; read at `stats.go:33`. Field doc `gc.go:39-40`: "total pre-dedup, pre-compression size of everything ever stored". |
| 48 | B: `FSStore.nearDup` exists and depends on `PutResult.Signature` | **OK** | `internal/store/put.go:260`: `func (s *FSStore) nearDup(path string, root core.Hash, canonBytes int64, sig sketch.Signature) *NearDupInfo`, called at `:144` and `:188` with `res.Signature`. `PutResult.Signature sketch.Signature` at `types.go:55`; `NearDup *NearDupInfo` at `:58`. |
| 49 | B: `Put` order is redact → canonicalize → chunk | **OK** | `internal/store/put.go:93-94` doc ("REDACT, then canonicalize, then chunk"), `:114-115` redact, `:119` `Canon.Run`, `:128` `splitChecked`. |
| 50 | B: `rec.Signature.IsNearDup(p.Signature, thr)` | **OK** | `internal/sketch/minhash.go:585`: `func (s Signature) IsNearDup(o Signature, threshold float64) bool`. |
| 51 | B: `store.Store` read API for the Phase-1/e2e tests | **OK** (see #10) | `internal/store/store.go:24-35`: `Put`, `PutBytes`, `GetChunk`, `GetRoot(ctx, root) (Root, error)`, `Open(ctx, root) (io.ReadCloser, error)`, `OpenSpan`, `Has`. `store.Root{Hash, Chunks []ChunkRef, CanonBytes, RawBytes, Tokens}` at `types.go:19-29`; `store.ChunkRef = core.ChunkRef` (alias, `types.go:11`) so the plan's `[]core.ChunkRef` is correct. |
| 52 | B: `store.PutOptions{Tool, Path, Canon, KeepRaw, Ephemeral}` and `PutResult{Root, Novel, Reused, Signature, NearDup, Truncated, Redacted}` | **OK** | `internal/store/types.go:32-44` and `:47-66`. |
| 53 | B: `ToolUseRecord` carries `Signature`, `Status`, `SupersededBy`, `Ephemeral`, `Subagent`, `ArgsDigest`, `ArgsPreview` | **OK** | `internal/store/tooluse.go:33-57`. `MarkSuperseded(ctx, older, by)` older-first at `tooluseindex.go:294`, idempotent (`:317-319`). |
| 54 | B: `GCPolicy{RetainDays, RetainSessions, DryRun, Deadline}` / `GCReport{ScannedObjects, DeletedObjects, BytesFreed, Truncated}` | **OK** | `internal/store/gc.go:12-20` and `:23-32`. |
| 55 | B: `SegmentLog` has `Frontier`, `Current`, `Open`, `Close` with the shapes the plan calls | **OK** | `internal/store/segment.go:38-57`: `Open(ctx, Segment) (core.SegmentID, error)`, `Close(ctx, id, endTurn, feats map[string]float64) error`, `Current(ctx, s) (Segment, error)`, `Frontier(ctx, s) (core.TurnIndex, error)`. `Segment{ID, Session, StartTurn, EndTurn, StartTS, EndTS, Features, Tokens, EncodedOnce, CheckpointSeq, Closed, BloomRef}` at `:15-34`. |
| 56 | C: eight classes named `timestamps ansi pids addresses tmpPaths durations crlf paths` | **OK** | `internal/canon/types.go:12-26`; registration order at `internal/canon/classes.go:22-30`. |
| 57 | C: nil `Strip` means "every class"; empty non-nil means "exactly none, plus always-on"; `alwaysOn = {crlf, paths}` | **OK** | `internal/canon/classes.go:63-82` (`gateSet`): "A nil Strip means 'every class' — the documented meaning of the zero Options … A non-nil Strip, INCLUDING an empty one, means exactly those classes"; `:40` `var alwaysOn = map[Class]bool{ClassCRLF: true, ClassPaths: true}`, "applied regardless of Options.Strip" (`:33`). |
| 58 | C: `TestKnownDeletionMediatedLimit` exists | **OK** | `internal/canon/golden_test.go:309`. |
| 59 | C: `BenchmarkRun_GoTest` and `BenchmarkRun_Bash100KB` exist | **OK** | `internal/canon/bench_test.go:66` and `:50`. |
| 60 | C/decision 12: `canon.MinHashOptions` is an **alias** of `sketch.MinHashOptions`, so `sketch.MinHashOptions{…}` compiles at the call site | **OK** | `internal/canon/classes.go:9`: `type MinHashOptions = sketch.MinHashOptions` ("It is an alias, not a defined type"). Fields `{Enabled, Permutations, ShingleSize, NearDupThreshold}` at `internal/sketch/minhash.go:121-135`; `canon.Options{Strip, KeepDeltas, MinHash}` at `internal/canon/types.go:59-66`. `canon.DefaultShingleSize = 5` (`classes.go:18`). |
| 61 | D: `handleFlush` = `registry.End`, `ingest.CloseSession`, `SessionEnd` seam, `contract.WriteMarker`, `SketchSet.Save`, `Drain` | **OK** | `internal/daemon/handlers.go:712-747` (`flushRoute`), in that order; `Sketches.Save` at `:733-735`, after the seam at `:723-727`. |
| 62 | D: `SketchSet.Save` returns early unless dirty; `SketchSet.Write` sets dirty | **OK** | `internal/daemon/sketchset.go:143-145` (`if !s.dirty { return }`) and `:78-83` (`fn(s); s.dirty = true`). `Save(root string, log logging.Logger)` never writes `tried.bloom` (`:133-134`, `:158-160`). |
| 63 | D: `SketchSet` exported members are `Touch`/`Explore`/`Top` (+`Tried`) | **OK** | `internal/daemon/sketchset.go:40-49`: `Tried *sketch.Bloom; Touch *sketch.CMS; Explore *sketch.HLL; Top *sketch.MisraGries`. |
| 64 | D: `handleObserveTool`/`handleObserveStop` do `registry.Touch` then `ingest.Accept` unless `ModeOff` | **OK** | `handlers.go:330-338` both delegate to `acceptHotPathEvent` (`:343-365`): unconditional `registry.Touch`, then `if !d.monitor.Mode().MayRecord() { return OK }`, and `MayRecord()` is `m != ModeOff` (`internal/contract/mode.go:60-61`). |
| 65 | D: `handleObservePrompt` + `promptReplyDeadline` | **OK** (with #8) | `handlers.go:372-406`; `promptReplyDeadline = 250 * time.Millisecond` at `handlers.go:22`, enforced by racing a goroutine against the deadline in `callObservePromptWithDeadline` (`:413-435`). `internal/cli/hookclient.go:29` keeps a cli-local copy of the same 250 ms. |
| 66 | D: `buildRoutes` copies `o.Ops()` then fills from `defaultRoutes` | **OK** | `internal/daemon/daemon.go:296-309`; `defaultRoutes` at `:311-326`. `Options.Handle` "replacing any previous registration" at `options.go:75-81`; `Bind` at `:101-103` (pointer receiver over unexported `binds`). |
| 67 | D: `SessionRegistry.HotMode()`, not `HotPathMode()` | **OK** | `internal/daemon/registry.go:132`: `func (r *SessionRegistry) HotMode() ipc.HotPathMode`. |
| 68 | D: `TestIngestACKPrecedesProcessing` and `TestMarkerIsWrittenByFlushAndCheckpointOnly` exist | **OK** | `internal/daemon/ingest_test.go:112` and `internal/daemon/daemon_test.go:1161`. |
| 69 | D: `contract.Monitor.RunAll` | **OK** | `internal/contract/monitor.go:31`: `RunAll(ctx context.Context, e Env) ([]Result, Mode)`. |
| 70 | D: `Options.Sketches *SketchSet`, `Options.Sched scheduler.Runtime` | **OK** | `internal/daemon/options.go:37` and `:40`; mirrored on `Services` at `:113` and `:116`. |
| 71 | D: `daemon.New` runs the bind loop **before** the registry/idle controller exist, and `Idle()`/`Registry()` are on the `Daemon` interface | **OK** | `internal/daemon/daemon.go:232-234` bind loop; `:256-263` registry + idle. Interface at `:63-75` (`Run, Registry() *SessionRegistry, Drain, Idle() IdleController, Stop`). `New(o Options)` takes Options **by value** (`:209`), so `Bind` must be called on an addressable `opts` before the call — exactly as the plan says. |
| 72 | E: pre-step (b) — `rawExtras` forwards the resolved agent name and `resolveEvent` restores `Event.Extra` from `req.Raw` | **OK — now landed (uncommitted)** | `internal/hookio/event.go:36` is unchanged: ``Extra map[string]json.RawMessage `json:"-"` ``. Client side, `internal/cli/hookclient.go` now has `type subagentExtras struct { Subagent bool \`json:"subagent"\`; Agent string \`json:"agent,omitempty"\` }`, `var subagentNameKeys = []string{"subagent_type", "agent_name", "agent"}` and `func subagentName(ev hookio.Event) string` (first key whose value is a **non-empty JSON string** wins; anything else falls through), with `rawExtras` emitting `json.Marshal(subagentExtras{Subagent: true, Agent: subagentName(ev)})` — so an unnamed subagent still marshals to exactly `{"subagent":true}`. Daemon side, `resolveEvent` now calls `ev.Extra = restoredExtra(ev.Extra, req.Raw)`, merging `req.Raw`'s top-level keys into a **fresh** map with the Event's own values winning, and returning `extra` unchanged when `raw` is absent, not an object, or malformed. Consequence for `stop.go`: `e.Extra["agent"]` is populated daemon-side, and it holds a **JSON-encoded string** (`json.RawMessage`), so `subagentName` in `internal/observer` must `json.Unmarshal` it — the plan's `Extra["agent"] = "\"code-reviewer\""` test fixture is right. Note the restored map also carries `"subagent"`, and for `checkpoint` it carries `"trigger"`. |
| 73 | E: hook subcommand spellings | **OK** | Six, `internal/cli/hooks.go:9-42` — see the verbatim list below. |
| 74 | F: `Defaults()` `store.canonicalize` | **OK** | `internal/config/defaults.go:27-35`: `Enabled: true`, `Strip: {"timestamps","ansi","pids","addresses","tmpPaths","durations"}` (six; **no** `crlf`), `MinHash{Enabled: true, Permutations: 128, NearDupThreshold: 0.9}` — so the plan's `TestOnToolUse_CanonOptionsFromConfig` expectation `{crlf, timestamps, ansi, pids, addresses, tmpPaths, durations}` is right after the observer prepends `crlf`. |
| 75 | F: `Defaults()` `store.retention` = 30 days / 10 sessions | **OK** | `internal/config/defaults.go:23-26`. Matches `TestOnSessionEnd_GCPolicyFromConfig`. |
| 76 | F: `runtime.hotPath.maxPayloadBytes` value and Go field path | **OK** | `internal/config/defaults.go:125`: `MaxPayloadBytes: 1048576`; field path `Config.Runtime.HotPath.MaxPayloadBytes` (`internal/config/runtime.go:39`). Validation floor is 4096 (`validate.go:221`). Its own doc string is "maximum NDJSON request line size accepted by the daemon" — the plan repurposes it as the observer's body-truncation cap, which is a deliberate reading, not an error. |
| 77 | F: `cfg.Get("runtime.hotPath.maxPayloadBytes")` | **OK** | `internal/config/config.go:280`: `func (c Config) Get(dotted string) (any, bool)` over a reflection index; returns the concrete `int`. |
| 78 | F: how tests build a Config with canonicalization disabled | **OK** (no helper) | There is **no** `config.Parse` and no disable helper. The exported API is `Defaults()` (`defaults.go:14`), `Load(env Env) (Config, Provenance, []Warning, error)` (`load.go:26`, requires a non-empty `Env.ProjectRoot` and reads files), `StripJSONC`, `ViolationsFromWarnings`. Every test builds `cfg := config.Defaults()` then mutates the struct (e.g. `internal/store/bench_test.go:33`, `internal/canon/bench_test.go:52`). Do the same: `cfg.Store.Canonicalize.Enabled = false`. |
| 79 | G: `SynthSpec` fields | **OK** | `internal/eval/synthesize.go:9-30`: `Turns, ToolMix, FileRereadRate, TestOutputNoise, Changepoints, Eliminations, SubagentCalls, DependencyChangeAt, CompactionAt` (nine — the plan names eight and omits `DependencyChangeAt`, which is fine). |
| 80 | G: `Synthesize(seed, spec)` signature | **OK** | `internal/eval/synth.go:83`: `func Synthesize(seed int64, spec SynthSpec) Session`. |
| 81 | G: `Session`/`Turn`/`ToolCall` field names | **OK** | `internal/eval/types.go:47-58` (`ID string`, `Turns []Turn`, `CompactionAt`, `Meta`, `Synthetic`), `:16-29` (`Index, Role, TS, Text, ToolCalls, Tokens`), `:32-43` (`ID core.ToolUseID, Name, Args json.RawMessage, Result json.RawMessage, Paths []string`). |
| 82 | G: `call.Result = mustCompactJSON(map[string]int{"tokens": tokens})`, `args: null` | **OK** | `internal/eval/synth.go:393` (and `:381` for the elimination branch); `mustCompactJSON` at `:521`. `testdata/sessions/synthetic/dep-change-1.json` shows `"args": null, "result": {"tokens": 278}, "paths": ["src/pkg0/file1.go"]`. The plan's diagnosis is exactly right. |
| 83 | G: `testdata/sessions/synthetic/` exists | **OK** | Present, with `CORPUS.json`, `dep-change-1..3.json`, `long-idle-1.json`, … alongside `testdata/sessions/recorded/`. |
| 84 | H: `testdata/corpora/toolout/` groups and counts; `<name>.txt.meta.json` = `{"tool":…,"path":…}` | **OK** | Nine group dirs, 30 `.txt` files: `ansi` 1, `bash` 5, `fileread` 4, `git` 3, `glob` 1, `grep` 2, `sp06` 4, `testrunner` 9, `webfetch` 1. Sidecar shape confirmed, e.g. `testdata/corpora/toolout/fileread/read-auth-ts.txt.meta.json` → `{"tool":"Read","path":"src/auth.ts"}`. |
| 85 | H: "-v2" variants in the `fileread` and `sp06` groups | **OK** | `fileread/read-auth-ts-v2.txt`, `fileread/read-catalog-ts-v2.txt`; `sp06/fileread-auth-v1..v4.txt` (so `-v2` is present in both, though `sp06` runs v1–v4). |
| 86 | H: `test/dedup` exists with `testrunnerGainFloor` = 1.25 | **OK** | `test/dedup/dedup_test.go:34`: `const testrunnerGainFloor = 1.25`, asserted at `:67`. Files: `dedup_test.go`, `report.go`. It is a declared composition root (`tools/devtool/importrules.go:41`). |
| 87 | H: `internal/testutil` `FakeClock` | **OK** | `internal/testutil/clock.go:24` `type FakeClock struct`, `:38` `NewFakeClock(t0 time.Time) *FakeClock`, `:43` `Now()`, `:51` `Since(t)`, `:57` `Advance(d)`; `Epoch` at `:10`; satisfies `core.Clock` (`:30`). Also useful and unmentioned by the plan: `testutil.NewProject(t, opts…)` (`project.go:163`) and `(*Project).Store(t) store.Store` (`project.go:270`), which opens a real store with the project's cfg/log/clock and registers `Close` — the shortest path to the plan's "real store on `t.TempDir()`" rows. |
| 88 | H: `pgregory.net/rapid` is available for property tests | **OK** | `go.mod:14`: `pgregory.net/rapid v1.1.0`, in the **direct** require block. |
| 89 | I: `bench-hotpath` flags `--iterations --hook --warm-daemon --json`, and the gate fails the build | **OK** | `test/bench/hotpath/main.go:109-126` (plus `--project` and `--under-coload`); `--hook` accepts only `""`/`observe-tool` (`:139-147`); `runMain` returns 1 when `report.GateFailed()` (`:182-185`). `tools/devtool/benchhotpath.go:12-20` forwards args to `go run ./test/bench/hotpath`. CI's `bench-gate` runs exactly the plan's command (`.github/workflows/ci.yml:185`). |
| 90 | I: `-timeout=30m` convention | **OK** | `tools/devtool/test.go:37`: `const wholeTreeTestTimeout = "30m"`, applied by `taskTest` (`:26`) and `taskTestRace` (`:41`); CI matches (`.github/workflows/ci.yml:122,124`). Note `taskTest` runs `taskFmtCheck(nil)` first (`test.go:23`). |
| 91 | I: `test/e2e` harness builds and drives the real binary | **OK** | `test/e2e/harness.go:40` `func Build(t *testing.T) string` (once per process, `sync.Once`) and `:102` `func Run(t *testing.T, bin string, args []string, stdin []byte, env map[string]string) (stdout, stderr []byte, code int)`. Example: `TestE2EHookRoundTrip` (`daemon_e2e_test.go:236`). It asserts the **WAL**, not the store, precisely because nothing wires a store today (see #1). |
| 92 | K/L: manifest SessionEnd timeout 20 s; §12.1 quote; §3.3 file list; §0 amendment rule; §6.4 75% floor; §11.6; §10 5–8 commit band | **OK** | `internal/pluginmanifest/manifest.go:101-108` (`SessionEnd`→`flush`, timeout **20**; `SubagentStop`→`observe stop --subagent`, 10; `binaryRef = "${CLAUDE_PLUGIN_ROOT}/bin/qompack"` at `:26`), mirrored byte-for-byte in `plugin/hooks/hooks.json`. §12.1 at `plans/00-ARCHITECTURE.md:2714-2717` reads "L0 and L1 keep running (observe, chunk, store, sketches, DAG, verbatim capture, elimination records — …)". §3.3 at `:441-490` lists `state/` ("daemon-persisted: bocd.json, scheduler.json, frontier.json" — `observer.json` is an addition to that enumeration), `sketches/tried.bloom`, `index/segments.jsonl`. §0 at `:23-26`: "you do not work around it. You open a branch `arch/<short-reason>` off `develop`, change §5, get it merged, and rebase." §6.4 at `:2294` ("everything else 75%") and `:2298-2303` ("A drop below the floor fails the **`cover`** job (§8), not `verify`"). Rules W-1/W-3 at `:2218-2226`. §10 at `:2481`: "**Commit count: 5–8 commits per subplan.**" |

---

## Section E — verbatim

### `internal/cli/daemon.go`, the ~25 lines around `daemon.New(` (lines 71–95)

```go
	faultCorruptConfigIfNeeded(root)

	cfg, _, cfgErr := LoadConfigAndReport(config.Env{
		ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
	}, log, reg)
	if cfgErr != nil {
		cfg = config.Defaults()
		log.Loud("daemon: could not load configuration, using defaults", "err", cfgErr.Error())
	}

	opts := daemon.NewOptions(root, cfg)
	opts.Log = log
	opts.Metrics = reg
	opts.Clock = core.SystemClock() // §6.1's own "connection deadlines are always real wall-clock time" rule applies to the daemon's own lifecycle clock too.

	d, err := daemon.New(opts)
	if err != nil {
		log.Loud("daemon: could not construct", "err", err.Error())
		return nil
	}

	if *foreground {
		fmt.Fprintf(errw, "qompack daemon: starting for project %s\n", root)
	}
```

Error handling: every failure path in `runDaemon` **returns `nil`** after a `log.Loud` /
`fmt.Fprintln(errw, …)` — a malformed flag (`:35`), an unresolvable project root (`:43-45`),
`paths.EnsureLayout` failure (`:48-50`), a config load failure (falls back to `config.Defaults()`,
`:76-79`) and `daemon.New` itself (`:87-90`). §2.3's exit-0 rule is absolute here, which is why the
plan's "log Loud and carry on" shape for `WireObserver` is right.

`daemon.NewOptions` (`internal/daemon/options.go:58-68`) fills `ProjectRoot`, `Cfg`, `Log`,
`Metrics`, `Clock` and `Sketches` — **not** `Store`, `Graph`, `Ledger`, `Grammar`, `Sched` or
`Checkpoints`.

### `internal/cli/hookclient.go` — `rawExtras` and the `--subagent` dispatch (post-amendment)

```go
// subagentExtras is `observe stop --subagent`'s Request.Raw shape. Agent is omitempty so an
// unnamed subagent marshals to exactly {"subagent":true} — byte-for-byte what shipped before the
// name was resolved here, which is what keeps an already-installed daemon's decodeSubagent working
// against a newer hook client and vice versa.
type subagentExtras struct {
	Subagent bool   `json:"subagent"`
	Agent    string `json:"agent,omitempty"`
}

// subagentNameKeys are the payload keys a subagent's name may arrive under, in priority order.
// They are read from hookio.Event.Extra, which is where ReadEvent files every top-level key
// Event's own struct tags do not claim.
var subagentNameKeys = []string{"subagent_type", "agent_name", "agent"}

// subagentName resolves the agent's name out of ev.Extra, or "" when no key holds one.
//
// It MUST run here, in the hook-client process. hookio.Event.Extra is tagged `json:"-"`, so
// ReadEvent fills it on this side of the IPC boundary and ipc.EncodeRequest then drops it: a
// daemon-side reader of e.Extra sees an empty map in production, and every subagent capture would
// be named "subagent". Request.Raw is the field that does cross, so the resolution happens where
// the data still exists and the ANSWER is what travels.
//
// A key whose value is not a non-empty JSON string — a number, an object, an empty string, or the
// key simply being absent — falls through to the next candidate rather than winning with a
// nonsense name.
func subagentName(ev hookio.Event) string {
	for _, k := range subagentNameKeys {
		var name string
		if err := json.Unmarshal(ev.Extra[k], &name); err == nil && name != "" {
			return name
		}
	}
	return ""
}

func rawExtras(op ipc.Op, args []string, ev hookio.Event) json.RawMessage {
	switch op {
	case ipc.OpObserveStop:
		if hasFlag(args, "--subagent") {
			b, _ := json.Marshal(subagentExtras{Subagent: true, Agent: subagentName(ev)})
			return b
		}
	case ipc.OpCheckpoint:
		if ev.Trigger != "" {
			b, _ := json.Marshal(map[string]string{"trigger": ev.Trigger})
			return b
		}
	}
	return nil
}

// hasFlag reports whether flag appears verbatim among args.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
```

Attached at `hookclient.go:241-244` (unchanged):

```go
		req := ipc.Request{
			Op: spec.op, Session: ev.SessionID, TS: core.UnixMilli(ts),
			Reply: spec.reply, Event: &ev, Raw: rawExtras(spec.op, args, ev),
		}
```

Daemon side, `internal/daemon/handlers.go` (post-amendment) — `resolveEvent` now restores `Extra`:

```go
func resolveEvent(req ipc.Request) *hookio.Event {
	ev := &hookio.Event{SessionID: req.Session}
	if req.Event != nil {
		cp := *req.Event
		if cp.SessionID == "" {
			cp.SessionID = req.Session
		}
		ev = &cp
	}
	ev.Extra = restoredExtra(ev.Extra, req.Raw)
	return ev
}

// restoredExtra merges raw's top-level keys into extra, returning extra unchanged when raw is not
// a JSON object (nil, a null, an array, a scalar, or malformed — all of which a corrupt spool line
// can produce, and none of which is an error worth failing a hook over).
//
// A key already present in extra WINS: the Event's own value is what an in-process caller set
// deliberately, while raw is a reconstruction. The merge always allocates a fresh map rather than
// writing into extra, because resolveEvent copies the Event by VALUE — the copy shares the
// caller's map header, so writing through it would mutate a request the caller still holds.
func restoredExtra(extra map[string]json.RawMessage, raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 {
		return extra
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return extra
	}
	merged := make(map[string]json.RawMessage, len(extra)+len(m))
	for k, v := range m {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}

// decodeSubagent reads observe.stop's {"subagent":true} marker out of req.Raw. A missing or
// unparseable Raw reports false — the ordinary, non-subagent Stop hook.
func decodeSubagent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var payload struct {
		Subagent bool `json:"subagent"`
	}
	_ = json.Unmarshal(raw, &payload)
	return payload.Subagent
}
```

`--subagent` is **not** a declared `flag.FlagSet` flag anywhere; it is matched verbatim by `hasFlag`
against the raw argv. Daemon-side, `e.Extra["agent"]` is a `json.RawMessage` holding a **JSON-encoded
string**, so `internal/observer`'s own `subagentName` must `json.Unmarshal` it (the plan's
`Extra["agent"] = "\"code-reviewer\""` fixture is correct), and the restored map also carries
`"subagent"` (and `"trigger"` for `checkpoint`).

### `internal/store/put.go` — `canonOptions` (post-amendment), the rule `verbatimOptions()` must satisfy

```go
func (s *FSStore) canonOptions(o PutOptions) canon.Options {
	cc := s.cfg.Store.Canonicalize
	opts := canon.Options{
		KeepDeltas: o.KeepRaw,
		MinHash: sketch.MinHashOptions{
			Enabled:          cc.MinHash.Enabled,
			Permutations:     cc.MinHash.Permutations,
			NearDupThreshold: cc.MinHash.NearDupThreshold,
		},
	}
	if cc.Enabled {
		opts.Strip = make([]canon.Class, 0, len(cc.Strip))
		for _, c := range cc.Strip {
			opts.Strip = append(opts.Strip, canon.Class(c))
		}
	}
	// A caller that supplied its own canon.Options wins: PutOptions.Canon is the per-call override
	// the §5.8 shape provides for exactly this.
	if o.Canon.Strip != nil {
		opts.Strip = o.Canon.Strip
		if !o.Canon.MinHash.Enabled {
			opts.MinHash.Enabled = false
		}
	}
	return opts
}
```

### `internal/daemon/daemon.go` — the hoist (post-amendment)

```go
	// The monitor is constructed BEFORE the bind loop, not after, so svc.Mode can be assigned
	// before any bind body runs. …
	statePath := filepath.Join(paths.Of(o.ProjectRoot).State, "contract.json")
	monitor := contract.NewMonitor(o.Log, o.Metrics, statePath)
	for _, a := range contract.StandardAssertions() {
		_ = monitor.Register(a)
	}
	svc.Mode = monitor.Mode

	for _, bind := range o.binds {
		bind(svc)
	}
	DeclareProducers(svc)
```

The registry and idle controller are still built **after** this block, so `RegisterObserverIdleWork`
must still run after `daemon.New` returns.

### Exact hook subcommand spellings (`internal/cli/hooks.go:9-42`)

| Subcommand (argv) | ipc.Op | Reply? | Deadline const | Manifest event / timeout |
|---|---|---|---|---|
| `observe tool` | `ipc.OpObserveTool` | no | hot-path `AckDeadlineMs`, floor 8 ms | `PostToolUse`, matcher `*`, 5 s |
| `observe prompt` | `ipc.OpObservePrompt` | yes | `promptReplyDeadline` = 250 ms | `UserPromptSubmit`, 5 s |
| `observe stop` (`--subagent` optional) | `ipc.OpObserveStop` | no | hot-path `AckDeadlineMs` | `Stop` 5 s / `SubagentStop` 10 s |
| `session-start` | `ipc.OpSessionStart` | yes | `sessionStartReplyDeadline` = 10 s | `SessionStart`, 15 s |
| `checkpoint` | `ipc.OpCheckpoint` | yes | `checkpointReplyDeadline` = 15 s | `PreCompact`, 20 s |
| `flush` | `ipc.OpFlush` | yes | `flushReplyDeadline` = 15 s | `SessionEnd`, 20 s |

Deadline constants: `internal/cli/hookclient.go:25-36`. Manifest: `internal/pluginmanifest/manifest.go:101-108`,
rendered to `plugin/hooks/hooks.json` as `${CLAUDE_PLUGIN_ROOT}/bin/qompack <args>`.
`session-start` is the one hook with a body of its own (`runSessionStart`, `sessionstart.go`); the
other five go through `doHook(hookSpec{…})`.

---

## Section I — verbatim

### `devtool` task registry (`tools/devtool/main.go:26-48`) — 21 tasks, no `verify`

```go
var tasks = map[string]func(args []string) error{
	"fmt":                   taskFmt,
	"fmt-check":             taskFmtCheck,
	"lint":                  taskLint,
	"vet":                   taskVet,
	"build":                 taskBuild,
	"build-all":             taskBuildAll,
	"test":                  taskTest,
	"test-race":             taskTestRace,
	"cover":                 taskCover,
	"bench":                 taskBench,
	"bench-compare":         taskBenchCompare,
	"bench-hotpath":         taskBenchHotpath,
	"replay":                taskReplay,
	"plugin-validate":       taskPluginValidate,
	"fsck":                  taskFsck,
	"ci-local":              taskCILocal,
	"gen-config-docs":       taskGenConfigDocs,
	"gen-contract-fixtures": taskGenContractFixtures,
	"gen-fixtures":          taskGenFixtures,
	"install-hooks":         taskInstallHooks,
	"check-commit-msg":      taskCheckCommitMsg,
}
```

Dispatch is single-task (`main.go:62`): `task, rest := args[0], args[1:]`.

### `ci-local` (`tools/devtool/cilocal.go:12-23`)

```go
// ciLocalSteps runs in exactly the order the implementation spec's task table gives:
// fmt-check -> lint -> vet -> build -> test -> cover -> plugin-validate -> gen-config-docs (check mode).
var ciLocalSteps = []ciLocalStep{
	{"fmt-check", taskFmtCheck, nil},
	{"lint", taskLint, nil},
	{"vet", taskVet, nil},
	{"build", taskBuild, nil},
	{"test", taskTest, nil},
	{"cover", taskCover, nil},
	{"plugin-validate", taskPluginValidate, nil},
	{"gen-config-docs", taskGenConfigDocs, []string{"--check"}},
}
```

### What actually covers gofumpt / lint / vet / nomagic / import-graph / test-only-dep / build

gofumpt → `devtool fmt-check` (`fmt.go:68`, `gofumpt -l`, fails on any listed file) and `devtool fmt`
(`fmt.go:57`, `gofumpt -l -w`). `go vet ./...` → `devtool vet`. `go build ./...` → `devtool build`.
Everything else is a **sub-check of `devtool lint`** (`tools/devtool/lint.go:26-35`):

```go
var lintSubchecks = []lintSubcheck{
	{"golangci-lint", runGolangciLintCheck},
	{"nomagic", runNomagicCheck},
	{"importgraph", runImportGraph},
	{"testdeps", runTestDeps},
	{"bindeps", runBinDeps},
	{"sleepcheck", runSleepCheck},
	{"stubskips", runStubSkips},
	{"runpatterns", runPlanRunPatterns},
	{"docmarkers", runPlanMarkers},
}
```

plus `coveragefloors`, appended by `planchecks.go`'s `init()`. `devtool lint --only=a,b,c` restricts
the run (`lint.go:50-71`; CI's `security` job uses `--only=importgraph,testdeps,bindeps`).

The nearest local equivalent of the plan's `verify` is therefore four separate commands:

```
go run ./tools/devtool fmt-check
go run ./tools/devtool lint
go run ./tools/devtool vet
go run ./tools/devtool build
```

Two `lint` sub-checks react to SP-08 landing: `stubskips` (`stubskips.go:30`, `ruleW1Msg =
"behaviour: implementation is a stub (Rule W-1)"`) stops reporting the observertest skip on its own,
and `runpatterns` (`planchecks.go:598`) begins resolving `plans/V3-SP-08-observer-l0.md`'s `-run`
patterns against `internal/observer`'s declared test funcs once the plan document is in scope (scope
is derived from `landedSubplans` — see MISMATCH #5). All the plan's `-run` patterns use bare `|`,
which is what that check requires.

### `bench-gate` and `replay-gate` are CI jobs, not devtool tasks (`.github/workflows/ci.yml`)

```yaml
  bench-gate:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version: '1.26.6', cache: true }
      - run: go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json "bench-${{ matrix.os }}.json"
      - uses: actions/upload-artifact@v4
        with: { name: "bench-${{ matrix.os }}", path: "bench-${{ matrix.os }}.json" }

  replay-gate:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      # … checkout, setup-go, capture the PR body for the sign-off scan …
      - run: >
          go run ./tools/devtool replay
          --corpus testdata/sessions/synthetic
          --baseline testdata/baseline/phase0.json
          --phase 0
          --growth testdata/golden/eval/growth/stats-growth.json
          --sketch testdata/golden/eval/growth/health.json
          --signoff "$RUNNER_TEMP/pr-body.md"
          --max-cpu 2m
          --ci
```

The other jobs: `verify` (`ci.yml:14` — the four commands above plus the attribution-trailer grep and
the conventional-commit scan), `lint-windows` (`:75`), `test` (`:96` — `go test -race -timeout=30m ./...`
on Linux/macOS with `CGO_ENABLED=1`, `go test -count=2 -timeout=30m ./...` on Windows), `cover`
(`:126`, `go run ./tools/devtool cover`), `crossbuild` (`:137`, `devtool build-all`), `plugin-validate`
(`:224`, plus `git diff --exit-code -- plugin/`), `security` (`:233` — govulncheck +
`devtool lint --only=importgraph,testdeps,bindeps` + a `net/http|net/url|crypto/tls` and `os/exec`
allowlist grep), `docs` (`:254`, `devtool gen-config-docs --check`). `devtool bench-compare` is
deliberately **not** in CI (`ci.yml:152-172`): it is run locally on a quiet machine against
`testdata/bench-baseline.txt`.

### `bench-hotpath` flags (`test/bench/hotpath/main.go:106-126`)

```go
	fs := flag.NewFlagSet("bench-hotpath", flag.ContinueOnError)
	fs.SetOutput(errw)
	f := flags{}
	fs.IntVar(&f.iterations, "iterations", defaultIterations, "number of B-A/B-D spawn samples")
	fs.StringVar(&f.hook, "hook", "observe-tool", "hook subcommand to spawn for B-A/B-D (only observe-tool is supported today)")
	fs.BoolVar(&f.warmDaemon, "warm-daemon", false, "pre-populate the daemon before measuring: a small hot-path observe.tool tranche plus admin.ping traffic for the rest (FIX ROUND 2, N-1)")
	fs.StringVar(&f.jsonPath, "json", "", "write the out.json artifact to this path (omit to skip)")
	fs.StringVar(&f.project, "project", "", "use this directory as the temp project instead of creating one")
	fs.BoolVar(&f.underCoload, "under-coload", false,
		"declare that this run shares its host with unrelated concurrent work (e.g. the whole-tree `go test ./...`), "+
			"so the wall-clock B-E row is reported instead of gated; the CPU-time B-E gate is unaffected")
```

`--hook` accepts only `""` or `observe-tool` (`main.go:139-147`); anything else is a usage error.
Exit code is 1 when `report.GateFailed()` (`main.go:182-185`), which is what makes `bench-gate` a gate.

### The `nomagic` checker (`tools/lint/nomagic/`)

```go
// tools/lint/nomagic/literals.go
var forbiddenFloats = []float64{0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}

var forbiddenInts = []int64{20000, 12000, 10000, 8000, 2048, 1024, 4096, 16384, 300, 120, 450}
```

Run as `go run ./tools/lint/nomagic ./...` (`lint.go:43-45`). Exemptions: `internal/config/defaults.go`,
`*_test.go`, and an `//nomagic:allow <reason>` comment — with `allowMarker = "nomagic:allow"`
(`nomagic.go:105`) and a **bare** marker being its own diagnostic (`nomagic.go:49`:
`"bare //nomagic:allow with no reason; state why this literal is exempt (D11, §11.6)"`).
The one existing annotation in `internal/observer` is `tombstone.go:14`:

```go
const bytesPerKB = 1024 //nomagic:allow binary byte-unit divisor for size rendering, not store.chunk.min
```

### The import-graph layer check (`tools/devtool/importrules.go`, `importgraph.go`)

The allow-table is a **Go table**, not a read of `plans/00-ARCHITECTURE.md` §3.2:

```go
// tools/devtool/importrules.go:6
var foundation = []string{"core", "paths", "config", "logging", "obs"}

// :34-47 — may import anything; nothing may import them
var compositionRoots = map[string]bool{
	"daemon": true, "cli": true, "commands": true, "testutil": true, "cmd/qompack": true,
	"test/e2e": true, "test/guards": true, "test/dedup": true, "test/bench/hotpath": true,
	"test/replay": true, "test/integration": true,
}

// :90
	"observer": {"hookio", "store", "chunk", "canon", "sketch", "dag", "grammar", "negknow", "tokens"},
```

Every non-foundation package additionally gets `foundation` appended at check time
(`effectiveAllow`). "A package that exists on disk but is absent from both `allow` and
`compositionRoots` is an error: new packages must be declared here, which forces an architecture
amendment" (`importrules.go:49-52`). `internal/daemon` is a composition root, so
`internal/daemon/observer_ops.go` may import `observer`, `symbols`, `scheduler` and `contract` freely.

### The commit-message check (`tools/devtool/checkcommitmsg.go`)

```go
var subjectRE = regexp.MustCompile(`^(feat|fix|docs|test|refactor|perf|build|ci|chore|revert)(\([a-z0-9/_.,-]+\))?: .{1,64}$`)

var forbiddenTrailerRE = regexp.MustCompile(`(?i)co-authored-by|signed-off-by|generated with`)

var refsRE = regexp.MustCompile(`(?m)^Refs:`)

const robotEmoji = "🤖"

const maxBodyLineLen = 100
```

Rules enforced by `validateCommitMsg` (`:58-95`), in order: non-empty; subject must not end in `.`;
subject matches `subjectRE`; **every** line (subject included) free of the attribution trailers and
of `🤖`; every **body** line ≤ 100 runes; and a `Refs:` footer is required when the type is `feat` or
`fix`. Usage is `devtool check-commit-msg <commit-msg-file>` (installed as the `commit-msg` hook by
`devtool install-hooks`). CI re-checks the same grammar over `git log --format=%s` and greps
`git log --format=%B` for the trailers (`.github/workflows/ci.yml:25-48`), including a separate
trailing-period check.

### Coverage floor per package

`plans/OWNERS.tsv:45` → `observer<TAB>SP-08<TAB>75<TAB>OnToolUse` (package, owner, floor, stub probe).
`tools/devtool/cover.go` reads that file; `landedSubplans` (`cover.go:40-48`) currently holds
`{SP-01 … SP-07}` and gates whether a floor is live, and `cover_test.go:263-267` fails if `SP-08`
appears in it. `plans/00-ARCHITECTURE.md:2298-2303`: "A drop below the floor fails the **`cover`** job
(§8), not `verify`… The floors themselves are data, in `plans/OWNERS.tsv` — that file, not this table,
is what `cover` reads."

### `test/e2e` harness

`test/e2e/harness.go`:

```go
func Build(t *testing.T) string
func Run(t *testing.T, bin string, args []string, stdin []byte, env map[string]string) (stdout, stderr []byte, code int)
```

`Build` compiles `./cmd/qompack` once per test binary (`sync.Once`, torn down in `TestMain`); `Run`
spawns it with `cmd.Dir = filepath.Dir(bin)` — deliberately **not** the repository — feeds `stdin`,
and returns the exit code rather than asserting it.

Per-file helpers: `e2eEnv(p *testutil.Project) map[string]string` (`daemon_e2e_test.go:122`),
`e2eStatus(t, root) daemon.StatusSnapshot` (`:134`), `e2eWaitDaemonUp(t, root)` (`:225`),
`e2eShutdownIfReachable(t, root)` (`faultinject_test.go:172`), and the payload builders
`sessionStartPayload` (`:549`), `sessionStartFor` (`:553`), `sessionEndPayload` (`:562`),
`observeToolPayload` (`:569`), `requireParsesAsOutput` (`:582`), `e2eCountFileLines` (`:535`).

Example (`daemon_e2e_test.go:236-274`, abridged):

```go
func TestE2EHookRoundTrip(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	env := e2eEnv(p)

	stdout, stderr, code := Run(t, bin, []string{"session-start"}, sessionStartPayload(t, p.Root), env)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	requireParsesAsOutput(t, stdout)

	e2eWaitDaemonUp(t, p.Root)

	const n = 50
	for i := 0; i < n; i++ {
		stdout, stderr, code := Run(t, bin, []string{"observe", "tool"}, observeToolPayload(t, p.Root), env)
		require.Equal(t, 0, code, "observe tool #%d: stderr:\n%s", i, stderr)
		requireParsesAsOutput(t, stdout)
	}

	walPath := filepath.Join(paths.Of(p.Root).Spool, "wal-"+string(e2eSession)+".ndjson")
	// … require.Eventually on the WAL line count, then e2eStatus for the live session
}
```

Other e2e files: `hooks_test.go` (`TestE2E_AllSixHooksExitZero:57`, `TestMain:20`),
`faultinject_test.go` (`TestHooksExitZeroUnderFaults:58` — the shape
`TestE2E_HooksExitZeroUnderFaultInjection` should follow), `store_test.go`, `v1_integration_test.go`.

### `test/bench/hotpath`

Files: `main.go`, `delivery.go`, `measure.go`, `payload.go`, `process.go`, `report.go`, `transport.go`.
It builds the real binary (`process.go:58`, `go build -o … ./cmd/qompack`), starts a real daemon child,
optionally warms it, measures the spawn floor / B-A / B-D / B-E, reads B-B off the daemon's own
`status` op, and writes `Report` as JSON. `payload.go:81-91` already synthesizes per-tool hook payloads
(`{"file_path":…}` for Read, `{"command":"go test ./..."}` for Bash, `{"pattern":…,"path":…}` for Grep)
— the same shapes the Phase-1 `eventsFor` needs. It is a declared composition root
(`importrules.go:44`).
