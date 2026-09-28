package main

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assembleForArchive assembles one target into a fresh temp directory with the fixture builder and
// returns the bundle directory. Six real cross-compiles would measure the Go toolchain; what these
// tests measure is the packer.
func assembleForArchive(t *testing.T, tgt bundleTarget) string {
	t.Helper()
	out := t.TempDir()
	dir, _, err := testAssembly(t, out).assemble(tgt)
	if err != nil {
		t.Fatalf("assembling %v: %v", tgt, err)
	}
	return dir
}

// archiveEntries reads a zip back into name -> bytes, so the assertions below are about what a
// consumer would actually extract rather than about how it was written.
func archiveEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, f := range openArchive(t, path) {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %s in %s: %v", f.Name, path, err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("reading %s in %s: %v", f.Name, path, err)
		}
		out[f.Name] = b
	}
	return out
}

// openArchive returns a zip's central-directory entries; the reader is closed on test cleanup.
func openArchive(t *testing.T, path string) []*zip.File {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	t.Cleanup(func() { _ = zr.Close() })
	return zr.File
}

// TestArchiveName_EveryTargetIsZip is C7.5: a Claude Code marketplace `archive` source is "Zip
// archive downloaded over HTTPS" (plugin-marketplaces, fetched 2026-09-22), so every one of the six
// release targets ships a .zip — the four POSIX ones included, which used to ship .tar.gz.
func TestArchiveName_EveryTargetIsZip(t *testing.T) {
	for _, tgt := range releaseTargets {
		dirName := bundleDirName("0.3.0", bundleTarget{OS: tgt.GOOS, Arch: tgt.GOARCH})
		if got, want := archiveName(dirName, tgt.GOOS), dirName+".zip"; got != want {
			t.Errorf("archiveName(%s, %s) = %q, want %q", dirName, tgt.GOOS, got, want)
		}
	}
}

// TestWriteArchive_ZipCarriesUnixModesAndARootLayout pins what a host extracting the archive reads.
//
//   - Layout: "Claude Code looks for .claude-plugin/ at the top of the archive, then inside a single
//     top-level folder", so members sit at the root and .claude-plugin/plugin.json is a top-level
//     member path.
//   - Modes: bin/ is 0755 and everything else 0644, recorded as Unix external attributes (creator
//     host Unix) so an extractor that honours them — which is still unverified for Claude Code on
//     linux/darwin and is an owner action on a published pre-release — restores the executable bit.
func TestWriteArchive_ZipCarriesUnixModesAndARootLayout(t *testing.T) {
	for _, tgt := range []bundleTarget{{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"}} {
		t.Run(tgt.OS, func(t *testing.T) {
			path, err := writeArchive(assembleForArchive(t, tgt), tgt.OS)
			if err != nil {
				t.Fatalf("writeArchive: %v", err)
			}
			var sawManifest, sawBin bool
			for _, f := range openArchive(t, path) {
				if strings.Contains(f.Name, `\`) || strings.HasPrefix(f.Name, "/") {
					t.Errorf("%s: member %q is not a relative slash path", path, f.Name)
				}
				if f.CreatorVersion>>8 != 3 {
					t.Errorf("%s: %s records creator host %d, want 3 (Unix) so its mode is read", path, f.Name, f.CreatorVersion>>8)
				}
				want := os.FileMode(archiveFileMode)
				if strings.HasPrefix(f.Name, "bin/") {
					want = archiveBinMode
					sawBin = true
				}
				if got := f.Mode().Perm(); got != want {
					t.Errorf("%s: %s has mode %o, want %o", path, f.Name, got, want)
				}
				if f.Name == ".claude-plugin/plugin.json" {
					sawManifest = true
				}
			}
			if !sawManifest {
				t.Errorf("%s has no top-level .claude-plugin/plugin.json", path)
			}
			if !sawBin {
				t.Errorf("%s has no bin/ member", path)
			}
		})
	}
}

// TestWriteArchive_MembersMatchTheBundleDirectory is the packer's central claim: the archive and
// the directory hold the same files with the same bytes. Anything else means a release ships
// something the determinism evidence was never computed over.
func TestWriteArchive_MembersMatchTheBundleDirectory(t *testing.T) {
	for _, tgt := range []bundleTarget{{"linux", "amd64"}, {"windows", "amd64"}} {
		t.Run(tgt.OS, func(t *testing.T) {
			dir := assembleForArchive(t, tgt)
			path, err := writeArchive(dir, tgt.OS)
			if err != nil {
				t.Fatalf("writeArchive: %v", err)
			}
			if got, want := filepath.Base(path), archiveName(filepath.Base(dir), tgt.OS); got != want {
				t.Fatalf("archive name = %q, want %q", got, want)
			}

			entries := archiveEntries(t, path)
			members, err := archiveMembers(dir)
			if err != nil {
				t.Fatalf("archiveMembers: %v", err)
			}
			if len(entries) != len(members) {
				t.Fatalf("archive holds %d member(s), the directory holds %d", len(entries), len(members))
			}
			for _, m := range members {
				onDisk, readErr := os.ReadFile(m.Path)
				if readErr != nil {
					t.Fatalf("reading %s: %v", m.Path, readErr)
				}
				inArchive, ok := entries[m.Name]
				if !ok {
					t.Errorf("%s is in the bundle directory and not in the archive", m.Name)
					continue
				}
				if !bytes.Equal(onDisk, inArchive) {
					t.Errorf("%s: the archived bytes differ from the directory's", m.Name)
				}
			}
			for _, want := range []string{identityFileName, checksumsFileName} {
				if _, ok := entries[want]; !ok {
					t.Errorf("the archive omits %s; a bundle without its identity document is not the "+
						"bundle the evidence describes", want)
				}
			}
		})
	}
}

// TestWriteArchive_Deterministic packs two independent assemblies of the same source and compares
// the archives byte for byte.
//
// Two separate temp directories rather than one repacked twice: the directory's own file mtimes
// differ between the two assemblies, which is exactly the input the packer must not be reading.
func TestWriteArchive_Deterministic(t *testing.T) {
	for _, tgt := range []bundleTarget{{"linux", "arm64"}, {"windows", "arm64"}} {
		t.Run(tgt.OS, func(t *testing.T) {
			first, err := writeArchive(assembleForArchive(t, tgt), tgt.OS)
			if err != nil {
				t.Fatalf("first writeArchive: %v", err)
			}
			second, err := writeArchive(assembleForArchive(t, tgt), tgt.OS)
			if err != nil {
				t.Fatalf("second writeArchive: %v", err)
			}
			a, err := os.ReadFile(first)
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(second)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Errorf("two assemblies of the same source packed to different bytes (%d vs %d); "+
					"checksums.txt asserts they do not", len(a), len(b))
			}
		})
	}
}

// TestArchiveMode pins the two normalised modes. The bundled binary must stay executable after a
// round trip through an archive built on a machine with no executable bit at all.
func TestArchiveMode(t *testing.T) {
	for name, want := range map[string]os.FileMode{
		"bin/qompack":              archiveBinMode,
		"bin/qompack.exe":          archiveBinMode,
		"BUNDLE.json":              archiveFileMode,
		"commands/status.md":       archiveFileMode,
		".claude-plugin/plugin.js": archiveFileMode,
	} {
		if got := archiveMode(name); got != want {
			t.Errorf("archiveMode(%q) = %o, want %o", name, got, want)
		}
	}
}

// TestWriteArchiveChecksums covers the file a downloader verifies before extracting anything: bare
// archive names (so `sha256sum -c` works from inside dist/bundle) in sorted order.
func TestWriteArchiveChecksums(t *testing.T) {
	out := t.TempDir()
	var archives []string
	for _, tgt := range []bundleTarget{{"linux", "amd64"}, {"windows", "amd64"}} {
		dir, _, err := testAssembly(t, out).assemble(tgt)
		if err != nil {
			t.Fatalf("assembling %v: %v", tgt, err)
		}
		a, err := writeArchive(dir, tgt.OS)
		if err != nil {
			t.Fatalf("writeArchive: %v", err)
		}
		archives = append(archives, a)
	}
	p, err := writeArchiveChecksums(out, archives)
	if err != nil {
		t.Fatalf("writeArchiveChecksums: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	if len(lines) != len(archives) {
		t.Fatalf("checksums.txt has %d line(s) for %d archive(s)", len(lines), len(archives))
	}
	for _, line := range lines {
		parts := bytes.SplitN(line, []byte("  "), 2)
		if len(parts) != 2 || len(parts[0]) != 64 {
			t.Fatalf("line %q is not sha256sum format", line)
		}
		if bytes.ContainsAny(parts[1], `/\`) {
			t.Errorf("line %q names a path, not a bare archive name; `sha256sum -c` is run from "+
				"inside the directory holding them", line)
		}
	}
}

// TestArchiveAssembledBundles_RemovesStaleArchives is F9: a leftover archive and checksums.txt
// under --out disappear before the new pack, so extra_files cannot upload an unchecksummed file.
func TestArchiveAssembledBundles_RemovesStaleArchives(t *testing.T) {
	out := t.TempDir()
	staleNames := []string{
		"qompack-plugin-old-linux-amd64.tar.gz",
		"qompack-plugin-old-windows-amd64.zip",
		marketplaceFileName,
	}
	for _, name := range staleNames {
		if err := os.WriteFile(filepath.Join(out, name), []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(out, checksumsFileName), []byte("stale  old.zip\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tgt := bundleTarget{"linux", "amd64"}
	dir, _, err := testAssembly(t, out).assemble(tgt)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if err := archiveAssembledBundles(out, []bundleTarget{tgt}, map[bundleTarget]string{tgt: dir}); err != nil {
		t.Fatalf("archiveAssembledBundles: %v", err)
	}
	for _, name := range staleNames {
		if _, err := os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
			t.Errorf("stale %s still exists: %v", name, err)
		}
	}
	sums, err := os.ReadFile(filepath.Join(out, checksumsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sums, []byte("stale")) || bytes.Contains(sums, []byte("old-")) {
		t.Errorf("checksums.txt still names a stale archive:\n%s", sums)
	}
}
