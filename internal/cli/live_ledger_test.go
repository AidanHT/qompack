package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// The Phase 4 live lane's retrieval findings (plans/sdd/V6-closeout/live/report.md, owner decision
// D45), reproduced without a host: the shipped composition (runDaemon through Dispatch), the shipped
// hook clients (Dispatch of the real hook subcommands, in process), and the `mcp` op exactly as
// `qompack mcp` forwards it — with an EMPTY session, because the stdio process has none to send.
//
//   - F-UAT01-2 / install D1: the MCP server's own tool_use record was filed at turn 0 after the
//     hook records of later turns, so fsck's index.tool_use row failed after any session with an
//     MCP call.
//   - retrieval D1: the elimination ledger opened only at the daemon's first compaction, so before
//     one (and after any daemon restart) record_eliminated and already_tried answered "not present
//     in this build".
//   - retrieval D2: session-scoped eliminations were stored with "session":"", so every later
//     session of the project saw them.
//   - retrieval D3: already_tried kept answering active in the session that changed a dependency.
//   - retrieval D5: a checkpoint sealed after an active elimination carried eliminated [] and
//     decisions [], and `why` withheld an MCP-origin record's evidence as having no path provenance.
//   - retrieval D8: timeline reported the live session's open segment as turns 0-0 and accepted an
//     inverted range.
//
// Nothing here reads the developer's own Claude settings: every rig points HOME, USERPROFILE and
// CLAUDE_CONFIG_DIR at a temporary directory before the daemon starts, because the retrieval
// handlers consult the host's permission rules for any path-bearing record.

// liveWait bounds waiting for the daemon's ingest workers to publish what a hook delivered, and
// liveTick is how often the index is re-read meanwhile. The hooks under test ACK before a worker
// publishes, so every assertion about the index waits for the record it is about.
const (
	liveWait = 15 * time.Second
	liveTick = 20 * time.Millisecond
)

// The elimination every ledger row records: a session-scoped claim about src/pool.go that rests on
// config/pool.yaml, the shape UAT-08 used.
const (
	liveTarget   = "src/pool.go:DialPool"
	liveApproach = "widen pool timeout"
	liveReason   = "the stall comes from the server-side max_idle cap in config/pool.yaml, " +
		"not from the client timeout"
	liveDepPath = "config/pool.yaml"
)

// liveRig is one running daemon over a fresh project and the hook payload builders its rows share.
type liveRig struct {
	root string
	home string
}

// newLiveRig starts runDaemon over a fresh project with a fake home, and stops it at cleanup.
func newLiveRig(t *testing.T) *liveRig {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	root := bootstrapProject(t)
	stop := bootstrapDaemon(t, root)
	t.Cleanup(stop)
	return &liveRig{root: root, home: home}
}

// getenv pins the hook clients' project root. A hook client resolves its root from the process's
// working directory, as the host runs it from the project; this test process runs from the package
// directory, so QOMPACK_PROJECT_ROOT (paths.Resolve's override) names the rig's project instead.
// Without it the client scopes every Read against the wrong root and refuses it as out of project.
func (r *liveRig) getenv(k string) string {
	if k == "QOMPACK_PROJECT_ROOT" {
		return r.root
	}
	return ""
}

// hook drives one shipped hook subcommand in process for sess and requires it to exit 0.
func (r *liveRig) hook(t *testing.T, sess core.SessionID, cmd []string, event string, extra map[string]any) {
	t.Helper()
	p := map[string]any{
		"hook_event_name": event,
		"session_id":      string(sess),
		"cwd":             r.root,
		"transcript_path": filepath.Join(r.root, "transcript.jsonl"),
	}
	for k, v := range extra {
		p[k] = v
	}
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), hookCmds(),
		append(append([]string{"qompack"}, cmd...), "--project", r.root),
		Env{Getenv: r.getenv, Stdin: bytes.NewReader(raw), Clock: testClock(), HomeDir: r.home},
		&out, &errw)
	require.Equal(t, ExitOK, code, "%v must exit 0: %s", cmd, errw.String())
}

func (r *liveRig) start(t *testing.T, sess core.SessionID) {
	t.Helper()
	r.hook(t, sess, []string{"session-start"}, "SessionStart", map[string]any{"source": "startup"})
}

func (r *liveRig) prompt(t *testing.T, sess core.SessionID, text string) {
	t.Helper()
	r.hook(t, sess, []string{"observe", "prompt"}, "UserPromptSubmit", map[string]any{"prompt": text})
}

func (r *liveRig) endTurn(t *testing.T, sess core.SessionID) {
	t.Helper()
	r.hook(t, sess, []string{"observe", "stop"}, "Stop", map[string]any{"stop_hook_active": false})
}

func (r *liveRig) end(t *testing.T, sess core.SessionID) {
	t.Helper()
	r.hook(t, sess, []string{"flush"}, "SessionEnd", map[string]any{"reason": "exit"})
}

func (r *liveRig) compact(t *testing.T, sess core.SessionID) {
	t.Helper()
	r.hook(t, sess, []string{"checkpoint"}, "PreCompact", map[string]any{"trigger": "manual"})
}

// read writes content to rel inside the project and delivers the PostToolUse a Read of it produces,
// then waits until the daemon has indexed it.
func (r *liveRig) read(t *testing.T, sess core.SessionID, id, rel, content string) {
	t.Helper()
	abs := filepath.Join(r.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o700))
	require.NoError(t, os.WriteFile(abs, []byte(content), 0o600))
	r.hook(t, sess, []string{"observe", "tool"}, "PostToolUse", map[string]any{
		"tool_name":     "Read",
		"tool_use_id":   id,
		"tool_input":    map[string]any{"file_path": abs},
		"tool_response": map[string]any{"type": "text", "file": map[string]any{"filePath": abs, "content": content}},
	})
	r.waitFor(t, "the Read "+id, func(recs []liveToolUse) bool { return liveHas(recs, id) })
}

// liveToolUse is the part of one index/tool_use.jsonl content line these rows read, spelled with the
// store's compact keys.
type liveToolUse struct {
	Op      string `json:"op"`
	ID      string `json:"id"`
	Session string `json:"s"`
	Turn    int    `json:"turn"`
	Tool    string `json:"tool"`
}

// toolUses reads the index in file order, skipping supersede marks.
func (r *liveRig) toolUses(t *testing.T) []liveToolUse {
	t.Helper()
	b, err := paths.ReadFileShared(filepath.Join(paths.Of(r.root).Index, "tool_use.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []liveToolUse
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec liveToolUse
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Op != "" {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// waitFor waits until cond holds over the index and returns the index it held over.
func (r *liveRig) waitFor(t *testing.T, what string, cond func([]liveToolUse) bool) []liveToolUse {
	t.Helper()
	tick := time.NewTicker(liveTick)
	defer tick.Stop()
	deadline := time.NewTimer(liveWait)
	defer deadline.Stop()
	for {
		recs := r.toolUses(t)
		if cond(recs) {
			return recs
		}
		select {
		case <-deadline.C:
			t.Fatalf("the daemon never indexed %s; index=%+v\ndaemon log:\n%s", what, recs, r.dayLog(t))
		case <-tick.C:
		}
	}
}

// dayLog returns every daemon log file under .qompack/logs, for a failure message that says why an
// event never landed.
func (r *liveRig) dayLog(t *testing.T) string {
	t.Helper()
	dir := paths.Of(r.root).Logs
	entries, err := os.ReadDir(paths.Long(dir))
	if err != nil {
		return err.Error()
	}
	var b strings.Builder
	for _, e := range entries {
		if raw, rerr := paths.ReadFileShared(filepath.Join(dir, e.Name())); rerr == nil {
			b.WriteString("== " + e.Name() + "\n")
			b.Write(raw)
		}
	}
	return b.String()
}

// prompts counts sess's UserPromptSubmit records.
func livePrompts(recs []liveToolUse, sess core.SessionID) int {
	n := 0
	for _, rec := range recs {
		if rec.Session == string(sess) && rec.Tool == "UserPromptSubmit" {
			n++
		}
	}
	return n
}

func liveHas(recs []liveToolUse, id string) bool {
	for _, rec := range recs {
		if rec.ID == id {
			return true
		}
	}
	return false
}

// call forwards one tools/call exactly as `qompack mcp` does and decodes its one JSON body into v
// when v is non-nil.
func (r *liveRig) call(t *testing.T, name string, args map[string]any, v any) daemon.MCPOpResponse {
	t.Helper()
	payload := bootstrapCall(t, r.root, name, args)
	if v != nil {
		require.Len(t, payload.Content, 1, "%s must answer one text block: %v", name, payload.Content)
		require.NoError(t, json.Unmarshal([]byte(payload.Content[0].Text), v),
			"%s must answer JSON: %s", name, payload.Content[0].Text)
	}
	return payload
}

// liveEliminated is record_eliminated's acknowledgement, plus the not-present body's keys so a
// pre-fix answer decodes into something assertable rather than into a JSON error.
type liveEliminated struct {
	ID                  string   `json:"id"`
	Status              string   `json:"status"`
	Scope               string   `json:"scope"`
	Evidence            string   `json:"evidence"`
	DependsOnUnresolved []string `json:"depends_on_unresolved"`
	Available           *bool    `json:"available"`
	Reason              string   `json:"reason"`
}

// liveTried is already_tried's result.
type liveTried struct {
	State        string   `json:"state"`
	Reason       string   `json:"reason"`
	Note         string   `json:"note"`
	StaleBecause []string `json:"stale_because"`
	Degraded     bool     `json:"degraded"`
	Available    *bool    `json:"available"`
}

// recordElimination records the rows' shared elimination for whichever session the daemon resolves.
func (r *liveRig) recordElimination(t *testing.T) liveEliminated {
	t.Helper()
	var ack liveEliminated
	payload := r.call(t, mcp.ToolRecordEliminated, map[string]any{
		"target": liveTarget, "approach": liveApproach, "reason": liveReason,
		"depends_on": []string{liveDepPath},
	}, &ack)
	require.False(t, payload.IsError, "record_eliminated must record: %v", payload.Content)
	require.NotEmpty(t, ack.ID, "record_eliminated must answer the id it recorded, not %+v", ack)
	require.Equal(t, "active", ack.Status)
	require.Empty(t, ack.DependsOnUnresolved, "%s was captured, so it must resolve", liveDepPath)
	return ack
}

// alreadyTried asks the rows' shared question.
func (r *liveRig) alreadyTried(t *testing.T) liveTried {
	t.Helper()
	var got liveTried
	payload := r.call(t, mcp.ToolAlreadyTried, map[string]any{"target": liveTarget, "approach": liveApproach}, &got)
	require.False(t, payload.IsError, "already_tried must answer: %v", payload.Content)
	return got
}

// eliminationLines reads records/eliminations.jsonl's record lines.
func (r *liveRig) eliminationLines(t *testing.T) []map[string]any {
	t.Helper()
	b, err := paths.ReadFileShared(filepath.Join(paths.Of(r.root).Records, "eliminations.jsonl"))
	require.NoError(t, err, "the elimination must be on disk")
	var out []map[string]any
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		if _, isRecord := m["target"]; isRecord {
			out = append(out, m)
		}
	}
	return out
}

// primeSession opens sess and captures the dependency the elimination rests on.
func (r *liveRig) primeSession(t *testing.T, sess core.SessionID) {
	t.Helper()
	r.start(t, sess)
	r.prompt(t, sess, "find out why DialPool stalls")
	r.waitFor(t, "the first prompt", func(recs []liveToolUse) bool { return livePrompts(recs, sess) == 1 })
	r.read(t, sess, "toolu_live_pool_yaml_1", liveDepPath, "max_idle: 4\n")
	r.read(t, sess, "toolu_live_pool_go_1", "src/pool.go", "package pool\n\nfunc DialPool() {}\n")
}

// TestLiveMCPSelfRecordKeepsSessionTurnOrder is F-UAT01-2: after a session's hooks have moved it
// past turn 0, the record the MCP server files for its own answer must sit at the session's current
// turn, so index.tool_use stays monotone and fsck exits 0.
func TestLiveMCPSelfRecordKeepsSessionTurnOrder(t *testing.T) {
	const sess = core.SessionID("sess-live-mcp-turn")
	r := newLiveRig(t)

	r.start(t, sess)
	r.prompt(t, sess, "read README.md and tell me the code word")
	r.waitFor(t, "the first prompt", func(recs []liveToolUse) bool { return livePrompts(recs, sess) == 1 })
	r.read(t, sess, "toolu_live_readme", "README.md", "the code word is PELICAN\n")
	r.endTurn(t, sess)
	r.prompt(t, sess, "now recall PELICAN through the qompack tool")
	before := r.waitFor(t, "the second prompt", func(recs []liveToolUse) bool { return livePrompts(recs, sess) == 2 })
	floor := 0
	for _, rec := range before {
		if rec.Session == string(sess) && rec.Turn > floor {
			floor = rec.Turn
		}
	}
	require.Positive(t, floor, "fixture sanity: the session must have moved past turn 0")

	payload := r.call(t, mcp.ToolExpand, map[string]any{"tool_use_id": "toolu_live_readme"}, nil)
	require.False(t, payload.IsError, "expand must answer: %v", payload.Content)
	selfID, ok := payload.Meta["tool_use_id"].(string)
	require.True(t, ok, "the answer must name the self-record it filed; meta=%v", payload.Meta)

	r.read(t, sess, "toolu_live_after", "notes.txt", "a later read in the same turn\n")
	recs := r.toolUses(t)
	var self *liveToolUse
	for i := range recs {
		if recs[i].ID == selfID {
			self = &recs[i]
		}
	}
	require.NotNil(t, self, "the self-record %s must be indexed", selfID)
	require.Equal(t, string(sess), self.Session, "the self-record must belong to the live session")
	require.GreaterOrEqual(t, self.Turn, floor,
		"the self-record must be filed at the session's current turn, not before turn %d", floor)

	code, doc, _ := fsckJSON(t, r.root)
	row := fsckRequireRow(t, doc, "index.tool_use")
	require.Equal(t, true, row["ok"], "index.tool_use must hold after an MCP call: %s", fsckDetail(row))
	require.NotContains(t, fsckDetail(row), "turns are monotone")
	_ = code // other rows (for example a live daemon's delivery state) are not this test's subject
}

// TestLiveLedgerToolsAnswerBeforeFirstCompaction is retrieval D1: with no compaction yet, both
// ledger tools work — record_eliminated records and already_tried answers from the record.
func TestLiveLedgerToolsAnswerBeforeFirstCompaction(t *testing.T) {
	const sess = core.SessionID("sess-live-ledger-early")
	r := newLiveRig(t)
	r.primeSession(t, sess)

	var before liveTried
	r.call(t, mcp.ToolAlreadyTried, map[string]any{"target": liveTarget, "approach": "retry the dial"}, &before)
	require.Equal(t, "absent", before.State, "an unrecorded approach is absent, not %+v", before)

	ack := r.recordElimination(t)
	require.Equal(t, "session", ack.Scope)

	got := r.alreadyTried(t)
	require.Equal(t, "active", got.State, "the elimination just recorded must answer active: %+v", got)
	require.Equal(t, liveReason, got.Reason)
}

// TestLiveSessionScopedEliminationStaysInItsSession is retrieval D2: the record carries the session
// that made it, and a later session of the project does not see it.
func TestLiveSessionScopedEliminationStaysInItsSession(t *testing.T) {
	const (
		first  = core.SessionID("sess-live-scope-a")
		second = core.SessionID("sess-live-scope-b")
	)
	r := newLiveRig(t)
	r.primeSession(t, first)
	ack := r.recordElimination(t)

	lines := r.eliminationLines(t)
	require.Len(t, lines, 1)
	require.Equal(t, ack.ID, lines[0]["id"])
	require.Equal(t, string(first), lines[0]["session"], "a session-scoped record must name its session")
	require.Equal(t, "active", r.alreadyTried(t).State, "the recording session sees its own record")

	r.end(t, first)
	r.start(t, second)
	r.prompt(t, second, "a new session in the same project")
	r.waitFor(t, "the second session's prompt", func(recs []liveToolUse) bool { return livePrompts(recs, second) == 1 })

	got := r.alreadyTried(t)
	require.Equal(t, "absent", got.State,
		"another session's session-scoped elimination must not answer in this session: %+v", got)
}

// TestLiveAlreadyTriedSeesInSessionDependencyChange is retrieval D3: once a new version of a
// dependency is captured, the next already_tried in the same session answers stale.
func TestLiveAlreadyTriedSeesInSessionDependencyChange(t *testing.T) {
	const sess = core.SessionID("sess-live-stale")
	r := newLiveRig(t)
	r.primeSession(t, sess)
	r.recordElimination(t)
	require.Equal(t, "active", r.alreadyTried(t).State)

	r.read(t, sess, "toolu_live_pool_yaml_2", liveDepPath, "max_idle: 64\n")

	got := r.alreadyTried(t)
	require.Equal(t, "stale", got.State, "a captured dependency change must reach already_tried in-session: %+v", got)
	require.Len(t, got.StaleBecause, 1)
	require.Contains(t, got.StaleBecause[0], liveDepPath)
}

// TestLivePreCompactCarriesEliminationAndDecision is retrieval D5: a checkpoint sealed after an
// active elimination carries it, derives the rejected-alternative decision from it, and `why`
// answers that decision with its evidence rather than withholding it.
func TestLivePreCompactCarriesEliminationAndDecision(t *testing.T) {
	const sess = core.SessionID("sess-live-why")
	r := newLiveRig(t)
	r.primeSession(t, sess)
	r.compact(t, sess) // a first seal, so the elimination below lands in a LIVE draft's successor
	ack := r.recordElimination(t)
	r.compact(t, sess)

	reader, err := checkpoint.OpenReader(r.root, logging.Nop(), obs.New(testClock()))
	require.NoError(t, err)
	cp, _, err := reader.Latest(context.Background(), sess)
	require.NoError(t, err)

	var carried bool
	for _, rec := range cp.Eliminated {
		carried = carried || rec.ID == ack.ID
	}
	require.True(t, carried, "the checkpoint must carry the active elimination %s: %+v", ack.ID, cp.Eliminated)

	var decision checkpoint.Decision
	for _, d := range cp.Decisions {
		if len(d.AlternativesRejected) == 1 && d.AlternativesRejected[0] == liveApproach {
			decision = d
		}
	}
	require.NotEmpty(t, decision.ID, "the checkpoint must carry the rejected-alternative decision: %+v", cp.Decisions)
	require.Equal(t, ack.Evidence, decision.Evidence.String())

	var why struct {
		Found            bool   `json:"found"`
		DecisionID       string `json:"decision_id"`
		Evidence         string `json:"evidence"`
		EvidenceWithheld string `json:"evidence_withheld"`
	}
	payload := r.call(t, mcp.ToolWhy, map[string]any{"decision_id": string(decision.ID)}, &why)
	require.False(t, payload.IsError, "why must answer: %v", payload.Content)
	require.True(t, why.Found, "why must find the decision the checkpoint carries")
	require.Equal(t, ack.Evidence, why.Evidence)
	require.Empty(t, why.EvidenceWithheld,
		"the evidence is the reason text record_eliminated stored, which has no file to authorize")
}

// liveTimeline is timeline's body.
type liveTimeline struct {
	To       int `json:"to"`
	Segments []struct {
		StartTurn int  `json:"start_turn"`
		EndTurn   int  `json:"end_turn"`
		Tokens    int  `json:"tokens"`
		Closed    bool `json:"closed"`
	} `json:"segments"`
}

// TestLiveTimelineShowsOpenSegmentProgress is retrieval D8's first half: the live session's open
// segment reports where the session actually is, not turns 0-0.
func TestLiveTimelineShowsOpenSegmentProgress(t *testing.T) {
	const sess = core.SessionID("sess-live-timeline")
	r := newLiveRig(t)
	r.primeSession(t, sess)
	r.endTurn(t, sess)
	r.prompt(t, sess, "second prompt")
	r.waitFor(t, "the second prompt", func(recs []liveToolUse) bool { return livePrompts(recs, sess) == 2 })
	r.read(t, sess, "toolu_live_timeline_3", "src/other.go", "package pool\n\nfunc Other() {}\n")
	recs := r.toolUses(t)
	last := 0
	for _, rec := range recs {
		if rec.Session == string(sess) && rec.Turn > last {
			last = rec.Turn
		}
	}
	require.Positive(t, last, "fixture sanity: the session must have moved past turn 0")

	var tl liveTimeline
	payload := r.call(t, mcp.ToolTimeline, map[string]any{}, &tl)
	require.False(t, payload.IsError, "timeline must answer: %v", payload.Content)
	require.NotEmpty(t, tl.Segments)
	open := tl.Segments[len(tl.Segments)-1]
	require.False(t, open.Closed, "the live session's latest segment is open")
	require.GreaterOrEqual(t, open.EndTurn, last,
		"an open segment must report the session's current turn, not %d-%d", open.StartTurn, open.EndTurn)
	require.GreaterOrEqual(t, tl.To, last, "the default upper bound is the current frontier")
	if len(tl.Segments) == 1 {
		// The observer's own segment, never rolled by the scheduler: its running count is known.
		require.Positive(t, open.Tokens, "the open segment has captured content, so it is not 0 tokens")
	}
}

// TestLiveTimelineRefusesAnInvertedRange is retrieval D8's second half: from after to is a tool
// error, never a silently empty or silently reordered answer.
func TestLiveTimelineRefusesAnInvertedRange(t *testing.T) {
	const sess = core.SessionID("sess-live-timeline-range")
	r := newLiveRig(t)
	r.primeSession(t, sess)

	payload := r.call(t, mcp.ToolTimeline, map[string]any{"from": "5", "to": "2"}, nil)
	require.True(t, payload.IsError, "from > to must be refused as a tool error: %v", payload.Content)
	require.Contains(t, payload.Content[0].Text, "from")
}
