package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syntheticSums returns a checksums map naming the six archives of version, each with a distinct
// digest derived from its name.
func syntheticSums(version string) map[string]string {
	out := map[string]string{}
	for _, t := range releaseTargets {
		name := archiveName(bundleDirName(version, bundleTarget{OS: t.GOOS, Arch: t.GOARCH}), t.GOOS)
		sum := sha256.Sum256([]byte(name))
		out[name] = hex.EncodeToString(sum[:])
	}
	return out
}

func TestParseArchiveChecksums(t *testing.T) {
	a := strings.Repeat("a", 64)
	b := strings.Repeat("B", 64)
	got, err := parseArchiveChecksums([]byte(a + "  one.zip\r\n" + b + " *two.zip\n\n"))
	if err != nil {
		t.Fatalf("parseArchiveChecksums: %v", err)
	}
	if got["one.zip"] != a || got["two.zip"] != strings.ToLower(b) || len(got) != 2 {
		t.Errorf("parsed %v", got)
	}
	for name, bad := range map[string]string{
		"short digest":  "abc  one.zip\n",
		"one space":     a + " one.zip\n",
		"path":          a + "  dist/one.zip\n",
		"windows path":  a + `  dist\one.zip` + "\n",
		"listed twice":  a + "  one.zip\n" + a + "  one.zip\n",
		"not hex":       strings.Repeat("g", 64) + "  one.zip\n",
		"missing name":  a + "  \n",
		"trailing junk": "garbage\n",
	} {
		if _, err := parseArchiveChecksums([]byte(bad)); err == nil {
			t.Errorf("%s: parsed %q without error", name, bad)
		}
	}
}

// TestBuildMarketplace_SixPinnedEntries pins the adopted design: six archive entries named
// qompack-<os>-<arch>, each pinned by sha256 to that target's GitHub Release zip, no version on
// any entry, and a document the validator accepts.
func TestBuildMarketplace_SixPinnedEntries(t *testing.T) {
	sums := syntheticSums("0.3.0")
	doc, err := buildMarketplace("v0.3.0", marketplaceRepoDefault, sums)
	if err != nil {
		t.Fatalf("buildMarketplace: %v", err)
	}
	if len(doc.Plugins) != 6 {
		t.Fatalf("%d entries, want 6", len(doc.Plugins))
	}
	for i, tgt := range releaseTargets {
		e := doc.Plugins[i]
		asset := fmt.Sprintf("qompack-plugin-0.3.0-%s-%s.zip", tgt.GOOS, tgt.GOARCH)
		if want := "qompack-" + tgt.GOOS + "-" + tgt.GOARCH; e.Name != want {
			t.Errorf("entry %d name %q, want %q", i, e.Name, want)
		}
		if want := "https://github.com/AidanHT/qompack/releases/download/v0.3.0/" + asset; e.Source.URL != want {
			t.Errorf("%s url %q, want %q", e.Name, e.Source.URL, want)
		}
		if e.Source.Source != "archive" || e.Source.SHA256 != sums[asset] {
			t.Errorf("%s source %+v, want archive pinned to %s", e.Name, e.Source, sums[asset])
		}
	}

	raw, err := marshalBundleJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"version"`) {
		t.Errorf("an entry carries a version; plugin.json is the one place it lives:\n%s", raw)
	}
	version, err := validateMarketplace(raw, marketplaceRepoDefault)
	if err != nil {
		t.Fatalf("the generated document fails validation: %v\n%s", err, raw)
	}
	if version != "0.3.0" {
		t.Errorf("validated version %q, want 0.3.0", version)
	}
	again, _ := buildMarketplace("v0.3.0", marketplaceRepoDefault, sums)
	raw2, _ := marshalBundleJSON(again)
	if string(raw) != string(raw2) {
		t.Error("two generations from the same checksums differ")
	}
}

func TestBuildMarketplace_RefusesMissingStaleOrBadTag(t *testing.T) {
	missing := syntheticSums("0.3.0")
	delete(missing, "qompack-plugin-0.3.0-darwin-arm64.zip")
	if _, err := buildMarketplace("v0.3.0", marketplaceRepoDefault, missing); err == nil {
		t.Error("a checksums file missing one target produced a marketplace")
	}

	stale := syntheticSums("0.3.0")
	stale["qompack-plugin-0.2.0-linux-amd64.zip"] = strings.Repeat("c", 64)
	if _, err := buildMarketplace("v0.3.0", marketplaceRepoDefault, stale); err == nil {
		t.Error("a stale archive in the checksums was silently ignored")
	}

	if _, err := buildMarketplace("v0.3.0", marketplaceRepoDefault, syntheticSums("0.2.9")); err == nil {
		t.Error("archives of another version were pinned under v0.3.0")
	}
	for _, tag := range []string{"0.3.0", "v", "v../x", ""} {
		if _, err := buildMarketplace(tag, marketplaceRepoDefault, syntheticSums("0.3.0")); err == nil {
			t.Errorf("tag %q was accepted", tag)
		}
	}
}

// TestValidateMarketplace_Rejects mutates a valid document one way at a time.
func TestValidateMarketplace_Rejects(t *testing.T) {
	doc, err := buildMarketplace("v0.3.0", marketplaceRepoDefault, syntheticSums("0.3.0"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := marshalBundleJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(f func(m map[string]any)) []byte {
		var m map[string]any
		if err := json.Unmarshal(base, &m); err != nil {
			t.Fatal(err)
		}
		f(m)
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	entry := func(m map[string]any, i int) map[string]any {
		return m["plugins"].([]any)[i].(map[string]any)
	}
	src := func(m map[string]any, i int) map[string]any { return entry(m, i)["source"].(map[string]any) }

	for name, raw := range map[string][]byte{
		"entry sets version":   mutate(func(m map[string]any) { entry(m, 0)["version"] = "0.3.0" }),
		"entry relaxes strict": mutate(func(m map[string]any) { entry(m, 1)["strict"] = false }),
		"no description":       mutate(func(m map[string]any) { delete(m, "description") }),
		"five entries":         mutate(func(m map[string]any) { m["plugins"] = m["plugins"].([]any)[:5] }),
		"renamed entry":        mutate(func(m map[string]any) { entry(m, 2)["name"] = "qompack" }),
		"http url": mutate(func(m map[string]any) {
			src(m, 0)["url"] = strings.Replace(src(m, 0)["url"].(string), "https://", "http://", 1)
		}),
		"foreign asset": mutate(func(m map[string]any) {
			src(m, 0)["url"] = src(m, 1)["url"] // entry 0 pinned to entry 1's target
		}),
		"two versions": mutate(func(m map[string]any) {
			src(m, 3)["url"] = strings.ReplaceAll(src(m, 3)["url"].(string), "0.3.0", "0.3.1")
		}),
		"upper-case digest": mutate(func(m map[string]any) { src(m, 4)["sha256"] = strings.Repeat("A", 64) }),
		"git source":        mutate(func(m map[string]any) { src(m, 5)["source"] = "github" }),
		"wrong name":        mutate(func(m map[string]any) { m["name"] = "claude-plugins-official" }),
	} {
		if _, err := validateMarketplace(raw, marketplaceRepoDefault); err == nil {
			t.Errorf("%s: validated\n%s", name, raw)
		}
	}
}

// TestMarketplace_PinsTheArchivesTheBundlerWrote is the seam C7.5 depends on: assemble all six
// targets, pack them, write checksums.txt, generate the marketplace from it, and confirm every
// entry pins the sha256 of the zip actually on disk.
func TestMarketplace_PinsTheArchivesTheBundlerWrote(t *testing.T) {
	out := t.TempDir()
	asm := testAssembly(t, out)
	asm.version = "0.3.0"
	tgts := make([]bundleTarget, 0, len(releaseTargets))
	dirs := map[bundleTarget]string{}
	for _, rt := range releaseTargets {
		tgt := bundleTarget{OS: rt.GOOS, Arch: rt.GOARCH}
		dir, _, err := asm.assemble(tgt)
		if err != nil {
			t.Fatalf("assemble %v: %v", tgt, err)
		}
		tgts = append(tgts, tgt)
		dirs[tgt] = dir
	}
	if err := archiveAssembledBundles(out, tgts, dirs); err != nil {
		t.Fatalf("archiveAssembledBundles: %v", err)
	}

	sumsPath := filepath.Join(out, checksumsFileName)
	mpPath := filepath.Join(out, marketplaceFileName)
	if err := taskMarketplace([]string{"--tag", "v0.3.0", "--checksums", sumsPath, "--out", mpPath}); err != nil {
		t.Fatalf("devtool marketplace: %v", err)
	}
	raw, err := os.ReadFile(mpPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc marketplaceDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, e := range doc.Plugins {
		asset := e.Source.URL[strings.LastIndex(e.Source.URL, "/")+1:]
		sum, _, err := hashFile(filepath.Join(out, asset))
		if err != nil {
			t.Fatalf("%s: the pinned archive %s is not the one on disk: %v", e.Name, asset, err)
		}
		if sum != e.Source.SHA256 {
			t.Errorf("%s pins %s but %s hashes to %s", e.Name, e.Source.SHA256, asset, sum)
		}
	}

	// --check agrees with what was written, and notices an edit.
	if err := taskMarketplace([]string{"--tag", "v0.3.0", "--checksums", sumsPath, "--out", mpPath, "--check"}); err != nil {
		t.Errorf("--check on the freshly generated file: %v", err)
	}
	if err := taskMarketplace([]string{"--validate", mpPath}); err != nil {
		t.Errorf("--validate: %v", err)
	}
	edited := strings.Replace(string(raw), doc.Plugins[0].Source.SHA256, strings.Repeat("0", 64), 1)
	if err := os.WriteFile(mpPath, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := taskMarketplace([]string{"--tag", "v0.3.0", "--checksums", sumsPath, "--out", mpPath, "--check"}); err == nil {
		t.Error("--check accepted a hand-edited digest")
	}

	// A rerun of bundle --archive sweeps the stale marketplace with the stale archives.
	if err := removeStaleArchives(out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mpPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a stale %s survived the sweep: %v", marketplaceFileName, err)
	}
}

func TestTaskMarketplace_Usage(t *testing.T) {
	for _, args := range [][]string{
		{},                     // no --tag
		{"--tag", "v1", "pos"}, // positional
		{"--nope"},
	} {
		if err := taskMarketplace(args); !errors.Is(err, errUsage) {
			t.Errorf("taskMarketplace(%q) = %v, want a usage error", args, err)
		}
	}
}
