package main

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/pluginmanifest"
)

// renderedHooksJSON is hooks.json as internal/pluginmanifest renders it for goos, with the async
// option on or off.
func renderedHooksJSON(t *testing.T, version, goos string, async bool) string {
	t.Helper()
	m := pluginmanifest.ForTarget(version, goos)
	if async {
		m = pluginmanifest.WithAsyncHooks(m)
	}
	files, err := m.Files()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return string(files["plugin/hooks/hooks.json"])
}

func readBundleFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestAssembleBundle_AsyncHooks: with asyncHooks the bundle ships the WithAsyncHooks rendering of
// hooks.json; without it, today's ForTarget bytes, with no async key anywhere.
func TestAssembleBundle_AsyncHooks(t *testing.T) {
	for _, tgt := range []bundleTarget{{"linux", "amd64"}, {"windows", "amd64"}} {
		t.Run(tgt.OS, func(t *testing.T) {
			off := testAssembly(t, t.TempDir())
			offDir, _, err := off.assemble(tgt)
			if err != nil {
				t.Fatalf("assemble (off): %v", err)
			}
			got := readBundleFile(t, offDir, "hooks/hooks.json")
			if want := renderedHooksJSON(t, off.version, tgt.OS, false); got != want {
				t.Errorf("default hooks.json:\n got %s\nwant %s", got, want)
			}
			if strings.Contains(got, `"async"`) {
				t.Errorf("default hooks.json carries an async key:\n%s", got)
			}

			on := testAssembly(t, t.TempDir())
			on.asyncHooks = true
			onDir, _, err := on.assemble(tgt)
			if err != nil {
				t.Fatalf("assemble (on): %v", err)
			}
			got = readBundleFile(t, onDir, "hooks/hooks.json")
			if want := renderedHooksJSON(t, on.version, tgt.OS, true); got != want {
				t.Errorf("async hooks.json:\n got %s\nwant %s", got, want)
			}
			if n := strings.Count(got, `"async": true`); n != 3 {
				t.Errorf("async hooks.json marks %d entries async, want 3:\n%s", n, got)
			}
			// Only hooks.json differs between the two bundles.
			for _, rel := range listBundleTree(t, offDir) {
				if rel == "hooks/hooks.json" || rel == identityFileName || rel == checksumsFileName {
					continue
				}
				if readBundleFile(t, offDir, rel) != readBundleFile(t, onDir, rel) {
					t.Errorf("%s differs between the default and the async bundle", rel)
				}
			}
		})
	}
}

// TestTaskBundle_AsyncHooksFlag drives the real flag path: `--async-hooks` renders the option-on
// hooks.json, and its absence renders today's bytes. The build step is the fixture builder, so the
// test measures the flag wiring rather than the Go toolchain.
func TestTaskBundle_AsyncHooksFlag(t *testing.T) {
	prevRoot, prevBuild := root, goBuildReleaseFn
	root = testModuleRoot(t)
	goBuildReleaseFn = func(goos, goarch, out, _ string) error {
		return fixtureBuilder(t)(goos, goarch, out)
	}
	t.Cleanup(func() { root, goBuildReleaseFn = prevRoot, prevBuild })

	const version = "v9.9.9-testing"
	tgt := bundleTarget{"linux", "amd64"}
	for _, tc := range []struct {
		name  string
		extra []string
		async bool
	}{
		{"flag absent", nil, false},
		{"flag present", []string{"--async-hooks"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir()
			args := append([]string{"--version", version, "--target", tgt.OS + "/" + tgt.Arch, "--out", out}, tc.extra...)
			if err := taskBundle(args); err != nil {
				t.Fatalf("taskBundle %v: %v", args, err)
			}
			got := readBundleFile(t, filepath.Join(out, bundleDirName(version, tgt)), "hooks/hooks.json")
			if want := renderedHooksJSON(t, version, tgt.OS, tc.async); got != want {
				t.Errorf("hooks.json:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// renderedTreeHooksJSON is hooks.json as internal/pluginmanifest renders it for goos with the given
// async transforms applied, WithAsyncHooks first.
func renderedTreeHooksJSON(t *testing.T, version, goos string, asyncHooks, asyncPrompt bool) string {
	t.Helper()
	m := pluginmanifest.ForTarget(version, goos)
	if asyncHooks {
		m = pluginmanifest.WithAsyncHooks(m)
	}
	if asyncPrompt {
		m = pluginmanifest.WithAsyncPrompt(m)
	}
	files, err := m.Files()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return string(files["plugin/hooks/hooks.json"])
}

// TestAssembleBundle_AsyncPrompt: with asyncPrompt the bundle ships the WithAsyncPrompt rendering
// of hooks.json (UserPromptSubmit alone async); with asyncHooks as well, the union of the two sets.
// Only hooks.json differs from the default bundle.
func TestAssembleBundle_AsyncPrompt(t *testing.T) {
	for _, tgt := range []bundleTarget{{"linux", "amd64"}, {"windows", "amd64"}} {
		t.Run(tgt.OS, func(t *testing.T) {
			off := testAssembly(t, t.TempDir())
			offDir, _, err := off.assemble(tgt)
			if err != nil {
				t.Fatalf("assemble (off): %v", err)
			}
			for _, tc := range []struct {
				name                    string
				asyncHooks, asyncPrompt bool
				wantAsync               int
			}{
				{"prompt", false, true, 1},
				{"hooks and prompt", true, true, 4},
			} {
				asm := testAssembly(t, t.TempDir())
				asm.asyncHooks, asm.asyncPrompt = tc.asyncHooks, tc.asyncPrompt
				dir, _, err := asm.assemble(tgt)
				if err != nil {
					t.Fatalf("assemble (%s): %v", tc.name, err)
				}
				got := readBundleFile(t, dir, "hooks/hooks.json")
				if want := renderedTreeHooksJSON(t, asm.version, tgt.OS, tc.asyncHooks, tc.asyncPrompt); got != want {
					t.Errorf("%s hooks.json:\n got %s\nwant %s", tc.name, got, want)
				}
				if n := strings.Count(got, `"async": true`); n != tc.wantAsync {
					t.Errorf("%s hooks.json marks %d entries async, want %d:\n%s", tc.name, n, tc.wantAsync, got)
				}
				for _, rel := range listBundleTree(t, offDir) {
					if rel == "hooks/hooks.json" || rel == identityFileName || rel == checksumsFileName {
						continue
					}
					if readBundleFile(t, offDir, rel) != readBundleFile(t, dir, rel) {
						t.Errorf("%s: %s differs between the default and the async bundle", tc.name, rel)
					}
				}
			}
		})
	}
}

// TestTaskBundle_AsyncPromptFlag drives the real flag path: `--async-prompt` renders the
// WithAsyncPrompt hooks.json, alone and beside `--async-hooks`, and its absence leaves today's
// bytes. The build step is the fixture builder, as in TestTaskBundle_AsyncHooksFlag.
func TestTaskBundle_AsyncPromptFlag(t *testing.T) {
	prevRoot, prevBuild := root, goBuildReleaseFn
	root = testModuleRoot(t)
	goBuildReleaseFn = func(goos, goarch, out, _ string) error {
		return fixtureBuilder(t)(goos, goarch, out)
	}
	t.Cleanup(func() { root, goBuildReleaseFn = prevRoot, prevBuild })

	const version = "v9.9.9-testing"
	tgt := bundleTarget{"linux", "amd64"}
	for _, tc := range []struct {
		name                    string
		extra                   []string
		asyncHooks, asyncPrompt bool
	}{
		{"flag absent", nil, false, false},
		{"async-prompt", []string{"--async-prompt"}, false, true},
		{"both flags", []string{"--async-prompt", "--async-hooks"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir()
			args := append([]string{"--version", version, "--target", tgt.OS + "/" + tgt.Arch, "--out", out}, tc.extra...)
			if err := taskBundle(args); err != nil {
				t.Fatalf("taskBundle %v: %v", args, err)
			}
			got := readBundleFile(t, filepath.Join(out, bundleDirName(version, tgt)), "hooks/hooks.json")
			if want := renderedTreeHooksJSON(t, version, tgt.OS, tc.asyncHooks, tc.asyncPrompt); got != want {
				t.Errorf("hooks.json:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// TestTaskBundle_ModsWithAsyncHooks: the flags combine. `--mods --async-hooks` ships the hooks
// module (hooks.json names ./register.ts, the mod sources sit beside it) and marks exactly
// PostToolUse, Stop and SubagentStop async; adding `--async-prompt` marks UserPromptSubmit as well.
// Every other event stays synchronous. The build step is the fixture builder.
func TestTaskBundle_ModsWithAsyncHooks(t *testing.T) {
	prevRoot, prevBuild := root, goBuildReleaseFn
	root = testModuleRoot(t)
	goBuildReleaseFn = func(goos, goarch, out, _ string) error {
		return fixtureBuilder(t)(goos, goarch, out)
	}
	t.Cleanup(func() { root, goBuildReleaseFn = prevRoot, prevBuild })

	const version = "v9.9.9-testing"
	tgt := bundleTarget{"linux", "amd64"}
	for _, tc := range []struct {
		name      string
		extra     []string
		wantAsync []string
	}{
		{"mods and async-hooks", []string{"--mods", "--async-hooks"},
			[]string{"PostToolUse", "Stop", "SubagentStop"}},
		{"mods, async-hooks and async-prompt", []string{"--mods", "--async-hooks", "--async-prompt"},
			[]string{"PostToolUse", "Stop", "SubagentStop", "UserPromptSubmit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir()
			args := append([]string{"--version", version, "--target", tgt.OS + "/" + tgt.Arch, "--out", out}, tc.extra...)
			if err := taskBundle(args); err != nil {
				t.Fatalf("taskBundle %v: %v", args, err)
			}
			dir := filepath.Join(out, bundleDirName(version, tgt))

			tree := make(map[string]bool)
			for _, rel := range listBundleTree(t, dir) {
				tree[rel] = true
			}
			for _, rel := range modBundleSources {
				if !tree[rel] {
					t.Errorf("bundle is missing mod source %s", rel)
				}
			}

			var doc struct {
				Modules []string `json:"modules"`
				Hooks   map[string][]struct {
					Hooks []map[string]any `json:"hooks"`
				} `json:"hooks"`
			}
			readBundleJSON(t, dir, "hooks/hooks.json", &doc)
			if len(doc.Modules) != 1 || doc.Modules[0] != pluginmanifest.ModuleEntry {
				t.Errorf("hooks.json modules = %v, want [%s]", doc.Modules, pluginmanifest.ModuleEntry)
			}
			if len(doc.Hooks) != 7 {
				t.Errorf("hooks.json has %d events, want 7", len(doc.Hooks))
			}
			var gotAsync []string
			for event, groups := range doc.Hooks {
				for _, g := range groups {
					for _, h := range g.Hooks {
						if v, has := h["async"]; has {
							if v != true {
								t.Errorf("%s: async = %v, want true", event, v)
							}
							gotAsync = append(gotAsync, event)
						}
					}
				}
			}
			sort.Strings(gotAsync)
			if !slices.Equal(gotAsync, tc.wantAsync) {
				t.Errorf("async events = %v, want %v (every other event synchronous)", gotAsync, tc.wantAsync)
			}
		})
	}
}
