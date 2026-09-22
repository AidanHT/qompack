package daemon

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Segmented delivery journal (SP20-D4 capacity rollover). The four legacy journal/seal files are the
// ORIGINAL segment (sequence 0) and are never renamed, rebased or truncated. When the active segment
// fills, a NEW segment is created under state/delivery-segments/<seq>/ with the same four filenames and
// a fresh empty window; the generation store (delivery_generation.go) keeps the full nonce/arrival/ACK
// history so an archived nonce still resolves and a session's arrivals stay dense across the boundary.
//
// # Authority is an immutable chained transition log + an atomic head (NOT a self-hash)
//
// Per main's review of the first proposal, a single mutable pointer with a self-integrity hash is NOT a
// commit/continuity proof. The authority is therefore modelled exactly like the generation manifest:
//
//   - delivery-journal-log.jsonl — an append-only, chained, versioned log. Record `seq` is an IMMUTABLE
//     transition: {seq, active, base_root}, chained from its predecessor. The durable, fsynced append of
//     a record IS the commit of that transition.
//   - delivery-journal.json — the atomic head (the file main's backup watches). It names the last
//     committed transition; it is a fast-start checkpoint, never the sole authority. A head behind a
//     complete chained tail is repaired FORWARD to the tail (head loss, never a silent revert); a head
//     ahead of the log, off a record boundary, or disagreeing with the record it names, is refused.
//
// # Absence is legacy ONLY on a genuinely unmigrated tree
//
// Losing the authority after new writes must NOT reopen the frozen legacy segment and re-mint arrivals.
// So an active-0 transition is committed BEFORE any generation store or segment is staged. On open:
//
//   - head + valid log present  → recover the active segment and its base root.
//   - no head AND no log        → no authority. The journal open decides: a genuinely unmigrated tree
//     (no delivery-generations, no delivery-segments) initialises active-0; a tree that HAS such
//     migration evidence but no authority is head loss → refused (never silently legacy).
//   - anything inconsistent (log without head, head without log, torn/parity/chain/boundary failure,
//     head ahead of log) → errSegmentUnavailable, never permission to recreate history.
//
// # Confinement
//
// The head, log and staged segments are reached through an os.Root pinned to the state directory, so a
// swapped symlink on a parent cannot redirect a read or write. Reads fstat the opened handle
// (SameFile) and bound a regular file; a FIFO/dir/irregular file or an oversize file is refused.

const (
	deliverySegmentsDir      = "delivery-segments"
	deliverySegmentHeadFile  = "delivery-journal.json"      // atomic active-segment head (backup-watched)
	deliverySegmentLogFile   = "delivery-journal-log.jsonl" // append-only immutable transition log
	deliverySegmentFormat    = "qompack.delivery.segments.v1"
	deliverySegmentVersion   = 1
	deliverySegmentChainSalt = "qompack.delivery.segments.chain.v1"
	// deliverySegmentSeqWidth zero-pads a segment directory name so the uint64 range sorts
	// lexicographically the same as numerically. //nomagic:allow uint64 decimal digit width
	deliverySegmentSeqWidth = 20
	// deliverySegmentMaxLine bounds one transition record. //nomagic:allow small JSON record byte bound
	deliverySegmentMaxLine = 4096 //nomagic:allow fixed authority wire-record bound, not a configurable context budget
	// deliverySegmentMaxLog bounds the authority log a store will open (one tiny record per rotation).
	// //nomagic:allow authority-log byte ceiling
	deliverySegmentMaxLog = 1 << 20
	deliverySegmentTemp   = ".tmp-seg-"
)

// errSegmentUnavailable marks an authority that cannot be read as itself, whose referenced segment or
// base root is missing, or migration evidence surviving without an authority. Never a fresh identity,
// never permission to recreate history.
var errSegmentUnavailable = errors.New("delivery segments: authority unavailable")

var segChainSeed = radixDigest(deliverySegmentChainSalt, nil)

// segTransition is one immutable authority-log record. Seq is dense from 0; seq 0 is the initial
// active-0 (legacy) transition. Active is the active segment after this transition. BaseRoot is the
// generation root preceding the active segment (empty only for the active-0 record).
type segTransition struct {
	Version  int    `json:"v"`
	Seq      int64  `json:"seq"`
	Active   uint64 `json:"active"`
	BaseRoot string `json:"base_root"`
	Chain    string `json:"chain"`
}

// segHead is the atomic checkpoint naming the last committed transition; LastLen lets recovery verify
// the head sits on a record boundary in O(1).
type segHead struct {
	Version  int    `json:"v"`
	Format   string `json:"format"`
	Seq      int64  `json:"seq"`
	Active   uint64 `json:"active"`
	BaseRoot string `json:"base_root"`
	Chain    string `json:"chain"`
	LogBytes int64  `json:"log_bytes"`
	LastLen  int64  `json:"last_len"`
}

// segChain folds a transition's identity into its predecessor's chain — a REAL chain the reader
// validates record by record, not a bare self-report.
func segChain(prev radixHash, seq int64, active uint64, baseRoot string) radixHash {
	body := fmt.Sprintf("%d\x00%d\x00%s", seq, active, baseRoot)
	buf := append(append([]byte(nil), prev[:]...), body...)
	return radixDigest(deliverySegmentChainSalt, buf)
}

// deliverySegments is the runtime authority handle, held for the journal's life. It owns the confined
// head/log and appends a transition on each rotation. Callers serialise it under the journal's rotation
// barrier; it takes no lock of its own.
type deliverySegments struct {
	stateDir string
	confine  *os.Root
	logPath  string // absolute, for diagnostics/tests
	headPath string

	log      *os.File
	seq      int64 // -1 until an authority exists
	active   uint64
	baseRoot string
	chain    radixHash
	logBytes int64
}

// openDeliverySegments opens (confined) the authority under stateDir and recovers it. authorityExists is
// false with a nil error ONLY when neither head nor log is present (the caller then decides fresh-init
// vs refuse based on migration evidence). Every inconsistency is errSegmentUnavailable.
func openDeliverySegments(stateDir string) (handle *deliverySegments, authorityExists bool, err error) {
	confine, err := pinDeliveryDirectory(stateDir)
	if err != nil {
		return nil, false, errSegmentUnavailable
	}
	s := &deliverySegments{
		stateDir: stateDir,
		confine:  confine,
		logPath:  filepath.Join(stateDir, deliverySegmentLogFile),
		headPath: filepath.Join(stateDir, deliverySegmentHeadFile),
		seq:      -1,
		chain:    segChainSeed,
	}
	exists, err := s.recover()
	if err != nil {
		_ = confine.Close()
		return nil, false, err
	}
	want := int64(0)
	if exists {
		want = s.logBytes
	}
	f, err := openDeliveryAppend(s.confine, deliverySegmentLogFile, want)
	if err != nil {
		_ = confine.Close()
		return nil, false, errSegmentUnavailable
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != want {
		_ = f.Close()
		_ = confine.Close()
		return nil, false, errSegmentUnavailable
	}
	s.log = f
	return s, exists, nil
}

func (s *deliverySegments) close() error {
	var err error
	if s.log != nil {
		err = s.log.Close()
		s.log = nil
	}
	if s.confine != nil {
		err = errors.Join(err, s.confine.Close())
		s.confine = nil
	}
	return err
}

func (s *deliverySegments) activeSeg() uint64   { return s.active }
func (s *deliverySegments) baseRootHex() string { return s.baseRoot }

// recover reads the head and validates the tail beyond it, in bounded time (root-anchored via LastLen).
func (s *deliverySegments) recover() (bool, error) {
	head, hasHead, err := readSegHead(s.confine)
	if err != nil {
		return false, err
	}
	linfo, lerr := s.confine.Lstat(deliverySegmentLogFile)
	hasLog := lerr == nil
	if lerr != nil && !os.IsNotExist(lerr) {
		return false, errSegmentUnavailable
	}
	if hasLog && (!linfo.Mode().IsRegular() || linfo.Size() > deliverySegmentMaxLog) {
		return false, errSegmentUnavailable
	}
	if !hasHead {
		if hasLog && linfo.Size() > 0 {
			return false, errSegmentUnavailable // a log with no head is head loss; preserve, refuse
		}
		return false, nil // no authority at all
	}
	if !hasLog {
		return false, errSegmentUnavailable
	}
	size := linfo.Size()
	if size < head.LogBytes {
		return false, errSegmentUnavailable // head names bytes the log does not hold
	}
	lf, err := s.confine.Open(deliverySegmentLogFile)
	if err != nil {
		return false, errSegmentUnavailable
	}
	defer func() { _ = lf.Close() }()
	opened, err := lf.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(linfo, opened) || opened.Size() != size {
		return false, errSegmentUnavailable
	}
	if err := verifySegHeadBoundary(lf, head); err != nil {
		return false, err
	}
	s.seq, s.active, s.baseRoot, s.chain, s.logBytes = head.Seq, head.Active, head.BaseRoot, mustRadixHash(head.Chain), head.LogBytes
	if size == head.LogBytes {
		return true, nil
	}
	adopted, err := scanSegTail(lf, head, size)
	if err != nil {
		return false, err
	}
	s.seq, s.active, s.baseRoot, s.chain, s.logBytes = adopted.seq, adopted.active, adopted.baseRoot, adopted.chain, adopted.end
	if err := s.writeHead(adopted.lastLen); err != nil {
		return false, err
	}
	return true, nil
}

// initActiveZero commits the initial active-0 transition. It MUST run before any generation store or
// segment is staged, so that later evidence without an authority is detectable as head loss. It refuses
// if an authority already exists.
func (s *deliverySegments) initActiveZero() error {
	if s.seq >= 0 {
		return errSegmentUnavailable
	}
	return s.appendTransition(0, "")
}

// commitTransition appends the next transition (the rotation commit) naming the new active segment and
// the generation root that preceded it, then checkpoints the head. baseRoot must be a valid 32-byte hex.
func (s *deliverySegments) commitTransition(active uint64, baseRoot string) error {
	if s.seq < 0 {
		return errSegmentUnavailable // active-0 must exist first
	}
	if s.seq == math.MaxInt64 || active != uint64(s.seq+1) || s.active != uint64(s.seq) {
		return errSegmentUnavailable // sequence and active segment advance together
	}
	if _, ok := hexToRadixHash(baseRoot); !ok {
		return errSegmentUnavailable
	}
	return s.appendTransition(active, baseRoot)
}

// appendTransition writes the record durably (append + fsync = the commit), then WriteAtomic-s the head.
func (s *deliverySegments) appendTransition(active uint64, baseRoot string) error {
	seq := s.seq + 1
	chain := segChain(s.chain, seq, active, baseRoot)
	rec := segTransition{Version: deliverySegmentVersion, Seq: seq, Active: active, BaseRoot: baseRoot, Chain: hex.EncodeToString(chain[:])}
	line, err := json.Marshal(rec)
	if err != nil {
		return errSegmentUnavailable
	}
	line = append(line, '\n')
	if len(line) > deliverySegmentMaxLine || s.logBytes > deliverySegmentMaxLog-int64(len(line)) {
		return errSegmentUnavailable
	}
	n, err := s.log.Write(line)
	if err != nil || n != len(line) {
		return errSegmentUnavailable
	}
	if err := s.log.Sync(); err != nil {
		return errSegmentUnavailable
	}
	prev := *s
	s.seq, s.active, s.baseRoot, s.chain, s.logBytes = seq, active, baseRoot, chain, s.logBytes+int64(len(line))
	if err := s.writeHead(int64(len(line))); err != nil {
		s.seq, s.active, s.baseRoot, s.chain, s.logBytes = prev.seq, prev.active, prev.baseRoot, prev.chain, prev.logBytes
		return err
	}
	return paths.SyncDir(s.stateDir)
}

func (s *deliverySegments) writeHead(lastLen int64) error {
	head := segHead{
		Version: deliverySegmentVersion, Format: deliverySegmentFormat, Seq: s.seq, Active: s.active,
		BaseRoot: s.baseRoot, Chain: hex.EncodeToString(s.chain[:]), LogBytes: s.logBytes, LastLen: lastLen,
	}
	b, err := json.Marshal(head)
	if err != nil {
		return errSegmentUnavailable
	}
	return s.writeConfinedAtomic(deliverySegmentHeadFile, b)
}

// writeConfinedAtomic stages, fsyncs, and atomically renames a small file through the pinned root, then
// fsyncs the directory — the symlink-safe durable pattern the radix and generation store use.
func (s *deliverySegments) writeConfinedAtomic(name string, data []byte) error {
	tmp := deliverySegmentTemp + rand.Text()
	f, err := s.confine.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errSegmentUnavailable
	}
	if n, err := f.Write(data); err != nil || n != len(data) {
		_ = f.Close()
		_ = s.confine.Remove(tmp)
		return errSegmentUnavailable
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = s.confine.Remove(tmp)
		return errSegmentUnavailable
	}
	if err := f.Close(); err != nil {
		_ = s.confine.Remove(tmp)
		return errSegmentUnavailable
	}
	if err := s.confine.Rename(tmp, name); err != nil {
		_ = s.confine.Remove(tmp)
		return errSegmentUnavailable
	}
	return paths.SyncDir(s.stateDir)
}

// ── head/log IO (confined, bounded, SameFile-guarded) ──────────────────────────────────────────────

func readSegHead(confine *os.Root) (segHead, bool, error) {
	info, err := confine.Lstat(deliverySegmentHeadFile)
	if os.IsNotExist(err) {
		return segHead{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > deliverySegmentMaxLine {
		return segHead{}, false, errSegmentUnavailable
	}
	f, err := confine.Open(deliverySegmentHeadFile)
	if err != nil {
		return segHead{}, false, errSegmentUnavailable
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return segHead{}, false, errSegmentUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(f, deliverySegmentMaxLine+1))
	if err != nil || len(raw) > deliverySegmentMaxLine {
		return segHead{}, false, errSegmentUnavailable
	}
	var head segHead
	if json.Unmarshal(raw, &head) != nil || head.Version != deliverySegmentVersion || head.Format != deliverySegmentFormat ||
		head.Seq < 0 || head.Active != uint64(head.Seq) || head.LogBytes <= 0 || head.LastLen < 1 || head.LastLen > head.LogBytes || head.LastLen > deliverySegmentMaxLine {
		return segHead{}, false, errSegmentUnavailable
	}
	canon, err := json.Marshal(head)
	if err != nil || !bytes.Equal(canon, raw) {
		return segHead{}, false, errSegmentUnavailable // reject unknown/reordered keys
	}
	if _, ok := hexToRadixHash(head.Chain); !ok {
		return segHead{}, false, errSegmentUnavailable
	}
	if !validSegBaseRoot(head.Active, head.BaseRoot) {
		return segHead{}, false, errSegmentUnavailable
	}
	return head, true, nil
}

// validSegBaseRoot ties the base root to the active segment: active 0 (legacy) carries an empty base
// root; every real segment carries a 32-byte hex root.
func validSegBaseRoot(active uint64, baseRoot string) bool {
	if active == 0 {
		return baseRoot == ""
	}
	_, ok := hexToRadixHash(baseRoot)
	return ok
}

func verifySegHeadBoundary(lf *os.File, head segHead) error {
	start := head.LogBytes - head.LastLen
	if start < 0 {
		return errSegmentUnavailable
	}
	if start > 0 {
		var b [1]byte
		if _, err := lf.ReadAt(b[:], start-1); err != nil || b[0] != '\n' {
			return errSegmentUnavailable
		}
	}
	buf := make([]byte, head.LastLen)
	if _, err := lf.ReadAt(buf, start); err != nil {
		return errSegmentUnavailable
	}
	rec, chain, ok := decodeSegTransition(buf)
	if !ok || rec.Seq != head.Seq || rec.Active != head.Active || rec.BaseRoot != head.BaseRoot ||
		hex.EncodeToString(chain[:]) != head.Chain {
		return errSegmentUnavailable
	}
	return nil
}

type segTailState struct {
	seq      int64
	active   uint64
	baseRoot string
	chain    radixHash
	end      int64
	lastLen  int64
}

// scanSegTail validates (head.LogBytes, size) as chain extensions of the head, adopting complete records
// and refusing (preserving) a torn trailing record. Each transition's active must advance.
func scanSegTail(lf *os.File, head segHead, size int64) (segTailState, error) {
	if _, err := lf.Seek(head.LogBytes, io.SeekStart); err != nil {
		return segTailState{}, errSegmentUnavailable
	}
	rd := newSegLineReader(lf)
	last := segTailState{
		seq: head.Seq, active: head.Active, baseRoot: head.BaseRoot, chain: mustRadixHash(head.Chain),
		end: head.LogBytes, lastLen: head.LastLen,
	}
	for last.end < size {
		line, complete, rerr := rd.next()
		if rerr != nil {
			return segTailState{}, errSegmentUnavailable
		}
		if len(line) == 0 && !complete {
			break
		}
		if !complete {
			return segTailState{}, errSegmentUnavailable // torn trailing partial: preserve, refuse
		}
		rec, chain, ok := decodeSegTransition(line)
		if !ok || last.seq == math.MaxInt64 || rec.Seq != last.seq+1 || rec.Active != uint64(rec.Seq) ||
			!validSegBaseRoot(rec.Active, rec.BaseRoot) || chain != segChain(last.chain, rec.Seq, rec.Active, rec.BaseRoot) {
			return segTailState{}, errSegmentUnavailable
		}
		last = segTailState{seq: rec.Seq, active: rec.Active, baseRoot: rec.BaseRoot, chain: chain, end: last.end + int64(len(line)), lastLen: int64(len(line))}
	}
	if last.end != size {
		return segTailState{}, errSegmentUnavailable
	}
	return last, nil
}

func decodeSegTransition(line []byte) (segTransition, radixHash, bool) {
	var rec segTransition
	if json.Unmarshal(line, &rec) != nil || rec.Version != deliverySegmentVersion {
		return segTransition{}, radixHash{}, false
	}
	canon, err := json.Marshal(rec)
	if err != nil || len(canon)+1 != len(line) || !bytes.Equal(canon, line[:len(canon)]) || line[len(line)-1] != '\n' {
		return segTransition{}, radixHash{}, false
	}
	chain, ok := hexToRadixHash(rec.Chain)
	if !ok {
		return segTransition{}, radixHash{}, false
	}
	return rec, chain, true
}

// segLineReader reads newline-delimited records with a per-line bound, reporting complete vs torn tail.
type segLineReader struct {
	f   *os.File
	buf []byte
	pos int
	n   int
}

func newSegLineReader(f *os.File) *segLineReader {
	return &segLineReader{f: f, buf: make([]byte, 64<<10)}
}

func (r *segLineReader) next() (line []byte, complete bool, err error) {
	var acc []byte
	for {
		for r.pos < r.n {
			c := r.buf[r.pos]
			r.pos++
			acc = append(acc, c)
			if len(acc) > deliverySegmentMaxLine {
				return nil, false, errSegmentUnavailable
			}
			if c == '\n' {
				return acc, true, nil
			}
		}
		m, rerr := r.f.Read(r.buf)
		r.n, r.pos = m, 0
		if m == 0 {
			if rerr == io.EOF || rerr == nil {
				return acc, len(acc) == 0, nil
			}
			return nil, false, rerr
		}
	}
}

// ── segment paths and staging (confined) ───────────────────────────────────────────────────────────

// segmentDir is the directory holding a segment's four journal/seal files. Sequence 0 is the ORIGINAL
// legacy segment: its files live directly under state/, unmoved.
func segmentDir(stateDir string, seq uint64) string {
	if seq == 0 {
		return stateDir
	}
	return filepath.Join(stateDir, deliverySegmentsDir, fmt.Sprintf("%0*d", deliverySegmentSeqWidth, seq))
}

// segmentLeasePath is a segment's lease-journal file; the ack journal and both position seals derive
// from filepath.Dir of it exactly as the journal already derives them.
func segmentLeasePath(stateDir string, seq uint64) string {
	return filepath.Join(segmentDir(stateDir, seq), deliveryLeaseFile)
}

// segmentIsFreshEmpty reports whether a staged segment directory is EXACTLY a fresh, empty, valid
// segment. A crashed attempt that produced anything else is a conflict — preserved and refused, never
// overwritten. A dir that does not exist yet is (false, nil): create, do not adopt.
func segmentIsFreshEmpty(stateDir string, seq uint64) (bool, error) {
	if seq == 0 {
		return false, errSegmentUnavailable
	}
	state, err := pinDeliveryDirectory(stateDir)
	if err != nil {
		return false, errSegmentUnavailable
	}
	defer func() { _ = state.Close() }()
	_, err = state.Lstat(deliverySegmentsDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	segments, err := pinDeliveryChild(state, deliverySegmentsDir, false)
	if err != nil {
		return false, errSegmentUnavailable
	}
	defer func() { _ = segments.Close() }()
	name := fmt.Sprintf("%0*d", deliverySegmentSeqWidth, seq)
	if _, err := segments.Lstat(name); os.IsNotExist(err) {
		return false, nil
	}
	segment, err := pinDeliveryChild(segments, name, false)
	if err != nil {
		return false, errSegmentUnavailable
	}
	defer func() { _ = segment.Close() }()
	directory, err := segment.Open(".")
	if err != nil {
		return false, errSegmentUnavailable
	}
	// Four files are the whole staged format. A fifth name is conflicting evidence.
	names, readErr := directory.ReadDir(5)
	closeErr := directory.Close()
	if (readErr != nil && readErr != io.EOF) || closeErr != nil || len(names) != 4 {
		return false, errSegmentUnavailable
	}
	for _, jp := range []struct {
		journal string
		seal    string
		seed    core.Hash
	}{
		{deliveryLeaseFile, deliveryPositionFile, deliveryChainSeed},
		{deliveryAckFile, deliveryAckPositionFile, deliveryAckChainSeed},
	} {
		seal, err := encodeDeliveryPositionV1(0, 0, jp.seed)
		if err != nil || !freshSegmentFileMatches(segment, jp.journal, nil) ||
			!freshSegmentFileMatches(segment, jp.seal, seal) {
			return false, errSegmentUnavailable
		}
	}
	if !samePinnedDirectory(state, stateDir) ||
		!samePinnedDirectory(segments, filepath.Join(stateDir, deliverySegmentsDir)) ||
		!samePinnedDirectory(segment, segmentDir(stateDir, seq)) {
		return false, errSegmentUnavailable
	}
	return true, nil
}

func freshSegmentFileMatches(root *os.Root, name string, expected []byte) bool {
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() != int64(len(expected)) {
		return false
	}
	f, err := root.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) || opened.Size() != before.Size() {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(len(expected))+1))
	return err == nil && bytes.Equal(raw, expected)
}

// createFreshSegment stages a new, empty segment (dir + four empty journal/seal files, fsynced),
// idempotent against an identical fresh segment and refusing a dir that already exists with any other
// content — a staged conflicting attempt is preserved, never overwritten.
func createFreshSegment(stateDir string, seq uint64) error {
	if seq == 0 {
		return errSegmentUnavailable
	}
	state, err := pinDeliveryDirectory(stateDir)
	if err != nil {
		return errSegmentUnavailable
	}
	defer func() { _ = state.Close() }()
	segments, err := pinDeliveryChild(state, deliverySegmentsDir, true)
	if err != nil {
		return errSegmentUnavailable
	}
	defer func() { _ = segments.Close() }()
	fresh, err := segmentIsFreshEmpty(stateDir, seq)
	if err != nil {
		return err
	}
	if fresh {
		if !samePinnedDirectory(state, stateDir) ||
			!samePinnedDirectory(segments, filepath.Join(stateDir, deliverySegmentsDir)) {
			return errSegmentUnavailable
		}
		return nil
	}
	name := fmt.Sprintf("%0*d", deliverySegmentSeqWidth, seq)
	if _, err := segments.Lstat(name); !os.IsNotExist(err) {
		return errSegmentUnavailable
	}
	dir := segmentDir(stateDir, seq)
	segment, err := pinDeliveryChild(segments, name, true)
	if err != nil {
		return errSegmentUnavailable
	}
	defer func() { _ = segment.Close() }()
	for _, f := range []struct {
		journal string
		seal    string
		seed    core.Hash
	}{
		{filepath.Join(dir, deliveryLeaseFile), filepath.Join(dir, deliveryPositionFile), deliveryChainSeed},
		{filepath.Join(dir, deliveryAckFile), filepath.Join(dir, deliveryAckPositionFile), deliveryAckChainSeed},
	} {
		if err := createSegmentFile(segment, filepath.Base(f.journal), nil); err != nil {
			return errSegmentUnavailable
		}
		data, err := encodeDeliveryPositionV1(0, 0, f.seed)
		if err != nil {
			return errSegmentUnavailable
		}
		if err := createSegmentFile(segment, filepath.Base(f.seal), data); err != nil {
			return errSegmentUnavailable
		}
	}
	if !samePinnedDirectory(state, stateDir) ||
		!samePinnedDirectory(segments, filepath.Join(stateDir, deliverySegmentsDir)) ||
		!samePinnedDirectory(segment, dir) {
		return errSegmentUnavailable
	}
	if err := paths.SyncDir(dir); err != nil {
		return errSegmentUnavailable
	}
	return paths.SyncDir(filepath.Join(stateDir, deliverySegmentsDir))
}
