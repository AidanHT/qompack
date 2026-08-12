package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// modulePath is this repository's own module path (go.mod's `module` line).
const modulePath = "github.com/qompack/qompack"

// pinnedModfile is the -modfile argument every pinned external tool is invoked with, from the
// repository root (00-ARCHITECTURE.md §2.6): `go run -modfile=tools/pinned/go.mod <import/path>`.
const pinnedModfile = "tools/pinned/go.mod"

// Import paths of the four pinned tool commands (tools/pinned/tools.go blank-imports the same
// four).
const (
	gofumptPkg      = "mvdan.cc/gofumpt"
	golangciLintPkg = "github.com/golangci/golangci-lint/cmd/golangci-lint"
	govulncheckPkg  = "golang.org/x/vuln/cmd/govulncheck"
	benchstatPkg    = "golang.org/x/perf/cmd/benchstat"
)

// findModuleRoot resolves the directory containing this repository's go.mod via `go env GOMOD`,
// which is correct regardless of the directory devtool happened to be invoked from.
func findModuleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	p := strings.TrimSpace(string(out))
	if p == "" || p == os.DevNull || strings.EqualFold(filepath.Base(p), "nul") {
		return "", errors.New("devtool must be run with `go run ./tools/devtool <task>` from inside the qompack module")
	}
	return filepath.Dir(p), nil
}

// runInherit runs name with args, streaming its stdin/stdout/stderr straight through to
// devtool's own — the right choice for anything a human is meant to read as it happens (gofumpt,
// golangci-lint, go build/test, …). env overrides/extends the current process environment; a nil
// or empty map leaves the environment untouched.
func runInherit(env map[string]string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = mergeEnv(env)
	return cmd.Run()
}

// runCapture runs name with args and captures stdout/stderr separately, for callers that need to
// parse the output rather than show it verbatim.
func runCapture(env map[string]string, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Env = mergeEnv(env)
	err = cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}

// mergeEnv returns os.Environ() with extra's keys overridden (or added).
func mergeEnv(extra map[string]string) []string {
	base := os.Environ()
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		key, _, found := strings.Cut(kv, "=")
		if found {
			if _, override := extra[key]; override {
				continue
			}
		}
		out = append(out, kv)
	}
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}

// goInherit runs `go <args...>` from root with inherited stdio.
func goInherit(args ...string) error { return runInherit(nil, "go", args...) }

// goInheritEnv runs `go <args...>` from root with inherited stdio and the given environment
// overrides (e.g. CGO_ENABLED, GOOS, GOARCH).
func goInheritEnv(env map[string]string, args ...string) error {
	return runInherit(env, "go", args...)
}

// pinnedArgs builds the argv for `go run -modfile=tools/pinned/go.mod <pkg> <extra...>`.
func pinnedArgs(pkg string, extra ...string) []string {
	args := []string{"run", "-modfile=" + pinnedModfile, pkg}
	return append(args, extra...)
}

// pinnedRunInherit runs a pinned tool with inherited stdio.
func pinnedRunInherit(pkg string, extra ...string) error {
	return runInherit(nil, "go", pinnedArgs(pkg, extra...)...)
}

// pinnedCapture runs a pinned tool and captures its output.
func pinnedCapture(pkg string, extra ...string) (stdout, stderr []byte, err error) {
	return runCapture(nil, "go", pinnedArgs(pkg, extra...)...)
}

// dirExists reports whether p exists and is a directory.
func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// dirHasGoFiles reports whether dir exists and directly contains at least one *.go file. It is
// how every task that depends on a package another subplan has not written yet ("the subject
// doesn't exist yet") tells absence from presence, without ever importing that package.
func dirHasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

// exeSuffix returns ".exe" for goos == "windows" and "" otherwise.
func exeSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// resolveVersion returns the version string burned into internal/core.Version at link time: the
// output of `git describe`, or a fixed development fallback when that is unavailable (a shallow
// checkout with no tags, or no git at all). git describe is read-only and never mutates the repo.
func resolveVersion() string {
	// --tags without --always: on a repo with no tags this FAILS rather than falling back to the
	// short SHA, which is what we want. An untagged tree has no release identity, so stamping it
	// with a commit hash would make `qompack version` report something that is not a version and
	// would contradict the version the plugin manifest advertises. Returning "" means "do not
	// stamp", leaving internal/core.Version's compiled-in default in place.
	out, _, err := runCapture(nil, "git", "describe", "--tags", "--dirty")
	if err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v
		}
	}
	return ""
}

// versionLdflags builds the -ldflags value shared by the build and build-all tasks. An empty
// version omits the -X entirely, so the binary reports internal/core.Version's compiled-in value.
func versionLdflags(version string) string {
	if version == "" {
		return "-s -w"
	}
	return "-s -w -X " + modulePath + "/internal/core.Version=" + version
}
