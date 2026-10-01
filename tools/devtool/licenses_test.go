package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShippedModulesEqualTheAllowlistIntersection is the claim the notices page rests on: the
// modules it reproduces are exactly the ones bindeps permits AND `go list -deps` actually pulls.
//
// It runs the real toolchain because that is the only thing that can answer the second half. Six
// `go list -deps` invocations against an already-warm build cache is the same work bindeps does on
// every `devtool lint` run.
func TestShippedModulesEqualTheAllowlistIntersection(t *testing.T) {
	withRepoRoot(t)

	got, err := shippedModulePaths()
	if err != nil {
		t.Fatalf("shippedModulePaths: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no shipped module found; the derivation is broken and the notices page would be empty")
	}
	for _, p := range got {
		if _, ok := shippedLicenseID[p]; !ok {
			t.Errorf("%s reaches the binary and has no licence classification; a released artifact "+
				"may not carry an unclassified dependency", p)
		}
		if allowedBinDep(p) {
			continue
		}
		// The module PATH is not always an import path (golang.org/x/sys is the module, and only
		// golang.org/x/sys/windows is the allowed import), so a module whose own path is not
		// allowed must have at least one allowed import path beneath it.
		if !allowedBinDep(p + "/windows") {
			t.Errorf("%s is reached by the binary and is not permitted by allowedBinDep; bindeps "+
				"and licenses disagree about what ships", p)
		}
	}
	for p := range shippedLicenseID {
		found := false
		for _, g := range got {
			if g == p {
				found = true
			}
		}
		if !found {
			t.Errorf("shippedLicenseID classifies %s, which no release target actually pulls; the "+
				"notices page would claim a redistribution that does not happen", p)
		}
	}
	t.Logf("shipped modules: %v", got)
}

// withRepoRoot points the package-level `root` at this repository for the duration of one test,
// the way run() does for a real invocation.
func withRepoRoot(t *testing.T) {
	t.Helper()
	r, err := findModuleRoot()
	if err != nil {
		t.Fatalf("findModuleRoot: %v", err)
	}
	prev := root
	root = r
	t.Cleanup(func() { root = prev })
}

// TestModuleLicenseTexts_RendersAFakeModule covers the collector without the module cache: a
// directory with a LICENSE renders, and a file that merely mentions licensing does not.
func TestModuleLicenseTexts_RendersAFakeModule(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("LICENSE", "MIT License\r\n\r\nCopyright (c) 2026 Nobody\r\n")
	write("sub/COPYING", "GPL-ish text\n")
	write("licensing.md", "prose about licences\n")
	write("testdata/LICENSE", "a fixture, not a licence this module is under\n")

	texts, err := moduleLicenseTexts(dir)
	if err != nil {
		t.Fatalf("moduleLicenseTexts: %v", err)
	}
	var names []string
	for _, tx := range texts {
		names = append(names, tx.Rel)
	}
	want := []string{"LICENSE", "sub/COPYING"}
	if len(names) != len(want) {
		t.Fatalf("collected %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("collected[%d] = %q, want %q", i, names[i], want[i])
		}
	}
	if strings.Contains(texts[0].Body, "\r") {
		t.Error("CRLF must be normalised, or the page differs between a Windows and a Linux run")
	}
}

// TestRenderNotices_DetectsAChangedVersion is `--check`'s reason for existing, isolated: a bumped
// dependency changes the rendered page, so a stale committed page cannot survive the gate.
func TestRenderNotices_DetectsAChangedVersion(t *testing.T) {
	mods := []licenseModule{{
		Path: "example.com/dep", Version: "v1.0.0", ID: "MIT",
		Texts: []licenseText{{Rel: "LICENSE", Body: "MIT License\n"}},
	}}
	tooling := []toolingRow{{Path: "example.com/tool", Version: "v2.0.0", ID: "MPL-2.0", Note: "test only, not shipped"}}

	before := renderNotices("MIT License\n", goLicense{Toolchain: "go1.0.0", Body: "Go\n"}, mods, tooling)
	mods[0].Version = "v1.0.1"
	after := renderNotices("MIT License\n", goLicense{Toolchain: "go1.0.0", Body: "Go\n"}, mods, tooling)

	if before == after {
		t.Fatal("a dependency version bump did not change the rendered page, so --check could never detect one")
	}
	for _, want := range []string{"example.com/dep", "v1.0.1", "MPL-2.0", "test only, not shipped"} {
		if !strings.Contains(after, want) {
			t.Errorf("the rendered page omits %q", want)
		}
	}
}

// TestGoModVersions reads the repository's own go.mod, so the parser is checked against the file
// it is actually pointed at rather than a fixture that could drift from it.
func TestGoModVersions(t *testing.T) {
	withRepoRoot(t)

	versions, err := goModVersions(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("goModVersions: %v", err)
	}
	for _, direct := range []string{"github.com/klauspost/compress", "pgregory.net/rapid"} {
		if versions["direct:"+direct] == "" {
			t.Errorf("%s must be parsed as a direct requirement", direct)
		}
	}
	if versions["gopkg.in/yaml.v3"] == "" {
		t.Error("an indirect requirement must still be parsed, so its version can be reported")
	}
	if versions["direct:gopkg.in/yaml.v3"] != "" {
		t.Error("an `// indirect` requirement must not be recorded as direct; the closing table " +
			"lists direct dependencies only")
	}
}

// TestNotices_CarryTheGoLicence is audit F4's second half (V6 close-out D53(e)). The Go runtime and
// standard library are compiled into every released binary, so the binary redistributes them and
// Go's BSD-3-Clause licence asks for its notice in that binary form. The committed page used to
// say the standard library "is not redistributed by this repository", which was false. The page
// must reproduce the LICENSE of the toolchain the repository builds with, and must not repeat the
// false statement.
func TestNotices_CarryTheGoLicence(t *testing.T) {
	withRepoRoot(t)

	page, err := os.ReadFile(filepath.Join(root, noticesFileName))
	if err != nil {
		t.Fatalf("read %s: %v", noticesFileName, err)
	}
	text := strings.ReplaceAll(string(page), "\r\n", "\n")

	goLicense, err := goToolchainLicense()
	if err != nil {
		t.Fatalf("goToolchainLicense: %v", err)
	}
	if !strings.Contains(goLicense.Body, "The Go Authors") {
		t.Fatalf("GOROOT's LICENSE does not name The Go Authors; read the wrong file?\n%s", goLicense.Body)
	}
	// The text is looked for inside the Go section, not anywhere on the page: golang.org/x/sys
	// carries a LICENSE with the same words, so a page-wide search would pass without the section.
	var indented strings.Builder
	writeIndented(&indented, goLicense.Body)
	section := strings.Index(text, "### The Go runtime and standard library")
	if section < 0 || !strings.Contains(text[section:], indented.String()) {
		t.Errorf("%s does not reproduce the Go distribution's LICENSE, which every binary carries in "+
			"its runtime and standard library; run `devtool licenses --write`", noticesFileName)
	}
	if strings.Contains(text, "is not redistributed by this") {
		t.Errorf("%s still says the Go standard library is not redistributed; it is compiled into "+
			"every binary", noticesFileName)
	}
}

// TestRenderNotices_GoLicenceIsARedistributedSection pins where the Go licence sits: in §2, among
// what the binary carries, with the toolchain named, and not in §3's not-redistributed list.
func TestRenderNotices_GoLicenceIsARedistributedSection(t *testing.T) {
	goLic := goLicense{Toolchain: "go1.99.9", Body: "Copyright 2009 The Go Authors.\n\nBSD text\n"}
	page := renderNotices("MIT License\n", goLic, nil, nil)

	sec2 := strings.Index(page, "## 2. Redistributed dependencies")
	sec3 := strings.Index(page, "## 3. Build- and test-time dependencies")
	goSec := strings.Index(page, "### The Go runtime and standard library")
	if sec2 < 0 || sec3 < 0 || goSec < 0 {
		t.Fatalf("the page lacks a section; §2 %d, §3 %d, Go %d:\n%s", sec2, sec3, goSec, page)
	}
	if goSec < sec2 || goSec > sec3 {
		t.Errorf("the Go licence must sit inside §2 (redistributed), at %d, not between %d and %d", goSec, sec2, sec3)
	}
	for _, want := range []string{"go1.99.9", "    Copyright 2009 The Go Authors.", "    BSD text", "BSD-3-Clause"} {
		if !strings.Contains(page, want) {
			t.Errorf("the rendered page omits %q", want)
		}
	}
	if !strings.Contains(page, "None: the binary links no third-party module.") {
		t.Errorf("with no shipped module the page must still say so")
	}
}
