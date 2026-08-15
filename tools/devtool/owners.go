package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ownerRow is one line of plans/OWNERS.tsv: package name (bare, e.g. "store" or "cmd/qompack"),
// owning subplan, the §6.4 coverage floor that binds once the owner lands, and the probe method
// name isStub()-style checks call to tell a stub from a real implementation ("-" when the package
// has no stub phase because SP-01 implements it immediately).
type ownerRow struct {
	Package string
	Owner   string
	Floor   int
	Probe   string
}

// loadOwners parses plans/OWNERS.tsv: tab-separated, "#"-prefixed comment lines and blank lines
// ignored, an optional "package\towner\tfloor\tprobe" header line ignored.
func loadOwners(path string) ([]ownerRow, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", path, err)
	}

	var rows []ownerRow
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return nil, fmt.Errorf("%s:%d: expected 4 tab-separated fields, got %d: %q", path, i+1, len(fields), line)
		}
		if fields[0] == "package" && fields[1] == "owner" {
			continue // header line
		}
		floor, ferr := strconv.Atoi(strings.TrimSpace(fields[2]))
		if ferr != nil {
			return nil, fmt.Errorf("%s:%d: bad floor %q: %w", path, i+1, fields[2], ferr)
		}
		rows = append(rows, ownerRow{
			Package: strings.TrimSpace(fields[0]),
			Owner:   strings.TrimSpace(fields[1]),
			Floor:   floor,
			Probe:   strings.TrimSpace(fields[3]),
		})
	}
	return rows, nil
}

// packageKeyOf maps a full package import path (possibly a <pkg>test conformance subpackage) to
// the bare key plans/OWNERS.tsv uses: both ".../internal/store" and ".../internal/store/storetest"
// map to "store"; ".../cmd/qompack" maps to "cmd/qompack".
func packageKeyOf(importPath string) string {
	rel := strings.TrimPrefix(importPath, modulePath+"/")
	if rel == importPath {
		return importPath
	}
	if rel == "cmd/qompack" {
		return "cmd/qompack"
	}
	rel = strings.TrimPrefix(rel, "internal/")
	key, _, _ := strings.Cut(rel, "/")
	return key
}
