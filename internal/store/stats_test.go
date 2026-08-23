package store

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fixtureVersions are the four versions of the one ~23 KB TypeScript file the SP-06 corpus carries:
// the "four reads of one file" case of Qompack.md §8.2, with 2, 18 and 240 lines changed between
// successive versions.
var fixtureVersions = []string{
	"fileread-auth-v1.txt",
	"fileread-auth-v2.txt",
	"fileread-auth-v3.txt",
	"fileread-auth-v4.txt",
}

// TestStats_DedupRatio asserts the four fixture versions, each read four times, deduplicate to at
// least 4:1 — and that RawBytes counts EVERY put, including the exact duplicates, which is what
// makes the ratio a deduplication ratio rather than a compression ratio.
//
// WAVE-1 RECORD. Measured at the V2 checkpoint against the REAL chunker and the REAL
// canonicalizers: 81.11 (raw 393 380 B, on disk 4 850 B, 10 objects). SP-06 recorded 12.40 here
// with internal/chunk and internal/canon still SP-01 stubs (§2.6a ⑤); §2.6a predicted an increase
// and got one — 6.5x. The floor stays at 4.0 because that is Qompack.md §10's Phase 1 exit
// criterion, not a description of this corpus: a number this far above it would turn any future
// regression into a still-passing test if the assertion tracked the measurement.
func TestStats_DedupRatio(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	var wantRaw int64
	for _, name := range fixtureVersions {
		body := fixture(t, name)
		for read := 0; read < 4; read++ {
			_, err := tp.Store.PutBytes(ctx, body, PutOptions{Tool: "FileRead", Path: "src/auth.ts"})
			require.NoError(t, err)
			wantRaw += int64(len(body))
		}
	}

	st, err := tp.Store.Stats(ctx)
	require.NoError(t, err)
	t.Logf("four versions x four reads: DedupRatio=%.2f raw=%d bytes=%d objects=%d",
		st.DedupRatio, st.RawBytes, st.Bytes, st.Objects)
	require.Equal(t, wantRaw, st.RawBytes,
		"RawBytes must count every put, including the twelve exact duplicates")
	require.Positive(t, st.Bytes)
	require.GreaterOrEqual(t, st.DedupRatio, 4.0,
		"four reads of four versions must dedup to at least 4:1 (got %.2f: raw=%d bytes=%d)",
		st.DedupRatio, st.RawBytes, st.Bytes)
}

// TestPhase1ExitCriterion_ReadHeavy is Qompack.md §10's Phase 1 exit criterion, quoted verbatim:
//
//	Exit criterion: store size vs. raw transcript ratio ≥ 4:1 on read-heavy sessions (measure with
//	and without canonicalization — the gap on test-output-heavy sessions justifies O2 on its own);
//	hook p99 < 15ms.
//
// This asserts the ratio half. The hook-p99 half is SP-05's B-A budget and SP-08's to close.
//
// The corpus is generated here rather than committed: ten files, each read four times, with an edit
// applied between the second and third read — forty puts, which is the shape of a read-heavy
// session where the same handful of files is re-read as the work proceeds.
//
// The ten files are deliberately GENUINELY DISTINCT rather than ten copies of one fixture. Ten
// near-identical files would measure cross-file deduplication, which is not what this criterion is
// about and which inflates the ratio by roughly an order of magnitude; the number that has to clear
// 4:1 is the one produced by re-reading and editing a set of unrelated files.
//
// WAVE-1 RECORD. Measured at the V2 checkpoint against the REAL chunker and the REAL
// canonicalizers: 24.52 (raw 878 852 B, on disk 35 842 B, 78 objects), against SP-06's
// pre-canonicalization 6.34 (§2.6a ⑤). An increase, which is what §3.6 says to expect; a DECREASE
// would have been the regression to investigate. The 4.0 floor is the criterion and stays.
func TestPhase1ExitCriterion_ReadHeavy(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	var rawTotal int64
	for f := 0; f < 10; f++ {
		path := fmt.Sprintf("src/module%02d/auth.ts", f)
		before := syntheticSource(f, 0)
		after := syntheticSource(f, 1) // the same file after an edit

		for read := 0; read < 4; read++ {
			body := before
			if read >= 2 { // the edit lands between the second and third read
				body = after
			}
			_, err := tp.Store.PutBytes(ctx, body, PutOptions{Tool: "FileRead", Path: path})
			require.NoError(t, err)
			rawTotal += int64(len(body))
		}
	}

	st, err := tp.Store.Stats(ctx)
	require.NoError(t, err)
	t.Logf("read-heavy corpus: DedupRatio=%.2f raw=%d bytes=%d objects=%d",
		st.DedupRatio, st.RawBytes, st.Bytes, st.Objects)
	require.Equal(t, rawTotal, st.RawBytes)
	require.GreaterOrEqual(t, st.DedupRatio, 4.0,
		"Phase 1 exit criterion: store size vs. raw transcript ratio must be >= 4:1 on a "+
			"read-heavy session (got %.2f: raw=%d bytes=%d objects=%d)",
		st.DedupRatio, st.RawBytes, st.Bytes, st.Objects)
}

// syntheticSource builds ~23 KB of deterministic, source-shaped text for one module.
//
// Each module's identifiers and string literals are derived from its own index, so two modules
// share only the language's boilerplate — which is exactly how ten real files in one repository
// relate. revision > 0 rewrites one contiguous block, standing in for an edit between two reads.
func syntheticSource(module, revision int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "// module %02d — generated fixture, revision %d\n", module, revision)
	fmt.Fprintf(&b, "import { Session%02d, Token%02d } from \"./types%02d\";\n\n", module, module, module)

	const lines = 420
	editFrom, editTo := lines/2, lines/2+40
	for i := 0; i < lines; i++ {
		if revision > 0 && i >= editFrom && i < editTo {
			fmt.Fprintf(&b, "  const patched%02d_%03d = refreshToken%02d(%d, \"rev%d\");\n",
				module, i, module, i*7+module, revision)
			continue
		}
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, "export function handler%02d_%03d(req: Session%02d): Token%02d {\n", module, i, module, module)
		case 1:
			fmt.Fprintf(&b, "  const scope%02d = resolveScope(req, %d, \"m%02d/%03d\");\n", module, i*13+module, module, i)
		case 2:
			fmt.Fprintf(&b, "  if (!scope%02d.valid) throw new AuthError(\"m%02d:%03d denied\");\n", module, module, i)
		default:
			fmt.Fprintf(&b, "  return issue%02d(scope%02d, %d);\n}\n\n", module, module, i*31+module)
		}
	}
	return []byte(b.String())
}

// TestStats_SublinearGrowth asserts Qompack.md §11.3's "store growth sublinear in session length
// after dedup": a long run of puts of a slowly mutating payload must not cost twenty-five times
// what the first eight did.
//
// WAVE-1 RECORD. Measured at the V2 checkpoint against the real pipeline: 6 642 B after 8 puts and
// 41 976 B after 120, a factor of 6.32 for fifteen times the puts.
func TestStats_SublinearGrowth(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	// ~100 KB of line-structured content, mutated in one contiguous region each round — the shape
	// of a file being edited, which is what content-defined chunking is supposed to absorb.
	var b strings.Builder
	for i := 0; b.Len() < 100<<10; i++ {
		fmt.Fprintf(&b, "line %05d: the quick brown fox jumps over the lazy dog\n", i)
	}
	payload := []byte(b.String())
	mutateAt := len(payload) / 2
	mutateLen := len(payload) / 100 // 1 %

	var after8 int64
	for i := 0; i < growthTotalPuts; i++ {
		body := append([]byte{}, payload...)
		copy(body[mutateAt:mutateAt+mutateLen],
			bytes.Repeat([]byte(fmt.Sprintf("~edit %04d~", i)), mutateLen))
		_, err := tp.Store.PutBytes(ctx, body, PutOptions{Tool: "FileRead", Path: "src/big.ts"})
		require.NoError(t, err)

		if i == growthSnapshotPuts-1 {
			st, serr := tp.Store.Stats(ctx)
			require.NoError(t, serr)
			after8 = st.Bytes
		}
	}

	st, err := tp.Store.Stats(ctx)
	require.NoError(t, err)
	require.Positive(t, after8)
	t.Logf("sublinear growth: %d puts=%d B, %d puts=%d B, factor %.2f (linear would be %d, gate at %.2f)",
		growthSnapshotPuts, after8, growthTotalPuts, st.Bytes, float64(st.Bytes)/float64(after8),
		growthLinearFactor, float64(growthLinearFactor)/growthGateDivisor)
	requireGrowthUnderGate(t, after8, st.Bytes)
}

// The growth fixture's two put counts, named because the gate below is derived from their ratio
// rather than from a literal.
const (
	growthSnapshotPuts = 8
	growthTotalPuts    = 120

	// growthLinearFactor is what this fixture would grow by with NO deduplication at all: 120 puts
	// of the same ~100 KB payload cost 15 times what the first 8 cost.
	growthLinearFactor = growthTotalPuts / growthSnapshotPuts

	// growthGateDivisor puts the gate at half of linear. The shipped store measures 6.32x against a
	// linear 15x, so half — 7.5x — leaves about 16 % headroom over what dedup actually achieves
	// while staying decisively below the number that means dedup stopped working.
	//
	// The bound this replaces was `Bytes < 25*after8`, which is ABOVE linear: a store that
	// deduplicated nothing at all passed it with 40 % to spare, so the assertion could not fail for
	// the reason it exists. TestStats_GrowthGateFailsWithoutDedup is the proof that this one can.
	growthGateDivisor = 2.0
)

// requireGrowthUnderGate is the §11.4 sublinear-growth assertion, in one place so the gate and its
// own mutation test cannot drift apart.
func requireGrowthUnderGate(t *testing.T, snapshot, total int64) {
	t.Helper()
	limit := int64(float64(growthLinearFactor) / growthGateDivisor * float64(snapshot))
	require.Less(t, total, limit,
		"store growth must be sublinear in session length after dedup: %d puts=%d B, %d puts=%d B "+
			"(factor %.2f); linear for this fixture is %dx and the gate binds at %.2fx",
		growthSnapshotPuts, snapshot, growthTotalPuts, total, float64(total)/float64(snapshot),
		growthLinearFactor, float64(growthLinearFactor)/growthGateDivisor)
}

// TestStats_GrowthGateFailsWithoutDedup is the mutation test for the gate above, and it is not
// optional: a growth bound that no achievable growth can exceed reports success while measuring
// nothing, which is what the 25x bound did for all of wave 1.
//
// Dedup is "disabled" the only way a content-addressed store allows — by storing content that
// shares no chunk with what came before. Every put is an independently generated payload, so the
// store grows linearly by construction, and the gate must reject it.
func TestStats_GrowthGateFailsWithoutDedup(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	var after8 int64
	for i := 0; i < growthTotalPuts; i++ {
		// Every LINE carries the put index, so no window of this payload can equal a window of any
		// other put's — which is what makes the content share no chunk, deterministically and
		// without a random source.
		var b strings.Builder
		for n := 0; b.Len() < 100<<10; n++ {
			fmt.Fprintf(&b, "put %04d line %05d: the quick brown fox of put %04d\n", i, n, i)
		}
		_, err := tp.Store.PutBytes(ctx, []byte(b.String()),
			PutOptions{Tool: "FileRead", Path: fmt.Sprintf("src/uniq%04d.ts", i)})
		require.NoError(t, err)

		if i == growthSnapshotPuts-1 {
			st, serr := tp.Store.Stats(ctx)
			require.NoError(t, serr)
			after8 = st.Bytes
		}
	}

	st, err := tp.Store.Stats(ctx)
	require.NoError(t, err)
	require.Positive(t, after8)

	limit := int64(float64(growthLinearFactor) / growthGateDivisor * float64(after8))
	require.GreaterOrEqual(t, st.Bytes, limit,
		"undeduplicated growth measured %.2fx, which is under the %.2fx gate — the gate is too loose "+
			"to detect dedup failing at all (%d puts=%d B, %d puts=%d B)",
		float64(st.Bytes)/float64(after8), float64(growthLinearFactor)/growthGateDivisor,
		growthSnapshotPuts, after8, growthTotalPuts, st.Bytes)
	t.Logf("without dedup: factor %.2f against a %.2fx gate", float64(st.Bytes)/float64(after8),
		float64(growthLinearFactor)/growthGateDivisor)
}

// TestStats_CountsIndexCardinalities asserts the non-size fields report what they claim to.
func TestStats_CountsIndexCardinalities(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	res, err := tp.Store.PutBytes(ctx, []byte("stats cardinality body\n"), PutOptions{Path: "src/a.ts"})
	require.NoError(t, err)
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: "tu-1", Session: "sess-stats", TS: 1, Tool: "FileRead", Root: res.Root.Hash, Path: "src/a.ts",
	}))
	require.NoError(t, tp.Store.AppendFileVersion(ctx, "src/a.ts", FileVersion{
		TS: 1, Root: res.Root.Hash, Turn: 0, Bytes: res.Root.RawBytes,
	}))
	id, err := tp.Store.Segments().Open(ctx, Segment{Session: "sess-stats", StartTurn: 0})
	require.NoError(t, err)
	require.NoError(t, tp.Store.Segments().Close(ctx, id, 3, map[string]float64{"tokens": 10}))

	st, err := tp.Store.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, st.ToolUses)
	require.Equal(t, 1, st.Files)
	require.Equal(t, 1, st.Segments)
	require.Equal(t, len(res.Root.Chunks), st.Objects)
	require.Contains(t, st.Sketches, sketchSignatureKey)
}

// TestStats_EmptyStoreHasZeroRatio asserts an empty store reports a zero ratio rather than dividing
// by zero.
func TestStats_EmptyStoreHasZeroRatio(t *testing.T) {
	tp := newTestStore(t)
	st, err := tp.Store.Stats(context.Background())
	require.NoError(t, err)
	require.Zero(t, st.Bytes)
	require.Zero(t, st.DedupRatio)
}
