package store

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/tokens"
)

// SP20-D3: a recovery side record must belong to exactly one content root, and the fidelity a put
// reports must be the fidelity a read of the same bytes reports.
//
// Every test here compiles against the store as it was BEFORE the fix (none of them names a helper
// the fix added), so reverting only the production change turns them red rather than unbuildable.

// sharedTokenInputs are two tool outputs that differ in their bodies but carry the SAME volatile
// token at the SAME canonical offset, so their canonicalizations remove identical delta lists.
func sharedTokenInputs() (a, b []byte) {
	return []byte(timestampPrefix + "09:11:04\nbody of the first output\n"),
		[]byte(timestampPrefix + "09:11:04\nbody of the second, different output\n")
}

// requirePutAndReadAgree asserts that the fidelity PutBytes reported for input is the fidelity
// RestoreOriginal reports for the same root, and that any recoverable label reads back input
// byte for byte.
func requirePutAndReadAgree(t *testing.T, tp *testProject, res PutResult, input []byte) {
	t.Helper()
	got, fid, err := tp.Store.RestoreOriginal(context.Background(), res.Root.Hash)
	require.NoError(t, err, "root %s", res.Root.Hash.Short())
	require.Equal(t, res.Fidelity, fid, "put and read must report the same fidelity for the same bytes")
	if fid == FidelityExact || fid == FidelityFull {
		require.Equal(t, input, got, "a %s label must read back the original byte for byte", fid)
	}
}

// TestPutBytes_SharedVolatileTokenRestoresBothRootsExactly is SP20-D3's characterization: two roots
// whose canonicalization removed the same token at the same offset must each own a recovery record
// declaring itself as base, and both must restore exactly — before and after a reopen.
func TestPutBytes_SharedVolatileTokenRestoresBothRootsExactly(t *testing.T) {
	p := newProject(t)
	tp := openOver(t, p, withCanon(canonStripTimestamp()))
	ctx := context.Background()
	inA, inB := sharedTokenInputs()

	a, err := tp.Store.PutBytes(ctx, inA, PutOptions{Path: "src/a.log", KeepRaw: true})
	require.NoError(t, err)
	b, err := tp.Store.PutBytes(ctx, inB, PutOptions{Path: "src/b.log", KeepRaw: true})
	require.NoError(t, err)
	require.NotEqual(t, a.Root.Hash, b.Root.Hash, "fixture sanity: the bodies differ, so the roots differ")
	require.Equal(t, FidelityExact, a.Fidelity)
	require.Equal(t, FidelityExact, b.Fidelity)

	for _, c := range []struct {
		res PutResult
		in  []byte
	}{{a, inA}, {b, inB}} {
		got, fid, err := tp.Store.RestoreOriginal(ctx, c.res.Root.Hash)
		require.NoError(t, err, "root %s", c.res.Root.Hash.Short())
		require.Equal(t, FidelityExact, fid)
		require.Equal(t, c.in, got)
	}

	tp.Store.mu.RLock()
	da, db := tp.Store.rootIndex[a.Root.Hash].Deltas, tp.Store.rootIndex[b.Root.Hash].Deltas
	recA, recB := tp.Store.rootIndex[da], tp.Store.rootIndex[db]
	tp.Store.mu.RUnlock()
	require.NotEqual(t, da, db, "two content roots must not share one recovery record")
	require.NotNil(t, recA)
	require.NotNil(t, recB)
	require.Equal(t, a.Root.Hash, recA.Base, "A's record declares A")
	require.Equal(t, b.Root.Hash, recB.Base, "B's record declares B")

	require.NoError(t, tp.Store.Close())
	reopened := openOver(t, p, withCanon(canonStripTimestamp()))
	for _, c := range []struct {
		res PutResult
		in  []byte
	}{{a, inA}, {b, inB}} {
		got, fid, err := reopened.Store.RestoreOriginal(ctx, c.res.Root.Hash)
		require.NoError(t, err, "after a reopen")
		require.Equal(t, FidelityExact, fid, "after a reopen")
		require.Equal(t, c.in, got, "after a reopen")
	}
}

// TestPutBytes_PutAndReadFidelityAgree is SP20-D3's related half (V5 report section 22 item 29):
// for every recovery shape, the label a put reports is the label a read of that root reports, on
// a fresh put and on a root-level dedup hit of the same bytes.
func TestPutBytes_PutAndReadFidelityAgree(t *testing.T) {
	cases := []struct {
		name    string
		canon   func() storeOpt
		input   string
		keepRaw bool
		want    Fidelity
	}{
		{
			"nothing volatile, KeepRaw", func() storeOpt { return withCanon(canonStripTimestamp()) },
			"no volatile token in this output\n", true, FidelityExact,
		},
		{
			"nothing volatile, no KeepRaw", func() storeOpt { return withCanon(canonStripTimestamp()) },
			"no volatile token in this output\n", false, FidelityCanonical,
		},
		{
			"proven delta", func() storeOpt { return withCanon(canonStripTimestamp()) },
			timestampPrefix + "09:11:04\nbody\n", true, FidelityExact,
		},
		{
			"unproven delta falls back to a full original", func() storeOpt { return withCanon(canonLossyDeltas()) },
			timestampPrefix + "09:11:04\nbody\n", true, FidelityFull,
		},
		{
			"volatile, no KeepRaw", func() storeOpt { return withCanon(canonStripTimestamp()) },
			timestampPrefix + "09:11:04\nbody\n", false, FidelityCanonical,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tp := newTestStore(t, c.canon())
			ctx := context.Background()
			o := PutOptions{Path: "src/out.log", KeepRaw: c.keepRaw}

			first, err := tp.Store.PutBytes(ctx, []byte(c.input), o)
			require.NoError(t, err)
			require.Equal(t, c.want, first.Fidelity, "fresh put")
			requirePutAndReadAgree(t, tp, first, []byte(c.input))

			again, err := tp.Store.PutBytes(ctx, []byte(c.input), o)
			require.NoError(t, err)
			require.Equal(t, first.Root.Hash, again.Root.Hash, "fixture sanity: a dedup hit")
			require.Equal(t, c.want, again.Fidelity, "dedup hit of the same bytes")
			requirePutAndReadAgree(t, tp, again, []byte(c.input))
		})
	}
}

// TestPutBytes_DedupHitNeverClaimsAnotherPutsOriginal pins the dedup half of the same rule. Two
// inputs that differ ONLY in the volatile token canonicalize to one root, whose recovery record is
// the first input's. The second put's original is not what that root restores, so the second put
// must not report a recoverable fidelity for it.
func TestPutBytes_DedupHitNeverClaimsAnotherPutsOriginal(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()
	first := []byte(timestampPrefix + "09:11:04\nthe same body\n")
	second := []byte(timestampPrefix + "09:11:05\nthe same body\n")

	a, err := tp.Store.PutBytes(ctx, first, PutOptions{Path: "src/out.log", KeepRaw: true})
	require.NoError(t, err)
	b, err := tp.Store.PutBytes(ctx, second, PutOptions{Path: "src/out.log", KeepRaw: true})
	require.NoError(t, err)
	require.Equal(t, a.Root.Hash, b.Root.Hash, "fixture sanity: one canonical root")
	require.Equal(t, FidelityExact, a.Fidelity)

	got, fid, err := tp.Store.RestoreOriginal(ctx, b.Root.Hash)
	require.NoError(t, err)
	require.Equal(t, FidelityExact, fid)
	require.Equal(t, first, got, "the root restores the FIRST put's original")
	require.Equal(t, FidelityCanonical, b.Fidelity,
		"the second put's original is not restorable, so its label must not claim it is")
}

// gatedEstimator is a tokens.Estimator whose FIRST EstimateRoot call signals entered and then
// blocks until release is closed; every later call returns at once. PutBytes makes that first call
// for a fresh content root after its dedup check and before its content line is published, so the
// gate holds a put inside exactly that window. It prices nothing: its tests are about ordering.
type gatedEstimator struct {
	once             sync.Once
	entered, release chan struct{}
}

func newGatedEstimator() *gatedEstimator {
	return &gatedEstimator{entered: make(chan struct{}), release: make(chan struct{})}
}

func (*gatedEstimator) Estimate([]byte, tokens.Class) core.Tokens       { return 0 }
func (*gatedEstimator) EstimateString(string, tokens.Class) core.Tokens { return 0 }
func (*gatedEstimator) Calibrate(core.Tokens, core.Tokens)              {}
func (*gatedEstimator) Factor() float64                                 { return 1 }

func (g *gatedEstimator) EstimateRoot(context.Context, []core.ChunkRef, tokens.Class) core.Tokens {
	first := false
	g.once.Do(func() { first = true; close(g.entered) })
	if first {
		<-g.release
	}
	return 0
}

// withEstimator injects a tokens.Estimator double.
func withEstimator(e tokens.Estimator) storeOpt {
	return func(_ *config.Config, d *Deps) { d.Tokens = e }
}

// TestPutBytes_ConcurrentPutsOfOneRootClaimOnlyTheOriginalItRestores is the concurrent form of
// TestPutBytes_DedupHitNeverClaimsAnotherPutsOriginal (SP20-D3 review 1). Put A is held inside
// PutBytes' write window — past its dedup check, before its content line — while put B, whose
// input differs from A's only in the volatile token and so canonicalizes to the same root, runs.
// B must not become a second writer of that root: it waits for A, deduplicates onto A's record and
// reports canonical. Exactly one put claims an exact original, it is the one the root restores, in
// memory and after a reopen, and roots.jsonl carries one content line for the root and one
// recovery record declaring it.
//
// The bounded wait is how long B is given to run straight through while A is held, which is what
// it does without a per-root put lock: the test is then red with two content lines, two recovery
// records and two exact labels. With the lock B cannot finish before A is released, so the wait
// always runs out. Its length can only make the test miss the defect on a starved machine; it
// cannot fail a correct store.
func TestPutBytes_ConcurrentPutsOfOneRootClaimOnlyTheOriginalItRestores(t *testing.T) {
	const window = time.Second
	p := newProject(t)
	gate := newGatedEstimator()
	tp := openOver(t, p, withCanon(canonStripTimestamp()), withEstimator(gate))
	ctx := context.Background()
	inA := []byte(timestampPrefix + "09:11:04\nthe same body\n")
	inB := []byte(timestampPrefix + "09:11:05\nthe same body\n")

	var a, b PutResult
	var errA, errB error
	doneA, doneB := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(doneA)
		a, errA = tp.Store.PutBytes(ctx, inA, PutOptions{Path: "src/a.log", KeepRaw: true})
	}()
	<-gate.entered // A has missed the dedup and not yet published its content line
	go func() {
		defer close(doneB)
		b, errB = tp.Store.PutBytes(ctx, inB, PutOptions{Path: "src/b.log", KeepRaw: true})
	}()
	select {
	case <-doneB:
	case <-time.After(window):
	}
	close(gate.release)
	<-doneA
	<-doneB
	require.NoError(t, errA)
	require.NoError(t, errB)
	require.Equal(t, a.Root.Hash, b.Root.Hash, "fixture sanity: one canonical root")
	root := a.Root.Hash

	require.Equal(t, FidelityExact, a.Fidelity, "A wrote the root and its recovery record")
	require.Equal(t, FidelityCanonical, b.Fidelity,
		"B's original is not what the root restores, so its label must not claim it is")
	requirePutAndReadAgree(t, tp, a, inA)

	var content, records int
	for _, ln := range tp.indexLines(t, rootsFile) {
		if strings.Contains(ln, `"root":"`+root.String()+`"`) {
			content++
		}
		if strings.Contains(ln, `"base":"`+root.String()+`"`) {
			records++
		}
	}
	require.Equal(t, 1, content, "one content line for the root")
	require.Equal(t, 1, records, "one recovery record declaring the root")

	require.NoError(t, tp.Store.Close())
	reopened := openOver(t, p, withCanon(canonStripTimestamp()))
	got, fid, err := reopened.Store.RestoreOriginal(ctx, root)
	require.NoError(t, err)
	require.Equal(t, FidelityExact, fid, "after a reopen")
	require.Equal(t, inA, got, "after a reopen the root still restores A, the put that claimed it")
}

// ── the record's wire shape ──────────────────────────────────────────────────────────────────

// wantDeltaRecord renders what a delta side record's payload must be, through encoding/json as an
// oracle independent of the store's hand-written marshaller: {"base":…,"deltas":[…]} with the
// compact {"o","l","c","s"} entries and HTML escaping off, exactly as every other writer here.
func wantDeltaRecord(t *testing.T, base core.Hash, deltas []canon.Delta) []byte {
	t.Helper()
	type entry struct {
		O int    `json:"o"`
		L int    `json:"l"`
		C string `json:"c"`
		S string `json:"s"`
	}
	rec := struct {
		Base   string  `json:"base"`
		Deltas []entry `json:"deltas"`
	}{Base: base.String(), Deltas: []entry{}}
	for _, d := range deltas {
		rec.Deltas = append(rec.Deltas, entry{O: d.Offset, L: d.Len, C: string(d.Class), S: d.Original})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(rec))
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// canonStripped runs the timestamp-stripping double directly, deltas kept.
func canonStripped(t *testing.T, in []byte) canon.Result {
	t.Helper()
	cr, err := canonStripTimestamp().Run("", "", in, canon.Options{KeepDeltas: true})
	require.NoError(t, err)
	return cr
}

// TestPutBytes_DeltaRecordPayloadNamesItsBase pins the new payload: the record's own bytes name the
// base, which is what gives each base a distinct address.
func TestPutBytes_DeltaRecordPayloadNamesItsBase(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()
	input := []byte(timestampPrefix + "09:11:04\nfirst body\n" + timestampPrefix + "09:11:05\nsecond\n")

	res, err := tp.Store.PutBytes(ctx, input, PutOptions{Path: "src/log.txt", KeepRaw: true})
	require.NoError(t, err)
	tp.Store.mu.RLock()
	deltaRoot := tp.Store.rootIndex[res.Root.Hash].Deltas
	tp.Store.mu.RUnlock()

	payload, err := tp.Store.readRootBytes(ctx, deltaRoot)
	require.NoError(t, err)
	require.Equal(t, string(wantDeltaRecord(t, res.Root.Hash, canonStripped(t, input).Deltas)), string(payload))
}

// ── GC keeps each base with its own record ───────────────────────────────────────────────────

// TestGC_EachBaseRetainsItsOwnDeltaRecord is the GC half of SP20-D3's acceptance: with two roots
// that share a removed token, a reference to either one retains that root and its OWN record, a
// reference to a record retains the base it declares, and neither drags the sibling pair along.
func TestGC_EachBaseRetainsItsOwnDeltaRecord(t *testing.T) {
	ctx := context.Background()
	inA, inB := sharedTokenInputs()
	type pair struct {
		tp         *testProject
		a, b       PutResult
		recA, recB core.Hash
	}
	seed := func(t *testing.T) pair {
		t.Helper()
		tp := newTestStore(t, withCanon(canonStripTimestamp()))
		a, err := tp.Store.PutBytes(ctx, inA, PutOptions{Path: "src/a.log", KeepRaw: true})
		require.NoError(t, err)
		b, err := tp.Store.PutBytes(ctx, inB, PutOptions{Path: "src/b.log", KeepRaw: true})
		require.NoError(t, err)
		tp.Store.mu.RLock()
		p := pair{
			tp: tp, a: a, b: b, recA: tp.Store.rootIndex[a.Root.Hash].Deltas,
			recB: tp.Store.rootIndex[b.Root.Hash].Deltas,
		}
		tp.Store.mu.RUnlock()
		require.NotEqual(t, p.recA, p.recB, "each base owns its record")
		return p
	}
	requireLive := func(t *testing.T, tp *testProject, live, gone []core.Hash) {
		t.Helper()
		for _, h := range live {
			_, err := tp.Store.GetRoot(ctx, h)
			require.NoError(t, err, "%s must be retained", h.Short())
		}
		for _, h := range gone {
			_, err := tp.Store.GetRoot(ctx, h)
			require.ErrorIs(t, err, core.ErrNotFound, "%s must be collected", h.Short())
		}
	}

	t.Run("a referenced base keeps its own record and not its sibling's", func(t *testing.T) {
		p := seed(t)
		writeCheckpointJSON(t, p.tp, "0001.json", p.b.Root.Hash.String())
		_, err := p.tp.Store.GC(ctx, forceCollect)
		require.NoError(t, err)
		requireLive(t, p.tp, []core.Hash{p.b.Root.Hash, p.recB}, []core.Hash{p.a.Root.Hash, p.recA})
		requirePutAndReadAgree(t, p.tp, p.b, inB)
	})
	t.Run("a referenced record keeps the base it declares", func(t *testing.T) {
		p := seed(t)
		writeCheckpointJSON(t, p.tp, "0001.json", p.recA.String())
		_, err := p.tp.Store.GC(ctx, forceCollect)
		require.NoError(t, err)
		requireLive(t, p.tp, []core.Hash{p.a.Root.Hash, p.recA}, []core.Hash{p.b.Root.Hash, p.recB})
		requirePutAndReadAgree(t, p.tp, p.a, inA)
	})
	t.Run("both bases referenced keep both records", func(t *testing.T) {
		p := seed(t)
		writeCheckpointJSON(t, p.tp, "0001.json", p.a.Root.Hash.String(), p.b.Root.Hash.String())
		_, err := p.tp.Store.GC(ctx, forceCollect)
		require.NoError(t, err)
		requireLive(t, p.tp, []core.Hash{p.a.Root.Hash, p.recA, p.b.Root.Hash, p.recB}, nil)
		requirePutAndReadAgree(t, p.tp, p.a, inA)
		requirePutAndReadAgree(t, p.tp, p.b, inB)
	})
}

// ── roots written before the fix ─────────────────────────────────────────────────────────────

// plantPreFixSharedRecord writes, through the store's own primitives, exactly what PutBytes wrote
// BEFORE SP20-D3 for two KeepRaw inputs whose canonicalization removed the same delta list: both
// content roots, and ONE delta record carrying the pre-fix payload (the bare delta array) and
// declaring the FIRST root, which both content roots point at.
func plantPreFixSharedRecord(t *testing.T, tp *testProject, inA, inB []byte) (a, b, shared core.Hash) {
	t.Helper()
	content := func(in []byte) (Root, []canon.Delta) {
		cr := canonStripped(t, in)
		chunks := tp.Store.splitChecked(cr.Canonical)
		refs := make([]ChunkRef, len(chunks))
		for i, c := range chunks {
			_, _, err := tp.Store.putObject(c.Hash, cr.Canonical[c.Offset:c.Offset+int64(c.Len)])
			require.NoError(t, err)
			refs[i] = ChunkRef{Hash: c.Hash, Len: c.Len}
		}
		return Root{
			Hash: chunk.RootHash(chunks), Chunks: refs,
			CanonBytes: int64(len(cr.Canonical)), RawBytes: int64(len(in)),
		}, cr.Deltas
	}
	rootA, deltasA := content(inA)
	rootB, deltasB := content(inB)
	require.Equal(t, marshalDeltas(deltasA), marshalDeltas(deltasB), "fixture sanity: identical delta lists")

	shared, err := tp.Store.putSideRecord(context.Background(), marshalDeltas(deltasA), deltaToolName,
		tokens.ClassJSON, rootA.Hash, false)
	require.NoError(t, err)
	for _, r := range []Root{rootA, rootB} {
		require.NoError(t, tp.Store.appendRoot(rootEntry{Root: r, TS: tp.Store.now(), Path: "src/x.log", Deltas: shared}))
	}
	return rootA.Hash, rootB.Hash, shared
}

// TestRestoreOriginal_PreFixSharedRecordIsNeverExact states what happens to a root written BEFORE
// SP20-D3 that points at a record its sibling owns. The index is append-only, so the fix cannot
// re-point it: it stays unrecoverable, and every path reports it that way. RestoreOriginal answers
// corrupt with no bytes; a later put of its exact bytes deduplicates onto it and reports
// canonical, never exact; the sibling that owns the record keeps restoring exactly; the pre-fix
// payload still reads; and GC keeps the trio together while the unrecoverable root is referenced.
func TestRestoreOriginal_PreFixSharedRecordIsNeverExact(t *testing.T) {
	p := newProject(t)
	tp := openOver(t, p, withCanon(canonStripTimestamp()))
	inA, inB := sharedTokenInputs()
	a, b, shared := plantPreFixSharedRecord(t, tp, inA, inB)
	require.NoError(t, tp.Store.Close())

	// Everything below reads the planted state back from disk, through the current reader.
	tp = openOver(t, p, withCanon(canonStripTimestamp()))
	ctx := context.Background()

	rec, fid, err := tp.Store.ReadDelta(ctx, shared)
	require.NoError(t, err, "a pre-fix record (a bare delta array) still reads")
	require.Equal(t, FidelityExact, fid)
	require.Equal(t, a, rec.Base, "and still declares the first root only on its index line")
	require.Len(t, rec.Deltas, 1)

	got, fid, err := tp.Store.RestoreOriginal(ctx, a)
	require.NoError(t, err)
	require.Equal(t, FidelityExact, fid, "the record's owner is unaffected")
	require.Equal(t, inA, got)

	got, fid, err = tp.Store.RestoreOriginal(ctx, b)
	require.ErrorIs(t, err, ErrDeltaCorrupt)
	require.Equal(t, FidelityCorrupt, fid)
	require.Nil(t, got, "an unrecoverable root yields no bytes")

	againB, err := tp.Store.PutBytes(ctx, inB, PutOptions{Path: "src/x.log", KeepRaw: true})
	require.NoError(t, err)
	require.Equal(t, b, againB.Root.Hash, "fixture sanity: the planted root is what a put of these bytes produces")
	require.Equal(t, FidelityCanonical, againB.Fidelity, "a dedup hit must not launder the unrecoverable root")
	againA, err := tp.Store.PutBytes(ctx, inA, PutOptions{Path: "src/x.log", KeepRaw: true})
	require.NoError(t, err)
	require.Equal(t, a, againA.Root.Hash)
	require.Equal(t, FidelityExact, againA.Fidelity, "the owner's pre-fix record is still recognised as its own")

	writeCheckpointJSON(t, tp, "0001.json", b.String())
	_, err = tp.Store.GC(ctx, forceCollect)
	require.NoError(t, err)
	for _, h := range []core.Hash{a, b, shared} {
		_, err := tp.Store.GetRoot(ctx, h)
		require.NoError(t, err, "%s: B holds the shared record and the record declares A", h.Short())
	}
	_, fid, err = tp.Store.RestoreOriginal(ctx, b)
	require.ErrorIs(t, err, ErrDeltaCorrupt)
	require.Equal(t, FidelityCorrupt, fid, "retention does not make it recoverable")
}

// ── a record address some other root already holds ───────────────────────────────────────────

// TestPutBytes_DeltaAddressHeldByAnotherRootFallsBackToAFullOriginal covers the one way a known
// delta-record address can still be the wrong record once the payload names its base: an ordinary
// content root whose bytes happen to equal the payload. Pointing at it would claim a recovery that
// root cannot give, so the put retains its original whole instead.
func TestPutBytes_DeltaAddressHeldByAnotherRootFallsBackToAFullOriginal(t *testing.T) {
	var noRedact storeOpt = func(_ *config.Config, d *Deps) { d.Redact = fixedRedactor{} }
	tp := newTestStore(t, withCanon(canonStripTimestamp()), noRedact)
	ctx := context.Background()
	input := []byte(timestampPrefix + "09:11:04\nbody\n")

	cr := canonStripped(t, input)
	base := chunk.RootHash(tp.Store.splitChecked(cr.Canonical))
	payload := wantDeltaRecord(t, base, cr.Deltas)
	squatter, err := tp.Store.PutBytes(ctx, payload, PutOptions{Path: "src/squat.json"})
	require.NoError(t, err)
	require.Equal(t, chunk.RootHash(tp.Store.splitChecked(payload)), squatter.Root.Hash,
		"fixture sanity: an ordinary content root holds the address this put's record would take")

	res, err := tp.Store.PutBytes(ctx, input, PutOptions{Path: "src/log.txt", KeepRaw: true})
	require.NoError(t, err)
	require.Equal(t, base, res.Root.Hash)
	require.Equal(t, FidelityFull, res.Fidelity, "an address another root holds is not this base's record")
	requirePutAndReadAgree(t, tp, res, input)

	tp.Store.mu.RLock()
	entry := tp.Store.rootIndex[res.Root.Hash]
	tp.Store.mu.RUnlock()
	require.True(t, entry.Deltas.IsZero(), "the content record must not point at the squatter")
	require.False(t, entry.Orig.IsZero())
	require.Equal(t, int64(1), tp.counter("store.delta.addressTaken"))
}

// TestReadDelta_PayloadAndIndexBaseMustAgree asserts a record whose payload names one base while
// its index line names another is corrupt: neither declaration can be trusted to reconstruct.
//
// The error must name the disagreement, not merely be corrupt. A reader that cannot parse the
// payload at all (the pre-SP20-D3 one, which expects a bare delta array) also answers corrupt, so
// the outcome alone would pass with the base check deleted; the message pins the check itself.
// TestReadDelta_PayloadAndIndexBaseAgreeingReadsExact is the positive twin.
func TestReadDelta_PayloadAndIndexBaseMustAgree(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()
	x := gcSeed(t, tp, "src/x.txt", "the base the payload names\n")
	y := gcSeed(t, tp, "src/y.txt", "the base the index line names\n")
	deltas := []canon.Delta{{Offset: 0, Original: timestampPrefix + "09:11:04\n", Class: canon.ClassTimestamps}}

	rec, err := tp.Store.putSideRecord(ctx, wantDeltaRecord(t, x.Hash, deltas), deltaToolName,
		tokens.ClassJSON, y.Hash, false)
	require.NoError(t, err)
	_, fid, err := tp.Store.ReadDelta(ctx, rec)
	require.ErrorIs(t, err, ErrDeltaCorrupt)
	require.Equal(t, FidelityCorrupt, fid)
	require.ErrorContains(t, err,
		"names base "+x.Hash.Short()+" in its payload but "+y.Hash.Short()+" in the index",
		"the record is corrupt BECAUSE its two base declarations disagree")
}

// TestReadDelta_PayloadAndIndexBaseAgreeingReadsExact is the positive twin of the test above: a
// record whose payload and index line name the same base reads exact, with that base and the
// deltas its payload carries.
func TestReadDelta_PayloadAndIndexBaseAgreeingReadsExact(t *testing.T) {
	tp := newTestStore(t, withCanon(canonStripTimestamp()))
	ctx := context.Background()
	x := gcSeed(t, tp, "src/x.txt", "the base both declarations name\n")
	deltas := []canon.Delta{{Offset: 0, Len: 0, Original: timestampPrefix + "09:11:04\n", Class: canon.ClassTimestamps}}

	rec, err := tp.Store.putSideRecord(ctx, wantDeltaRecord(t, x.Hash, deltas), deltaToolName,
		tokens.ClassJSON, x.Hash, false)
	require.NoError(t, err)
	got, fid, err := tp.Store.ReadDelta(ctx, rec)
	require.NoError(t, err)
	require.Equal(t, FidelityExact, fid)
	require.Equal(t, x.Hash, got.Base)
	require.Equal(t, deltas, got.Deltas)
}

// ── the verbatim claim on disk ───────────────────────────────────────────────────────────────

// TestPutBytes_VerbatimClaimPersistsOnAVersionOneLine asserts the "canonicalization changed
// nothing" claim survives a reopen, is written only when true, and does not move the record
// version: a v=1-only reader still loads the content line and merely reads its bytes as canonical.
func TestPutBytes_VerbatimClaimPersistsOnAVersionOneLine(t *testing.T) {
	p := newProject(t)
	tp := openOver(t, p, withCanon(canonStripTimestamp()))
	ctx := context.Background()
	input := []byte("no volatile token in this output\n")

	res, err := tp.Store.PutBytes(ctx, input, PutOptions{Path: "src/out.log", KeepRaw: true})
	require.NoError(t, err)
	plain, err := tp.Store.PutBytes(ctx, []byte("another output, no KeepRaw\n"), PutOptions{Path: "src/plain.log"})
	require.NoError(t, err)

	lines := tp.indexLines(t, rootsFile)
	require.Len(t, lines, 2, "a verbatim claim writes no side record")
	require.Contains(t, lines[0], `"verbatim":true`)
	require.True(t, strings.HasPrefix(lines[0], `{"v":1,`), "verbatim does not move the version: %s", lines[0])
	require.NotContains(t, lines[1], `"verbatim"`, "the key is omitted when there is no claim")
	require.NoError(t, tp.Store.Close())

	reopened := openOver(t, p, withCanon(canonStripTimestamp()))
	got, fid, err := reopened.Store.RestoreOriginal(ctx, res.Root.Hash)
	require.NoError(t, err)
	require.Equal(t, FidelityExact, fid)
	require.Equal(t, input, got)
	_, fid, err = reopened.Store.RestoreOriginal(ctx, plain.Root.Hash)
	require.NoError(t, err)
	require.Equal(t, FidelityCanonical, fid)
}
