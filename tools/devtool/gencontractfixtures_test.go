package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderManifest_ByteIdentical is the assertion that makes --record safe to run against a tree
// of frozen fixtures: decoding every committed MANIFEST.json and re-rendering it must reproduce
// the original bytes exactly. If it does not, a --record run would rewrite manifests it never
// touched and the resulting diff would hide the one line that actually changed.
func TestRenderManifest_ByteIdentical(t *testing.T) {
	repo := testModuleRoot(t)
	prev := root
	root = repo
	t.Cleanup(func() { root = prev })

	pkgs, err := contractPackages()
	if err != nil {
		t.Fatalf("contractPackages: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no contract fixture packages found; testdata/golden/contracts must not be empty")
	}

	for _, pkg := range pkgs {
		m, path, err := readManifest(pkg)
		if err != nil {
			t.Fatalf("%s: %v", pkg, err)
		}
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", pkg, err)
		}
		if got := string(renderManifest(m)); got != string(original) {
			t.Errorf("renderManifest(%s) is not byte-identical to the committed file.\n got:\n%s\nwant:\n%s",
				pkg, got, original)
		}
	}
}

// TestVerifyFixtures_AcceptsTheFrozenSet asserts the no-argument mode passes against the committed
// tree and writes nothing. It is the mode CI runs, and a failure here means a manifest disagrees
// with plans/OWNERS.tsv or names a file that is not on disk.
func TestVerifyFixtures_AcceptsTheFrozenSet(t *testing.T) {
	repo := testModuleRoot(t)
	prev := root
	root = repo
	t.Cleanup(func() { root = prev })

	owners, err := loadOwners(filepath.Join(root, "plans", "OWNERS.tsv"))
	if err != nil {
		t.Fatalf("loadOwners: %v", err)
	}
	if err := verifyFixtures(owners); err != nil {
		t.Fatalf("verifyFixtures against the committed tree: %v", err)
	}
}

// TestCheckManifest_RejectsViolations drives checkManifest over the four §16 invariants that a
// hand-edited manifest can break.
func TestCheckManifest_RejectsViolations(t *testing.T) {
	owners := map[string]ownerRow{"store": {Package: "store", Owner: "SP-06", Floor: 90, Probe: "PutBytes"}}

	tests := []struct {
		name string
		m    manifest
		want int
	}{
		{
			name: "clean",
			m: manifest{Package: "store", Owner: "SP-06", Fixtures: []fixtureEntry{
				{Name: "a", Kind: kindBehaviour, State: statePending},
			}},
			want: 0,
		},
		{
			name: "package field disagrees with its directory",
			m:    manifest{Package: "negknow", Owner: "SP-06"},
			want: 1,
		},
		{
			name: "owner disagrees with OWNERS.tsv",
			m:    manifest{Package: "store", Owner: "SP-01"},
			want: 1,
		},
		{
			name: "a format fixture may not be pending",
			m: manifest{Package: "store", Owner: "SP-06", Fixtures: []fixtureEntry{
				{Name: "a", Kind: kindFormat, State: statePending},
			}},
			want: 1,
		},
		{
			name: "unknown kind and state each count once",
			m: manifest{Package: "store", Owner: "SP-06", Fixtures: []fixtureEntry{
				{Name: "a", Kind: "guess", State: "maybe"},
			}},
			want: 2,
		},
		{
			name: "a duplicate fixture name",
			m: manifest{Package: "store", Owner: "SP-06", Fixtures: []fixtureEntry{
				{Name: "a", Kind: kindBehaviour, State: statePending},
				{Name: "a", Kind: kindBehaviour, State: statePending},
			}},
			want: 1,
		},
		{
			name: "a frozen fixture naming a file that is not there",
			m: manifest{Package: "store", Owner: "SP-06", Fixtures: []fixtureEntry{
				{Name: "a", Kind: kindFormat, State: stateFrozen, Want: "want/nope.jsonl"},
			}},
			want: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store", manifestFile)
			got := checkManifest(tc.m, "store", path, owners)
			if len(got) != tc.want {
				t.Fatalf("got %d problem(s), want %d: %v", len(got), tc.want, got)
			}
		})
	}
}

// TestReturnsNotImplemented asserts the source-level stub probe: the shape every SP-01 stub has
// reads as a stub, and a real body does not.
func TestReturnsNotImplemented(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want bool
	}{
		{"bare sentinel", `package p
import "x/core"
func (s) Probe() error { return core.ErrNotImplemented }`, true},
		{"wrapped sentinel", `package p
import ("fmt"; "x/core")
func (s) Probe() error { return fmt.Errorf("%w: soon", core.ErrNotImplemented) }`, true},
		{"zero value plus sentinel", `package p
import "x/core"
func (s) Probe() (Result, error) { return Result{}, core.ErrNotImplemented }`, true},
		{"a real implementation", `package p
func (s) Probe() error { return s.write() }`, false},
		{"a different sentinel", `package p
import "x/core"
func (s) Probe() error { return core.ErrBudget }`, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := parseProbeBody(t, tc.src)
			if got := returnsNotImplemented(body); got != tc.want {
				t.Fatalf("returnsNotImplemented = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIsStubPackage drives the Rule W-2 gate over the three shapes a probe can have in this
// repository. It builds its own tiny tree rather than reading internal/, so it is a statement
// about the rule and not about whichever packages happen to have landed today.
func TestIsStubPackage(t *testing.T) {
	tests := []struct {
		name    string
		sources map[string]string
		probe   string
		want    bool
		wantErr bool
	}{
		{
			name:    "every declaration is a stub",
			sources: map[string]string{"a.go": "package p\nimport \"x/core\"\nfunc (s) Probe() error { return core.ErrNotImplemented }\n"},
			probe:   "Probe",
			want:    true,
		},
		{
			name: "one real declaration lifts the refusal",
			sources: map[string]string{
				"a.go": "package p\nimport \"x/core\"\nfunc (s) Probe() error { return core.ErrNotImplemented }\n",
				"b.go": "package p\nfunc (t) Probe() error { return nil }\n",
			},
			probe: "Probe",
			want:  false,
		},
		{
			name:    "the probe exists only as an interface method",
			sources: map[string]string{"a.go": "package p\ntype I interface{ Probe() error }\n"},
			probe:   "Probe",
			want:    true,
		},
		{
			name:    "a _test.go declaration does not count",
			sources: map[string]string{"a_test.go": "package p\nfunc (s) Probe() error { return nil }\n"},
			probe:   "Probe",
			wantErr: true,
		},
		{
			name:    "the probe appears nowhere",
			sources: map[string]string{"a.go": "package p\nfunc (s) Other() error { return nil }\n"},
			probe:   "Probe",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prev := root
			root = t.TempDir()
			t.Cleanup(func() { root = prev })

			dir := filepath.Join(root, "internal", "fake")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, src := range tc.sources {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			got, err := isStubPackage("fake", tc.probe)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want an error, got stub=%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("isStubPackage: %v", err)
			}
			if got != tc.want {
				t.Fatalf("isStubPackage = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRecordFixtures_RefusesAStub asserts the Rule W-2 gate itself: --record on a package whose
// probe still reports core.ErrNotImplemented must refuse, naming the package, the probe and the
// owner, and must not touch the manifest.
func TestRecordFixtures_RefusesAStub(t *testing.T) {
	prev := root
	root = t.TempDir()
	t.Cleanup(func() { root = prev })

	pkgDir := filepath.Join(root, "internal", "fake")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "fake.go"),
		[]byte("package fake\nimport \"x/core\"\nfunc (s) Probe() error { return core.ErrNotImplemented }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixtureDir := filepath.Join(root, filepath.FromSlash(contractsRel), "fake")
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	before := renderManifest(manifest{
		Package: "fake", Owner: "SP-99",
		Fixtures: []fixtureEntry{{Name: "a", Kind: kindBehaviour, State: statePending}},
	})
	manifestPath := filepath.Join(fixtureDir, manifestFile)
	if err := os.WriteFile(manifestPath, before, 0o644); err != nil {
		t.Fatal(err)
	}

	owners := []ownerRow{{Package: "fake", Owner: "SP-99", Floor: 75, Probe: "Probe"}}
	err := recordFixtures("fake", owners)
	if err == nil {
		t.Fatal("--record must refuse while the implementation is a stub (Rule W-2)")
	}
	for _, want := range []string{"fake", "Probe", "SP-99", "Rule W-2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q; got: %v", want, err)
		}
	}

	after, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(before) {
		t.Error("a refused --record must leave the manifest untouched")
	}
}

// TestPromote_FlipsOnlyWhatWasRecorded asserts promote fills in a recorded fixture's want path and
// freezes it, leaves an already-frozen fixture alone, and reports the ones with no bytes on disk.
func TestPromote_FlipsOnlyWhatWasRecorded(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "want"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "want", "recorded.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := manifest{Package: "fake", Owner: "SP-99", Fixtures: []fixtureEntry{
		{Name: "already", Kind: kindFormat, State: stateFrozen, Want: "want/already.jsonl"},
		{Name: "recorded", Kind: kindBehaviour, State: statePending},
		{Name: "absent", Kind: kindBehaviour, State: statePending},
	}}

	recorded, missing := promote(&m, dir, []string{"recorded", "absent"})

	if len(recorded) != 1 || recorded[0] != "recorded" {
		t.Fatalf("recorded = %v, want [recorded]", recorded)
	}
	if len(missing) != 1 || missing[0] != "absent" {
		t.Fatalf("missing = %v, want [absent]", missing)
	}
	if m.Fixtures[0].Want != "want/already.jsonl" || m.Fixtures[0].State != stateFrozen {
		t.Error("an already-frozen fixture must not be rewritten")
	}
	if m.Fixtures[1].Want != "want/recorded.jsonl" || m.Fixtures[1].State != stateFrozen {
		t.Errorf("the recorded fixture must be frozen with its want path filled in: %+v", m.Fixtures[1])
	}
	if m.Fixtures[2].State != statePending {
		t.Error("a fixture with no recorded bytes must stay record-by-owner")
	}
}

// parseProbeBody parses a one-declaration source fixture and returns the body of the func named
// Probe, so returnsNotImplemented can be driven over real syntax rather than a hand-built AST.
func parseProbeBody(t *testing.T, src string) *ast.BlockStmt {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "probe.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "Probe" {
			return fn.Body
		}
	}
	t.Fatal("the fixture declares no func named Probe")
	return nil
}

// TestJSONString_NoHTMLEscaping asserts a note containing "<" or "&" round-trips as itself, which
// is what keeps a re-rendered manifest byte-identical to a hand-authored one.
func TestJSONString_NoHTMLEscaping(t *testing.T) {
	got := jsonString(`a<b&c "quoted" §`)
	want := `"a<b&c \"quoted\" §"`
	if got != want {
		t.Fatalf("jsonString = %s, want %s", got, want)
	}
	var back string
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("the rendered literal must be valid JSON: %v", err)
	}
	if back != `a<b&c "quoted" §` {
		t.Fatalf("round-trip changed the value: %q", back)
	}
}
