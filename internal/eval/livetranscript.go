package eval

// ── The host transcript: what Claude Code wrote to its own session log ──────────────────────────
//
// V6 close-out C5.4. The stream (livestream.go) is what the host told the driver; the transcript
// JSONL the host keeps under its projects directory is what it recorded for itself, and three facts
// a trial needs exist only there:
//
//   - the duration the host measured for every hook it ran (`attachment` of type hook_success and
//     its failure siblings, field durationMs) — the hook latency the HOST saw, which a pipe
//     timestamp can only approximate;
//   - the additional context a hook injected (`hook_additional_context`), which is where a
//     Qompack rehydration block becomes visible as something the model was actually given;
//   - the host's post-compaction bookkeeping (compact_boundary with its trigger, stop-hook
//     summaries with their hook errors, and the local-command output a /compact left behind).
//
// The transcript also carries things a trial record must never copy: the operator's identity
// (`session_context`), the whole system prompt (`prompt_snapshot`) and every message body. The
// parser below therefore reads only the record kinds it names and keeps no message text at all,
// except the context a hook injected — which is the plugin's own output and the point of the trial.
//
// The field names were read off Claude Code 2.1.280's real transcripts of the three smoke sessions
// (plans/sdd/V6-closeout/eval/runs/), and the redacted fixture under testdata/live/ is cut from one.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// qompackInjectedMarkers open every rehydration block Qompack injects: the current spelling and the
// one 0.3.x and earlier wrote (checkpoint.InjectionOpenTag, LegacyInjectionOpenTag). They are the
// plugin's own markers (internal/rehydrate), matched here as data so this package does not import
// the producer.
var qompackInjectedMarkers = []string{"<!-- qompack:session-record", "<!-- qompack:injected"}

// hasQompackMarker reports whether text carries either spelling of the rehydration marker.
func hasQompackMarker(text string) bool {
	for _, m := range qompackInjectedMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// maxInjectionKeepBytes bounds how much of one injected context a record keeps verbatim. The size
// is always recorded in full; the text is kept so a reader can see what the model was given.
const maxInjectionKeepBytes = 64 << 10

// HostTranscript is the parsed subset of one host transcript.
type HostTranscript struct {
	// HookRuns is every hook execution the host recorded, in order.
	HookRuns []TranscriptHookRun `json:"hook_runs"`
	// Contexts is every hook_additional_context the host recorded, in order.
	Contexts []TranscriptContext `json:"contexts"`
	// Compactions is every compact_boundary the host recorded. PostTokens is absent from the
	// transcript's form of the record, so it is zero here and the stream's value is authoritative.
	Compactions []HostCompaction `json:"compactions"`
	// LocalOutput is every <local-command-stdout> body the host recorded.
	LocalOutput []string `json:"local_output,omitempty"`
	// HookErrors is every hook error a stop-hook summary listed, verbatim and bounded.
	HookErrors []string `json:"hook_errors,omitempty"`
	// Lines is how many non-empty lines were read; TruncatedTail as in HostStream.
	Lines         int  `json:"lines"`
	TruncatedTail bool `json:"truncated_tail"`
}

// TranscriptHookRun is one hook execution with the duration the host measured.
type TranscriptHookRun struct {
	// Type is the host's attachment type: hook_success, or a failure kind.
	Type      string `json:"type"`
	HookName  string `json:"hook_name"`
	HookEvent string `json:"hook_event"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	// DurationMS is the host-measured duration; HasDuration says whether the host reported one.
	DurationMS  int64  `json:"duration_ms"`
	HasDuration bool   `json:"has_duration"`
	Command     string `json:"command,omitempty"`
}

// TranscriptContext is one hook's injected additional context.
type TranscriptContext struct {
	HookEvent string `json:"hook_event"`
	HookName  string `json:"hook_name"`
	// Bytes is the full size of the injected content.
	Bytes int `json:"bytes"`
	// Qompack reports that the content carries a Qompack rehydration block.
	Qompack bool `json:"qompack"`
	// Text is the content when it is a Qompack block, bounded to maxInjectionKeepBytes.
	Text string `json:"text,omitempty"`
}

type rawTranscriptLine struct {
	Type       string          `json:"type"`
	Subtype    string          `json:"subtype"`
	Attachment json.RawMessage `json:"attachment"`
	Message    *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	CompactMetadata *struct {
		Trigger    string `json:"trigger"`
		PreTokens  int64  `json:"preTokens"`
		DurationMS int64  `json:"durationMs"`
	} `json:"compactMetadata"`
	HookErrors []json.RawMessage `json:"hookErrors"`
}

type rawTranscriptAttachment struct {
	Type       string          `json:"type"`
	HookName   string          `json:"hookName"`
	HookEvent  string          `json:"hookEvent"`
	ExitCode   *int            `json:"exitCode"`
	DurationMS *int64          `json:"durationMs"`
	Command    string          `json:"command"`
	Content    json.RawMessage `json:"content"`
}

// hookContextType is the attachment type that records injected context. Every other attachment
// whose type starts with hookAttachmentPrefix and names a hook event records a hook EXECUTION:
// hook_success is the only one observed on 2.1.280, and a failure kind the host adds later is
// counted as a run with its own type rather than dropped because this parser did not predict it.
const (
	hookAttachmentPrefix = "hook_"
	hookContextType      = "hook_additional_context"
	hookSuccessType      = "hook_success"
)

// ParseHostTranscript parses one host transcript. A malformed line is an error naming it, except a
// final line with no trailing newline, which is recorded as TruncatedTail: the host may still have
// been writing when the trial ended.
func ParseHostTranscript(r io.Reader) (HostTranscript, error) {
	var out HostTranscript
	br := bufio.NewReader(r)
	lineNo := 0
	for {
		raw, err := br.ReadBytes('\n')
		if len(raw) > 0 {
			lineNo++
			line := strings.TrimSpace(string(raw))
			if line != "" {
				out.Lines++
				if perr := out.line([]byte(line)); perr != nil {
					if errors.Is(err, io.EOF) && raw[len(raw)-1] != '\n' {
						out.TruncatedTail = true
						break
					}
					return HostTranscript{}, fmt.Errorf("eval: host transcript line %d: %w", lineNo, perr)
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return HostTranscript{}, fmt.Errorf("eval: reading the host transcript: %w", err)
		}
	}
	return out, nil
}

func (t *HostTranscript) line(line []byte) error {
	var rl rawTranscriptLine
	if err := json.Unmarshal(line, &rl); err != nil {
		return err
	}
	switch rl.Type {
	case "attachment":
		return t.attachment(rl.Attachment)
	case "system":
		switch rl.Subtype {
		case "compact_boundary":
			if rl.CompactMetadata != nil {
				t.Compactions = append(t.Compactions, HostCompaction{
					Trigger: rl.CompactMetadata.Trigger, PreTokens: rl.CompactMetadata.PreTokens,
					DurationMS: rl.CompactMetadata.DurationMS,
				})
			}
		case "stop_hook_summary":
			for _, e := range rl.HookErrors {
				t.HookErrors = append(t.HookErrors, boundRunes(hookErrorText(e), hookProblemDetailRunes))
			}
		}
	case "user":
		if rl.Message == nil {
			return nil
		}
		var text string
		if json.Unmarshal(rl.Message.Content, &text) != nil {
			return nil // structured content: tool results, not recorded
		}
		if strings.HasPrefix(text, localStdoutOpen) {
			body := strings.TrimSuffix(strings.TrimPrefix(text, localStdoutOpen), localStdoutClose)
			t.LocalOutput = append(t.LocalOutput, strings.TrimSpace(body))
		}
	}
	return nil
}

func (t *HostTranscript) attachment(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var a rawTranscriptAttachment
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(a.Type, hookAttachmentPrefix) && a.Type != hookContextType && a.HookEvent != "":
		run := TranscriptHookRun{
			Type: a.Type, HookName: a.HookName, HookEvent: a.HookEvent, ExitCode: a.ExitCode, Command: a.Command,
		}
		if a.DurationMS != nil {
			run.DurationMS, run.HasDuration = *a.DurationMS, true
		}
		t.HookRuns = append(t.HookRuns, run)
	case a.Type == hookContextType:
		text := contextText(a.Content)
		c := TranscriptContext{
			HookEvent: a.HookEvent, HookName: a.HookName, Bytes: len(text),
			Qompack: hasQompackMarker(text),
		}
		if c.Qompack {
			c.Text = text
			if len(c.Text) > maxInjectionKeepBytes {
				c.Text = c.Text[:maxInjectionKeepBytes]
			}
		}
		t.Contexts = append(t.Contexts, c)
	}
	return nil
}

// contextText flattens a hook_additional_context content value, which the host writes as a list
// of strings (and which is accepted as a single string too).
func contextText(raw json.RawMessage) string {
	var parts []string
	if json.Unmarshal(raw, &parts) == nil {
		return strings.Join(parts, "\n")
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one
	}
	return ""
}

// hookErrorText renders one stop-hook-summary error, which the host writes as a string or an
// object, as text.
func hookErrorText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// HookLatencies groups the host-measured duration of every hook run by hook name, in milliseconds,
// ascending within each name. Runs the host recorded no duration for are skipped.
func (t HostTranscript) HookLatencies() map[string][]int64 {
	out := map[string][]int64{}
	for _, r := range t.HookRuns {
		if r.HasDuration {
			out[r.HookName] = append(out[r.HookName], r.DurationMS)
		}
	}
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool { return out[k][i] < out[k][j] })
	}
	return out
}

// QompackInjections returns the Qompack rehydration contexts the host recorded, and their total
// size in bytes.
func (t HostTranscript) QompackInjections() ([]TranscriptContext, int) {
	var out []TranscriptContext
	total := 0
	for _, c := range t.Contexts {
		if c.Qompack {
			out = append(out, c)
			total += c.Bytes
		}
	}
	return out, total
}

// HookProblems lists the hook failures the transcript shows: local-command reports (a rejected
// PreCompact output lands here) and hook runs recorded under a failure type.
func (t HostTranscript) HookProblems() []HostHookProblem {
	var out []HostHookProblem
	for _, lo := range t.LocalOutput {
		out = append(out, ParseHookFailures(lo, -1)...)
	}
	for _, r := range t.HookRuns {
		if r.Type == hookSuccessType {
			continue
		}
		out = append(out, HostHookProblem{
			Turn: -1, Event: r.HookEvent, Command: r.Command, Kind: HookProblemOutcome,
			Detail: boundRunes(fmt.Sprintf("%s recorded as %s", r.HookName, r.Type), hookProblemDetailRunes),
		})
	}
	for _, e := range t.HookErrors {
		out = append(out, HostHookProblem{Turn: -1, Kind: HookProblemFailed, Detail: e})
	}
	return out
}

// MergeHookProblems combines problem lists from several sources — the stream, the transcript, the
// host's stderr — without counting one failure twice: the same rejected PreCompact output reaches
// both the stream and the transcript, in texts that need not be byte-identical. Problems are keyed
// by event, kind and command; for each key the merged list holds as many problems as the source
// that reported it most often, taken from the earliest source that has them (the stream, which
// knows the turn). Two genuine failures of one hook in one session are still two.
func MergeHookProblems(sources ...[]HostHookProblem) []HostHookProblem {
	key := func(p HostHookProblem) string { return p.Event + "\x00" + p.Kind + "\x00" + p.Command }
	want := map[string]int{}
	for _, src := range sources {
		n := map[string]int{}
		for _, p := range src {
			n[key(p)]++
		}
		for k, c := range n {
			want[k] = max(want[k], c)
		}
	}
	var out []HostHookProblem
	have := map[string]int{}
	for _, src := range sources {
		for _, p := range src {
			k := key(p)
			if have[k] < want[k] {
				have[k]++
				out = append(out, p)
			}
		}
	}
	return out
}
