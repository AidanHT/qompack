package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// NotRecordedSkip is the ONE message a consumer test may skip with when the contract fixture it
// needs has not been recorded yet (implementation spec §16, Rule W-2).
//
// devtool lint's stubskips sub-check matches this string exactly and hard-fails any skip whose
// reason is none of the three permitted messages, so it is a constant here rather than a literal
// re-typed at each call site:
//
//	input, want, frozen := testutil.ContractFixture(t, "store", "dedup_second_put")
//	if !frozen {
//		t.Skip(testutil.NotRecordedSkip)
//	}
const NotRecordedSkip = "contract fixture not yet recorded (Rule W-2)"

// contractsDir is testdata/golden/contracts/ relative to the repository root: one directory per
// package, each with a MANIFEST.json (implementation spec §16).
const contractsDir = "testdata/golden/contracts"

// manifestName is the per-package fixture index inside contractsDir.
const manifestName = "MANIFEST.json"

// stateFrozen marks a fixture whose want/ bytes are recorded and byte-frozen for every consumer.
// Any other state — in practice "record-by-owner" — means the owning subplan has not run
// `devtool gen-contract-fixtures --record <pkg>` yet.
const stateFrozen = "frozen"

// contractManifest is one package's testdata/golden/contracts/<pkg>/MANIFEST.json.
type contractManifest struct {
	Package  string            `json:"package"`
	Owner    string            `json:"owner"`
	Fixtures []contractFixture `json:"fixtures"`
}

// contractFixture is one entry of a contractManifest's fixtures array. Input and Want are paths
// relative to the package's own fixture directory; either may be empty, and for a behaviour
// fixture awaiting its owner, Want always is.
type contractFixture struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Input string `json:"input"`
	Want  string `json:"want"`
	Note  string `json:"note"`
}

// ContractFixture is the accessor every consumer of a golden contract fixture uses
// (implementation spec §16). It returns the fixture's input bytes, its expected-output bytes, and
// whether the fixture is frozen.
//
// A frozen fixture is byte-final: every consumer, in every wave, asserts against exactly these
// bytes, and a real implementation that cannot reproduce them is a verification failure rather
// than a fixture bug (Rule W-2). When frozen is false the fixture is still record-by-owner, both
// byte slices may be nil, and the consumer must skip with NotRecordedSkip.
//
// A fixture named in no manifest, or a frozen fixture whose files are missing from disk, fails the
// test immediately: that is a repository problem, not a not-yet-implemented one, and silently
// treating it as "not recorded" would let a deleted fixture masquerade as pending work.
func ContractFixture(t *testing.T, pkg, name string) (input, want []byte, frozen bool) {
	t.Helper()

	root, err := repoRoot()
	if err != nil {
		t.Fatalf("testutil: locating the repository root for contract fixture %s/%s: %v", pkg, name, err)
	}
	dir := filepath.Join(root, filepath.FromSlash(contractsDir), pkg)

	m := readContractManifest(t, filepath.Join(dir, manifestName))
	fx, ok := findFixture(m, name)
	if !ok {
		t.Fatalf("testutil: contract fixture %q is not declared in %s", name, filepath.Join(dir, manifestName))
	}
	frozen = fx.State == stateFrozen

	input = readFixtureFile(t, dir, fx.Input, frozen, "input")
	want = readFixtureFile(t, dir, fx.Want, frozen, "want")
	return input, want, frozen
}

// readContractManifest decodes one MANIFEST.json, failing the test with a message that names the
// file rather than propagating a bare decode error.
func readContractManifest(t *testing.T, path string) contractManifest {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("testutil: reading %s: %v", path, err)
	}
	var m contractManifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("testutil: parsing %s: %v", path, err)
	}
	return m
}

// findFixture returns the named fixture from m.
func findFixture(m contractManifest, name string) (contractFixture, bool) {
	for _, fx := range m.Fixtures {
		if fx.Name == name {
			return fx, true
		}
	}
	return contractFixture{}, false
}

// readFixtureFile reads one of a fixture's two files.
//
// An empty rel means the fixture declares no file of that role, which is legitimate: the hookio
// fixtures are inputs with no want, and a record-by-owner fixture has no want yet. A NAMED file
// that is missing is fatal for a frozen fixture and tolerated (nil) for one that is not, because
// the owning subplan may well be about to create the input it will record from.
func readFixtureFile(t *testing.T, dir, rel string, frozen bool, role string) []byte {
	t.Helper()
	if rel == "" {
		return nil
	}
	path := filepath.Join(dir, filepath.FromSlash(rel))
	b, err := os.ReadFile(path)
	if err == nil {
		return b
	}
	if frozen {
		t.Fatalf("testutil: frozen contract fixture is missing its %s file %s: %v", role, path, err)
	}
	return nil
}
