package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCoverProfile(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "coverage.out")
	content := `mode: atomic
github.com/qompack/qompack/internal/core/hash.go:10.2,12.3 2 1
github.com/qompack/qompack/internal/core/hash.go:14.2,16.3 3 0
github.com/qompack/qompack/internal/core/ids.go:5.2,7.3 1 1
`
	if err := os.WriteFile(profile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing profile: %v", err)
	}

	stats, err := parseCoverProfile(profile)
	if err != nil {
		t.Fatalf("parseCoverProfile: %v", err)
	}
	s, ok := stats["github.com/qompack/qompack/internal/core"]
	if !ok {
		t.Fatalf("expected stats for internal/core, got: %v", stats)
	}
	if s.total != 6 || s.covered != 3 {
		t.Fatalf("unexpected stat: %+v", s)
	}
	if got, want := s.pct(), 50.0; got != want {
		t.Fatalf("pct() = %v, want %v", got, want)
	}
}

func TestProbeStillStub(t *testing.T) {
	dir := t.TempDir()

	stubFile := filepath.Join(dir, "store.go")
	mustWrite(t, stubFile, `package store

import "github.com/qompack/qompack/internal/core"

// PutBytes is not implemented yet.
func PutBytes() error {
	return core.ErrNotImplemented
}
`)
	if !probeStillStub(dir, "PutBytes") {
		t.Error("a bare `return core.ErrNotImplemented` body should be detected as still-stub")
	}

	realFile := filepath.Join(dir, "real.go")
	mustWrite(t, realFile, `package store

// PutBytes actually does something.
func PutBytes() error {
	x := 1
	if x == 1 {
		return nil
	}
	return nil
}
`)
	dir2 := t.TempDir()
	realFile2 := filepath.Join(dir2, "real.go")
	mustWrite(t, realFile2, `package store

func PutBytes() error {
	x := 1
	if x == 1 {
		return nil
	}
	return nil
}
`)
	if probeStillStub(dir2, "PutBytes") {
		t.Error("a multi-statement body must not be flagged as still-stub")
	}

	_ = realFile // keep both fixtures for clarity even though only dir2 is asserted on

	if probeStillStub(dir, "NoSuchMethod") {
		t.Error("a probe name that matches no declaration must not be flagged as still-stub")
	}
}

func TestIsBareNotImplementedStub_WrappedError(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "wrapped.go")
	mustWrite(t, f, `package store

import (
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

func Evaluate() error {
	return fmt.Errorf("store: %w", core.ErrNotImplemented)
}
`)
	if !probeStillStub(dir, "Evaluate") {
		t.Error("a return wrapping core.ErrNotImplemented via fmt.Errorf should still be detected")
	}
}
