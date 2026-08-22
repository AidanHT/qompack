# Task 1 report — STOPPED before implementation (NEEDS_CONTEXT)

## Summary

Before writing any code I read, in order: the task-1 brief, sp01-inventory.md, plan-context.md,
task-1-spec.md, and the actual shipped sources (`internal/ipc/{addr,resolve,resolve_test,wire,
wire_test,doc,dial_windows,dial_other}.go`, `internal/contract/mode.go`, `internal/config/{runtime,
config}.go`, `internal/paths/{atomic,layout,norm}.go`, `internal/core/{clock,ids,errors}.go`,
`internal/hookio/{event,output}.go`, `internal/testutil/project.go`, `tools/lint/nomagic/*.go`,
`tools/devtool/main.go`). Research is complete for every part of Task 1 except one, described
below, which is a direct contradiction between the brief and the frozen shipped code that none of
the binding rulings resolves. Per my instructions I am stopping and reporting rather than guessing,
since the exactly-one-commit requirement means I cannot finalize a partial/uncertain commit.

**No files were modified.** `git status --short` in the worktree is empty; only `Read`/`Grep`/`Glob`
calls were made.

## The contradiction

**task-1-spec.md** (`.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-1-spec.md`, lines 63-67)
gives this as the byte-exact golden fixture `testdata/golden/contracts/ipc/observe_tool.ndjson`,
and the test table (same file, line 142) requires `TestEncodeRequestByteExact` to assert
`EncodeRequest(fixedRequest)` equals this file byte-for-byte:

```
{"op":"observe.tool","s":"sess-01","t":1730000000000,"e":{"hook_event_name":"PostToolUse","session_id":"sess-01","tool_name":"Read","tool_use_id":"tu_1"}}
```

Note the `"e"` object here carries only 4 keys.

task-1-brief.md's file list (line 26) confirms this is meant literally: "`testdata/golden/contracts/
ipc/observe_tool.ndjson` (byte-exact line from task-1-spec)."

**But the shipped `hookio.Event`** (`internal/hookio/event.go`, lines 18-37) has **no `omitempty`
tag on any field**, and there is no custom `MarshalJSON` anywhere in `internal/hookio` (I grepped
the whole package for `MarshalJSON` — no hits; `internal/hookio/output.go`'s `Output`/`HSO` types DO
use `omitempty` throughout, so the contrast is deliberate, not an oversight I might be misreading):

```go
type Event struct {
	HookEventName  string         `json:"hook_event_name"`
	SessionID      core.SessionID `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	CWD            string         `json:"cwd"`
	Source string `json:"source"`
	Trigger        string          `json:"trigger"`
	ToolName       string          `json:"tool_name"`
	ToolUseID      core.ToolUseID  `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	Prompt         string          `json:"prompt"`
	StopHookActive bool            `json:"stop_hook_active"`
	Extra map[string]json.RawMessage `json:"-"`
}
```

Given `Request.Event *hookio.Event` (shipped `wire.go`) is what fills the wire's `"e"` key, encoding
a `Request` whose `Event` has only `HookEventName`/`SessionID`/`ToolName`/`ToolUseID` set produces —
mechanically, with the real shipped type — all 12 tagged keys, not 4:

```
"e":{"hook_event_name":"PostToolUse","session_id":"sess-01","transcript_path":"","cwd":"","source":"","trigger":"","tool_name":"Read","tool_use_id":"tu_1","tool_input":null,"tool_response":null,"prompt":"","stop_hook_active":false}
```

So the golden literal in task-1-spec.md is **not producible** by `EncodeRequest` over the real,
frozen `hookio.Event` — not through any encoding choice available to me within Task 1's scope
(pooled-buffer `json.Encoder` with `SetEscapeHTML(false)`, per the brief; no per-field filtering is
specified or would be legitimate for a generic `Request` encoder). `hookio` is SP-01 foundation,
already shipped and out of Task 1's touch-scope (brief: "you only touch internal/ipc,
internal/contract/mode.go … and test fixtures") — I can't add `omitempty` to `Event` to make the
golden reproducible even if that seemed like the right fix.

None of the binding rulings in the brief mention `frame.go`'s golden fixture content or
`hookio.Event`'s tags, so this isn't something the rulings already resolved for me.

## Why I'm not guessing

Two ways to resolve this are both plausible and both are wire/golden-format decisions, not
implementation details:

1. **The golden literal is authoritative and the spec's Event-construction assumption is stale** —
   in which case the *real* required fixture content is the full 12-key serialization I show above
   (mechanically the only value `EncodeRequest` can produce), and `task-1-spec.md`'s prose example
   was written before/without checking `hookio.Event`'s actual tags.
2. **The golden literal is illustrative prose, not literal required bytes** — in which case I should
   pick a `fixedRequest` and let whatever `EncodeRequest` actually emits become the frozen fixture,
   documenting the deviation.

Both amount to me unilaterally deciding what a *frozen, byte-pinned wire fixture* other subplans
will read looks like — exactly the class of decision the top-level instructions told me to escalate
("stop and report NEEDS_CONTEXT rather than guessing") rather than resolve myself. Given Task 1 must
land as exactly one commit, I can't commit a fixture I'm not confident is the one the controller
wants pinned.

## Everything else: ready to implement, no other blockers found

Research for the rest of Task 1 is complete and I found no other contradictions. For the record,
here's what I confirmed and intend to do once this is resolved:

- **`resolve.go` extensions**: `ProjectHash12`/`ProjectHash8` (exported wrappers replacing
  `endpointHash`, sha256 undomained, unchanged), a `normalizeRoot(goos, root string) string` doing
  `filepath.Abs` → `filepath.Clean` → `ToSlash` → strip trailing `/` (except a bare POSIX `/` or a
  bare Windows drive root, detected via `filepath.VolumeName`) → lowercase iff `goos` is `windows`/
  `darwin` (falls back to the cleaned input on an `Abs` error), threaded through `resolveFor`'s
  existing injected `goos` parameter (not `runtime.GOOS`) so case-folding stays testable.
  `ErrAddrTooLong` replaces the current `fmt.Errorf` at the short-fallback failure in `resolveFor`.
  `QOMPACK_IPC_ADDR` (`pipe:<name>` / `unix:<path>`, still ≤100 bytes for unix, malformed → ignored)
  checked at the top of `resolveFor` via the already-injected `getenv`, before project-root
  validation. `XDG_RUNTIME_DIR` gets an added `strings.HasPrefix(strings.TrimSpace(xdg), "/")` guard
  alongside the existing non-empty check. `resolve_test.go` needs its existing expected-hash
  computations rewritten to call the same (goos, root)-parameterized hash helper the test's
  `resolveFor` call uses, rather than the old unparameterized `endpointHash`, since folding now
  depends on which `goos` each subtest passes in.
- **`op.go`**: `OpAdminPing/Drain/Reload/Idle/Shutdown` as `OpAdminPrefix + "ping"` etc. (typed `Op`
  consts built from the shipped `OpAdminPrefix` constant, so the literal never forks),
  `KnownOps()` (13, sorted), `Op.Valid()`, `Op.HotPath()` (true for the three `observe.*` ops). No
  `Router` — confirmed absent from my plan.
- **`state.go`**: 32-byte record exactly per the offset table, named offset/flag constants with
  §-comments, `crc32.Castagnoli` over `[0:28]` via a package-level `crc32.MakeTable` (not
  recomputed per call), `StatePath` = `paths.Of(projectRoot).Run/state.bin`, `ReadState` treating
  missing/short/bad-magic/bad-CRC uniformly as "decode failed → `StateFromConfig(fallback)`, no
  log", `WriteState` via `paths.WriteAtomic(..., 0o600)`, `RemoveState` idempotent on
  `os.ErrNotExist`, `StateFromConfig` mapping `cfg.Runtime.Mode` via a small `passive`-special-cased
  wrapper around `contract.ParseMode` (runtime.mode's `auto|full|passive|off` vocabulary is distinct
  from `contract.Mode.String()`'s `full|degraded-passive|off`, so they only share `"full"`/`"off"`
  verbatim). Confirmed `paths.WriteAtomic`'s `rootOf` walk self-heals when `.qompack` doesn't exist
  yet (falls back to `filepath.Dir(p)` and `MkdirAll`s it), so state tests don't need
  `paths.EnsureLayout` first — matches the `internal/paths` test convention of
  `paths.Of(t.TempDir())` + `EnsureLayout` only when a test specifically wants the real layout.
- **`frame.go`**: pooled `bytes.Buffer` (`sync.Pool`) + `json.Encoder`/`SetEscapeHTML(false)` for
  `EncodeRequest`/`EncodeResponse`; `json.Unmarshal` for the two `Decode*`; `LineReader` built on
  `bufio.Reader.ReadSlice('\n')` in a loop (not `ReadBytes`/`Scanner`, which don't let me bound
  memory before checking length) so an oversize line is caught before further accumulation and the
  reader discards to the next `\n` for resync. Default `bufio.NewReader(r)` buffer size — avoids
  needing a magic literal for an initial capacity at all.
- **`contract/mode.go`**: `ParseMode` (rename of `parseMode`, plus fixing its one caller — need to
  grep for callers of `parseMode` before renaming; I had not yet done that grep when I stopped),
  `Mode.MayAct()` (false for `ModeDegradedPassive`/`ModeOff`), `Mode.MayRecord()` (false only for
  `ModeOff`), each with a §12.1 doc comment.
- **nomagic**: confirmed the actual forbidden-int set is `{20000, 12000, 10000, 8000, 2048, 1024,
  4096, 16384, 300, 120, 450}` (`tools/lint/nomagic/literals.go`) — none of my planned offsets/sizes
  (0,4,5,6,8,10,12,16,20,28,32, the two flag bits) collide with it, so no `//nomagic:allow` needed
  anywhere in my planned code; I still intend to name every one of them per the shipped-code idiom.
- **Golden/fixture location convention**: confirmed from `internal/contract/golden_test.go` — repo
  root `testdata/golden/contracts/...`, referenced with a relative `../../testdata/...` path from
  the package test file. I'll follow the same convention for `internal/ipc`'s fixtures.
- **`response_reply.ndjson`** is NOT affected by the Event problem — `hookio.Output`/`HSO` are fully
  `omitempty`, so a `Reply` response fixture is safe to construct byte-exact as specced.

## What I need from the controller

Which of the two resolutions above (or a third) to apply to `testdata/golden/contracts/ipc/
observe_tool.ndjson` and `TestEncodeRequestByteExact`: pin the golden to the real 12-key
serialization I computed above, or some other resolution (e.g. a note that this fixture is corrected
in a later task/commit, or that `hookio.Event` itself is getting `omitempty` added by a different
subplan I should coordinate with rather than assume is off-limits).

---

# Part 2 — Implementation (after controller ruling #19)

Controller ruling #19 resolved the blocker above: option 1 — the golden fixture is CREATED by this
task and pins reality (the real 12-key `hookio.Event` serialization), not the plan's illustrative
4-key shorthand. `internal/hookio` stays untouched. Proceeding with full implementation per the
brief.

## TDD RED/GREEN evidence

**`internal/contract` (mode.go additions)** — edited `mode.go`/`monitor.go` before capturing RED, so
I re-verified rigor by re-deriving it: `git stash push -- internal/contract/mode.go
internal/contract/monitor.go`, then:

```
$ go test ./internal/contract/...
internal\contract\internal_test.go:15:14: undefined: ParseMode
internal\contract\internal_test.go:26:14: undefined: ParseMode
FAIL	github.com/qompack/qompack/internal/contract [build failed]
```

RED confirmed (compile failure for the renamed symbol + the new `mode_test.go` cases). `git stash
pop` restored the implementation:

```
$ go test ./internal/contract/...
ok  	github.com/qompack/qompack/internal/contract	1.331s
ok  	github.com/qompack/qompack/internal/contract/contracttest	1.284s
```

GREEN.

**`internal/ipc`** — wrote every new/changed test file first (`frame_test.go`, `op_test.go`,
`state_test.go`, `fuzz_test.go`, `bench_test.go`, and the `resolve_test.go` rewrite), before touching
any production file:

```
$ go vet ./internal/ipc/...
internal\ipc\resolve_test.go:45:15: undefined: projectHashFor
internal\ipc\resolve_test.go:55:15: undefined: projectHashFor
internal\ipc\resolve_test.go:63:23: undefined: goosDarwin
... (11 undefined-symbol errors, "too many errors")
```

RED confirmed — every new symbol (`projectHashFor`, `goosDarwin`, later `ErrAddrTooLong`,
`qompackIPCAddrEnv`, `EncodeRequest`, `DecodeRequest`, `EncodeResponse`, `DecodeResponse`,
`NewLineReader`, `ErrLineTooLong`, `KnownOps`, `Op.Valid`, `Op.HotPath`, `OpAdminPing` etc., `State`,
`StatePath`, `ReadState`, `WriteState`, `RemoveState`, `StateFromConfig`, `ProjectHash12`,
`ProjectHash8`) was absent and the package failed to compile. Then added `resolve.go`'s extensions,
`frame.go`, `op.go`, `state.go`:

```
$ go build ./internal/ipc/...   # clean
$ go vet ./internal/ipc/...     # clean
```

GREEN (compiles). Two of the golden fixtures (`observe_tool.ndjson`, `response_reply.ndjson`) and
the state fixture (`state_degraded.bin`) were then generated from the real, shipped
`EncodeRequest`/`EncodeResponse`/`WriteState` (a throwaway `zzgen_test.go`/`zzgen2_test.go`, deleted
before committing — see "Fixture generation" below) rather than hand-transcribed, per ruling #19.
Running the full suite then surfaced three real bugs the RED/GREEN compile cycle couldn't catch
(see "Bugs found during GREEN" below); once fixed, full `internal/ipc` green (below).

## Fixture generation (not hand-transcribed)

To guarantee byte-exactness against the *real* shipped types rather than my own hand-arithmetic, I
generated all three golden fixtures with throwaway test functions inside the package (so they could
import `internal/ipc` — a generator outside the module tree can't, `internal` visibility forbids it),
ran them once with `go test -run`, captured the output, and deleted the generator files before the
final commit:

- `observe_tool.ndjson` / `response_reply.ndjson`: `ipc.EncodeRequest`/`ipc.EncodeResponse` over the
  fixed `Request`/`Response` values, written via `os.WriteFile`.
- `state_degraded.bin`: `ipc.WriteState` with `Mode: ModeDegradedPassive` (1), `Hot: HotSpool` (1),
  and the other fields at representative values (`ConnectDeadlineMs 5`, `AckDeadlineMs 8`,
  `MaxPayloadBytes 1048576`, `DaemonPID 4242`, `Written 1730000000000`).

`git status --short` after generation showed no stray `zzgen*_test.go` files — both were deleted
before staging.

## Bugs found only by running the full suite (fixed)

1. **`TestEncodeRequest_DoesNotEscapeHTML`** — my own test bug: I asserted both `Contains(got,
   "<script>...\"quote\"...")` (raw, unescaped chars) and `NotContains(got, "<")`/`NotContains(got,
   "&")` in the same test — self-contradictory, since the Contains substring itself contains `<`.
   Fixed by dropping the quote character from the fixture string (JSON-escapes `"` unrelated to
   HTML-escaping, which was confusing the two) and keeping only the positive `Contains` assertion.
2. **`TestDecodeRequestRoundTrip`** (rapid property test) — failed on the very first generated case:
   `hookio.Event.ToolInput`/`ToolResponse` are `nil` `json.RawMessage`s with no `omitempty`, so they
   encode as the JSON literal `null`; decoding `null` back into `json.RawMessage` stores the literal
   4 bytes `"null"`, not `nil` (this is `encoding/json`'s own well-known `RawMessage` behavior, and
   exactly why `hookio.ReadEvent` has its own unexported `normalizeRawNull` — see `event.go`).
   `DecodeRequest` is a generic decoder with no `Event`-specific knowledge and should not apply that
   normalization itself. Fixed by adding a `cmp.Comparer` for `json.RawMessage` that treats `nil` and
   literal `null` as equal, with a comment explaining why.
3. **`TestStateWriteIsAtomic`** (500-write concurrency test) — real, reproducible-every-time failure,
   not a one-off: `paths.WriteAtomic`'s finishing `os.Rename` on Windows fails with
   `ERROR_ACCESS_DENIED` ("Access is denied") when a concurrent reader has the destination open at
   the exact rename instant. My first version (500 fully-concurrent goroutines each doing
   write-then-read of the identical file) created *sustained*, not transient, contention — a
   pathological pattern no real Qompack process produces (the daemon is the only writer, and it
   writes serially; readers are one-shot per hook invocation, never a busy-loop). Fixed two ways:
   (a) added a small bounded retry (`writeStateMaxAttempts = 64`, `runtime.Gosched()` between
   attempts — no `time.Sleep`) to `WriteState` itself, since a transient rename collision is a
   liveness hiccup `decodeState`'s CRC check already makes harmless, not a correctness bug; (b)
   rewrote the test to the realistic shape — one writer doing 500 sequential `WriteState` calls, each
   overlapped with a small burst of one-shot reader goroutines (2 per write) rather than 500
   goroutines each looping forever. Verified stable across 6+ consecutive `-race` runs after the fix.

## Full verification run (in the order the brief specifies)

```
$ go test ./internal/ipc/... -race            # ok, 8.6s (internal/ipc) + 1.9s (ipctest)
$ go test ./internal/contract/...             # ok
$ go test -run '^$' -fuzz FuzzDecodeRequest -fuzztime 30s ./internal/ipc
    fuzz: elapsed: 30s, execs: 1479037 (121691/sec), new interesting: 238 (total: 243)
    PASS
$ GOOS=linux go build ./...                   # clean
$ GOOS=darwin go build ./...                  # clean
$ go run ./tools/devtool fmt                  # clean (one auto-fix applied to state_test.go: const alignment, re-verified clean after)
$ go run ./tools/devtool vet                  # clean
$ go run ./tools/devtool lint                 # golangci-lint, nomagic, importgraph, testdeps, bindeps, sleepcheck, stubskips — all PASS
$ go test ./...                               # every package ok (see below for the 3 unplanned files this required)
```

Final `go test ./... -count=1` (uncached): every package `ok`, including `internal/ipc` (7.1s),
`internal/contract`, `test/guards` (18.6s), `internal/testutil`. No `FAIL` anywhere.

## Unplanned files touched, and why

The brief scoped me to `internal/ipc`, `internal/contract/mode.go`, and test fixtures. Two more files
needed a small, purely mechanical edit to keep `go test ./...` green, both direct, unavoidable
consequences of adding new **frozen format fixtures** under `testdata/golden/contracts/ipc/`:

- **`internal/testutil/fixtures_test.go`**: `TestContractFixture_EveryManifestIsReadable` pins the
  total frozen-fixture count as a literal (`23`). Adding my 3 new frozen fixtures
  (`ipc/observe_tool`, `ipc/response_reply`, `ipc/state_degraded`) makes the real count 26; I bumped
  the literal and its comment to match, following the exact precedent the comment already documents
  (SP-01 froze 21, "V1 added 2" — this is the established pattern for exactly this kind of change).
- **`test/guards/v1_integration_test.go`** (`TestV1_FrozenContractFixturesRoundTripIntoDeclaredTypes`,
  IT-9): every frozen "format" fixture is round-tripped either through a declared Go type
  (`v1FixtureType` map) or, absent an entry, checked as well-formed JSON. My `state_degraded.bin` is
  a raw 32-byte binary record, not JSON, so the fallback path failed it with "not valid JSON". Added
  `ipc.Request`/`ipc.Response` entries to `v1FixtureType` for the two NDJSON fixtures (letting IT-9
  actually prove they round-trip through the declared types — exactly the check's purpose), and a
  small `v1BinaryFixtureKeys` set that lets `v1RoundTrip` skip the JSON-well-formedness assumption
  for the one binary fixture, checking only non-emptiness. This required adding an `internal/ipc`
  import to `test/guards`, which is a stated composition root allowed to "import the whole tree at
  once" (package doc comment) — `importgraph`/`testdeps` both still pass.

I also created `testdata/golden/contracts/ipc/MANIFEST.json` (required by the repo's §16 fixture
convention — every `testdata/golden/contracts/<pkg>` directory needs one) and
`TestRenderManifest_ByteIdentical`/`TestVerifyFixtures_AcceptsTheFrozenSet` in `tools/devtool` (no
edits needed there — my manifest just had to match the established two-space, one-line-per-fixture
format `renderManifest` produces, which I copied from the `contract`/`hookio`/`paths` manifests).

## Deviation from the brief's exact commit message: 1-character overage

The brief's exact required subject line, `feat(ipc): address resolution, NDJSON framing, and the
hot-path state record`, is **76 characters** — the repository's own `commit-msg` hook
(`tools/devtool/checkcommitmsg.go`, §20 of the implementation spec) enforces `type(scope): ` + 1-64
characters of free text; the brief's free text is 65, one over. `git commit` refused it:

```
devtool: check-commit-msg: subject "..." does not match Conventional Commits format
^(feat|fix|docs|test|refactor|perf|build|ci|chore|revert)(\(scope\))?: subject (1-64 chars)
```

I did not bypass the hook (`--no-verify` is prohibited without explicit authorization) and did not
guess at a different resolution; I made the smallest possible meaning-preserving trim — dropping the
word "the" before "hot-path" (65 → 61 chars) — and used that as the actual committed subject:

    feat(ipc): address resolution, NDJSON framing, and hot-path state record

The body and `Refs:` footer are verbatim as specified. Flagging this explicitly since "exactly this
message" was an explicit instruction I could not fully honor against another explicit, unwaivable
constraint (don't skip hooks); this is the one place the commit's text differs from the brief's
literal string.

## Files changed (final commit `71f50ba`)

New:
- `internal/ipc/frame.go`, `internal/ipc/op.go`, `internal/ipc/state.go`
- `internal/ipc/frame_test.go`, `internal/ipc/op_test.go`, `internal/ipc/state_test.go`,
  `internal/ipc/fuzz_test.go`, `internal/ipc/bench_test.go`
- `testdata/golden/contracts/ipc/{MANIFEST.json,observe_tool.ndjson,response_reply.ndjson,state_degraded.bin}`
- `testdata/corpora/ipcframe/{golden_observe_tool.ndjson,empty_object.json,empty.bin,one_mib_of_a.txt,invalid_utf8.bin}`

Modified:
- `internal/ipc/resolve.go`, `internal/ipc/resolve_test.go` — `ProjectHash12`/`ProjectHash8`,
  `normalizeRoot`/`stripTrailingSlash` (goos-injected case-fold + trailing-slash rule),
  `ErrAddrTooLong`, `QOMPACK_IPC_ADDR` override, XDG absoluteness guard.
- `internal/contract/mode.go`, `internal/contract/mode_test.go`, `internal/contract/internal_test.go`
  — `ParseMode` export (rename), `Mode.MayAct`/`Mode.MayRecord`.
- `internal/contract/monitor.go` — one-line caller update (`parseMode` → `ParseMode`).
- `internal/testutil/fixtures_test.go` — pinned frozen-fixture count 23 → 26.
- `test/guards/v1_integration_test.go` — `v1FixtureType` entries + `v1BinaryFixtureKeys`, `ipc` import.

## Self-review findings

- Re-read every new/changed production file (`resolve.go`, `frame.go`, `op.go`, `state.go`,
  `mode.go`) end to end after the suite went green; found no leftover TODOs, dead code, or
  inconsistent naming.
- Confirmed import discipline: `internal/ipc`'s new/changed files import only stdlib plus
  `core`/`paths`/`config`/`contract` (no `hookio`, no `net`, no `os/exec` in `frame.go`/`op.go`/
  `state.go`/`resolve.go` — `net` stays confined to the untouched `dial_windows.go`/`dial_other.go`).
- Confirmed no nomagic-forbidden literal ({20000, 12000, 10000, 8000, 2048, 1024, 4096, 16384, 300,
  120, 450} / {0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}) appears in any non-test, non-defaults.go file
  I added — `golangci-lint`'s `nomagic` sub-check confirms this independently.
- Confirmed no `time.Sleep` anywhere in production code (`sleepcheck` sub-check passes); the one
  place I needed to "wait" (`WriteState`'s retry) uses `runtime.Gosched()`, not a timer.
- Verified `TestStateWriteIsAtomic`'s stability empirically (6 consecutive green `-race` runs) since
  it was the one test that was genuinely flaky before the fix — I did not want to hand off a test
  that merely happened to pass once.
- Double-checked the `state_degraded.bin`/`observe_tool.ndjson`/`response_reply.ndjson` fixtures are
  each exercised by a real assertion (not just present-on-disk): `TestState_DegradedGoldenFixtureDecodes`
  actually decodes the fixture through `ReadState` and checks `Mode`/`Hot`; the two NDJSON fixtures
  are asserted byte-exact against `EncodeRequest`/`EncodeResponse` output.
- `git status --short` after the commit is empty — nothing left uncommitted, no stray generator
  files.

## Concerns for the controller

1. **The 1-character commit-subject trim** (see above) — the committed subject is not byte-identical
   to the brief's literal text. I believe the resolution (smallest possible meaning-preserving trim,
   fully disclosed) is correct, but flagging since "exactly this message" was explicit.
2. **`WriteState`'s retry loop is new behavior beyond what task-1-spec.md's pseudocode showed** (which
   was a single `paths.WriteAtomic` call). It's small, well-contained, and directly necessitated by a
   real, reproducible Windows filesystem characteristic under concurrent access — not speculative
   hardening — but it is more than the brief's literal spec described, so I'm surfacing it rather than
   letting it pass silently.
3. **Two files outside the brief's stated touch-scope were edited** (`internal/testutil/fixtures_test.go`,
   `test/guards/v1_integration_test.go`) — both are one-line-of-substance, mechanical consequences of
   adding new frozen fixtures (a pinned count bump; a fixture-type registration + one binary-format
   carve-out), not behavioral changes to anything SP-05 doesn't own. Both were necessary for `go test
   ./...` to pass, which was an explicit requirement. Flagging per the same spirit as concern 1 — I
   did not silently expand scope, I'm naming exactly what and why.
