package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/pluginmanifest"
)

// The marketplace is how a user adds Qompack from its public repository (C7.5): one
// `.claude-plugin/marketplace.json` listing six entries, one per release target, each an `archive`
// source pinned by sha256 to that target's release zip. The design was adopted on 2026-09-22 and
// every rule below is the plugin-marketplaces and plugins-reference documentation of that date:
//
//   - `archive` source: "Zip archive downloaded over HTTPS ... Requires Claude Code v2.1.224 or
//     later", fields `url` (HTTPS only) and `sha256` ("Claude Code verifies every download against
//     it and refuses the install on a mismatch").
//   - Entry names differ from plugin.json's `name` ("qompack"), which is allowed: "When a marketplace
//     entry lists the plugin under a different name, the marketplace entry name is what
//     `enabledPlugins` keys and `/plugin` use."
//   - No `version` in the entries: "Avoid setting `version` in both `plugin.json` and the
//     marketplace entry. Claude Code always uses the `plugin.json` value", and each bundle's
//     plugin.json already carries it.
//   - A `description` on the marketplace, because `claude plugin validate --strict` treats a
//     missing one as an error (test/e2e/install_helpers_test.go records the same).
//
// The document is GENERATED from dist/bundle/checksums.txt — never edited by hand — so a digest in
// it is always the digest of the archive the release uploaded.
const (
	// marketplaceFileName is the document's name, both as a release asset beside the archives and
	// inside .claude-plugin/.
	marketplaceFileName = "marketplace.json"
	// marketplaceCommittedPath is where the repository carries it once a release is published: the
	// path `/plugin marketplace add AidanHT/qompack` reads on the default branch.
	marketplaceCommittedPath = ".claude-plugin/" + marketplaceFileName
	// marketplaceName is the marketplace identifier users type after the @:
	// `/plugin install qompack-linux-amd64@qompack`.
	marketplaceName = "qompack"
	// marketplaceRepoDefault is the GitHub repository whose releases host the archives.
	marketplaceRepoDefault = "AidanHT/qompack"
	// marketplaceEntryPrefix starts every entry name; the target follows it.
	marketplaceEntryPrefix = bundleProductName + "-"
	// marketplaceSourceArchive is the `source` discriminator of an archive plugin source.
	marketplaceSourceArchive = "archive"
)

// marketplaceDoc is .claude-plugin/marketplace.json.
type marketplaceDoc struct {
	Name        string             `json:"name"`
	Owner       marketplaceOwner   `json:"owner"`
	Description string             `json:"description"`
	Plugins     []marketplaceEntry `json:"plugins"`
}

// marketplaceOwner is the required owner block.
type marketplaceOwner struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// marketplaceEntry is one per-target plugin entry.
type marketplaceEntry struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Source      marketplaceSource `json:"source"`
	Homepage    string            `json:"homepage"`
	Repository  string            `json:"repository"`
	License     string            `json:"license"`
}

// marketplaceSource is an `archive` plugin source.
type marketplaceSource struct {
	Source string `json:"source"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// taskMarketplace generates, checks or validates the marketplace document.
//
//	devtool marketplace --tag v0.3.0 [--checksums dist/bundle/checksums.txt] [--out dist/bundle/marketplace.json]
//	devtool marketplace --tag v0.3.0 --check [--checksums …] [--out …]
//	devtool marketplace --validate .claude-plugin/marketplace.json
func taskMarketplace(args []string) error {
	fs := flag.NewFlagSet("marketplace", flag.ContinueOnError)
	tag := fs.String("tag", "", "the release tag the archives were published under, e.g. v0.3.0")
	checksums := fs.String("checksums", filepath.ToSlash(filepath.Join(bundleOutDefault, checksumsFileName)),
		"the archives' sha256sum-format checksums.txt (devtool bundle --archive writes it)")
	out := fs.String("out", filepath.ToSlash(filepath.Join(bundleOutDefault, marketplaceFileName)),
		"where the generated marketplace.json is written (or, with --check, compared)")
	repo := fs.String("repo", marketplaceRepoDefault, "the GitHub owner/repo whose releases host the archives")
	check := fs.Bool("check", false, "regenerate and compare with --out instead of writing it")
	validate := fs.String("validate", "", "validate an existing marketplace.json and exit")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}
	if fs.NArg() > 0 {
		return errors.Join(errUsage, fmt.Errorf("marketplace: unexpected argument %q", fs.Arg(0)))
	}

	if *validate != "" {
		raw, err := os.ReadFile(rootRelative(*validate))
		if err != nil {
			return fmt.Errorf("marketplace: %w", err)
		}
		version, err := validateMarketplace(raw, *repo)
		if err != nil {
			return fmt.Errorf("marketplace: %s: %w", *validate, err)
		}
		fmt.Printf("marketplace: %s is valid (%d targets, version %s)\n", *validate, len(releaseTargets), version)
		return nil
	}

	if *tag == "" {
		return errors.Join(errUsage, errors.New("marketplace: --tag is required (the release tag, e.g. v0.3.0)"))
	}
	rawSums, err := os.ReadFile(rootRelative(*checksums))
	if err != nil {
		return fmt.Errorf("marketplace: reading the archive checksums: %w", err)
	}
	sums, err := parseArchiveChecksums(rawSums)
	if err != nil {
		return fmt.Errorf("marketplace: %s: %w", *checksums, err)
	}
	doc, err := buildMarketplace(*tag, *repo, sums)
	if err != nil {
		return fmt.Errorf("marketplace: %w", err)
	}
	raw, err := marshalBundleJSON(doc)
	if err != nil {
		return err
	}
	// The generator's output is held to the same validator anything else is, so a regression in
	// buildMarketplace cannot write a document the validator would refuse.
	if _, err := validateMarketplace(raw, *repo); err != nil {
		return fmt.Errorf("marketplace: the generated document fails validation: %w", err)
	}

	dest := rootRelative(*out)
	if *check {
		onDisk, err := os.ReadFile(dest)
		if err != nil {
			return fmt.Errorf("marketplace --check: %w", err)
		}
		if !bytes.Equal(normalizeLF(onDisk), raw) {
			return fmt.Errorf("marketplace --check: %s does not match what %s and %s generate; "+
				"regenerate it rather than editing it", *out, *tag, *checksums)
		}
		fmt.Printf("marketplace: %s matches the generator (%s)\n", *out, *tag)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), bundleDirPerm); err != nil {
		return err
	}
	if err := os.WriteFile(dest, raw, bundleFilePerm); err != nil {
		return err
	}
	fmt.Printf("marketplace: wrote %s (%d targets, %s)\n", *out, len(doc.Plugins), *tag)
	return nil
}

// normalizeLF collapses CRLF so a Windows checkout of a committed document compares equal.
func normalizeLF(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// sha256HexRE is a lowercase sha256 digest, the spelling checksums.txt and the pin both use.
var sha256HexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// parseArchiveChecksums reads a sha256sum-format file (`<digest>  <name>`, as writeArchiveChecksums
// writes it) into archive name -> digest. A malformed line, a path instead of a bare name and a
// name listed twice are all errors: the document built from this pins what users download.
func parseArchiveChecksums(raw []byte) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(string(normalizeLF(raw)), "\n") {
		if line == "" {
			continue
		}
		digest, name, ok := strings.Cut(line, "  ")
		if !ok {
			// `sha256sum` marks binary mode with " *name"; accept it, since both mean the same bytes.
			digest, name, ok = strings.Cut(line, " *")
		}
		digest = strings.ToLower(digest)
		switch {
		case !ok || !sha256HexRE.MatchString(digest):
			return nil, fmt.Errorf("line %d is not `<sha256>  <name>`: %q", i+1, line)
		case name == "" || strings.ContainsAny(name, `/\`):
			return nil, fmt.Errorf("line %d names %q, not a bare archive name", i+1, name)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("line %d lists %s a second time", i+1, name)
		}
		out[name] = digest
	}
	return out, nil
}

// marketplaceTargetName is the entry name for one target: qompack-<os>-<arch>.
func marketplaceTargetName(goos, goarch string) string {
	return marketplaceEntryPrefix + goos + "-" + goarch
}

// marketplaceArchiveURL is where GitHub serves a release asset.
func marketplaceArchiveURL(repo, tag, asset string) string {
	return "https://github.com/" + repo + "/releases/download/" + tag + "/" + asset
}

// marketplaceOSNames and marketplaceArchNames spell the targets for people.
var (
	marketplaceOSNames   = map[string]string{"windows": "Windows", "linux": "Linux", "darwin": "macOS"}
	marketplaceArchNames = map[string]string{"amd64": "x86-64", "arm64": "ARM64"}
)

// tagVersion returns the version a release tag names: the tag without its leading v. The release
// workflow assembles with `--version "${GITHUB_REF_NAME#v}"`, so this is the version in every
// archive's file name.
func tagVersion(tag string) (string, error) {
	version, ok := strings.CutPrefix(tag, "v")
	if !ok {
		return "", fmt.Errorf("tag %q does not start with v; release tags are vX.Y.Z", tag)
	}
	if err := validateBundleVersion(version); err != nil {
		return "", fmt.Errorf("tag %q: %w", tag, err)
	}
	return version, nil
}

// buildMarketplace returns the document for tag, one entry per release target in releaseTargets
// order, each pinned to the digest checksums.txt records for that target's archive. Every target
// must be present, and no other qompack archive may be: a stale or mismatched archive in the
// checksums file is exactly the file a user would otherwise be pinned to.
func buildMarketplace(tag, repo string, sums map[string]string) (marketplaceDoc, error) {
	version, err := tagVersion(tag)
	if err != nil {
		return marketplaceDoc{}, err
	}
	homepage := "https://github.com/" + repo
	doc := marketplaceDoc{
		Name:  marketplaceName,
		Owner: marketplaceOwner{Name: "Qompack", URL: homepage},
		Description: pluginmanifest.Description + " " +
			"Each entry is one release target; install exactly one, the entry for your OS and CPU.",
	}
	want := map[string]bool{}
	for _, t := range releaseTargets {
		asset := archiveName(bundleDirName(version, bundleTarget{OS: t.GOOS, Arch: t.GOARCH}), t.GOOS)
		want[asset] = true
		digest, ok := sums[asset]
		if !ok {
			return marketplaceDoc{}, fmt.Errorf("the checksums list no %s; every release target needs its archive", asset)
		}
		doc.Plugins = append(doc.Plugins, marketplaceEntry{
			Name: marketplaceTargetName(t.GOOS, t.GOARCH),
			Description: fmt.Sprintf("Qompack for %s on %s (%s/%s). Install only the one entry that "+
				"matches your machine.", marketplaceOSNames[t.GOOS], marketplaceArchNames[t.GOARCH], t.GOOS, t.GOARCH),
			Source: marketplaceSource{
				Source: marketplaceSourceArchive,
				URL:    marketplaceArchiveURL(repo, tag, asset),
				SHA256: digest,
			},
			Homepage:   homepage,
			Repository: homepage,
			License:    "MIT",
		})
	}
	var extra []string
	for name := range sums {
		if strings.HasPrefix(name, bundleProductName+"-plugin-") && !want[name] {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return marketplaceDoc{}, fmt.Errorf("the checksums list archive(s) %s that are not this tag's six "+
			"targets; a marketplace must never pin a stale or foreign archive", strings.Join(extra, ", "))
	}
	return doc, nil
}

// validateMarketplace checks a marketplace document against the adopted design, independently of
// how it was produced, and returns the one version every entry pins. It reads the raw JSON as well
// as the typed view, because a forbidden key (`version`, a relaxed `strict`) is exactly what a
// typed decode would silently drop.
func validateMarketplace(raw []byte, repo string) (string, error) {
	var doc marketplaceDoc
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return "", fmt.Errorf("not a marketplace document: %w", err)
	}
	var loose struct {
		Plugins []map[string]json.RawMessage `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &loose); err != nil {
		return "", err
	}
	switch {
	case doc.Name != marketplaceName:
		return "", fmt.Errorf("marketplace name is %q, want %q", doc.Name, marketplaceName)
	case doc.Owner.Name == "":
		return "", errors.New("the owner has no name; it is required")
	case strings.TrimSpace(doc.Description) == "":
		return "", errors.New("the marketplace has no description; `claude plugin validate --strict` requires one")
	case len(doc.Plugins) != len(releaseTargets):
		return "", fmt.Errorf("%d entries, want one per release target (%d)", len(doc.Plugins), len(releaseTargets))
	}

	byName := map[string]marketplaceEntry{}
	for i, e := range doc.Plugins {
		if _, dup := byName[e.Name]; dup {
			return "", fmt.Errorf("entry %q is listed twice", e.Name)
		}
		byName[e.Name] = e
		for _, forbidden := range []string{"version", "strict"} {
			if _, ok := loose.Plugins[i][forbidden]; ok {
				return "", fmt.Errorf("entry %q sets %q; plugin.json carries the version and the entry "+
					"must stay strict", e.Name, forbidden)
			}
		}
	}

	var version string
	for _, t := range releaseTargets {
		name := marketplaceTargetName(t.GOOS, t.GOARCH)
		e, ok := byName[name]
		if !ok {
			return "", fmt.Errorf("no entry %q", name)
		}
		if e.Source.Source != marketplaceSourceArchive {
			return "", fmt.Errorf("%s: source is %q, want %q", name, e.Source.Source, marketplaceSourceArchive)
		}
		if !sha256HexRE.MatchString(e.Source.SHA256) {
			return "", fmt.Errorf("%s: sha256 %q is not a lowercase 64-hex digest", name, e.Source.SHA256)
		}
		prefix := "https://github.com/" + repo + "/releases/download/v"
		rest, ok := strings.CutPrefix(e.Source.URL, prefix)
		if !ok {
			return "", fmt.Errorf("%s: url %q is not a %s release asset over HTTPS", name, e.Source.URL, repo)
		}
		v, asset, ok := strings.Cut(rest, "/")
		if !ok {
			return "", fmt.Errorf("%s: url %q has no asset after the tag", name, e.Source.URL)
		}
		if want := archiveName(bundleDirName(v, bundleTarget{OS: t.GOOS, Arch: t.GOARCH}), t.GOOS); asset != want {
			return "", fmt.Errorf("%s: url names %q, want %q (the tag's own archive for this target)", name, asset, want)
		}
		if version == "" {
			version = v
		} else if v != version {
			return "", fmt.Errorf("%s pins version %s while another entry pins %s; one marketplace is one release", name, v, version)
		}
	}
	return version, nil
}
