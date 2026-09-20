package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Host validation is the second half of Qompack.md §7.5: "Repository JSON parsing alone does not
// validate the installed plugin." `devtool plugin-validate` is this repository agreeing with
// itself; `claude plugin validate` is the HOST's own opinion of the same bytes.
//
// What it is NOT is an installed-plugin canary. The directory is validated where it sits, so
// installed manifest resolution, ${CLAUDE_PLUGIN_ROOT} expansion and launcher discovery stay
// unverified — and the record says so in every copy, because a reader who finds only "success:
// true" will otherwise read it as the stronger claim.
//
// That labelling rule is also why the verdict logic below is as careful as it is. This record is a
// committed deliverable, so every way it could overstate what happened is a defect: a non-zero exit
// is never overridden by a hopeful line of JSON, a CLI that never ran is never spelled "rejected",
// and the record always says which signal the verdict came from.
const (
	claudeBinary = "claude"

	hostValidationKind = "claude-plugin-validate"

	hostValidationLabel = "Directory validation, not an installed-plugin canary: the bundle was " +
		"validated where it sits. Installed manifest resolution, ${CLAUDE_PLUGIN_ROOT} expansion and " +
		"launcher discovery remain unverified until the platform matrix covers them."

	hostValidationAbsent = "unverified: claude CLI not on PATH"

	// claudeProbeTimeout bounds each `claude` invocation, so a CLI that hangs fails the task
	// instead of hanging the developer's terminal.
	claudeProbeTimeout = 2 * time.Minute
)

// The five outcomes a host-validation attempt can have. They are distinct in the record and in the
// message because they are distinct facts: only `rejected` is the host saying the bundle is wrong.
const (
	outcomeAccepted    = "accepted"
	outcomeRejected    = "rejected"
	outcomeTimedOut    = "timed-out"
	outcomeCouldNotRun = "could-not-run"
	outcomeUnverified  = "unverified"
)

// Where the pass/fail decision came from. Recorded because a future CLI that renames or drops
// `success` would otherwise silently demote every record to an exit-status verdict with nothing
// saying so.
const (
	verdictFromJSON       = "the CLI's --json `success` field, together with its exit status"
	verdictFromExitNoKey  = "the CLI's exit status (its JSON output carried no `success` field)"
	verdictFromExitNoJSON = "the CLI's exit status (its output was not JSON)"
	verdictNone           = "none: the CLI produced no verdict"
)

// claudeRun is one `claude` invocation's outcome. Stdout and Stderr are kept apart deliberately:
// folding them together (as CombinedOutput does) lets a single update notice or warning line make
// a `--json` verdict unparseable, which would demote a perfectly good answer to raw text.
type claudeRun struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	// Err is set when the CLI could not be run at all, or was killed at the deadline. It is not
	// set for a CLI that ran and exited non-zero — that is a verdict, not a failure to ask.
	Err error
	// TimedOut distinguishes the deadline from a launch failure.
	TimedOut bool
}

// claudeProbe is the pair of external dependencies host validation has. They are injected so the
// verdict logic below is testable: an absent CLI, a non-zero exit, output that is not JSON, JSON
// without a `success` key and a CLI that never ran are all branches that decide what a committed
// artifact claims, and none of them can be reached by running the happy path on this machine.
type claudeProbe struct {
	lookPath func() (string, error)
	run      func(bin string, args ...string) claudeRun
}

// osClaudeProbe is the real probe: PATH lookup and a bounded subprocess.
func osClaudeProbe() claudeProbe {
	return claudeProbe{
		lookPath: func() (string, error) { return exec.LookPath(claudeBinary) },
		run:      runClaude,
	}
}

// runClaude executes the CLI with a deadline, capturing the two streams separately.
func runClaude(bin string, args ...string) claudeRun {
	ctx, cancel := context.WithTimeout(context.Background(), claudeProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // G204: fixed subcommand over a path this tool just assembled
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	out := claudeRun{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// A killed process surfaces as an *exec.ExitError, so the deadline has to be checked
		// FIRST or a timeout is indistinguishable from the host rejecting the bundle.
		out.TimedOut = true
		out.ExitCode = -1
		out.Err = fmt.Errorf("`%s %s` did not finish within %s", claudeBinary, strings.Join(args, " "), claudeProbeTimeout)
	case errors.As(err, &exitErr):
		out.ExitCode = exitErr.ExitCode()
	default:
		out.ExitCode = -1
		out.Err = err
	}
	return out
}

// hostValidation is the committed evidence record. ExitCode and Success are pointers so that a
// record with no verdict omits them entirely: a zero exit code and a false success would both read
// as findings, and neither was observed.
type hostValidation struct {
	Kind          string          `json:"kind"`
	Label         string          `json:"label"`
	Outcome       string          `json:"outcome"`
	Unverified    string          `json:"unverified,omitempty"`
	Host          bundleTarget    `json:"host"`
	Go            string          `json:"go"`
	ClaudeCLI     string          `json:"claudeCli,omitempty"`
	Command       []string        `json:"command,omitempty"`
	ExitCode      *int            `json:"exitCode,omitempty"`
	Success       *bool           `json:"success,omitempty"`
	VerdictSource string          `json:"verdictSource"`
	ExecError     string          `json:"execError,omitempty"`
	Output        json.RawMessage `json:"output,omitempty"`
	OutputRaw     string          `json:"outputRaw,omitempty"`
	Stderr        string          `json:"stderr,omitempty"`
	Bundle        bundleIdentity  `json:"bundle"`
}

// rejectEvidenceDirectoryPath refuses an --evidence path that ends in a separator. The flag
// names a FILE; a trailing slash would write a regular file whose name is the directory (Join
// strips the separator) while upload-artifact with a trailing-slash pattern matches directories
// only, so the record would be uploaded as nothing.
func rejectEvidenceDirectoryPath(p string) error {
	if p == "" {
		return nil
	}
	if strings.HasSuffix(p, "/") || strings.HasSuffix(p, `\`) {
		return fmt.Errorf("bundle: --evidence %q ends in a path separator; it names a file, not a directory", p)
	}
	return nil
}

// writeHostValidation runs the host validator over the assembled bundle and records the result,
// either to evidencePath or to stdout.
//
// A missing CLI is not a failure of this task — it is an absence of evidence, recorded as one. A
// CLI that ran and rejected the bundle IS a failure, because that is the host telling us the
// bundle is malformed. A timeout and a CLI that never ran are failures too, but they are neither
// of the first two and the message says which.
func writeHostValidation(dir string, id bundleIdentity, evidencePath string) error {
	if err := rejectEvidenceDirectoryPath(evidencePath); err != nil {
		return err
	}
	rec := runHostValidation(dir, id)
	raw, err := marshalBundleJSON(rec)
	if err != nil {
		return err
	}

	if evidencePath == "" {
		fmt.Print(string(raw))
	} else {
		p := rootRelative(evidencePath)
		if err := os.MkdirAll(filepath.Dir(p), bundleDirPerm); err != nil {
			return err
		}
		if err := os.WriteFile(p, raw, bundleFilePerm); err != nil {
			return err
		}
		fmt.Printf("bundle: host validation record written to %s\n", evidencePath)
	}

	name := filepath.Base(dir)
	switch rec.Outcome {
	case outcomeUnverified:
		fmt.Println("bundle: " + rec.Unverified + " — the bundle is BUILT, not host-validated")
		return nil
	case outcomeTimedOut:
		return fmt.Errorf("bundle: `claude plugin validate --strict` did not finish within %s for %s, "+
			"so the host gave no verdict on it", claudeProbeTimeout, name)
	case outcomeCouldNotRun:
		return fmt.Errorf("bundle: `claude plugin validate --strict` could not be run for %s, "+
			"so the host gave no verdict on it: %s", name, rec.ExecError)
	case outcomeRejected:
		return fmt.Errorf("bundle: `claude plugin validate --strict` rejected %s (exit %s, verdict from %s)",
			name, exitCodeString(rec.ExitCode), rec.VerdictSource)
	}
	fmt.Printf("bundle: `claude plugin validate --strict --json` accepted %s (%s, verdict from %s)\n",
		name, hostValidationKind, rec.VerdictSource)
	return nil
}

// runHostValidation is writeHostValidation's real-probe entry point.
func runHostValidation(dir string, id bundleIdentity) hostValidation {
	return runHostValidationWith(osClaudeProbe(), dir, id)
}

// runHostValidationWith asks the host and builds the record. It never returns an error: every
// outcome, including the CLI being absent or failing to run, is part of the evidence.
func runHostValidationWith(p claudeProbe, dir string, id bundleIdentity) hostValidation {
	rec := hostValidation{
		Kind:   hostValidationKind,
		Label:  hostValidationLabel,
		Host:   bundleTarget{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Go:     runtime.Version(),
		Bundle: id,
	}

	bin, err := p.lookPath()
	if err != nil {
		rec.Outcome = outcomeUnverified
		rec.Unverified = hostValidationAbsent
		rec.VerdictSource = verdictNone
		return rec
	}

	redact := hostPathRedactor(root, filepath.Dir(dir), userHomeDir())
	rec.ClaudeCLI = redact(claudeCLIVersion(p, bin))

	args := []string{"plugin", "validate", dir, "--strict", "--json"}
	rec.Command = make([]string, 0, len(args)+1)
	rec.Command = append(rec.Command, claudeBinary)
	for _, a := range args {
		rec.Command = append(rec.Command, redact(a))
	}

	run := p.run(bin, args...)
	code := run.ExitCode
	rec.ExitCode = &code
	if run.Err != nil {
		rec.ExecError = redact(run.Err.Error())
	}

	stdout := redact(string(run.Stdout))
	if s := strings.TrimSpace(redact(string(run.Stderr))); s != "" {
		rec.Stderr = s
	}
	switch {
	case json.Valid([]byte(stdout)):
		rec.Output = json.RawMessage(stdout)
	case stdout != "":
		rec.OutputRaw = stdout
	}

	// A pointer, not a bool: a future CLI that renames the field must read as "no verdict" rather
	// than as a failure, which is what decoding into a bool would silently produce.
	var verdict struct {
		Success *bool `json:"success"`
	}
	parsed := json.Unmarshal([]byte(stdout), &verdict) == nil

	switch {
	case run.TimedOut:
		rec.Outcome = outcomeTimedOut
		rec.VerdictSource = verdictNone
	case run.Err != nil:
		rec.Outcome = outcomeCouldNotRun
		rec.VerdictSource = verdictNone
	default:
		var success bool
		switch {
		case parsed && verdict.Success != nil:
			// Both signals must agree. The exit code is never overridden by the payload: a CLI
			// that exits non-zero while printing {"success":true} is not a bundle this record may
			// call validated.
			success = code == 0 && *verdict.Success
			rec.VerdictSource = verdictFromJSON
		case parsed:
			success = code == 0
			rec.VerdictSource = verdictFromExitNoKey
		default:
			success = code == 0
			rec.VerdictSource = verdictFromExitNoJSON
		}
		rec.Success = &success
		rec.Outcome = outcomeRejected
		if success {
			rec.Outcome = outcomeAccepted
		}
	}
	return rec
}

// exitCodeString renders an optional exit code for a message.
func exitCodeString(code *int) string {
	if code == nil {
		return "unknown"
	}
	return fmt.Sprint(*code)
}

// claudeCLIVersion returns `claude --version`'s output, or a marker when it could not be read. The
// version is load-bearing evidence: the host contract is undocumented and changes between
// releases, so a record that does not say which CLI produced it says very little.
//
// The caller redacts the result, because a failure message embeds the resolved binary path.
func claudeCLIVersion(p claudeProbe, bin string) string {
	run := p.run(bin, "--version")
	switch {
	case run.Err != nil:
		return fmt.Sprintf("unknown (`%s --version` could not be run: %v)", claudeBinary, run.Err)
	case run.ExitCode != 0:
		return fmt.Sprintf("unknown (`%s --version` exited %d)", claudeBinary, run.ExitCode)
	}
	return strings.TrimSpace(string(run.Stdout))
}

// userHomeDir returns the user's home directory, or "" when it cannot be determined.
func userHomeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// hostPathRedactor removes this machine's absolute paths from anything the record retains. The
// repository sits under the user's home directory, the CLI names the directory it was pointed at in
// every message it emits, and it may name paths of its own under ~/.claude; the evidence is
// committed to a public repository, so none of them may reach the tree.
//
// Both the plain and the JSON-escaped spelling of each prefix are replaced, because the CLI emits
// each in different places — the plain one in prose, the backslash-doubled one inside JSON.
func hostPathRedactor(repoRoot, outParent, homeDir string) func(string) string {
	type rule struct{ from, to string }
	var rules []rule
	add := func(p, label string) {
		if p == "" {
			return
		}
		rules = append(rules, rule{strings.ReplaceAll(p, `\`, `\\`), label}, rule{p, label})
	}
	// Most specific first: an output directory outside the repository, then the repository, then
	// the home directory that contains both. Reversing the order would replace the home prefix of
	// the repository path and leave the rest of it in the record.
	if repoRoot == "" || !strings.HasPrefix(outParent, repoRoot) {
		add(outParent, "<out>")
	}
	add(repoRoot, "<repo>")
	add(homeDir, "<home>")

	return func(s string) string {
		for _, r := range rules {
			s = strings.ReplaceAll(s, r.from, r.to)
		}
		return s
	}
}
