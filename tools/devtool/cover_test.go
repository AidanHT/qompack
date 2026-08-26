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

// TestIsCompositionRoot covers the 00-ARCHITECTURE.md §6.4 exemption. The negative cases carry
// the weight here: the exemption is only defensible while it stays narrow, so each way of growing
// a second declaration must revoke it. A regression that widened this into "any main package" is
// exactly the silent hole the amendment argues against.
func TestIsCompositionRoot(t *testing.T) {
	realShape := `package main

import (
	"context"
	"os"

	"github.com/qompack/qompack/internal/cli"
)

func main() {
	os.Exit(cli.Dispatch(context.Background(), cli.All(), os.Args, cli.Env{}, os.Stdout, os.Stderr))
}
`

	exempt := map[string]string{
		"dispatch only, the cmd/qompack shape": realShape,
		"no imports at all":                    "package main\n\nfunc main() {}\n",
	}
	for name, src := range exempt {
		t.Run("exempt/"+name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, filepath.Join(dir, "main.go"), src)
			if !isCompositionRoot(dir) {
				t.Error("expected the composition-root exemption to apply")
			}
		})
	}

	notExempt := map[string]string{
		"a second function":      "package main\n\nfunc main() {}\n\nfunc helper() int { return 1 }\n",
		"a method":               "package main\n\ntype t struct{}\n\nfunc (t) m() {}\n\nfunc main() {}\n",
		"a package-level var":    "package main\n\nvar version = \"dev\"\n\nfunc main() {}\n",
		"a package-level const":  "package main\n\nconst limit = 10\n\nfunc main() {}\n",
		"a type declaration":     "package main\n\ntype opts struct{ n int }\n\nfunc main() {}\n",
		"not package main":       "package cli\n\nfunc main() {}\n",
		"no func main":           "package main\n\nfunc other() {}\n",
		"does not parse as Go":   "package main\n\nfunc main( {\n",
		"empty, nothing to skip": "",
	}
	for name, src := range notExempt {
		t.Run("not-exempt/"+name, func(t *testing.T) {
			dir := t.TempDir()
			if src != "" {
				mustWrite(t, filepath.Join(dir, "main.go"), src)
			}
			if isCompositionRoot(dir) {
				t.Error("expected the floor to still apply")
			}
		})
	}

	t.Run("not-exempt/second file adds a declaration", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "main.go"), realShape)
		mustWrite(t, filepath.Join(dir, "flags.go"), "package main\n\nfunc parseFlags() {}\n")
		if isCompositionRoot(dir) {
			t.Error("a declaration in a sibling file must revoke the exemption too")
		}
	})

	t.Run("exempt/test files are ignored", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "main.go"), realShape)
		mustWrite(t, filepath.Join(dir, "main_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
		if !isCompositionRoot(dir) {
			t.Error("a _test.go file must not revoke the exemption")
		}
	})

	t.Run("not-exempt/unreadable directory", func(t *testing.T) {
		if isCompositionRoot(filepath.Join(t.TempDir(), "no-such-dir")) {
			t.Error("an unreadable directory must fail closed, keeping the floor")
		}
	})
}

// TestFloorApplies pins the rule that decides whether a §6.4 coverage floor is live: a package is
// measured once its owning subplan has landed, and exempt before that because a stub's coverage
// number measures nothing.
func TestFloorApplies(t *testing.T) {
	t.Run("absent package is not yet present", func(t *testing.T) {
		row := ownerRow{Package: "eval", Owner: "SP-02", Floor: 85, Probe: "Load"}
		applies, why := floorApplies(row, filepath.Join(t.TempDir(), "no-such-dir"))
		if applies {
			t.Error("a package that does not exist yet cannot be below its floor")
		}
		if want := "not yet present: eval"; why != want {
			t.Errorf("reason = %q, want %q", why, want)
		}
	})

	t.Run("composition root is exempt", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
		row := ownerRow{Package: "cmd/qompack", Owner: "SP-01", Floor: 75, Probe: "-"}
		applies, why := floorApplies(row, dir)
		if applies {
			t.Error("§6.4 exempts a composition root")
		}
		if want := "exempt (composition root, §6.4): cmd/qompack"; why != want {
			t.Errorf("reason = %q, want %q", why, want)
		}
	})

	// negknow is SP-09's and stays a stub until wave 2. This case used SP-03/sketch while SP-03 was
	// unlanded, which asserted an exemption that stopped existing the moment wave 1 merged. Any
	// still-unlanded owner works — the subject here is the reason string, not the package — but it
	// has to be one that is still a stub, or the case passes for the wrong reason.
	t.Run("an unlanded owner is exempt and the log names it", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "negknow.go"), "package negknow\n\nfunc New() {}\n")
		row := ownerRow{Package: "negknow", Owner: "SP-09", Floor: 90, Probe: "Query"}
		applies, why := floorApplies(row, dir)
		if applies {
			t.Error("a stub's coverage number measures nothing, so its floor cannot bind yet")
		}
		if want := "exempt (stub, owned by SP-09): negknow"; why != want {
			t.Errorf("reason = %q, want %q", why, want)
		}
	})

	t.Run("a landed owner is on the floor", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "eval.go"), "package eval\n\nfunc Load() {}\n")
		row := ownerRow{Package: "eval", Owner: "SP-02", Floor: 85, Probe: "Load"}
		applies, why := floorApplies(row, dir)
		if !applies {
			t.Errorf("SP-02 has landed, so internal/eval is measured; got exemption %q", why)
		}
		if why != "" {
			t.Errorf("an applicable floor has no exemption reason, got %q", why)
		}
	})
}

// TestLandedSubplansMatchesTheBranch keeps the landed set honest. Every subplan that has landed
// owns at least one package that is no longer a stub, so a set that drifts ahead of the branch
// gates a package nobody has written yet — and one that drifts behind silently unmeasures a
// package that shipped.
func TestLandedSubplansMatchesTheBranch(t *testing.T) {
	owners, err := loadOwners(filepath.Join(testModuleRoot(t), "plans", "OWNERS.tsv"))
	if err != nil {
		t.Fatalf("loadOwners: %v", err)
	}
	known := map[string]bool{}
	for _, o := range owners {
		known[o.Owner] = true
	}
	for id := range landedSubplans {
		if !known[id] {
			t.Errorf("landedSubplans names %s, which owns nothing in plans/OWNERS.tsv", id)
		}
	}
	// The missing-entry half. Wave 0 plus every wave-1 subplan merged so far must be listed, or the
	// §6.4 floor of every package it owns is exempt at any coverage, including 0%.
	//
	// This assertion used to read the other way for SP-03 — "SP-03 has not landed; internal/sketch
	// is still a stub" — which was right while it was a tripwire and reads backwards the moment the
	// wave lands. SP-02's handoff §4.1 asked for it to be rewritten here rather than deleted,
	// because the set still has to keep agreeing with the branch for waves 2 through 6.
	for _, id := range []string{"SP-01", "SP-02", "SP-03", "SP-04", "SP-05", "SP-06", "SP-07"} {
		if !landedSubplans[id] {
			t.Errorf("%s has landed on develop but is missing from landedSubplans, so every "+
				"package it owns is exempt from its §6.4 floor at any coverage, including 0%%", id)
		}
	}
	// The tripwire half, kept in the same breath as the half above: listing a subplan early binds
	// a §6.4 floor against code that is still a stub.
	//
	// Wave 2 is now cut. SP-08 left this list in commit ba477c5, which made internal/observer's
	// probe real — it is listed as landed above, from its own branch rather than from a merge
	// commit, because cover's exempt-but-real cross-check fails the moment a probe goes real while
	// its subplan is unlisted. SP-09 has not landed and stays here.
	for _, id := range []string{"SP-09"} {
		if landedSubplans[id] {
			t.Errorf("%s is listed as landed, but it has not landed yet", id)
		}
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
