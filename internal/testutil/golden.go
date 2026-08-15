package testutil

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// updateFlagName is the -update flag every golden test in this repository shares: `go test ./...
// -update` rewrites every golden file instead of comparing against it.
const updateFlagName = "update"

// update registers -update, or adopts an already-registered one.
//
// The adoption half matters. A few packages that predate testutil declare their own
// `var update = flag.Bool("update", …)` in a _test.go file; the moment such a package's test
// binary also links testutil, two registrations of the same flag name panic the binary at
// initialization with "flag redefined". Looking the flag up first turns that collision into the
// thing both sides actually wanted — one -update flag that drives every golden in the binary.
var update = registerUpdateFlag()

// registerUpdateFlag returns a reader for the -update flag's current value, registering the flag
// if nothing else has. It returns a func rather than the *bool flag.Bool hands back, because an
// already-registered flag exposes its value only through the flag.Value interface.
func registerUpdateFlag() func() bool {
	if f := flag.Lookup(updateFlagName); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	p := flag.Bool(updateFlagName, false, "rewrite golden files under testdata/golden/ instead of comparing against them")
	return func() bool { return *p }
}

// goldenPerm is the mode a rewritten golden file is created with: an ordinary readable data file,
// deliberately not the 0o600 the runtime store uses, because goldens are committed source.
const goldenPerm = 0o644

// goldenDirPerm is the mode Golden creates a missing testdata/golden/<pkg>/ directory with.
const goldenDirPerm = 0o755

// Golden compares got against testdata/golden/<pkg>/<name>, where <pkg> is the directory name of
// the calling test's own package, and fails the test on any difference. With -update it rewrites
// the file instead and reports success.
//
// Comparison normalizes CRLF to LF on BOTH sides before diffing. That is not cosmetic: the repo's
// .gitattributes marks *.golden as -text, but a golden with any other extension — .json, .jsonl,
// .txt — is subject to `* text=auto eol=lf`, and a Windows checkout can still hand a tool CRLF
// bytes. Without normalization every such golden would fail on the primary development platform
// for a reason that has nothing to do with the code under test. The file itself is always written
// with LF, so an -update run on Windows produces the same bytes an -update run on Linux does.
func Golden(t *testing.T, name string, got []byte) {
	t.Helper()
	goldenAt(t, callerPkg(2), name, got)
}

// GoldenJSON marshals v as indented JSON with HTML escaping disabled — matching every other JSON
// writer in this codebase, so a "<" in a path or a diff appears literally in the golden rather
// than as < — and compares it through Golden.
func GoldenJSON(t *testing.T, name string, v any) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("testutil.GoldenJSON: encoding %s: %v", name, err)
	}
	goldenAt(t, callerPkg(2), name, buf.Bytes())
}

// goldenAt is the shared body of Golden and GoldenJSON, taking the package directory explicitly
// so the exported wrappers can each resolve their own caller.
func goldenAt(t *testing.T, pkg, name string, got []byte) {
	t.Helper()

	root, err := repoRoot()
	if err != nil {
		t.Fatalf("testutil: locating the repository root for golden %s/%s: %v", pkg, name, err)
	}
	dir := filepath.Join(root, "testdata", "golden", pkg)
	p := filepath.Join(dir, name)

	gotLF := toLF(got)

	if update() {
		if err := os.MkdirAll(filepath.Dir(p), goldenDirPerm); err != nil {
			t.Fatalf("testutil: creating golden directory %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, gotLF, goldenPerm); err != nil {
			t.Fatalf("testutil: rewriting golden %s: %v", p, err)
		}
		return
	}

	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("testutil: golden %s is missing (%v); rerun with -update to create it", p, err)
	}
	if goldenEqual(want, got) {
		return
	}
	t.Errorf("golden %s does not match (-want +got):\n%s", p, cmp.Diff(string(toLF(want)), string(gotLF)))
}

// goldenEqual is the comparison itself, factored out so it can be tested directly: two byte
// slices are equal for golden purposes when they are equal after every CRLF collapses to a bare
// LF.
func goldenEqual(want, got []byte) bool {
	return bytes.Equal(toLF(want), toLF(got))
}

// toLF returns b with every CRLF collapsed to a bare LF. It is applied to both sides of every
// golden comparison and to the bytes written by -update.
func toLF(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// callerPkg returns the directory name of the source file `skip` frames up the stack, which for a
// Go test is the name of the package under test: internal/config's tests resolve to "config" and
// so read testdata/golden/config/. skip is counted from callerPkg's own frame, so an exported
// helper that calls it directly passes 2.
func callerPkg(skip int) string {
	_, file, _, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	return filepath.Base(filepath.Dir(file))
}

// repoRoot returns the repository root: the nearest ancestor of the current working directory
// that contains a go.mod. Every test runs with its own package directory as the working
// directory, so this resolves the same absolute path no matter which package is under test —
// which is what lets testdata/golden/ and testdata/golden/contracts/ be single, shared trees at
// the root rather than duplicated per package.
func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; ; {
		if fi, statErr := os.Stat(filepath.Join(d, "go.mod")); statErr == nil && fi.Mode().IsRegular() {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no go.mod found in %s or any parent directory", wd)
		}
		d = parent
	}
}
