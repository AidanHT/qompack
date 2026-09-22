package eval

// ── The live host stream: what a headless Claude Code session prints ─────────────────────────────
//
// V6 close-out C5.4. A live trial drives `claude -p --input-format stream-json --output-format
// stream-json --verbose --include-hook-events` and keeps every line the host printed. This file
// turns those lines into typed turns without inventing anything the host did not say.
//
// The shape below was read off Claude Code 2.1.280's real output (the three smoke sessions preserved
// under plans/sdd/V6-closeout/eval/runs/), not from memory, and four of its facts decide how the
// numbers may be used:
//
//   - One `result` line closes each user turn. In streaming-input mode every user message gets its
//     own result, and a local command such as /compact gets one too (with `local_command` set,
//     `num_turns` 0 and an all-zero `usage`).
//   - A result's `usage` is THAT turn's main agent loop only. Its `modelUsage` and `total_cost_usd`
//     are the process's RUNNING totals, and on a resumed session they also carry the spend the host
//     restored from the transcript. Summing either across results double-counts; accounting
//     therefore differences consecutive running totals (liveaccount.go) and never adds them.
//   - Several `assistant` lines can share one message id: the host emits one line per content
//     block, each repeating the same usage. They are one API request and are counted once.
//   - An assistant line's `output_tokens` is the value the API reported at message_start, a
//     placeholder. Real output volume exists only on the result line.
//
// JSON keys of the types here are snake_case: a parsed stream is written to disk as part of a trial
// record, and on-disk artifacts in this package follow ledger.go's convention, not the camelCase
// §5.18 in-memory shapes.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// maxHostLineBytes bounds one stream line. A tool result the host echoes back can be large, and a
// bufio.Scanner's default 64 KiB token limit would turn a legitimate line into a parse failure.
const maxHostLineBytes = 64 << 20

// HostStream is one headless session's stream, split into user turns.
type HostStream struct {
	// Init is the first `system/init` line, or nil when the host never printed one.
	Init *HostInit `json:"init"`
	// InitCount is how many `system/init` lines the host printed. It re-prints one per turn and
	// after a compaction, so a count above one is normal.
	InitCount int `json:"init_count"`
	// Turns is every user turn in order. A turn whose Result is nil is one the stream ended inside.
	Turns []HostTurn `json:"turns"`
	// Lines is how many non-empty lines were read.
	Lines int `json:"lines"`
	// TruncatedTail reports that the final line was not valid JSON and had no trailing newline:
	// the process was cut off mid-write. It is recorded rather than failing the parse, because the
	// turns before it are still evidence.
	TruncatedTail bool `json:"truncated_tail"`
	// Other counts every line kind this parser does not interpret, keyed "type" or "type/subtype",
	// so a new host event is visible in the record instead of silently dropped.
	Other map[string]int `json:"other,omitempty"`
}

// HostInit is the session metadata the host reports at start-up.
type HostInit struct {
	SessionID         string            `json:"session_id"`
	Model             string            `json:"model"`
	CWD               string            `json:"cwd"`
	ClaudeCodeVersion string            `json:"claude_code_version"`
	PermissionMode    string            `json:"permission_mode"`
	APIKeySource      string            `json:"api_key_source"`
	Plugins           []HostPlugin      `json:"plugins"`
	PluginErrors      []HostPluginError `json:"plugin_errors,omitempty"`
	MCPServers        []HostMCPServer   `json:"mcp_servers"`
	Tools             []string          `json:"tools"`
}

// HostPlugin is one plugin the host reports as loaded.
type HostPlugin struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
}

// HostPluginError is one plugin the host failed to load.
type HostPluginError struct {
	Plugin  string `json:"plugin"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// HostMCPServer is one MCP server the host reports with its connection status.
type HostMCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
}

// HostTurn is everything the host printed between one user turn's start and its result.
type HostTurn struct {
	// Index is the turn's 0-based position.
	Index int `json:"index"`
	// Requests is every API request the host reported in this turn, deduplicated by message id,
	// in first-seen order. Subagent requests carry a ParentToolUseID.
	Requests []HostRequest `json:"requests"`
	// Hooks is every hook the host started in this turn, paired with its response when one came.
	Hooks []HostHook `json:"hooks,omitempty"`
	// Compactions is every compact_boundary the host reported in this turn.
	Compactions []HostCompaction `json:"compactions,omitempty"`
	// CompactAttempts is every compaction outcome the host reported on a status line, failed ones
	// included. A failed attempt ("too_few_groups", say) has no compact_boundary, so without this a
	// compaction that was asked for and refused would be indistinguishable from one never asked for.
	CompactAttempts []HostCompactAttempt `json:"compact_attempts,omitempty"`
	// Retries is every api_retry event in this turn.
	Retries []HostRetry `json:"retries,omitempty"`
	// LocalOutput is the text of every `<local-command-stdout>` message the host replayed in this
	// turn. A local command's diagnostics — a hook the host rejected during /compact, say — reach
	// the stream only this way.
	LocalOutput []string `json:"local_output,omitempty"`
	// CompactSummaryChars is the length of the host's post-compaction summary message, when one
	// was replayed in this turn.
	CompactSummaryChars int `json:"compact_summary_chars,omitempty"`
	// Result is the line that closed the turn, or nil when the stream ended first.
	Result *HostResult `json:"result"`
	// FirstRecvMS and LastRecvMS are the receive times of the turn's first and last lines, in
	// milliseconds since the process started, when the driver supplied receive times; else 0.
	FirstRecvMS int64 `json:"first_recv_ms"`
	LastRecvMS  int64 `json:"last_recv_ms"`
}

// HostRequest is one API request, as the host reported it on its assistant lines.
type HostRequest struct {
	MessageID string `json:"message_id"`
	RequestID string `json:"request_id,omitempty"`
	Model     string `json:"model"`
	// ParentToolUseID is empty for the main loop and names the spawning tool call for a subagent.
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`
	// Usage is the request's usage as first reported. Input and cache volumes are exact; its
	// OutputTokens is the host's message_start placeholder and must not be summed as output.
	Usage HostUsage `json:"usage"`
	// ToolUses is every tool call the request made.
	ToolUses []HostToolUse `json:"tool_uses,omitempty"`
	// Text is the request's visible text blocks, concatenated.
	Text string `json:"text,omitempty"`
}

// HostToolUse is one tool call.
type HostToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

// HostUsage is one usage object as the host printed it.
type HostUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	// CacheCreation5m and CacheCreation1h are the TTL split of CacheCreationInputTokens. Both are
	// nil when the host printed no `cache_creation` object: an absent split is unknown, not zero.
	CacheCreation5m *int64 `json:"cache_creation_5m,omitempty"`
	CacheCreation1h *int64 `json:"cache_creation_1h,omitempty"`
	// ThinkingTokens is output_tokens_details.thinking_tokens: the part of OutputTokens that was
	// extended thinking. Nil when the host did not report it.
	ThinkingTokens *int64 `json:"thinking_tokens,omitempty"`
}

// HostModelUsage is one model's entry in a result's `modelUsage`: running totals, subagents and
// host-internal requests (compaction summaries, helpers) included.
type HostModelUsage struct {
	InputTokens              int64   `json:"input_tokens"`
	OutputTokens             int64   `json:"output_tokens"`
	CacheReadInputTokens     int64   `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64   `json:"cache_creation_input_tokens"`
	WebSearchRequests        int64   `json:"web_search_requests"`
	ThinkingTokens           *int64  `json:"thinking_tokens,omitempty"`
	CostUSD                  float64 `json:"cost_usd"`
	CostBasis                string  `json:"cost_basis,omitempty"`
	ContextWindow            int64   `json:"context_window,omitempty"`
}

// HostHook is one hook execution the host reported with --include-hook-events.
type HostHook struct {
	HookID string `json:"hook_id"`
	// Name is the host's label, e.g. "SessionStart:compact" or "PostToolUse:Read".
	Name string `json:"name"`
	// Event is the hook event, e.g. "SessionStart".
	Event string `json:"event"`
	// Responded reports whether a hook_response line arrived for this hook.
	Responded bool   `json:"responded"`
	Outcome   string `json:"outcome,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	// Output is the hook's stdout as the host echoed it.
	Output string `json:"output,omitempty"`
	// StartedRecvMS and RespondedRecvMS are receive times, when the driver supplied them. Their
	// difference approximates the latency the host saw; it is an observation at the pipe, not a
	// timer inside the host.
	StartedRecvMS   int64 `json:"started_recv_ms,omitempty"`
	RespondedRecvMS int64 `json:"responded_recv_ms,omitempty"`
}

// HostCompaction is one compact_boundary.
type HostCompaction struct {
	Trigger    string `json:"trigger"`
	PreTokens  int64  `json:"pre_tokens"`
	PostTokens int64  `json:"post_tokens"`
	DurationMS int64  `json:"duration_ms"`
}

// HostCompactAttempt is one system/status line carrying a compact_result.
type HostCompactAttempt struct {
	// Result is the host's verdict, e.g. "success" or "failed".
	Result string `json:"result"`
	// Error is the host's reason for a failure, e.g. "too_few_groups".
	Error string `json:"error,omitempty"`
}

// HostRetry is one api_retry event.
type HostRetry struct {
	Attempt     int    `json:"attempt"`
	ErrorStatus *int   `json:"error_status,omitempty"`
	Error       string `json:"error,omitempty"`
}

// HostResult is the line that closes a turn.
type HostResult struct {
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	// LocalCommand names the built-in command this turn ran instead of a model turn, e.g.
	// "compact".
	LocalCommand   string                    `json:"local_command,omitempty"`
	DurationMS     int64                     `json:"duration_ms"`
	DurationAPIMS  int64                     `json:"duration_api_ms"`
	NumTurns       int                       `json:"num_turns"`
	TotalCostUSD   float64                   `json:"total_cost_usd"`
	Usage          HostUsage                 `json:"usage"`
	ModelUsage     map[string]HostModelUsage `json:"model_usage"`
	Result         string                    `json:"result"`
	TerminalReason string                    `json:"terminal_reason,omitempty"`
	StopReason     string                    `json:"stop_reason,omitempty"`
	// PermissionDenials is how many tool calls the host denied during the turn.
	PermissionDenials int      `json:"permission_denials"`
	Errors            []string `json:"errors,omitempty"`
	// SubagentsSpawned is subagent_stats.spawned, when the host reported it.
	SubagentsSpawned int `json:"subagents_spawned"`
}

// Answer is the text a turn ended with: the result text when the host printed one, else the last
// main-loop request's text.
func (t HostTurn) Answer() string {
	if t.Result != nil && t.Result.Result != "" {
		return t.Result.Result
	}
	for i := len(t.Requests) - 1; i >= 0; i-- {
		if t.Requests[i].ParentToolUseID == "" && t.Requests[i].Text != "" {
			return t.Requests[i].Text
		}
	}
	return ""
}

// ── raw wire shapes ─────────────────────────────────────────────────────────────────────────────

type rawLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

type rawUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreation            *struct {
		Ephemeral5m *int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h *int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	OutputTokensDetails *struct {
		ThinkingTokens *int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

func (u rawUsage) typed() HostUsage {
	out := HostUsage{
		InputTokens:              u.InputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
		OutputTokens:             u.OutputTokens,
	}
	if u.CacheCreation != nil && u.CacheCreation.Ephemeral5m != nil && u.CacheCreation.Ephemeral1h != nil {
		m5, h1 := *u.CacheCreation.Ephemeral5m, *u.CacheCreation.Ephemeral1h
		out.CacheCreation5m, out.CacheCreation1h = &m5, &h1
	}
	if u.OutputTokensDetails != nil && u.OutputTokensDetails.ThinkingTokens != nil {
		th := *u.OutputTokensDetails.ThinkingTokens
		out.ThinkingTokens = &th
	}
	return out
}

type rawInit struct {
	SessionID         string            `json:"session_id"`
	Model             string            `json:"model"`
	CWD               string            `json:"cwd"`
	ClaudeCodeVersion string            `json:"claude_code_version"`
	PermissionMode    string            `json:"permissionMode"`
	APIKeySource      string            `json:"apiKeySource"`
	Plugins           []HostPlugin      `json:"plugins"`
	PluginErrors      []HostPluginError `json:"plugin_errors"`
	MCPServers        []HostMCPServer   `json:"mcp_servers"`
	Tools             []string          `json:"tools"`
}

type rawAssistant struct {
	Message struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage rawUsage `json:"usage"`
	} `json:"message"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	RequestID       string  `json:"request_id"`
}

type rawUser struct {
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// localStdoutOpen and compactSummaryPrefix recognise the two host-authored user messages that
// carry evidence: a local command's output, and the summary a compaction replaces history with.
const (
	localStdoutOpen      = "<local-command-stdout>"
	localStdoutClose     = "</local-command-stdout>"
	compactSummaryPrefix = "This session is being continued from a previous conversation"
)

type rawHook struct {
	HookID    string `json:"hook_id"`
	HookName  string `json:"hook_name"`
	HookEvent string `json:"hook_event"`
	Output    string `json:"output"`
	ExitCode  *int   `json:"exit_code"`
	Outcome   string `json:"outcome"`
}

type rawCompact struct {
	CompactMetadata struct {
		Trigger    string `json:"trigger"`
		PreTokens  int64  `json:"pre_tokens"`
		PostTokens int64  `json:"post_tokens"`
		DurationMS int64  `json:"duration_ms"`
	} `json:"compact_metadata"`
}

type rawStatus struct {
	CompactResult string `json:"compact_result"`
	CompactError  string `json:"compact_error"`
}

type rawRetry struct {
	Attempt     int    `json:"attempt"`
	ErrorStatus *int   `json:"error_status"`
	Error       string `json:"error"`
}

type rawModelUsage struct {
	InputTokens              int64   `json:"inputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	WebSearchRequests        int64   `json:"webSearchRequests"`
	ThinkingTokens           *int64  `json:"thinkingTokens"`
	CostUSD                  float64 `json:"costUSD"`
	CostBasis                string  `json:"costBasis"`
	ContextWindow            int64   `json:"contextWindow"`
}

type rawResult struct {
	Subtype           string                   `json:"subtype"`
	IsError           bool                     `json:"is_error"`
	LocalCommand      string                   `json:"local_command"`
	DurationMS        int64                    `json:"duration_ms"`
	DurationAPIMS     int64                    `json:"duration_api_ms"`
	NumTurns          int                      `json:"num_turns"`
	TotalCostUSD      float64                  `json:"total_cost_usd"`
	Usage             rawUsage                 `json:"usage"`
	ModelUsage        map[string]rawModelUsage `json:"modelUsage"`
	Result            string                   `json:"result"`
	TerminalReason    string                   `json:"terminal_reason"`
	StopReason        *string                  `json:"stop_reason"`
	PermissionDenials []json.RawMessage        `json:"permission_denials"`
	Errors            []string                 `json:"errors"`
	SubagentStats     *struct {
		Spawned int `json:"spawned"`
	} `json:"subagent_stats"`
}

// ── the parser ──────────────────────────────────────────────────────────────────────────────────

// streamParser accumulates one stream. It is a type rather than a closure so each line kind has
// its own small method.
type streamParser struct {
	out     HostStream
	cur     *HostTurn
	byMsgID map[string]int
	hookIdx map[string]int
}

// ParseHostStream parses one headless session's stream-json output.
//
// recvMS, when non-nil, is the receive time of each non-empty line in milliseconds since the
// process started, aligned with the lines as read; it is what hook and turn timings are computed
// from. A malformed line is an error naming the line, except a final line with no trailing newline,
// which is recorded as TruncatedTail: a cut-off process loses its last write, and the turns before
// it are still evidence.
func ParseHostStream(r io.Reader, recvMS []int64) (HostStream, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return HostStream{}, fmt.Errorf("eval: reading the host stream: %w", err)
	}
	endsWithNewline := len(raw) > 0 && raw[len(raw)-1] == '\n'

	p := &streamParser{byMsgID: map[string]int{}, hookIdx: map[string]int{}}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxHostLineBytes)

	lineNo := 0
	var pending []byte
	for sc.Scan() {
		lineNo++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if pending != nil {
			// The previous line failed to parse and was not the last one.
			return HostStream{}, fmt.Errorf("eval: host stream line %d is not JSON", lineNo-1)
		}
		var at int64
		if p.out.Lines < len(recvMS) {
			at = recvMS[p.out.Lines]
		}
		p.out.Lines++
		if perr := p.line(line, at); perr != nil {
			var syn *json.SyntaxError
			if errors.As(perr, &syn) || errors.Is(perr, io.ErrUnexpectedEOF) {
				pending = append([]byte(nil), line...)
				continue
			}
			return HostStream{}, fmt.Errorf("eval: host stream line %d: %w", lineNo, perr)
		}
	}
	if err := sc.Err(); err != nil {
		return HostStream{}, fmt.Errorf("eval: scanning the host stream: %w", err)
	}
	if pending != nil {
		if endsWithNewline {
			return HostStream{}, fmt.Errorf("eval: host stream line %d is not JSON", lineNo)
		}
		p.out.TruncatedTail = true
	}
	if p.cur != nil {
		p.out.Turns = append(p.out.Turns, *p.cur)
	}
	return p.out, nil
}

// turn returns the open turn, opening one if needed.
func (p *streamParser) turn(at int64) *HostTurn {
	if p.cur == nil {
		p.cur = &HostTurn{Index: len(p.out.Turns), FirstRecvMS: at}
		p.byMsgID = map[string]int{}
		p.hookIdx = map[string]int{}
	}
	p.cur.LastRecvMS = at
	return p.cur
}

// other counts a line kind this parser does not interpret.
func (p *streamParser) other(kind string) {
	if p.out.Other == nil {
		p.out.Other = map[string]int{}
	}
	p.out.Other[kind]++
}

// line dispatches one line by its type and subtype.
func (p *streamParser) line(line []byte, at int64) error {
	var head rawLine
	if err := json.Unmarshal(line, &head); err != nil {
		return err
	}
	switch head.Type {
	case "system":
		return p.system(head.Subtype, line, at)
	case "assistant":
		return p.assistant(line, at)
	case "result":
		return p.result(line, at)
	case "user":
		return p.user(line, at)
	case "rate_limit_event", "stream_event":
		p.turn(at)
		return nil
	default:
		p.other(head.Type)
		return nil
	}
}

// system handles the system subtypes that carry evidence.
func (p *streamParser) system(subtype string, line []byte, at int64) error {
	switch subtype {
	case "init":
		var ri rawInit
		if err := json.Unmarshal(line, &ri); err != nil {
			return err
		}
		p.out.InitCount++
		if p.out.Init == nil {
			hi := HostInit(ri)
			p.out.Init = &hi
		}
		p.turn(at)
	case "hook_started", "hook_response":
		return p.hook(subtype, line, at)
	case "compact_boundary":
		var rc rawCompact
		if err := json.Unmarshal(line, &rc); err != nil {
			return err
		}
		t := p.turn(at)
		t.Compactions = append(t.Compactions, HostCompaction(rc.CompactMetadata))
	case "api_retry":
		var rr rawRetry
		if err := json.Unmarshal(line, &rr); err != nil {
			return err
		}
		t := p.turn(at)
		t.Retries = append(t.Retries, HostRetry(rr))
	case "status":
		var rs rawStatus
		if err := json.Unmarshal(line, &rs); err != nil {
			return err
		}
		t := p.turn(at)
		if rs.CompactResult != "" {
			t.CompactAttempts = append(t.CompactAttempts,
				HostCompactAttempt{Result: rs.CompactResult, Error: rs.CompactError})
		}
	case "thinking_tokens":
		p.turn(at)
	default:
		p.turn(at)
		p.other("system/" + subtype)
	}
	return nil
}

// user records the host-authored user messages that carry evidence. Tool results and the driver's
// own prompts are not copied: they are already in the stream file.
func (p *streamParser) user(line []byte, at int64) error {
	var ru rawUser
	if err := json.Unmarshal(line, &ru); err != nil {
		return err
	}
	t := p.turn(at)
	var text string
	if len(ru.Message.Content) == 0 || json.Unmarshal(ru.Message.Content, &text) != nil {
		return nil // structured content (tool results): nothing to record here
	}
	switch {
	case strings.HasPrefix(text, localStdoutOpen):
		body := strings.TrimSuffix(strings.TrimPrefix(text, localStdoutOpen), localStdoutClose)
		t.LocalOutput = append(t.LocalOutput, strings.TrimSpace(body))
	case strings.HasPrefix(text, compactSummaryPrefix):
		t.CompactSummaryChars = len(text)
	}
	return nil
}

// hook pairs hook_started and hook_response lines by hook id.
func (p *streamParser) hook(subtype string, line []byte, at int64) error {
	var rh rawHook
	if err := json.Unmarshal(line, &rh); err != nil {
		return err
	}
	t := p.turn(at)
	i, ok := p.hookIdx[rh.HookID]
	if !ok {
		t.Hooks = append(t.Hooks, HostHook{HookID: rh.HookID, Name: rh.HookName, Event: rh.HookEvent})
		i = len(t.Hooks) - 1
		p.hookIdx[rh.HookID] = i
	}
	h := &t.Hooks[i]
	if subtype == "hook_started" {
		h.StartedRecvMS = at
		return nil
	}
	h.Responded = true
	h.RespondedRecvMS = at
	h.Outcome = rh.Outcome
	h.ExitCode = rh.ExitCode
	h.Output = rh.Output
	return nil
}

// assistant folds one assistant line into its request, deduplicating by message id.
func (p *streamParser) assistant(line []byte, at int64) error {
	var ra rawAssistant
	if err := json.Unmarshal(line, &ra); err != nil {
		return err
	}
	t := p.turn(at)
	id := ra.Message.ID
	i, seen := p.byMsgID[id]
	if !seen || id == "" {
		req := HostRequest{
			MessageID: id,
			RequestID: ra.RequestID,
			Model:     ra.Message.Model,
			Usage:     ra.Message.Usage.typed(),
		}
		if ra.ParentToolUseID != nil {
			req.ParentToolUseID = *ra.ParentToolUseID
		}
		t.Requests = append(t.Requests, req)
		i = len(t.Requests) - 1
		if id != "" {
			p.byMsgID[id] = i
		}
	}
	req := &t.Requests[i]
	if req.RequestID == "" {
		req.RequestID = ra.RequestID
	}
	for _, c := range ra.Message.Content {
		switch c.Type {
		case "text":
			if req.Text != "" {
				req.Text += "\n"
			}
			req.Text += c.Text
		case "tool_use":
			req.ToolUses = append(req.ToolUses, HostToolUse{ID: c.ID, Name: c.Name, Input: c.Input})
		}
	}
	return nil
}

// result closes the open turn.
func (p *streamParser) result(line []byte, at int64) error {
	var rr rawResult
	if err := json.Unmarshal(line, &rr); err != nil {
		return err
	}
	t := p.turn(at)
	res := &HostResult{
		Subtype:           rr.Subtype,
		IsError:           rr.IsError,
		LocalCommand:      rr.LocalCommand,
		DurationMS:        rr.DurationMS,
		DurationAPIMS:     rr.DurationAPIMS,
		NumTurns:          rr.NumTurns,
		TotalCostUSD:      rr.TotalCostUSD,
		Usage:             rr.Usage.typed(),
		Result:            rr.Result,
		TerminalReason:    rr.TerminalReason,
		PermissionDenials: len(rr.PermissionDenials),
		Errors:            rr.Errors,
		ModelUsage:        make(map[string]HostModelUsage, len(rr.ModelUsage)),
	}
	if rr.StopReason != nil {
		res.StopReason = *rr.StopReason
	}
	if rr.SubagentStats != nil {
		res.SubagentsSpawned = rr.SubagentStats.Spawned
	}
	for model, mu := range rr.ModelUsage {
		res.ModelUsage[model] = HostModelUsage(mu)
	}
	t.Result = res
	p.out.Turns = append(p.out.Turns, *t)
	p.cur = nil
	return nil
}

// HookLatencies groups every responded hook's pipe-observed latency by hook name, in milliseconds,
// sorted ascending within each name. Hooks with no receive times are skipped.
func (s HostStream) HookLatencies() map[string][]int64 {
	out := map[string][]int64{}
	for _, t := range s.Turns {
		for _, h := range t.Hooks {
			if !h.Responded || h.StartedRecvMS == 0 || h.RespondedRecvMS < h.StartedRecvMS {
				continue
			}
			out[h.Name] = append(out[h.Name], h.RespondedRecvMS-h.StartedRecvMS)
		}
	}
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool { return out[k][i] < out[k][j] })
	}
	return out
}

// PluginLoaded reports whether the host listed a plugin of this name as loaded.
func (s HostStream) PluginLoaded(name string) bool {
	if s.Init == nil {
		return false
	}
	for _, pl := range s.Init.Plugins {
		if strings.EqualFold(pl.Name, name) {
			return true
		}
	}
	return false
}

// ── hook failures the host reported ─────────────────────────────────────────────────────────────

// hookValidationMarker is the text Claude Code prints when it rejects a hook's JSON output against
// its own schema. A rejected output is not an error the hook sees: the hook exited 0, the host
// dropped what it said, and the only trace is this sentence in a local command's output or the
// host's stderr. It is matched as data, verbatim, because it is the host's wording and not ours.
const hookValidationMarker = "Hook JSON output validation failed"

// hookFailureLine matches the host's "<Event> [<command>] failed: <reason>" report, which it prints
// in a local command's output ("Compacted PreCompact [...] failed: ...", a PreCompact output
// rejected during /compact) and on stderr ("SessionEnd hook [...] failed: Hook cancelled"). The
// event is the word before the bracket, or before the word "hook" when the host inserts it, so a
// prefix such as "Compacted " is never mistaken for the event.
var hookFailureLine = regexp.MustCompile(`([A-Za-z]+)(?: hook)? \[([^\]\r\n]*)\] failed: ([^\r\n]*)`)

// hookProblemDetailRunes bounds the detail a problem carries; the full text stays in the stream
// file the trial keeps.
const hookProblemDetailRunes = 400

// The kinds of hook problem.
const (
	// HookProblemOutputRejected is a hook whose output the host refused as invalid.
	HookProblemOutputRejected = "output_rejected"
	// HookProblemFailed is any other "<Event> [...] failed:" report.
	HookProblemFailed = "hook_failed"
	// HookProblemOutcome is a hook_response whose outcome was not "success".
	HookProblemOutcome = "hook_outcome"
)

// HostHookProblem is one hook the host reported as not having worked.
type HostHookProblem struct {
	// Turn is the turn the report arrived in, or -1 when it came from outside the stream (stderr).
	Turn int `json:"turn"`
	// Event is the hook event, e.g. "PreCompact".
	Event string `json:"event"`
	// Command is the hook command the host named, when it named one.
	Command string `json:"command,omitempty"`
	// Kind is HookProblemOutputRejected, HookProblemFailed or HookProblemOutcome.
	Kind string `json:"kind"`
	// Detail is the host's reason, bounded.
	Detail string `json:"detail"`
}

// ParseHookFailures extracts every "<Event> [<command>] failed: <reason>" report from text. turn
// is recorded on each problem as given.
func ParseHookFailures(text string, turn int) []HostHookProblem {
	var out []HostHookProblem
	for _, m := range hookFailureLine.FindAllStringSubmatch(text, -1) {
		kind := HookProblemFailed
		if strings.Contains(m[3], hookValidationMarker) {
			kind = HookProblemOutputRejected
		}
		out = append(out, HostHookProblem{
			Turn: turn, Event: m[1], Command: m[2], Kind: kind, Detail: boundRunes(m[3], hookProblemDetailRunes),
		})
	}
	return out
}

// HookProblems lists every hook failure the stream shows: reports replayed in a local command's
// output, and hook responses whose outcome was not a success. An empty result is evidence only
// that the stream carried no such report — the host prints nothing for some failures.
func (s HostStream) HookProblems() []HostHookProblem {
	var out []HostHookProblem
	for _, t := range s.Turns {
		for _, lo := range t.LocalOutput {
			out = append(out, ParseHookFailures(lo, t.Index)...)
		}
		for _, h := range t.Hooks {
			if !h.Responded || h.Outcome == "success" {
				continue
			}
			code := "none"
			if h.ExitCode != nil {
				code = strconv.Itoa(*h.ExitCode)
			}
			out = append(out, HostHookProblem{
				Turn: t.Index, Event: h.Event, Kind: HookProblemOutcome,
				Detail: boundRunes(fmt.Sprintf("%s outcome %q exit %s", h.Name, h.Outcome, code), hookProblemDetailRunes),
			})
		}
	}
	return out
}

// boundRunes bounds s to n runes, marking a cut with an ellipsis.
func boundRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
