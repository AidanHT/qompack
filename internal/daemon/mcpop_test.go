package daemon

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/store"
)

// The `mcp` op's own tests: the wiring seam InstallMCPOp installs, the §12.1 observable it flips,
// and the four things the daemon resolves that the stdio process cannot — the session, the turn,
// a tool error that is a RESULT, and a transport error that is not.
//
// Every one of these drives the handler InstallMCPOp registered rather than calling handleMCPOp
// directly, because the registration IS half of what is under test: an op registered under the
// wrong key, or a Bind appended to a copy of Options, compiles and then never runs.

// mcpOpSession is the session id these tests name explicitly, kept distinct from the ids the
// registry rows below invent so a failure message says which one it saw.
const mcpOpSession core.SessionID = "sess-mcpop"

// mcpOpLogger records Warn and Loud lines so a degradation test can assert the failure was
// SURFACED, not merely survived (§12: never silent). It is separate from registry_test.go's
// captureLogger, which records Loud only.
type mcpOpLogger struct {
	warns []string
	louds []string
}

// With returns the receiver: these tests assert on messages, never on accumulated fields.
func (l *mcpOpLogger) With(...any) logging.Logger { return l }

// Debug, Info and Error are ignored; no assertion in this file reads them.
func (l *mcpOpLogger) Debug(string, ...any) {}

// Info is ignored.
func (l *mcpOpLogger) Info(string, ...any) {}

// Warn records the message, which is how the history-write-failure row proves a Warn fired.
func (l *mcpOpLogger) Warn(msg string, _ ...any) { l.warns = append(l.warns, msg) }

// Error is ignored.
func (l *mcpOpLogger) Error(string, ...any) {}

// Loud records the message.
func (l *mcpOpLogger) Loud(msg string, _ ...any) { l.louds = append(l.louds, msg) }

// mcpOpLogger must satisfy the seam every daemon collaborator writes through.
var _ logging.Logger = (*mcpOpLogger)(nil)

// mcpOpDrops is a mcp.DropReporter that answers with a fixed, empty report.
//
// It exists so `dropped` reaches its own "no live session" branch rather than the earlier
// "rehydrator not present in this build" one: both render available:false, and a test that could
// not tell them apart would pass against a build with no drop reporter at all.
type mcpOpDrops struct{ calls int }

type mcpOpRedactor struct{ policy redact.Redactor }

func (r mcpOpRedactor) Redact(input []byte) ([]byte, []string) {
	output, matches := r.policy.Redact(input)
	rules := make([]string, len(matches))
	for i, match := range matches {
		rules[i] = match.Rule
	}
	return output, rules
}

// CurrentDrops reports nothing dropped, counting the call.
func (d *mcpOpDrops) CurrentDrops(context.Context, core.SessionID) ([]checkpoint.DropEntry, error) {
	d.calls++
	return nil, nil
}

// mcpOpDrops must satisfy the port ToolDeps declares.
var _ mcp.DropReporter = (*mcpOpDrops)(nil)

// mcpOpFixture is one installed `mcp` op with everything the assertions need to reach around it:
// the handler as registered, the Services the recorded Bind produced, a real store, and a real
// registry whose contents each test arranges for itself.
type mcpOpFixture struct {
	Root    string
	Cfg     config.Config
	Opts    *Options
	Store   store.Store
	Svc     *Services
	Handler ipc.Handler
	Reg     *SessionRegistry
	Clock   *fakeClock
	Log     *mcpOpLogger
	Drops   *mcpOpDrops
}

// newMCPOpFixture installs the op over a real store in a fresh project root.
//
// The store is REAL rather than a fake because three of the rows below assert on what the tool
// left behind — the ephemeral ToolUseRecord's session and turn — and a fake store would let the
// resolver be wrong in exactly the way that matters.
func newMCPOpFixture(t *testing.T) *mcpOpFixture {
	t.Helper()

	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)), "EnsureLayout(%s)", root)

	clk := newFakeClock(epoch)
	cfg := testConfig()
	log := &mcpOpLogger{}
	reg := obs.New(clk)

	st, err := store.Open(root, cfg, store.Deps{Log: logging.Nop(), Clock: clk})
	require.NoError(t, err, "store.Open(%s)", root)
	// The store holds open append-only handles on index/*.jsonl; on Windows an unreleased handle
	// fails this test's own t.TempDir cleanup as a confusing post-test error.
	t.Cleanup(func() { _ = st.Close() })

	o := NewOptions(root, cfg)
	o.Log = log
	o.Metrics = reg
	o.Clock = clk
	o.Store = st

	drops := &mcpOpDrops{}
	require.NoError(t, InstallMCPOp(&o, mcp.ToolDeps{
		Store:       st,
		Redactor:    mcpOpRedactor{policy: redact.New(cfg)},
		Rehydrator:  drops,
		Cfg:         cfg,
		ProjectRoot: root,
		Clock:       clk,
		Log:         log,
		Metrics:     reg,
	}), "InstallMCPOp")

	h, ok := o.Handler(ipc.OpMCP)
	require.True(t, ok, "InstallMCPOp must register a handler for the %q op", ipc.OpMCP)

	// The bind list is applied here exactly as daemon.New applies it: seeded Services first, then
	// every recorded Bind in registration order. Reaching into o.binds is what makes this a test
	// of the WIRING rather than of a hand-built Services.
	svc := &Services{Store: st}
	for _, bind := range o.binds {
		bind(svc)
	}

	return &mcpOpFixture{
		Root: root, Cfg: cfg, Opts: &o, Store: st, Svc: svc,
		Handler: h, Reg: NewSessionRegistry(), Clock: clk, Log: log, Drops: drops,
	}
}

// ctx returns the context the daemon's own dispatchOp would hand a handler: the Services and the
// SessionRegistry bound under this package's unexported keys.
func (f *mcpOpFixture) ctx() context.Context {
	return withRegistry(withServices(context.Background(), f.Svc), f.Reg)
}

// call marshals m and drives the registered handler with session on the ipc.Request.
func (f *mcpOpFixture) call(t *testing.T, session core.SessionID, m MCPOpRequest) ipc.Response {
	t.Helper()
	raw, err := json.Marshal(m)
	require.NoError(t, err, "marshalling the op payload")
	return f.Handler(f.ctx(), ipc.Request{Op: ipc.OpMCP, Session: session, Reply: true, Raw: raw})
}

// callTool is call for a kind=="call" payload, the shape the stdio process forwards.
func (f *mcpOpFixture) callTool(t *testing.T, session core.SessionID, name string, args any) ipc.Response {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err, "marshalling arguments for %s", name)
	return f.call(t, session, MCPOpRequest{Kind: MCPKindCall, Name: name, Args: raw})
}

// decode unpacks an OK response's Data as the op's own response payload.
func decodeMCPOp(t *testing.T, resp ipc.Response) MCPOpResponse {
	t.Helper()
	require.True(t, resp.OK, "the op must have succeeded; err=%q", resp.Err)
	var out MCPOpResponse
	require.NoError(t, json.Unmarshal(resp.Data, &out), "decoding MCPOpResponse from %s", resp.Data)
	return out
}

// mcpOpBody decodes the single JSON text block every tool result carries into v.
func mcpOpBody(t *testing.T, payload MCPOpResponse, v any) {
	t.Helper()
	require.Len(t, payload.Content, 1, "a tool result carries exactly one text block")
	require.NoError(t, json.Unmarshal([]byte(payload.Content[0].Text), v),
		"the tool body must be JSON: %s", payload.Content[0].Text)
}

// seedToolUse stores body and indexes it as one tool use, returning the record's id.
func (f *mcpOpFixture) seedToolUse(t *testing.T, tool, path, body string, turn core.TurnIndex) core.ToolUseID {
	t.Helper()
	res, err := f.Store.PutBytes(context.Background(), []byte(body), store.PutOptions{Tool: tool, Path: path})
	require.NoError(t, err, "PutBytes(%s)", path)

	id := core.ToolUseID("toolu-mcpop-" + string(rune('a'+int(turn)%26)) + res.Root.Hash.Short())
	require.NoError(t, f.Store.RecordToolUse(context.Background(), store.ToolUseRecord{
		ID: id, Session: mcpOpSession, Turn: turn, TS: core.NowMilli(f.Clock),
		Tool: tool, ArgsPreview: path, Root: res.Root.Hash, Path: path, Bytes: int64(len(body)),
	}), "RecordToolUse(%s)", id)
	return id
}

// ephemeralRecordOf looks up the ephemeral ToolUseRecord a successful retrieval wrote, addressed
// by the tool_use_id the response's own _meta carries.
//
// That record is where the daemon's two resolutions become observable: §8.7 stores every
// retrieval result back under a synthetic id, stamped with the session and turn the handler was
// given — which, for a call the stdio process forwarded, are precisely the ones resolveSession
// and resolveTurn produced.
func (f *mcpOpFixture) ephemeralRecordOf(t *testing.T, payload MCPOpResponse) store.ToolUseRecord {
	t.Helper()
	id, ok := payload.Meta["tool_use_id"].(string)
	require.True(t, ok, "a successful ephemeral result must report its tool_use_id; meta=%v", payload.Meta)
	rec, err := f.Store.ToolUse(context.Background(), core.ToolUseID(id))
	require.NoError(t, err, "ToolUse(%s)", id)
	return rec
}

// TestInstallMCPOpRegistersOp asserts both halves of the wiring InstallMCPOp exists to perform:
// the op is routable, and the recorded Bind — applied the way New applies it — leaves
// Services.MCPInitialized non-nil.
//
// The second half is the one that silently rots. Bind is a pointer-receiver append onto an
// unexported slice, so an InstallMCPOp handed an Options by value would compile, register nothing
// observable, and leave CMCPRegistered undeclared for the life of the process.
func TestInstallMCPOpRegistersOp(t *testing.T) {
	t.Parallel()

	o := NewOptions(t.TempDir(), testConfig())
	require.NoError(t, InstallMCPOp(&o, mcp.ToolDeps{Cfg: o.Cfg, ProjectRoot: o.ProjectRoot}))

	h, ok := o.Handler(ipc.OpMCP)
	require.True(t, ok, "the %q op must be registered", ipc.OpMCP)
	require.NotNil(t, h)

	svc := &Services{}
	require.Len(t, o.binds, 1, "InstallMCPOp records exactly one Bind")
	for _, bind := range o.binds {
		bind(svc)
	}
	require.NotNil(t, svc.MCPInitialized, "the recorded Bind must set the MCPInitialized seam")
}

// TestInstallMCPOpRejectsANilOptions pins the one refusal InstallMCPOp makes: without an Options
// there is nowhere to register, and returning nil would hide the wiring omission entirely.
func TestInstallMCPOpRejectsANilOptions(t *testing.T) {
	t.Parallel()

	require.Error(t, InstallMCPOp(nil, mcp.ToolDeps{}))
}

// TestMCPInitializedSeamFlipsAfterInitialize drives the §12.1 observable end to end: false before
// any handshake, true after one, and — with that Services — CMCPRegistered actually declared.
//
// The producer half matters as much as the flag: an assertion whose producer was never declared
// reports OK/SevInfo/not-yet-implemented and its real Check never runs, so a working MCP server
// beside an undeclared producer looks exactly like no MCP server at all.
func TestMCPInitializedSeamFlipsAfterInitialize(t *testing.T) {
	contract.ResetProducers()
	t.Cleanup(contract.ResetProducers)

	f := newMCPOpFixture(t)
	require.False(t, f.Svc.MCPInitialized(f.ctx()),
		"the observable must be false until the stdio server reports its handshake")

	resp := f.call(t, mcpOpSession, MCPOpRequest{Kind: MCPKindInitialized})
	require.True(t, resp.OK, "an initialized report must succeed; err=%q", resp.Err)
	require.True(t, f.Svc.MCPInitialized(f.ctx()), "the observable must be true after the handshake")

	DeclareProducers(f.Svc)
	require.True(t, contract.HasProducer(contract.CMCPRegistered),
		"a bound MCPInitialized seam must declare %s", contract.CMCPRegistered)
}

// TestInitializedWritesContractHistory asserts the handshake lands in state/history.json — the
// file SP-05's assertion reads — and nowhere else.
//
// state/contract.json is checked byte-for-byte because writing a SessionHistory over the Monitor's
// own mode/reason/results file is the corruption internal/contract/history.go warns about, and it
// would be invisible: the assertion would go on reporting "initialize-not-received" while the
// tools answered normally beside it.
//
// Idempotence is asserted with a SENTINEL key rather than by comparing the bytes to themselves: a
// re-marshalled SessionHistory is byte-identical to the one it replaced, so only a field the
// struct cannot round-trip can tell "did not rewrite" from "rewrote the same thing".
func TestInitializedWritesContractHistory(t *testing.T) {
	f := newMCPOpFixture(t)

	contractPath := paths.Of(f.Root).State + string(os.PathSeparator) + "contract.json"
	const contractBytes = `{"mode":"full","reason":"seeded by TestInitializedWritesContractHistory"}`
	require.NoError(t, os.WriteFile(paths.Long(contractPath), []byte(contractBytes), 0o600))

	require.True(t, f.call(t, mcpOpSession, MCPOpRequest{Kind: MCPKindInitialized}).OK)

	histPath := contract.HistoryPath(f.Root)
	require.True(t, contract.LoadHistory(histPath).MCPInitialized,
		"the handshake must set MCPInitialized in %s", histPath)

	after, err := os.ReadFile(paths.Long(contractPath))
	require.NoError(t, err)
	require.Equal(t, contractBytes, string(after),
		"state/contract.json is the Monitor's file and must be byte-unchanged")

	// A key SessionHistory has no field for: a rewrite drops it, a short-circuit keeps it.
	const sentinel = `{"version":1,"mcp_initialized":true,"seat_f_sentinel":"kept"}`
	require.NoError(t, os.WriteFile(paths.Long(histPath), []byte(sentinel), 0o600))

	require.True(t, f.call(t, mcpOpSession, MCPOpRequest{Kind: MCPKindInitialized}).OK)

	again, err := os.ReadFile(paths.Long(histPath))
	require.NoError(t, err)
	require.Equal(t, sentinel, string(again),
		"a second handshake must not rewrite an already-initialized history")
}

// TestInitializedSurvivesHistoryWriteFailure is §12.1's severity made executable: the observable
// is SevInfo, so a history that cannot be written surfaces a Warn and changes nothing else.
//
// The failure is provoked by pre-creating state/history.json as a DIRECTORY, which is the one
// shape that makes an atomic write fail identically on Windows and on POSIX — the rename lands on
// a directory either way. Making the directory itself unwritable does not work on Windows, where
// the read-only attribute is close to a no-op for directories.
func TestInitializedSurvivesHistoryWriteFailure(t *testing.T) {
	f := newMCPOpFixture(t)

	histPath := contract.HistoryPath(f.Root)
	require.NoError(t, os.MkdirAll(paths.Long(histPath), 0o755),
		"pre-creating %s as a directory is what makes the atomic write fail", histPath)

	resp := f.call(t, mcpOpSession, MCPOpRequest{Kind: MCPKindInitialized})
	require.True(t, resp.OK, "a failed observable must never fail the handshake; err=%q", resp.Err)
	require.True(t, f.Svc.MCPInitialized(f.ctx()),
		"the in-memory observable is independent of whether it could be persisted")
	require.NotEmpty(t, f.Log.warns, "the failure must be surfaced, never silent (§12)")
}

// TestDaemonMCPOpDispatchesToolCall is the ordinary path: a forwarded tools/call reaches the
// daemon-side server, runs against the warm store handle, and comes back as OK:true with a
// decodable payload carrying hits.
func TestDaemonMCPOpDispatchesToolCall(t *testing.T) {
	f := newMCPOpFixture(t)
	f.seedToolUse(t, "Read", "src/pool.ts", "export const poolTimeoutMs = 30_000;\n// a pool timeout here\n", 1)

	resp := f.callTool(t, mcpOpSession, mcp.ToolRecall, map[string]any{"query": "pool timeout", "k": 5})
	payload := decodeMCPOp(t, resp)
	require.False(t, payload.IsError, "recall over a seeded store must not be a tool error: %v", payload.Content)

	var body struct {
		Hits  []mcp.RecallHit `json:"hits"`
		Count int             `json:"count"`
		Found bool            `json:"found"`
	}
	mcpOpBody(t, payload, &body)
	require.True(t, body.Found, "recall must report a hit for content it just indexed")
	require.NotEmpty(t, body.Hits)
	require.Equal(t, len(body.Hits), body.Count)
}

// TestDaemonMCPOpUnknownToolIsPayloadError pins the §5.16 distinction the whole op rests on: the
// transport was healthy and the op routed correctly, so an unknown NAME is OK:true carrying a
// result the model reads — never ipc.Response{OK:false}, which a client would retry.
func TestDaemonMCPOpUnknownToolIsPayloadError(t *testing.T) {
	f := newMCPOpFixture(t)

	resp := f.callTool(t, mcpOpSession, "nope", map[string]any{})
	payload := decodeMCPOp(t, resp)
	require.True(t, payload.IsError, "an unknown tool must be a tool error, not a transport error")
	require.Len(t, payload.Content, 1)
	require.Contains(t, payload.Content[0].Text, `unknown tool "nope"`)
}

// TestDaemonMCPOpMalformedPayloadIsTransportError is the other side of that line: bytes that are
// not an op payload at all never reached a tool, so there is no tool result to return and the
// refusal must name the op it refused.
func TestDaemonMCPOpMalformedPayloadIsTransportError(t *testing.T) {
	f := newMCPOpFixture(t)

	resp := f.Handler(f.ctx(), ipc.Request{
		Op: ipc.OpMCP, Session: mcpOpSession, Reply: true, Raw: json.RawMessage(`this is not json`),
	})
	require.False(t, resp.OK, "a malformed payload is a transport failure, not a tool result")
	require.Contains(t, resp.Err, "mcp:", "the refusal must name the op it refused: %q", resp.Err)
	require.Empty(t, resp.Data, "a refused op carries no tool payload")
}

// TestDaemonMCPOpResolvesSessionFromRegistry is the reason the handlers execute daemon-side at
// all: Claude Code hands an MCP server no session_id, so the stdio process sends none and the
// daemon answers from its own registry — preferring the live session with the most recent
// activity.
//
// It is asserted through the ephemeral record rather than through the response, because the record
// is where the resolution is actually USED: §8.7 files every retrieval result under the session it
// belonged to, and a wrong answer there mis-files retrieval output for the rest of the session.
func TestDaemonMCPOpResolvesSessionFromRegistry(t *testing.T) {
	f := newMCPOpFixture(t)
	id := f.seedToolUse(t, "Bash", "", "the bytes an expand hands back\n", 1)

	const (
		ended = core.SessionID("sess-ended")
		live  = core.SessionID("sess-live")
	)
	f.Reg.Ensure(&hookio.Event{SessionID: ended}, 1_000)
	f.Reg.End(ended, 2_000)
	f.Reg.Ensure(&hookio.Event{SessionID: live}, 3_000)

	// Session deliberately EMPTY: this is exactly what forwardMCPCall sends.
	resp := f.callTool(t, "", mcp.ToolExpand, map[string]any{"tool_use_id": string(id)})
	payload := decodeMCPOp(t, resp)
	require.False(t, payload.IsError, "expand by tool_use_id must succeed: %v", payload.Content)

	require.Equal(t, live, f.ephemeralRecordOf(t, payload).Session,
		"the daemon must file the retrieval under the live session, not the ended one")
}

// TestDaemonMCPOpEmptyRegistryDegrades: with nothing to resolve to, the session is "" and every
// handler still answers. `dropped` says available:false because a drop report without a session is
// not a report at all, while `expand` — addressable by hash alone — succeeds and files its record
// under an empty session rather than inventing one.
func TestDaemonMCPOpEmptyRegistryDegrades(t *testing.T) {
	f := newMCPOpFixture(t)
	id := f.seedToolUse(t, "Bash", "", "content that outlived its context window\n", 1)
	require.Zero(t, f.Reg.Len(), "this row is specifically the empty-registry case")

	dropped := decodeMCPOp(t, f.callTool(t, "", mcp.ToolDropped, map[string]any{}))
	require.False(t, dropped.IsError, "an unresolvable session is a degradation, not a tool error")
	var drops struct {
		Available *bool  `json:"available"`
		Reason    string `json:"reason"`
	}
	mcpOpBody(t, dropped, &drops)
	require.NotNil(t, drops.Available, "dropped must state its availability explicitly")
	require.False(t, *drops.Available)
	require.Equal(t, "no live session", drops.Reason)
	require.Zero(t, f.Drops.calls, "with no session there is nothing to ask the reporter about")

	expanded := decodeMCPOp(t, f.callTool(t, "", mcp.ToolExpand, map[string]any{"tool_use_id": string(id)}))
	require.False(t, expanded.IsError, "expand is addressable by id alone: %v", expanded.Content)
	require.Equal(t, core.SessionID(""), f.ephemeralRecordOf(t, expanded).Session,
		"an unknown session must be recorded as unknown, never guessed")
}

// TestDaemonMCPOpResolvesTurnFromCurrentSegment pins the second resolution: SessionState carries
// no turn index, so "where are we now" comes from the store's currently open segment, whose
// EndTurn SP-06 initialises to its StartTurn — making max(StartTurn, EndTurn) a defined, monotone
// lower bound rather than a guess.
//
// The no-open-segment half is asserted in the same test because the two answers are one decision:
// an honest 0 and a resolved 12 must come out of the same code path, and a test that only covered
// the happy side would pass against a resolver that errored on the other.
func TestDaemonMCPOpResolvesTurnFromCurrentSegment(t *testing.T) {
	f := newMCPOpFixture(t)
	id := f.seedToolUse(t, "Bash", "", "expandable bytes\n", 1)

	const openStartTurn = core.TurnIndex(12)
	_, err := f.Store.Segments().Open(context.Background(), store.Segment{
		Session: mcpOpSession, StartTurn: openStartTurn, EndTurn: openStartTurn,
		StartTS: core.NowMilli(f.Clock),
	})
	require.NoError(t, err, "opening the segment the turn is resolved from")

	resolved := decodeMCPOp(t, f.callTool(t, mcpOpSession, mcp.ToolExpand, map[string]any{"tool_use_id": string(id)}))
	require.False(t, resolved.IsError, "expand must succeed: %v", resolved.Content)
	require.Equal(t, openStartTurn, f.ephemeralRecordOf(t, resolved).Turn,
		"Turn:0 must be resolved from the open segment's StartTurn")

	// A session with no open segment of its own: honest 0, and no error anywhere.
	const other = core.SessionID("sess-no-segment")
	unresolved := decodeMCPOp(t, f.callTool(t, other, mcp.ToolExpand, map[string]any{"tool_use_id": string(id)}))
	require.False(t, unresolved.IsError, "a session with no segment must still expand: %v", unresolved.Content)
	rec := f.ephemeralRecordOf(t, unresolved)
	require.Equal(t, core.TurnIndex(0), rec.Turn, "no open segment means an honest zero, not an error")
	require.Equal(t, other, rec.Session)
}

// TestDaemonMCPOpHonoursAnExplicitTurn asserts the caller's own turn wins when it has one: only a
// zero Turn is resolved here, because a caller that knows where it is knows better than a lower
// bound derived from a segment.
func TestDaemonMCPOpHonoursAnExplicitTurn(t *testing.T) {
	f := newMCPOpFixture(t)
	id := f.seedToolUse(t, "Bash", "", "expandable bytes\n", 1)

	_, err := f.Store.Segments().Open(context.Background(), store.Segment{
		Session: mcpOpSession, StartTurn: 12, EndTurn: 12, StartTS: core.NowMilli(f.Clock),
	})
	require.NoError(t, err)

	args, err := json.Marshal(map[string]any{"tool_use_id": string(id)})
	require.NoError(t, err)
	resp := f.call(t, mcpOpSession, MCPOpRequest{
		Kind: MCPKindCall, Name: mcp.ToolExpand, Args: args, Turn: 7,
	})
	payload := decodeMCPOp(t, resp)
	require.False(t, payload.IsError, "expand must succeed: %v", payload.Content)
	require.Equal(t, core.TurnIndex(7), f.ephemeralRecordOf(t, payload).Turn,
		"an explicit turn must not be overwritten by the segment lower bound")
}

// TestDaemonMCPOpUnknownKindIsRefused pins the third branch of the payload switch: a kind this
// build does not understand is a transport-level refusal naming the kind, not a silent dispatch to
// the tool router.
func TestDaemonMCPOpUnknownKindIsRefused(t *testing.T) {
	f := newMCPOpFixture(t)

	resp := f.call(t, mcpOpSession, MCPOpRequest{Kind: "teleport"})
	require.False(t, resp.OK)
	require.Contains(t, resp.Err, "teleport", "the refusal must name the kind it refused: %q", resp.Err)
}
