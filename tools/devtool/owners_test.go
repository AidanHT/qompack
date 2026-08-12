package main

import (
	"path/filepath"
	"testing"
)

// TestLoadOwners_ParsesRealFile parses the actual plans/OWNERS.tsv and sanity-checks a few known
// rows, so a future edit that breaks the TSV shape (wrong column count, unparsable floor) is
// caught here rather than only inside cover/stubskips at lint time.
func TestLoadOwners_ParsesRealFile(t *testing.T) {
	r := testModuleRoot(t)
	rows, err := loadOwners(filepath.Join(r, "plans", "OWNERS.tsv"))
	if err != nil {
		t.Fatalf("loadOwners: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one row")
	}

	byPkg := make(map[string]ownerRow, len(rows))
	for _, row := range rows {
		byPkg[row.Package] = row
	}

	core, ok := byPkg["core"]
	if !ok {
		t.Fatal("expected a row for package \"core\"")
	}
	if core.Owner != "SP-01" || core.Floor != 75 || core.Probe != "-" {
		t.Errorf("unexpected core row: %+v", core)
	}

	store, ok := byPkg["store"]
	if !ok {
		t.Fatal("expected a row for package \"store\"")
	}
	if store.Owner != "SP-06" || store.Floor != 90 || store.Probe != "PutBytes" {
		t.Errorf("unexpected store row: %+v", store)
	}

	cmdQompack, ok := byPkg["cmd/qompack"]
	if !ok {
		t.Fatal("expected a row for package \"cmd/qompack\"")
	}
	if cmdQompack.Owner != "SP-01" {
		t.Errorf("unexpected cmd/qompack row: %+v", cmdQompack)
	}
}

func TestPackageKeyOf(t *testing.T) {
	cases := []struct {
		importPath string
		want       string
	}{
		{modulePath + "/internal/store", "store"},
		{modulePath + "/internal/store/storetest", "store"},
		{modulePath + "/internal/core", "core"},
		{modulePath + "/cmd/qompack", "cmd/qompack"},
		{"not-in-module", "not-in-module"},
	}
	for _, c := range cases {
		if got := packageKeyOf(c.importPath); got != c.want {
			t.Errorf("packageKeyOf(%q) = %q, want %q", c.importPath, got, c.want)
		}
	}
}
