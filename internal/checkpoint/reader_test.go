package checkpoint_test

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/checkpoint/checkpointtest"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
	"github.com/stretchr/testify/require"
)

// readerSession is the session every fixture below writes under unless a test needs two.
const readerSession = core.SessionID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A")

// readerFixtureFileMode is the mode a corrupted artifact is temporarily reopened with.
// paths.CreateNew chmods every checkpoint to 0444, which on Windows sets FILE_ATTRIBUTE_READONLY,
// so a test that wants to tamper with one has to clear that first.
const readerFixtureFileMode = 0o600

// readerEnv is one reader under test plus the observers it was constructed with, so a test can
// assert the counter and the Loud the reader itself emitted rather than a package-level one.
type readerEnv struct {
	l    paths.Layout
	r    checkpoint.Reader
	reg  obs.Registry
	loud *[]string
}

// newReaderEnv builds a real .qompack layout under t.TempDir() and opens a real reader over it.
// There are no fake packages here: every checkpoint below is written with paths.CreateNew and
// every manifest line with paths.AppendManifest, exactly as Finalize will.
func newReaderEnv(t *testing.T) *readerEnv {
	t.Helper()
	l := paths.Of(t.TempDir())
	require.NoError(t, paths.EnsureLayout(l))

	reg := obs.New(testutil.NewFakeClock(testutil.Epoch))
	r, err := checkpoint.OpenReader(l.Root, logging.Nop(), reg)
	require.NoError(t, err)

	var msgs []string
	logging.AttachLoudObserver(func(msg string, _ ...any) { msgs = append(msgs, msg) })
	t.Cleanup(func() { logging.AttachLoudObserver(nil) })

	return &readerEnv{l: l, r: r, reg: reg, loud: &msgs}
}

// write marshals one checkpoint, writes the immutable artifact and appends its manifest line,
// returning the canonical bytes so a test can compute the digest the manifest now claims.
func (e *readerEnv) write(t *testing.T, clk core.Clock, c checkpoint.Checkpoint) []byte {
	t.Helper()
	b, err := checkpoint.Marshal(c)
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(e.l, c.Seq), b))
	require.NoError(t, paths.AppendManifest(e.l, paths.ManifestEntry{
		Seq:     c.Seq,
		SHA256:  core.Hash(sha256.Sum256(b)).String(),
		Bytes:   int64(len(b)),
		Created: core.NowMilli(clk),
	}))
	return b
}

// chain writes n checkpoints for s, 1..n, each naming its predecessor's filename as Parent.
func (e *readerEnv) chain(t *testing.T, s core.SessionID, n int) {
	t.Helper()
	clk := testutil.NewFakeClock(testutil.Epoch)
	for seq := 1; seq <= n; seq++ {
		var parent string
		if seq > 1 {
			parent = filepath.Base(paths.CheckpointPath(e.l, core.CheckpointSeq(seq-1)))
		}
		e.write(t, clk, checkpoint.Checkpoint{
			Session: s,
			Seq:     core.CheckpointSeq(seq),
			Created: checkpoint.CreatedNow(clk),
			Parent:  parent,
			UserIntent: checkpoint.UserIntent{
				Original: "make the refresh endpoint stop losing sessions",
			},
			CurrentWork: checkpoint.CurrentWork{Goal: "checkpoint the reader", NextStep: "verify"},
		})
	}
}

// corrupt rewrites one byte of an artifact in place, leaving its length unchanged, so the only
// thing that differs from the manifest is the digest.
func (e *readerEnv) corrupt(t *testing.T, seq core.CheckpointSeq) {
	t.Helper()
	p := paths.Long(paths.CheckpointPath(e.l, seq))
	require.NoError(t, os.Chmod(p, readerFixtureFileMode))
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.NotEmpty(t, b)
	b[len(b)/2] ^= 0x20
	require.NoError(t, os.WriteFile(p, b, readerFixtureFileMode))
}

// TestListIsAscendingAndCarriesManifestFields asserts List maps paths.ReadManifest to []Ref with
// the artifact's own path, digest, size and creation instant, ascending by seq.
func TestListIsAscendingAndCarriesManifestFields(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 3)

	refs, err := e.r.List(context.Background())
	require.NoError(t, err)
	require.Len(t, refs, 3)

	for i, r := range refs {
		seq := core.CheckpointSeq(i + 1)
		require.Equal(t, seq, r.Seq)
		require.Equal(t, paths.CheckpointPath(e.l, seq), r.Path)
		require.Greater(t, r.Bytes, int64(0))
		require.Equal(t, core.NowMilli(testutil.NewFakeClock(testutil.Epoch)), r.Created)

		b, err := os.ReadFile(paths.Long(r.Path))
		require.NoError(t, err)
		require.Equal(t, core.Hash(sha256.Sum256(b)), r.SHA256)
		require.Equal(t, int64(len(b)), r.Bytes)
	}
}

// TestReaderRefLeavesWriterOnlyFieldsZero is the reader half of the writer-only-fields contract:
// Tokens and Frontier are populated by Finalize alone, so every Ref a Reader hands back leaves
// them zero until a versioned, immutable seq-to-frontier record exists. SP-11 must not read a
// zero here as "no tokens" or "frontier 0".
func TestReaderRefLeavesWriterOnlyFieldsZero(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 2)
	ctx := context.Background()

	refs, err := e.r.List(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, refs)

	_, getRef, err := e.r.Get(ctx, core.CheckpointSeq(2))
	require.NoError(t, err)
	_, latestRef, err := e.r.Latest(ctx, readerSession)
	require.NoError(t, err)

	for _, r := range append(refs, getRef, latestRef) {
		require.Equal(t, core.Tokens(0), r.Tokens, "Tokens is writer-only")
		require.Equal(t, core.TurnIndex(0), r.Frontier, "Frontier is writer-only")
	}
}

// TestListRejectsMalformedManifestLine asserts List surfaces a manifest it cannot read rather
// than skipping the line. The manifest is the index every other read depends on, so a skip would
// hide a checkpoint instead of reporting one.
func TestListRejectsMalformedManifestLine(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 2)

	f, err := os.OpenFile(paths.Long(paths.ManifestPath(e.l)), os.O_WRONLY|os.O_APPEND, readerFixtureFileMode)
	require.NoError(t, err)
	_, err = f.WriteString("garbage\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = e.r.List(context.Background())
	require.ErrorIs(t, err, core.ErrContract)
	require.Equal(t, int64(1), e.reg.Counter("checkpoint.manifest_badline").Value())
	require.Len(t, *e.loud, 1)
}

// TestGetDetectsManifestMismatch is §12's "refuse to use the affected checkpoint": a byte that
// changed under the manifest's digest is a contract violation, reported loudly and counted, never
// a silently-returned document.
func TestGetDetectsManifestMismatch(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 2)
	e.corrupt(t, 2)

	_, _, err := e.r.Get(context.Background(), core.CheckpointSeq(2))
	require.ErrorIs(t, err, core.ErrContract)
	require.Contains(t, err.Error(), "checkpoint 0002")
	require.Equal(t, int64(1), e.reg.Counter("checkpoint.manifest_mismatch").Value())
	require.Len(t, *e.loud, 1)
	require.Equal(t, "checkpoint manifest mismatch", (*e.loud)[0])
}

// TestGetMissingArtifactIsContract distinguishes the two absences. A seq the manifest never
// recorded is a miss; a seq it DID record whose file is gone is a broken invariant, because the
// manifest is the thing that promised the file exists.
func TestGetMissingArtifactIsContract(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 2)

	p := paths.Long(paths.CheckpointPath(e.l, core.CheckpointSeq(2)))
	require.NoError(t, os.Chmod(p, readerFixtureFileMode))
	require.NoError(t, os.Remove(p))

	_, _, err := e.r.Get(context.Background(), core.CheckpointSeq(2))
	require.ErrorIs(t, err, core.ErrContract)
	require.Equal(t, int64(1), e.reg.Counter("checkpoint.manifest_mismatch").Value())
}

// TestGetAbsentSeqIsNotFound pins the other half: nothing was ever promised, so nothing is
// broken.
func TestGetAbsentSeqIsNotFound(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 2)

	_, _, err := e.r.Get(context.Background(), core.CheckpointSeq(99))
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Empty(t, *e.loud, "a miss is not a contract violation")
}

// TestLatestFallsBackToParent is §12's fallback: a corrupted head does not end the chain, it is
// stepped over.
func TestLatestFallsBackToParent(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 3)
	e.corrupt(t, 3)

	c, ref, err := e.r.Latest(context.Background(), readerSession)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(2), c.Seq)
	require.Equal(t, core.CheckpointSeq(2), ref.Seq)
	require.Equal(t, int64(1), e.reg.Counter("checkpoint.manifest_mismatch").Value())
}

// TestLatestInheritsAnotherSessionsChain is the resumed-session rule: a session with no
// checkpoint of its own legitimately inherits the project's newest verifying one, rather than
// starting from nothing.
func TestLatestInheritsAnotherSessionsChain(t *testing.T) {
	e := newReaderEnv(t)
	clk := testutil.NewFakeClock(testutil.Epoch)
	const other = core.SessionID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4B")

	e.write(t, clk, checkpoint.Checkpoint{Session: readerSession, Seq: 1, Created: checkpoint.CreatedNow(clk)})
	e.write(t, clk, checkpoint.Checkpoint{
		Session: other, Seq: 2, Created: checkpoint.CreatedNow(clk),
		Parent: filepath.Base(paths.CheckpointPath(e.l, core.CheckpointSeq(1))),
	})

	own, _, err := e.r.Latest(context.Background(), readerSession)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(1), own.Seq, "a session's own newest checkpoint wins")

	inherited, _, err := e.r.Latest(context.Background(), core.SessionID("sess_unknown"))
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(2), inherited.Seq, "a resumed session inherits the newest")
}

// TestLatestNotFoundWhenNothingVerifies asserts Latest reports the miss rather than degrading the
// mode itself; the daemon wiring is what calls contract.Monitor.Degrade.
func TestLatestNotFoundWhenNothingVerifies(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 2)
	e.corrupt(t, 1)
	e.corrupt(t, 2)

	_, _, err := e.r.Latest(context.Background(), readerSession)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Equal(t, int64(2), e.reg.Counter("checkpoint.manifest_mismatch").Value())
}

// TestLatestOnEmptyStoreIsNotFound covers the cold project: no manifest at all.
func TestLatestOnEmptyStoreIsNotFound(t *testing.T) {
	e := newReaderEnv(t)

	_, _, err := e.r.Latest(context.Background(), readerSession)
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestChainReturnsOldestFirst asserts Chain follows Parent filenames down to the root and returns
// the result oldest-first, which is the order a rehydrator replays it in.
func TestChainReturnsOldestFirst(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 3)

	got, err := e.r.Chain(context.Background(), core.CheckpointSeq(3))
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, []core.CheckpointSeq{1, 2, 3},
		[]core.CheckpointSeq{got[0].Seq, got[1].Seq, got[2].Seq})
	require.Empty(t, got[0].Parent, "the root names no parent")
}

// TestChainOfRootIsItself asserts the single-element case: seq 1 has no parent to follow.
func TestChainOfRootIsItself(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 1)

	got, err := e.r.Chain(context.Background(), core.CheckpointSeq(1))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, core.CheckpointSeq(1), got[0].Seq)
}

// TestChainRejectsCycle asserts a parent whose seq is not strictly less than its child's aborts
// rather than walking forever. A self-parent is the degenerate case a corrupt or hand-edited
// artifact produces.
func TestChainRejectsCycle(t *testing.T) {
	e := newReaderEnv(t)
	clk := testutil.NewFakeClock(testutil.Epoch)

	e.write(t, clk, checkpoint.Checkpoint{Session: readerSession, Seq: 1, Created: checkpoint.CreatedNow(clk)})
	e.write(t, clk, checkpoint.Checkpoint{
		Session: readerSession, Seq: 2, Created: checkpoint.CreatedNow(clk),
		Parent: filepath.Base(paths.CheckpointPath(e.l, core.CheckpointSeq(2))),
	})

	_, err := e.r.Chain(context.Background(), core.CheckpointSeq(2))
	require.ErrorIs(t, err, core.ErrContract)
}

// TestChainRejectsUnparseableParent asserts a Parent that is not a checkpoint filename is a
// contract violation rather than a silently shortened chain.
func TestChainRejectsUnparseableParent(t *testing.T) {
	e := newReaderEnv(t)
	clk := testutil.NewFakeClock(testutil.Epoch)

	e.write(t, clk, checkpoint.Checkpoint{
		Session: readerSession, Seq: 1, Created: checkpoint.CreatedNow(clk),
		Parent: "MANIFEST.jsonl",
	})

	_, err := e.r.Chain(context.Background(), core.CheckpointSeq(1))
	require.ErrorIs(t, err, core.ErrContract)
}

// TestVerifyReturnsMismatchedSeqs is the read half of what `qompack fsck` reports: every artifact
// re-hashed against the manifest, and exactly the mismatching seqs returned, ascending.
func TestVerifyReturnsMismatchedSeqs(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 4)
	e.corrupt(t, 3)
	e.corrupt(t, 1)

	bad, err := e.r.Verify(context.Background())
	require.NoError(t, err)
	require.Equal(t, []core.CheckpointSeq{1, 3}, bad)
}

// TestVerifyIsEmptyNotNilWhenClean pins the empty-slice answer. A nil would decode as JSON null
// in fsck's report, which is a different claim from "nothing mismatched".
func TestVerifyIsEmptyNotNilWhenClean(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 2)

	bad, err := e.r.Verify(context.Background())
	require.NoError(t, err)
	require.NotNil(t, bad)
	require.Empty(t, bad)
}

// TestVerifyRejectsMalformedManifestLine asserts Verify surfaces an unreadable manifest for the
// same reason List does.
func TestVerifyRejectsMalformedManifestLine(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 1)

	f, err := os.OpenFile(paths.Long(paths.ManifestPath(e.l)), os.O_WRONLY|os.O_APPEND, readerFixtureFileMode)
	require.NoError(t, err)
	_, err = f.WriteString("{not json\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = e.r.Verify(context.Background())
	require.ErrorIs(t, err, core.ErrContract)
}

// TestReaderConformanceSuite runs SP-01's checkpointtest.RunReaderSuite against the real reader.
// It is what turns the suite's Rule W-1 skip off for this package: the shape block still runs,
// and every behaviour block SP-01 wrote now has to pass.
func TestReaderConformanceSuite(t *testing.T) {
	checkpointtest.RunReaderSuite(t, "checkpoint.OpenReader", func(t *testing.T) checkpointtest.ReaderFixture {
		e := newReaderEnv(t)
		e.chain(t, readerSession, 3)
		return checkpointtest.ReaderFixture{
			Reader:    e.r,
			Session:   readerSession,
			Seq:       core.CheckpointSeq(3),
			AbsentSeq: core.CheckpointSeq(99),
		}
	})
}

// TestOpenReaderAcceptsNilObservers asserts a caller that has not wired logging or metrics yet
// still gets a usable Reader rather than a nil dereference on the first contract violation, the
// same guarantee obs.go gives the receiver-less functions.
func TestOpenReaderAcceptsNilObservers(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))

	r, err := checkpoint.OpenReader(root, nil, nil)
	require.NoError(t, err)

	_, _, err = r.Latest(context.Background(), readerSession)
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestReaderRejectsUnparseableManifestDigest covers the line that decodes as JSON but does not
// name a digest. paths.ReadManifest accepts it — sha256 is a plain string in its schema — so this
// package is the only place it can be caught, and it is caught as a contract violation because a
// Ref whose SHA256 could not be parsed cannot verify anything.
func TestReaderRejectsUnparseableManifestDigest(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 1)
	require.NoError(t, paths.AppendManifest(e.l, paths.ManifestEntry{
		Seq: 2, SHA256: "not-a-digest", Bytes: 1, Created: core.NowMilli(testutil.NewFakeClock(testutil.Epoch)),
	}))
	ctx := context.Background()

	_, err := e.r.List(ctx)
	require.ErrorIs(t, err, core.ErrContract)

	_, _, err = e.r.Get(ctx, core.CheckpointSeq(2))
	require.ErrorIs(t, err, core.ErrContract)

	_, err = e.r.Verify(ctx)
	require.ErrorIs(t, err, core.ErrContract)

	require.Equal(t, int64(3), e.reg.Counter("checkpoint.manifest_badline").Value())
}

// TestReaderHonoursContextCancellation asserts every read reports the caller's cancellation
// rather than finishing the walk. Latest and Chain in particular read one artifact per link, so a
// PreCompact that has already run out of budget must be able to stop them.
func TestReaderHonoursContextCancellation(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 2)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := e.r.List(ctx)
	require.ErrorIs(t, err, context.Canceled)

	_, _, err = e.r.Get(ctx, core.CheckpointSeq(1))
	require.ErrorIs(t, err, context.Canceled)

	_, _, err = e.r.Latest(ctx, readerSession)
	require.ErrorIs(t, err, context.Canceled)

	_, err = e.r.Chain(ctx, core.CheckpointSeq(2))
	require.ErrorIs(t, err, context.Canceled)

	_, err = e.r.Verify(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

// TestChainSurfacesAMismatchedAncestor asserts Chain does not step over a corrupted link the way
// Latest does. Latest is looking for the best available checkpoint; Chain is asked for a specific
// lineage, and a lineage with a hole in it is not that lineage.
func TestChainSurfacesAMismatchedAncestor(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 3)
	e.corrupt(t, 1)

	_, err := e.r.Chain(context.Background(), core.CheckpointSeq(3))
	require.ErrorIs(t, err, core.ErrContract)
}

// TestChainOfAbsentSeqIsNotFound pins the miss, matching Get.
func TestChainOfAbsentSeqIsNotFound(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 1)

	_, err := e.r.Chain(context.Background(), core.CheckpointSeq(9))
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestChainRejectsAMissingAncestor covers a Parent that names a seq the manifest never recorded.
func TestChainRejectsAMissingAncestor(t *testing.T) {
	e := newReaderEnv(t)
	clk := testutil.NewFakeClock(testutil.Epoch)

	e.write(t, clk, checkpoint.Checkpoint{
		Session: readerSession, Seq: 2, Created: checkpoint.CreatedNow(clk),
		Parent: filepath.Base(paths.CheckpointPath(e.l, core.CheckpointSeq(1))),
	})

	_, err := e.r.Chain(context.Background(), core.CheckpointSeq(2))
	require.ErrorIs(t, err, core.ErrNotFound)
}

// TestListAndGetRejectAMalformedManifest asserts every read surfaces the same unreadable-manifest
// condition, so no entry point can be used to work around a manifest another one refuses.
func TestListAndGetRejectAMalformedManifest(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 1)

	f, err := os.OpenFile(paths.Long(paths.ManifestPath(e.l)), os.O_WRONLY|os.O_APPEND, readerFixtureFileMode)
	require.NoError(t, err)
	_, err = f.WriteString("[]\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	ctx := context.Background()

	_, _, err = e.r.Get(ctx, core.CheckpointSeq(1))
	require.ErrorIs(t, err, core.ErrContract)

	_, _, err = e.r.Latest(ctx, readerSession)
	require.ErrorIs(t, err, core.ErrContract)

	_, err = e.r.Chain(ctx, core.CheckpointSeq(1))
	require.ErrorIs(t, err, core.ErrContract)
}

// TestGetRefusesANewerSchemaVersion covers the last step of a read: an artifact whose bytes match
// the manifest exactly but whose schema this build cannot read. Migrate rejects it on its version
// field, which is the honest signal — the digest verified, so the file is not corrupt, it is
// simply from a newer plugin.
func TestGetRefusesANewerSchemaVersion(t *testing.T) {
	e := newReaderEnv(t)
	raw := []byte(`{"version":9999}` + "\n")

	require.NoError(t, paths.CreateNew(paths.CheckpointPath(e.l, core.CheckpointSeq(1)), raw))
	require.NoError(t, paths.AppendManifest(e.l, paths.ManifestEntry{
		Seq:     1,
		SHA256:  core.Hash(sha256.Sum256(raw)).String(),
		Bytes:   int64(len(raw)),
		Created: core.NowMilli(testutil.NewFakeClock(testutil.Epoch)),
	}))

	_, _, err := e.r.Get(context.Background(), core.CheckpointSeq(1))
	require.ErrorIs(t, err, core.ErrContract)
	require.Empty(t, *e.loud, "a readable artifact from a newer plugin is not a manifest mismatch")
	require.Equal(t, int64(0), e.reg.Counter("checkpoint.manifest_mismatch").Value())
}

// TestChainStopsAtTheDepthCapWithoutReportingCorruption is the "this project is old" case.
// Finalize opens every successor draft with parent = the seq it just wrote, so a project's
// checkpoints are one unbroken chain for its whole life and every PreCompact and every cadence
// tick adds a link: crossing the reader's depth cap is what long use looks like, not damage.
// Chain must hand back the newest window it walked together with a truncation signal that is NOT
// core.ErrContract — the class it uses for a corrupt artifact, an unreadable manifest and a cycle
// — because a caller that cannot tell those apart reports a broken session to a user whose store
// is perfectly healthy.
//
// Building one link past the cap is the only way to reach the branch, and the walk re-reads and
// re-hashes every artifact it visits, so this is one of the slower tests in the package.
func TestChainStopsAtTheDepthCapWithoutReportingCorruption(t *testing.T) {
	e := newReaderEnv(t)
	const links = 1025 // one past maxChainDepth
	e.chain(t, readerSession, links)

	got, err := e.r.Chain(context.Background(), core.CheckpointSeq(links))
	require.ErrorIs(t, err, checkpoint.ErrChainTruncated)
	require.NotErrorIs(t, err, core.ErrContract, "a long chain is not a broken store")
	require.Len(t, got, 1024, "the newest maxChainDepth links come back")
	require.Equal(t, core.CheckpointSeq(links), got[len(got)-1].Seq, "the window keeps the newest end")
	require.Equal(t, core.CheckpointSeq(2), got[0].Seq, "and is still ordered oldest-first")
}

// loudCapture is an injected logger that records Loud messages only. The F4-9 memo is per
// reader, so the test must count what THIS reader emitted, not the process-wide ring.
type loudCapture struct {
	mu   sync.Mutex
	msgs []string
}

func (l *loudCapture) With(...any) logging.Logger { return l }
func (l *loudCapture) Debug(string, ...any)       {}
func (l *loudCapture) Info(string, ...any)        {}
func (l *loudCapture) Warn(string, ...any)        {}
func (l *loudCapture) Error(string, ...any)       {}
func (l *loudCapture) Loud(msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, msg)
}

func (l *loudCapture) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.msgs...)
}

// TestListThenVerifyLoudsEachIntegrityDefectOnce is the owning-package half of F4-9:
// an orphan, a missing artifact and a flipped-bit artifact each Loud exactly once across
// List then Verify then Verify again (sweepIntegrity / noteIntegrity memo).
func TestListThenVerifyLoudsEachIntegrityDefectOnce(t *testing.T) {
	l := paths.Of(t.TempDir())
	require.NoError(t, paths.EnsureLayout(l))
	log := &loudCapture{}
	reg := obs.New(testutil.NewFakeClock(testutil.Epoch))
	r, err := checkpoint.OpenReader(l.Root, log, reg)
	require.NoError(t, err)
	e := &readerEnv{l: l, r: r, reg: reg}
	clk := testutil.NewFakeClock(testutil.Epoch)

	e.write(t, clk, checkpoint.Checkpoint{
		Session: readerSession, Seq: 1, Created: checkpoint.CreatedNow(clk),
		UserIntent:  checkpoint.UserIntent{Original: "missing artifact fixture"},
		CurrentWork: checkpoint.CurrentWork{Goal: "gone", NextStep: "verify"},
	})
	require.NoError(t, os.Remove(paths.Long(paths.CheckpointPath(l, 1))))

	e.write(t, clk, checkpoint.Checkpoint{
		Session: readerSession, Seq: 2, Created: checkpoint.CreatedNow(clk),
		UserIntent:  checkpoint.UserIntent{Original: "flipped bit fixture"},
		CurrentWork: checkpoint.CurrentWork{Goal: "mismatch", NextStep: "verify"},
	})
	e.corrupt(t, 2)

	orphan := checkpoint.Checkpoint{
		Session: readerSession, Seq: 3, Created: checkpoint.CreatedNow(clk),
		UserIntent:  checkpoint.UserIntent{Original: "orphan artifact fixture"},
		CurrentWork: checkpoint.CurrentWork{Goal: "unclaimed", NextStep: "verify"},
	}
	b, err := checkpoint.Marshal(orphan)
	require.NoError(t, err)
	require.NoError(t, paths.CreateNew(paths.CheckpointPath(l, 3), b))

	ctx := context.Background()
	_, err = r.List(ctx)
	require.NoError(t, err)
	_, err = r.Verify(ctx)
	require.NoError(t, err)
	_, err = r.Verify(ctx)
	require.NoError(t, err)

	got := log.all()
	require.Len(t, got, 3, "exactly one Loud per artifact, not one per pass: %v", got)
	joined := got[0] + "\n" + got[1] + "\n" + got[2]
	require.Contains(t, joined, "no MANIFEST line")
	require.Contains(t, joined, "missing")
	require.Contains(t, joined, "does not match its MANIFEST digest")
}

// TestLatestNamesTheCheckpointsItRefused is F-C4-UAT03-1 (owner decision D49) at the reader: a
// fallback is never silent, so the Ref Latest returns names every newer checkpoint it stepped over
// because it did not verify, newest first. A clean read names none, and so does a read that only
// passed over checkpoints OLDER than the one it returned.
func TestLatestNamesTheCheckpointsItRefused(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 4)
	e.corrupt(t, 4)
	e.corrupt(t, 3)
	e.corrupt(t, 1)

	c, ref, err := e.r.Latest(context.Background(), readerSession)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(2), c.Seq)
	require.Equal(t, []core.CheckpointSeq{4, 3}, ref.Refused)

	clean := newReaderEnv(t)
	clean.chain(t, readerSession, 2)
	_, ref, err = clean.r.Latest(context.Background(), readerSession)
	require.NoError(t, err)
	require.Empty(t, ref.Refused, "a clean read refused nothing")
}

// TestLatestNamesTheRefusalsWhenNothingVerifies keeps the account when there is no checkpoint left
// to fall back to: the error is still ErrNotFound, and the Ref beside it names what was refused, so
// the rehydration built from L0 and the ledger can say why it has no checkpoint.
func TestLatestNamesTheRefusalsWhenNothingVerifies(t *testing.T) {
	e := newReaderEnv(t)
	e.chain(t, readerSession, 2)
	e.corrupt(t, 1)
	e.corrupt(t, 2)

	_, ref, err := e.r.Latest(context.Background(), readerSession)
	require.ErrorIs(t, err, core.ErrNotFound)
	require.Equal(t, core.CheckpointSeq(0), ref.Seq)
	require.Equal(t, []core.CheckpointSeq{2, 1}, ref.Refused)
}
