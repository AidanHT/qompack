# Task 1 — Commit 1: ipc address resolution, NDJSON framing, op vocabulary, and the 32-byte hot-path state record

You are extending the existing package `internal/ipc` of the Go module `github.com/qompack/qompack` (repo worktree: `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05`, branch `feat/sp05-daemon-ipc-and-hot-path`).

Read these files IN THIS ORDER before writing anything:
1. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/sp01-inventory.md` — what SP-01 already shipped. **Shipped spellings always win over the plan text's placeholders.**
2. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/plan-context.md` — mission, design context, out-of-scope, global rules.
3. `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-1-spec.md` — your requirements verbatim from the plan (implementation spec for addr.go/frame.go/op.go/state.go, the test tables, fixtures list, and the commit-1 checklist). Exact values live there — use them verbatim.
4. The existing source: `internal/ipc/*.go` (addr.go, resolve.go, resolve_test.go, wire.go, wire_test.go, dial_windows.go, dial_other.go, doc.go), `internal/contract/mode.go`, `internal/config/runtime.go`, `internal/paths/atomic.go`, `internal/core/`.

## Binding rulings (controller decisions — the plan text is amended by these)

- **Keep SP-01's shipped spellings**: `NamedPipe`/`UnixSocket` (NOT AddrNamedPipe/AddrUnixSocket), `MaxLineBytes` (NOT DefaultMaxLine), `ACK`/`NAK` as shipped, `Request`/`Response` as shipped in wire.go. Do not rename anything SP-01 exported.
- **No `ipc.Router` type.** Routing stays in `daemon.Options`' handler map (a later task). Task 1 does NOT create op.go's Router. It DOES add: the five admin op constants (`OpAdminPing Op = "admin.ping"`, `OpAdminDrain`, `OpAdminReload`, `OpAdminIdle`, `OpAdminShutdown` — keep the shipped `OpAdminPrefix` and make the constants consistent with it), `func KnownOps() []Op` (sorted, all 13), `func (o Op) Valid() bool`, `func (o Op) HotPath() bool` (true for observe.tool/observe.prompt/observe.stop).
- **Resolve/addr work extends the shipped `resolve.go`**, which already implements the XDG→TempDir→short-fallback chain with `sunPathMax=100`. Your additions:
  - Export `ProjectHash12(projectRoot string) string` and `ProjectHash8(...)` (currently the unexported `endpointHash`). Keep plain sha256, undomained (comment already explains why).
  - Normalization: upgrade the hash input normalization to mirror `paths.Key` semantics: `filepath.Abs` → `filepath.Clean` → ToSlash → strip trailing `/` (except bare root) → lowercase on windows and darwin only (see `paths.DefaultFold`/`KeyFold`; on any Abs error fall back to the cleaned input). Update `resolve_test.go` expectations accordingly — SP-05 owns this package. Keep `resolveFor`'s injected-environment testability; you will need to inject goos into the normalization too so case-folding is testable cross-platform.
  - Add `var ErrAddrTooLong = errors.New("qompack: ipc address exceeds sun_path limit")` and return it (wrapped detail is fine) where resolve.go currently returns the fmt.Errorf for an unfittable short path. Keep `ErrUnresolvedRoot`.
  - Add the `QOMPACK_IPC_ADDR` override: parsed as `pipe:<name>` or `unix:<path>`, still subject to the 100-byte guard for unix; it overrides everything. Thread it through the injected `getenv` so it is testable. A malformed value is ignored (fall through to normal resolution) — the hot path never fails on an env var.
  - The plan row `TestResolveUnixPrefersXDG` requires XDG_RUNTIME_DIR to be used **only if non-empty and absolute** — add the absoluteness check (POSIX-style absolute, i.e. starts with `/`, since the value is a POSIX path even when the test runs on Windows).
- **contract/mode.go additions (small, additive, part of this commit):** export `func ParseMode(s string) (Mode, bool)` (rename the unexported `parseMode`; update its callers), and add `func (m Mode) MayAct() bool` (false for ModeDegradedPassive and ModeOff) and `func (m Mode) MayRecord() bool` (false only for ModeOff), each with a short doc comment citing §12.1. Do not touch anything else in internal/contract.
- **paths API reality**: `paths.WriteAtomic(p string, b []byte, perm fs.FileMode) error` (3 args). Use perm `0o600` for state.bin.

## What Task 1 delivers (files)

- `internal/ipc/frame.go`: `EncodeRequest`, `DecodeRequest`, `EncodeResponse`, `DecodeResponse` (json.Encoder, SetEscapeHTML(false), pooled bytes.Buffer via sync.Pool, exactly one trailing `\n`), `LineReader` (`NewLineReader(r io.Reader, maxLine int)`, `ReadLine() ([]byte, error)`, resynchronizes by discarding to next `\n` after an oversize line), `var ErrLineTooLong`. Max line defaults tie to shipped `MaxLineBytes`.
- `internal/ipc/op.go` (or extend wire.go — your call, prefer a new op.go for the op helpers): admin constants, `KnownOps`, `Valid`, `HotPath`.
- `internal/ipc/state.go`: the 32-byte state record exactly per task-1-spec's layout table (magic 'Q','P','S','1', little-endian, crc32 Castagnoli over [0:28]). Types/functions: `type State struct{...}` (fields per the plan's Produces section: Mode contract.Mode; Hot HotPathMode; ConnectDeadlineMs, AckDeadlineMs uint16; DaemonEnabled, SpoolOnBreach bool; MaxPayloadBytes uint32; DaemonPID uint32; Written core.UnixMilli), `StatePath(projectRoot)` = `<root>/.qompack/run/state.bin`, `ReadState(projectRoot string, fallback config.Config) State` (missing/short/bad-magic/bad-CRC → `StateFromConfig(fallback)`, silent), `WriteState` (paths.WriteAtomic), `RemoveState`, `StateFromConfig(cfg config.Config) State`. Runtime.mode string → contract.Mode mapping: "off"→ModeOff, "passive"→ModeDegradedPassive, anything else ("auto"/"full"/unknown)→ModeFull; use contract.ParseMode where it fits. Read the real config field spellings from `internal/config/runtime.go` (cfg.Runtime.Daemon.AckDeadlineMs etc.).
- `internal/ipc/resolve.go` (+ possibly split per build tags if genuinely needed — shipped code is tagless and injectable; keep that style): the extensions above.
- Fixtures: `testdata/golden/contracts/ipc/observe_tool.ndjson` (byte-exact line from task-1-spec), `testdata/golden/contracts/ipc/response_reply.ndjson`, `testdata/golden/contracts/ipc/state_degraded.bin` (32 bytes, mode=1, hot=1), `testdata/corpora/ipcframe/*` fuzz seeds. Note: check whether `testdata/` conventions in this repo put golden files at repo root `testdata/` or under the package — look at how existing golden tests (e.g. internal/contract/golden_test.go, internal/testutil) locate fixtures and follow the established convention.
- Tests: every row of the addr/frame/state test tables in task-1-spec (adapted to shipped spellings), plus `FuzzDecodeRequest`, `BenchmarkReadState`, `BenchmarkEncodeRequest`.

## Global constraints (binding)

- TDD: write the tests first, run them, confirm they fail (compile error for absent symbols counts, per the commit checklist), then implement. Record RED/GREEN evidence in your report.
- Import discipline: internal/ipc imports only foundation (core, paths, config, logging, obs) + hookio + contract. No `net` use in Task 1's files (dial/listen is Task 2). No os/exec.
- nomagic lint: no forbidden literals outside config defaults/tests; named constants with a §-reference comment for protocol numbers (many already exist in shipped code — follow that idiom). The state record's magic/offsets are protocol constants — name them.
- Every duration from config or a named constant. No time.Sleep anywhere.
- gofumpt -l clean, golangci-lint clean, go vet clean. Comment style: match the shipped files' voice (explanatory doc comments citing §-sections).
- Windows host: run `go test ./internal/ipc/... -race`, the 30s fuzz run, and cross-compile checks `GOOS=linux go build ./...` and `GOOS=darwin go build ./...` (use $env:GOOS in PowerShell or GOOS=... in bash).
- Run `go run ./tools/devtool fmt lint vet` (check devtool's actual task names in tools/devtool first; sp01-inventory.md lists them) and `go test ./internal/contract/...` (you touched mode.go) before committing.
- Exactly ONE commit containing all of this, with exactly this message (no Co-Authored-By, no attribution trailers):

```
feat(ipc): address resolution, NDJSON framing, and the hot-path state record

Windows named pipes and Unix sockets resolve from a plain sha256 of the normalized
project root per 00-ARCHITECTURE §2.4, with the sun_path guard macOS requires. The
32-byte state record exists so the hot path pays one small read instead of a config
load before it decides whether to connect at all.

Also exports contract.ParseMode and adds Mode.MayAct/MayRecord, which the state
record and the daemon's mode enforcement read.

Refs: SP-05, §8.1, 00-ARCHITECTURE §2.4
```

## Report

Write your full report to `.superpowers/sdd/V2-SP-05-daemon-ipc-and-hot-path/task-1-report.md` (implementation notes, TDD RED/GREEN evidence with commands and output excerpts, files changed, self-review findings, concerns). Reply with only: Status, commit SHA+subject, one-line test summary, concerns, report path.
