package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// The bundle is the unit SP-17 ships: one directory per release target, holding the generated
// plugin tree, the binary built for that target, and two documents that state what is inside it.
//
// It is deliberately NOT an archive. An archive would put a compression format, a member order and
// a modification time between the bytes this repository produces and the bytes a host reads, and
// every one of those is a place where two builds of the same source stop agreeing. A directory has
// none of them: the identity is computed from the files themselves, so `checksums.txt` is
// reproducible by `sha256sum -c` on a machine that has never seen this repository.
const (
	// bundleProductName is the `name` field of BUNDLE.json and the plugin's own name.
	bundleProductName = "qompack"
	// bundleOutDefault is where `devtool bundle` assembles, relative to the repository root.
	// /dist/ is git-ignored: a bundle is a build output, never a committed artifact.
	bundleOutDefault = "dist/bundle"
	// identityFileName and checksumsFileName are the two documents that describe the bundle, and
	// therefore the two files no listing inside them may cover.
	identityFileName  = "BUNDLE.json"
	checksumsFileName = "checksums.txt"
	// pluginTreePrefix is the directory internal/pluginmanifest's keys carry, because in this
	// repository the generated tree lives under plugin/. In a bundle that tree IS the root, so the
	// prefix is stripped: a host looks for .claude-plugin/plugin.json beside the plugin root, not
	// one directory down.
	pluginTreePrefix = "plugin/"
)

const (
	bundleDirPerm  = 0o755
	bundleFilePerm = 0o644
)

// bundleTarget is one GOOS/GOARCH pair, in the spelling BUNDLE.json records it.
type bundleTarget struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// bundleSource records which commit the bundle was assembled from, and whether the worktree
// carried uncommitted changes at the time. `dirty` is not cosmetic: a bundle assembled from a
// dirty tree cannot be reproduced from its commit, so it can never be treated as a release
// artifact, and nothing but this flag would say so afterwards.
type bundleSource struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
}

// bundleFile is one entry of the bundle's content listing.
type bundleFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// bundleIdentity is BUNDLE.json: everything needed to say which bundle this is and what is in it,
// and deliberately nothing that varies between two assemblies of the same source. In particular
// there is no build timestamp — a clock reading would make every bundle unique and the
// determinism rule unenforceable.
type bundleIdentity struct {
	Name    string       `json:"name"`
	Version string       `json:"version"`
	Target  bundleTarget `json:"target"`
	Go      string       `json:"go"`
	Source  bundleSource `json:"source"`
	Files   []bundleFile `json:"files"`
}

// bundleBuilder compiles ./cmd/qompack for one target into out. It is a field of bundleAssembly
// rather than a direct call so the unit tests can substitute a fixture binary: proving the
// assembler's layout, identity and determinism does not require six real cross-compiles, and a
// test that paid for them would be measuring the Go toolchain.
type bundleBuilder func(goos, goarch, out string) error

// bundleAssembly is one `devtool bundle` run's fixed inputs. Every field is settled before the
// first target is assembled, which is what makes the six bundles of a single run agree with each
// other about version and provenance.
type bundleAssembly struct {
	version   string
	source    bundleSource
	goVersion string
	outDir    string
	build     bundleBuilder
}

// taskBundle assembles the per-target plugin bundles (00-ARCHITECTURE.md §2.6, plans/V6-SP-17).
func taskBundle(args []string) error {
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	version := fs.String("version", "",
		"version to stamp into the binary, plugin.json and BUNDLE.json alike "+
			"(default: `git describe --tags --dirty`, else internal/core.Version)")
	outDir := fs.String("out", bundleOutDefault, "directory to assemble the per-target bundles under")
	hostValidate := fs.Bool("host-validate", false,
		"run the host's own `claude plugin validate` against the host target's bundle")
	archive := fs.Bool("archive", false,
		"also pack each assembled bundle into a reproducible .zip (every target), and write "+
			"checksums.txt over them; these are the files a release uploads")
	evidence := fs.String("evidence", "",
		"file to write the --host-validate record to (default: print it to stdout)")
	var targets repeatedFlag
	fs.Var(&targets, "target", "os/arch to assemble, repeatable (default: all six release targets)")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}
	// A target given positionally (`devtool bundle windows/amd64`) would otherwise be dropped on
	// the floor and all six assembled, which looks like the tool ignoring what it was asked for.
	if fs.NArg() > 0 {
		return errors.Join(errUsage, fmt.Errorf(
			"bundle: unexpected argument %q; targets are named with --target os/arch", fs.Arg(0)))
	}
	if *evidence != "" && !*hostValidate {
		return errors.Join(errUsage, errors.New("bundle: --evidence is the destination of --host-validate's record, so it requires that flag"))
	}
	if err := rejectEvidenceDirectoryPath(*evidence); err != nil {
		return errors.Join(errUsage, err)
	}

	tgts, err := resolveBundleTargets(targets)
	if err != nil {
		return errors.Join(errUsage, err)
	}

	// `git describe` is only consulted when it can still decide the answer, so an explicit
	// --version does not spawn git at all.
	described := ""
	if *version == "" {
		described = resolveVersion()
	}
	v := bundleVersion(*version, described, core.Version)
	if err := validateBundleVersion(v); err != nil {
		return errors.Join(errUsage, err)
	}
	ldflags := versionLdflags(v)
	asm := bundleAssembly{
		version:   v,
		source:    gitSource(),
		goVersion: runtime.Version(),
		outDir:    rootRelative(*outDir),
		build: func(goos, goarch, out string) error {
			return goBuildRelease(goos, goarch, out, ldflags)
		},
	}

	dirs := make(map[bundleTarget]string, len(tgts))
	ids := make(map[bundleTarget]bundleIdentity, len(tgts))
	for _, tgt := range tgts {
		dir, id, assembleErr := asm.assemble(tgt)
		if assembleErr != nil {
			return fmt.Errorf("bundle: %s/%s: %w", tgt.OS, tgt.Arch, assembleErr)
		}
		dirs[tgt], ids[tgt] = dir, id
		fmt.Printf("bundle: %s (%d file(s), version %s)\n", filepath.Base(dir), len(id.Files), id.Version)
	}

	if *archive {
		if err := archiveAssembledBundles(asm.outDir, tgts, dirs); err != nil {
			return err
		}
	}

	if !*hostValidate {
		return nil
	}
	host := bundleTarget{OS: runtime.GOOS, Arch: runtime.GOARCH}
	dir, ok := dirs[host]
	if !ok {
		return fmt.Errorf("bundle: --host-validate needs the host target %s/%s, which --target did not select",
			host.OS, host.Arch)
	}
	return writeHostValidation(dir, ids[host], *evidence)
}

// archiveAssembledBundles packs each assembled directory and writes one checksums.txt over the
// results. The targets are walked in their assembly order so the printed lines match the bundles
// above them; the checksum file itself is sorted by archive name, because that is what a reader
// verifying it will be comparing against.
func archiveAssembledBundles(outDir string, tgts []bundleTarget, dirs map[bundleTarget]string) error {
	if err := removeStaleArchives(outDir); err != nil {
		return fmt.Errorf("bundle: clearing stale archives under %s: %w", outDir, err)
	}
	archives := make([]string, 0, len(tgts))
	for _, tgt := range tgts {
		a, err := writeArchive(dirs[tgt], tgt.OS)
		if err != nil {
			return fmt.Errorf("bundle: %s/%s: %w", tgt.OS, tgt.Arch, err)
		}
		archives = append(archives, a)
		fmt.Printf("bundle: packed %s\n", filepath.Base(a))
	}
	sums, err := writeArchiveChecksums(outDir, archives)
	if err != nil {
		return fmt.Errorf("bundle: writing the archive checksums: %w", err)
	}
	fmt.Printf("bundle: %s covers %d archive(s)\n", filepath.Base(sums), len(archives))
	return nil
}

// repeatedFlag collects a flag given more than once, in the order it was given.
type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ",") }

func (r *repeatedFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// bundleVersion applies the one version rule: an explicit --version wins, then `git describe
// --tags --dirty`, then internal/core.Version's compiled default. The result is stamped into the
// -X ldflag, into plugin.json and into BUNDLE.json in the same run, so the binary, the manifest
// and the identity cannot report three different versions of the same bundle.
func bundleVersion(override, described, compiled string) string {
	if override != "" {
		return override
	}
	if described != "" {
		return described
	}
	return compiled
}

// validateBundleVersion refuses a version that is not usable as a single directory-name segment.
//
// The version reaches the bundle directory name, and assemble points os.RemoveAll at that
// directory: `--version ../../x` would escape --out entirely and delete something the caller never
// named. An empty version would collapse the name to a double hyphen and make two versions
// indistinguishable.
func validateBundleVersion(version string) error {
	switch {
	case version == "":
		return errors.New("bundle: the version is empty; pass --version or tag the repository")
	case version == "." || version == "..":
		return fmt.Errorf("bundle: version %q is a directory reference, not a version", version)
	case version != filepath.Base(version), strings.ContainsAny(version, `/\`):
		return fmt.Errorf("bundle: version %q contains a path separator; it must be a single name segment", version)
	}
	return nil
}

// resolveBundleTargets turns the --target arguments into targets, defaulting to all six of
// 00-ARCHITECTURE.md §2.6. A target outside that set is refused rather than built: the six are the
// combinations the platform matrix has (or will have) evidence for, and a bundle for a seventh
// would carry an identity document asserting a target nobody tested.
func resolveBundleTargets(raw []string) ([]bundleTarget, error) {
	if len(raw) == 0 {
		out := make([]bundleTarget, 0, len(releaseTargets))
		for _, t := range releaseTargets {
			out = append(out, bundleTarget{OS: t.GOOS, Arch: t.GOARCH})
		}
		return out, nil
	}

	known := make([]string, 0, len(releaseTargets))
	for _, t := range releaseTargets {
		known = append(known, t.GOOS+"/"+t.GOARCH)
	}

	out := make([]bundleTarget, 0, len(raw))
	seen := make(map[bundleTarget]bool, len(raw))
	for _, spec := range raw {
		goos, goarch, found := strings.Cut(spec, "/")
		if !found || goos == "" || goarch == "" || strings.Contains(goarch, "/") {
			return nil, fmt.Errorf("bundle: --target %q is not an os/arch pair; one of: %s",
				spec, strings.Join(known, ", "))
		}
		var ok bool
		for _, t := range releaseTargets {
			if t.GOOS == goos && t.GOARCH == goarch {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("bundle: --target %q is not one of the six release targets: %s",
				spec, strings.Join(known, ", "))
		}
		tgt := bundleTarget{OS: goos, Arch: goarch}
		// A target named twice is assembled once: the second pass would clear and rebuild the
		// first's directory for no gain.
		if seen[tgt] {
			continue
		}
		seen[tgt] = true
		out = append(out, tgt)
	}
	return out, nil
}

// bundleDirName is the per-target directory name. The target is part of the name because the six
// bundles of one version differ only in their binary, and a reader holding one of them has no
// other way to tell which.
func bundleDirName(version string, tgt bundleTarget) string {
	return fmt.Sprintf("%s-plugin-%s-%s-%s", bundleProductName, version, tgt.OS, tgt.Arch)
}

// bundleBinPath is where the bundled binary sits, as a bundle-relative slash path. It is exactly
// the path the target's hooks.json and .mcp.json name after ${CLAUDE_PLUGIN_ROOT}/
// (pluginmanifest.BinaryRef), which TestAssembleBundle_Layout holds equal per target.
func bundleBinPath(goos string) string {
	return "bin/" + bundleProductName + exeSuffix(goos)
}

// assemble builds one target's bundle under a.outDir and returns its directory and identity.
//
// The directory is cleared first. Assembling over an existing tree would let a file from an
// earlier version survive into a later bundle, where it would be hashed, listed in BUNDLE.json and
// shipped as though it belonged there.
func (a bundleAssembly) assemble(tgt bundleTarget) (string, bundleIdentity, error) {
	if a.build == nil {
		return "", bundleIdentity{}, errors.New("no build step configured")
	}
	// Checked here as well as at the flag, because this is the line that decides what
	// os.RemoveAll is pointed at.
	if err := validateBundleVersion(a.version); err != nil {
		return "", bundleIdentity{}, err
	}

	dir := filepath.Join(a.outDir, bundleDirName(a.version, tgt))
	if err := os.RemoveAll(dir); err != nil {
		return "", bundleIdentity{}, fmt.Errorf("clearing %s: %w", dir, err)
	}

	tree, err := pluginTreeFiles(a.version, tgt.OS)
	if err != nil {
		return "", bundleIdentity{}, err
	}
	for rel, b := range tree {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), bundleDirPerm); err != nil {
			return "", bundleIdentity{}, err
		}
		if err := os.WriteFile(p, b, bundleFilePerm); err != nil {
			return "", bundleIdentity{}, err
		}
	}

	binRel := bundleBinPath(tgt.OS)
	binAbs := filepath.Join(dir, filepath.FromSlash(binRel))
	if err := os.MkdirAll(filepath.Dir(binAbs), bundleDirPerm); err != nil {
		return "", bundleIdentity{}, err
	}
	if err := a.build(tgt.OS, tgt.Arch, binAbs); err != nil {
		return "", bundleIdentity{}, fmt.Errorf("building %s: %w", binRel, err)
	}

	files, err := hashBundleTree(dir)
	if err != nil {
		return "", bundleIdentity{}, err
	}
	id := bundleIdentity{
		Name:    bundleProductName,
		Version: a.version,
		Target:  tgt,
		Go:      a.goVersion,
		Source:  a.source,
		Files:   files,
	}

	raw, err := marshalBundleJSON(id)
	if err != nil {
		return "", bundleIdentity{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, identityFileName), raw, bundleFilePerm); err != nil {
		return "", bundleIdentity{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, checksumsFileName), renderChecksums(files), bundleFilePerm); err != nil {
		return "", bundleIdentity{}, err
	}
	return dir, id, nil
}

// pluginTreeFiles returns the plugin tree generated for goos, keyed by BUNDLE-relative slash path.
//
// The tree is rendered per target because every hook and the MCP server name the exact executable
// that target's bundle ships (bundleBinPath): exec form spawns `command` directly, and on Windows
// "exec form requires command to resolve to a real executable such as a .exe" (C1.11).
//
// internal/pluginmanifest keys its output by repository path, so every key starts with "plugin/".
// A bundle's root IS the plugin root, so the prefix comes off — and a key that does not carry it
// is an error rather than a file written to an unexpected place, because that would mean the
// generator's layout changed underneath the assembler.
func pluginTreeFiles(version, goos string) (map[string][]byte, error) {
	files, err := pluginmanifest.ForTarget(version, goos).Files()
	if err != nil {
		return nil, fmt.Errorf("generating the plugin tree: %w", err)
	}
	out := make(map[string][]byte, len(files))
	for rel, b := range files {
		trimmed, found := strings.CutPrefix(rel, pluginTreePrefix)
		if !found {
			return nil, fmt.Errorf("internal/pluginmanifest produced %q, which is not under %q", rel, pluginTreePrefix)
		}
		out[trimmed] = b
	}
	return out, nil
}

// hashBundleTree lists every file in the bundle with its sha256 and size, sorted by slash path.
// The two identity documents are excluded: they describe the bundle, so they cannot describe
// themselves, and including them would make the listing depend on its own contents.
func hashBundleTree(dir string) ([]bundleFile, error) {
	var out []bundleFile
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
		slash := filepath.ToSlash(rel)
		if slash == identityFileName || slash == checksumsFileName {
			return nil
		}
		sum, n, hashErr := hashFile(p)
		if hashErr != nil {
			return hashErr
		}
		out = append(out, bundleFile{Path: slash, SHA256: sum, Bytes: n})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hashing %s: %w", dir, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// hashFile streams p through sha256 and returns the hex digest and the byte count, so a bundled
// binary is never held in memory whole.
func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// renderChecksums writes the listing in `sha256sum` format — digest, two spaces, path, LF — so it
// can be verified with `sha256sum -c checksums.txt` from inside the bundle directory by somebody
// who has none of this repository's tooling.
func renderChecksums(files []bundleFile) []byte {
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "%s  %s\n", f.SHA256, f.Path)
	}
	return []byte(b.String())
}

// marshalBundleJSON renders a bundle document with two-space indentation and a trailing newline,
// matching internal/pluginmanifest's own encoding so every JSON file in a bundle is spelled the
// same way. HTML escaping is off because it would rewrite characters in a path for no reason.
func marshalBundleJSON(v any) ([]byte, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// gitSource reads the commit and worktree cleanliness this assembly is made from. Both calls are
// read-only. When git is unavailable the commit is empty rather than invented: an unknown
// provenance must read as unknown, not as clean.
func gitSource() bundleSource {
	var src bundleSource
	if out, _, err := runCapture(nil, "git", "rev-parse", "HEAD"); err == nil {
		src.Commit = strings.TrimSpace(string(out))
	}
	if out, _, err := runCapture(nil, "git", "status", "--porcelain"); err == nil {
		src.Dirty = strings.TrimSpace(string(out)) != ""
	}
	return src
}

// rootRelative resolves a user-supplied output path against the repository root, leaving an
// absolute path alone.
func rootRelative(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, filepath.FromSlash(p))
}
