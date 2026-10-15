package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// modSourceDir holds the mod's TypeScript sources: the repository's first product .ts files,
// embedded into the binary by internal/pluginmanifest and shipped in bundles.
//
// No existing gate reached them. gofumpt formats Go only, the docmarkers scan reads Markdown only,
// and the EOL attributes name *.go and *.md. Each gets an explicit rule for exactly this directory
// below rather than a widened pattern, so nothing outside it changes: in particular the .ts files
// under testdata/ elsewhere are canonicalization fixtures whose CRLF and trailing whitespace are
// the point, and no rule here touches them.
const modSourceDir = "internal/pluginmanifest/modsrc"

// utf8BOM is the byte-order mark a mod source must not start with.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// modSourceFiles lists every .ts file under modSourceDir, recursively, as sorted slash paths
// relative to root. A missing directory is no files.
func modSourceFiles(root string) ([]string, error) {
	dir := filepath.Join(root, filepath.FromSlash(modSourceDir))
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".ts") {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// modSourceProblems reports each formatting rule one mod source breaks: no byte-order mark, LF
// line endings only, no trailing whitespace on any line, and exactly one newline at the end.
//
// These are the rules that make the embedded bytes the same on every checkout and every machine.
// Layout beyond them (indentation, quotes, semicolons) is left to review: holding it would need a
// TypeScript formatter, and the pinned toolchain has none.
func modSourceProblems(b []byte) []string {
	var probs []string
	if bytes.HasPrefix(b, utf8BOM) {
		probs = append(probs, "starts with a byte-order mark")
	}
	if bytes.IndexByte(b, '\r') >= 0 {
		probs = append(probs, "has CR line endings")
	}
	if !bytes.HasSuffix(b, []byte("\n")) || bytes.HasSuffix(b, []byte("\n\n")) {
		probs = append(probs, "does not end in exactly one newline")
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	for i, line := range lines {
		if strings.TrimRight(line, " \t") != line {
			probs = append(probs, fmt.Sprintf("line %d has trailing whitespace", i+1))
		}
	}
	return probs
}

// fixModSource repairs everything modSourceProblems reports and leaves a conforming file
// byte-identical.
func fixModSource(b []byte) []byte {
	s := string(bytes.TrimPrefix(b, utf8BOM))
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	s = strings.TrimRight(strings.Join(lines, "\n"), "\n")
	return []byte(s + "\n")
}

// checkModSources is fmt-check's rule for mod sources: it fails listing every file that breaks
// modSourceProblems' rules.
func checkModSources(root string) error {
	files, err := modSourceFiles(root)
	if err != nil {
		return err
	}
	var offenders []string
	for _, f := range files {
		b, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if readErr != nil {
			return readErr
		}
		for _, p := range modSourceProblems(b) {
			offenders = append(offenders, f+": "+p)
		}
	}
	if len(offenders) > 0 {
		return fmt.Errorf("%d mod source problem(s); run `devtool fmt`:\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
	return nil
}

// fixModSources is fmt's rule for mod sources: it rewrites every file that breaks them, and only
// those files.
func fixModSources(root string) error {
	files, err := modSourceFiles(root)
	if err != nil {
		return err
	}
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		fixed := fixModSource(b)
		if bytes.Equal(fixed, b) {
			continue
		}
		if writeErr := os.WriteFile(p, fixed, 0o644); writeErr != nil { // #nosec G306 -- tracked source file
			return writeErr
		}
		fmt.Println(f)
	}
	return nil
}

// findModSourceMarkers is the docmarkers rule for a mod source: the same placeholder test the
// specification documents get, applied to the comment text of each line only. A placeholder that
// matters sits in prose, and TypeScript code is full of shapes the Markdown rule would misread —
// `<T extends string>` has a capital head and a tail — so code and string literals are not read.
func findModSourceMarkers(file, content string) []planMarker {
	var out []planMarker
	inBlock := false
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := scanner.Text()
		var comment string
		trimmed := strings.TrimSpace(line)
		switch {
		case inBlock || strings.HasPrefix(trimmed, "/*"):
			comment = line
			inBlock = !strings.Contains(trimmed, "*/")
		default:
			if i := strings.Index(line, "//"); i >= 0 {
				comment = line[i:]
			}
		}
		for _, tok := range unfilledMarkers(comment) {
			out = append(out, planMarker{file: file, line: lineNo, token: tok})
		}
	}
	return out
}

// modSourceMarkers scans every mod source under root for unfilled placeholders and reports how
// many files it read.
func modSourceMarkers() ([]planMarker, int, error) {
	files, err := modSourceFiles(root)
	if err != nil {
		return nil, 0, err
	}
	var found []planMarker
	for _, f := range files {
		b, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if readErr != nil {
			return nil, 0, readErr
		}
		found = append(found, findModSourceMarkers(f, string(b))...)
	}
	return found, len(files), nil
}
