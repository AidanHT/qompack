package sketch_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/sketch"
)

// goldenDir is testdata/golden/contracts/sketch relative to this package.
//
// The fixtures live at the REPOSITORY testdata root rather than in a package-local testdata/,
// because Rule W-2 makes them a cross-package contract: SP-04 and SP-06 assert against these exact
// bytes in the same wave, and a package-local directory would be invisible to them.
var goldenDir = filepath.Join("..", "..", "testdata", "golden", "contracts", "sketch")

// The manifest's fixed fields (implementation spec §16). Every fixture here is a format fixture —
// §5.7 specifies its byte layout, so nothing about it is waiting on an implementation — and a
// format fixture is frozen by definition; devtool's gen-contract-fixtures rejects any other state
// for one.
const (
	goldenManifestFile  = "MANIFEST.json"
	goldenManifestPkg   = "sketch"
	goldenManifestOwner = "SP-03"
	goldenFixtureKind   = "format"
	goldenFixtureState  = "frozen"
)

// goldenPerm and goldenDirPerm are the modes a regenerated fixture and its directory are created
// with. They match internal/testutil/golden.go's convention and exist for its stated reason: these
// are ordinary readable data files under version control, deliberately not the 0o600/0o700 the
// runtime .qompack store uses, because a golden is committed source rather than session state.
const (
	goldenPerm    = 0o644
	goldenDirPerm = 0o755
)

// goldenCreated is the Created stamp every fixture carries. It is a fixed constant rather than a
// clock reading because these bytes are frozen: a timestamp in the header would make the frame
// different on every run and the whole fixture meaningless.
const goldenCreated core.UnixMilli = 1_700_000_000_000

// The canonical dimensions, taken from Appendix C's sketches block so the fixtures describe the
// shapes Qompack actually ships with.
const (
	goldenBloomCapacity = 10000
	goldenBloomFPRate   = 0.01
	goldenCMSEpsilon    = 0.001
	goldenCMSDelta      = 0.01
	goldenHLLRegisters  = 2048
	goldenMGCounters    = 64
	goldenPermutations  = 128
	goldenShingleSize   = 8
)

// The fixed input sequence. Both numbers are part of the contract: changing either changes every
// fixture's bytes, which is a format break requiring a FormatVersion bump.
const (
	goldenKeyCount = 256
	goldenDocBytes = 4096
)

// updateGolden reads the -update flag, registering it only if nothing else already has.
//
// The lookup-first form is mandatory here, not defensive. internal/testutil registers -update at
// package initialization and is linked into this test binary through io_test.go, so a second
// flag.Bool("update", …) would panic the binary at init with "flag redefined" — before any test in
// the package got to run.
var updateGolden = func() func() bool {
	if f := flag.Lookup("update"); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	p := flag.Bool("update", false, "regenerate testdata/golden/contracts/sketch fixtures")
	return func() bool { return *p }
}()

// goldenFixture is one frozen binary and the manifest row that describes it.
type goldenFixture struct {
	// name is the fixture's manifest name, the key testutil.ContractFixture looks it up by.
	name string
	// file is the fixture's flat filename under goldenDir.
	file string
	// kind is the sketch.Kind its frame must declare.
	kind sketch.Kind
	// note is the human half of the manifest note; the byte count and digest are appended to it.
	note string
	// build returns the canonical sketch, rebuilt from scratch so no case can observe another's
	// mutations.
	build func() sketch.Sketch
	// fresh returns an empty receiver of the same concrete type, for the decode direction.
	fresh func() sketch.Sketch
}

// goldenKeys is the fixed key sequence every counting fixture is fed.
func goldenKeys() [][]byte {
	keys := make([][]byte, goldenKeyCount)
	for i := range keys {
		keys[i] = fmt.Appendf(nil, "qompack/golden/key/%04d", i)
	}
	return keys
}

// goldenDocument is the fixed input the MinHash fixture is computed over. It is prose-shaped rather
// than a byte ramp so its 8-byte shingles are varied: a document whose shingle set is degenerate
// would freeze a signature that says nothing about the estimator.
func goldenDocument() []byte {
	var doc []byte
	for i := 0; len(doc) < goldenDocBytes; i++ {
		doc = fmt.Appendf(doc, "line %04d: qompack golden minhash fixture, shingle %d\n", i, i*7%13)
	}
	return doc[:goldenDocBytes]
}

// goldenFixtures returns the five frozen fixtures in manifest order.
func goldenFixtures() []goldenFixture {
	return []goldenFixture{
		{
			name: "bloom_10000_0.01",
			file: "bloom-10000-0.01.v1.bin",
			kind: sketch.KindBloom,
			note: "QPKS v1 Bloom frame",
			build: func() sketch.Sketch {
				b := sketch.NewBloom(goldenBloomCapacity, goldenBloomFPRate)
				b.SetCreated(goldenCreated)
				for _, k := range goldenKeys() {
					b.Add(k)
				}
				return b
			},
			fresh: func() sketch.Sketch { return sketch.NewBloom(goldenBloomCapacity, goldenBloomFPRate) },
		},
		{
			name: "cms_0.001_0.01",
			file: "cms-0.001-0.01.v1.bin",
			kind: sketch.KindCMS,
			note: "QPKS v1 Count-Min frame",
			build: func() sketch.Sketch {
				c := sketch.NewCMS(goldenCMSEpsilon, goldenCMSDelta)
				c.SetCreated(goldenCreated)
				for i, k := range goldenKeys() {
					c.Add(k, uint32(i%7+1))
				}
				return c
			},
			fresh: func() sketch.Sketch { return sketch.NewCMS(goldenCMSEpsilon, goldenCMSDelta) },
		},
		{
			name: "hll_2048",
			file: "hll-2048.v1.bin",
			kind: sketch.KindHLL,
			note: "QPKS v1 HyperLogLog frame",
			build: func() sketch.Sketch {
				h := sketch.NewHLL(goldenHLLRegisters)
				h.SetCreated(goldenCreated)
				for _, k := range goldenKeys() {
					h.Add(k)
				}
				return h
			},
			fresh: func() sketch.Sketch { return sketch.NewHLL(goldenHLLRegisters) },
		},
		{
			name: "mg_64",
			file: "mg-64.v1.bin",
			kind: sketch.KindMisraGries,
			note: "QPKS v1 Misra-Gries frame",
			build: func() sketch.Sketch {
				m := sketch.NewMisraGries(goldenMGCounters)
				m.SetCreated(goldenCreated)
				for i, k := range goldenKeys() {
					m.Add(string(k), i%5+1)
				}
				return m
			},
			fresh: func() sketch.Sketch { return sketch.NewMisraGries(goldenMGCounters) },
		},
		{
			name: "minhash_128",
			file: "minhash-128.v1.bin",
			kind: sketch.KindMinHash,
			note: "QPKS v1 MinHash frame",
			build: func() sketch.Sketch {
				return &sketch.SigSketch{
					Sig: sketch.MinHash(goldenDocument(), sketch.MinHashOptions{
						Enabled:      true,
						Permutations: goldenPermutations,
						ShingleSize:  goldenShingleSize,
					}),
					Created: goldenCreated,
				}
			},
			fresh: func() sketch.Sketch { return &sketch.SigSketch{} },
		},
	}
}

// contractFixtureRow is one row of testdata/golden/contracts/sketch/MANIFEST.json (§16).
type contractFixtureRow struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Input string `json:"input"`
	Want  string `json:"want"`
	Note  string `json:"note"`
}

// contractManifest is the whole manifest document.
type contractManifest struct {
	Package  string               `json:"package"`
	Owner    string               `json:"owner"`
	Fixtures []contractFixtureRow `json:"fixtures"`
}

// wantManifest builds the manifest the committed file must equal, given each fixture's frozen
// bytes. The note carries the byte count and the SHA-256 the plan asks be recorded, so a fixture
// swapped underneath the manifest is visible in a diff of the manifest alone.
func wantManifest(frames map[string][]byte) contractManifest {
	m := contractManifest{Package: goldenManifestPkg, Owner: goldenManifestOwner}
	for _, fx := range goldenFixtures() {
		frame := frames[fx.name]
		digest := sha256.Sum256(frame)
		m.Fixtures = append(m.Fixtures, contractFixtureRow{
			Name:  fx.name,
			Kind:  goldenFixtureKind,
			State: goldenFixtureState,
			Input: "",
			Want:  fx.file,
			Note:  fmt.Sprintf("%s; bytes=%d sha256=%s", fx.note, len(frame), hex.EncodeToString(digest[:])),
		})
	}
	return m
}

// TestGolden_OnDiskStability is the frozen-format assertion (Rule W-2): the five canonical sketches
// must marshal to exactly the committed bytes, and the manifest must describe exactly those bytes.
//
// Regenerate the binaries with `go test ./internal/sketch -run TestGolden -update`. These fixtures
// are frozen after commit 6: a change to any of them is a format break that requires a
// FormatVersion bump and a new decoder case arm, not a fixture refresh.
//
// # Why -update does not rewrite MANIFEST.json
//
// The manifest is checked by decoding it into contractManifest and comparing values, not by
// re-rendering its bytes. Its byte LAYOUT — two-space indent, one fixture per line, six keys in a
// fixed order — is tools/devtool's contract: renderManifest writes it and
// TestRenderManifest_ByteIdentical holds all ten manifests to it. Reproducing that layout here
// would be a second definition of a format another package already owns and already tests, so this
// test asserts the property that is actually its business (every row describes the fixture on disk,
// with the size and digest the current build produces) and leaves the layout alone.
//
// The consequence is deliberate: a format break makes this subtest fail with the exact note strings
// required, and the manifest is then edited by hand. For a document Rule W-2 calls byte-final, a
// deliberate edit is the right ergonomics — it forces the FormatVersion conversation that a silent
// regeneration would skip.
func TestGolden_OnDiskStability(t *testing.T) {
	fixtures := goldenFixtures()

	frames := make(map[string][]byte, len(fixtures))
	for _, fx := range fixtures {
		frame, err := fx.build().MarshalBinary()
		require.NoError(t, err, "%s: building the canonical sketch", fx.name)
		require.NotEmpty(t, frame)
		frames[fx.name] = frame
	}

	if updateGolden() {
		if os.Getenv("CI") != "" {
			t.Fatal("refusing to regenerate golden fixtures in CI: a fixture the implementation " +
				"cannot reproduce is a verification failure, not a fixture bug (Rule W-2)")
		}
		require.NoError(t, os.MkdirAll(goldenDir, goldenDirPerm))
		for _, fx := range fixtures {
			require.NoError(t, os.WriteFile(filepath.Join(goldenDir, fx.file), frames[fx.name], goldenPerm))
		}
		t.Logf("regenerated %d fixtures in %s", len(fixtures), goldenDir)
	}

	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			path := filepath.Join(goldenDir, fx.file)
			committed, err := os.ReadFile(path)
			require.NoError(t, err, "the frozen fixture must be committed; regenerate with -update")

			want, got := frames[fx.name], committed
			// Length and digest are asserted before the bytes so a failure reports two short
			// numbers instead of a 54 KB diff.
			require.Len(t, got, len(want), "%s changed size: this is a format break, not a fixture refresh", fx.file)
			require.Equal(t, sha256.Sum256(want), sha256.Sum256(got),
				"%s no longer matches what this build marshals (Rule W-2)", fx.file)
			require.True(t, bytes.Equal(want, got))
		})
	}

	t.Run(goldenManifestFile, func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(goldenDir, goldenManifestFile))
		require.NoError(t, err)

		var got contractManifest
		require.NoError(t, json.Unmarshal(raw, &got))
		require.Equal(t, wantManifest(frames), got,
			"the manifest must describe exactly these five fixtures, with each note recording the "+
				"size and digest the current build produces")
	})
}

// TestGolden_V1StillDecodes is the upgrade-path assertion: this build's decoder must still read the
// committed v1 bytes. §6.2 makes sketches permanent memory, so the oldest file on disk has to keep
// decoding forever; when FormatVersion becomes 2 this test is what proves the `case 1:` arm was
// kept rather than replaced.
func TestGolden_V1StillDecodes(t *testing.T) {
	for _, fx := range goldenFixtures() {
		t.Run(fx.name, func(t *testing.T) {
			committed, err := os.ReadFile(filepath.Join(goldenDir, fx.file))
			require.NoError(t, err)

			h, body, err := sketch.DecodeHeader(committed)
			require.NoError(t, err, "the committed v1 frame must decode with the current decoder")
			require.Equal(t, sketch.HeaderMagic, h.Magic)
			require.Equal(t, sketch.FormatVersion, h.Ver)
			require.Equal(t, fx.kind, h.Kind)
			require.Equal(t, goldenCreated, h.Created)
			require.NotZero(t, h.CRC32C, "DecodeHeader reports the stored checksum")
			require.NotNil(t, body)

			got := fx.fresh()
			require.NoError(t, got.UnmarshalBinary(committed))
			remarshalled, err := got.MarshalBinary()
			require.NoError(t, err)
			require.True(t, bytes.Equal(committed, remarshalled),
				"decode then re-encode must be byte-identical, or the frame is not self-describing")
		})
	}
}
