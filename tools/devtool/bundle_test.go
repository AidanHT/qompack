package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// fixtureBuilder stands in for the real `go build`. Six cross-compiles inside a unit test would
// make the layout and identity assertions cost minutes and would measure the Go toolchain rather
// than the assembler; the one test that must prove the real build works is
// TestAssembleBundle_HostRealBuild below.
func fixtureBuilder(t *testing.T) bundleBuilder {
	t.Helper()
	return func(goos, goarch, out string) error {
		return os.WriteFile(out, []byte("fixture-binary "+goos+"/"+goarch+"\n"), 0o600)
	}
}

// testAssembly is the assembler every unit test here drives: a fixed version, a fixed source
// identity and a fixed toolchain string, so nothing in the produced bundle varies with the
// machine it runs on.
func testAssembly(t *testing.T, outDir string) bundleAssembly {
	t.Helper()
	return bundleAssembly{
		version:   "v9.9.9-testing",
		source:    bundleSource{Commit: "0123456789abcdef0123456789abcdef01234567", Dirty: false},
		goVersion: "go1.26.6",
		outDir:    outDir,
		build:     fixtureBuilder(t),
		legal:     fixtureLegalFiles(),
	}
}

// fixtureLegalFiles stands in for the repository's LICENSE and THIRD_PARTY_NOTICES.md, so the
// layout and determinism tests do not depend on the notices page's current bytes.
// TestReadBundleLegalFiles_ReadsTheRepositoryCopies checks the real files are what a run reads.
func fixtureLegalFiles() map[string][]byte {
	return map[string][]byte{
		"LICENSE":                []byte("fixture licence\n"),
		"THIRD_PARTY_NOTICES.md": []byte("# fixture notices\n"),
	}
}

// TestBundleVersion_Precedence pins the single version rule: an explicit --version wins, then
// `git describe`, then internal/core.Version's compiled default. The rule matters because the same
// string is stamped into three places at once — the -X ldflag, plugin.json and BUNDLE.json — and a
// bundle whose manifest and binary disagree about their own version is unfixable after the fact.
func TestBundleVersion_Precedence(t *testing.T) {
	for _, tc := range []struct{ name, override, described, compiled, want string }{
		{"override wins over both", "v3.0.0", "v2.0.0-1-gabc", "0.1.0", "v3.0.0"},
		{"describe wins over compiled", "", "v2.0.0-1-gabc", "0.1.0", "v2.0.0-1-gabc"},
		{"compiled is the last resort", "", "", "0.1.0", "0.1.0"},
		{"override wins with nothing else", "v3.0.0", "", "", "v3.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bundleVersion(tc.override, tc.described, tc.compiled); got != tc.want {
				t.Errorf("bundleVersion(%q, %q, %q) = %q, want %q",
					tc.override, tc.described, tc.compiled, got, tc.want)
			}
		})
	}
}

// TestBundleDirName pins the per-target directory name, including the Windows targets, which do
// not get a suffix of their own: the platform shows up as the -windows-amd64 segment, and only the
// binary inside carries .exe.
func TestBundleDirName(t *testing.T) {
	for _, tc := range []struct {
		tgt  bundleTarget
		want string
	}{
		{bundleTarget{"linux", "amd64"}, "qompack-plugin-v1.2.3-linux-amd64"},
		{bundleTarget{"darwin", "arm64"}, "qompack-plugin-v1.2.3-darwin-arm64"},
		{bundleTarget{"windows", "amd64"}, "qompack-plugin-v1.2.3-windows-amd64"},
	} {
		if got := bundleDirName("v1.2.3", tc.tgt); got != tc.want {
			t.Errorf("bundleDirName(v1.2.3, %v) = %q, want %q", tc.tgt, got, tc.want)
		}
	}
}

// TestAssembleBundle_Layout asserts the bundle holds exactly the contract's file set for a POSIX
// and a Windows target: the generated plugin tree with no "plugin/" directory left in the middle
// of it, the per-target binary under bin/, and the two identity files. "Exactly" is the assertion
// that matters — an extra file nobody declared is a file no checksum line covers.
func TestAssembleBundle_Layout(t *testing.T) {
	for _, tc := range []struct {
		tgt     bundleTarget
		wantBin string
	}{
		{bundleTarget{"linux", "arm64"}, "bin/qompack"},
		{bundleTarget{"windows", "amd64"}, "bin/qompack.exe"},
	} {
		t.Run(tc.tgt.OS+"-"+tc.tgt.Arch, func(t *testing.T) {
			asm := testAssembly(t, t.TempDir())
			dir, _, err := asm.assemble(tc.tgt)
			if err != nil {
				t.Fatalf("assemble: %v", err)
			}

			want := []string{
				".claude-plugin/plugin.json",
				".mcp.json",
				"BUNDLE.json",
				"LICENSE",
				"THIRD_PARTY_NOTICES.md",
				"checksums.txt",
				tc.wantBin,
				"commands/dropped.md",
				"commands/eval.md",
				"commands/pin.md",
				"commands/recall.md",
				"commands/status.md",
				"commands/why.md",
				"hooks/hooks.json",
			}
			sort.Strings(want)
			got := listBundleTree(t, dir)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("bundle tree:\n got %v\nwant %v", got, want)
			}

			// Every hook and the MCP server launch EXACTLY the executable this bundle ships, in
			// exec form (C1.11): `command` is ${CLAUDE_PLUGIN_ROOT}/<the binary>, the subcommand is
			// `args`, and there is no shell to resolve an extension or split a path.
			wantCmd := "${CLAUDE_PLUGIN_ROOT}/" + tc.wantBin
			var hooks struct {
				Hooks map[string][]struct {
					Hooks []struct {
						Command string   `json:"command"`
						Args    []string `json:"args"`
					} `json:"hooks"`
				} `json:"hooks"`
			}
			readBundleJSON(t, dir, "hooks/hooks.json", &hooks)
			if len(hooks.Hooks) != 7 {
				t.Errorf("hooks.json declares %d events, want 7", len(hooks.Hooks))
			}
			for event, groups := range hooks.Hooks {
				for _, g := range groups {
					for _, h := range g.Hooks {
						if h.Command != wantCmd {
							t.Errorf("hooks.json %s: command %q, want the bundled binary %q", event, h.Command, wantCmd)
						}
						if len(h.Args) == 0 {
							t.Errorf("hooks.json %s: no args, so the host would run it as a shell command", event)
						}
					}
				}
			}
			var mcpDoc struct {
				MCPServers map[string]struct {
					Command string   `json:"command"`
					Args    []string `json:"args"`
				} `json:"mcpServers"`
			}
			readBundleJSON(t, dir, ".mcp.json", &mcpDoc)
			if srv := mcpDoc.MCPServers["qompack"]; srv.Command != wantCmd || len(srv.Args) == 0 {
				t.Errorf(".mcp.json qompack server = %+v, want command %q with args", srv, wantCmd)
			}

			// plugin.json carries the assembler's version, not internal/core.Version's default.
			pj, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(".claude-plugin/plugin.json")))
			if err != nil {
				t.Fatalf("read plugin.json: %v", err)
			}
			var plugin struct{ Version string }
			if err := json.Unmarshal(pj, &plugin); err != nil {
				t.Fatalf("plugin.json: %v", err)
			}
			if plugin.Version != asm.version {
				t.Errorf("plugin.json version = %q, want %q", plugin.Version, asm.version)
			}
		})
	}
}

// TestAssembleBundle_Identity checks every BUNDLE.json field against what was actually written:
// the file list must cover the whole tree except the two identity files, each hash must be the
// real sha256 of the bytes on disk, and the paths must be sorted and slash-separated so a Windows
// assembly and a Linux one produce the same document.
func TestAssembleBundle_Identity(t *testing.T) {
	asm := testAssembly(t, t.TempDir())
	tgt := bundleTarget{"windows", "arm64"}
	dir, id, err := asm.assemble(tgt)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	if id.Name != "qompack" {
		t.Errorf("name = %q, want qompack", id.Name)
	}
	if id.Version != asm.version {
		t.Errorf("version = %q, want %q", id.Version, asm.version)
	}
	if id.Target != tgt {
		t.Errorf("target = %v, want %v", id.Target, tgt)
	}
	if id.Go != asm.goVersion {
		t.Errorf("go = %q, want %q", id.Go, asm.goVersion)
	}
	if id.Source != asm.source {
		t.Errorf("source = %v, want %v", id.Source, asm.source)
	}

	// The identity files describe the bundle, so they cannot describe themselves.
	var paths []string
	for _, f := range id.Files {
		paths = append(paths, f.Path)
		if strings.Contains(f.Path, `\`) {
			t.Errorf("path %q is not slash-separated", f.Path)
		}
		full := filepath.Join(dir, filepath.FromSlash(f.Path))
		b, readErr := os.ReadFile(full)
		if readErr != nil {
			t.Fatalf("BUNDLE.json lists %s, which is not in the bundle: %v", f.Path, readErr)
		}
		sum := sha256.Sum256(b)
		if want := hex.EncodeToString(sum[:]); f.SHA256 != want {
			t.Errorf("%s: sha256 = %q, want %q", f.Path, f.SHA256, want)
		}
		if f.Bytes != int64(len(b)) {
			t.Errorf("%s: bytes = %d, want %d", f.Path, f.Bytes, len(b))
		}
	}
	if !sort.StringsAreSorted(paths) {
		t.Errorf("BUNDLE.json files are not sorted: %v", paths)
	}
	for _, self := range []string{"BUNDLE.json", "checksums.txt"} {
		for _, p := range paths {
			if p == self {
				t.Errorf("BUNDLE.json lists %s, which it must exclude", self)
			}
		}
	}

	// Everything else in the tree must be covered.
	listed := make(map[string]bool, len(paths))
	for _, p := range paths {
		listed[p] = true
	}
	for _, p := range listBundleTree(t, dir) {
		if p == "BUNDLE.json" || p == "checksums.txt" {
			continue
		}
		if !listed[p] {
			t.Errorf("%s is in the bundle but absent from BUNDLE.json", p)
		}
	}

	// No timestamp anywhere in the bundle's own documents.
	raw, err := os.ReadFile(filepath.Join(dir, "BUNDLE.json"))
	if err != nil {
		t.Fatalf("read BUNDLE.json: %v", err)
	}
	for _, banned := range []string{"time", "date", "built", "timestamp"} {
		if strings.Contains(strings.ToLower(string(raw)), banned) {
			t.Errorf("BUNDLE.json mentions %q; the identity must carry no clock reading:\n%s", banned, raw)
		}
	}
}

// TestAssembleBundle_ChecksumsFormat pins the `sha256sum` spelling — "<hex><space><space><path>",
// LF-terminated — because the file exists to be checkable by the tool of that name on a machine
// that has never seen this repository.
func TestAssembleBundle_ChecksumsFormat(t *testing.T) {
	asm := testAssembly(t, t.TempDir())
	dir, id, err := asm.assemble(bundleTarget{"linux", "amd64"})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		t.Fatalf("read checksums.txt: %v", err)
	}
	text := string(raw)
	if strings.Contains(text, "\r") {
		t.Error("checksums.txt contains CR; the bundle is LF-only on every target")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != len(id.Files) {
		t.Fatalf("checksums.txt has %d line(s), BUNDLE.json lists %d file(s)", len(lines), len(id.Files))
	}
	for i, line := range lines {
		want := id.Files[i].SHA256 + "  " + id.Files[i].Path
		if line != want {
			t.Errorf("line %d = %q, want %q", i+1, line, want)
		}
	}
	if !strings.HasSuffix(text, "\n") {
		t.Error("checksums.txt does not end in a newline")
	}
}

// TestAssembleBundle_Deterministic is the contract's whole point: the same source at the same
// version assembled twice must produce byte-identical identity documents. It assembles into two
// separate temp directories rather than twice into one, so a stale file left behind by the first
// run cannot make the second one look reproducible.
func TestAssembleBundle_Deterministic(t *testing.T) {
	tgt := bundleTarget{"darwin", "amd64"}

	read := func() (bundleJSON, checksums []byte) {
		t.Helper()
		asm := testAssembly(t, t.TempDir())
		dir, _, err := asm.assemble(tgt)
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "BUNDLE.json"))
		if err != nil {
			t.Fatalf("read BUNDLE.json: %v", err)
		}
		c, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
		if err != nil {
			t.Fatalf("read checksums.txt: %v", err)
		}
		return b, c
	}

	firstJSON, firstSums := read()
	secondJSON, secondSums := read()
	if string(firstJSON) != string(secondJSON) {
		t.Errorf("BUNDLE.json differs between assemblies:\n%s\n---\n%s", firstJSON, secondJSON)
	}
	if string(firstSums) != string(secondSums) {
		t.Errorf("checksums.txt differs between assemblies:\n%s\n---\n%s", firstSums, secondSums)
	}
}

// TestResolveBundleTargets covers both halves of --target: no flag means all six release targets,
// and anything outside that set is refused rather than quietly built. A seventh target would be a
// platform nobody has evidence for, which is exactly what the release matrix exists to bound.
func TestResolveBundleTargets(t *testing.T) {
	all, err := resolveBundleTargets(nil)
	if err != nil {
		t.Fatalf("resolveBundleTargets(nil): %v", err)
	}
	if len(all) != len(releaseTargets) {
		t.Errorf("default target set has %d entries, want %d", len(all), len(releaseTargets))
	}
	for i, tgt := range all {
		if tgt.OS != releaseTargets[i].GOOS || tgt.Arch != releaseTargets[i].GOARCH {
			t.Errorf("target %d = %v, want %s/%s", i, tgt, releaseTargets[i].GOOS, releaseTargets[i].GOARCH)
		}
	}

	one, err := resolveBundleTargets([]string{"linux/arm64"})
	if err != nil {
		t.Fatalf("resolveBundleTargets(linux/arm64): %v", err)
	}
	if len(one) != 1 || one[0] != (bundleTarget{"linux", "arm64"}) {
		t.Errorf("resolveBundleTargets(linux/arm64) = %v", one)
	}

	for _, bad := range []string{"plan9/amd64", "linux/riscv64", "linux", "linux/amd64/extra", ""} {
		if got, err := resolveBundleTargets([]string{bad}); err == nil {
			t.Errorf("resolveBundleTargets(%q) = %v, want an error", bad, got)
		}
	}

	// A target named twice is assembled once: the second pass would clear and rebuild the first's
	// directory for no gain.
	dup, err := resolveBundleTargets([]string{"linux/amd64", "windows/arm64", "linux/amd64"})
	if err != nil {
		t.Fatalf("resolveBundleTargets(duplicates): %v", err)
	}
	if len(dup) != 2 || dup[0] != (bundleTarget{"linux", "amd64"}) || dup[1] != (bundleTarget{"windows", "arm64"}) {
		t.Errorf("resolveBundleTargets(duplicates) = %v, want the two distinct targets in order", dup)
	}
}

// TestAssembleBundle_HostRealBuild is the one test that compiles for real: everything above
// substitutes a fixture binary, which proves the assembler and proves nothing about the flag list
// it hands `go build`. Exactly one host-target compile, so the cost is a single build rather than
// six cross-compiles.
func TestAssembleBundle_HostRealBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("platform: -short skips the real `go build` for the host target")
	}

	prev := root
	root = testModuleRoot(t)
	t.Cleanup(func() { root = prev })

	const version = "v0.0.0-hostbuildtest"
	asm := bundleAssembly{
		version:   version,
		source:    bundleSource{Commit: "deadbeef", Dirty: true},
		goVersion: runtime.Version(),
		outDir:    t.TempDir(),
		legal:     mustReadBundleLegalFiles(t),
		build: func(goos, goarch, out string) error {
			return goBuildRelease(goos, goarch, out, versionLdflags(version))
		},
	}

	tgt := bundleTarget{runtime.GOOS, runtime.GOARCH}
	dir, id, err := asm.assemble(tgt)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	binRel := "bin/qompack" + exeSuffix(tgt.OS)
	fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(binRel)))
	if err != nil {
		t.Fatalf("stat %s: %v", binRel, err)
	}
	if fi.Size() == 0 {
		t.Fatalf("%s is empty", binRel)
	}

	var found bool
	for _, f := range id.Files {
		if f.Path == binRel {
			found = true
			if f.Bytes != fi.Size() {
				t.Errorf("BUNDLE.json records %d bytes for %s, the file is %d", f.Bytes, binRel, fi.Size())
			}
		}
	}
	if !found {
		t.Errorf("BUNDLE.json does not list %s; files: %v", binRel, id.Files)
	}
}

// TestVersionLdflags covers the three properties the bundle depends on: the version reaches
// internal/core.Version, the build id is cleared so two builds of the same source agree, and an
// empty version stamps nothing rather than stamping an empty string.
func TestVersionLdflags(t *testing.T) {
	got := versionLdflags("v1.2.3")
	for _, want := range []string{"-s", "-w", "-buildid=", "-X " + modulePath + "/internal/core.Version=v1.2.3"} {
		if !strings.Contains(got, want) {
			t.Errorf("versionLdflags(v1.2.3) = %q, missing %q", got, want)
		}
	}
	if empty := versionLdflags(""); strings.Contains(empty, "-X ") {
		t.Errorf("versionLdflags(\"\") = %q, want no -X stamp", empty)
	} else if !strings.Contains(empty, "-buildid=") {
		t.Errorf("versionLdflags(\"\") = %q, missing -buildid=", empty)
	}
}

// listBundleTree lists every file under dir as a sorted slash-separated relative path.
func listBundleTree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(out)
	return out
}

// TestGoBuildArgs pins the argv and environment of the one `go build` invocation `build`,
// `build-all` and `bundle` all go through.
//
// The flags were previously unassertable: they lived inside goBuildRelease's call, so deleting
// -trimpath or -buildvcs=false left the whole suite green while packaging/README.md §4 and every
// BUNDLE.json went on asserting reproducibility. TestAssembleBundle_Deterministic cannot catch it
// either — it substitutes a fixture binary, so it proves byte-identity of the metadata and of a
// constant stand-in, not of a compiled artifact.
func TestGoBuildArgs(t *testing.T) {
	t.Run("argv", func(t *testing.T) {
		out := filepath.Join("dist", "qompack")
		const ldflags = "-s -w -buildid= -X example.com/x/internal/core.Version=v1.2.3"
		want := []string{
			"build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", out, "./cmd/qompack",
		}
		got := goBuildArgs(out, ldflags)
		if len(got) != len(want) {
			t.Fatalf("goBuildArgs = %q, want %q", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("goBuildArgs[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("env", func(t *testing.T) {
		host := goBuildEnv("", "")
		if host["CGO_ENABLED"] != "0" {
			t.Errorf("host CGO_ENABLED = %q, want 0", host["CGO_ENABLED"])
		}
		// A host build must leave GOOS/GOARCH inherited, so `go env` decides what "host" means.
		for _, k := range []string{"GOOS", "GOARCH"} {
			if v, ok := host[k]; ok {
				t.Errorf("host build sets %s=%q; it must be inherited", k, v)
			}
		}

		cross := goBuildEnv("darwin", "arm64")
		for k, want := range map[string]string{"CGO_ENABLED": "0", "GOOS": "darwin", "GOARCH": "arm64"} {
			if cross[k] != want {
				t.Errorf("cross build %s = %q, want %q", k, cross[k], want)
			}
		}
		if len(cross) != 3 {
			t.Errorf("cross build env = %v, want exactly CGO_ENABLED/GOOS/GOARCH", cross)
		}
	})
}

// TestValidateBundleVersion guards the one string that reaches both the directory name and the
// os.RemoveAll that clears it. `--version ../../x` would escape --out entirely and delete something
// the caller never named.
func TestValidateBundleVersion(t *testing.T) {
	for _, ok := range []string{"v1.2.3", "0.1.0", "v0.2.0-602-gacbb0f2-dirty", "v1.2.3+build.5"} {
		if err := validateBundleVersion(ok); err != nil {
			t.Errorf("validateBundleVersion(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "../../x", "a/b", `a\b`, "/abs", `C:\x`} {
		if err := validateBundleVersion(bad); err == nil {
			t.Errorf("validateBundleVersion(%q) = nil, want an error", bad)
		}
	}
}

// TestAssembleBundle_RefusesEscapingVersion checks the guard at the line that actually matters:
// assemble points os.RemoveAll at the directory the version names, so the refusal has to happen
// there and not only at the flag.
func TestAssembleBundle_RefusesEscapingVersion(t *testing.T) {
	outDir := t.TempDir()
	sentinel := filepath.Join(filepath.Dir(outDir), "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("do not delete"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	asm := testAssembly(t, outDir)
	asm.version = ".." + string(filepath.Separator) + ".."
	if _, _, err := asm.assemble(bundleTarget{"linux", "amd64"}); err == nil {
		t.Fatal("assemble accepted a version containing a path separator")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("assemble removed something outside --out: %v", err)
	}
}

// TestTaskBundle_RejectsPositionalArgs: `devtool bundle windows/amd64` used to silently assemble
// all six targets, which reads as the tool ignoring what it was asked for.
func TestTaskBundle_RejectsPositionalArgs(t *testing.T) {
	err := taskBundle([]string{"windows/amd64"})
	if err == nil {
		t.Fatal("taskBundle accepted a positional target")
	}
	if !errors.Is(err, errUsage) {
		t.Errorf("taskBundle error = %v, want a usage error", err)
	}
	if !strings.Contains(err.Error(), "--target") {
		t.Errorf("taskBundle error = %v, want it to name --target", err)
	}
}

// readBundleJSON decodes one bundle-relative JSON file.
func readBundleJSON(t *testing.T, dir, rel string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v\n%s", rel, err, b)
	}
}
