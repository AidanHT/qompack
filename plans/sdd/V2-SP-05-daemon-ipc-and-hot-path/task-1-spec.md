### `internal/ipc/addr.go`

**Responsibility.** Deterministic, I/O-free resolution of the transport address from the project root.

```go
func ProjectHash12(projectRoot string) string
func ProjectHash8(projectRoot string) string
func Resolve(projectRoot string) (Addr, error)
```

`normalizeRoot(projectRoot)`: `filepath.Abs` → `filepath.Clean` → replace `\` with `/` → strip a trailing `/` (except a bare root) → on `windows` and `darwin` only, `strings.ToLower`. This mirrors `paths.Key` case semantics so two spellings of one project resolve to one daemon.

`ProjectHash12` and `ProjectHash8`:

```go
func projectHash(root string, n int) string {
    sum := sha256.Sum256([]byte(normalizeRoot(root)))   // array must be bound before slicing
    return hex.EncodeToString(sum[:])[:n]
}
func ProjectHash12(root string) string { return projectHash(root, 12) }
func ProjectHash8(root string) string  { return projectHash(root, 8) }
```

**Deliberately plain `sha256`, not `core.HashBytes`,** because 00-ARCH §2.4 specifies `sha256(normalizedAbsProjectRoot)` with no domain separator; a comment states this so nobody "fixes" it.

Resolution:

- `windows`: `Addr{Kind: AddrNamedPipe, Path: pipePrefix + ProjectHash12(root)}` where `const pipePrefix = ` `` `\\.\pipe\qompack.` `` (a Go raw string literal, so the backslashes are literal), yielding `\\.\pipe\qompack.<hash12>`. No length guard is needed (the pipe namespace allows 256 chars).
- everything else: try in order
  1. `filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "qompack", hash12+".sock")` — only if `XDG_RUNTIME_DIR` is non-empty and absolute;
  2. `filepath.Join(os.TempDir(), fmt.Sprintf("qompack-%d", os.Getuid()), hash12+".sock")`;
  and if the chosen path is `> 100` bytes, fall back to `filepath.Join(os.TempDir(), "qp-"+hash8+".sock")`. If *that* is still `> 100` bytes, return `Addr{}, ErrAddrTooLong`.
- `QOMPACK_IPC_ADDR` overrides everything (tests, CI, and the bench harness use it to keep sockets inside `t.TempDir()`); it is parsed as `pipe:<name>` or `unix:<path>` and still subject to the 100-byte guard.

`os.Getuid()` does not exist on Windows in a useful form; the POSIX branch lives in `addr_unix.go` (build tag `!windows`) and the Windows branch in `addr_windows.go`, with `addr.go` holding `normalizeRoot`, the hashes and `ErrAddrTooLong`.

**Failure modes.** `ErrAddrTooLong` is the only error. Callers (`NewClient`) treat it as "spool-only for this process" — never a hook failure.

---

### `internal/ipc/frame.go`

**Responsibility.** The wire format, byte-for-byte.

```go
const (
    ACK byte = 0x06
    NAK byte = 0x15
)
const DefaultMaxLine = 1 << 20 // 1 MiB, §2.4 "1 MiB max line"

func EncodeRequest(req Request) ([]byte, error)          // compact JSON + '\n', no HTML escaping
func DecodeRequest(line []byte) (Request, error)
func EncodeResponse(resp Response) ([]byte, error)
func DecodeResponse(line []byte) (Response, error)
func NewLineReader(r io.Reader, maxLine int) *LineReader
func (lr *LineReader) ReadLine() ([]byte, error)          // returns ErrLineTooLong past maxLine
var ErrLineTooLong = errors.New("qompack: ipc line exceeds max")
```

Encoding uses a `json.Encoder` with `SetEscapeHTML(false)` writing into a pooled `bytes.Buffer` (`sync.Pool`) so the hot path allocates once. `json.Encoder.Encode` already appends `\n`; do not add a second one. `LineReader` wraps `bufio.Reader` with an explicit growth cap rather than `bufio.Scanner`, so an oversize line yields `ErrLineTooLong` *and* the reader can resynchronize by discarding to the next `\n`.

Byte-exact request example (this is a golden fixture, `testdata/golden/contracts/ipc/observe_tool.ndjson`):

```
{"op":"observe.tool","s":"sess-01","t":1730000000000,"e":{"hook_event_name":"PostToolUse","session_id":"sess-01","tool_name":"Read","tool_use_id":"tu_1"}}
```

Fire-and-forget exchange: client writes the line; server writes exactly one byte, `0x06` or `0x15`. Reply exchange (`"r":true`): client writes the line; server writes one NDJSON `Response` line. There is no length prefix and no other framing.

---

### `internal/ipc/op.go`

**Responsibility.** The op vocabulary and the **op-routing table**, which is data rather than a `switch` so wave-3 subplans register handlers instead of editing daemon internals.

```go
type Router struct {
    mu       sync.RWMutex
    m        map[Op]Handler
    fallback Handler
}
func NewRouter() *Router { return &Router{m: make(map[Op]Handler, len(KnownOps()))} }
func (r *Router) Handle(op Op, h Handler)   // last registration wins; logs at Debug on replace
func (r *Router) SetFallback(h Handler)
func (r *Router) Ops() []Op                 // sorted, for `self-test` and `status`
func (r *Router) Route(ctx context.Context, req Request) Response {
    r.mu.RLock(); h, ok := r.m[req.Op]; fb := r.fallback; r.mu.RUnlock()
    if !ok { if fb == nil { return Response{OK: false, Err: "unknown op: " + string(req.Op)} }; h = fb }
    defer func() { /* recover → Response{OK:false, Err:"panic: …"}; never propagate */ }()
    return h(ctx, req)
}
```

The panic recovery is inside `Route`, not in each handler, and is the mechanism behind §12.3 "MCP tool panic → recovered at the handler boundary, never kills the server".

`Op.HotPath()` returns true for `OpObserveTool`, `OpObservePrompt`, `OpObserveStop` — the three that flow through the spool submode.

---

### `internal/ipc/state.go` — the 32-byte hot-path state record

**Responsibility.** Give the thin client everything it needs to decide *whether and how to connect* in **one** `os.ReadFile` of 32 bytes, so the hot path never runs `config.Load` (two file opens plus env scan) and never stats several files.

Path: `<projectRoot>/.qompack/run/state.bin`. Written by the daemon on start, on every mode/submode transition, and on every config reload. Removed on clean daemon stop.

Layout (little-endian, exactly 32 bytes):

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | magic `'Q','P','S','1'` |
| 4 | 1 | `mode` (0 = full, 1 = degraded-passive, 2 = off) |
| 5 | 1 | `hot` (0 = sync, 1 = spool) |
| 6 | 2 | `connectDeadlineMs` uint16 |
| 8 | 2 | `ackDeadlineMs` uint16 |
| 10 | 2 | `flags` uint16 — bit 0 `daemonEnabled`, bit 1 `spoolOnBreach` |
| 12 | 4 | `maxPayloadBytes` uint32 |
| 16 | 4 | `daemonPID` uint32 |
| 20 | 8 | `written` int64 unix milliseconds |
| 28 | 4 | `crc32c` (Castagnoli) over bytes `[0:28]` |

```go
func ReadState(projectRoot string, fallback config.Config) State
```
reads the file; on any of *missing*, *short*, *bad magic*, *bad CRC*, it returns `StateFromConfig(fallback)` without logging (a missing state file is the normal first-run case). `WriteState` uses `paths.WriteAtomic` — 32 bytes through the `.qompack/tmp` staging path, so a client never observes a torn record.

The client's `fallback` is `config.Defaults()`, which is a pure function with no I/O. That is the entire reason this design exists: on the hot path we pay one 32-byte read and zero config parsing.

---

### `internal/ipc` — addressing and framing (`addr_test.go`, `frame_test.go`)

| Test | Setup | Input | Expected |
|---|---|---|---|
| `TestProjectHash12Deterministic` | — | `C:\Proj\Foo` and `C:/Proj/Foo/` | identical 12-char lowercase hex; `len == 12` |
| `TestProjectHash12CaseFoldsOnWindowsAndDarwin` | — | `/Proj/Foo` vs `/proj/FOO` | equal on `windows`/`darwin`, different on `linux` |
| `TestResolveUnixPrefersXDG` | `XDG_RUNTIME_DIR=/run/user/1000` | root `/p` | `Addr{AddrUnixSocket, "/run/user/1000/qompack/<h12>.sock"}` |
| `TestResolveUnixFallsBackToTempDir` | `XDG_RUNTIME_DIR` unset | root `/p` | path under `os.TempDir()/qompack-<uid>/` |
| `TestResolveUnixSunPathGuard` | `XDG_RUNTIME_DIR` = a 120-char dir | root `/p` | path is `<TempDir>/qp-<h8>.sock`, `len <= 100` |
| `TestResolveUnixSunPathImpossible` | `TMPDIR` = a 200-char dir, `XDG` unset | root `/p` | `ErrAddrTooLong` |
| `TestResolveWindowsPipeName` | `windows` only | root `C:\p` | `\\.\pipe\qompack.<h12>` |
| `TestEncodeRequestByteExact` | fixed `Request` | golden `observe_tool.ndjson` | encoded bytes equal the golden byte-for-byte, exactly one trailing `\n`, no `\u003c` escaping |
| `TestDecodeRequestRoundTrip` | — | 20 rapid-generated `Request`s | `DecodeRequest(EncodeRequest(r)) == r` (`cmp.Diff` empty) |
| `TestLineReaderRejectsOversize` | `maxLine=64` | a 100-byte line then a 10-byte line | first returns `ErrLineTooLong`; second returns the 10-byte line (resynchronization works) |
| `FuzzDecodeRequest` | seed corpus: the golden, `{}`, `` , 1 MiB of `a` | arbitrary bytes | never panics; returns a value or an error |

### `internal/ipc` — state file (`state_test.go`)

| Test | Input | Expected |
|---|---|---|
| `TestStateRoundTrip` | `State{ModeDegradedPassive, HotSpool, 5, 8, true, true, 1048576, 4242, 1730000000000}` | `ReadState` returns exactly that; file is exactly 32 bytes |
| `TestStateMissingFallsBackToDefaults` | no file | `ReadState(root, config.Defaults())` returns `ModeFull`, `HotSync`, `AckDeadlineMs == 8`, `ConnectDeadlineMs == 5` |
| `TestStateBadCRCFallsBack` | write a valid record, flip byte 12 | returns the defaults, no error, no log |
| `TestStateShortFileFallsBack` | 17 bytes | returns the defaults |
| `TestStateWriteIsAtomic` | 500 concurrent `WriteState` + `ReadState` under `-race` | every read yields a valid CRC; no torn record |
| `BenchmarkReadState` | warm file cache | budget note: must be **< 100 µs/op**, since it is the only I/O on the hot path before `dial` |

### Fixtures to create

- `testdata/golden/contracts/ipc/observe_tool.ndjson` — the byte-exact request line above.
- `testdata/golden/contracts/ipc/response_reply.ndjson` — a `Reply` response with an `Output`.
- `testdata/golden/contracts/ipc/state_degraded.bin` — a 32-byte state record, `mode=1`, `hot=1`.
- `testdata/golden/contracts/contract/history_degraded.json` — a persisted degraded `History`.
- `testdata/golden/contracts/contract/transcript_with_sentinel.jsonl` — 40 lines, the sentinel in the tail.
- `testdata/corpora/ipcframe/` — fuzz seeds for `DecodeRequest` (the golden, `{}`, empty, 1 MiB of `a`, invalid UTF-8).

---

### Commit 1

```
feat(ipc): address resolution, NDJSON framing, and the hot-path state record

Windows named pipes and Unix sockets resolve from a plain sha256 of the normalized
project root per 00-ARCHITECTURE §2.4, with the sun_path guard macOS requires. The
32-byte state record exists so the hot path pays one small read instead of a config
load before it decides whether to connect at all.

Refs: SP-05, §8.1, 00-ARCHITECTURE §2.4
```

- [ ] Write `internal/ipc/addr_test.go`, `frame_test.go`, `state_test.go` and the fuzz target first; run `go test ./internal/ipc/...` and confirm they **fail to compile** (symbols absent).
- [ ] Add `internal/ipc/addr.go`, `addr_unix.go`, `addr_windows.go`, `frame.go`, `op.go`, `state.go`, `doc.go`.
- [ ] Add fixtures `testdata/golden/contracts/ipc/{observe_tool.ndjson,response_reply.ndjson,state_degraded.bin}` and `testdata/corpora/ipcframe/*`.
- [ ] Run `go test ./internal/ipc/... -race`, `go test -run Fuzz -fuzz FuzzDecodeRequest -fuzztime 30s ./internal/ipc`, `go run ./tools/devtool fmt lint vet`.
- [ ] All tests green before committing.

