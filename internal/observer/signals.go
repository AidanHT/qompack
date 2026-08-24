package observer

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// Signals is the L0 → L3 hand-off of gap G1.5 (00-ARCHITECTURE.md §5.21): the task-boundary
// evidence a hook payload carries, extracted in L0 and consumed by the scheduler's composite
// trigger. It is a plain value rather than a scheduler type precisely because observer may not
// import scheduler (§3.2) — the hand-off crosses that boundary as data, not as a call.
type Signals struct {
	// TodoCompleted reports that a todo item moved to completed in this event.
	TodoCompleted bool
	// TestPassed reports that a test run in this event succeeded.
	TestPassed bool
	// GitCommit reports that this event was a git commit.
	GitCommit bool
	// Paths lists the file paths this event touched, RAW: exactly as the host submitted them,
	// separators, casing and relativity included. Normalization to paths.Key form is the
	// observer's own job on the PostToolUse path, before OnSignals is called — it is deliberately
	// not done here, because everything in this file is a pure function of a hookio.Event so that
	// ExtractSignals stays reusable by the daemon and trivially fuzzable.
	Paths []string
}

// ExtractSignals reads the task-boundary signals out of one hook payload: a todo transition, a
// passing test run, a git commit, and the paths the event touched. The scheduler treats each as
// evidence that a unit of work closed, which is when compaction is cheapest (Qompack.md §8.4).
//
// It is pure — no clock, no state, no I/O — and it never panics or errors: a payload whose shape
// the host changed underneath us yields the zero value of whatever could not be read, which is
// also the honest answer, because no boundary was observed.
//
// Note what it does NOT do. TodoCompleted reports the STATE of the list, not the transition into
// it; newly-completed detection compares against the session's previous list and therefore lives
// in the observer's stateful path, because §5.21 requires this function to stay pure.
func ExtractSignals(e Event) Signals {
	return Signals{
		TodoCompleted: hasCompletedTodo(e),
		TestPassed:    ExtractTestOutcome(e) == TestPass,
		GitCommit:     isGitCommit(e),
		Paths:         PathsFromInput(e.ToolName, e.ToolInput),
	}
}

// TestOutcome is the verdict ExtractTestOutcome reads out of a test-runner invocation.
type TestOutcome uint8

const (
	// TestUnknown means the event was not a recognized test run, or the run reported no verdict
	// this package knows how to read. It is the zero value, so an unread payload is never
	// mistaken for a result.
	TestUnknown TestOutcome = iota
	// TestPass means the run reported success.
	TestPass
	// TestFail means the run reported at least one failure.
	TestFail
)

// maxScanBytes bounds how many tool_response bytes the pure decoders in this file will look at:
// 4 MiB. It bounds the decoder's allocation and nothing else — the CONFIGURED cap
// (runtime.hotPath.maxPayloadBytes) is applied separately, on the observer's own PostToolUse path,
// because this file may not read observer state and stay pure.
//
// 4 MiB is deliberately larger than that setting's 1 MiB default, so in practice the two caps
// never interact. When a user configures a larger payload cap this one binds first, and the
// retained prefix is scanned as-is: a JSON payload cut at 4 MiB no longer parses, so it falls
// through to the raw-bytes branch of responseText rather than being repaired.
const maxScanBytes = 4 << 20

// reTestRunner recognizes the command lines whose output is worth scanning for a verdict. It is
// the gate, not the verdict: plenty of ordinary output contains the word PASS, and only a
// recognized runner turns that into evidence that a unit of work closed.
var reTestRunner = regexp.MustCompile(`(?i)\b(go\s+test|npm\s+(run\s+)?test|yarn\s+test|pnpm\s+test|jest|vitest|pytest|python\s+-m\s+pytest|cargo\s+test|dotnet\s+test|mvn\s+test|gradle\s+test|rspec|ctest)\b`)

// reTestPass are the success summaries of the runners above, one per output dialect.
var reTestPass = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^ok\s+\S+`),
	regexp.MustCompile(`(?m)^PASS\b`),
	regexp.MustCompile(`(?i)\btests?:\s+\d+\s+passed\b`),
	regexp.MustCompile(`(?i)=+\s*\d+\s+passed`),
	regexp.MustCompile(`test result: ok\.`),
	regexp.MustCompile(`(?i)\bOK\s+\(\d+\s+tests?\)`),
}

// reTestFail are the failure summaries, checked before the success ones: a run with one failure is
// not a passing test run, however many tests passed alongside it.
//
// The bare FAILED pattern is deliberately case-SENSITIVE while every other pattern here is not.
// Uppercase FAILED is the standalone marker pytest, cargo and jest print for a failure; lowercase
// "failed" is a count word that means nothing without its count, and every clean cargo, pytest and
// jest summary ends with "0 failed". A case-insensitive match would therefore read every passing
// run as a failure and suppress the task boundary on the most common summary line there is. The
// counted form is covered by the next pattern, whose [1-9] is what makes a zero count harmless.
var reTestFail = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^FAIL\b`),
	regexp.MustCompile(`(?m)^---\s+FAIL`),
	regexp.MustCompile(`\bFAILED\b`),
	regexp.MustCompile(`(?i)\b[1-9]\d*\s+failed\b`),
	regexp.MustCompile(`test result: FAILED\.`),
}

// reGitCommit recognizes a commit anywhere in a command line, including the -C form every commit
// into a sibling worktree is spelled with, because commits are almost always the tail of a chain.
var reGitCommit = regexp.MustCompile(`(?i)\bgit\s+(-C\s+\S+\s+)?commit\b`)

// reGitNoop recognizes the two ways git reports that a commit committed nothing. The command ran,
// but no work closed, so firing a compaction on it would spend tokens for nothing.
var reGitNoop = regexp.MustCompile(`(?i)nothing to commit|no changes added to commit`)

// ExtractTestOutcome reads the verdict of a test run out of one hook payload, or TestUnknown when
// the event was not one. Two conditions gate it: the tool must be a shell (a file whose contents
// happen to contain "PASS" is not a test run), and the command must name a recognized runner.
//
// Failure is checked before success on purpose. A run that reports both — "2 failed, 40 passed" —
// closed nothing.
func ExtractTestOutcome(e Event) TestOutcome {
	if !isShellTool(e.ToolName) || !reTestRunner.MatchString(commandOf(e)) {
		return TestUnknown
	}

	body := responseText(e)
	for _, re := range reTestFail {
		if re.Match(body) {
			return TestFail
		}
	}
	for _, re := range reTestPass {
		if re.Match(body) {
			return TestPass
		}
	}
	return TestUnknown
}

// isShellTool reports whether hostName is one of the two shells of §2.2 whose command line the
// signal extractors parse.
func isShellTool(hostName string) bool {
	switch NormalizeToolName(hostName) {
	case "Bash", "PowerShell":
		return true
	default:
		return false
	}
}

// isGitCommit reports whether this event was a git commit that actually committed something.
func isGitCommit(e Event) bool {
	if !isShellTool(e.ToolName) || !reGitCommit.MatchString(commandOf(e)) {
		return false
	}
	return !reGitNoop.Match(responseText(e))
}

// todoStatusCompleted is the status value Claude Code writes for a finished todo item.
const todoStatusCompleted = "completed"

// todoInput is the tolerant shape of TodoWrite's tool_input. Every field this file unmarshals into
// is optional: a shape the host changed yields zero values rather than an error.
type todoInput struct {
	Todos []struct {
		Status string `json:"status"`
	} `json:"todos"`
}

// hasCompletedTodo reports whether the event is a TodoWrite whose list contains at least one
// completed item.
func hasCompletedTodo(e Event) bool {
	if e.ToolName != "TodoWrite" {
		return false
	}
	var in todoInput
	if err := json.Unmarshal(e.ToolInput, &in); err != nil {
		return false
	}
	for _, todo := range in.Todos {
		if todo.Status == todoStatusCompleted {
			return true
		}
	}
	return false
}

// pathInput is the union of the path-bearing keys across every host tool: the three top-level
// spellings and the per-edit array MultiEdit sends.
type pathInput struct {
	FilePath     string `json:"file_path"`
	Path         string `json:"path"`
	NotebookPath string `json:"notebook_path"`
	Edits        []struct {
		FilePath string `json:"file_path"`
	} `json:"edits"`
}

// PathsFromInput returns the paths a tool input names, in a fixed order — file_path, path,
// notebook_path, then every edits[].file_path — deduplicated with first-seen order preserved.
// Order is what makes the result usable as a feature: the changepoint detector compares the path
// sets of consecutive turns, and a set that reordered itself would look like movement that never
// happened.
//
// Values are returned RAW, exactly as the host submitted them; the caller normalizes with
// paths.Norm/paths.Key. An input naming no path at all returns nil rather than an empty slice, so
// that ExtractSignals of an empty event is exactly the zero Signals.
//
// tool is not dispatched on: the key union above covers every §2.2 tool, so a host tool that grows
// a new path-bearing key is picked up without a table edit. It is part of the signature §5.21
// publishes so callers pass the name they already hold rather than deciding which keys apply.
func PathsFromInput(tool string, in json.RawMessage) []string {
	var v pathInput
	if err := json.Unmarshal(in, &v); err != nil {
		return nil
	}

	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		for _, seen := range out {
			if seen == p {
				return
			}
		}
		out = append(out, p)
	}

	add(v.FilePath)
	add(v.Path)
	add(v.NotebookPath)
	for _, edit := range v.Edits {
		add(edit.FilePath)
	}
	return out
}

// commandOf returns the shell command a tool input carries, or "" when the payload has none or
// cannot be read.
func commandOf(e Event) string {
	var in struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(e.ToolInput, &in); err != nil {
		return ""
	}
	return in.Command
}

// toolResponse is the tolerant union of the tool_response object shapes Claude Code sends. Content
// stays raw because it is a string in one dialect and an array of typed blocks in another.
type toolResponse struct {
	Content json.RawMessage `json:"content"`
	Stdout  string          `json:"stdout"`
	Stderr  string          `json:"stderr"`
}

// contentBlock is one element of the array form of tool_response.content.
type contentBlock struct {
	Text string `json:"text"`
}

// responseText unwraps e.ToolResponse into the plain text the signal patterns scan, trying the
// four shapes the host uses in a fixed order: a bare JSON string; an object with "content", either
// a string or an array of blocks whose text is joined with newlines; an object with "stdout", plus
// stderr behind a newline when it carries anything; and anything else as compacted JSON.
//
// The order is what makes it deterministic rather than a guess, and every step is failure-tolerant:
// a payload that matches no shape is scanned as its own raw bytes rather than discarded, because a
// verdict line is still a verdict line inside JSON nobody could parse.
//
// The input is capped at maxScanBytes before any decoding, so a decoded result can never exceed it
// either — a JSON string is always at least as long as the text it encodes.
func responseText(e Event) []byte {
	raw := e.ToolResponse
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxScanBytes {
		raw = raw[:maxScanBytes]
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []byte(text)
	}

	var resp toolResponse
	if err := json.Unmarshal(raw, &resp); err == nil {
		if len(resp.Content) > 0 {
			var content string
			if err := json.Unmarshal(resp.Content, &content); err == nil {
				return []byte(content)
			}
			var blocks []contentBlock
			if err := json.Unmarshal(resp.Content, &blocks); err == nil {
				parts := make([]string, len(blocks))
				for i, block := range blocks {
					parts[i] = block.Text
				}
				return []byte(strings.Join(parts, "\n"))
			}
		}
		if resp.Stdout != "" || resp.Stderr != "" {
			if resp.Stderr == "" {
				return []byte(resp.Stdout)
			}
			return []byte(resp.Stdout + "\n" + resp.Stderr)
		}
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return raw
	}
	return compact.Bytes()
}
