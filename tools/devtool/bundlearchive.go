package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// An archive is the shippable spelling of a bundle, and it exists for exactly one reason: a
// GitHub release uploads files, not directories. packaging/README.md §1 explains why the bundle
// ITSELF is a directory — an archive interposes a compression format, a member order and a
// modification time between the bytes this repository produces and the bytes a host reads — so
// everything below is about removing those three interpositions again rather than accepting them.
//
// Member paths are bundle-RELATIVE: the archive root is the plugin root, exactly as the directory
// root is (packaging/README.md §1), so `claude --plugin-dir <dir>` finds `.claude-plugin/plugin.json`
// at the top. An archive that wrapped its contents in one more directory would need the host to
// strip a level nobody documented.
const (
	// archiveEpoch is the modification time every member carries. Unix 0 rather than the file's
	// own mtime: a real mtime is the single largest source of two archives of the same tree
	// disagreeing, and nothing downstream reads it — BUNDLE.json carries the provenance.
	archiveEpoch = 0
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

// archiveName is the archive a target's bundle directory is packed into, beside it in --out.
func archiveName(dirName, goos string) string {
	if goos == "windows" {
		return dirName + ".zip"
	}
	return dirName + ".tar.gz"
}

// removeStaleArchives deletes leftover *.zip, *.tar.gz and checksums.txt under outDir so a
// previous version's archive cannot survive into extra_files unchecksummed.
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
		if name == checksumsFileName || strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz") {
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

// writeArchive packs dir into an archive beside it and returns the archive's path.
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
	if goos == "windows" {
		err = writeZip(f, members)
	} else {
		err = writeTarGz(f, members)
	}
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

// writeTarGz writes the members as a USTAR tar inside a gzip stream with an empty header.
//
// FormatUSTAR is named rather than inferred: the PAX format archive/tar would otherwise choose for
// a long name or a sub-second time writes extended header records, and those records are one more
// thing that can differ between two packings of the same tree. Uid/Gid/Uname/Gname are left zero
// and empty so nothing about the packing machine's accounts reaches the artifact.
func writeTarGz(w io.Writer, members []archiveMember) error {
	gz := gzip.NewWriter(w)
	// Name and ModTime are the two fields a gzip header can carry about the machine that wrote
	// it. Both are cleared: an empty name and a zero ModTime encode as zero bytes.
	gz.Name, gz.Comment, gz.ModTime = "", "", time.Time{}
	tw := tar.NewWriter(gz)
	for _, m := range members {
		h := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     m.Name,
			Mode:     int64(m.Mode.Perm()),
			Size:     m.Size,
			ModTime:  time.Unix(archiveEpoch, 0).UTC(),
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(h); err != nil {
			return fmt.Errorf("%s: %w", m.Name, err)
		}
		if err := copyFileInto(tw, m.Path); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
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
