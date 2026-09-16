package main

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCLI is a scripted `claude`. runHostValidationWith's branches decide what a committed evidence
// record claims, and none of them — a non-zero exit, output that is not JSON, JSON without a
// `success` key, a CLI that never ran, a deadline — is reachable by running the happy path on a
// machine that has a working CLI.
type fakeCLI struct {
	absent   bool
	version  claudeRun
	validate claudeRun
	calls    [][]string
}

func (f *fakeCLI) probe() claudeProbe {
	return claudeProbe{
		lookPath: func() (string, error) {
			if f.absent {
				return "", exec.ErrNotFound
			}
			return filepath.Join("usr", "local", "bin", claudeBinary), nil
		},
		run: func(_ string, args ...string) claudeRun {
			f.calls = append(f.calls, args)
			if len(args) > 0 && args[0] == "--version" {
				return f.version
			}
			return f.validate
		},
	}
}

// okVersion is the `claude --version` reply every case that gets that far uses.
func okVersion() claudeRun { return claudeRun{Stdout: []byte("2.1.263 (Claude Code)\n")} }

// TestRunHostValidation drives every outcome the record can carry. The three it exists for are the
// ones the happy path hides: a non-zero exit must never be overridden by a hopeful payload, JSON
// without a `success` key must read as "no verdict from the payload" rather than as a failure, and
// a CLI that never ran (or was killed at the deadline) must never be spelled "rejected".
func TestRunHostValidation(t *testing.T) {
	prevRoot := root
	repo := filepath.Join(t.TempDir(), "repo")
	root = repo
	t.Cleanup(func() { root = prevRoot })

	dir := filepath.Join(repo, "dist", "bundle", "qompack-plugin-v1.2.3-linux-amd64")
	id := bundleIdentity{Name: bundleProductName, Version: "v1.2.3"}

	wantBool := func(b bool) *bool { return &b }

	for _, tc := range []struct {
		name          string
		cli           fakeCLI
		wantOutcome   string
		wantSuccess   *bool
		wantSource    string
		wantExit      *int
		wantExecError bool
		wantJSONOut   bool
		wantRawOut    string
		wantStderr    string
	}{
		{
			name:        "CLI absent is recorded as unverified, never as a pass or a failure",
			cli:         fakeCLI{absent: true},
			wantOutcome: outcomeUnverified,
			wantSource:  verdictNone,
		},
		{
			name: "zero exit and success:true is the only accepted shape",
			cli: fakeCLI{version: okVersion(), validate: claudeRun{
				Stdout: []byte(`{"success":true,"strict":true}`),
			}},
			wantOutcome: outcomeAccepted,
			wantSuccess: wantBool(true),
			wantSource:  verdictFromJSON,
			wantExit:    new(int),
			wantJSONOut: true,
		},
		{
			name: "a non-zero exit is not overridden by success:true in the payload",
			cli: fakeCLI{version: okVersion(), validate: claudeRun{
				Stdout: []byte(`{"success":true}`), ExitCode: 1,
			}},
			wantOutcome: outcomeRejected,
			wantSuccess: wantBool(false),
			wantSource:  verdictFromJSON,
			wantExit:    func() *int { c := 1; return &c }(),
			wantJSONOut: true,
		},
		{
			name: "success:false with a zero exit is still a rejection",
			cli: fakeCLI{version: okVersion(), validate: claudeRun{
				Stdout: []byte(`{"success":false,"errors":["bad manifest"]}`),
			}},
			wantOutcome: outcomeRejected,
			wantSuccess: wantBool(false),
			wantSource:  verdictFromJSON,
			wantExit:    new(int),
			wantJSONOut: true,
		},
		{
			name: "JSON without a success key falls back to the exit status and says so",
			cli: fakeCLI{version: okVersion(), validate: claudeRun{
				Stdout: []byte(`{"strict":true,"manifest":{}}`),
			}},
			wantOutcome: outcomeAccepted,
			wantSuccess: wantBool(true),
			wantSource:  verdictFromExitNoKey,
			wantExit:    new(int),
			wantJSONOut: true,
		},
		{
			name: "JSON without a success key and a non-zero exit is a rejection",
			cli: fakeCLI{version: okVersion(), validate: claudeRun{
				Stdout: []byte(`{"strict":true}`), ExitCode: 2,
			}},
			wantOutcome: outcomeRejected,
			wantSuccess: wantBool(false),
			wantSource:  verdictFromExitNoKey,
			wantExit:    func() *int { c := 2; return &c }(),
			wantJSONOut: true,
		},
		{
			name: "output that is not JSON is kept raw and judged on the exit status",
			cli: fakeCLI{version: okVersion(), validate: claudeRun{
				Stdout: []byte("validated 1 plugin\n"),
			}},
			wantOutcome: outcomeAccepted,
			wantSuccess: wantBool(true),
			wantSource:  verdictFromExitNoJSON,
			wantExit:    new(int),
			wantRawOut:  "validated 1 plugin\n",
		},
		{
			name: "a warning on stderr does not make the JSON verdict unparseable",
			cli: fakeCLI{version: okVersion(), validate: claudeRun{
				Stdout: []byte(`{"success":true}`),
				Stderr: []byte("warning: a newer version is available\n"),
			}},
			wantOutcome: outcomeAccepted,
			wantSuccess: wantBool(true),
			wantSource:  verdictFromJSON,
			wantExit:    new(int),
			wantJSONOut: true,
			wantStderr:  "warning: a newer version is available",
		},
		{
			name: "a CLI that never ran is could-not-run, not rejected",
			cli: fakeCLI{version: okVersion(), validate: claudeRun{
				ExitCode: -1, Err: errors.New("fork/exec: permission denied"),
			}},
			wantOutcome:   outcomeCouldNotRun,
			wantSource:    verdictNone,
			wantExit:      func() *int { c := -1; return &c }(),
			wantExecError: true,
		},
		{
			name: "a deadline kill is timed-out, not rejected",
			cli: fakeCLI{version: okVersion(), validate: claudeRun{
				ExitCode: -1, TimedOut: true, Err: errors.New("did not finish within 2m0s"),
			}},
			wantOutcome:   outcomeTimedOut,
			wantSource:    verdictNone,
			wantExit:      func() *int { c := -1; return &c }(),
			wantExecError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli := tc.cli
			rec := runHostValidationWith(cli.probe(), dir, id)

			if rec.Outcome != tc.wantOutcome {
				t.Errorf("outcome = %q, want %q", rec.Outcome, tc.wantOutcome)
			}
			if rec.VerdictSource != tc.wantSource {
				t.Errorf("verdictSource = %q, want %q", rec.VerdictSource, tc.wantSource)
			}
			switch {
			case tc.wantSuccess == nil && rec.Success != nil:
				t.Errorf("success = %v, want absent (no verdict was produced)", *rec.Success)
			case tc.wantSuccess != nil && rec.Success == nil:
				t.Errorf("success is absent, want %v", *tc.wantSuccess)
			case tc.wantSuccess != nil && *rec.Success != *tc.wantSuccess:
				t.Errorf("success = %v, want %v", *rec.Success, *tc.wantSuccess)
			}
			switch {
			case tc.wantExit == nil && rec.ExitCode != nil:
				t.Errorf("exitCode = %d, want absent", *rec.ExitCode)
			case tc.wantExit != nil && rec.ExitCode == nil:
				t.Errorf("exitCode is absent, want %d", *tc.wantExit)
			case tc.wantExit != nil && *rec.ExitCode != *tc.wantExit:
				t.Errorf("exitCode = %d, want %d", *rec.ExitCode, *tc.wantExit)
			}
			if got := rec.ExecError != ""; got != tc.wantExecError {
				t.Errorf("execError present = %v (%q), want %v", got, rec.ExecError, tc.wantExecError)
			}
			if got := len(rec.Output) > 0; got != tc.wantJSONOut {
				t.Errorf("parsed JSON output present = %v (%s), want %v", got, rec.Output, tc.wantJSONOut)
			}
			if rec.OutputRaw != tc.wantRawOut {
				t.Errorf("outputRaw = %q, want %q", rec.OutputRaw, tc.wantRawOut)
			}
			if rec.Stderr != tc.wantStderr {
				t.Errorf("stderr = %q, want %q", rec.Stderr, tc.wantStderr)
			}

			// The label is the §7.5 promise and must survive every branch.
			if rec.Label != hostValidationLabel || rec.Kind != hostValidationKind {
				t.Errorf("kind/label lost: %q / %q", rec.Kind, rec.Label)
			}
			// Whatever happened, no absolute path from this machine may reach the record.
			blob, err := json.Marshal(rec)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(blob), repo) {
				t.Errorf("the record leaks this checkout's location:\n%s", blob)
			}
		})
	}
}

// TestRunHostValidation_CommandIsRecorded pins the command the record says was run — the evidence
// is only reproducible if it names the exact invocation — and checks that the directory in it is
// redacted rather than committed verbatim.
func TestRunHostValidation_CommandIsRecorded(t *testing.T) {
	prevRoot := root
	repo := filepath.Join(t.TempDir(), "repo")
	root = repo
	t.Cleanup(func() { root = prevRoot })

	dir := filepath.Join(repo, "dist", "bundle", "qompack-plugin-v1.2.3-linux-amd64")
	cli := fakeCLI{version: okVersion(), validate: claudeRun{Stdout: []byte(`{"success":true}`)}}
	rec := runHostValidationWith(cli.probe(), dir, bundleIdentity{})

	want := []string{claudeBinary, "plugin", "validate", "", "--strict", "--json"}
	if len(rec.Command) != len(want) {
		t.Fatalf("command = %v, want %d elements", rec.Command, len(want))
	}
	for i, w := range want {
		if i == 3 {
			continue // the bundle directory, asserted below
		}
		if rec.Command[i] != w {
			t.Errorf("command[%d] = %q, want %q", i, rec.Command[i], w)
		}
	}
	if !strings.HasPrefix(rec.Command[3], "<repo>") {
		t.Errorf("command[3] = %q, want it to start with <repo>", rec.Command[3])
	}
	if rec.ClaudeCLI != "2.1.263 (Claude Code)" {
		t.Errorf("claudeCli = %q", rec.ClaudeCLI)
	}

	// Two invocations, in this order: the version probe, then the validation itself.
	if len(cli.calls) != 2 || cli.calls[0][0] != "--version" || cli.calls[1][0] != "plugin" {
		t.Errorf("calls = %v", cli.calls)
	}
}

// TestClaudeCLIVersion_FailuresAreReported covers the two ways the version probe can fail. The
// string it returns is redacted by its caller, which TestRunHostValidation's leak assertion covers;
// what matters here is that neither failure is silently reported as a version.
func TestClaudeCLIVersion_FailuresAreReported(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  claudeRun
	}{
		{"could not be run", claudeRun{ExitCode: -1, Err: errors.New("permission denied")}},
		{"non-zero exit", claudeRun{ExitCode: 3, Stderr: []byte("unknown flag")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli := fakeCLI{version: tc.run}
			got := claudeCLIVersion(cli.probe(), "claude")
			if !strings.HasPrefix(got, "unknown (") {
				t.Errorf("claudeCLIVersion = %q, want an `unknown (...)` marker", got)
			}
		})
	}
}

// TestHostPathRedactor covers the one property that makes the host-validation record safe to
// commit: the CLI names the directory it was pointed at in every message, that directory lives
// under the developer's home, and the record is checked into a public repository. Both spellings
// must go — the plain one the CLI prints in prose and the backslash-doubled one it emits inside
// JSON — and a path under the home directory that is in neither the repository nor the output
// directory must go too.
func TestHostPathRedactor(t *testing.T) {
	const (
		home = `C:\Users\someone`
		repo = `C:\Users\someone\Projects\qompack`
		out  = `D:\scratch\bundles`
	)

	t.Run("bundle inside the repository", func(t *testing.T) {
		redact := hostPathRedactor(repo, repo+`\dist\bundle`, home)
		got := redact(`{"target":"C:\\Users\\someone\\Projects\\qompack\\dist\\bundle\\x\\plugin.json"}`)
		if strings.Contains(got, "someone") {
			t.Errorf("home directory survived redaction: %s", got)
		}
		if !strings.Contains(got, "<repo>") {
			t.Errorf("repository root was not labelled: %s", got)
		}
		if plain := redact(repo + `\dist`); plain != `<repo>\dist` {
			t.Errorf("plain spelling = %q, want %q", plain, `<repo>\dist`)
		}
	})

	t.Run("bundle outside the repository", func(t *testing.T) {
		redact := hostPathRedactor(repo, out, home)
		got := redact(out + `\qompack-plugin-v1-windows-amd64`)
		if strings.Contains(got, "scratch") {
			t.Errorf("out directory survived redaction: %s", got)
		}
		if !strings.HasPrefix(got, "<out>") {
			t.Errorf("out directory was not labelled: %s", got)
		}
	})

	t.Run("a home path in neither is still redacted", func(t *testing.T) {
		redact := hostPathRedactor(repo, repo+`\dist`, home)
		for _, in := range []string{
			home + `\.claude\settings.json`,
			`{"config":"C:\\Users\\someone\\.claude\\settings.json"}`,
		} {
			if got := redact(in); strings.Contains(got, "someone") {
				t.Errorf("redact(%q) = %q, home survived", in, got)
			}
		}
	})

	t.Run("nothing to redact leaves the text alone", func(t *testing.T) {
		redact := hostPathRedactor("", "", "")
		const s = `{"success":true}`
		if got := redact(s); got != s {
			t.Errorf("redact(%q) = %q", s, got)
		}
	})
}

// TestEvidencePathRefusesATrailingSeparator pins F2: --evidence names a file, so a path that
// ends in a separator is refused rather than written as a file named after the directory.
func TestEvidencePathRefusesATrailingSeparator(t *testing.T) {
	for _, p := range []string{"dist/evidence/", `dist\evidence\`} {
		err := rejectEvidenceDirectoryPath(p)
		if err == nil {
			t.Errorf("rejectEvidenceDirectoryPath(%q) = nil, want an error", p)
		} else if !strings.Contains(err.Error(), "path separator") {
			t.Errorf("rejectEvidenceDirectoryPath(%q) = %v, want it to name the separator", p, err)
		}
	}
	if err := rejectEvidenceDirectoryPath("dist/evidence/host-validation.json"); err != nil {
		t.Errorf("a file path must be accepted, got %v", err)
	}
	if err := rejectEvidenceDirectoryPath(""); err != nil {
		t.Errorf("the empty path (stdout) must be accepted, got %v", err)
	}
}
