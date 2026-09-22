# MCP e2e harness: live stderr-capture data race — resolution

**Date:** 2026-09-22
**Branch / worktree:** `verify/v6` @ `57097f6` in `C:/Users/Quant/Documents/Programming/Projects/qompack-v6`
**Scope (exclusive source):** `test/e2e/mcp_e2e_test.go`, plus new `test/e2e/mcp_stderr_capture_test.go`.
**Everything else was read-only.** Issues found outside scope are returned to main in the final section.

## 1. The observed race

Captured under the new product-child lane (`GOFLAGS=-race CGO_ENABLED=1
QOMPACK_REQUIRE_CHILD_RACE=1`) on Linux, frozen source
`/work/linux-readers-20260922-1422` in container `qompack-v6-linux-verification`. Artifacts:

- `plans/sdd/V6-remediation/linux-product-child-race-artifacts/race.8310`
- `plans/sdd/V6-remediation/linux-product-child-race-artifacts/test.log` — `TestStdioServerEndToEnd` FAILs after `TestE2EHookRoundTrip` passes.

`race.8310` is a textbook unsynchronized `bytes.Buffer`:

- **Read** at `mcp_e2e_test.go:151` — `(*mcpE2EChild).send` calling `c.stderr.String()` →
  `bytes.(*Buffer).String()`, on the test goroutine.
- **Previous write** — `bytes.(*Buffer).grow` ← `ReadFrom` ← `io.Copy` ←
  `os/exec.(*Cmd).writerDescriptor.func1`, on the goroutine `os/exec` spawns in `Cmd.Start` to
  drain the child's stderr into `cmd.Stderr`.

### Root cause

`mcpE2EStartWithEnv` set `cmd.Stderr = &stderr` where `stderr` was a plain `bytes.Buffer`. `os/exec`
copies the child's stderr into that buffer from a goroutine it owns, continuously, for the **whole
life of the child**. The harness reads the *same* buffer via `c.stderr.String()` on every `send`,
`await`, and `finish` — to include the child's diagnostics in assertion failure messages. Those two
accesses overlap in wall-clock time: a `String()` for an error message can land while `io.Copy` is
mid-`grow`. `bytes.Buffer` provides no synchronization, so this is a genuine data race — it just
happens to be *reported* through `send`'s error argument, which Go evaluates eagerly even on the
success path (`require.NoError(t, err, "...: %s", c.stderr.String())`).

Read sites on the same buffer, all racing the live copy: lines 151 (`send`), 167 and 185 (`await`),
217/221 (`finish` timeout/error), 225 (`finish` final emptiness assertion).

## 2. The fix

A mutex-guarded buffer, `syncBuffer`, replaces the raw `bytes.Buffer` for the child's stderr sink.

- It exposes **only** `Write([]byte) (int, error)` — so it still satisfies `io.Writer` for
  `cmd.Stderr` — and `String() string` — so assertions can still read it. Both take one mutex over
  one embedded `bytes.Buffer`. No `bytes.Buffer` method is promoted, so no caller can reach the
  buffer unlocked. This is precisely the "mutex-protected io.Writer/String helper with no exposed
  bytes.Buffer methods" the brief prescribed.
- `mcpE2EChild.stderr` changed from `*bytes.Buffer` to `*syncBuffer`; the local in
  `mcpE2EStartWithEnv` from `var stderr bytes.Buffer` to `var stderr syncBuffer`. Every read site
  (`c.stderr.String()`) is unchanged in spelling and behavior.

### What the fix deliberately does *not* do (per the brief's constraints)

- **Detector not suppressed:** the race lane still runs `-race`; the fix removes the race rather than
  masking it.
- **No diagnostics dropped:** every failure message still prints the child's stderr; `String()`
  returns the same content, now under a lock.
- **No waiting for the child before responding:** the read path is unchanged — `send`/`await`/
  `finish` never block on the child's lifetime; they take a short mutex only.
- **Protocol assertions and timeouts untouched:** `mcpE2E*Bound`/`Tick`, the id-ordering checks, the
  notification-produces-no-response check, span/chunk contracts — all byte-for-byte unchanged.

## 3. Stdout / close-lifetime audit (directly related races)

I audited the rest of the helper's concurrency for races of the same class. Findings, none of which
required an in-scope change:

- **stdout drain goroutine → `lines` channel (`await`/`finish` read):** fully synchronized by the
  channel. The `trailing` slice in `finish` is written only by the drain goroutine and read only
  after `<-drained`, so the channel close is a happens-before edge. Safe.
- **`finish` runs `cmd.Wait()` concurrently with the still-live stdout scanner.** This is the
  documented `StdoutPipe` caveat ("incorrect to call Wait before all reads have completed"), but it
  is **not a data race on shared memory**: `os.File`/`poll.FD` serialize a concurrent `Close` against
  an in-flight `Read` at the fd layer and hand the reader an error, on which the scanner simply stops
  and closes `lines`. The detector does not flag it and did not in `race.8310`. Left unchanged — a
  restructuring here would be an unrelated change, returned to main (see §5).
- **`c.cmd.Process.Kill()` on the exit-timeout path** races nothing: `Kill` is safe concurrent with
  the `Wait` goroutine.

So the stderr buffer was the sole memory-level race in this helper. `stdin` writes (`send`) and its
close (`finish`) are driven sequentially by the single test goroutine.

## 4. Validation performed here — and its honest limits

- **gofumpt** (pinned, `go run -modfile=tools/pinned/go.mod mvdan.cc/gofumpt -l`) on both in-scope
  files: **no output** → already conformant.
- **One focused race test**, `TestSyncBufferConcurrentWriterReader` in the new
  `mcp_stderr_capture_test.go`, drives `syncBuffer` through the exact shape of the original race —
  one `io.Copy` writer (the `os/exec` side, growing the backing array) against 8 concurrent
  `String()` readers (the assertion side) — and asserts no bytes are lost. Run under `-race` and
  recorded:
  - `python dist/v6-remediation/run.py mcp-harness-syncbuffer-race-20260922 go test -race -run '^TestSyncBufferConcurrentWriterReader$' -count=1 -v ./test/e2e/`
  - **Result: PASS, exit 0, no data race** (Windows/amd64, go1.26.6, CGO_ENABLED=1, gcc from
    msys2 ucrt64). Evidence: `plans/sdd/V6-remediation/runs/mcp-harness-syncbuffer-race-20260922.{log,json,source.json}`.
  - Compiling that test also compiled the whole `test/e2e` package under `-race`, confirming the
    field type change (`*bytes.Buffer` → `*syncBuffer`) breaks no other caller.

### Limits I did not overcome (by scope, and honestly)

- **I did not reproduce the original failing test.** `TestStdioServerEndToEnd` builds the binary,
  starts a real daemon and a real child, and needs the Linux product-child lane; the brief reserves
  running the required actual tests, the new overlay, and Docker to main. My focused test proves the
  *helper type* is race-free under the same access pattern; it does **not** prove the end-to-end test
  now passes on Linux under the required lane. **Main must re-run `TestStdioServerEndToEnd` (and the
  suite) in a fresh overlay** — not the immutable `/work/linux-readers-20260922-1422` snapshot — to
  confirm `race.8310` is gone in situ.
- The focused run is Windows, not the Linux lane that captured the race; the race detector is the
  same instrumentation on both, and the raced type is OS-independent, but the authoritative
  confirmation is main's Linux re-run.

## 5. Returned to main (out of scope — read, not edited)

- **`finish` / `StdoutPipe` + `Wait` ordering (§3, bullet 2).** Benign today (fd-layer serialization,
  not a memory race), but it is the documented "Wait before reads complete" anti-pattern. If main
  wants belt-and-suspenders, drain the scanner goroutine to completion (`<-drained`) *before*
  `cmd.Wait()` returns is consumed, or read stdout fully before Wait. Not required to close
  `race.8310`; noted so the class is on record.
- No other files were touched. Any race in daemon/store sources, `race_instrumentation_test.go`,
  workflows, or the lane definition remains main's.
