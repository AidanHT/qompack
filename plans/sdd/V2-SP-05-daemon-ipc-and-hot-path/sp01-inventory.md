# SP-01 Shipped Symbol Inventory (for SP-05 implementers)

Repo researched (read-only): `C:/Users/Quant/Documents/Programming/Projects/qompack-sp05`

This document is an exact inventory of what SP-01 already shipped in the packages SP-05
(daemon/IPC/hot-path/contract-monitor) builds on. Every signature below was copied verbatim from
the source via the Read tool. Where the plan text (`plans/V2-SP-05-daemon-ipc-and-hot-path.md`)
uses different placeholder names, use the spellings in this document instead — they are what the
pinned tests actually check.

---

## 1. `internal/ipc/ipctest` — conformance suite

### 1.1 Factory/suite API

File: `internal/ipc/ipctest/suite.go`

```go
// Transport is what a framing round-trip needs and no single §5.4 interface provides: a Client
// already talking to a Server that is running the Handler the suite supplied. The Addr is carried
// for diagnostics only — the suite never dials it itself.
type Transport struct {
	Client ipc.Client
	Addr   ipc.Addr
}

// RunClientSuite is the conformance suite for ipc.Client.
func RunClientSuite(t *testing.T, name string, factory func(t *testing.T) ipc.Client)

// RunSpoolWriterSuite is the conformance suite for ipc.SpoolWriter.
func RunSpoolWriterSuite(t *testing.T, name string, factory func(t *testing.T) ipc.SpoolWriter)

// RunServerSuite is the conformance suite for ipc.Server.
func RunServerSuite(t *testing.T, name string, factory func(t *testing.T) ipc.Server)

// RunTransportSuite is the conformance suite for the wire format itself.
func RunTransportSuite(t *testing.T, name string, factory func(t *testing.T, h ipc.Handler) Transport)
```

A real implementation must plug into one of these four factory shapes. `RunTransportSuite`'s
factory departs from the `func(t) T` template because it must also accept the `ipc.Handler` the
suite supplies.

### 1.2 Subtests registered and what each asserts

**`RunClientSuite`** (`&lt;name&gt;` is caller-supplied):
- `&lt;name&gt;/shape` — construct Client, send a probe `Request`, require a known error (nil or one of
  the 4 sentinels), require an "honourable" `Response` (`Mode.String() != "unknown"`, `Hot` is
  `HotSync`/`HotSpool`), require `Close()` returns a known error.
- `&lt;name&gt;/behaviour/send_never_propagates_an_error` — `Send` must always return `err == nil`
  (spools on failure instead); response must be honourable.
- `&lt;name&gt;/behaviour/send_always_reports_a_mode_a_client_can_honour` — for `OpObserveTool`,
  `OpObservePrompt`, `OpStatus`, every response carries an honourable Mode/Hot.
- `&lt;name&gt;/behaviour/a_cancelled_context_still_does_not_propagate_an_error` — `Send` with an
  already-cancelled context still returns `nil` error and an honourable response.

**`RunSpoolWriterSuite`**:
- `&lt;name&gt;/shape` — construct SpoolWriter, require known error from `Append`, call `Path()` (no
  error return; any value including empty is shape-valid).
- `&lt;name&gt;/behaviour/append_writes_one_ndjson_line_per_request` — 3 appended requests round-trip
  as exactly 3 NDJSON lines, in order, each parseable back to the same Op/Session/TS.
- `&lt;name&gt;/behaviour/append_only_never_rewrites_an_earlier_line` — a second `Append` leaves the
  first line byte-identical.
- `&lt;name&gt;/behaviour/no_line_exceeds_the_frame_limit` — an oversize request (line &gt; `ipc.MaxLineBytes`)
  is refused with a known error, spool left byte-identical; every remaining line ≤ `MaxLineBytes`.
- `&lt;name&gt;/behaviour/path_is_stable_and_non_empty` — `Path()` non-empty and does not change after use.

**`RunServerSuite`**:
- `&lt;name&gt;/shape` — construct Server, call `Addr()` (no error; a stub's zero Addr is shape-valid),
  `Serve` against an already-cancelled context returns a known "serve" error, `Close()` returns a
  known error.
- `&lt;name&gt;/behaviour/addr_is_resolved_and_stable` — `Addr().Path` non-empty, `Addr().Kind` is
  `NamedPipe` or `UnixSocket`, `Addr()` stable across calls.
- `&lt;name&gt;/behaviour/serve_returns_when_the_context_is_cancelled` — cancelling the context makes
  `Serve` return within `suiteWait` (10s) with a known serve error.
- `&lt;name&gt;/behaviour/close_stops_serve` — calling `Close()` (without cancelling context) makes a
  concurrent `Serve` return within `suiteWait`.

**`RunTransportSuite`**:
- `&lt;name&gt;/shape` — `factory(t, nopHandler)` yields a Transport whose Client, sent a probe, returns
  a known error and an honourable response.
- `&lt;name&gt;/behaviour/fire_and_forget_request_round_trips_the_frame` — request with a custom `Raw`
  payload arrives at handler byte-equivalent (JSONEq), same Op/Session/TS, `Reply == false`;
  `res.OK == true` (the `\x06` ACK).
- `&lt;name&gt;/behaviour/reply_request_returns_the_handlers_response` — `Reply: true` `OpStatus`
  request returns the handler's `Data` verbatim (JSONEq) plus an honourable response.
- `&lt;name&gt;/behaviour/a_refused_request_is_not_ok_but_is_still_not_an_error` — handler returns
  `Response{OK:false, Err:reason}`; `Send` still returns `nil` error, `res.OK == false` (the
  `\x15` NAK), response honourable.
- `&lt;name&gt;/behaviour/an_oversize_request_never_reaches_the_handler` — oversize request never
  invokes the handler (checked via non-blocking channel receive), `err == nil`, `res.OK == false`.

### 1.3 Rule W-1 skip mechanism (exact strings/code)

```go
// ruleW1SkipMsg is the exact, mandatory Rule W-1 skip reason (00-ARCHITECTURE.md §5.22; §15 of
// plans/V1-SP-01-foundation-toolchain-and-contracts.md). devtool lint's stubskips sub-check greps
// test output for this literal string, so it must never be paraphrased.
const ruleW1SkipMsg = "behaviour: implementation is a stub (Rule W-1)"
```

Each `Run*Suite` calls a `skipIfStub*` helper right after the shape block and before the
behaviour block; if it returns `true` the function returns without running the behaviour block:

```go
func skipIfStubClient(t *testing.T, factory func(t *testing.T) ipc.Client) bool {
	t.Helper()
	if isStubClient(t, factory) {
		t.Skip(ruleW1SkipMsg)
		return true
	}
	return false
}
```

Stub detection is via `core.IsNotImplemented(err)` on a probe call: `isStubClient` probes `Send`;
`isStubSpool` probes `Append`; `isStubServer` probes `Serve` with an already-cancelled context;
`isStubTransport` probes the paired Client's `Send`. `skipIfStubSpool`/`skipIfStubServer`/
`skipIfStubTransport` are structurally identical, each calling `t.Skip(ruleW1SkipMsg)`.

`suite_test.go` demonstrates real usage: `TestIPCSuite_ShapePassesAgainstStub` runs each suite
against hand-rolled `fakeStubClient{}`/`fakeStubSpool{}`/`fakeStubServer{}`/stub `Transport` and
proves shape passes / behaviour is skipped. `TestRunClientSuite_AgainstQompackStub` and
`TestRunSpoolWriterSuite_AgainstQompackStub` run against the real `ipc.NewClient`/`ipc.NewSpool`
stubs shipped today. `TestRunSpoolWriterSuite_AgainstAWorkingSpool` runs the SpoolWriter
behaviour block against a hand-written, correct `workingSpool` proving the spec is satisfiable —
enforces `MaxLineBytes` before writing, wraps overflow as `fmt.Errorf("%w: ...", core.ErrBudget, ...)`.
`ipc` ships **no real Server at all** (§5.4 gives it no constructor) — confirmed by
`TestRunServerSuite_StubIsSkipped` / `TestRunTransportSuite_StubIsSkipped`.

### 1.4 Exported symbols currently in package `ipc`

`internal/ipc/addr.go`:
```go
type AddrKind uint8

const (
	NamedPipe AddrKind = iota + 1
	UnixSocket
)

type Addr struct {
	Kind AddrKind
	Path string
}
```
(`dirPerm = 0o700`, `socketPerm = 0o600` unexported.)

`internal/ipc/client.go`:
```go
type Client interface {
	// Send is the hot path. It connects, writes, awaits ACK within deadline, and returns.
	// On ANY failure it spools to disk and returns (Response{OK:false}, nil) — never an error
	// that a hook would propagate.
	Send(ctx context.Context, req Request, deadline time.Duration) (Response, error)
	Close() error
}

type SpoolWriter interface {
	Append(req Request) error
	// Path returns the file Append writes to, so /qompack:status and the daemon's drain can name
	// it. A SpoolWriter that has not created its file yet reports the path it will use.
	Path() string
}

type Server interface {
	// Serve accepts connections until ctx is cancelled or Close is called, dispatching each
	// request to h. It returns nil on an orderly shutdown.
	Serve(ctx context.Context, h Handler) error
	Addr() Addr
	Close() error
}

type Handler func(ctx context.Context, req Request) Response

// NewClient returns a stub Client. SP-05 owns the real one.
func NewClient(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry) Client

// NewSpool returns a stub SpoolWriter rooted at dir, which is <root>/.qompack/spool in every
// real caller.
func NewSpool(dir string) (SpoolWriter, error)
```
(`stubClient`, `stubSpool` unexported — every method reports `core.ErrNotImplemented`,
`stubSpool.Path()` returns `""`.)

`internal/ipc/resolve.go`:
```go
var ErrUnresolvedRoot = errors.New("ipc: project root must not be empty")

// Resolve returns the local endpoint for projectRoot, exactly as 00-ARCHITECTURE.md §2.4 specifies it.
func Resolve(projectRoot string) (Addr, error)
```
(`resolveFor`, `endpointHash`, and path-building constants — `windowsPipePrefix`, `pipeName`,
`xdgRuntimeDirEnv`, `sockDirName`, `sockDirPrefix`, `shortSockPrefix`, `sockExt`, `sunPathMax`,
`hash12Len`, `hash8Len`, `goosWindows` — are unexported.)

`internal/ipc/wire.go`:
```go
type Op string

const (
	OpObserveTool   Op = "observe.tool"
	OpObservePrompt Op = "observe.prompt"
	OpObserveStop   Op = "observe.stop"
	OpSessionStart  Op = "session.start"
	OpCheckpoint    Op = "checkpoint"
	OpFlush         Op = "flush"
	OpStatus        Op = "status"
	OpMCP           Op = "mcp"
)

const OpAdminPrefix = "admin."

type HotPathMode uint8

const (
	HotSync HotPathMode = iota
	HotSpool
)

const (
	ACK byte = 0x06
	NAK byte = 0x15
)

const MaxLineBytes = 1 << 20

type Request struct {
	Op      Op             `json:"op"`
	Session core.SessionID `json:"s"`
	TS      core.UnixMilli `json:"t"`
	Reply bool `json:"r,omitempty"`
	Event *hookio.Event   `json:"e,omitempty"`
	Raw   json.RawMessage `json:"x,omitempty"`
}

type Response struct {
	OK     bool            `json:"ok"`
	Mode   contract.Mode   `json:"mode"`
	Hot    HotPathMode     `json:"hot"`
	Output *hookio.Output  `json:"out,omitempty"`
	Err    string          `json:"err,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}
```

`internal/ipc/dial_other.go` (`//go:build !windows`) and `dial_windows.go` (`//go:build windows`)
— `dial` is **unexported** in both, but is the real transport primitive SP-05 wires into
`Client.Send`:
```go
// !windows
func dial(a Addr, timeout time.Duration) (net.Conn, error) {
	d := net.Dialer{Timeout: timeout}
	return d.Dial("unix", a.Path)
}

// windows (uses github.com/Microsoft/go-winio)
func dial(a Addr, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(a.Path, &timeout)
}
```

`internal/ipc/doc.go`: `ipc` is the only package permitted to import `net` (exempted in the
network-import guard for the unix-socket dial in this directory). Import allow-set: `hookio`,
`contract`, plus foundation (`core`, `paths`, `config`, `logging`, `obs`). `Resolve` and the
platform dial helpers are real/shipped by SP-01; `Client`, `SpoolWriter`, `Server` are stubs;
ACK/NAK/MaxLineBytes are real, normative constants.

---

## 2. `internal/contract/contracttest` — conformance suite

### 2.1 Factory/suite API and subtests

File: `internal/contract/contracttest/suite.go`

```go
func RunMonitorSuite(t *testing.T, name string, factory func(t *testing.T) contract.Monitor)
func RunHistorySuite(t *testing.T, name string, factory func(t *testing.T) contract.History)
```

**`RunMonitorSuite`**:
- `&lt;name&gt;/shape` — registers a well-formed `alwaysAssertion(probeID, true, contract.SevInfo)`
  (`probeID = contract.CSessionStartFires`), requires known error from `Register`; calls `RunAll`,
  requires a known Mode (`ModeFull`/`ModeDegradedPassive`/`ModeOff`) and every result has a known
  Severity; calls `Mode()`, `Degrade(...)`, `Restore(...)` (no error returns), and `Report()`.
- `&lt;name&gt;/behaviour/not_yet_implemented_result_never_degrades` — an assertion that DECLARES
  `SevCritical` but whose `Check` reports `OK:true, Severity:SevInfo` must leave the Monitor at
  `ModeFull`.
- `&lt;name&gt;/behaviour/critical_failure_degrades_to_passive` — a registered `SevCritical` failing
  result degrades to `ModeDegradedPassive`; `Report()` carries the failing result.
- `&lt;name&gt;/behaviour/warn_failure_does_not_degrade` — failing `SevWarn`/`SevInfo` results leave
  `ModeFull`.
- `&lt;name&gt;/behaviour/two_consecutive_clean_runs_restore` — degrade, one clean run stays degraded,
  second consecutive clean run restores to `ModeFull`.
- `&lt;name&gt;/behaviour/results_follow_registration_order` — `RunAll` result order == registration
  order.
- `&lt;name&gt;/behaviour/report_reflects_the_most_recent_run` — `Report()` is the last run only, not
  an accumulating log.
- `&lt;name&gt;/behaviour/register_refuses_an_unusable_assertion` — `Register(contract.Assertion{})`
  (no ID) and one with no Check both error; nothing gets registered.

**`RunHistorySuite`**:
- `&lt;name&gt;/shape` — calls `Saw`, `LastSeen`, `Record`, `Sessions() >= 0` on a fresh History (none
  of History's 4 methods has an error return).
- `&lt;name&gt;/behaviour/record_then_saw` — `Saw` false before `Record`, true after; recording one ID
  doesn't mark another as seen.
- `&lt;name&gt;/behaviour/last_seen_returns_the_recorded_instant` — `LastSeen` returns the recorded
  instant; a later `Record` advances it.
- `&lt;name&gt;/behaviour/unknown_id_is_never_saw` — `Saw` false and `LastSeen` returns `(0, false)`
  for an ID never recorded.
- `&lt;name&gt;/behaviour/sessions_never_decreases` — `Sessions()` monotone non-decreasing, never
  negative.

Notable: `contracttest.RunMonitorSuite`'s behaviour block **runs and passes today** against
`contract.NewMonitor` (the real Monitor mechanics ship in SP-01, §12.1 requires them real in wave
0). `RunHistorySuite`'s behaviour block is the ordinary skip case since `contract` ships **no**
History implementation at all.

`suiteEnv()` helper: `func suiteEnv() contract.Env { return contract.Env{Clock: suiteClock{}} }`
(no Cfg/Store/Event/Log — `contracttest` may only import `contract`, `testutil`, `core` per §3.2).

`suite_test.go` demonstrates: `fakeStubMonitor{}` (Register returns `core.ErrNotImplemented`),
`fakeStubHistory{}` (all zero-value returns), `memHistory` (unexported, in-memory correct History
proving the spec satisfiable), and `newRealMonitor(t)` built via
`contract.NewMonitor(logging.Nop(), obs.New(newFakeClock()), statePath)` with
`statePath = filepath.Join(paths.Of(t.TempDir()).State, "contract.json")`.

### 2.2 golden_test.go — pinned `contract.json` / result-set shape

File: `internal/contract/golden_test.go`. Golden fixture:
`testdata/golden/contracts/contract/want/result_set.json` (plus a `MANIFEST.json` alongside).

`TestResultSet_MatchesFrozenGolden` builds a monitor via `contract.NewMonitor(...)`, registers all
9 `contract.StandardAssertions()`, runs `RunAll` at a fixed clock
(`resultSetClock = &fakeClock{now: time.UnixMilli(1767225510000).UTC()}`), asserts `mode ==
ModeFull`, `len(results) == 9`, and does a **byte-level** `require.Equal` of
`json.MarshalIndent(results, "", "  ")` against the golden file.
`TestResultSet_GoldenRoundTripsLosslessly` unmarshals the golden back into `[]contract.Result` and
re-marshals to prove exact round-trip.

Pinned golden JSON (all 9 entries, `"ok": true`, `"severity": 0` i.e. `SevInfo`,
`"observed": "not-yet-implemented"`, `"ts": 1767225510000`):

```json
[
  {"id": "session_start.fires", "ok": true, "severity": 0, "expected": "SessionStart hook fires", "observed": "not-yet-implemented", "ts": 1767225510000},
  {"id": "session_start.source_compact", "ok": true, "severity": 0, "expected": "SessionStart arrives with source=compact after PreCompact", "observed": "not-yet-implemented", "ts": 1767225510000},
  {"id": "hook.additional_context_delivered", "ok": true, "severity": 0, "expected": "additionalContext reaches the transcript", "observed": "not-yet-implemented", "ts": 1767225510000},
  {"id": "precompact.has_time_to_write", "ok": true, "severity": 0, "expected": "PreCompact has time to write", "observed": "not-yet-implemented", "ts": 1767225510000},
  {"id": "precompact.custom_instructions_accepted", "ok": true, "severity": 0, "expected": "custom_instructions accepted", "observed": "not-yet-implemented", "ts": 1767225510000},
  {"id": "hook.payload_shape", "ok": true, "severity": 0, "expected": "hook payload shape matches hookio.Event", "observed": "not-yet-implemented", "ts": 1767225510000},
  {"id": "mcp.server_registered", "ok": true, "severity": 0, "expected": "MCP server received initialize", "observed": "not-yet-implemented", "ts": 1767225510000},
  {"id": "transcript.readable", "ok": true, "severity": 0, "expected": "transcript_path exists and parses", "observed": "not-yet-implemented", "ts": 1767225510000},
  {"id": "plugin.root_resolves", "ok": true, "severity": 0, "expected": "CLAUDE_PLUGIN_ROOT expands to an existing binary", "observed": "not-yet-implemented", "ts": 1767225510000}
]
```

The `state` struct in `monitor.go` that produces the on-disk `contract.json` shape (results
embedded as `[]Result`, mode persisted as its **string** spelling):
```go
type state struct {
	Mode      string         `json:"mode"`
	Reason    string         `json:"reason,omitempty"`
	Since     core.UnixMilli `json:"since"`
	CleanRuns int            `json:"cleanRuns"`
	Results   []Result       `json:"results,omitempty"`
}
```

### 2.3 monitor_test.go / standard_test.go / internal_test.go — pinned behaviors, strings, shapes

**Exact "not-yet-implemented" string** — appears independently in both `standard.go` and
`contracttest/suite.go` (each comment notes it is matched literally elsewhere, e.g. by
`test/guards`):
```go
// standard.go:12
const notYetImplementedObserved = "not-yet-implemented"
// contracttest/suite.go:54
const notYetImplementedObserved = "not-yet-implemented"
```
Used by unexported `notYetImplemented(id ID, declared Severity, desc string) Assertion`, whose
`Check` always returns:
```go
return Result{
	ID: id, OK: true, Severity: SevInfo,
	Expected: desc, Observed: notYetImplementedObserved, TS: core.NowMilli(e.Clock),
}
```

**Severity** (`severity.go`):
```go
type Severity uint8

const (
	SevInfo Severity = iota
	SevWarn
	SevCritical
)

func (s Severity) label() string // unexported: "info" | "warn" | "critical" | "unknown"
```
`mode_test.go::TestSeverity_ValuesAreOrdered` pins `SevInfo < SevWarn < SevCritical`.

**Mode strings**, pinned by `mode_test.go::TestMode_StringIsFrozen`:
```
ModeFull.String()            == "full"
ModeDegradedPassive.String() == "degraded-passive"
ModeOff.String()             == "off"
Mode(99).String()            == "unknown"   // total function, never blank
```

**monitor_test.go pinned behaviors**:
- Fresh Monitor + `StandardAssertions()` → `ModeFull`, all 9 results `OK:true`,
  `Severity:SevInfo`, `Observed:"not-yet-implemented"`.
- Flipping exactly one standard assertion (`CSessionStartFires`) to `OK:false, SevCritical` →
  `ModeDegradedPassive`; LOUD log captured non-empty; `state["mode"] == "degraded-passive"`;
  `state["reason"]` contains the ID; `obs.Registry` counter `contract.degrade == 1`.
- A failing `SevWarn`/`SevInfo`-only run → `ModeFull`, and **no** `contract.json` is written at
  all (`os.IsNotExist`) — "a run that changes nothing must not write state/contract.json".
- Two-consecutive-clean-runs-restore state machine (first clean run does not restore; streak
  resets on further failure); exactly 2 LOUD lines total for one degrade + one restore; counter
  `contract.restore == 1`.
- `TestMonitor_DegradationSurvivesIntoTheNextSession` — a second `NewMonitor` over the same
  `statePath` starts already `ModeDegradedPassive` and `Report()` replays the persisted failing
  result.
- `TestMonitor_CorruptStateFileFallsBackToModeFull` — `"{not json"`, `{"mode":"sideways"}`, `""`
  all fall back to `ModeFull` (§12.3 "fail toward do nothing").
- `TestMonitor_DegradeWritesLoudLog` — `.qompack/logs/LOUD.log` contains "degrading to passive
  recording" and the failing assertion's ID.
- `TestMonitor_DegradeIsLoudWithoutAStatePath` / `TestMonitor_NilLoggerAndRegistryAreTolerated` —
  `NewMonitor(nil, nil, "")` never panics and still degrades LOUD-ly.
- `TestMonitor_RunAllToleratesANilClock` — `RunAll` with `Env{}` (nil Clock) still timestamps
  every result (substitutes `core.SystemClock()`).
- `TestMonitor_ReportIsACopy` — mutating a `Report()` slice does not affect internal state.
- `TestMonitor_ModeOffIsNeverEnteredOrLeftAutomatically` — a monitor loaded with persisted
  `{"mode":"off",...}` keeps reporting `ModeOff` from `RunAll` even given a critical failing
  assertion, and never rewrites the operator's `"off"` mode on disk.
- `TestMonitor_DegradeReasonNamesExpectedAndObserved` — persisted `reason` and results carry both
  Expected and Observed text verbatim.

**standard_test.go pinned behaviors**:
- `TestStandardAssertions_MatchTheNormativeTable` — pins exact order, ID, declared Severity, and
  Description of all 9 `StandardAssertions()` entries.
- `TestStandardAssertions_IDsAreUnique` — exactly 9 unique IDs.
- `TestStandardAssertions_EveryCheckReportsNotYetImplemented` — every `Check` reports
  `OK:true, Severity:SevInfo, Observed:"not-yet-implemented", Expected:<Description>`, stamped
  from `Env.Clock` (not wall time).
- `TestStandardAssertions_DeclaredSeveritiesAreNotFlattened` — declared severities: **4
  SevCritical, 4 SevWarn, 1 SevInfo**.

**internal_test.go pinned behaviors** (package `contract`, white-box):
- `TestParseMode_RoundTripsEveryMode` / `TestParseMode_RejectsUnrecognizedValues` — unexported
  `parseMode` is `String`'s exact inverse for the 3 modes; rejects `""`, `"unknown"`, `"FULL"`,
  `"degraded"`, `"passive"`, always falling back to `(ModeFull, false)`.
- `TestSeverity_Label` — `"info"`, `"warn"`, `"critical"`, `Severity(99).label() == "unknown"`.
- `TestDegradeReason_IsOrderIndependent` — unexported `degradeReason([]Result)` is order-
  independent.
- `TestDegradeReason_IgnoresPassingAndNonCriticalResults` — passing critical + failing warn →
  `"no critical assertion failed"`.
- `TestFailedSummary_ListsEveryFailureWithItsSeverity` — unexported `failedSummary([]Result)`
  pinned output: `"precompact.has_time_to_write/warn,session_start.fires/critical"`.
- `TestLatestTS_ReturnsTheNewestStamp` — `latestTS(nil) == 0`;
  `latestTS([]Result{{TS:3},{TS:7},{TS:5}}) == 7`.

**contract_test.go** (in `test/guards`, not `internal/contract` — see §14 below) pins that an
assertion whose producer is absent from the build reports `OK=true, Severity=SevInfo,
Observed="not-yet-implemented"` — never a failure — while the assertion keeps its declared
severity for later real wiring. Exact strings: `"a freshly built develop must report ModeFull
(§12.1)"`, `"assertion %s: an absent producer reports OK, never a failure (§12.1)"`, `"assertion
%s: an absent producer reports SevInfo regardless of its declared severity"`.

### 2.4 `History` interface, verbatim

`internal/contract/assertion.go` (lines 65-78):
```go
// History is the observed-hook-firing record an Env carries (SP-01's decided spelling for the type
// §5.19 names but does not define). Implementations persist across sessions: Sessions counts how
// many sessions have been observed in total, which is what an assertion phrased as "absence across
// two sessions" needs in order to distinguish "not seen yet" from "not seen, twice".
type History interface {
	// Saw reports whether id has been observed at least once, this session or a prior one.
	Saw(id ID) bool
	// LastSeen returns when id was last observed, and false if it never was.
	LastSeen(id ID) (core.UnixMilli, bool)
	// Record notes an observation of id at time at.
	Record(id ID, at core.UnixMilli)
	// Sessions returns the number of sessions observed.
	Sessions() int
}
```

`Env` carries a `History` field (`assertion.go` line 62):
```go
type Env struct {
	ProjectRoot string
	Event       hookio.Event
	Cfg         config.Config
	Store       store.Store
	Log         logging.Logger
	Clock       core.Clock
	History History
}
```

**Gap for SP-05**: `contract` ships **no constructor and no implementation** of `History` at all.
`Monitor.RunAll` does **not** populate `e.History` — no file in `internal/contract` (including
`monitor.go`) ever reads or writes `Env.History`; it is read only by `Assertion.Check` functions,
none of which exist yet (all are `notYetImplemented`).

### 2.5 Every `contract.History` reference repo-wide (grep)

```
internal/contract/assertion.go:59   // comment: "History is the cross-session record..."
internal/contract/assertion.go:62   History History                              (Env field)
internal/contract/assertion.go:65   // History is the observed-hook-firing record...
internal/contract/assertion.go:69   type History interface {                     (declaration)

internal/contract/contracttest/suite.go:1     doc comment referencing contract.Monitor and contract.History
internal/contract/contracttest/suite.go:16    doc comment: "contract.History has no implementation in this package at all..."
internal/contract/contracttest/suite.go:135   doc comment: "RunHistorySuite is the conformance suite for contract.History..."
internal/contract/contracttest/suite.go:138   func RunHistorySuite(t *testing.T, name string, factory func(t *testing.T) contract.History)
internal/contract/contracttest/suite.go:295   func isStubHistory(t *testing.T, factory func(t *testing.T) contract.History) bool
internal/contract/contracttest/suite.go:304   func skipIfStubHistory(t *testing.T, factory func(t *testing.T) contract.History) bool

internal/contract/contracttest/behaviour.go:199  func runHistoryRecordThenSawCase(...)
internal/contract/contracttest/behaviour.go:214  func runHistoryLastSeenCase(...)
internal/contract/contracttest/behaviour.go:236  func runHistoryUnknownIDCase(...)
internal/contract/contracttest/behaviour.go:250  func runHistorySessionsMonotoneCase(...)

internal/contract/contracttest/suite_test.go:120, 146, 155  three RunHistorySuite(...) calls
```
No other file in the repo references `contract.History` or constructs/consumes `Env.History`.
(The only unrelated `History` hit elsewhere is `store.Store.FileHistory(...)` — a distinct,
unrelated method for file version history, not contract observation history.)

### 2.6 `Mode` type verbatim, `ParseMode` export status

`internal/contract/mode.go`, full file:
```go
package contract

// Mode is the observable degradation state of the plugin (00-ARCHITECTURE.md §5.19, §12.1).
type Mode uint8

// The three modes.
//
// ModeDegradedPassive is §12.1's contract-failure state: L0 and L1 keep running (observe, chunk,
// store, sketches, DAG, verbatim capture, elimination records — the store stays correct and the
// session's data is not lost), and everything that ACTS is off (no additionalContext injection, no
// customInstructions, no scheduler-initiated checkpoints, no drop report). ModeOff is the operator
// decision — runtime.mode = "off" — not a state the monitor ever enters on its own.
const (
	ModeFull Mode = iota
	ModeDegradedPassive
	ModeOff
)

// String returns the mode's canonical spelling. These three strings are the ones §12 uses in prose
// and /qompack:status prints, and they are the on-disk spelling in state/contract.json, so they are
// frozen: a rename is a data-format change, not a cosmetic one.
func (m Mode) String() string {
	switch m {
	case ModeFull:
		return "full"
	case ModeDegradedPassive:
		return "degraded-passive"
	case ModeOff:
		return "off"
	}
	return "unknown"
}

// parseMode is String's inverse, used only when reading state/contract.json back. It reports false
// for anything it does not recognize — including "unknown" — so a corrupt or future-format state
// file falls back to ModeFull (§12.3: everything else fails toward "do nothing") rather than
// leaving the session in a mode nobody chose.
func parseMode(s string) (Mode, bool) {
	for _, m := range []Mode{ModeFull, ModeDegradedPassive, ModeOff} {
		if m.String() == s {
			return m, true
		}
	}
	return ModeFull, false
}
```

**`ParseMode` is NOT exported** — it is `parseMode` (lowercase), unexported. There is **no
`Mode.MayAct`/`MayRecord`-style method** anywhere in the package — only `String()` exists on
`Mode`.

### 2.7 Exported symbols: mode.go, monitor.go, standard.go, severity.go, ids.go

```go
// ids.go
type ID string

const (
	CSessionStartFires         ID = "session_start.fires"
	CSessionStartSourceCompact ID = "session_start.source_compact"
	CAdditionalContext         ID = "hook.additional_context_delivered"
	CPreCompactTiming          ID = "precompact.has_time_to_write"
	CPreCompactCustomInstr     ID = "precompact.custom_instructions_accepted"
	CHookPayloadShape          ID = "hook.payload_shape"
	CMCPRegistered             ID = "mcp.server_registered"
	CTranscriptReadable        ID = "transcript.readable"
	CPluginRootResolves        ID = "plugin.root_resolves"
)

// standard.go
func StandardAssertions() []Assertion
// (notYetImplementedObserved const and notYetImplemented(...) helper unexported)

// monitor.go
type Monitor interface {
	Register(a Assertion) error
	RunAll(ctx context.Context, e Env) ([]Result, Mode)
	Mode() Mode
	Degrade(reason string, results []Result)
	Restore(reason string)
	Report() []Result
}

func NewMonitor(log logging.Logger, m obs.Registry, statePath string) Monitor
// (cleanRunsToRestore=2, statePerm=0o600, state struct, monitor struct+methods, degradeReason,
// failedSummary, latestTS all unexported)

// assertion.go
type Result struct {
	ID       ID
	OK       bool
	Severity Severity
	Expected string
	Observed string
	TS       core.UnixMilli
	Detail   string
}

type Assertion struct {
	ID          ID
	Severity    Severity
	Description string
	Check func(ctx context.Context, e Env) Result
}
```

`internal/contract/doc.go`: import allow-set is `hookio`, `store`, plus foundation. SP-01 ships
Monitor mechanics for real (Register/RunAll/Mode/Degrade/Restore/Report, §12.1 state machine,
`state/contract.json` persistence, LOUD logging) and `StandardAssertions`' nine entries with real
declared severities but all-`notYetImplemented` Checks. SP-05 owns replacing each `Assertion.Check`
with a real observation, and — per the History gap above — presumably also owns the first real
`contract.History` implementation.

---

## 3. `internal/hookio`

`internal/hookio/event.go` (lines 13-37) — `Event` struct, verbatim:
```go
type Event struct {
	HookEventName  string         `json:"hook_event_name"`
	SessionID      core.SessionID `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	CWD            string         `json:"cwd"`
	Source string `json:"source"`   // SessionStart's discriminator: startup|resume|compact|clear
	Trigger        string          `json:"trigger"`  // PreCompact's discriminator: manual|auto
	ToolName       string          `json:"tool_name"`
	ToolUseID      core.ToolUseID  `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	Prompt         string          `json:"prompt"`
	StopHookActive bool            `json:"stop_hook_active"`
	Extra map[string]json.RawMessage `json:"-"`
}
```

`internal/hookio/output.go` (lines 8-16) — `Output` struct, verbatim:
```go
type Output struct {
	Continue           *bool  `json:"continue,omitempty"`
	SuppressOutput     *bool  `json:"suppressOutput,omitempty"`
	HookSpecificOutput *HSO   `json:"hookSpecificOutput,omitempty"`
	SystemMessage      string `json:"systemMessage,omitempty"`
}
```

HSO-equivalent type (`internal/hookio/output.go` lines 18-33):
```go
type HSO struct {
	HookEventName string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext,omitempty"`
	CustomInstructions string `json:"customInstructions,omitempty"`
}

const (
	hookEventSessionStart = "SessionStart"
	hookEventPreCompact   = "PreCompact"
)
```

Signatures:
```go
// event.go:76-90
func ReadEvent(r io.Reader, limit int64) (Event, []byte, error)

// output.go:35-59
func WriteOutput(w io.Writer, o Output) error
func Empty() Output { return Output{} }
func SessionStartOutput(ctx string) Output {
	return Output{HookSpecificOutput: &HSO{HookEventName: hookEventSessionStart, AdditionalContext: ctx}}
}
func PreCompactOutput(instr string) Output {
	return Output{HookSpecificOutput: &HSO{HookEventName: hookEventPreCompact, CustomInstructions: instr}}
}
```

**Limits/budget behavior**: `ReadEvent` reads `limit+1` bytes so it can distinguish "exactly limit
bytes" from "larger than limit" without holding more than `limit+1` bytes in memory:
```go
raw, err := io.ReadAll(io.LimitReader(r, limit+1))
if err != nil {
	return Event{}, raw, fmt.Errorf("hookio: ReadEvent: read: %w", err)
}
if int64(len(raw)) > limit {
	truncated := raw[:limit]
	return Event{}, truncated, fmt.Errorf("%w: hook payload exceeds %d byte limit", core.ErrBudget, limit)
}
```
Confirmed by `event_test.go:166-173` (`TestReadEvent_LimitExceeded`):
`require.ErrorIs(t, err, core.ErrBudget)`. Test default limit constant:
`const defaultTestLimit = 1 << 20 // 1 MiB, matching runtime.hotPath.maxPayloadBytes's default`.
`WriteOutput` has **no** size limit — unbounded.

---

## 4. `internal/config`

### 4.1 Full `Runtime` config tree, verbatim (`internal/config/runtime.go`)

```go
type RuntimeCfg struct {
	Mode      string        `json:"mode" doc:"overall operating mode" enum:"auto|full|passive|off" sec:"00-ARCH §12"`
	Daemon    DaemonCfg     `json:"daemon"`
	HotPath   HotPathCfg    `json:"hotPath"`
	Logging   LogCfg        `json:"logging"`
	Redact    RedactCfg     `json:"redact"`
	Telemetry TelemetryCfg  `json:"telemetry"`
	Rehydrate RehydrateCfg  `json:"rehydrate"`
	MCP       MCPCfg        `json:"mcp"`
	Budgets   BudgetsCfg    `json:"budgets"`
	Selection RSelectionCfg `json:"selection"`
	Tokens    RTokensCfg    `json:"tokens"`
}

type DaemonCfg struct {
	Enabled           bool `json:"enabled"           doc:"run the resident per-project daemon" sec:"00-ARCH §2.4"`
	IdleExitSeconds   int  `json:"idleExitSeconds"   doc:"seconds with zero live sessions before the daemon exits" sec:"00-ARCH §2.4"`
	MaxSessions       int  `json:"maxSessions"       doc:"maximum concurrent sessions the daemon tracks" sec:"00-ARCH §2.4"`
	AckDeadlineMs     int  `json:"ackDeadlineMs"     doc:"deadline for the daemon's one-byte ACK on the hot path" sec:"00-ARCH §2.4"`
	ConnectDeadlineMs int  `json:"connectDeadlineMs" doc:"deadline for a hot-path client to connect to the daemon" sec:"00-ARCH §2.4"`
}

type HotPathCfg struct {
	BudgetMs        int  `json:"budgetMs"        doc:"B-A hot-path latency budget in milliseconds" rng:"(0,∞)" sec:"§8.1"`
	BreachWindows   int  `json:"breachWindows"   doc:"consecutive 512-sample windows over budget before the daemon spools instead of syncing" rng:"[1,∞)" sec:"§8.1"`
	SpoolOnBreach   bool `json:"spoolOnBreach"   doc:"degrade to spool-only mode when the hot-path budget is breached" sec:"§8.1"`
	MaxPayloadBytes int  `json:"maxPayloadBytes" doc:"maximum NDJSON request line size accepted by the daemon" rng:"[4096,∞)" sec:"00-ARCH §2.4"`
}

type LogCfg struct {
	Level     string `json:"level"     doc:"minimum log level written to the day log" enum:"debug|info|warn|error" sec:"00-ARCH §5.2"`
	MaxFileMB int    `json:"maxFileMB" doc:"log file size in MB before rotation" sec:"00-ARCH §5.2"`
	MaxFiles  int    `json:"maxFiles"  doc:"number of rotated log files retained" sec:"00-ARCH §5.2"`
}

type RedactCfg struct {
	Enabled  bool     `json:"enabled"  doc:"scrub secrets before content enters the store" sec:"00-ARCH §5.23"`
	Patterns []string `json:"patterns" doc:"additional user-supplied secret-detection patterns" sec:"00-ARCH §5.23"`
}

type TelemetryCfg struct {
	Enabled bool `json:"enabled" doc:"send telemetry; hardwired off, the key exists only to say so" sec:"§7.1"`
}

type RehydrateCfg struct {
	MinTokens        int `json:"minTokens"        doc:"lower bound of the rehydration budget" rng:"[1,maxTokens]" sec:"§8.6"`
	MaxTokens        int `json:"maxTokens"        doc:"upper bound of the rehydration budget" rng:"[minTokens,∞)" sec:"§8.6"`
	SkillIndexTokens int `json:"skillIndexTokens" doc:"token budget for the compact skill index" rng:"[1,∞)" sec:"§8.6"`
	EliminationsTopN int `json:"eliminationsTopN" doc:"number of eliminated approaches surfaced verbatim in the rehydrated digest" rng:"[1,∞)" sec:"§8.6"`
}

type MCPCfg struct {
	SpanWidenLines   int `json:"spanWidenLines"   doc:"lines to widen a minimal span by when the caller requests more context" rng:"[0,∞)" sec:"§8.7"`
	MaxResponseBytes int `json:"maxResponseBytes" doc:"maximum bytes an MCP tool response may return" rng:"[4096,∞)" sec:"§8.7"`
}

type BudgetsCfg struct {
	L0IngestMs           int `json:"l0IngestMs"           doc:"B-B latency budget: daemon read to WAL append returned"                      rng:"(0,∞)" sec:"00-ARCH §2.4"`
	L0ProcessMs          int `json:"l0ProcessMs"          doc:"B-C latency budget: WAL to fully chunked, stored, DAG/sketches updated"       rng:"(0,∞)" sec:"00-ARCH §2.4"`
	CheckpointFinalizeMs int `json:"checkpointFinalizeMs" doc:"B-E latency budget: PreCompact entry to exit"                                 rng:"(0,∞)" sec:"00-ARCH §2.4"`
	MCPToolCallMs        int `json:"mcpToolCallMs"        doc:"B-F latency budget: MCP request to response"                                  rng:"(0,∞)" sec:"00-ARCH §2.4"`
}

type RSelectionCfg struct {
	SubmodularEnabled bool `json:"submodularEnabled" doc:"ship-order gate: enable submodular selection; refused without p-selection (closing-note-3)" sec:"Closing note"`
}

type RTokensCfg struct {
	ProseCharsPerToken  float64 `json:"proseCharsPerToken"  doc:"baseline characters-per-token estimate for prose"                 rng:"[1,20]" sec:"00-ARCH §10 (G10.2)"`
	CodeCharsPerToken   float64 `json:"codeCharsPerToken"   doc:"baseline characters-per-token estimate for source code"           rng:"[1,20]" sec:"00-ARCH §10 (G10.2)"`
	JSONCharsPerToken   float64 `json:"jsonCharsPerToken"   doc:"baseline characters-per-token estimate for JSON"                  rng:"[1,20]" sec:"00-ARCH §10 (G10.2)"`
	DiffCharsPerToken   float64 `json:"diffCharsPerToken"   doc:"baseline characters-per-token estimate for unified diffs"         rng:"[1,20]" sec:"00-ARCH §10 (G10.2)"`
	BinaryCharsPerToken float64 `json:"binaryCharsPerToken" doc:"baseline characters-per-token estimate for opaque binary content" rng:"[1,20]" sec:"00-ARCH §10 (G10.2)"`

	ImagePixelsPerToken int `json:"imagePixelsPerToken" doc:"pixels per token used to estimate image cost from dimensions" rng:"[1,∞)" sec:"00-ARCH §10 (G10.2)"`
	ImageMaxTokens      int `json:"imageMaxTokens"      doc:"cap on estimated tokens for a single image"                   sec:"00-ARCH §10 (G10.2)"`
	PDFTokensPerPage    int `json:"pdfTokensPerPage"    doc:"tokens charged per PDF page"                                  rng:"[1,∞)" sec:"00-ARCH §10 (G10.2)"`

	CalibrationMin   float64 `json:"calibrationMin"   doc:"lower clamp on the per-project calibration factor"                        rng:"(0,1]" sec:"00-ARCH §10 (G10.2)"`
	CalibrationMax   float64 `json:"calibrationMax"   doc:"upper clamp on the per-project calibration factor"                        rng:"[1,∞)" sec:"00-ARCH §10 (G10.2)"`
	CalibrationAlpha float64 `json:"calibrationAlpha" doc:"exponential-moving-average weight applied to each new calibration sample" rng:"(0,1]" sec:"00-ARCH §10 (G10.2)"`
}
```

**Structural note**: `RuntimeCfg` does NOT embed `SchedulerCfg` — `Scheduler` is a top-level field
of `Config` (root), defined in `config.go`, not nested under Runtime.

### 4.2 `Scheduler.Idle` struct (in `internal/config/config.go`, not runtime.go)

```go
// SchedulerCfg is the L3 scheduler's configuration (Appendix C "scheduler").
type SchedulerCfg struct {
	SoftFloorPct      float64        `json:"softFloorPct"      doc:"fraction of the effective context window below which compaction never fires" rng:"(0,1)" sec:"§8.4"`
	HardCeilingMargin int            `json:"hardCeilingMargin" doc:"token headroom below Claude Code's own auto-compact threshold at which the plugin forces a checkpoint" rng:"(0,∞)" sec:"§2.5"`
	YoungDaly         YoungDalyCfg   `json:"youngDaly"`
	Changepoint       ChangepointCfg `json:"changepoint"`
	Cache             CacheCfg       `json:"cache"`
	Idle              IdleCfg        `json:"idle"`
}

// IdleCfg controls §8.4 idle-time detection and background work.
type IdleCfg struct {
	DetectAfterSeconds int  `json:"detectAfterSeconds" doc:"seconds of inactivity before the session is considered idle" sec:"§8.4"`
	BackgroundWork     bool `json:"backgroundWork"     doc:"run idle-time background work (shadow checkpoint, GC, slice/Δ-score refresh)" sec:"§8.4"`
	DeepCutWhenCold    bool `json:"deepCutWhenCold"    doc:"prefer a deep cut once the cache is provably cold during an idle gap" sec:"§8.4"`
}
```

### 4.3 `config.Env`

`internal/config/config.go` (lines 243-257):
```go
type Env struct {
	ProjectRoot string
	HomeDir     string
	Getenv      func(string) string
	Flags       map[string]string // from the invoking subcommand's --set k=v
}
```

### 4.4 `config.Load`

`internal/config/load.go` (lines 15-26):
```go
// Load composes five layers, deep-merged per leaf key, lowest precedence first: built-in
// defaults, the user-global file, the project file, QOMPACK_* environment variables, and --set
// flags. It returns a non-nil error only when env.ProjectRoot is empty — every other problem is
// reported through the returned []Warning instead.
func Load(env Env) (Config, Provenance, []Warning, error)
```

### 4.5 `Defaults()` — actual runtime/scheduler values (`internal/config/defaults.go`)

Store (sketch-adjacent, shown for completeness):
```go
Store: StoreCfg{
	Chunk: ChunkCfg{Min: 1024, Target: 4096, Max: 16384},
	Compression: "zstd",
	Retention: RetentionCfg{Days: 30, Sessions: 10},
	Canonicalize: CanonicalizeCfg{
		Enabled: true,
		Strip:   []string{"timestamps", "ansi", "pids", "addresses", "tmpPaths", "durations"},
		MinHash: MinHashCfg{Enabled: true, Permutations: 128, NearDupThreshold: 0.9},
	},
},
```

Scheduler:
```go
Scheduler: SchedulerCfg{
	SoftFloorPct:      0.55,
	HardCeilingMargin: 20000,
	YoungDaly:         YoungDalyCfg{Enabled: true, MeasuredDeltaSeconds: nil},
	Changepoint:       ChangepointCfg{HazardRate: 0.004, Features: []string{"paths", "tools", "time", "todos"}},
	Cache:             CacheCfg{ReadMultiplier: 0.1, WriteMultiplier: 1.25, TTLSeconds: 300},
	Idle:              IdleCfg{DetectAfterSeconds: 120, BackgroundWork: true, DeepCutWhenCold: true},
},
```

Runtime:
```go
Runtime: RuntimeCfg{
	Mode: "auto",
	Daemon: DaemonCfg{Enabled: true, IdleExitSeconds: 1800, MaxSessions: 8, AckDeadlineMs: 8, ConnectDeadlineMs: 5},
	HotPath: HotPathCfg{BudgetMs: 15, BreachWindows: 3, SpoolOnBreach: true, MaxPayloadBytes: 1048576},
	Logging: LogCfg{Level: "info", MaxFileMB: 10, MaxFiles: 5},
	Redact: RedactCfg{Enabled: true, Patterns: []string{}},
	Telemetry: TelemetryCfg{Enabled: false},
	Rehydrate: RehydrateCfg{MinTokens: 8000, MaxTokens: 12000, SkillIndexTokens: 450, EliminationsTopN: 8},
	MCP: MCPCfg{SpanWidenLines: 40, MaxResponseBytes: 262144},
	Budgets: BudgetsCfg{L0IngestMs: 2, L0ProcessMs: 50, CheckpointFinalizeMs: 2000, MCPToolCallMs: 250},
	Selection: RSelectionCfg{SubmodularEnabled: false},
	Tokens: RTokensCfg{
		ProseCharsPerToken: 4.0, CodeCharsPerToken: 3.6, JSONCharsPerToken: 3.2,
		DiffCharsPerToken: 3.4, BinaryCharsPerToken: 3.0,
		ImagePixelsPerToken: 750, ImageMaxTokens: 1600, PDFTokensPerPage: 1800,
		CalibrationMin: 0.6, CalibrationMax: 1.6, CalibrationAlpha: 0.2,
	},
},
```

### 4.6 Warning / Provenance types

Both live in `config.go` (NOT `provenance.go` — that file holds only `Provenance.Render`):
```go
type Origin uint8   // OriginDefault < OriginUserFile < OriginProjectFile < OriginEnv < OriginFlag

type Source struct {
	Origin   Origin
	Location string
}

type Provenance map[string]Source

type Warning struct {
	Key      string
	Message  string
	Location string
}

type Violation struct {
	Key     string
	Message string
	Got     any
	Want    any
}
```
`Provenance.Render(cfg Config, w io.Writer) error` is in `provenance.go:21`.

---

## 5. `internal/obs`

### 5.1 Histogram / HistSnapshot / Counter / Gauge, verbatim

`internal/obs/hist.go` (lines 10-29):
```go
type Histogram interface {
	Observe(d time.Duration)
	Snapshot() HistSnapshot
	Reset()
}

type HistSnapshot struct {
	N    int64
	P50  time.Duration
	P95  time.Duration
	P99  time.Duration
	P999 time.Duration
	Max  time.Duration
}
```

`internal/obs/counter.go` (lines 5-18):
```go
type Counter interface {
	Add(n int64)
	Value() int64
}

type Gauge interface {
	Set(v int64)
	Add(d int64)
	Value() int64
}
```

### 5.2 `obs.New(clock)` constructor

`internal/obs/registry.go` (lines 71-80):
```go
func New(clock core.Clock) Registry {
	return &registry{
		clock:        clock,
		hists:        make(map[string]*histogram),
		counters:     make(map[string]*counter),
		gauges:       make(map[string]*gauge),
		breachStreak: make(map[BudgetID]int),
	}
}
```

### 5.3 `Timed`

`internal/obs/registry.go` (lines 176-183):
```go
func Timed(h Histogram, f func() error) error {
	start := time.Now()
	err := f()
	h.Observe(time.Since(start))
	return err
}
```

### 5.4 `Registry` interface (`Persist`, `CheckBudgets`, etc.)

`internal/obs/registry.go` (lines 39-51):
```go
type Registry interface {
	Hist(name string) Histogram
	Counter(name string) Counter
	Gauge(name string) Gauge
	Snapshot() Snapshot
	// CheckBudgets evaluates every GATED budget's current percentile against cfg and returns one
	// BudgetBreach per budget presently over limit. Stateful: Windows counts consecutive
	// over-limit calls per budget, maintained inside the Registry across calls.
	CheckBudgets(cfg config.Config) []BudgetBreach
	// Persist writes the current Snapshot to l.Metrics/latency.json via paths.WriteAtomic.
	Persist(l paths.Layout) error
}
```
Concrete: `func (r *registry) Persist(l paths.Layout) error` (registry.go:164).

### 5.5 `Budgets()` return shape

`internal/obs/budgets.go` (lines 47-63):
```go
type Budget struct {
	ID    BudgetID
	Hist  string
	Pct   int
	Gated bool
	Limit func(config.Config) time.Duration
}

func Budgets() []Budget
```

### 5.6 Histogram name constants and `Budget.Hist` reachability

`internal/obs/budgets.go` (lines 32-39) — unexported string constants:
```go
const (
	histHookControlled     = "hook_controlled"
	histL0Ingest           = "l0_ingest"
	histL0Process          = "l0_process"
	histHookWall           = "hook_wall"
	histCheckpointFinalize = "checkpoint_finalize"
	histMCPToolCall        = "mcp_tool_call"
)
```
Wired via the exported `Budget.Hist` field inside `Budgets()`, e.g.:
```go
{
	ID: BA, Hist: histHookControlled, Pct: pctP99, Gated: true,
	Limit: func(c config.Config) time.Duration {
		return time.Duration(c.Runtime.HotPath.BudgetMs) * time.Millisecond
	},
},
```
(full list: BA/histHookControlled, BB/histL0Ingest, BC/histL0Process, BD/histHookWall,
BE/histCheckpointFinalize, BF/histMCPToolCall). Since `Hist` is a plain exported `string` field,
callers reach the value externally via `obs.Budgets()[i].Hist`, never the unexported constant
identifier.

---

## 6. `internal/paths`

### 6.1 `Layout` struct, verbatim

`internal/paths/layout.go` (lines 19-24):
```go
type Layout struct {
	Root, Dot                                      string // project root, <root>/.qompack
	Objects, Index, Sketches, DAG                  string
	Grammar, Checkpoints, Pins, Eval               string
	Records, State, Run, Spool, Logs, Metrics, Tmp string
}
```

### 6.2 `Of` / `EnsureLayout`

```go
// layout.go:26-28
func Of(root string) Layout

// layout.go:51-56
// EnsureLayout creates every directory l names with 0o700 permissions, then writes
// <root>/.qompack/.gitignore containing exactly "*\n" through WriteAtomic, unless it already
// exists (idempotent).
func EnsureLayout(l Layout) error
```

### 6.3 `WriteAtomic`, `AppendOnly`, `AppendJSONL`, `CreateNew`, `OpenFile`

```go
// atomic.go:88-94
func WriteAtomic(p string, b []byte, perm fs.FileMode) error

// appendonly.go:41-47
func OpenFile(p string, flag int, perm fs.FileMode) (*os.File, error)

// appendonly.go:60-63
func AppendOnly(p string) (io.WriteCloser, error)

// appendonly.go:75-85
func AppendJSONL(p string, v any) error

// appendonly.go:109-113
func CreateNew(p string, b []byte) error
```

### 6.4 `Resolve(getenv, payloadCWD)`

`internal/paths/resolve.go` (lines 15-26):
```go
func Resolve(getenv func(string) string, payloadCWD string) (string, error)
```
Order: `QOMPACK_PROJECT_ROOT` env → walk `payloadCWD` upward to nearest `.git` → `payloadCWD`
itself.

### 6.5 `Long` / `IsProtected`

```go
// long_windows.go:10-21 (//go:build windows)
func Long(p string) string

// long_other.go:5-7 (//go:build !windows) — identity function
func Long(p string) string { return p }

// appendonly.go:18-23
func IsProtected(root, p string) bool
```

---

## 7. `internal/core`

### 7.1 Sentinel errors

`internal/core/errors.go` (lines 7-25):
```go
var (
	ErrNotImplemented = errors.New("qompack: not implemented")
	ErrNotFound = errors.New("qompack: not found")
	// ErrAppendOnly is the §7.4 invariant.
	ErrAppendOnly = errors.New("qompack: append-only violation")
	// ErrAlreadyEncoded is the DPI guard of §4.6.
	ErrAlreadyEncoded = errors.New("qompack: segment already encoded (DPI guard)")
	// ErrBudget signals a token, byte, or latency budget was exceeded.
	ErrBudget = errors.New("qompack: budget exceeded")
	// ErrDegraded signals the session is running in degraded-passive mode (§12).
	ErrDegraded = errors.New("qompack: running in degraded mode")
	// ErrContract signals a host hook-contract violation (G9.3, §12.1).
	ErrContract = errors.New("qompack: host contract violated")
)
```

### 7.2 `IsNotImplemented`

`internal/core/errors.go` (lines 27-30):
```go
func IsNotImplemented(err error) bool { return errors.Is(err, ErrNotImplemented) }
```

### 7.3 `ErrBudget`

Plain `error` sentinel (part of the `var` block above), NOT a distinct type:
`ErrBudget = errors.New("qompack: budget exceeded")`.

### 7.4 `Version`

`internal/core/version.go:6`:
```go
// Version is the plugin version. Overridden at link time by SP-17's release build via
// -X github.com/qompack/qompack/internal/core.Version=<tag>. Single source the plugin manifest,
// the MCP server handshake and `qompack version` all read.
var Version = "0.1.0"
```
There is **no** `Version()` function — it is a package-level `var Version string`.

### 7.5 `SessionID`, `ToolUseID`, `UnixMilli`, `TurnIndex` (and siblings)

`internal/core/ids.go` (lines 6-23):
```go
type (
	SessionID string
	ToolUseID string
	TurnIndex int
	SegmentID int
	CheckpointSeq int
	DecisionID string
	Tokens int
	UnixMilli int64
)
```
`clock.go:25` adds `func (t UnixMilli) Time() time.Time`.

### 7.6 `Hash` / `HashBytes`

`internal/core/hash.go`:
```go
type Hash [sha256.Size]byte    // :53
func HashBytes(domain string, b []byte) Hash { ... }   // :57
```

---

## 8. `internal/logging`

### 8.1 `Logger` interface, verbatim

`internal/logging/logger.go` (lines 20-32):
```go
type Logger interface {
	// With returns a derived Logger that prepends kv to every subsequent call.
	With(kv ...any) Logger
	Debug(msg string, kv ...any)
	Info(msg string, kv ...any)
	Warn(msg string, kv ...any)
	Error(msg string, kv ...any)
	// Loud writes to the day log AND to <dir>/LOUD.log (append-only, never rotated) AND appends
	// to the process-wide ring LastLoud reads AND fires the observer AttachLoudObserver
	// installed, if any. Reserved for contract violations and degradation transitions. Never
	// silent (§12).
	Loud(msg string, kv ...any)
}
```

### 8.2 Constructors

```go
// logger.go:60,66,83
func New(dir string, lvl Level) (Logger, io.Closer, error)
func NewWithLimits(dir string, lvl Level, maxFileMB, maxFiles int) (Logger, io.Closer, error)
func Nop() Logger { return logger{s: nil, minLevel: Loud} }
```

### 8.3 `Level`

`internal/logging/level.go`:
```go
type Level uint8

const (
	Debug Level = iota
	Info
	Warn
	Error
	Loud
)
```

### 8.4 `LastLoud` / `AttachLoudObserver`

```go
// loud.go:30
func LastLoud() []string
// loud.go:55
func AttachLoudObserver(fn func(msg string, kv ...any))
```

### 8.5 LOUD.log filename and write path

`internal/logging/logger.go:43`:
```go
const loudFileName = "LOUD.log"
```
Written from `(l logger) Loud(...)` (logger.go:114-125) → `l.s.writeLoud(line)` →
`internal/logging/logger.go:252-263`:
```go
func (s *sink) writeLoud(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loudFile == nil {
		w, err := paths.AppendOnly(filepath.Join(s.dir, loudFileName))
		if err != nil {
			return
		}
		s.loudFile = w
	}
	_, _ = io.WriteString(s.loudFile, line)
}
```
File is `<dir>/LOUD.log`, opened via `paths.AppendOnly`, append-only, never rotated (unlike the
day log).

---

## 9. `internal/testutil` — exported helpers by file

**clock.go**
```go
var Epoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
type FakeClock struct { ... }
func NewFakeClock(t0 time.Time) *FakeClock
func (c *FakeClock) Now() time.Time
func (c *FakeClock) Since(t time.Time) time.Duration
func (c *FakeClock) Advance(d time.Duration)
```

**project.go**
```go
const E2EBinaryEnv = "QOMPACK_E2E_BINARY"
type Project struct {
	Root  string
	Cfg   config.Config
	Clock *FakeClock
	Log   logging.Logger
	// home, env, seam unexported
}
type ProjectOpt func(*projectOpts)
func WithGit() ProjectOpt
func WithConfig(jsonText string) ProjectOpt
func WithEnv(k, v string) ProjectOpt
func WithFiles(files map[string]string) ProjectOpt        // option constructor
func WithClock(t0 time.Time) ProjectOpt
func NewProject(t *testing.T, opts ...ProjectOpt) *Project
func (p *Project) Getenv(k string) string
func (p *Project) Home() string
func (p *Project) Store(t *testing.T) store.Store
func (p *Project) WithFiles(t *testing.T, files map[string]string) *Project  // method, same name as option func
func HookNames() []string
func (p *Project) RunHook(t *testing.T, name string, e hookio.Event) hookio.Output
func (p *Project) AssertAppendOnly(t *testing.T)
```

**fixtures.go**
```go
const NotRecordedSkip = "contract fixture not yet recorded (Rule W-2)"
func ContractFixture(t *testing.T, pkg, name string) (input, want []byte, frozen bool)
```

**golden.go**
```go
func Golden(t *testing.T, name string, got []byte)
func GoldenJSON(t *testing.T, name string, v any)
```

**spawn.go** — no exported identifiers (`spawn`, `gitInit` unexported); the one file permitted to
import `os/exec`.

**winpath.go**
```go
const (
	SpacePathFile  = "src/my folder/a b.ts"
	CRLFFile       = "src/crlf.ts"
	ReadOnlyFile   = "src/readonly.ts"
	UpperCaseFile  = "src/Foo.ts"
	LowerCaseFile  = "src/foo.ts"
)
var LongPathFile = longPathName()
func WindowsHostileFiles() map[string]string
```

**doc.go** — package doc only, no declarations.

---

## 10. `internal/cli`, `cmd/qompack`, and `internal/commands`

### 10.1 `commands.go` — subcommand table

`internal/cli/commands.go` (lines 23-34):
```go
func All() []Cmd {
	cmds := hookCmds()
	cmds = append(cmds,
		Cmd{Name: "version", Summary: "print the plugin version", Run: runVersion},
		Cmd{Name: "config print", Summary: "print the effective configuration", Run: runConfigPrint},
		Cmd{Name: "config schema", Summary: "print the configuration JSON Schema", Run: runConfigSchema},
	)
	for _, ni := range notImplemented {
		cmds = append(cmds, Cmd{Name: ni.name, Summary: ni.summary, Run: notImplementedRun(ni.name)})
	}
	return cmds
}
```

`Cmd` type (`internal/cli/dispatch.go`, lines 35-45):
```go
type Cmd struct {
	// Name is the subcommand as typed, including a space for two-word forms ("observe tool").
	Name string
	Summary string
	// Hook marks a subcommand invoked by the host as a hook. Hook commands must ALWAYS exit 0
	// (§2.3), which Dispatch enforces regardless of what Run returns or panics with.
	Hook bool
	Run func(ctx context.Context, env Env, args []string, out, errw io.Writer) error
}
```

There is **no `commands.All` slice variable** — `All()` is a function returning `[]Cmd`, built
fresh each call from `hookCmds()` + 3 explicit entries + a `notImplemented` data table.

Every registered subcommand name (exact strings):
- Hooks (`hookCmds()`, `internal/cli/hooks.go:157-190`, all `Hook: true`): `"observe tool"`,
  `"observe prompt"`, `"observe stop"`, `"session-start"`, `"checkpoint"`, `"flush"`
- Direct: `"version"`, `"config print"`, `"config schema"`
- `notImplemented` table (`commands.go:38-51`): `"mcp"`, `"daemon"`, `"status"`, `"recall"`,
  `"pin"`, `"why"`, `"dropped"`, `"eval"`, `"fsck"`, `"doctor"`, `"bench"`, `"self-test"`

### 10.2 `config.go`

```go
// config.go:31
func LoadConfigAndReport(env config.Env, log logging.Logger, reg obs.Registry) (config.Config, config.Provenance, error)

// config.go:84
func AttachLoudCounter(reg obs.Registry)
```
(`persistViolations` unexported.)

### 10.3 `recover.go` — `runGuarded`

`internal/cli/recover.go:24` — **unexported**:
```go
func runGuarded(ctx context.Context, cmd Cmd, env Env, args []string, out, errw io.Writer) (err error)
```
Wraps `cmd.Run` with deferred `recover()`. On panic: logs via
`logging.Nop().Loud("panic in subcommand", "subcommand", cmd.Name, "recover", fmt.Sprint(r),
"stack", stack)`, writes a stderr line; for hook commands, if nothing was written yet writes
`hookio.Empty()` and forces `err = nil` (hooks never propagate failure); for non-hook commands
sets `err = fmt.Errorf("%w: panic: %v", errAlreadyReported, r)`. After a normal (non-panicking)
`cmd.Run`, if the command is a hook and nothing was written, also writes `hookio.Empty()`.

### 10.4 `osdir.go` — `isDir`

`internal/cli/osdir.go:13-16` — **unexported**:
```go
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
```

### 10.5 `cmd/qompack/main.go` — full content, verbatim

```go
// Command qompack is the single static binary that is the entire plugin runtime: hook entry
// points, the MCP server, the resident daemon, and the user-facing subcommands.
//
// It is dispatch only, under 150 lines, with no package-level initialization beyond variable
// declarations. That restraint is a latency requirement, not a style preference: this binary is
// spawned on the hot path of every tool call, and budget B-A (§2.4) allows 15 ms p99 for the whole
// process — spawn, connect, write, acknowledge, exit. Work done in init() is work every hook pays.
package main

import (
	"context"
	"os"

	"github.com/qompack/qompack/internal/cli"
	"github.com/qompack/qompack/internal/core"
)

func main() {
	env := cli.Env{
		Getenv: os.Getenv,
		Stdin:  os.Stdin,
		Clock:  core.SystemClock(),
	}
	os.Exit(cli.Dispatch(context.Background(), cli.All(), os.Args, env, os.Stdout, os.Stderr))
}
```

### 10.6 Does the CLI already ship `qompack version`? Yes.

`internal/cli/commands.go:26,62-66`:
```go
Cmd{Name: "version", Summary: "print the plugin version", Run: runVersion},
...
func runVersion(_ context.Context, _ Env, _ []string, out, _ io.Writer) error {
	_, err := fmt.Fprintln(out, core.Version)
	return err
}
```
Registered unconditionally inside `All()` (not gated behind `notImplemented`), so `qompack
version` is live in every build and prints `core.Version` (`"0.1.0"` by default).

### 10.7 Bonus: `internal/commands` package (distinct from `internal/cli`)

Composition-root package for the seven `/qompack:*` slash-command backends
(00-ARCHITECTURE.md §5.17):
```go
// :21-27
type Command interface {
	Name() string
	Run(ctx context.Context, args []string, out io.Writer) error
}

// :34-45
type Deps struct {
	Store       store.Store
	Ledger      negknow.Ledger
	Checkpoints checkpoint.Reader
	Writer      checkpoint.Writer
	Pins        pins.Store
	Sched       scheduler.Runtime
	Eval        eval.Harness
	Metrics     obs.Registry
	Contract    contract.Monitor
	Cfg         config.Config
}

// :56-62
func All(d Deps) []Command
// :66-70
func Names() []string

// :49
var commandNames = []string{"status", "recall", "pin", "checkpoint", "why", "dropped", "eval"}
```
Every command currently reports `core.ErrNotImplemented` via unexported `stubCommand.Run` until
SP-14 lands.

---

## 11. `internal/sketch`

### 11.1 Constructor signatures, verbatim

```go
// bloom.go:23
func NewBloom(capacity int, fpRate float64) *Bloom

// cms.go:16
func NewCMS(epsilon, delta float64) *CMS

// hll.go:15
func NewHLL(registers int) *HLL

// misragries.go:22
func NewMisraGries(k int) *MisraGries
```
There is **no `NewMinHash`** — MinHash is a free function, not a constructor:
```go
// minhash.go:31
func MinHash(data []byte, o MinHashOptions) Signature { return Signature{} }
```

### 11.2 Save/Load — stub status

Package-level `Save`/`Load` (`header.go:58-67`) are **stubs**:
```go
func Save(p string, s Sketch) error { return core.ErrNotImplemented }
func Load(p string, s Sketch) error { return core.ErrNotImplemented }
```
Every type's `Header()`/`MarshalBinary()`/`UnmarshalBinary()` are also stubs (e.g.
`bloom.go:69-75`, `cms.go:39-45`, `hll.go:30-36`, `misragries.go:39-45`, `minhash.go:44-47`) —
`MarshalBinary`/`UnmarshalBinary` all return `core.ErrNotImplemented`, `Header()` returns
`Header{}`. **All Save/Load/Marshal/Unmarshal bodies across the package are stubs; none has real
encode/decode logic yet.** Other operations (`Add`, `Test`, `Estimate`, `Cardinality`, `Top`,
`MergeFrom`, `Scale`, `HeavyHitters`, `Jaccard`, `IsNearDup`, `ResizeTarget`) are also stubs —
SP-01 ships the types, SP-03 lands the real math.

### 11.3 Sketch-related config fields (`internal/config/config.go`)

```go
type CanonicalizeCfg struct {
	Enabled bool       `json:"enabled" doc:"run per-tool canonicalizers before chunking" sec:"§8.1"`
	Strip   []string   `json:"strip"   doc:"canonicalizer classes to strip before chunking" enum:"timestamps|ansi|pids|addresses|tmpPaths|durations|crlf|paths" sec:"§8.1"`
	MinHash MinHashCfg `json:"minhash"`
}

type MinHashCfg struct {
	Enabled          bool    `json:"enabled"          doc:"compute a MinHash signature per stored result to detect near-duplicates" sec:"§8.1"`
	Permutations     int     `json:"permutations"     doc:"number of MinHash permutation functions" rng:"[16,512]" sec:"§8.1"`
	NearDupThreshold float64 `json:"nearDupThreshold" doc:"Jaccard similarity above which two results are treated as near-duplicates" rng:"(0,1]" sec:"§8.1"`
}

type SketchesCfg struct {
	Bloom BloomCfg `json:"bloom"`
	CMS   CMSCfg   `json:"cms"`
	HLL   HLLCfg   `json:"hll"`
}

type BloomCfg struct {
	Capacity int     `json:"capacity" doc:"expected number of entries the tried.bloom filter is sized for" rng:"[100,∞)"  sec:"Appendix A"`
	FPRate   float64 `json:"fpRate"   doc:"target false-positive rate of the tried.bloom filter"            rng:"(0,0.25)" sec:"Appendix A"`
}

type CMSCfg struct {
	Epsilon              float64 `json:"epsilon"               doc:"Count-Min sketch error factor ε" rng:"(0,1)" sec:"Appendix A"`
	Delta                float64 `json:"delta"                 doc:"Count-Min sketch failure probability δ" rng:"(0,1)" sec:"Appendix A"`
	WarmStartFromProject bool    `json:"warmStartFromProject"  doc:"seed the Count-Min sketch from project-scoped history at session start" sec:"§6.2"`
}

type HLLCfg struct {
	Registers int `json:"registers" doc:"HyperLogLog register count" rng:"power of two in [64,65536]" sec:"§6.2"`
}
```
Validation ranges: `sketches.bloom.capacity ≥ 100`, `sketches.bloom.fpRate ∈ (0,0.25)`,
`sketches.cms.epsilon ∈ (0,1)`, `sketches.cms.delta ∈ (0,1)`, `sketches.hll.registers` power of
two in `[64,65536]`, `store.canonicalize.minhash.permutations ∈ [16,512]`,
`store.canonicalize.minhash.nearDupThreshold ∈ (0,1]`.

---

## 12. `internal/daemon`

### 12.1 Exported types/funcs, verbatim (`daemon.go`)

```go
// :35-40
type SketchSet struct {
	Tried   *sketch.Bloom
	Touch   *sketch.CMS
	Explore *sketch.HLL
	Top     *sketch.MisraGries
}

// :48-67
type Options struct {
	ProjectRoot string
	Cfg         config.Config
	Log         logging.Logger
	Metrics     obs.Registry
	Clock       core.Clock

	Store       store.Store
	Ledger      negknow.Ledger
	Sketches    *SketchSet
	Graph       dag.Graph
	Grammar     grammar.Sequitur
	Sched       scheduler.Runtime
	Checkpoints checkpoint.Writer

	// handlers is the op-routing table: a map rather than a switch so a later wave adds an op by
	// calling Handle at wiring time instead of editing a function in this package.
	handlers map[ipc.Op]ipc.Handler
}

func (o *Options) Handle(op ipc.Op, h ipc.Handler)      // :74
func (o *Options) Handler(op ipc.Op) (ipc.Handler, bool) // :82
func (o *Options) Ops() []ipc.Op                         // :88

// :99-108
type IdleController interface {
	Register(name string, prio int, fn func(ctx context.Context) error)
	Notify(lastActivity core.UnixMilli)
	IsIdle(now core.UnixMilli) bool
	RunOnce(ctx context.Context, budget time.Duration) (ran []string, err error)
}

// :117-120
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[core.SessionID]*SessionState
}

// :123-127
type SessionState struct {
	ID           core.SessionID
	Hot          ipc.HotPathMode
	LastActivity core.UnixMilli
}

func NewSessionRegistry() *SessionRegistry             // :130
func (r *SessionRegistry) Get(id core.SessionID) (*SessionState, bool) // :135
func (r *SessionRegistry) Len() int                     // :143

// :150-162
type Daemon interface {
	Run(ctx context.Context) error
	Registry() *SessionRegistry
	Drain(ctx context.Context) (int, error)
	Idle() IdleController
	Stop(ctx context.Context) error
}

func New(o Options) (Daemon, error)   // :166
```

Unexported types worth noting:
```go
// :181-185
type stubDaemon struct {
	opts     Options
	registry *SessionRegistry
	idle     stubIdle
}

// :204-209
type stubIdle struct {
	mu    sync.Mutex
	work  []idleWork
	last  core.UnixMilli
	dirty bool
}

// :211-215
type idleWork struct {
	name string
	prio int
	fn   func(ctx context.Context) error
}
```
`stubIdle` methods: `Register`, `Notify`, `IsIdle` (always `false`), `RunOnce` (always
`ErrNotImplemented`), and test-only `Registered() []string`.

### 12.2 What `daemon_test.go` pins

- `TestNew_SucceedsWithNoServices`: `daemon.New(daemon.Options{ProjectRoot: t.TempDir()})` must
  succeed with **all service fields nil**; `d.Registry()` and `d.Idle()` must never be nil.
- `TestStubOperationsReportNotImplemented`: `d.Run`, `d.Stop`, `d.Drain` (returns `(0, err)`),
  `d.Idle().RunOnce(ctx, 0)` (returns `(nil, err)`) — all satisfy `core.IsNotImplemented`.
- `TestSketchSet_CarriesAllFourStructures`: pins the exact four-field spelling `Tried`, `Touch`,
  `Explore`, `Top`:
  ```go
  s := &daemon.SketchSet{
  	Tried:   sketch.NewBloom(512, 0.01),
  	Touch:   sketch.NewCMS(0.001, 0.01),
  	Explore: sketch.NewHLL(14),
  	Top:     sketch.NewMisraGries(64),
  }
  ```
  and assignability into `Options{Sketches: s}`. Test comment: "pins the SP-01 §1341 spelling of
  SketchSet. The set had three fields, missing Top... Wiring the pair here is what makes that
  impossible to lose again."
- `TestHandle_RoutingTableIsData`: `var o daemon.Options` zero-value usable;
  `o.Handler("observe.tool")` returns `(nil, false)` before registration; `o.Handle(op,
  handlerFunc)` with `func(context.Context, ipc.Request) ipc.Response`; re-registering the same
  op **replaces** (not duplicates — `o.Ops()` stays length 2, old closure never called again);
  `o.Ops()` matched via `require.ElementsMatch`.
- `TestIdleController_AcceptsRegistrationsBeforeItCanRun`: `d.Idle()` type-asserts to
  `interface{ Registered() []string }`; `idle.Notify(0)` then
  `idle.IsIdle(core.UnixMilli(1<<40))` must be `false` unconditionally.
- `TestSessionRegistry_EmptyAndSafe` / `TestSessionRegistry_ConcurrentReadsAreRaceFree`: pin
  `daemon.NewSessionRegistry()`, `r.Len()`, `r.Get(id)` as real (non-stub), thread-safe under
  `-race` (8 goroutines × 100 iterations).

### 12.3 `Options.Handle` receiver / `Routes` field

`Options.Handle` is a **pointer-receiver** method (`func (o *Options) Handle(...)`, not a value
receiver). `Options` has **no `Routes *ipc.Router` field**. Full field list: `ProjectRoot`, `Cfg`,
`Log`, `Metrics`, `Clock`, `Store`, `Ledger`, `Sketches`, `Graph`, `Grammar`, `Sched`,
`Checkpoints`, plus unexported `handlers map[ipc.Op]ipc.Handler`. No `ipc.Router` type exists
anywhere in `daemon.go`.

### 12.4 `daemon.NewOptions`

**Does not exist.** Repo-wide grep for `NewOptions|Routes` in `internal/daemon` returned no
matches. `Options` is always constructed as a plain struct literal.

---

## 13. Test guards (`test/guards`)

- **contract_test.go** — pins §12.1's rule that an assertion whose *producer* is absent reports
  `OK=true, Severity=SevInfo, Observed="not-yet-implemented"` on a fresh build (never a failure),
  while the assertion keeps its declared severity. Exact strings: `"a freshly built develop must
  report ModeFull (§12.1)"`, `"assertion %s: an absent producer reports OK, never a failure
  (§12.1)"`, `"assertion %s: an absent producer reports SevInfo regardless of its declared
  severity"`. **SP-05 risk**: wiring a real `Check` but forgetting to preserve declared
  `Severity`, or making a not-yet-implemented op report `OK=false`, breaks this guard.
- **network_test.go** — mechanizes D10 ("no network I/O, ever"). Banned imports:
  `var bannedImports = []string{"net/http", "net/url", "crypto/tls"}`. Only package allowed bare
  `net`: `const netAllowedIn = "internal/ipc"`. **SP-05 risk**: any HTTP client/library reachable
  from the daemon, or socket code placed outside `internal/ipc`, fails this guard.
- **writeset_test.go** — mechanizes §13 invariant 7: writes confined to `<projectRoot>/.qompack/`
  and `<home>/.qompack/` (`const dotQompack = ".qompack"`). Runs the six real hook subcommands
  in-process and diffs the tree (sha256 per file) before/after; `offenders` must be empty while
  the store must still have been touched. Guarded prefixes:
  `allowed := []string{filepath.Join(root, dotQompack)+sep, filepath.Join(home, dotQompack)+sep}`.
  **SP-05 risk**: a detached daemon self-respawn, IPC socket/pipe file, spool file, log, or PID
  file written outside `.qompack/` (e.g. a stray OS-temp-dir artifact) trips this guard.
- **buildorder_test.go** — encodes the four "closing note" build-order priorities via `probes.go`
  probes (`evalProbe`, `storeProbe`, `negknowProbe`, `checkpointProbe`, `analyzerProbe`, each
  checking `core.IsNotImplemented`). Also pins `checkpoint.incrementalSpanInstruction` and
  `checkpoint.frontier.advanceOnSegmentClose` default `true`, `checkpoint.frontier.maxResidualTokens`
  default `20000`, `runtime.selection.submodularEnabled` default `false`. **SP-05 risk**: daemon
  wiring that makes `checkpointProbe` report non-stub while `storeProbe`/`negknowProbe` still
  report stub fails `TestGuard_StoreAndNegknowBeforeCheckpoint`.
- **v1_integration_test.go** — cross-component guard (IT-3–IT-10). Most load-bearing for SP-05:
  `TestV1_AppendOnlyInvariantSurvivesRealHookRun` (a second full hook session must not touch
  `checkpoints/`, `pins/`, or the bloom file — byte-for-byte digest comparison) and
  `TestV1_WriteSetConfinedAcrossFullHookSequence` (same write-set rule, driven through the
  **compiled binary as separate processes**, additionally asserting nothing leaks into the OS
  temp dir and `.qompack/tmp/` is empty after the run). `TestV1_ToolchainGatesRejectRealViolations`
  proves negative-test gates aren't vacuous, including the import-graph rejection test — directly
  relevant since SP-05 adds daemon/ipc wiring that must respect `tools/devtool/importrules.go`.
  **SP-05 risk**: writing outside `.qompack/`, leaving a leftover process/socket/temp file,
  mutating an append-only artifact on a second run, or introducing a forbidden import edge.
- **probes.go** — defines the `probe` struct and the five package probes; `probeDeps()` returns
  `logging.Nop(), obs.New(clk), clk` as the dependency set probes must use.

---

## 14. `tools/devtool`

### 14.1 Registered task names, verbatim (`main.go:26-46`)

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
	"bench-hotpath":         taskBenchHotpath,
	"replay":                taskReplay,
	"plugin-validate":       taskPluginValidate,
	"fsck":                  taskFsck,
	"ci-local":              taskCILocal,
	"gen-config-docs":       taskGenConfigDocs,
	"gen-contract-fixtures": taskGenContractFixtures,
	"install-hooks":         taskInstallHooks,
	"check-commit-msg":      taskCheckCommitMsg,
}
```

### 14.2 `bench-hotpath` — declared, currently a no-op stub

```go
// benchhotpath.go:11-19
func taskBenchHotpath(args []string) error {
	dir := filepath.Join(root, "test", "bench", "hotpath")
	if !dirHasGoFiles(dir) {
		fmt.Println("bench-hotpath: harness not present (owned by SP-05)")
		return nil
	}
	runArgs := append([]string{"run", "./test/bench/hotpath"}, args...)
	return goInherit(runArgs...)
}
```

### 14.3 `importrules.go` — allowed import graph for ipc/daemon/cli/contract

```go
// importrules.go:11-19
var compositionRoots = map[string]bool{
	"daemon":      true,
	"cli":         true,
	"commands":    true,
	"testutil":    true,
	"cmd/qompack": true,
	"test/e2e":    true,
	"test/guards": true,
}
```
`daemon` and `cli` are **composition roots** — not present in the `allow` map at all (they may
import anything; nothing may import them). Exact `allow` map lines for `ipc` and `contract`:
```go
// importrules.go:60-61
"contract": {"hookio", "store"},
"ipc":      {"hookio", "contract"},
```
Every non-foundation package additionally gets `foundation = {"core", "paths", "config",
"logging", "obs"}` appended implicitly.

### 14.4 nomagic linter allowance mechanism (`tools/lint/nomagic`)

`go/analysis`-based static check (D11/§11.6) flagging any `int`/`float` literal duplicating a
value in `internal/config/defaults.go` when it appears outside that file. Two exemption
mechanisms: (1) whole-file — `_test.go` files, `defaults.go` itself, and anything under `tools/`,
`test/`, `testdata/` are always skipped; (2) line-level — a trailing `//nomagic:allow <reason>`
comment (`const allowMarker = "nomagic:allow"`) exempts only that line, but a bare
`//nomagic:allow` with no reason is itself flagged: `"bare //nomagic:allow with no reason; state
why this literal is exempt (D11, §11.6)"`.

---

## 15. `internal/pluginmanifest`

Full exported API, verbatim (`manifest.go`):
```go
type Manifest struct {
	Plugin   PluginJSON
	Hooks    HooksJSON
	MCP      MCPJSON
	Commands []CommandDoc
}

type PluginJSON struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      Author   `json:"author"`
	Homepage    string   `json:"homepage"`
	Keywords    []string `json:"keywords"`
}

type Author struct{ Name string `json:"name"` }

type HooksJSON struct{ Hooks map[string][]HookGroup `json:"hooks"` }

type HookGroup struct {
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []HookEntry `json:"hooks"`
}

type HookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type MCPJSON struct{ MCPServers map[string]MCPServer `json:"mcpServers"` }

type MCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type CommandDoc struct {
	Name         string
	Description  string
	ArgumentHint string
	Subcommand   string
	AllowedTools string
}

func Default(version string) Manifest             // :160
func (m Manifest) Files() (map[string][]byte, error) // :224
func Write(dir string, m Manifest) error            // :254

type Diff struct {
	Path   string
	Reason string
	Want   []byte
	Got    []byte
}

func Validate(dir string, m Manifest) []Diff        // :284
```

**PreCompact timeout for SP-05's checkpoint route**: no dedicated field, but the unexported
`hookSpecs` table (manifest.go:101-109) has the exact binding:
```go
var hookSpecs = []hookSpec{
	{event: "PostToolUse", matcher: "*", args: "observe tool", timeout: 5},
	{event: "UserPromptSubmit", args: "observe prompt", timeout: 5},
	{event: "SessionStart", args: "session-start", timeout: 15},
	{event: "PreCompact", args: "checkpoint", timeout: 20},
	{event: "Stop", args: "observe stop", timeout: 5},
	{event: "SubagentStop", args: "observe stop --subagent", timeout: 10},
	{event: "SessionEnd", args: "flush", timeout: 20},
}
```
`PreCompact` → `checkpoint` subcommand, **20-second** host-enforced hook timeout. Baked into the
generated `HooksJSON`/`HookEntry.Timeout` via `Default()` and frozen by `golden_test.go`
(`TestManifest_GoldenBytes`, `TestBundle_OnDiskMatchesGenerator`) — any change to this timeout
must go through `hookSpecs` or both golden tests fail.

---

## 16. `.github/workflows/ci.yml` and `nightly.yml`

### 16.1 bench-gate job body, verbatim (`ci.yml` lines 89-103)

```yaml
  bench-gate:
    # NOT a required check until the end of wave 1 (§8): the hot-path harness is SP-05's.
    # SP-05 removes this continue-on-error when test/bench/hotpath lands.
    continue-on-error: true
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version: '1.26.x', cache: true }
      - run: go run ./tools/devtool bench-hotpath --iterations 2000 --json bench.json
```

Related job in `nightly.yml` (lines 62-75), `bench-deep` (no `continue-on-error`, deeper run,
uploads artifact):
```yaml
  bench-deep:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version: '1.26.x', cache: true }
      - run: go run ./tools/devtool bench-hotpath --iterations 5000 --json bench.json
      - uses: actions/upload-artifact@v4
        with: { name: "bench-${{ matrix.os }}", path: bench.json }
```

### 16.2 All job names

**ci.yml**: `verify`, `test`, `cover`, `crossbuild`, `bench-gate` (soft, `continue-on-error:
true`), `replay-gate` (soft, `continue-on-error: true`, pending SP-02's replay driver),
`plugin-validate`, `security` (govulncheck + import allowlist enforcement — no `net/http`/
`net/url`/`crypto/tls` outside `internal/ipc`; no `os/exec` outside `daemon`/`cli`/`testutil`),
`docs`.

**nightly.yml**: `fuzz` (matrix of 8 fuzz targets across `chunk`, `canon`, `sketch`, `hookio`,
`ipc`, `config`, `checkpoint`, `redact` — skips gracefully if the fuzz func doesn't exist yet),
`race-windows` (`CGO_ENABLED=1` override), `bench-deep`, `replay-live` (skips gracefully if no
`QOMPACK_SESSIONS_DIR` secret corpus configured).

---

## 17. `plans/OWNERS.tsv`

Header (comment line before data): `# package	owner	floor	probe`

Rows for ipc, daemon, contract, cli, obs (verbatim):
```
obs	SP-01	75	-
cli	SP-01	75	-
ipc	SP-05	75	Send
daemon	SP-05	75	Run
contract	SP-05	75	RunAll
```
Column semantics (from the file's leading comment): `floor` is the §6.4 line-coverage floor;
`probe` names the method `isStub()` calls to decide whether the package is still a stub ("-"
means no stub phase — SP-01 implements it now). `ipc`, `daemon`, and `contract` are explicitly
owned by **SP-05**.

---

## 18. `plans/00-ARCHITECTURE.md` — quoted sections

### §2.4 — Process and IPC architecture (lines 128-204)

```markdown
### 2.4 Process and IPC architecture

```
Claude Code
   │  spawn (hook)                     ┌───────────────────────────────┐
   ▼                                   │  qompack daemon (per project) │
qompack observe tool ──connect──────►  │  ┌─────────────────────────┐  │
   │  write 1 NDJSON line              │  │ session registry        │  │
   │  read 1-byte ACK  (deadline 8 ms) │  │ in-memory: bloom, cms,  │  │
   │                                   │  │ hll, DAG, sequitur,     │  │
   ├─ ACK ──────────► exit 0           │  │ BOCD, scheduler state   │  │
   │                                   │  ├─────────────────────────┤  │
   └─ timeout/refused                  │  │ ingest queue (ring+WAL) │  │
       └─► append .qompack/spool/  ──► │  │ worker pool → L1 store  │  │
           client-<pid>.ndjson         │  │ idle worker → O3/O5     │  │
           exit 0                      │  └─────────────────────────┘  │
                                       └───────────────────────────────┘
```

**Transport.**

- Windows: named pipe `\\.\pipe\qompack.<hash12>` via `github.com/Microsoft/go-winio`,
  ACL'd to the current user SID only. `hash12` = first 12 hex chars of
  `sha256(normalizedAbsProjectRoot)`.
- POSIX: `SOCK_STREAM` Unix socket. Path resolution order:
  1. `$XDG_RUNTIME_DIR/qompack/<hash12>.sock`
  2. `<os.TempDir()>/qompack-<uid>/<hash12>.sock`
  Directory `0700`, socket `0600`. If the resolved path exceeds 100 bytes (macOS `sun_path` is
  104), fall back to `<os.TempDir()>/qp-<hash8>.sock`.

**Framing.** One request per line: UTF-8 JSON, `\n`-terminated, 1 MiB max line. Response for
fire-and-forget requests is a single byte `\x06` (ACK) or `\x15` (NAK). Requests that need data
back (`UserPromptSubmit` additionalContext, MCP-over-daemon, `status`) set `"reply": true` and
receive one NDJSON response line instead.

**Why ACK instead of pure fire-and-forget.** A bare write followed by immediate `exit` is
*usually* delivered but is not guaranteed to be observed before the daemon's read loop is torn
down on abnormal daemon exit, and on Windows message-mode pipes a client close can race the
server read. One byte costs ~50 µs and converts "usually" into "provably enqueued." Budget
allows it.

**Daemon lifecycle.**

- Started by `qompack session-start` (off the hot path, generous hook timeout). Also started
  lazily by any client that finds no listener, using a detached spawn
  (`SysProcAttr{HideWindow:true, CreationFlags: CREATE_NO_WINDOW|DETACHED_PROCESS}` on Windows;
  `Setsid` on POSIX) — the *spawning* client does not wait for it, it spools and exits.
- Singleton per project via `.qompack/run/daemon.lock` (`O_CREATE|O_EXCL`, contains pid + start
  time + pipe path). A lock whose pid is dead is stale and reclaimable.
- Idle-exits after `runtime.daemonIdleExitSeconds` (default 1800) with zero live sessions.
- Drains `.qompack/spool/*.ndjson` on start and on every idle tick, then deletes drained files.
- Crash-safe: the WAL (`spool/wal-<session>.ndjson`, `O_APPEND`, no fsync) is the durability
  boundary. ACK is sent after the WAL append returns, before any indexing work.

**Latency budgets (normative — these are what CI gates on).**

| ID | Clock | Budget | Enforced |
|---|---|---|---|
| **B-A** | `hook_controlled` — client `main()` entry → `exit` (connect + write + ACK) | **p99 < 15 ms** (§11.3 L0) | CI on linux/macos/windows, 5 000 iterations |
| **B-B** | `l0_ingest` — daemon read → WAL append returned | p99 < 2 ms | daemon self-metrics + CI |
| **B-C** | `l0_process` — WAL → fully chunked, stored, DAG/sketches updated (async) | p99 < 50 ms | soft; overrun → sampling + backpressure, never blocking |
| **B-D** | `hook_wall` — includes host process creation | reported, not gated; tracked in `/qompack:status` and the bench artifact | — |
| **B-E** | `checkpoint_finalize` — `PreCompact` entry → exit | **p99 < 2 s** (§11.3 L4) | CI |
| **B-F** | `mcp_tool_call` — request → response | p95 < 250 ms (`minimal` span) | CI |

B-A is the number the design document names. B-D is reported honestly because process creation is
the host's cost and no plugin architecture can remove it; hiding it inside B-A would be dishonest
measurement, which is exactly the sin §1.3 RC-3 indicts.

**When the budget is exceeded (§8.1 fallback).** The daemon keeps a rolling 512-sample HDR
histogram per hook. If B-A p99 exceeds budget for 3 consecutive 512-sample windows, the daemon
sets `hotPathMode = spool` in the session registry and returns it in the next ACK's NAK-with-hint
frame; clients then skip the connect entirely and append straight to the spool for the rest of
the session. This is the "degrade to async queue-and-drain rather than blocking" clause,
implemented as an observable state transition, logged at WARN, and surfaced by
`/qompack:status`.
```

### §5.4 — `internal/ipc` and `internal/daemon` (lines 692-776)

```markdown
### 5.4 `internal/ipc` and `internal/daemon`

```go
// ipc
type Addr struct{ Kind AddrKind; Path string } // NamedPipe | UnixSocket
func Resolve(projectRoot string) (Addr, error)

type Request struct {
    Op       Op              `json:"op"`
    Session  core.SessionID  `json:"s"`
    TS       core.UnixMilli  `json:"t"`
    Reply    bool            `json:"r,omitempty"`
    Event    *hookio.Event   `json:"e,omitempty"`
    Raw      json.RawMessage `json:"x,omitempty"`
}
type Op string // "observe.tool" "observe.prompt" "observe.stop" "session.start"
               // "checkpoint" "flush" "status" "mcp" "admin.*"
type Response struct {
    OK     bool            `json:"ok"`
    Mode   contract.Mode   `json:"mode"`
    Hot    HotPathMode     `json:"hot"`   // Sync | Spool  — clients honour this immediately
    Output *hookio.Output  `json:"out,omitempty"`
    Err    string          `json:"err,omitempty"`
    Data   json.RawMessage `json:"data,omitempty"`
}

type Client interface {
    // Send is the hot path. It connects, writes, awaits ACK within deadline, and returns.
    // On ANY failure it spools to disk and returns (Response{OK:false}, nil) — never an error
    // that a hook would propagate.
    Send(ctx context.Context, req Request, deadline time.Duration) (Response, error)
    Close() error
}
func NewClient(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry) Client

type SpoolWriter interface{ Append(req Request) error; Path() string }
func NewSpool(dir string) (SpoolWriter, error)

type Server interface {
    Serve(ctx context.Context, h Handler) error
    Addr() Addr
    Close() error
}
type Handler func(ctx context.Context, req Request) Response

// daemon
type Daemon interface {
    Run(ctx context.Context) error
    Registry() *SessionRegistry
    Drain(ctx context.Context) (int, error)   // spool + WAL replay; idempotent
    Idle() IdleController                     // O3/O5 background work
    Stop(ctx context.Context) error
}
type Options struct {
    ProjectRoot string; Cfg config.Config; Log logging.Logger
    Metrics obs.Registry; Clock core.Clock
    Store store.Store; Ledger negknow.Ledger; Sketches *SketchSet
    Graph dag.Graph; Grammar grammar.Sequitur; Sched scheduler.Runtime
    Checkpoints checkpoint.Writer
}
func New(o Options) (Daemon, error)

// Extension seams (SP-05 ships these so later waves wire in WITHOUT editing daemon internals,
// which is what keeps wave-3 subplans from colliding inside one package):
//   - Handle registers an Op handler; the op-routing table is data, not a switch.
//   - IdleController.Register adds O3/O5 background work.
//   - Services is the late-bound dependency set; nil members mean "not built yet" and every
//     call site must tolerate that (waves 1–2 run with Checkpoints and Sched nil).
//
// Handle takes a POINTER receiver. The routing table is an unexported map that Handle allocates
// on first use, so a value receiver would mutate a copy and register nothing — every caller would
// silently get an empty table. Wiring is therefore `o := daemon.Options{...}; o.Handle(...)` and
// composition roots must pass &o where an *Options is wanted.
func (*Options) Handle(op ipc.Op, h ipc.Handler)

type IdleController interface {
    // Register work that may run only when the session is idle (§8.4 O3).
    Register(name string, prio int, fn func(ctx context.Context) error)
    Notify(lastActivity core.UnixMilli)
    IsIdle(now core.UnixMilli) bool
    RunOnce(ctx context.Context, budget time.Duration) (ran []string, err error)
}
type SessionRegistry struct{ /* per-session live state, hot-path mode, counters */ }
```
```

### §5.19 — `internal/contract` (G9.3, §12) (lines 1738-1782)

```markdown
### 5.19 `internal/contract` (G9.3, §12)

```go
type ID string
const (
    CSessionStartFires      ID = "session_start.fires"
    CSessionStartSourceCompact ID = "session_start.source_compact"
    CAdditionalContext      ID = "hook.additional_context_delivered"
    CPreCompactTiming       ID = "precompact.has_time_to_write"
    CPreCompactCustomInstr  ID = "precompact.custom_instructions_accepted"
    CHookPayloadShape       ID = "hook.payload_shape"
    CMCPRegistered          ID = "mcp.server_registered"
    CTranscriptReadable     ID = "transcript.readable"
    CPluginRootResolves     ID = "plugin.root_resolves"
)
type Severity uint8 // SevInfo, SevWarn, SevCritical
type Result struct {
    ID ID; OK bool; Severity Severity
    Expected, Observed string; TS core.UnixMilli; Detail string
}
type Mode uint8 // ModeFull, ModeDegradedPassive, ModeOff
func (m Mode) String() string

type Assertion struct {
    ID ID; Severity Severity; Description string
    Check func(ctx context.Context, e Env) Result
}
type Env struct {
    ProjectRoot string; Event hookio.Event; Cfg config.Config
    Store store.Store; Log logging.Logger; Clock core.Clock
    History History      // observed hook firings this session and previous ones
}
type Monitor interface {
    Register(a Assertion) error
    RunAll(ctx context.Context, e Env) ([]Result, Mode)
    Mode() Mode
    // Degrade logs LOUD, writes .qompack/logs/LOUD.log, sets mode, and persists the reason so
    // /qompack:status and the next SessionStart both surface it. NEVER silent.
    Degrade(reason string, results []Result)
    Restore(reason string)
    Report() []Result
}
func NewMonitor(log logging.Logger, m obs.Registry, statePath string) Monitor
func StandardAssertions() []Assertion
```
```

### §12.1, §12.2, §12.3 (lines 2293-2353)

```markdown
### 12.1 Contract monitor (G9.3, §12 row 1)

Every `SessionStart`, before any other work, `contract.Monitor.RunAll` executes
`StandardAssertions()` (§5.19). Each assertion is a real observation, not a version check:

| Assertion | How it is asserted |
|---|---|
| `session_start.fires` | a marker written at `SessionEnd`/`PreCompact` is found by the next `SessionStart`; absence across two sessions ⇒ fail |
| `session_start.source_compact` | after a `PreCompact` is observed, the next `SessionStart` must arrive with `source == "compact"` within the same session id. Recorded in `state/contract.json` and evaluated on the *following* start |
| `hook.additional_context_delivered` | `SessionStart` emits a sentinel token in `additionalContext`; the next `UserPromptSubmit` reads the transcript tail and looks for it. Not found ⇒ fail |
| `precompact.has_time_to_write` | measured `PreCompact` wall time vs. the manifest timeout; p99 > 60% of timeout ⇒ warn, timeout hit ⇒ fail |
| `precompact.custom_instructions_accepted` | the emitted instruction's sentinel phrase is searched for in the post-compaction summary; absent ⇒ warn (advisory by design, §8.5) |
| `hook.payload_shape` | required fields present and typed as `hookio.Event` expects |
| `mcp.server_registered` | the MCP server received `initialize` at least once this session |
| `transcript.readable` | `transcript_path` exists and parses |
| `plugin.root_resolves` | `${CLAUDE_PLUGIN_ROOT}` expanded to an existing binary |

**Assertions whose observable does not exist yet.** Some assertions depend on a subsystem that a
later wave delivers: `mcp.server_registered` (SP-13), `precompact.custom_instructions_accepted`
and `precompact.has_time_to_write` (SP-10), `hook.additional_context_delivered` (SP-11). An
assertion whose *producer is absent from the build* returns `OK: true, Severity: SevInfo` with
`Observed: "not-yet-implemented"` — it must never degrade the session. Wiring this wrong would
put every wave-1 and wave-2 verification run into `degraded-passive` and silently disable the very
paths those waves are testing. A CI test asserts a freshly built `develop` reports `ModeFull`.

**On any `SevCritical` failure:** `Monitor.Degrade` is called. That means, in order:
`logging.Loud` (log file + `LOUD.log` + `systemMessage` on the next hook that may emit one),
persist the reason to `state/contract.json`, set `Mode = ModeDegradedPassive`, and record it in
`obs`. `/qompack:status` leads with a banner naming the failed assertion, what was expected, and
what was observed. **Nothing fails silently — that is the whole point of the mechanism.**

`ModeDegradedPassive` behaviour: L0 and L1 keep running (observe, chunk, store, sketches, DAG,
verbatim capture, elimination records — the store stays correct and the session's data is not
lost). Everything that *acts* is off: no `additionalContext` injection, no `customInstructions`,
no scheduler-initiated checkpoints, no drop report. MCP retrieval tools stay available, because
they are pull-based and cannot make anything worse. The session then behaves exactly as it does
without Qompack, which is §7.1's stated requirement.

Recovery: assertions re-run every `SessionStart`. Two consecutive clean runs call
`Monitor.Restore`, logged just as loudly as the degradation.

### 12.2 Hot-path overrun (§8.1)

Described in §2.4. `sync` → `spool` submode transition on 3 consecutive breach windows; logged at
WARN; visible in `/qompack:status`; automatically reverts after 3 clean windows in a subsequent
session. Under `spool`, the client never connects: it appends and exits, and the daemon drains on
its idle tick. Data is not lost; only freshness is.

### 12.3 Everything else fails toward "do nothing"

| Failure | Response |
|---|---|
| daemon unreachable | client spools, exits 0, spawns a detached daemon for next time |
| spool write fails | drop the event, increment `obs.Counter("l0.dropped")`, `Loud` once per session |
| store corrupt (bad CRC, truncated object) | quarantine the object to `.qompack/tmp/quarantine/`, `Loud`, continue; `qompack fsck` repairs |
| checkpoint MANIFEST mismatch | `Loud`, refuse to use the affected checkpoint, fall back to its parent, degrade to passive |
| bloom load fails | rebuild from `records/eliminations.jsonl` (§3.3); if that fails, `already_tried` returns `absent` for everything — never a false positive |
| config invalid | per-leaf fallback to default + `Loud` (§11.3) |
| MCP tool panic | recovered at the handler boundary, returned as `IsError`, never kills the server |
| any hook panic | recovered in `cli`, logged, `exit 0` with empty output |
| `PreCompact` about to exceed its timeout | `Finalize` the draft as-is (importance-ordered, so a truncated checkpoint is still the best available for its size — §6.9) and return |

---
```

### §3.2 — Layer mapping (§7.2 L0-L7) (lines 343-396)

```markdown
### 3.2 Layer mapping (§7.2 L0–L7)

| Layer | §7.2 responsibility | Packages | Owning subplan |
|---|---|---|---|
| **L0 Observer** | PostToolUse · UserPromptSubmit · SessionStart · SessionEnd · Stop | `hookio` `cli` `ipc` `daemon` `observer` | SP-01 (`hookio`, `cli`) · SP-05 (`ipc`, `daemon`) · SP-08 (`observer`) |
| **L1 Store** | CDC chunks · Merkle index · sketches · dependence DAG · segment log | `chunk` `canon` `symbols` `store` `sketch` `dag` | SP-04 (`chunk` `canon` `symbols`) · SP-06 (`store` `redact`) · SP-03 (`sketch`) · SP-07 (`dag`) |
| **L2 Analyzer** | slicing · Δ-scoring · submodular · Sequitur · redundancy | `analyzer` `grammar` `negknow` `dag` | SP-15 (`analyzer` `grammar`) · SP-09 (`negknow`) · SP-07 (`dag` slicing) |
| **L3 Scheduler** | BOCD · Young–Daly · p-selection · TTL awareness | `scheduler` | SP-12 |
| **L4 Checkpointer** | PreCompact → immutable versioned artifact, importance-ordered | `checkpoint` `pins` | SP-10 |
| **L5 Rehydrator** | SessionStart(compact) · progressive budget fill · drop report | `rehydrate` `rules` `skills` | SP-11 |
| **L6 Retrieval** | recall · expand · re_read · already_tried · timeline · why · dropped | `mcp` `commands` | SP-13 (MCP) · SP-14 (commands) |
| **L7 Evaluation** | replay harness · Belady OPT · CI gate | `eval` `test/replay` | SP-02 |
| **cross** | config, logging, metrics, contracts, degradation, tokens | `core` `paths` `config` `logging` `obs` `contract` `tokens` `pluginmanifest` `testutil` | SP-01 (+ SP-05 `contract`, + SP-06 `tokens` exact accounting) |

**Dependency rule (enforced by an import-graph test in `verify`).** The L0–L7 numbering in §7.2
describes *data flow*, not import direction: `observer` (L0) legitimately calls `store` (L1) on
the write path, while `mcp` (L6) calls `store` on the read path. A numeric ordering therefore
cannot express the constraint, and the rule is an explicit DAG instead. The allowed import sets
are exhaustive; anything not listed is forbidden.

| Package | May import |
|---|---|
| `core` | — (nothing in `internal/`) |
| `paths`, `config` | `core` |
| `logging`, `obs` | `core` `paths` `config` |
| *(the five above are the **foundation**; every package below may also import all of them)* | |
| `hookio`, `sketch`, `chunk`, `symbols`, `redact`, `grammar`, `rules`, `skills`, `pins`, `tokens`, `eval`, `scheduler` | foundation only |
| `canon` | `sketch` |
| `dag` | — |
| `store` | `chunk` `canon` `sketch` `symbols` `redact` `tokens` |
| `negknow` | `sketch` `store` `dag` |
| `analyzer` | `store` `dag` `sketch` `scheduler` |
| `checkpoint` | `store` `dag` `negknow` `pins` `grammar` `tokens` |
| `rehydrate` | `checkpoint` `store` `negknow` `dag` `rules` `skills` `tokens` |
| `mcp` | `store` `negknow` `checkpoint` |
| `contract` | `hookio` `store` |
| `ipc` | `hookio` `contract` |
| `observer` | `hookio` `store` `chunk` `canon` `sketch` `dag` `grammar` `negknow` `tokens` |
| `daemon`, `cli`, `commands`, `testutil`, `cmd/qompack` | **composition roots** — may import anything; nothing may import them |

Consequences worth stating explicitly, because they are the ones that would otherwise be
discovered as import cycles in wave 1:

- `store` must **not** import `negknow` — hence `ChangedSince` takes `[]core.Dep`, not
  `[]negknow.Dep` (§4, §5.8, §5.10).
- `tokens` must **not** import `store` — hence `EstimateRoot` takes `[]core.ChunkRef` (§5.20).
- `checkpoint` imports `pins`, so `Invariant` is defined in `pins` and aliased by `checkpoint`
  (§5.14).
- `scheduler` is a pure package: `Evaluate` is a pure function and `Runtime` is an *interface*.
  The `Runtime` implementation, which assembles `Candidate`s from `dag` and `store`, lives in
  `internal/daemon` (§5.13).
- `observer` must not import `scheduler` or `checkpoint`: it emits `observer.Signals` and the
  daemon translates them into `scheduler.Features`.
```

### §3.3 — Runtime data: `.qompack/` (§7.4) (lines 397-469)

```markdown
### 3.3 Runtime data: `.qompack/` (§7.4)

**Location.** `.qompack/` lives at the **root of the project Claude Code is operating on**,
resolved in this order and cached per daemon:

1. `QOMPACK_PROJECT_ROOT` (tests, CI, explicit override)
2. `cwd` / `project_dir` from the hook payload, walked upward to the nearest `.git`
3. the payload `cwd` itself

It is **never** the plugin install directory and never `~`. Global, cross-project state lives in
`~/.qompack/` (`config.json` user-global layer, `daemons.json` registry, `calibration.json`).

**It is not source.** `.gitignore` contains `/.qompack/`, and SP-01 additionally writes
`.qompack/.gitignore` containing `*` on first use, so the store self-ignores even in a project
whose `.gitignore` we never touch.

```
.qompack/                       # gitignored, project-root
├── .gitignore                  # contains "*" — self-ignoring
├── config.json                 # project layer (Appendix C, partial)
├── objects/ab/cd/<sha256>.zst  # content-addressed, zstd, 2-level fanout   [§8.2]
├── index/
│   ├── tool_use.jsonl          # tool_use_id → root hash, ts, tool, args    (append-only)
│   ├── files.json              # path → [(ts, root_hash)] version history
│   ├── roots.jsonl             # root hash → chunk list, sizes              (append-only)
│   └── segments.jsonl          # changepoint-delimited segment log          (append-only)
├── sketches/
│   ├── tried.bloom             # negative knowledge — NEVER regenerated from a summary
│   ├── touch.cms
│   └── explore.hll
├── dag/deps.jsonl              # dependence edges                           (append-only)
├── grammar/actions.seq         # Sequitur state over the action log
├── checkpoints/
│   ├── MANIFEST.jsonl          # (seq, sha256, bytes, created)              (append-only)
│   ├── 0001.json               # immutable, importance-ordered
│   └── 0002.json
├── pins/invariants.json        # user- and agent-pinned, never summarized
├── eval/{replay/,opt/}
│   ── ── ── ── ── ── ── ── ──  runtime-only, not in §7.4, additive:
├── records/eliminations.jsonl  # structured source of truth for tried.bloom (append-only)
├── state/                      # daemon-persisted: bocd.json, scheduler.json, frontier.json
├── run/                        # daemon.lock, daemon.pid, socket path hint  (never committed)
├── spool/                      # WAL + client fallback queue                (append-only)
├── logs/qompack-YYYYMMDD.log   # rotated, 10 MB × 5
├── metrics/latency.json        # rolling histograms for /status and bench
└── tmp/                        # atomic-write staging (same volume as target)
```

**Append-only invariant (§7.4, mechanically enforced).** `checkpoints/`, `pins/`, and
`sketches/tried.bloom` are additive-only.

- `paths.AppendOnly(p)` opens `O_WRONLY|O_APPEND|O_CREATE` and returns an error if `O_TRUNC` is
  requested. It is the *only* write path allowed into `*.jsonl`.
- `paths.CreateNew(p)` uses `O_EXCL`. Checkpoint files use it — writing `0007.json` twice is an
  error, not an overwrite. After a successful write the file is set read-only
  (`0444` / `FILE_ATTRIBUTE_READONLY`).
- `checkpoints/MANIFEST.jsonl` records `(seq, sha256, bytes, created)` per checkpoint;
  `qompack fsck` re-hashes every checkpoint against it. Any mismatch is a loud failure and flips
  the session to `degraded-passive`.
- `pins/invariants.json` is written by read-modify-**append**: it is a JSON array persisted as a
  JSONL log (`pins/invariants.jsonl` internally) with `invariants.json` regenerated as a
  materialized view; the log is the truth. Deletion is a tombstone record, never a rewrite.
- `sketches/tried.bloom` may be *replaced* only by `negknow.RebuildBloom`, whose input is
  `records/eliminations.jsonl` filtered to `status:"active"` — never a checkpoint, never a
  summary, never context. The rebuild writes a new file and renames; the previous file is kept as
  `tried.bloom.<seq>.bak` for one generation.
- A conformance test (`paths.TestAppendOnlyGuard`) attempts truncation, in-place rewrite, and
  out-of-order seq writes against all three locations and asserts every one fails.

**Atomic writes.** `paths.WriteAtomic(p, b)` writes to `.qompack/tmp/<rand>`, `Sync()`, then
`os.Rename` onto `p` (same volume by construction, so `MoveFileEx(REPLACE_EXISTING)` semantics
hold on Windows). Directory fsync on POSIX. Never used for append-only targets.
```

---

## 19. `internal/pluginmanifest`

See §15 above (combined for the two related asks in the task).

---

## 20. `qompack version` subcommand

**Yes, already shipped.** See §10.6 above: `Cmd{Name: "version", ...}` is registered
unconditionally in `internal/cli/commands.go`'s `All()`, backed by `runVersion` which prints
`core.Version` (default `"0.1.0"`, link-time-overridable via `-X .../core.Version=<tag>`).

---

## Divergences from the SP-05 plan text

The plan text (`plans/V2-SP-05-daemon-ipc-and-hot-path.md`) assumes a number of placeholder
names/shapes that differ from what SP-01 actually shipped. Implementers should use the shipped
spellings, not the plan's placeholders:

- **`ipc.AddrNamedPipe`/`ipc.AddrUnixSocket` names** — do not exist. The shipped type is
  `AddrKind` with values `NamedPipe` and `UnixSocket` (no `Addr`-prefix on the enum values):
  `type AddrKind uint8; const (NamedPipe AddrKind = iota + 1; UnixSocket)`.
- **`DefaultMaxLine`** — does not exist. The shipped constant is `ipc.MaxLineBytes = 1 << 20`
  (`wire.go`), matching §2.4's "1 MiB max line".
- **`ipc.Router` type** — does not exist anywhere in the codebase (confirmed by grep). Routing
  is done via `daemon.Options.handlers map[ipc.Op]ipc.Handler` plus the `Handle`/`Handler`/`Ops`
  methods on `*Options` — there is no separate Router type or a `Routes *ipc.Router` field on
  `Options`.
- **`ProjectHash12`/`ProjectHash8`** — do not exist as exported symbols. `ipc/resolve.go` has an
  unexported `endpointHash` helper and unexported length constants `hash12Len`/`hash8Len`; the
  hashing logic is internal to `Resolve`, not separately exposed.
- **`ErrAddrTooLong`** — does not exist. The only exported error in `ipc` is
  `ErrUnresolvedRoot = errors.New("ipc: project root must not be empty")`. The "fall back to a
  shorter socket path" behavior described in §2.4 is presumably handled internally in `resolveFor`
  without a distinct exported sentinel.
- **`QOMPACK_IPC_ADDR` override** — no evidence found of this env var being read anywhere in
  `internal/ipc`; `Resolve(projectRoot string) (Addr, error)` takes only a project root, not an
  env lookup function. (Contrast with `paths.Resolve(getenv, payloadCWD)`, which *is*
  env-injectable — `ipc.Resolve` is not.)
- **`obs.NewRegistry`** — the constructor is `obs.New(clock core.Clock) Registry`, not
  `NewRegistry`.
- **`Counter.Inc`** — does not exist. `Counter` has `Add(n int64)` and `Value() int64` only; use
  `Add(1)` for increment.
- **`obs` Metric* dotted names like `"hook.controlled"`** — the shipped histogram names use
  underscores, not dots, and are unexported string constants reached only via `Budget.Hist`:
  `"hook_controlled"`, `"l0_ingest"`, `"l0_process"`, `"hook_wall"`, `"checkpoint_finalize"`,
  `"mcp_tool_call"`. There is no exported `Metric*` constant family.
- **`paths.WriteAtomic(p, b)` 2-arg** — the shipped signature is 3-arg:
  `WriteAtomic(p string, b []byte, perm fs.FileMode) error`.
- **`paths.AppendOnly` returning `*os.File`** — it returns `io.WriteCloser`, not `*os.File`:
  `func AppendOnly(p string) (io.WriteCloser, error)`.
- **`contract.History` as a struct with `LoadHistory`/`SaveHistory`** — `History` is an
  **interface**, not a struct, with methods `Saw`, `LastSeen`, `Record`, `Sessions` — no
  `LoadHistory`/`SaveHistory` functions exist anywhere. Additionally, **no implementation of
  `History` ships in `internal/contract` at all** — SP-05 likely needs to build the first real
  one (a gap the plan may not have anticipated).
- **`contract.ParseMode` exported** — it is unexported (`parseMode`, lowercase), usable only
  from within package `contract`.
- **`Mode.MayAct`/`MayRecord` methods** — do not exist. `Mode` has exactly one method, `String()
  string`. Any "can this mode act/record" logic must be built by SP-05 itself (e.g. a switch on
  `Mode` at the call site), not looked up as an existing method.
- **`StandardAssertions` with real checks** — all 9 `StandardAssertions()` entries currently have
  `Check` functions that unconditionally return `OK:true, Severity:SevInfo,
  Observed:"not-yet-implemented"`, regardless of their *declared* severity (4 critical, 4 warn, 1
  info). SP-05 replaces these Check bodies with real observations — but must preserve the
  declared ID/Severity/Description exactly (pinned by `standard_test.go` and the golden fixture).
- **config keys `runtime.daemon.enabled`** — this key does exist as described:
  `RuntimeCfg.Daemon.Enabled` maps to JSON path `runtime.daemon.enabled` (via `DaemonCfg{Enabled
  bool `json:"enabled"`}` nested under `RuntimeCfg{Daemon DaemonCfg `json:"daemon"`}`). No
  divergence here — this one matches the plan.
- **`daemon.NewOptions`** — does not exist. `Options` is always built via a plain struct literal
  (`daemon.Options{...}`); there is no constructor function.
- **value-receiver `Options.Handle` with `Routes *ipc.Router` field** — both parts are wrong:
  `Handle` is a **pointer**-receiver method (`func (o *Options) Handle(...)`), and there is **no
  `Routes` field of any kind** — routing state is the unexported `handlers
  map[ipc.Op]ipc.Handler` field, manipulated only through `Handle`/`Handler`/`Ops`.
- **`obs.CheckBudgets` as a stub** — `CheckBudgets(cfg config.Config) []BudgetBreach` is part of
  the `Registry` interface and is a real, stateful method on the concrete `*registry` (tracks
  consecutive-breach windows per budget) — not confirmed to be a no-op stub; treat as real unless
  further testing shows otherwise (this document did not exhaustively verify its internal
  correctness, only its shape).
