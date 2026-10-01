package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// `devtool licenses` writes THIRD_PARTY_NOTICES.md: the licence texts this repository is obliged
// to redistribute alongside the binary, and an explicit statement of which dependencies are NOT
// redistributed because they never reach it.
//
// The module set is DERIVED, never transcribed. A notices file assembled by hand is wrong the day
// after a dependency changes and nothing says so — which is the same failure mode the bindeps
// allow-list exists to prevent, so this task reuses that allow-list and intersects it with what
// `go list -deps ./cmd/qompack` actually pulls on the six release targets. A module that is
// permitted but unreached is not listed: including it would claim a redistribution that does not
// happen.
const noticesFileName = "THIRD_PARTY_NOTICES.md"

// shippedLicenseID is the SPDX identifier for each module that may reach the binary. The mapping
// is a table rather than a guess from the licence text, because "which licence is this" is a legal
// classification and a regex over prose is not one. A module reaching the binary without a row
// here fails the task: an unclassified dependency in a shipped artifact is exactly the thing this
// file exists to make impossible to ship quietly.
var shippedLicenseID = map[string]string{
	"github.com/klauspost/compress": "BSD-3-Clause, with Apache-2.0 and MIT components " +
		"(the module's own LICENSE carries all three; see the text below)",
	"github.com/Microsoft/go-winio": "MIT",
	"golang.org/x/sys":              "BSD-3-Clause",
}

// toolingLicenseID is every other DIRECT dependency: build-time or test-time only, never linked
// into a released binary. `devtool lint --only=bindeps` proves that claim on all six targets.
var toolingLicenseID = map[string]string{
	"github.com/google/go-cmp":    "BSD-3-Clause",
	"github.com/stretchr/testify": "MIT",
	"golang.org/x/tools":          "BSD-3-Clause",
	"pgregory.net/rapid":          "MPL-2.0",
}

// licenseText is one licence file copied out of the module cache.
type licenseText struct {
	// Rel is the file's path within the module, so a reader can tell the module's own LICENSE
	// from one belonging to a subdirectory it also carries.
	Rel  string
	Body string
}

// goLicense is the Go distribution's LICENSE and the toolchain go.mod pins. The runtime and the
// standard library are compiled into every released binary, so this text ships like a module's.
type goLicense struct {
	// Toolchain is go.mod's `toolchain` directive, e.g. go1.26.6. It is read from go.mod rather than
	// from the running toolchain so the page renders the same on every machine that checks it.
	Toolchain string
	Body      string
}

// licenseModule is one redistributed dependency.
type licenseModule struct {
	Path    string
	Version string
	ID      string
	Texts   []licenseText
}

// taskLicenses generates or checks THIRD_PARTY_NOTICES.md.
func taskLicenses(args []string) error {
	fs := flag.NewFlagSet("licenses", flag.ContinueOnError)
	write := fs.Bool("write", false, "write "+noticesFileName)
	check := fs.Bool("check", false, "regenerate in memory and fail if "+noticesFileName+" has drifted")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}
	if *write == *check {
		return errors.Join(errUsage, errors.New("licenses: pass exactly one of --write or --check"))
	}

	rendered, err := renderNoticesFromRepo()
	if err != nil {
		return err
	}
	out := filepath.Join(root, noticesFileName)
	if *write {
		if err := os.WriteFile(out, []byte(rendered), bundleFilePerm); err != nil {
			return err
		}
		fmt.Printf("licenses: wrote %s\n", noticesFileName)
		return nil
	}

	current, err := os.ReadFile(out)
	if err != nil {
		return fmt.Errorf("licenses: %s is missing; run `devtool licenses --write`: %w", noticesFileName, err)
	}
	if string(current) != rendered {
		return fmt.Errorf("licenses: %s is out of date (%d bytes on disk, %d regenerated); "+
			"run `devtool licenses --write`", noticesFileName, len(current), len(rendered))
	}
	fmt.Printf("licenses: %s is current\n", noticesFileName)
	return nil
}

// renderNoticesFromRepo collects the shipped modules from the real toolchain and renders the page.
func renderNoticesFromRepo() (string, error) {
	project, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return "", fmt.Errorf("licenses: reading the project LICENSE: %w", err)
	}
	versions, err := goModVersions(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	paths, err := shippedModulePaths()
	if err != nil {
		return "", err
	}
	mods := make([]licenseModule, 0, len(paths))
	for _, p := range paths {
		id, ok := shippedLicenseID[p]
		if !ok {
			return "", fmt.Errorf("licenses: %s reaches the shipped binary and has no licence "+
				"classification; add one to shippedLicenseID before releasing", p)
		}
		dir, err := moduleCacheDir(p)
		if err != nil {
			return "", err
		}
		texts, err := moduleLicenseTexts(dir)
		if err != nil {
			return "", err
		}
		if len(texts) == 0 {
			return "", fmt.Errorf("licenses: no LICENSE or COPYING file found under %s for %s", dir, p)
		}
		mods = append(mods, licenseModule{Path: p, Version: versions[p], ID: id, Texts: texts})
	}
	tooling, err := toolingRows(versions, paths)
	if err != nil {
		return "", err
	}
	goLic, err := goToolchainLicense()
	if err != nil {
		return "", err
	}
	return renderNotices(strings.ReplaceAll(string(project), "\r\n", "\n"), goLic, mods, tooling), nil
}

// goModToolchainRE matches go.mod's toolchain directive.
var goModToolchainRE = regexp.MustCompile(`(?m)^toolchain\s+(go\S+)\s*$`)

// goToolchainLicense reads go.mod's toolchain directive and the LICENSE of the Go distribution
// `go env GOROOT` names. The go command resolves GOROOT for this module the way a build does
// (GOTOOLCHAIN selects go.mod's toolchain when the local one is older), so the text is the licence
// of the distribution that compiles the release. A missing directive or file is an error: a page
// that silently dropped the Go licence would repeat the defect this section fixes.
func goToolchainLicense() (goLicense, error) {
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return goLicense{}, fmt.Errorf("licenses: reading go.mod: %w", err)
	}
	m := goModToolchainRE.FindStringSubmatch(strings.ReplaceAll(string(mod), "\r\n", "\n"))
	if m == nil {
		return goLicense{}, errors.New("licenses: go.mod has no toolchain directive, so the page " +
			"cannot name the Go distribution whose licence every binary carries")
	}
	stdout, stderr, err := runCapture(nil, "go", "env", "GOROOT")
	if err != nil {
		return goLicense{}, fmt.Errorf("licenses: go env GOROOT: %w\n%s", err, stderr)
	}
	goroot := strings.TrimSpace(string(stdout))
	if goroot == "" {
		return goLicense{}, errors.New("licenses: go env GOROOT printed nothing")
	}
	body, err := os.ReadFile(filepath.Join(goroot, "LICENSE"))
	if err != nil {
		return goLicense{}, fmt.Errorf("licenses: reading the Go distribution's LICENSE: %w", err)
	}
	return goLicense{Toolchain: m[1], Body: strings.ReplaceAll(string(body), "\r\n", "\n")}, nil
}

// goModRequireRE matches one require line of a go.mod, with or without the indirect marker.
var goModRequireRE = regexp.MustCompile(`(?m)^\s*(\S+)\s+(v\S+)(\s+// indirect)?\s*$`)

// goModVersions reads every module version go.mod pins, and which of them are direct.
func goModVersions(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("licenses: reading go.mod: %w", err)
	}
	out := map[string]string{}
	for _, m := range goModRequireRE.FindAllStringSubmatch(string(b), -1) {
		if m[1] == "module" || m[1] == "go" || m[1] == "toolchain" {
			continue
		}
		out[m[1]] = m[2]
		if m[3] == "" {
			out["direct:"+m[1]] = m[2]
		}
	}
	if len(out) == 0 {
		return nil, errors.New("licenses: go.mod declares no requirements; the parser is broken")
	}
	return out, nil
}

// toolingRow is one closing-table entry: a direct dependency that is not redistributed.
type toolingRow struct{ Path, Version, ID, Note string }

// toolingRows lists every direct dependency that is not in shipped, classified from
// toolingLicenseID. An unclassified one is an error for the same reason a shipped one is.
func toolingRows(versions map[string]string, shipped []string) ([]toolingRow, error) {
	isShipped := map[string]bool{}
	for _, p := range shipped {
		isShipped[p] = true
	}
	var out []toolingRow
	for key, v := range versions {
		p, ok := strings.CutPrefix(key, "direct:")
		if !ok || isShipped[p] {
			continue
		}
		id, known := toolingLicenseID[p]
		if !known {
			return nil, fmt.Errorf("licenses: %s is a direct dependency with no licence "+
				"classification; add one to toolingLicenseID", p)
		}
		note := "test/tooling only, not shipped"
		if id == "MPL-2.0" {
			note = "test only, not shipped — `devtool lint --only=bindeps` proves it never reaches " +
				"the binary on any of the six release targets, so its file-level copyleft is never triggered"
		}
		out = append(out, toolingRow{Path: p, Version: v, ID: id, Note: note})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// shippedModulePaths returns, sorted, every non-stdlib module other than this one that
// `go list -deps ./cmd/qompack` reaches on at least one release target.
func shippedModulePaths() ([]string, error) {
	seen := map[string]bool{}
	for _, tgt := range releaseTargets {
		env := map[string]string{"CGO_ENABLED": "0", "GOOS": tgt.GOOS, "GOARCH": tgt.GOARCH}
		stdout, stderr, err := runCapture(env, "go", "list", "-deps",
			"-f", "{{if .Module}}{{.Module.Path}}{{end}}", "./cmd/qompack")
		if err != nil {
			return nil, fmt.Errorf("licenses: go list -deps (%s/%s): %w\n%s", tgt.GOOS, tgt.GOARCH, err, stderr)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(stdout)), "\n") {
			if p := strings.TrimSpace(line); p != "" && p != modulePath {
				seen[p] = true
			}
		}
	}
	return sortedKeys(seen), nil
}

// sortedKeys is the set-to-slice step every caller here wants.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// moduleCacheDir resolves a module path to its extracted directory in the module cache.
func moduleCacheDir(path string) (string, error) {
	stdout, stderr, err := runCapture(nil, "go", "list", "-m", "-f", "{{.Dir}}", path)
	if err != nil {
		return "", fmt.Errorf("licenses: go list -m %s: %w\n%s", path, err, stderr)
	}
	dir := strings.TrimSpace(string(stdout))
	if dir == "" {
		return "", fmt.Errorf("licenses: %s has no extracted directory in the module cache", path)
	}
	return dir, nil
}

// licenseFileRE matches the conventional licence file names, at any depth within a module.
var licenseFileRE = regexp.MustCompile(`^(LICEN[CS]E|COPYING)([-.].*)?$`)

// moduleLicenseTexts reads every licence file a module carries, sorted by its path within the
// module. The whole tree is walked rather than only its root because a module can vendor code
// under a second licence and keep that licence beside it — github.com/klauspost/compress carries
// four, and a notices file that reproduced only the top one would under-report what it ships.
func moduleLicenseTexts(dir string) ([]licenseText, error) {
	var out []licenseText
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !licenseFileRE.MatchString(strings.ToUpper(d.Name())) {
			return nil
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, licenseText{Rel: filepath.ToSlash(rel), Body: strings.ReplaceAll(string(b), "\r\n", "\n")})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("licenses: walking %s: %w", dir, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}
