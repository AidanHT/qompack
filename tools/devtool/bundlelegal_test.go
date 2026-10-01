package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every bundle and every release zip ships the repository's LICENSE and THIRD_PARTY_NOTICES.md
// (V6 close-out audit F4, D53(e)). The binary statically links go-winio (MIT), klauspost/compress
// (BSD-3-Clause with Apache-2.0 and MIT parts), golang.org/x/sys (BSD-3-Clause) and the Go runtime
// (BSD-3-Clause), and BSD-3-Clause's binary-form condition needs the notices in the distribution
// itself. goreleaser uploads only the zips, checksums.txt and marketplace.json, so a notice that
// is not inside the zip is a notice no user receives.

// mustReadBundleLegalFiles reads the repository's two legal files the way a real run does.
func mustReadBundleLegalFiles(t *testing.T) map[string][]byte {
	t.Helper()
	legal, err := readBundleLegalFiles(testModuleRoot(t))
	if err != nil {
		t.Fatalf("readBundleLegalFiles: %v", err)
	}
	return legal
}

// TestReadBundleLegalFiles_ReadsTheRepositoryCopies pins what a real run ships: exactly LICENSE
// and THIRD_PARTY_NOTICES.md, byte for byte the repository's copies (LF, as .gitattributes checks
// them out).
func TestReadBundleLegalFiles_ReadsTheRepositoryCopies(t *testing.T) {
	repo := testModuleRoot(t)
	legal := mustReadBundleLegalFiles(t)
	if len(legal) != len(bundleLegalFiles) {
		t.Fatalf("read %d legal file(s), want %d: %v", len(legal), len(bundleLegalFiles), legal)
	}
	for _, name := range []string{"LICENSE", noticesFileName} {
		got, ok := legal[name]
		if !ok {
			t.Fatalf("readBundleLegalFiles does not return %s", name)
		}
		want, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
		if !bytes.Equal(got, want) {
			t.Errorf("%s: the bundle copy differs from the repository's", name)
		}
	}
	if !strings.Contains(string(legal[noticesFileName]), "### The Go runtime and standard library") {
		t.Errorf("the notices a bundle ships lack Go's licence section")
	}
}

// TestReadBundleLegalFiles_MissingFileIsAnError: a checkout without either file must not produce a
// bundle that silently lacks it.
func TestReadBundleLegalFiles_MissingFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("MIT\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBundleLegalFiles(dir); err == nil || !strings.Contains(err.Error(), noticesFileName) {
		t.Errorf("readBundleLegalFiles without %s = %v, want an error naming it", noticesFileName, err)
	}
	if err := os.WriteFile(filepath.Join(dir, noticesFileName), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBundleLegalFiles(dir); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("readBundleLegalFiles with an empty %s = %v, want an error", noticesFileName, err)
	}
}

// TestAssembleBundle_ShipsTheLicenceAndNotices: the two files sit at the bundle root, carry the
// bytes the assembly was given, and are hashed into BUNDLE.json like every other shipped file.
func TestAssembleBundle_ShipsTheLicenceAndNotices(t *testing.T) {
	asm := testAssembly(t, t.TempDir())
	dir, id, err := asm.assemble(bundleTarget{"windows", "amd64"})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	listed := map[string]bool{}
	for _, f := range id.Files {
		listed[f.Path] = true
	}
	for name, want := range asm.legal {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("the bundle has no %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
		if !listed[name] {
			t.Errorf("BUNDLE.json does not list %s", name)
		}
	}
}

// TestAssembleBundle_RefusesWithoutLegalFiles: an assembly missing either file fails rather than
// producing a bundle without it.
func TestAssembleBundle_RefusesWithoutLegalFiles(t *testing.T) {
	for _, drop := range []string{"LICENSE", noticesFileName} {
		asm := testAssembly(t, t.TempDir())
		delete(asm.legal, drop)
		if _, _, err := asm.assemble(bundleTarget{"linux", "amd64"}); err == nil || !strings.Contains(err.Error(), drop) {
			t.Errorf("assemble without %s = %v, want an error naming it", drop, err)
		}
	}
}

// TestWriteArchive_CarriesTheLicenceAndNotices is the half a user receives: the release zip, the
// only per-target file goreleaser uploads, holds both files at its root with their bytes.
func TestWriteArchive_CarriesTheLicenceAndNotices(t *testing.T) {
	for _, tgt := range []bundleTarget{{"windows", "amd64"}, {"linux", "amd64"}} {
		dir := assembleForArchive(t, tgt)
		zipPath, err := writeArchive(dir, tgt.OS)
		if err != nil {
			t.Fatalf("writeArchive %v: %v", tgt, err)
		}
		entries := archiveEntries(t, zipPath)
		for name, want := range fixtureLegalFiles() {
			got, ok := entries[name]
			if !ok {
				t.Errorf("%s: the zip has no %s at its root", filepath.Base(zipPath), name)
				continue
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s: %s differs from the bundle's", filepath.Base(zipPath), name)
			}
		}
	}
}
