// This is an INTERNAL test package (package observer, not observer_test) because responseText and
// commandOf are unexported and are where the host's payload shapes are actually absorbed. Asserting
// them only through ExtractSignals would mean every shape failure surfaced as "no signal detected",
// which is also what a correct extractor returns for an ordinary read.
package observer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// toolEvent builds a PostToolUse payload from raw tool_input and tool_response JSON, so a test can
// pin the exact bytes the host would have sent rather than a Go value's round trip.
func toolEvent(tool, input, response string) Event {
	e := Event{HookEventName: "PostToolUse", ToolName: tool}
	if input != "" {
		e.ToolInput = json.RawMessage(input)
	}
	if response != "" {
		e.ToolResponse = json.RawMessage(response)
	}
	return e
}

// shellEvent builds a PostToolUse payload for a shell tool running command and returning response.
// response is wrapped as a bare JSON string — one of the four shapes responseText recognizes, and
// the one that keeps the test's expectation readable.
func shellEvent(tb testing.TB, tool, command, response string) Event {
	tb.Helper()
	in, err := json.Marshal(struct {
		Command string `json:"command"`
	}{Command: command})
	require.NoError(tb, err)
	out, err := json.Marshal(response)
	require.NoError(tb, err)
	return toolEvent(tool, string(in), string(out))
}

// TestExtractSignals_TodoCompleted is G1.5's cheapest task boundary: the agent's own todo list says
// a unit of work closed. Compaction is cheapest exactly there (Qompack.md §8.4).
func TestExtractSignals_TodoCompleted(t *testing.T) {
	e := toolEvent("TodoWrite", `{"todos":[{"content":"a","status":"completed"},{"content":"b","status":"pending"}]}`, "")

	got := ExtractSignals(e)
	require.True(t, got.TodoCompleted, "one completed item in the list is a boundary")
	require.False(t, got.TestPassed)
	require.False(t, got.GitCommit)
}

// TestExtractSignals_TodoNoneCompleted asserts an in-flight todo list is not a boundary. Writing
// the plan is not finishing the work, and a compaction fired here would evict the context the next
// step needs.
func TestExtractSignals_TodoNoneCompleted(t *testing.T) {
	e := toolEvent("TodoWrite", `{"todos":[{"content":"a","status":"in_progress"},{"content":"b","status":"pending"}]}`, "")

	require.False(t, ExtractSignals(e).TodoCompleted)
}

// TestExtractTestOutcome_GoPass covers `go test`'s passing form: the per-package "ok" line.
func TestExtractTestOutcome_GoPass(t *testing.T) {
	e := shellEvent(t, "Bash", "go test ./...", "ok  \tgithub.com/x/y\t0.31s\n")

	require.Equal(t, TestPass, ExtractTestOutcome(e))
	require.True(t, ExtractSignals(e).TestPassed)
}

// TestExtractTestOutcome_GoFail covers `go test`'s failing form.
func TestExtractTestOutcome_GoFail(t *testing.T) {
	e := shellEvent(t, "Bash", "go test ./...", "--- FAIL: TestX\nFAIL\n")

	require.Equal(t, TestFail, ExtractTestOutcome(e))
	require.False(t, ExtractSignals(e).TestPassed, "a failing run closes nothing")
}

// TestExtractTestOutcome_JestPass covers the jest/vitest summary line.
func TestExtractTestOutcome_JestPass(t *testing.T) {
	e := shellEvent(t, "Bash", "npm test", "Tests:       42 passed, 42 total")

	require.Equal(t, TestPass, ExtractTestOutcome(e))
}

// TestExtractTestOutcome_PytestMixed is the fail-wins rule: a run with one failure is not a passing
// test run, however many tests passed alongside it.
func TestExtractTestOutcome_PytestMixed(t *testing.T) {
	e := shellEvent(t, "Bash", "pytest -q", "2 failed, 40 passed")

	require.Equal(t, TestFail, ExtractTestOutcome(e))
}

// TestExtractTestOutcome_CargoPass is the counter-case to the fail-wins rule, and the reason the
// FAILED pattern is case-sensitive: cargo's passing summary contains the literal word "failed" with
// a zero count in front of it. Reading that as a failure would suppress a task boundary on every
// clean cargo, pytest and jest run there is.
func TestExtractTestOutcome_CargoPass(t *testing.T) {
	e := shellEvent(t, "Bash", "cargo test", "test result: ok. 12 passed; 0 failed")

	require.Equal(t, TestPass, ExtractTestOutcome(e))
}

// TestExtractTestOutcome_NotATestCommand asserts the command gates the response. Plenty of output
// contains the word PASS; only a recognized test runner makes it a verdict.
func TestExtractTestOutcome_NotATestCommand(t *testing.T) {
	e := shellEvent(t, "Bash", "ls -la", "PASS")

	require.Equal(t, TestUnknown, ExtractTestOutcome(e))
	require.False(t, ExtractSignals(e).TestPassed)
}

// TestExtractTestOutcome_NotAShellTool asserts a test verdict can only come from a shell. A file
// whose contents happen to contain "PASS" is not a test run.
func TestExtractTestOutcome_NotAShellTool(t *testing.T) {
	e := toolEvent("Read", `{"file_path":"go.mod","command":"go test ./..."}`, `"PASS\n"`)

	require.Equal(t, TestUnknown, ExtractTestOutcome(e))
}

// TestExtractTestOutcome_PowerShell asserts the second shell of §2.2 is recognized too. Windows is
// this project's primary development platform, so a Bash-only extractor would miss the boundary
// signal exactly where it is developed.
func TestExtractTestOutcome_PowerShell(t *testing.T) {
	e := shellEvent(t, "PowerShell", "go test ./internal/observer/", "ok  \tgithub.com/x/y\t0.31s\n")

	require.Equal(t, TestPass, ExtractTestOutcome(e))
}

// TestExtractSignals_GitCommit is G1.5's strongest boundary: a commit is a unit of work the session
// itself declared finished.
func TestExtractSignals_GitCommit(t *testing.T) {
	e := shellEvent(t, "Bash", `git commit -m "x"`, "[main 3f2a1] x\n 2 files changed")

	require.True(t, ExtractSignals(e).GitCommit)
}

// TestExtractSignals_GitCommitNoop asserts a commit that committed nothing is not a boundary. The
// command matched, but no work closed, and firing a compaction here would spend tokens for nothing.
func TestExtractSignals_GitCommitNoop(t *testing.T) {
	e := shellEvent(t, "Bash", `git commit -m "x"`, "nothing to commit, working tree clean")

	require.False(t, ExtractSignals(e).GitCommit)
}

// TestExtractSignals_GitCommitInChain asserts the command is scanned rather than matched whole:
// commits are almost always the tail of a chained command line.
func TestExtractSignals_GitCommitInChain(t *testing.T) {
	e := shellEvent(t, "Bash", "git add -A && git commit -m x", "[main 3f2a1] x\n")

	require.True(t, ExtractSignals(e).GitCommit)
}

// TestExtractSignals_GitCommitWithDashC asserts the -C form is recognized. It is how every commit
// into a sibling worktree is spelled, so missing it would lose the boundary on exactly the
// multi-worktree workflow this repository is developed with.
func TestExtractSignals_GitCommitWithDashC(t *testing.T) {
	e := shellEvent(t, "Bash", "git -C ../qompack-develop commit -m x", "[develop 3f2a1] x\n")

	require.True(t, ExtractSignals(e).GitCommit)
}

// TestPathsFromInput_Read covers the single-path shape every file tool uses.
func TestPathsFromInput_Read(t *testing.T) {
	got := PathsFromInput("FileRead", json.RawMessage(`{"file_path":"src/a.ts"}`))

	require.Equal(t, []string{"src/a.ts"}, got)
}

// TestPathsFromInput_MultiEdit covers the edits array, the dedup, and the fact that order is
// preserved. Order is what makes the value usable as a feature: the changepoint detector compares
// consecutive turns, and a set that reordered itself would look like movement that never happened.
func TestPathsFromInput_MultiEdit(t *testing.T) {
	got := PathsFromInput("FileEdit", json.RawMessage(`{"file_path":"a","edits":[{"file_path":"b"},{"file_path":"a"}]}`))

	require.Equal(t, []string{"a", "b"}, got)
}

// TestPathsFromInput_Glob asserts the search shape: the pattern is not a path, the search root is.
func TestPathsFromInput_Glob(t *testing.T) {
	got := PathsFromInput("Glob", json.RawMessage(`{"pattern":"**/*.go","path":"internal"}`))

	require.Equal(t, []string{"internal"}, got)
}

// TestPathsFromInput_NotebookAndOrder pins the emission order of the three top-level keys.
func TestPathsFromInput_NotebookAndOrder(t *testing.T) {
	got := PathsFromInput("FileEdit", json.RawMessage(`{"notebook_path":"n.ipynb","path":"p","file_path":"f"}`))

	require.Equal(t, []string{"f", "p", "n.ipynb"}, got)
}

// TestPathsFromInput_EmptyIsNil asserts an input with no path at all yields nil rather than an
// empty slice. It is not a style preference: observertest's conformance suite requires
// ExtractSignals(Event{}) to equal the zero Signals exactly, and []string{} does not.
func TestPathsFromInput_EmptyIsNil(t *testing.T) {
	require.Nil(t, PathsFromInput("Bash", json.RawMessage(`{"command":"ls"}`)))
	require.Nil(t, PathsFromInput("Bash", nil))
}

// TestExtractSignals_MalformedJSON is the degradation rule (§12.3) applied to a pure function: a
// payload shape the host changed underneath us yields no signals, never a panic and never an error.
func TestExtractSignals_MalformedJSON(t *testing.T) {
	cases := []struct{ name, tool, input, response string }{
		{name: "truncated input object", tool: "Read", input: "{not json", response: `"ok"`},
		{name: "truncated response object", tool: "Bash", input: `{"command":"go test ./..."}`, response: "{not json"},
		{name: "input is an array", tool: "TodoWrite", input: `["completed"]`, response: ""},
		{name: "response is a number", tool: "Bash", input: `{"command":"git commit -m x"}`, response: "12"},
		{name: "todos is not an array", tool: "TodoWrite", input: `{"todos":"completed"}`, response: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got Signals
			require.NotPanics(t, func() { got = ExtractSignals(toolEvent(tc.tool, tc.input, tc.response)) })
			require.False(t, got.TodoCompleted)
			require.False(t, got.TestPassed)
			require.Nil(t, got.Paths)
		})
	}
}

// TestExtractSignals_ZeroEventIsZeroSignals is the assertion observertest/behaviour.go makes from
// outside the package, pinned here too because it is what constrains PathsFromInput's nil return.
func TestExtractSignals_ZeroEventIsZeroSignals(t *testing.T) {
	require.Equal(t, Signals{}, ExtractSignals(Event{}), "an empty payload must not manufacture a boundary")
}

// TestExtractSignals_ReadReportsItsPath asserts the ordinary case: a file read is not a boundary,
// but it does report what it touched, which is what feeds the changepoint features.
func TestExtractSignals_ReadReportsItsPath(t *testing.T) {
	e := toolEvent("Read", `{"file_path":"src/auth.ts"}`, `{"content":"export async function refreshToken() {}"}`)

	require.Equal(t, Signals{Paths: []string{"src/auth.ts"}}, ExtractSignals(e))
}

// TestExtractSignals_PathsAreRaw pins what Signals.Paths carries: the host's own bytes.
// Normalization to paths.Key form happens in the observer's PostToolUse path, not here, so that
// everything in this file stays a pure function of the event.
func TestExtractSignals_PathsAreRaw(t *testing.T) {
	const winPath = `C:\Users\dev\src\Auth.ts`
	in, err := json.Marshal(struct {
		FilePath string `json:"file_path"`
	}{FilePath: winPath})
	require.NoError(t, err)

	got := ExtractSignals(toolEvent("Read", string(in), ""))
	require.Equal(t, []string{winPath}, got.Paths, "the raw host path must survive unchanged")
}

// TestResponseText_AllFourShapes covers every branch of the unwrapper, in the order it tries them.
func TestResponseText_AllFourShapes(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{name: "bare json string", raw: `"x"`, want: "x"},
		{name: "content string", raw: `{"content":"x"}`, want: "x"},
		{
			name: "content blocks joined with newlines",
			raw:  `{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}`,
			want: "a\nb",
		},
		{name: "stdout and stderr", raw: `{"stdout":"o","stderr":"e"}`, want: "o\ne"},
		{name: "stdout alone carries no separator", raw: `{"stdout":"o"}`, want: "o"},
		{name: "an unrecognized object falls back to compacted json", raw: `{"exit_code": 0}`, want: `{"exit_code":0}`},
		{name: "an array falls back to compacted json", raw: `[1, 2]`, want: `[1,2]`},
		{name: "unparseable bytes are used as-is", raw: `{not json`, want: `{not json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, string(responseText(toolEvent("Bash", "", tc.raw))))
		})
	}

	require.Nil(t, responseText(Event{}), "an absent response is no text at all")
}

// TestCommandOf covers the one field the shell extractors read, including the shapes that carry no
// command at all.
func TestCommandOf(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{name: "present", input: `{"command":"go test ./..."}`, want: "go test ./..."},
		{name: "absent", input: `{"file_path":"a.ts"}`, want: ""},
		{name: "malformed", input: `{not json`, want: ""},
		{name: "wrong type", input: `{"command":42}`, want: ""},
		{name: "missing", input: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, commandOf(toolEvent("Bash", tc.input, "")))
		})
	}
}

// corpusToolOutDir is testdata/corpora/toolout/, spelled relative to this package. It is SP-04's
// committed raw tool output, reused rather than extended — SP-08's fixture list says so in as many
// words — so the fuzzer starts from bytes a real session actually produced.
const corpusToolOutDir = "../../testdata/corpora/toolout"

// fuzzCorpusSeeds names the corpus payloads FuzzExtractSignals starts from, paired with a command
// line that makes each one reachable: a test-runner payload is only ever scanned for a verdict when
// the command that produced it was a test runner.
var fuzzCorpusSeeds = []struct{ tool, command, file string }{
	{tool: "Bash", command: "go test ./...", file: "testrunner/go-test-pass.txt"},
	{tool: "Bash", command: "go test ./...", file: "testrunner/go-test-fail.txt"},
	{tool: "Bash", command: "cargo test", file: "testrunner/cargo-test-pass.txt"},
	{tool: "Bash", command: "pytest -q", file: "testrunner/pytest-fail.txt"},
	{tool: "Bash", command: "git status", file: "git/git-status.txt"},
	{tool: "Grep", command: "", file: "grep/grep-symbol.txt"},
	{tool: "Bash", command: "npm install", file: "ansi/ansi-colored-build.txt"},
}

// FuzzExtractSignals asserts the properties every pure extractor in this file owes the hot path: it
// never panics on any payload the host could send, and every path it reports is a non-empty, valid
// UTF-8 string — because those paths reach paths.Key, the DAG's node identifiers and the store's
// index, none of which may be handed a byte sequence that cannot round-trip through JSON.
func FuzzExtractSignals(f *testing.F) {
	f.Add("Bash", `{"command":"go test ./..."}`, `{"stdout":"ok  \tgithub.com/x/y\t0.31s\n"}`)
	f.Add("TodoWrite", `{"todos":[{"content":"a","status":"completed"}]}`, "")
	f.Add("Read", `{"file_path":"src/auth.ts"}`, `{"content":"package main"}`)
	f.Add("MultiEdit", `{"file_path":"a","edits":[{"file_path":"b"}]}`, `"done"`)
	f.Add("Bash", "{not json", "{not json")

	for _, seed := range fuzzCorpusSeeds {
		body, err := os.ReadFile(filepath.Join(corpusToolOutDir, seed.file))
		if err != nil {
			f.Fatalf("reading fuzz seed %s: %v", seed.file, err)
		}
		response, err := json.Marshal(string(body))
		if err != nil {
			f.Fatalf("encoding fuzz seed %s: %v", seed.file, err)
		}
		input, err := json.Marshal(struct {
			Command string `json:"command"`
		}{Command: seed.command})
		if err != nil {
			f.Fatalf("encoding fuzz seed command for %s: %v", seed.file, err)
		}
		f.Add(seed.tool, string(input), string(response))
	}

	f.Fuzz(func(t *testing.T, tool, input, response string) {
		e := toolEvent(tool, input, response)
		got := ExtractSignals(e)

		for _, p := range got.Paths {
			if !utf8.ValidString(p) {
				t.Fatalf("ExtractSignals reported a path that is not valid UTF-8: %q", p)
			}
			if p == "" {
				t.Fatal("ExtractSignals reported an empty path")
			}
		}
		if got.TestPassed && ExtractTestOutcome(e) != TestPass {
			t.Fatal("Signals.TestPassed disagreed with ExtractTestOutcome")
		}
	})
}
