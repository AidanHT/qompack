package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// An archive is the shippable spelling of a bundle, and it exists for exactly one reason: a
// GitHub release uploads files, not directories. packaging/README.md §1 explains why the bundle
// ITSELF is a directory — an archive interposes a compression format, a member order and a
// modification time between the bytes this repository produces and the bytes a host reads — so
// everything below is about removing those three interpositions again rather than accepting them.
//
// Every target ships a .zip, because a Claude Code marketplace `archive` source is a "Zip archive
// downloaded over HTTPS" (plugin-marketplaces, fetched 2026-09-22) and C7.5 publishes each target's
// archive as one. The four POSIX targets used to ship .tar.gz; a zip carries the same normalised
// Unix modes in its external attributes (creator host Unix), so bin/ stays 0755 in the archive.
// Whether Claude Code's own extractor restores that bit on linux/darwin is unverified until a
// published pre-release is installed on such a host (packaging/README.md §9).
//
// Member paths are bundle-RELATIVE: the archive root is the plugin root, exactly as the directory
// root is (packaging/README.md §1), so `claude --plugin-dir <dir>` finds `.claude-plugin/plugin.json`
// at the top — and "Claude Code looks for .claude-plugin/ at the top of the archive, then inside a
// single top-level folder", so the root layout is the one it tries first.
const (
	// zipDOSEpochDate is 1980-01-01 in MS-DOS date encoding ((year-1980)<<9 | month<<5 | day).
	// The zip format cannot express Unix 0, and a zeroed date field decodes to month 0 / day 0,
	// which some extractors reject. This is the earliest date the format has.
	zipDOSEpochDate = 1<<5 | 1
	zipDOSEpochTime = 0
	// archiveBinMode and archiveFileMode are the two modes an entry may carry. Normalised rather
	// than copied from disk because the assembling machine's umask is not part of the artifact:
	// Windows has no executable bit at all, so a bundle assembled there and one assembled on Linux
	// would otherwise ship different modes for the same file.
	archiveBinMode  = 0o755
	archiveFileMode = 0o644
)

// archiveName is the archive a target's bundle directory is packed into, beside it in --out. It is
// a .zip on every target; goos is kept so a future per-target format has one place to change.
func archiveName(dirName, _ string) string {
	return dirName + ".zip"
}

// removeStaleArchives deletes leftover *.zip, *.tar.gz, checksums.txt and marketplace.json under
// outDir so a previous version's archive — or a marketplace document pinning a previous version's
// digests — cannot survive into extra_files. *.tar.gz stays in the sweep because releases before
// C7.5 wrote them into the same directory.
func removeStaleArchives(outDir string) error {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == checksumsFileName || name == marketplaceFileName ||
			strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz") {
			if err := os.Remove(filepath.Join(outDir, name)); err != nil {
				return fmt.Errorf("removing stale %s: %w", name, err)
			}
		}
	}
	return nil
}

// archiveMember is one entry: its slash path inside the archive and the file it is read from.
type archiveMember struct {
	Name string
	Path string
	Mode os.FileMode
	Size int64
}

// archiveMembers lists every regular file under dir, sorted by slash path.
//
// Unlike hashBundleTree it includes BUNDLE.json and checksums.txt: those two describe the bundle's
// contents and so cannot appear in their own listing, but they are part of the bundle a user
// receives, and an archive that dropped them would ship a plugin with no identity document.
func archiveMembers(dir string) ([]archiveMember, error) {
	var out []archiveMember
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file; a bundle holds only regular files", p)
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		slash := filepath.ToSlash(rel)
		out = append(out, archiveMember{Name: slash, Path: p, Mode: archiveMode(slash), Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// archiveMode is the normalised mode for one member: executable under bin/, plain otherwise.
func archiveMode(slash string) os.FileMode {
	if strings.HasPrefix(slash, "bin/") {
		return archiveBinMode
	}
	return archiveFileMode
}

// writeArchive packs dir into a zip beside it and returns the archive's path.
func writeArchive(dir, goos string) (string, error) {
	members, err := archiveMembers(dir)
	if err != nil {
		return "", err
	}
	if len(members) == 0 {
		return "", fmt.Errorf("%s holds no files to archive", dir)
	}
	out := filepath.Join(filepath.Dir(dir), archiveName(filepath.Base(dir), goos))
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	err = writeZip(f, members)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(out)
		return "", fmt.Errorf("packing %s: %w", out, err)
	}
	return out, nil
}

// writeZip writes the members as a zip with DOS-epoch timestamps and no extra fields.
//
// h.Modified is left at its zero value on purpose: archive/zip emits an extended-timestamp extra
// field for any non-zero Modified, and that field would carry a real clock reading into an
// artifact whose whole point is that it carries none.
func writeZip(w io.Writer, members []archiveMember) error {
	zw := zip.NewWriter(w)
	for _, m := range members {
		h := &zip.FileHeader{
			Name:         m.Name,
			Method:       zip.Deflate,
			ModifiedDate: zipDOSEpochDate,
			ModifiedTime: zipDOSEpochTime,
		}
		h.SetMode(m.Mode)
		dst, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		if err := copyFileInto(dst, m.Path); err != nil {
			return err
		}
	}
	return zw.Close()
}

// copyFileInto streams one member's bytes into the archive writer.
func copyFileInto(dst io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(dst, f)
	return err
}

// writeArchiveChecksums writes outDir/checksums.txt over the archives, in `sha256sum` format with
// the archive's bare file name — so `sha256sum -c checksums.txt` verifies from inside dist/bundle.
//
// This is a SECOND checksums.txt and it is not a duplicate of the one inside each bundle: that one
// covers a bundle's contents, this one covers the archives themselves, which is what a downloader
// holds before extracting anything.
func writeArchiveChecksums(outDir string, archives []string) (string, error) {
	sort.Strings(archives)
	var b strings.Builder
	for _, a := range archives {
		sum, _, err := hashFile(a)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s  %s\n", sum, filepath.Base(a))
	}
	out := filepath.Join(outDir, checksumsFileName)
	if err := os.WriteFile(out, []byte(b.String()), bundleFilePerm); err != nil {
		return "", err
	}
	return out, nil
}
