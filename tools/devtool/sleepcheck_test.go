package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScanFileForSleep_FindsSelectorCall asserts a plain time.Sleep(...) call is reported, and a
// look-alike (a local type named time with its own Sleep method) is not: the check only fires for
// the identifier actually bound to the imported "time" package.
func TestScanFileForSleep_FindsSelectorCall(t *testing.T) {
	dir := t.TempDir()

	withSleep := filepath.Join(dir, "with_sleep.go")
	mustWrite(t, withSleep, `package x

import "time"

func f() {
	time.Sleep(time.Second)
}
`)
	hits, err := scanFileForSleep(withSleep)
	if err != nil {
		t.Fatalf("scanFileForSleep: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("want exactly one hit, got %d: %v", len(hits), hits)
	}

	aliased := filepath.Join(dir, "aliased.go")
	mustWrite(t, aliased, `package x

import tm "time"

func f() {
	tm.Sleep(tm.Second)
}
`)
	hits, err = scanFileForSleep(aliased)
	if err != nil {
		t.Fatalf("scanFileForSleep: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("want exactly one hit for an aliased import, got %d: %v", len(hits), hits)
	}

	noSleep := filepath.Join(dir, "no_sleep.go")
	mustWrite(t, noSleep, `package x

type time struct{}

func (time) Sleep() {}

func f() {
	var t time
	t.Sleep()
}
`)
	hits, err = scanFileForSleep(noSleep)
	if err != nil {
		t.Fatalf("scanFileForSleep: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("a locally-declared type named time must not be mistaken for the time package: %v", hits)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
