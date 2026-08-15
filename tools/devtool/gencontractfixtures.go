package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// contractsRel is testdata/golden/contracts, the §16 tree: one directory per package, each with a
// MANIFEST.json and the input/ and want/ files that manifest names.
const contractsRel = "testdata/golden/contracts"

// manifestFile is the per-package fixture index inside a contractsRel subdirectory.
const manifestFile = "MANIFEST.json"

// The two fixture kinds of §16. A format fixture describes a byte layout §5/§8.5 already
// specifies, so SP-01 hand-authored and froze it. A behaviour fixture describes what a real
// implementation produces, so only the owning subplan can record it.
const (
	kindFormat    = "format"
	kindBehaviour = "behaviour"
)

// The two fixture states. A frozen fixture is byte-final for every consumer in every later wave;
// record-by-owner means the owning subplan has not run --record yet, and consumers skip on it.
const (
	stateFrozen   = "frozen"
	statePending  = "record-by-owner"
	notRecordedW2 = "contract fixture not yet recorded (Rule W-2)"
)

// recorderTest is the test the owning subplan writes in its own package, and recorderEnv is how
// this task tells it where to write.
//
// §16 gives --record three jobs: refuse while the implementation is a stub, write the want/ file,
// and flip the state to frozen. The first and third are decisions about the repository, which is
// exactly what a task runner is for; the second is the package's own behaviour, which devtool
// cannot produce without importing internal/<pkg> — an edge that would make the task runner part
// of the dependency graph it exists to police. So devtool owns the gate and the promotion, and
// the owning package owns the bytes, connected by this convention.
const (
	recorderTest = "^TestRecordContractFixtures$"
	recorderEnv  = "QOMPACK_RECORD_CONTRACTS"
)

// fixtureEntry is one element of a manifest's fixtures array (§16).
type fixtureEntry struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Input string `json:"input"`
	Want  string `json:"want"`
	Note  string `json:"note"`
}

// manifest is one package's testdata/golden/contracts/<pkg>/MANIFEST.json.
type manifest struct {
	Package  string         `json:"package"`
	Owner    string         `json:"owner"`
	Fixtures []fixtureEntry `json:"fixtures"`
}

// taskGenContractFixtures maintains testdata/golden/contracts/** (implementation spec §16).
//
// With no arguments it VERIFIES and writes nothing: every manifest is checked against
// plans/OWNERS.tsv and against what is actually on disk, and the frozen set is reported. That is
// the mode CI and a routine `devtool gen-contract-fixtures` run in, and it is a no-op by design —
// a frozen fixture that a regeneration could rewrite would not be frozen.
//
// With --record <pkg> it records the behaviour fixtures that package owns: refuse while the
// implementation is still a stub (Rule W-2 exists to catch an implementation that cannot reproduce
// a fixture, which a stub trivially cannot), run the package's recorder test, then promote each
// fixture whose want/ file now exists to "frozen".
func taskGenContractFixtures(args []string) error {
	fs := flag.NewFlagSet("gen-contract-fixtures", flag.ContinueOnError)
	record := fs.String("record", "", "package name whose behaviour fixtures to record (refuses while that package is still a stub)")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}

	owners, err := loadOwners(filepath.Join(root, "plans", "OWNERS.tsv"))
	if err != nil {
		return fmt.Errorf("gen-contract-fixtures: %w", err)
	}

	if *record != "" {
		return recordFixtures(*record, owners)
	}
	return verifyFixtures(owners)
}

// verifyFixtures checks every manifest in the tree and writes nothing.
func verifyFixtures(owners []ownerRow) error {
	dirs, err := contractPackages()
	if err != nil {
		return fmt.Errorf("gen-contract-fixtures: %w", err)
	}
	if len(dirs) == 0 {
		fmt.Println("gen-contract-fixtures: no " + contractsRel + " tree yet; nothing to verify")
		return nil
	}

	ownerByPkg := ownerIndex(owners)

	var problems []string
	var frozen, pending int
	for _, pkg := range dirs {
		m, path, err := readManifest(pkg)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		problems = append(problems, checkManifest(m, pkg, path, ownerByPkg)...)
		for _, fx := range m.Fixtures {
			if fx.State == stateFrozen {
				frozen++
				continue
			}
			pending++
		}
	}

	fmt.Printf("gen-contract-fixtures: %d package(s), %d frozen fixture(s), %d awaiting --record\n",
		len(dirs), frozen, pending)
	if pending > 0 {
		fmt.Printf("  consumers of an unrecorded fixture skip with %q\n", notRecordedW2)
	}
	if len(problems) == 0 {
		fmt.Println("gen-contract-fixtures: OK (nothing written; the frozen set is byte-final)")
		return nil
	}
	sort.Strings(problems)
	for _, p := range problems {
		fmt.Println("  " + p)
	}
	return fmt.Errorf("gen-contract-fixtures: %d problem(s)", len(problems))
}

// checkManifest applies §16's invariants to one manifest and returns one message per violation.
func checkManifest(m manifest, pkg, path string, ownerByPkg map[string]ownerRow) []string {
	var problems []string
	rel := relOrSelf(root, path)

	if m.Package != pkg {
		problems = append(problems, fmt.Sprintf("%s: package field %q does not match its directory %q", rel, m.Package, pkg))
	}
	row, known := ownerByPkg[pkg]
	switch {
	case !known:
		problems = append(problems, fmt.Sprintf("%s: %q is not a package in plans/OWNERS.tsv", rel, pkg))
	case m.Owner != row.Owner:
		problems = append(problems, fmt.Sprintf("%s: owner %q disagrees with plans/OWNERS.tsv (%s)", rel, m.Owner, row.Owner))
	}

	seen := make(map[string]bool, len(m.Fixtures))
	for _, fx := range m.Fixtures {
		if fx.Name == "" {
			problems = append(problems, rel+": a fixture has no name")
			continue
		}
		if seen[fx.Name] {
			problems = append(problems, fmt.Sprintf("%s: fixture %q is declared twice", rel, fx.Name))
		}
		seen[fx.Name] = true

		if fx.Kind != kindFormat && fx.Kind != kindBehaviour {
			problems = append(problems, fmt.Sprintf("%s: fixture %q has kind %q; want %q or %q", rel, fx.Name, fx.Kind, kindFormat, kindBehaviour))
		}
		if fx.State != stateFrozen && fx.State != statePending {
			problems = append(problems, fmt.Sprintf("%s: fixture %q has state %q; want %q or %q", rel, fx.Name, fx.State, stateFrozen, statePending))
		}
		if fx.Kind == kindFormat && fx.State != stateFrozen {
			problems = append(problems, fmt.Sprintf(
				"%s: fixture %q is a format fixture and must be frozen: §5/§8.5 specify its bytes, so nothing about it is waiting on an implementation",
				rel, fx.Name))
		}

		// Only a frozen fixture's files must exist. A record-by-owner fixture may legitimately
		// name an input its owning subplan has not written yet.
		if fx.State != stateFrozen {
			continue
		}
		for role, name := range map[string]string{"input": fx.Input, "want": fx.Want} {
			if name == "" {
				continue
			}
			full := filepath.Join(filepath.Dir(path), filepath.FromSlash(name))
			if fi, err := os.Stat(full); err != nil || fi.Size() == 0 {
				problems = append(problems, fmt.Sprintf("%s: frozen fixture %q names %s file %q, which is missing or empty",
					rel, fx.Name, role, name))
			}
		}
	}
	return problems
}

// recordFixtures implements --record <pkg>.
func recordFixtures(pkg string, owners []ownerRow) error {
	row, ok := ownerIndex(owners)[pkg]
	if !ok {
		return fmt.Errorf("gen-contract-fixtures: %q is not a package in plans/OWNERS.tsv", pkg)
	}

	m, path, err := readManifest(pkg)
	if err != nil {
		return fmt.Errorf("gen-contract-fixtures: %w", err)
	}
	dir := filepath.Dir(path)

	pending := pendingFixtures(m)
	if len(pending) == 0 {
		fmt.Printf("gen-contract-fixtures: %s has no fixture awaiting --record; nothing to do\n", pkg)
		return nil
	}

	// The Rule W-2 gate. A stub reproduces nothing, so a fixture recorded from one would freeze
	// the stub's own output as the contract — which is the exact failure Rule W-2 exists to
	// prevent ("a fixture the real implementation cannot reproduce is a verification failure, not
	// a fixture bug").
	if row.Probe == "-" {
		return fmt.Errorf("gen-contract-fixtures: %s has no stub phase in plans/OWNERS.tsv, so it has no behaviour to record", pkg)
	}
	stub, err := isStubPackage(pkg, row.Probe)
	if err != nil {
		return fmt.Errorf("gen-contract-fixtures: %w", err)
	}
	if stub {
		return fmt.Errorf(
			"gen-contract-fixtures: refusing to record %s: internal/%s.%s still reports core.ErrNotImplemented. "+
				"%s owns this package; record only once the real implementation lands (Rule W-2)",
			pkg, pkg, row.Probe, row.Owner)
	}

	if err := runRecorder(pkg, dir); err != nil {
		return fmt.Errorf("gen-contract-fixtures: %w", err)
	}

	recorded, missing := promote(&m, dir, pending)
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf(
			"gen-contract-fixtures: %s's recorder produced no want/ file for: %s.\n"+
				"internal/%s must define %s, writing each fixture's bytes to <%s>/want/<fixture name>.<ext>",
			pkg, strings.Join(missing, ", "), pkg, strings.Trim(recorderTest, "^$"), recorderEnv)
	}

	if err := writeManifest(path, m); err != nil {
		return fmt.Errorf("gen-contract-fixtures: %w", err)
	}
	for _, name := range recorded {
		fmt.Printf("gen-contract-fixtures: recorded %s/%s and froze it\n", pkg, name)
	}
	return nil
}

// pendingFixtures returns the names of every fixture in m still awaiting its owner, in manifest
// order.
func pendingFixtures(m manifest) []string {
	var names []string
	for _, fx := range m.Fixtures {
		if fx.State != stateFrozen {
			names = append(names, fx.Name)
		}
	}
	return names
}

// runRecorder runs the owning package's recorder test with recorderEnv pointing at its fixture
// directory. A package with no such test is not an error here: go test exits 0 having matched
// nothing, and promote's "no want file appeared" message is the one that actually explains what
// the owner has to write.
func runRecorder(pkg, dir string) error {
	pkgDir := filepath.Join(root, "internal", pkg)
	if !dirHasGoFiles(pkgDir) {
		return fmt.Errorf("internal/%s has no Go files", pkg)
	}
	_, stderr, err := runCapture(
		map[string]string{recorderEnv: dir},
		"go", "test", "-count=1", "-run", recorderTest, "./internal/"+pkg+"/...",
	)
	if err != nil {
		return fmt.Errorf("running %s in internal/%s: %w\n%s", strings.Trim(recorderTest, "^$"), pkg, err, stderr)
	}
	return nil
}

// promote fills in each pending fixture's want path and flips it to frozen, and reports which
// fixtures the recorder did not produce a file for.
//
// A fixture that already names its want file keeps that name; one that does not — the shape every
// record-by-owner entry has today, with an empty want — is matched against want/ by base name, so
// the recorder chooses the extension its bytes deserve (.json, .jsonl, .txt) without the manifest
// having to predict it.
func promote(m *manifest, dir string, pending []string) (recorded, missing []string) {
	wantDir := filepath.Join(dir, "want")
	byBase := map[string]string{}
	if entries, err := os.ReadDir(wantDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			byBase[strings.TrimSuffix(name, filepath.Ext(name))] = name
		}
	}

	pendingSet := make(map[string]bool, len(pending))
	for _, n := range pending {
		pendingSet[n] = true
	}

	for i := range m.Fixtures {
		fx := &m.Fixtures[i]
		if !pendingSet[fx.Name] {
			continue
		}
		rel := fx.Want
		if rel == "" {
			base, ok := byBase[fx.Name]
			if !ok {
				missing = append(missing, fx.Name)
				continue
			}
			rel = "want/" + base
		}
		if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil || fi.Size() == 0 {
			missing = append(missing, fx.Name)
			continue
		}
		fx.Want = rel
		fx.State = stateFrozen
		recorded = append(recorded, fx.Name)
	}
	return recorded, missing
}

// isStubPackage reports whether internal/<pkg>'s probe operation still reports
// core.ErrNotImplemented.
//
// It is a source scan rather than a call, because devtool must not import internal/<pkg>: doing so
// would put the task runner inside the dependency graph its own importgraph sub-check polices.
//
// Three cases, in order:
//
//   - The package declares the probe concretely. It is a stub only when EVERY such declaration
//     returns core.ErrNotImplemented, so a half-landed implementation reads as landed and the
//     refusal lifts rather than blocking an owner mid-migration.
//   - The probe exists only as an interface method — the shape of a package whose wave-0 stub type
//     does not implement it at all yet. Nothing can produce bytes, so it is a stub.
//   - The name appears nowhere. That is a typo in plans/OWNERS.tsv, not a fact about the code, and
//     it is reported as an error rather than guessed at.
func isStubPackage(pkg, probe string) (bool, error) {
	files, err := parsePackageFiles(pkg)
	if err != nil {
		return false, err
	}

	concrete, stubs := 0, 0
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != probe || fn.Body == nil {
				continue
			}
			concrete++
			if returnsNotImplemented(fn.Body) {
				stubs++
			}
		}
	}
	if concrete > 0 {
		return stubs == concrete, nil
	}
	if declaresInterfaceMethod(files, probe) {
		return true, nil
	}
	return false, fmt.Errorf("internal/%s declares no %s, but plans/OWNERS.tsv names it as the stub probe", pkg, probe)
}

// parsePackageFiles parses every non-test Go file directly under internal/<pkg>.
func parsePackageFiles(pkg string) ([]*ast.File, error) {
	dir := filepath.Join(root, "internal", pkg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading internal/%s: %w", pkg, err)
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if perr != nil {
			return nil, fmt.Errorf("parsing internal/%s/%s: %w", pkg, e.Name(), perr)
		}
		files = append(files, f)
	}
	return files, nil
}

// declaresInterfaceMethod reports whether any interface type in files declares a method named name.
func declaresInterfaceMethod(files []*ast.File, name string) bool {
	for _, f := range files {
		found := false
		ast.Inspect(f, func(n ast.Node) bool {
			it, ok := n.(*ast.InterfaceType)
			if !ok || it.Methods == nil {
				return true
			}
			for _, m := range it.Methods.List {
				for _, id := range m.Names {
					if id.Name == name {
						found = true
					}
				}
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// returnsNotImplemented reports whether body contains a return statement whose value is
// core.ErrNotImplemented (directly, or wrapped in a call such as fmt.Errorf).
func returnsNotImplemented(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, res := range ret.Results {
			ast.Inspect(res, func(inner ast.Node) bool {
				sel, ok := inner.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if ok && ident.Name == "core" && sel.Sel.Name == "ErrNotImplemented" {
					found = true
				}
				return true
			})
		}
		return true
	})
	return found
}

// contractPackages returns the package directory names under testdata/golden/contracts, sorted.
func contractPackages() ([]string, error) {
	dir := filepath.Join(root, filepath.FromSlash(contractsRel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var pkgs []string
	for _, e := range entries {
		if e.IsDir() {
			pkgs = append(pkgs, e.Name())
		}
	}
	sort.Strings(pkgs)
	return pkgs, nil
}

// readManifest decodes one package's MANIFEST.json and returns it with its path.
func readManifest(pkg string) (manifest, string, error) {
	path := filepath.Join(root, filepath.FromSlash(contractsRel), pkg, manifestFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, path, fmt.Errorf("reading %s: %w", relOrSelf(root, path), err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return manifest{}, path, fmt.Errorf("parsing %s: %w", relOrSelf(root, path), err)
	}
	return m, path, nil
}

// ownerIndex keys plans/OWNERS.tsv by package name.
func ownerIndex(owners []ownerRow) map[string]ownerRow {
	byPkg := make(map[string]ownerRow, len(owners))
	for _, o := range owners {
		byPkg[o.Package] = o
	}
	return byPkg
}

// writeManifest renders m in the committed layout and replaces path.
func writeManifest(path string, m manifest) error {
	return os.WriteFile(path, renderManifest(m), 0o644)
}

// renderManifest emits a manifest in exactly the layout the 21 frozen fixtures were committed in:
// two-space indentation, one fixture per line with its six keys in a fixed order, LF endings, and
// a trailing newline.
//
// It is hand-rolled rather than json.MarshalIndent because MarshalIndent explodes every fixture
// over six lines, which would rewrite all eight committed manifests the first time --record ran —
// a diff that would bury the one line that actually changed. TestRenderManifest_ByteIdentical
// asserts this function reproduces every committed manifest byte for byte, which is what makes
// --record safe to run against a tree of frozen fixtures.
func renderManifest(m manifest) []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"package\": %s,\n", jsonString(m.Package))
	fmt.Fprintf(&b, "  \"owner\": %s,\n", jsonString(m.Owner))
	if len(m.Fixtures) == 0 {
		b.WriteString("  \"fixtures\": []\n}\n")
		return b.Bytes()
	}
	b.WriteString("  \"fixtures\": [\n")
	for i, fx := range m.Fixtures {
		fmt.Fprintf(&b, "    { \"name\": %s, \"kind\": %s, \"state\": %s, \"input\": %s, \"want\": %s, \"note\": %s }",
			jsonString(fx.Name), jsonString(fx.Kind), jsonString(fx.State),
			jsonString(fx.Input), jsonString(fx.Want), jsonString(fx.Note))
		if i < len(m.Fixtures)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("  ]\n}\n")
	return b.Bytes()
}

// jsonString renders s as a JSON string literal with HTML escaping disabled, so a "<" or "&" in a
// note survives as itself rather than becoming a \u escape.
func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		// encoding/json cannot fail on a string; returning the quoted form keeps this total.
		return "\"\""
	}
	return strings.TrimRight(b.String(), "\n")
}
