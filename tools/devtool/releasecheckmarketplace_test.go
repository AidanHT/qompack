package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaude is a claudeProbe whose `plugin validate` answers with success.
func fakeClaude(success bool, code int) claudeProbe {
	return claudeProbe{
		lookPath: func() (string, error) { return "claude", nil },
		run: func(_ string, args ...string) claudeRun {
			if len(args) == 1 && args[0] == "--version" {
				return claudeRun{Stdout: []byte("2.1.280 (Claude Code)\n")}
			}
			return claudeRun{Stdout: []byte(fmt.Sprintf(`{"success":%t}`, success)), ExitCode: code}
		},
	}
}

// TestReleaseCheckMarketplace covers every verdict the release gate can reach: host accepted, CLI
// absent (a pass that says host validation was NOT run), host rejected, and a committed document
// that is invalid.
func TestReleaseCheckMarketplace(t *testing.T) {
	o := releaseCheckOptions{Tag: "v0.3.0"}
	emptyRepo := t.TempDir()

	if got := releaseCheckMarketplaceWith(o, fakeClaude(true, 0), emptyRepo); got.Status != rcPass ||
		!strings.Contains(got.Detail, "accepted") {
		t.Errorf("accepted by the host: %+v", got)
	}
	absent := claudeProbe{lookPath: func() (string, error) { return "", errors.New("not on PATH") }}
	if got := releaseCheckMarketplaceWith(o, absent, emptyRepo); got.Status != rcPass ||
		!strings.Contains(got.Detail, "NOT run") {
		t.Errorf("CLI absent must pass and say host validation was NOT run: %+v", got)
	}
	if got := releaseCheckMarketplaceWith(o, fakeClaude(false, 1), emptyRepo); got.Status != rcFail {
		t.Errorf("rejected by the host must fail: %+v", got)
	}

	badRepo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(badRepo, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badRepo, filepath.FromSlash(marketplaceCommittedPath)),
		[]byte(`{"name":"qompack","owner":{"name":"x"},"description":"d","plugins":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := releaseCheckMarketplaceWith(o, absent, badRepo); got.Status != rcFail ||
		!strings.Contains(got.Detail, "committed") {
		t.Errorf("an invalid committed marketplace must fail the gate: %+v", got)
	}
}
