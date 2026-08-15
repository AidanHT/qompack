package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestContractFixture_FrozenFormat asserts a frozen format fixture hands back its recorded want/
// bytes and reports frozen, so a consumer asserts against it immediately rather than skipping.
func TestContractFixture_FrozenFormat(t *testing.T) {
	input, want, frozen := ContractFixture(t, "store", "tool_use_line")

	require.True(t, frozen, "the §16 format fixtures were frozen by SP-01")
	require.Nil(t, input, "this fixture declares no input file")
	require.NotEmpty(t, want)
	require.True(t, json.Valid(want), "one index/tool_use.jsonl line must be valid JSON")
}

// TestContractFixture_InputOnly asserts a fixture that declares an input and no want — the shape
// every hookio payload fixture has — returns the input and a nil want, and is still frozen.
func TestContractFixture_InputOnly(t *testing.T) {
	input, want, frozen := ContractFixture(t, "hookio", "post_tool_use")

	require.True(t, frozen)
	require.NotEmpty(t, input)
	require.Nil(t, want)
	require.True(t, json.Valid(input))
}

// TestContractFixture_RecordByOwner asserts an unrecorded behaviour fixture reports frozen=false
// and tolerates its input not existing yet, which is the state every wave-1+ fixture is in today.
// A consumer that sees frozen=false skips with NotRecordedSkip; this test asserts the flag rather
// than skipping, so the accessor itself stays covered.
func TestContractFixture_RecordByOwner(t *testing.T) {
	_, want, frozen := ContractFixture(t, "store", "dedup_second_put")

	require.False(t, frozen, "SP-06 records this one; it is record-by-owner until then (Rule W-2)")
	require.Nil(t, want)
	require.Equal(t, "contract fixture not yet recorded (Rule W-2)", NotRecordedSkip,
		"the skip message is matched verbatim by devtool lint's stubskips sub-check")
}

// TestContractFixture_EveryManifestIsReadable walks every package directory under
// testdata/golden/contracts/ and reads every fixture it declares, so a manifest that names a file
// that does not exist, or a frozen fixture whose bytes were deleted, fails here rather than in
// whichever wave-3 consumer happens to reach for it first.
func TestContractFixture_EveryManifestIsReadable(t *testing.T) {
	root, err := repoRoot()
	require.NoError(t, err)

	pkgs, manifests := allContractManifests(t, root)
	require.NotEmpty(t, pkgs)

	var frozenCount, pendingCount int
	for i, m := range manifests {
		require.Equal(t, pkgs[i], m.Package, "MANIFEST.json's package field must match its directory")
		require.NotEmpty(t, m.Owner)
		for _, fx := range m.Fixtures {
			require.Contains(t, []string{"format", "behaviour"}, fx.Kind)
			require.Contains(t, []string{stateFrozen, "record-by-owner"}, fx.State)

			_, want, frozen := ContractFixture(t, m.Package, fx.Name)
			if frozen {
				frozenCount++
				if fx.Want != "" {
					require.NotEmpty(t, want, "a frozen fixture naming a want file must have bytes in it")
				}
				continue
			}
			pendingCount++
			require.Equal(t, "behaviour", fx.Kind, "only a behaviour fixture may still be record-by-owner")
		}
	}

	// 28 = SP-01's 21 + V1's 2 + SP-03's 5, and each group is worth being able to point at.
	//
	// V1 added the two §16 fixtures that had never been declared: contract/result_set (the nine
	// §5.19 assertions as RunAll reports them) and config/appendix_c_defaults (the Appendix C
	// golden, declared where it already lives).
	//
	// SP-03 added the five §5.7 QPKS sketch frames — bloom, cms, hll, mg, minhash — which are the
	// first BINARY fixtures in the corpus; test/guards' IT-9 walker dispatches on the .bin
	// extension to check them, since a JSON round-trip cannot express a versioned, checksummed
	// byte layout.
	require.Equal(t, 28, frozenCount,
		"SP-01 froze 21 format fixtures, V1 added 2 and SP-03 added 5 sketch frames; "+
			"adding or losing one is a contract change")
	require.Equal(t, 5, pendingCount, "5 behaviour fixtures await their owning subplan")
}

// allContractManifests returns every package directory under testdata/golden/contracts/ and the
// MANIFEST.json each one carries, in directory order.
func allContractManifests(t *testing.T, root string) (pkgs []string, manifests []contractManifest) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(contractsDir))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkgs = append(pkgs, e.Name())
		manifests = append(manifests, readContractManifest(t, filepath.Join(dir, e.Name(), manifestName)))
	}
	return pkgs, manifests
}
