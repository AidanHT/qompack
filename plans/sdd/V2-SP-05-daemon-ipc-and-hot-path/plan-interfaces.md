## Interface contract

### Consumes (exact signatures from 00-ARCHITECTURE.md §4, §5 — call these, do not change them)

```go
// package core (§4)
type Hash [32]byte
func (h Hash) String() string
func (h Hash) Short() string
func HashBytes(domain string, b []byte) Hash
type SessionID string
type ToolUseID string
type TurnIndex int
type UnixMilli int64
type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
func SystemClock() Clock
var (
    ErrNotImplemented = errors.New("qompack: not implemented")
    ErrNotFound       = errors.New("qompack: not found")
    ErrAppendOnly     = errors.New("qompack: append-only violation")
    ErrDegraded       = errors.New("qompack: running in degraded mode")
    ErrContract       = errors.New("qompack: host contract violated")
)

// package paths (§3.3)
func Norm(projectRoot, p string) (string, error)
func Key(p string) string
func WriteAtomic(p string, b []byte) error
func AppendOnly(p string) (*os.File, error)
func CreateNew(p string) (*os.File, error)

// package config (§5.1, §11.5)
func Defaults() Config
func Load(env Env) (Config, Provenance, []Warning, error)
type Env struct { ProjectRoot, HomeDir string; Getenv func(string) string; Flags map[string]string }
// Runtime namespace reached as cfg.Runtime.Daemon.AckDeadlineMs, cfg.Runtime.Daemon.ConnectDeadlineMs,
// cfg.Runtime.Daemon.IdleExitSeconds, cfg.Runtime.Daemon.MaxSessions, cfg.Runtime.Daemon.Enabled,
// cfg.Runtime.HotPath.BudgetMs, cfg.Runtime.HotPath.BreachWindows, cfg.Runtime.HotPath.SpoolOnBreach,
// cfg.Runtime.HotPath.MaxPayloadBytes, cfg.Runtime.Mode, cfg.Scheduler.Idle.DetectAfterSeconds.
// The JSON keys in §11.5 are normative; if SP-01's Go field spelling differs, use SP-01's spelling.

// package logging (§5.2)
type Logger interface {
    With(kv ...any) Logger
    Debug(msg string, kv ...any); Info(msg string, kv ...any)
    Warn(msg string, kv ...any);  Error(msg string, kv ...any)
    Loud(msg string, kv ...any)
}
func New(dir string, lvl Level) (Logger, io.Closer, error)
func Nop() Logger

// package obs (§5.2)
type Histogram interface { Observe(d time.Duration); Snapshot() HistSnapshot; Reset() }
type HistSnapshot struct{ N int64; P50, P95, P99, P999, Max time.Duration }
type Registry interface {
    Hist(name string) Histogram
    Counter(name string) Counter
    Gauge(name string) Gauge
    Snapshot() Snapshot
    CheckBudgets(cfg config.Config) []BudgetBreach
}
type BudgetBreach struct{ Budget string; Observed, Limit time.Duration; Windows int }

// package hookio (§5.3)
type Event struct {
    HookEventName  string; SessionID core.SessionID; TranscriptPath string; CWD string
    Source string; Trigger string; ToolName string; ToolUseID core.ToolUseID
    ToolInput json.RawMessage; ToolResponse json.RawMessage; Prompt string
    StopHookActive bool; Extra map[string]json.RawMessage
}
func ReadEvent(r io.Reader, limit int64) (Event, []byte, error)
type Output struct {
    Continue *bool; SuppressOutput *bool; HookSpecificOutput *HSO; SystemMessage string
}
type HSO struct{ HookEventName, AdditionalContext, CustomInstructions string }
func WriteOutput(w io.Writer, o Output) error
func Empty() Output

// Consumed as nil-tolerant late-bound seams only (stubs in wave 1; never called when nil):
// store.Store, negknow.Ledger, dag.Graph, grammar.Sequitur, scheduler.Runtime, checkpoint.Writer,
// sketch.NewBloom/NewCMS/NewHLL/NewMisraGries, sketch.Save, sketch.Load.
```

### Produces (exact signatures later subplans rely on)

```go
// ── package ipc ─────────────────────────────────────────────────────────────
type AddrKind uint8
const (AddrNamedPipe AddrKind = iota; AddrUnixSocket)
type Addr struct{ Kind AddrKind; Path string }
func Resolve(projectRoot string) (Addr, error)
func ProjectHash12(projectRoot string) string
var ErrAddrTooLong = errors.New("qompack: ipc address exceeds sun_path limit")

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
    OpAdminPing     Op = "admin.ping"
    OpAdminDrain    Op = "admin.drain"
    OpAdminReload   Op = "admin.reload"
    OpAdminIdle     Op = "admin.idle"
    OpAdminShutdown Op = "admin.shutdown"
)
func KnownOps() []Op
func (o Op) Valid() bool
func (o Op) HotPath() bool // true for observe.tool / observe.prompt / observe.stop

type Request struct {
    Op      Op              `json:"op"`
    Session core.SessionID  `json:"s"`
    TS      core.UnixMilli  `json:"t"`
    Reply   bool            `json:"r,omitempty"`
    Event   *hookio.Event   `json:"e,omitempty"`
    Raw     json.RawMessage `json:"x,omitempty"`
}
type HotPathMode uint8
const (HotSync HotPathMode = iota; HotSpool)
func (m HotPathMode) String() string
type Response struct {
    OK     bool            `json:"ok"`
    Mode   contract.Mode   `json:"mode"`
    Hot    HotPathMode     `json:"hot"`
    Output *hookio.Output  `json:"out,omitempty"`
    Err    string          `json:"err,omitempty"`
    Data   json.RawMessage `json:"data,omitempty"`
}

type Client interface {
    Send(ctx context.Context, req Request, deadline time.Duration) (Response, error)
    Close() error
}
func NewClient(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry) Client
type ClientOptions struct {
    ProjectRoot     string
    State           State                                  // from ReadState; supplies mode, hot, deadlines, limits
    Self            string                                 // os.Executable(); "" disables lazy spawn
    Spawn           func(projectRoot, self string) error   // set by cli to daemon.SpawnDetached; nil disables lazy spawn
    ConnectDeadline time.Duration                          // 0 → State.ConnectDeadlineMs
    AckDeadline     time.Duration                          // 0 → State.AckDeadlineMs
    MaxLine         int                                    // 0 → DefaultMaxLine
    Clock           core.Clock                             // nil → core.SystemClock()
}
func NewClientWithOptions(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry, o ClientOptions) Client

type SpoolWriter interface{ Append(req Request) error; Path() string }
func NewSpool(dir string) (SpoolWriter, error)
func SpoolFiles(dir string) ([]string, error)
var ErrSpoolFull = errors.New("qompack: spool file at cap")
// ExternalizeThreshold reports the encoded-request size at or above which the client moves the
// oversized member to a side blob: min(cfg.Runtime.HotPath.MaxPayloadBytes, DefaultMaxLine).
func ExternalizeThreshold(cfg config.Config) int

const (
    ACK byte = 0x06
    NAK byte = 0x15
)
const DefaultMaxLine = 1 << 20 // 1 MiB, 00-ARCH §2.4 "1 MiB max line"
var ErrLineTooLong = errors.New("qompack: ipc line exceeds max")
type LineReader struct{ /* … */ }
func NewLineReader(r io.Reader, maxLine int) *LineReader
func (lr *LineReader) ReadLine() ([]byte, error)

type Handler func(ctx context.Context, req Request) Response
type Router struct{ /* … */ }
func NewRouter() *Router
func (r *Router) Handle(op Op, h Handler)
func (r *Router) Route(ctx context.Context, req Request) Response
func (r *Router) Ops() []Op
func (r *Router) SetFallback(h Handler)

type Server interface {
    Serve(ctx context.Context, h Handler) error
    Addr() Addr
    Close() error
}
func NewServer(a Addr, log logging.Logger, m obs.Registry, maxLine int) (Server, error)

// state.bin — the 32-byte hot-path state record (see Implementation spec)
type State struct {
    Mode              contract.Mode
    Hot               HotPathMode
    ConnectDeadlineMs uint16
    AckDeadlineMs     uint16
    DaemonEnabled     bool
    SpoolOnBreach     bool
    MaxPayloadBytes   uint32
    DaemonPID         uint32
    Written           core.UnixMilli
}
func StatePath(projectRoot string) string
func ReadState(projectRoot string, fallback config.Config) State
func WriteState(projectRoot string, s State) error
func RemoveState(projectRoot string) error
func StateFromConfig(cfg config.Config) State

// ── package daemon ──────────────────────────────────────────────────────────
type Daemon interface {
    Run(ctx context.Context) error
    Registry() *SessionRegistry
    Drain(ctx context.Context) (int, error)
    Idle() IdleController
    Stop(ctx context.Context) error
}
type Options struct {
    ProjectRoot string; Cfg config.Config; Log logging.Logger
    Metrics obs.Registry; Clock core.Clock
    Store store.Store; Ledger negknow.Ledger; Sketches *SketchSet
    Graph dag.Graph; Grammar grammar.Sequitur; Sched scheduler.Runtime
    Checkpoints checkpoint.Writer
    Routes *ipc.Router
    binds  []func(*Services)
}
func NewOptions(projectRoot string, cfg config.Config) Options
func New(o Options) (Daemon, error)
func (Options) Handle(op ipc.Op, h ipc.Handler)     // normative §5.4
func (o *Options) Bind(fn func(*Services))          // late binding for waves 2–3
var ErrOptionsUninitialized = errors.New("qompack: daemon.Options not built with NewOptions")

type Services struct {
    Store store.Store; Ledger negknow.Ledger; Sketches *SketchSet
    Graph dag.Graph; Grammar grammar.Sequitur; Sched scheduler.Runtime
    Checkpoints checkpoint.Writer
    // Function seams — nil until the owning subplan binds them. Every call site checks for nil.
    ObserveTool    func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08
    ObservePrompt  func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08
    ObserveStop    func(ctx context.Context, e hookio.Event, subagent bool) (hookio.Output, error) // SP-08
    SessionStart   func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08 + SP-11
    SessionEnd     func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-08
    PreCompact     func(ctx context.Context, e hookio.Event) (hookio.Output, error) // SP-10
    Rehydrate      func(ctx context.Context, e hookio.Event) (string, error)        // SP-11
    MCPInitialized func() bool                                                      // SP-13
    StatusExtra    func(ctx context.Context) map[string]any                         // SP-14
}
func ServicesFrom(ctx context.Context) *Services
func RegistryFrom(ctx context.Context) *SessionRegistry
func DaemonFrom(ctx context.Context) Daemon
func DeclareProducers(s *Services)

type IdleController interface {
    Register(name string, prio int, fn func(ctx context.Context) error)
    Notify(lastActivity core.UnixMilli)
    IsIdle(now core.UnixMilli) bool
    RunOnce(ctx context.Context, budget time.Duration) (ran []string, err error)
}

type SessionState struct {
    ID core.SessionID; Source string; TranscriptPath string
    StartedTS, LastActivityTS, EndedTS core.UnixMilli
    Events, Dropped, Externalized int64
    Hot ipc.HotPathMode
    Live bool
}
type SessionRegistry struct{ /* … */ }
func (r *SessionRegistry) Ensure(e hookio.Event, now core.UnixMilli) *SessionState
func (r *SessionRegistry) Touch(id core.SessionID, now core.UnixMilli)
func (r *SessionRegistry) End(id core.SessionID, now core.UnixMilli)
func (r *SessionRegistry) Live() int
func (r *SessionRegistry) Snapshot() []SessionState
func (r *SessionRegistry) LastActivity() core.UnixMilli   // newest LastActivityTS across all sessions
func (r *SessionRegistry) HotMode() ipc.HotPathMode
func (r *SessionRegistry) SetHotMode(m ipc.HotPathMode, reason string)

type SketchSet struct{ /* … */ }
func NewSketchSet(cfg config.Config) *SketchSet
func (s *SketchSet) Load(projectRoot string, log logging.Logger) error
func (s *SketchSet) Save(projectRoot string, log logging.Logger) error
func (s *SketchSet) Read(fn func(*SketchSet))
func (s *SketchSet) Write(fn func(*SketchSet))

type LockInfo struct{ PID int; Started core.UnixMilli; Addr string; Version string }
type Lock struct{ /* … */ }
func AcquireLock(projectRoot string, a ipc.Addr, clk core.Clock) (*Lock, error)
func ReadLock(projectRoot string) (LockInfo, bool)
func (l *Lock) Heartbeat() error
func (l *Lock) Release() error
var ErrLockHeld = errors.New("qompack: daemon already running for this project")

func EnsureRunning(projectRoot, self string, log logging.Logger, clk core.Clock) (spawned bool, err error)
func SpawnDetached(projectRoot, self string) error

type StatusSnapshot struct {
    Mode            string                     `json:"mode"`
    Hot             string                     `json:"hot"`
    PID             int                        `json:"pid"`
    Addr            string                     `json:"addr"`
    UptimeSeconds   float64                    `json:"uptime_seconds"`
    Sessions        []SessionState             `json:"sessions"`
    Budgets         []obs.BudgetBreach         `json:"budget_breaches"`
    Latency         map[string]obs.HistSnapshot `json:"latency"`
    Counters        map[string]int64           `json:"counters"`
    Contract        []contract.Result          `json:"contract"`
    SpoolFiles      int                        `json:"spool_files"`
    LoudTail        []string                   `json:"loud_tail"`
    Extra           map[string]any             `json:"extra,omitempty"`
}

// ── package contract ────────────────────────────────────────────────────────
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
type Severity uint8
const (SevInfo Severity = iota; SevWarn; SevCritical)
type Result struct {
    ID ID; OK bool; Severity Severity
    Expected, Observed string; TS core.UnixMilli; Detail string
}
type Mode uint8
const (ModeFull Mode = iota; ModeDegradedPassive; ModeOff)
func (m Mode) String() string
func ParseMode(s string) (Mode, bool)
func (m Mode) MayAct() bool     // false for ModeDegradedPassive and ModeOff
func (m Mode) MayRecord() bool  // false only for ModeOff

type Assertion struct { ID ID; Severity Severity; Description string; Check func(ctx context.Context, e Env) Result }
type Env struct {
    ProjectRoot string; Event hookio.Event; Cfg config.Config
    Store store.Store; Log logging.Logger; Clock core.Clock
    History History
}
type Monitor interface {
    Register(a Assertion) error
    RunAll(ctx context.Context, e Env) ([]Result, Mode)
    Mode() Mode
    Degrade(reason string, results []Result)
    Restore(reason string)
    Report() []Result
}
func NewMonitor(log logging.Logger, m obs.Registry, statePath string) Monitor
func StandardAssertions() []Assertion

type History struct {
    Version               int              `json:"version"`
    Sessions              int              `json:"sessions"`
    LastSessionID         core.SessionID   `json:"last_session_id"`
    LastMarkerTS          core.UnixMilli   `json:"last_marker_ts"`
    StartsWithoutMarker   int              `json:"starts_without_marker"`
    LastPreCompactTS      core.UnixMilli   `json:"last_precompact_ts"`
    LastPreCompactSession core.SessionID   `json:"last_precompact_session"`
    PreCompactWallMs      []int            `json:"precompact_wall_ms"`   // capped at the 64 newest
    PreCompactTimeoutMs   int              `json:"precompact_timeout_ms"` // written by the daemon from pluginmanifest; 0 = unknown
    PreCompactInstr       string           `json:"precompact_instr,omitempty"` // ≤256 chars of the emitted customInstructions
    AwaitingCompactStart  bool             `json:"awaiting_compact_start"`
    Sentinel              Sentinel         `json:"sentinel"`
    MCPInitialized        bool             `json:"mcp_initialized"`
    CleanRuns             int              `json:"clean_runs"`
    Mode                  Mode             `json:"mode"`
    DegradedReason        string           `json:"degraded_reason,omitempty"`
    DegradedSince         core.UnixMilli   `json:"degraded_since,omitempty"`
    Last                  []Result         `json:"last"`
}
func LoadHistory(statePath string) History
func SaveHistory(statePath string, h History) error

type Sentinel struct {
    Token     string         `json:"token"`
    IssuedTS  core.UnixMilli `json:"issued_ts"`
    Session   core.SessionID `json:"session"`
    Observed  bool           `json:"observed"`
    Chances   int            `json:"chances"`
}
func MintSentinel(sess core.SessionID, now core.UnixMilli) Sentinel
func RenderSentinel(s Sentinel) string          // the additionalContext line
func ScanTranscriptTail(path string, token string, tailBytes int64) (bool, error)

func DeclareProducer(id ID)
func HasProducer(id ID) bool
func ResetProducers()   // tests only
func MarkerPath(projectRoot string) string
func WriteMarker(projectRoot string, sess core.SessionID, now core.UnixMilli) error

// ── package obs (one added file: budgets.go) ────────────────────────────────
type Budget struct {
    ID      string        // "B-A" … "B-F"
    Metric  string        // histogram name
    Limit   time.Duration
    Gated   bool
    Clock   string        // the §2.4 "Clock" column, verbatim
}
func Budgets(cfg config.Config) []Budget
func BudgetByID(cfg config.Config, id string) (Budget, bool)
const (
    MetricHookControlled = "hook.controlled"          // B-A (estimated, daemon-side)
    MetricHookObserved   = "hook.controlled.observed" // B-A lower bound, purely daemon-observed
    MetricL0Ingest       = "l0.ingest"                // B-B
    MetricL0Process      = "l0.process"               // B-C
    MetricHookWall       = "hook.wall"                // B-D (bench harness only)
    MetricCheckpointFin  = "checkpoint.finalize"      // B-E
    MetricMCPToolCall    = "mcp.tool_call"            // B-F
)
```

---

