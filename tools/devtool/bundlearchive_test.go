package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
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

// archiveEntries reads an archive back into name -> bytes, so the assertions below are about what
// a consumer would actually extract rather than about how it was written.
func archiveEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	if filepath.Ext(path) == ".zip" {
		zr, err := zip.OpenReader(path)
		if err != nil {
			t.Fatalf("opening %s: %v", path, err)
		}
		defer func() { _ = zr.Close() }()
		for _, f := range zr.File {
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
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gunzipping %s: %v", path, err)
	}
	defer func() { _ = gz.Close() }()
	if gz.Name != "" || !gz.ModTime.IsZero() {
		t.Errorf("%s: gzip header carries name %q and mtime %v; both must be empty so the stream "+
			"does not record the machine that wrote it", path, gz.Name, gz.ModTime)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("reading %s in %s: %v", h.Name, path, err)
		}
		if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" {
			t.Errorf("%s: %s carries uid/gid %d/%d (%q/%q); a shipped archive names no accounts",
				path, h.Name, h.Uid, h.Gid, h.Uname, h.Gname)
		}
		if h.ModTime.Unix() != archiveEpoch {
			t.Errorf("%s: %s carries mtime %v, not the fixed epoch", path, h.Name, h.ModTime)
		}
		out[h.Name] = b
	}
	return out
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
		"commands/checkpoint.md":   archiveFileMode,
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
