package checkpoint_test

// The seal's durability barriers, pinned and then cut (C1.6/D24, w6-ckptsync).
//
// Finalize returning nil is a promise: the daemon answers PreCompact on it, the host compacts the
// conversation away, the draft that held the session is deleted and the successor draft names the
// new checkpoint as its parent. So a power cut after the promise must find the checkpoint sealed,
// and a cut before it must find the checkpoint absent — no MANIFEST line — with the draft still on
// disk to seal again. The one forbidden outcome is a MANIFEST line naming bytes or a name the cut
// took (the reader's core.ErrContract), and a seal whose segment marks the cut took (the DPI guard,
// which would let the next draft encode the same segments again).
//
// sealModel is the harness. It is the FileWriter's paths.Barriers and a wrapper round the draft's
// SegmentLog, so it sees every barrier the seal issues, in order. It also keeps a POSIX worst-case
// model of what each barrier made durable: a file's bytes up to the size its last sync saw, a
// directory's entries as its last sync listed them, the segment log up to its last sync. A cut at
// barrier k stops the seal there — that barrier and every later one fail, the power being off — and
// snapshots the disk; afterwards the disk is rebuilt either as a process crash leaves it (the page
// cache survives: the snapshot) or as a power loss does (only what the barriers made durable), the
// project is reopened, and the outcome is classified through a fresh Reader.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// errPowerCut is what every barrier returns from the cut onwards.
var errPowerCut = errors.New("injected power cut")

// Barrier names as sealModel records them: file:<base> for a file sync, dir:<base> for a directory
// sync, and marks for the segment log's sync.
const (
	stepMarks         = "marks"
	stepCheckpointDir = "dir:checkpoints"
	stepManifestFile  = "file:MANIFEST.jsonl"
)

// sealModel counts, cuts and models one seal's barriers. It is armed only around the call under
// test; unarmed, every barrier is the real call and nothing is recorded.
type sealModel struct {
	t       *testing.T
	cpDir   string
	segPath string
	// draftPath, draftSeq and debugPath are what must be untouched until the seal is complete: the
	// draft still on disk at the sequence being sealed, and no state/precompact.json naming it.
	draftPath string
	draftSeq  core.CheckpointSeq
	debugPath string

	armed bool
	cutAt int // 1-based barrier index to cut at; 0 cuts nothing
	cut   bool
	steps []string
	// dependentsTouched names every barrier at which something that depends on the seal had
	// already happened: the draft retired or replaced, or precompact.json written for the seq.
	dependentsTouched []string

	durNames map[string]bool  // checkpoints/ entries whose name is durable
	durSize  map[string]int64 // checkpoints/ file -> bytes durable (its last sync; baseline for old files)
	segDur   int64            // index/segments.jsonl bytes durable
	snapCP   map[string][]byte
	snapSeg  []byte
}

func newSealModel(t *testing.T, root string) *sealModel {
	l := paths.Of(root)
	return &sealModel{
		t: t, cpDir: l.Checkpoints, segPath: filepath.Join(l.Index, "segments.jsonl"),
		debugPath: filepath.Join(l.State, "precompact.json"),
	}
}

// arm starts recording. Everything on disk now is taken as durable, except index/segments.jsonl,
// whose durable size the caller fixed with baselineSegments before Advance appended its marks.
func (m *sealModel) arm(draftPath string, draftSeq core.CheckpointSeq, cutAt int) {
	m.draftPath, m.draftSeq, m.cutAt = draftPath, draftSeq, cutAt
	m.durNames, m.durSize = map[string]bool{}, map[string]int64{}
	for name, b := range m.readCheckpoints() {
		m.durNames[name] = true
		m.durSize[name] = int64(len(b))
	}
	m.armed = true
}

// baselineSegments records index/segments.jsonl's current size as durable. The fixture calls it
// right after a store Flush and before Advance, so the segment's own records are durable and the
// marks Advance appends are not, which is exactly the state the idle path leaves.
func (m *sealModel) baselineSegments() { m.segDur = fileSize(m.t, m.segPath) }

func (m *sealModel) barriers() paths.Barriers {
	return paths.Barriers{
		SyncFile: func(f *os.File) error {
			base := filepath.Base(f.Name())
			return m.barrier("file:"+base, f.Sync, func() {
				fi, err := f.Stat()
				require.NoError(m.t, err)
				m.durSize[base] = fi.Size()
			})
		},
		SyncDir: func(dir string) error {
			return m.barrier("dir:"+filepath.Base(dir), func() error { return paths.SyncDir(dir) }, func() {
				if filepath.Clean(dir) != filepath.Clean(m.cpDir) {
					return
				}
				m.durNames = map[string]bool{}
				for name := range m.readCheckpoints() {
					m.durNames[name] = true
				}
			})
		},
	}
}

// barrier runs one durability call under the model: recorded, checked against the seal's
// dependents, cut if it is the chosen one, and credited to the durable model only once it returns.
func (m *sealModel) barrier(step string, do func() error, credit func()) error {
	if !m.armed {
		return do()
	}
	if m.cut {
		return errPowerCut
	}
	m.steps = append(m.steps, step)
	m.checkDependents(step)
	if len(m.steps) == m.cutAt {
		m.snapshot()
		m.cut = true
		return errPowerCut
	}
	if err := do(); err != nil {
		return err
	}
	credit()
	return nil
}

// checkDependents records a barrier reached after something that depends on the seal: the seal is
// not complete until the last barrier returns, so nothing may yet have acted on it.
func (m *sealModel) checkDependents(step string) {
	raw, err := os.ReadFile(paths.Long(m.draftPath))
	switch {
	case err != nil:
		m.dependentsTouched = append(m.dependentsTouched, step+": draft file gone ("+err.Error()+")")
	default:
		var dw draftWire
		if jerr := json.Unmarshal(raw, &dw); jerr != nil || dw.Seq != m.draftSeq {
			m.dependentsTouched = append(m.dependentsTouched,
				fmt.Sprintf("%s: draft file no longer holds seq %d", step, m.draftSeq))
		}
	}
	if raw, err := os.ReadFile(paths.Long(m.debugPath)); err == nil {
		var dbg precompactDebug
		if json.Unmarshal(raw, &dbg) == nil && dbg.Seq == m.draftSeq {
			m.dependentsTouched = append(m.dependentsTouched, step+": precompact.json already names the seal")
		}
	}
}

func (m *sealModel) readCheckpoints() map[string][]byte {
	out := map[string][]byte{}
	entries, err := os.ReadDir(paths.Long(m.cpDir))
	require.NoError(m.t, err)
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(paths.Long(filepath.Join(m.cpDir, e.Name())))
		require.NoError(m.t, err)
		out[e.Name()] = b
	}
	return out
}

func (m *sealModel) snapshot() {
	m.snapCP = m.readCheckpoints()
	b, err := os.ReadFile(paths.Long(m.segPath))
	require.NoError(m.t, err)
	m.snapSeg = b
}

// rebuild puts the disk into the state the cut leaves: the snapshot for a process crash, or only
// what the barriers made durable for a power loss. The store must be closed first, so no handle holds
// a file this rewrites.
func (m *sealModel) rebuild(powerLoss bool) {
	current := m.readCheckpoints()
	names := map[string]bool{}
	for n := range current {
		names[n] = true
	}
	for n := range m.snapCP {
		names[n] = true
	}
	for n := range names {
		content, inSnap := m.snapCP[n]
		keep := inSnap
		if powerLoss {
			keep = inSnap && m.durNames[n]
			if keep {
				content = content[:min(m.durSize[n], int64(len(content)))]
			}
		}
		p := filepath.Join(m.cpDir, n)
		if _, err := os.Lstat(paths.Long(p)); err == nil {
			require.NoError(m.t, os.Chmod(paths.Long(p), 0o600))
			if !keep {
				require.NoError(m.t, os.Remove(paths.Long(p)))
			}
		}
		if keep {
			require.NoError(m.t, os.WriteFile(paths.Long(p), content, 0o600))
		}
	}
	seg := m.snapSeg
	if powerLoss {
		seg = seg[:min(m.segDur, int64(len(seg)))]
	}
	require.NoError(m.t, os.WriteFile(paths.Long(m.segPath), seg, 0o600))
}

// fileSize is p's current size.
func fileSize(t *testing.T, p string) int64 {
	t.Helper()
	fi, err := os.Stat(paths.Long(p))
	require.NoError(t, err)
	return fi.Size()
}

// recordingSegments is the draft's SegmentLog with its Sync routed through the model.
type recordingSegments struct {
	store.SegmentLog
	m *sealModel
}

func (r recordingSegments) Sync(ctx context.Context) error {
	s, ok := r.SegmentLog.(store.SegmentSync)
	require.True(r.m.t, ok, "fixture: the store's segment log offers SegmentSync")
	return r.m.barrier(stepMarks, func() error { return s.Sync(ctx) }, func() {
		r.m.segDur = fileSize(r.m.t, r.m.segPath)
	})
}

// reopenFx closes f's store and ledger and opens a fresh store, graph, ledger and writer over the
// same project, the way a restarted daemon would. The pins fake carries over: it holds no state on
// disk.
func reopenFx(t *testing.T, f *fx) *fx {
	t.Helper()
	st, err := store.Open(f.p.Root, f.p.Cfg, store.Deps{Log: f.p.Log, Clock: f.p.Clock})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	g, err := dag.Open(f.p.Root, f.p.Cfg, f.p.Log)
	require.NoError(t, err)
	led, err := negknow.Open(f.p.Root, f.p.Cfg, nil, negknow.Deps{
		Session: writerSession, Clock: f.p.Clock, Log: f.p.Log, Graph: g, Store: st,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })
	w, err := checkpoint.OpenWriter(f.p.Root, f.p.Cfg, f.p.Log, obs.New(f.p.Clock), f.p.Clock)
	require.NoError(t, err)
	re := &fx{t: t, p: f.p, store: st, graph: g, ledger: led, pins: f.pins, w: w, sess: f.sess, pos: f.pos}
	re.src = checkpoint.SourceSet{
		Store: st, Segments: st.Segments(), Ledger: led, Pins: f.pins,
		Graph: g, Grammar: grammar.New(), Tokens: tokens.NewForProject(f.p.Cfg, tokens.DefaultCalibPath(), f.p.Root),
	}
	return re
}

// sealFixture is one project with a draft ready to seal and the model armed round it. prior is how
// many checkpoints are sealed first, with the real barriers: 0 seals the project's first checkpoint
// (the seal creates MANIFEST.jsonl), 1 seals its second.
type sealFixture struct {
	f     *fx
	m     *sealModel
	seq   core.CheckpointSeq
	segID core.SegmentID
}

func newSealFixture(t *testing.T, prior int, cutAt int) sealFixture {
	t.Helper()
	f := newFx(t)
	m := newSealModel(t, f.p.Root)
	f.src.Segments = recordingSegments{SegmentLog: f.store.Segments(), m: m}

	f.prompt(0, "Fix the intermittent 500s on the refresh endpoint.", true)
	f.tool("toolu_seal_0001", 1, "Read", "src/auth.ts", "export function refreshToken() {}", false)
	segID := core.SegmentID(1)
	f.closedSeg(segID, 0, 3)
	if prior > 0 {
		d := f.begin()
		f.advance(d, segID)
		_, err := f.w.PreCompact(f.ctx(), f.precompactInput())
		require.NoError(t, err, "fixture: the prior checkpoint seals with the real barriers")
		f.tool("toolu_seal_0002", 4, "Read", "src/session.ts", "export function session() {}", false)
		segID = 2
		f.closedSeg(segID, 4, 7)
	}
	require.NoError(t, f.store.Flush(f.ctx()))
	m.baselineSegments()
	d := f.begin()
	f.advance(d, segID)
	m.arm(f.draftPath(), d.Seq(), cutAt)
	checkpoint.SetBarriersForTest(f.w, m.barriers())
	return sealFixture{f: f, m: m, seq: d.Seq(), segID: segID}
}

// sealOutcome classifies seq on a reopened project: sealed (its MANIFEST line verifies), absent (no
// line), or broken (a line the reader refuses).
type sealOutcome string

const (
	outcomeSealed sealOutcome = "sealed"
	outcomeAbsent sealOutcome = "absent"
)

func classifySeal(t *testing.T, root string, seq core.CheckpointSeq) (sealOutcome, checkpoint.Checkpoint) {
	t.Helper()
	r, err := checkpoint.OpenReader(root, logging.Nop(), obs.New(core.SystemClock()))
	require.NoError(t, err)
	cp, _, err := r.Get(context.Background(), seq)
	switch {
	case err == nil:
		return outcomeSealed, cp
	case errors.Is(err, core.ErrNotFound):
		return outcomeAbsent, checkpoint.Checkpoint{}
	}
	require.FailNow(t, "the cut left a MANIFEST line the reader refuses", "seq %d: %v", seq, err)
	return "", checkpoint.Checkpoint{}
}

// requireMarked asserts every segment the sealed artifact carries is marked encoded into it in the
// reopened segment log: the DPI guard survived the cut.
func requireMarked(t *testing.T, re *fx, cp checkpoint.Checkpoint, seq core.CheckpointSeq) {
	t.Helper()
	require.NotEmpty(t, cp.EncodedSegments, "fixture: the sealed artifact carries its segment")
	for _, id := range cp.EncodedSegments {
		seg, err := re.store.Segments().Get(context.Background(), id)
		require.NoError(t, err)
		require.True(t, seg.EncodedOnce, "segment %d is sealed into checkpoint %d but its mark was lost", id, seq)
		require.Equal(t, seq, seg.CheckpointSeq, "segment %d's mark names the checkpoint that sealed it", id)
	}
}

// expectedSealSteps is the whole barrier sequence of one seal, in order.
func expectedSealSteps(seq core.CheckpointSeq, createsManifest bool) []string {
	steps := []string{
		"file:" + filepath.Base(paths.CheckpointPath(paths.Layout{}, seq)), stepMarks,
		stepCheckpointDir, stepManifestFile,
	}
	if createsManifest {
		steps = append(steps, stepCheckpointDir)
	}
	return steps
}

// TestFinalizeSealsThroughItsBarriersInOrder pins the seal's barrier sequence — count and order —
// for the first checkpoint of a project (which creates the manifest) and for a later one, and pins
// that nothing depending on the seal has happened by the time any barrier runs.
func TestFinalizeSealsThroughItsBarriersInOrder(t *testing.T) {
	for _, prior := range []int{0, 1} {
		t.Run(fmt.Sprintf("prior=%d", prior), func(t *testing.T) {
			sf := newSealFixture(t, prior, 0)
			ref, err := sf.f.w.PreCompact(sf.f.ctx(), sf.f.precompactInput())
			require.NoError(t, err)
			require.Equal(t, sf.seq, ref.Ref.Seq)
			require.Equal(t, expectedSealSteps(sf.seq, prior == 0), sf.m.steps,
				"artifact bytes, segment marks, artifact name, MANIFEST line, and the manifest's name when "+
					"the seal created it — each exactly once, in that order")
			require.Empty(t, sf.m.dependentsTouched,
				"the draft is retired and precompact.json written only after the last barrier")
		})
	}
}

// TestFinalizeCutAtEveryBarrierLeavesTheCheckpointAbsentOrSealed cuts the seal at every barrier and
// after it returns, as a process crash and as a power loss, and reopens the project each time.
func TestFinalizeCutAtEveryBarrierLeavesTheCheckpointAbsentOrSealed(t *testing.T) {
	for _, prior := range []int{0, 1} {
		n := len(expectedSealSteps(0, prior == 0))
		for cutAt := 0; cutAt <= n; cutAt++ {
			for _, powerLoss := range []bool{false, true} {
				name := fmt.Sprintf("prior=%d/cut=%d/process-crash", prior, cutAt)
				if powerLoss {
					name = fmt.Sprintf("prior=%d/cut=%d/power-loss", prior, cutAt)
				}
				t.Run(name, func(t *testing.T) { runSealCut(t, prior, cutAt, powerLoss) })
			}
		}
	}
}

func runSealCut(t *testing.T, prior, cutAt int, powerLoss bool) {
	sf := newSealFixture(t, prior, cutAt)
	f, m := sf.f, sf.m
	_, err := f.w.PreCompact(f.ctx(), f.precompactInput())
	if cutAt == 0 {
		require.NoError(t, err)
		m.snapshot()
	} else {
		require.ErrorIs(t, err, errPowerCut, "a cut seal is reported failed, never sealed")
		require.Equal(t, expectedSealSteps(sf.seq, prior == 0)[:cutAt], m.steps, "the cut stopped the seal at barrier %d", cutAt)
	}
	require.Empty(t, m.dependentsTouched, "nothing acts on the seal before its last barrier")

	require.NoError(t, f.store.Close())
	require.NoError(t, f.ledger.Close())
	m.rebuild(powerLoss)
	re := reopenFx(t, f)

	outcome, cp := classifySeal(t, f.p.Root, sf.seq)
	if cutAt == 0 {
		require.Equal(t, outcomeSealed, outcome, "PreCompact returned nil, so the checkpoint must survive the cut")
	}
	if outcome == outcomeSealed {
		requireMarked(t, re, cp, sf.seq)
		return
	}

	// Absent: the session must still be sealable. The draft is on disk at the same sequence, and a
	// restarted writer resumes it and seals it.
	raw, err := os.ReadFile(paths.Long(f.draftPath()))
	require.NoError(t, err, "an absent checkpoint leaves its draft on disk")
	var dw draftWire
	require.NoError(t, json.Unmarshal(raw, &dw))
	require.Equal(t, sf.seq, dw.Seq, "the draft still holds the sequence the cut seal was for")
	require.Contains(t, dw.Encoded, sf.segID)

	_, err = re.w.Begin(re.ctx(), re.sess, 0, re.src)
	require.NoError(t, err)
	got, err := re.w.PreCompact(re.ctx(), re.precompactInput())
	require.NoError(t, err, "the resumed draft seals")
	outcome, cp = classifySeal(t, f.p.Root, got.Ref.Seq)
	require.Equal(t, outcomeSealed, outcome)
	require.Contains(t, cp.EncodedSegments, sf.segID, "the resumed seal carries the segment the cut seal lost")
	seg, err := re.store.Segments().Get(re.ctx(), sf.segID)
	require.NoError(t, err)
	require.True(t, seg.EncodedOnce, "the resumed seal leaves the segment marked, so no later draft re-encodes it")
}
