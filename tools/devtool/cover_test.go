package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/obs"
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

	// fsck is SP-17's (packaging and hardening, wave 6) and stays a stub until its wave. This case
	// used SP-03/sketch while SP-03 was unlanded, SP-09/negknow until SP-09 landed, and
	// SP-14/commands until V5-VERIFY listed the wave-4 subplans — each rotation asserting an
	// exemption that stopped existing the moment the owner landed. Any still-unlanded owner works —
	// the subject here is the reason string, not the package — but it has to be one that is still a
	// stub, or the case passes for the wrong reason.
	t.Run("an unlanded owner is exempt and the log names it", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "fsck.go"), "package fsck\n\nfunc Run() {}\n")
		row := ownerRow{Package: "fsck", Owner: "SP-17", Floor: 75, Probe: "Run"}
		applies, why := floorApplies(row, dir)
		if applies {
			t.Error("a stub's coverage number measures nothing, so its floor cannot bind yet")
		}
		if want := "exempt (stub, owned by SP-17): fsck"; why != want {
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
	// The missing-entry half. Wave 0, every wave-1 subplan merged so far, both wave-2 subplans
	// (SP-08 landed on develop; SP-09 lands in this branch's merge) and all four wave-3 subplans
	// must be listed, or the §6.4 floor of every package it owns is exempt at any coverage,
	// including 0%.
	//
	// This assertion used to read the other way for SP-03 — "SP-03 has not landed; internal/sketch
	// is still a stub" — which was right while it was a tripwire and reads backwards the moment the
	// wave lands. SP-02's handoff §4.1 asked for it to be rewritten here rather than deleted,
	// because the set still has to keep agreeing with the branch for waves 2 through 6.
	//
	// SP-12 joins the list from its OWN branch, the way SP-08 did rather than the way SP-06 and
	// SP-07 did: commit 3 of feat/sp12-scheduler-l3 makes internal/scheduler's probe Evaluate a
	// real implementation, and cover.go's exempt-but-real cross-check fires the moment a probe
	// stops looking like a stub while its subplan is unlisted — so the entry cannot wait for the
	// merge. SP-10 and SP-11 joined from their own branches too, at the wave-3 integration, and
	// SP-13 listed itself in its first commit, when internal/mcp's probe went real. SP-20 listed
	// itself from its own branch; SP-14, SP-15 and SP-21 were listed by V5-VERIFY after the wave-4
	// integration had merged them unlisted (their probes were real, their floors silently off).
	for _, id := range []string{"SP-01", "SP-02", "SP-03", "SP-04", "SP-05", "SP-06", "SP-07", "SP-08", "SP-09", "SP-10", "SP-11", "SP-12", "SP-13", "SP-20", "SP-14", "SP-15", "SP-21"} {
		if !landedSubplans[id] {
			t.Errorf("%s has landed on develop but is missing from landedSubplans, so every "+
				"package it owns is exempt from its §6.4 floor at any coverage, including 0%%", id)
		}
	}
	// The tripwire half, kept in the same breath as the half above: listing a subplan early binds
	// a §6.4 floor against code that is still a stub.
	//
	// Wave 2 has fully landed — SP-08 left this list in commit ba477c5 (its probe went real on its
	// own branch), and SP-09 leaves it in the merge that lands feat/sp09-negative-knowledge. Wave 3
	// has fully landed too — SP-12 and SP-13 left this list from their own branches, SP-10 and
	// SP-11 at the wave-3 integration (SP-19 M0-00) — so the list is empty until a wave-4 subplan
	// needs pinning. The exempt-but-real cross-check in cover.go is the other direction, and still
	// fails any probe that goes real while its subplan is unlisted.
	for _, id := range []string{} {
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

// TestCoverPasses_RunsE2EAloneWithoutTheColoadDeclaration pins cover's `go test` passes. Every
// package but test/e2e runs in one parallel pass that declares co-load and never the non-reference
// disk (one cause per run, test/guards' TestNonReferenceDisk_IsHostedCIOnly). test/e2e then runs
// alone with the co-load declaration taken back, as ADR 0010 decision 4 and ci.yml's `test-e2e` job
// run it: inside the whole-tree pass on ubuntu-latest its live-path rows' hooks took the designed
// degrade to the client spool (job 103834108633), and TestE2EHookRoundTrip, which counts the WAL,
// failed cover in runs 34804619564 and 36816905394 while the latter's `test-e2e` passed it on all
// three OSes.
func TestCoverPasses_RunsE2EAloneWithoutTheColoadDeclaration(t *testing.T) {
	e2e := modulePath + "/test/e2e"
	pkgs := []string{modulePath + "/internal/core", e2e, modulePath + "/test/guards"}
	passes := coverPasses(pkgs)
	if len(passes) != 2 {
		t.Fatalf("got %d passes, want 2: %+v", len(passes), passes)
	}

	shared, alone := passes[0], passes[1]
	if got, want := strings.Join(shared.pkgs, " "), modulePath+"/internal/core "+modulePath+"/test/guards"; got != want {
		t.Fatalf("shared pass = %q, want %q", got, want)
	}
	if v, ok := shared.env[obs.UnderColoadEnv]; !ok || v == "" {
		t.Fatalf("the shared pass must declare %s; env = %v", obs.UnderColoadEnv, shared.env)
	}
	if v, ok := shared.env[obs.NonReferenceDiskEnv]; !ok || v != "" {
		t.Fatalf("the co-loaded pass must take back an inherited %s (set it to empty); env = %v",
			obs.NonReferenceDiskEnv, shared.env)
	}
	if shared.profile != coverProfileName {
		t.Fatalf("the shared pass writes %q, want %q", shared.profile, coverProfileName)
	}

	if got := strings.Join(alone.pkgs, " "); got != e2e {
		t.Fatalf("isolated pass = %q, want %q", got, e2e)
	}
	if v, ok := alone.env[obs.UnderColoadEnv]; !ok || v != "" {
		t.Fatalf("test/e2e alone is not co-loaded: the pass must take %s back (set it to empty); env = %v",
			obs.UnderColoadEnv, alone.env)
	}
	if _, ok := alone.env[obs.NonReferenceDiskEnv]; ok {
		t.Fatalf("the isolated pass must inherit the job's %s, not set it; env = %v", obs.NonReferenceDiskEnv, alone.env)
	}
	if alone.profile == coverProfileName || alone.profile == "" {
		t.Fatalf("the isolated pass needs a profile of its own, got %q", alone.profile)
	}

	if got := coverPasses([]string{modulePath + "/internal/core"}); len(got) != 1 {
		t.Fatalf("a tree without test/e2e is one pass; got %+v", got)
	}
}

// TestCoverPasses_GiveTheIsolatedPassItsOwnHangGuard pins the -timeout of cover's `go test`
// passes. cover runs test/e2e alone and instrumented, so it is slower than the 1513.7 s and
// 1529.5 s the binary takes uninstrumented on the quiet Windows reference host (a phase3 night
// measured cover's pass at 995 s against 918 s uninstrumented). The isolated pass gets the 45m hang
// guard ci.yml's test-e2e gives the same binary (audit 2's #84); the shared pass keeps 30m.
func TestCoverPasses_GiveTheIsolatedPassItsOwnHangGuard(t *testing.T) {
	e2e := modulePath + "/test/e2e"
	core, guards := modulePath+"/internal/core", modulePath+"/test/guards"
	passes := coverPasses([]string{core, e2e, guards})
	if len(passes) != 2 {
		t.Fatalf("got %d passes, want 2: %+v", len(passes), passes)
	}
	want := [][]string{
		{
			"test", "-timeout=30m", "-coverprofile=" + coverProfileName, "-covermode=" + coverMode,
			core, guards,
		},
		{"test", "-timeout=45m", "-coverprofile=" + passes[1].profile, "-covermode=" + coverMode, e2e},
	}
	for i, pass := range passes {
		if got := coverTestArgs(pass); strings.Join(got, " ") != strings.Join(want[i], " ") {
			t.Fatalf("pass %d runs %q, want %q", i, got, want[i])
		}
	}
}

// TestAppendCoverProfile_MergesBlocksUnderOneModeLine pins how cover joins the isolated pass's
// profile onto the shared one: the blocks are appended, the second "mode:" line is not (go tool
// cover and parseCoverProfile both read one header), and a mode mismatch is refused, not merged.
func TestAppendCoverProfile_MergesBlocksUnderOneModeLine(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "coverage.out")
	src := filepath.Join(dir, "coverage-e2e.out")
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(dst, "mode: atomic\ngithub.com/qompack/qompack/internal/core/hash.go:10.2,12.3 2 1\n")
	write(src, "mode: atomic\ngithub.com/qompack/qompack/test/e2e/harness.go:5.2,7.3 4 0\n")
	if err := appendCoverProfile(dst, src); err != nil {
		t.Fatalf("appendCoverProfile: %v", err)
	}
	stats, err := parseCoverProfile(dst)
	if err != nil {
		t.Fatalf("parseCoverProfile: %v", err)
	}
	if s := stats[modulePath+"/internal/core"]; s.total != 2 || s.covered != 2 {
		t.Fatalf("internal/core = %+v, want 2/2", s)
	}
	if s := stats[modulePath+"/test/e2e"]; s.total != 4 || s.covered != 0 {
		t.Fatalf("test/e2e = %+v, want 0/4", s)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "mode:"); n != 1 {
		t.Fatalf("merged profile has %d mode lines, want 1:\n%s", n, b)
	}

	write(src, "mode: set\ngithub.com/qompack/qompack/test/e2e/harness.go:5.2,7.3 4 0\n")
	if err := appendCoverProfile(dst, src); err == nil {
		t.Fatal("a profile of another mode must be refused, not merged")
	}
}
