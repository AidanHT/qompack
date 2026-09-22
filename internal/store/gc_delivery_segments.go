package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Read-only decoder for the daemon's segmented delivery authority (SP20-D4; the shared retention
// contract in delivery-segment-retention-decision.md). GC must harvest the open-lease roots of EVERY
// committed segment, not just the legacy files, once a store has rotated — and it must never revert to
// the legacy segment when a rotation's authority is present but unreadable.
//
// This is a STRUCTURAL, byte-exact mirror of internal/daemon/delivery_segment.go, duplicated here
// because internal/daemon imports internal/store, so store cannot import it back (00-ARCHITECTURE.md
// §3.2). The producer wire is UNSHIPPED; nothing here mutates it or the shared public wire. The
// duplication owes an explicit cross-package agreement test (see gc-segment-integration-work.md): if
// the daemon's constants, field order, chain salt or record shape change, this decoder must change with
// them or it will (safely) refuse to read a valid authority.
//
// Authority (mirrored exactly):
//   - delivery-journal.json  — the atomic head naming the last committed transition.
//   - delivery-journal-log.jsonl — an append-only, sha256-chained, versioned transition log; record
//     seq is {v, seq, active, base_root, chain}, dense from 0 (seq 0 = the legacy active-0 transition).
//
// The reader validates the WHOLE log as a chain from the seed, requires the head to name an exact
// committed record on a byte boundary, and treats a complete tail beyond the head as newer authority
// WITHOUT writing anything. Any torn/missing/conflicting/unknown authority, or migration evidence
// surviving without an authority, is errRetentionRootsUnavailable — never a silent revert to legacy.

const (
	dsegDir         = "delivery-segments"
	dsegHeadFile    = "delivery-journal.json"
	dsegLogFile     = "delivery-journal-log.jsonl"
	dsegGensDir     = "delivery-generations" // migration evidence, alongside dsegDir
	dsegFormat      = "qompack.delivery.segments.v1"
	dsegChainSalt   = "qompack.delivery.segments.chain.v1"
	dsegVersion     = 1
	dsegSeqWidth    = 20
	dsegMaxLine     = 4096 //nomagic:allow mirrors the daemon authority wire-record bound
	dsegMaxLog      = 1 << 20
	dsegMaxSegments = 1 << 16 // explicit ceiling on committed transitions/segments; halts past it
)

var dsegChainSeed = dsegDigest(dsegChainSalt, nil)

// dsegDigest mirrors the daemon's radixDigest: sha256(domain ++ 0x00 ++ b).
func dsegDigest(domain string, b []byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(b)
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// dsegChain mirrors the daemon's segChain: it folds a transition into its predecessor's chain.
func dsegChain(prev [32]byte, seq int64, active uint64, baseRoot string) [32]byte {
	body := fmt.Sprintf("%d\x00%d\x00%s", seq, active, baseRoot)
	buf := append(append([]byte(nil), prev[:]...), body...)
	return dsegDigest(dsegChainSalt, buf)
}

func dsegHexHash(s string) ([32]byte, bool) {
	var h [32]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(h) {
		return [32]byte{}, false
	}
	copy(h[:], b)
	return h, true
}

// dsegValidBaseRoot ties the base root to the active segment exactly as the producer does: the legacy
// active-0 transition carries an empty base root; every real segment carries a 32-byte hex root.
func dsegValidBaseRoot(active uint64, baseRoot string) bool {
	if active == 0 {
		return baseRoot == ""
	}
	_, ok := dsegHexHash(baseRoot)
	return ok
}

// dsegTransition and dsegHead mirror the producer's records field-for-field and in order, so a
// json.Marshal round-trip reproduces the on-disk bytes and rejects unknown/reordered keys.
type dsegTransition struct {
	Version  int    `json:"v"`
	Seq      int64  `json:"seq"`
	Active   uint64 `json:"active"`
	BaseRoot string `json:"base_root"`
	Chain    string `json:"chain"`
}

type dsegHead struct {
	Version  int    `json:"v"`
	Format   string `json:"format"`
	Seq      int64  `json:"seq"`
	Active   uint64 `json:"active"`
	BaseRoot string `json:"base_root"`
	Chain    string `json:"chain"`
	LogBytes int64  `json:"log_bytes"`
	LastLen  int64  `json:"last_len"`
}

// dsegView is the committed and staged segment set GC must harvest. legacy is true only for a
// genuinely unmigrated tree (no authority and no migration evidence): the caller then harvests just the
// legacy segment 0, exactly as before segments existed.
//
// The head/log identity fields are the STABLE-FRONTIER witness: they record exactly what the authority
// (or its documented absence) looked like at resolve time, so dsegRecheckFrontier can prove, AFTER all
// retention has been harvested, that no rotation committed a switch underneath the pass. A committed
// switch is a durable log APPEND that precedes the head checkpoint, so both the head AND the whole log
// are witnessed, not just the head.
type dsegView struct {
	legacy    bool
	committed []uint64 // sorted, always includes 0 when not legacy
	staged    []uint64 // sorted; uncommitted dirs under delivery-segments, conservatively retained

	headPresent bool
	headBytes   []byte
	logPresent  bool
	logBytes    []byte
}

// dsegSegmentDir mirrors the producer: segment 0 lives directly under state/, others under
// state/delivery-segments/<20-digit seq>/.
func (s *FSStore) dsegSegmentDir(seq uint64) string {
	if seq == 0 {
		return s.l.State
	}
	return filepath.Join(s.l.State, dsegDir, fmt.Sprintf("%0*d", dsegSeqWidth, seq))
}

// resolveDeliverySegments decodes the authority read-only and returns the segments GC must harvest, or
// errRetentionRootsUnavailable when the authority is present but cannot be read as itself. It records
// the head before AND after enumeration (a stable-frontier check) so a rotation racing this read cannot
// hide a segment.
func (s *FSStore) resolveDeliverySegments(budget *gcBudget) (dsegView, error) {
	headPath := filepath.Join(s.l.State, dsegHeadFile)
	logPath := filepath.Join(s.l.State, dsegLogFile)

	head, hasHead, rawHead, err := s.dsegReadHead(headPath)
	if err != nil {
		return dsegView{}, err
	}
	logRaw, hasLog, err := s.dsegReadFileBounded(logPath, dsegMaxLog)
	if err != nil {
		return dsegView{}, err
	}

	frontier := dsegView{headPresent: hasHead, headBytes: rawHead, logPresent: hasLog, logBytes: logRaw}

	if !hasHead && !hasLog {
		evidence, err := s.dsegMigrationEvidence()
		if err != nil {
			return dsegView{}, err
		}
		if evidence {
			return dsegView{}, retentionUnavailable("delivery segment authority missing but migration evidence present")
		}
		frontier.legacy, frontier.committed = true, []uint64{0}
		return frontier, nil
	}
	if !hasHead {
		if len(logRaw) == 0 {
			// An empty log with no head is not authority; fall back only if nothing was migrated.
			evidence, err := s.dsegMigrationEvidence()
			if err != nil {
				return dsegView{}, err
			}
			if evidence {
				return dsegView{}, retentionUnavailable("delivery segment log empty, no head, migration evidence present")
			}
			frontier.legacy, frontier.committed = true, []uint64{0}
			return frontier, nil
		}
		return dsegView{}, retentionUnavailable("delivery segment log present without a head (head loss)")
	}
	if !hasLog {
		return dsegView{}, retentionUnavailable("delivery segment head present without a log")
	}

	trans, ends, err := dsegValidateLog(logRaw)
	if err != nil {
		return dsegView{}, err
	}
	// The head must name an exact committed record on a byte boundary; a head ahead of the log, or one
	// that disagrees with the record it names, is a conflict. A complete tail beyond the head is newer
	// authority and is adopted here without writing anything.
	if head.Seq < 0 || head.Seq > int64(len(trans)-1) {
		return dsegView{}, retentionUnavailable("delivery segment head seq %d is ahead of the log", head.Seq)
	}
	ht := trans[head.Seq]
	if ht.Active != head.Active || ht.BaseRoot != head.BaseRoot || ht.Chain != head.Chain ||
		ends[head.Seq] != head.LogBytes || dsegRecordLen(ends, head.Seq) != head.LastLen {
		return dsegView{}, retentionUnavailable("delivery segment head conflicts with the record it names")
	}

	committed := dsegActiveSet(trans)
	if len(committed) > dsegMaxSegments {
		return dsegView{}, retentionUnavailable("delivery segments exceed the %d ceiling", dsegMaxSegments)
	}
	// Every committed segment must have its FOUR journal/seal files present as confined, structurally
	// complete files — a lease journal and an ack journal (both harvested) plus both position seals
	// (validated, not harvested). Two files alone do not prove a committed segment; a missing or
	// malformed seal stops the sweep. A committed segment's files are REQUIRED, not optional.
	for _, seq := range committed {
		if err := s.dsegRequireSegmentFiles(seq); err != nil {
			return dsegView{}, err
		}
	}

	staged, err := s.dsegStagedSegments(budget, committed)
	if err != nil {
		return dsegView{}, err
	}

	frontier.committed, frontier.staged = committed, staged
	return frontier, nil
}

// dsegRequireSegmentFiles proves a committed segment has all four journal/seal files as confined,
// structurally complete files: the two journals need only be regular (their content is harvested and
// validated line by line), while the two position seals are decoded and their supported STRUCTURE is
// verified — the lease seal against the lease journal's chain identity, the ack seal against the ack's.
func (s *FSStore) dsegRequireSegmentFiles(seq uint64) error {
	dir := s.dsegSegmentDir(seq)
	if err := s.dsegRequireRegular(filepath.Join(dir, deliveryLeaseFile)); err != nil {
		return err
	}
	if err := s.dsegRequireRegular(filepath.Join(dir, deliveryAckFile)); err != nil {
		return err
	}
	if err := s.dsegRequireSeal(filepath.Join(dir, deliveryLeasePositionFile), dsealLeaseChainDomain, dsealLeaseSeed); err != nil {
		return err
	}
	return s.dsegRequireSeal(filepath.Join(dir, deliveryAckPositionFile), dsealAckChainDomain, dsealAckSeed)
}

// dsegRequireSeal reads a position seal through the confined, SameFile-guarded handle and validates it
// against the SUPPORTED seal structure — a v1 sidecar or a v2 fixed-size A/B image — including its own
// integrity fields (a v2 record's per-slot sum, a v1 record's canonical bounded fields). It fails
// closed for garbage, an unknown schema, or a torn/malformed seal.
//
// EXACT LIMIT. This validates the seal FILE's own supported structure and integrity; it deliberately
// does NOT re-scan the segment's journal, its chain, or the generation store to confirm the sealed
// position matches the journal bytes — that whole-journal fsck lives in the daemon's offline reader,
// which store cannot import (§3.2). This is a small read-only DUPLICATE of the producer's seal format;
// an explicit cross-package agreement test (main's delivery_readers_v6_test) is owed to keep them in
// step. If the producer's seal layout, sum domain, chain domains or bounds drift, this refuses a valid
// seal rather than mis-accepting one.
func (s *FSStore) dsegRequireSeal(path, chainDomain string, seed core.Hash) error {
	raw, present, err := s.dsegReadFileBounded(path, dsealFileSize)
	if err != nil {
		return err
	}
	if !present {
		return retentionUnavailable("required segment seal %s is missing", path)
	}
	if len(raw) == 0 {
		return retentionUnavailable("required segment seal %s is empty", path)
	}
	if !dsegValidateSeal(raw, chainDomain, seed) {
		return retentionUnavailable("required segment seal %s is not a supported v1/v2 seal", path)
	}
	return nil
}

// dsegValidateSeal reports whether raw is a supported, structurally-complete delivery position seal for
// the journal whose chain domain and seed are given: a fixed-size v2 A/B image whose selected slot(s)
// carry an in-bounds, correctly-summed record, or a v1 sidecar with canonical bounded fields.
func dsegValidateSeal(raw []byte, chainDomain string, seed core.Hash) bool {
	if len(raw) == dsealFileSize && dsealIsImage(raw) {
		return dsealSelect(raw, chainDomain, seed)
	}
	return dsealParseV1(raw, seed)
}

// dsegRecheckFrontier re-reads the authority AFTER all retention has been harvested and proves it is
// byte-identical to the witness taken at resolve time — head AND full log — and that every committed
// segment's required files still exist. A committed rotation is a durable log append that precedes the
// head checkpoint, so witnessing the head alone would miss it. Any change, any newly-appeared migration
// evidence on a legacy tree, or any now-missing required source halts the pass: a switch that raced this
// read must never let it sweep a segment it did not list.
func (s *FSStore) dsegRecheckFrontier(f dsegView) error {
	headPath := filepath.Join(s.l.State, dsegHeadFile)
	logPath := filepath.Join(s.l.State, dsegLogFile)

	_, headPresent, headRaw, err := s.dsegReadHead(headPath)
	if err != nil {
		return err
	}
	if headPresent != f.headPresent || !bytes.Equal(headRaw, f.headBytes) {
		return retentionUnavailable("delivery segment head changed during the harvest")
	}
	logRaw, logPresent, err := s.dsegReadFileBounded(logPath, dsegMaxLog)
	if err != nil {
		return err
	}
	if logPresent != f.logPresent || !bytes.Equal(logRaw, f.logBytes) {
		return retentionUnavailable("delivery segment log changed during the harvest")
	}
	if f.legacy {
		evidence, err := s.dsegMigrationEvidence()
		if err != nil {
			return err
		}
		if evidence {
			return retentionUnavailable("delivery segment migration evidence appeared during the harvest")
		}
		return nil
	}
	for _, seq := range f.committed {
		if err := s.dsegRequireSegmentFiles(seq); err != nil {
			return err
		}
	}
	return nil
}

// dsegReadHead reads and fully validates the head, returning its raw bytes for the stable-frontier
// comparison. present is false only when the head is genuinely absent.
func (s *FSStore) dsegReadHead(headPath string) (dsegHead, bool, []byte, error) {
	raw, present, err := s.dsegReadFileBounded(headPath, dsegMaxLine)
	if err != nil {
		return dsegHead{}, false, nil, err
	}
	if !present {
		return dsegHead{}, false, nil, nil
	}
	var h dsegHead
	if json.Unmarshal(raw, &h) != nil || h.Version != dsegVersion || h.Format != dsegFormat ||
		h.Seq < 0 || h.LogBytes <= 0 || h.LastLen < 1 || h.LastLen > h.LogBytes || h.LastLen > dsegMaxLine {
		return dsegHead{}, false, nil, retentionUnavailable("delivery segment head is malformed or unknown")
	}
	canon, cerr := json.Marshal(h)
	if cerr != nil || !bytes.Equal(canon, raw) {
		return dsegHead{}, false, nil, retentionUnavailable("delivery segment head has noncanonical bytes")
	}
	if _, ok := dsegHexHash(h.Chain); !ok || !dsegValidBaseRoot(h.Active, h.BaseRoot) {
		return dsegHead{}, false, nil, retentionUnavailable("delivery segment head chain/base root invalid")
	}
	return h, true, raw, nil
}

// dsegReadFileBounded opens path through the confined, non-regular-rejecting, SameFile-guarded,
// Windows-shared handle and reads up to max bytes. It returns present=false (nil error) only for a
// genuinely absent file; a disappearance after inspection, an oversize file, or any read error is
// errRetentionRootsUnavailable.
func (s *FSStore) dsegReadFileBounded(path string, max int64) (data []byte, present bool, err error) {
	fh, missing, err := s.openRetentionRoot(path)
	if err != nil {
		return nil, false, err
	}
	if missing {
		return nil, false, nil
	}
	defer func() { _ = fh.Close() }()
	data, rerr := io.ReadAll(io.LimitReader(fh, max+1))
	if rerr != nil {
		return nil, false, retentionUnavailable("read %s: %v", path, rerr)
	}
	if int64(len(data)) > max {
		return nil, false, retentionUnavailable("%s exceeds the %d-byte bound", path, max)
	}
	return data, true, nil
}

// dsegRequireRegular refuses unless path is a confined regular file: a missing required file, a symlink
// component, or a non-regular entry all halt the pass.
func (s *FSStore) dsegRequireRegular(path string) error {
	if nf := maintNoFollow(s.root, path); nf != nil {
		return retentionUnavailable("confine %s: %v", path, nf)
	}
	fi, err := os.Lstat(paths.Long(path))
	if err != nil {
		return retentionUnavailable("stat required segment file %s: %v", path, err)
	}
	if !fi.Mode().IsRegular() {
		return retentionUnavailable("required segment file %s is not a regular file", path)
	}
	return nil
}

// dsegMigrationEvidence reports whether the tree carries evidence of the new machinery — a
// delivery-generations directory, or a non-empty delivery-segments directory. Either present without a
// readable authority is head loss, not a fresh legacy tree. A symlink where such a directory belongs is
// itself an unreadable hazard and halts.
func (s *FSStore) dsegMigrationEvidence() (bool, error) {
	for _, name := range []string{dsegGensDir, dsegDir} {
		dir := filepath.Join(s.l.State, name)
		if nf := maintNoFollow(s.root, dir); nf != nil {
			if os.IsNotExist(nf) {
				continue
			}
			return false, retentionUnavailable("confine %s: %v", dir, nf)
		}
		fi, err := os.Lstat(paths.Long(dir))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, retentionUnavailable("stat %s: %v", dir, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return false, retentionUnavailable("%s is a symlink alias", dir)
		}
		if !fi.IsDir() {
			return false, retentionUnavailable("%s is not a directory", dir)
		}
		if name == dsegGensDir {
			return true, nil // the generation store's presence is migration evidence on its own
		}
		// delivery-segments counts as evidence only if it holds at least one entry.
		entries, rerr := s.dsegListSegmentDirs(nil)
		if rerr != nil {
			return false, rerr
		}
		if len(entries) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// dsegStagedSegments lists segment directories that are NOT in committed — staged, uncommitted attempts
// whose lease roots are conservatively retained (never deleted). committed dirs are excluded (their
// files are validated and harvested separately).
func (s *FSStore) dsegStagedSegments(budget *gcBudget, committed []uint64) ([]uint64, error) {
	all, err := s.dsegListSegmentDirs(budget)
	if err != nil {
		return nil, err
	}
	inCommitted := make(map[uint64]bool, len(committed))
	for _, c := range committed {
		inCommitted[c] = true
	}
	var staged []uint64
	for _, seq := range all {
		if !inCommitted[seq] {
			staged = append(staged, seq)
		}
	}
	return staged, nil
}

// dsegListSegmentDirs enumerates state/delivery-segments/ in bounded batches, returning the sequence of
// every canonically-named segment directory. A symlink alias, a non-directory, an entry whose name is
// not exactly a canonical 20-digit sequence, or a listing failure halts. budget may be nil (used from
// the evidence probe, which does not enumerate under a deadline).
func (s *FSStore) dsegListSegmentDirs(budget *gcBudget) ([]uint64, error) {
	dir := filepath.Join(s.l.State, dsegDir)
	if nf := maintNoFollow(s.root, dir); nf != nil {
		if os.IsNotExist(nf) {
			return nil, nil
		}
		return nil, retentionUnavailable("confine %s: %v", dir, nf)
	}
	fi, lerr := rootedLstat(filepath.Dir(dir), filepath.Base(dir))
	switch {
	case os.IsNotExist(lerr):
		return nil, nil
	case lerr != nil:
		return nil, retentionUnavailable("stat %s: %v", dir, lerr)
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, retentionUnavailable("%s is a symlink alias", dir)
	case !fi.IsDir():
		return nil, retentionUnavailable("%s is not a directory", dir)
	}
	d, oerr := os.Open(paths.Long(dir))
	if oerr != nil {
		return nil, retentionUnavailable("open %s: %v", dir, oerr)
	}
	defer func() { _ = d.Close() }()
	dfi, serr := d.Stat()
	if serr != nil || !dfi.IsDir() || !os.SameFile(fi, dfi) {
		return nil, retentionUnavailable("%s changed identity while opening", dir)
	}
	var out []uint64
	for {
		if budget != nil {
			if cerr := budget.ctx.Err(); cerr != nil {
				return nil, cerr
			}
		}
		ents, rerr := d.ReadDir(gcDirBatch)
		for _, e := range ents {
			seq, ok := dsegParseSeqName(e.Name())
			if !ok {
				return nil, retentionUnavailable("%s holds a non-canonical entry %q", dir, e.Name())
			}
			if e.IsDir() && e.Type()&os.ModeSymlink == 0 {
				if len(out) >= dsegMaxSegments {
					return nil, retentionUnavailable("delivery segments exceed the %d ceiling", dsegMaxSegments)
				}
				out = append(out, seq)
				continue
			}
			return nil, retentionUnavailable("%s entry %q is not a plain directory", dir, e.Name())
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return nil, retentionUnavailable("read %s: %v", dir, rerr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// dsegParseSeqName accepts only a canonical, zero-padded 20-digit sequence that is not segment 0
// (segment 0 never lives under delivery-segments). No inferred history from a loose numeric sort.
func dsegParseSeqName(name string) (uint64, bool) {
	if len(name) != dsegSeqWidth {
		return 0, false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return 0, false
		}
	}
	var seq uint64
	if _, err := fmt.Sscanf(name, "%d", &seq); err != nil {
		return 0, false
	}
	if seq == 0 || fmt.Sprintf("%0*d", dsegSeqWidth, seq) != name {
		return 0, false
	}
	return seq, true
}

// dsegValidateLog validates the entire transition log as a chain from the seed, returning the records
// and the cumulative byte end offset after each. Every record must be complete and canonical, seq dense
// from 0, active DENSE with seq (active == seq), the base root shaped for its active, and the chain
// exact. A torn trailing record, a gap, a non-dense active, or any mismatch is errRetentionRootsUnavailable.
func dsegValidateLog(raw []byte) ([]dsegTransition, []int64, error) {
	var trans []dsegTransition
	var ends []int64
	prev := dsegChainSeed
	var expectSeq int64
	pos := 0
	for pos < len(raw) {
		nl := bytes.IndexByte(raw[pos:], '\n')
		if nl < 0 {
			return nil, nil, retentionUnavailable("delivery segment log has a torn trailing record")
		}
		line := raw[pos : pos+nl+1]
		if len(line) > dsegMaxLine {
			return nil, nil, retentionUnavailable("delivery segment log record exceeds %d bytes", dsegMaxLine)
		}
		rec, chain, ok := dsegDecodeTransition(line)
		if !ok || rec.Seq != expectSeq {
			return nil, nil, retentionUnavailable("delivery segment log record %d is malformed or out of sequence", expectSeq)
		}
		// Active is DENSE from zero: the active segment index equals the transition sequence, so seq 0
		// is the legacy active-0 segment and each rotation advances active by exactly one. The producer
		// (commitTransition/readHead/scanTail) enforces the same; a gap or repeat is refused, never
		// inferred over.
		if rec.Active != uint64(rec.Seq) {
			return nil, nil, retentionUnavailable("delivery segment active %d is not dense with seq %d", rec.Active, rec.Seq)
		}
		if !dsegValidBaseRoot(rec.Active, rec.BaseRoot) || chain != dsegChain(prev, rec.Seq, rec.Active, rec.BaseRoot) {
			return nil, nil, retentionUnavailable("delivery segment chain/base root invalid at seq %d", rec.Seq)
		}
		trans = append(trans, rec)
		pos += nl + 1
		ends = append(ends, int64(pos))
		if len(trans) > dsegMaxSegments {
			return nil, nil, retentionUnavailable("delivery segment log exceeds the %d ceiling", dsegMaxSegments)
		}
		prev, expectSeq = chain, expectSeq+1
	}
	if len(trans) == 0 {
		return nil, nil, retentionUnavailable("delivery segment log is empty")
	}
	return trans, ends, nil
}

// dsegRecordLen returns the byte length of the record at index i, from the cumulative end offsets.
func dsegRecordLen(ends []int64, i int64) int64 {
	if i == 0 {
		return ends[0]
	}
	return ends[i] - ends[i-1]
}

func dsegDecodeTransition(line []byte) (dsegTransition, [32]byte, bool) {
	var rec dsegTransition
	if json.Unmarshal(line, &rec) != nil || rec.Version != dsegVersion {
		return dsegTransition{}, [32]byte{}, false
	}
	canon, err := json.Marshal(rec)
	if err != nil || len(canon)+1 != len(line) || !bytes.Equal(canon, line[:len(canon)]) || line[len(line)-1] != '\n' {
		return dsegTransition{}, [32]byte{}, false
	}
	chain, ok := dsegHexHash(rec.Chain)
	if !ok {
		return dsegTransition{}, [32]byte{}, false
	}
	return rec, chain, true
}

// dsegActiveSet is the sorted set of segment indices the committed transitions name active (always
// including 0, the legacy segment). These are exactly the segments that ever existed.
func dsegActiveSet(trans []dsegTransition) []uint64 {
	seen := make(map[uint64]bool, len(trans))
	var out []uint64
	for _, t := range trans {
		if !seen[t.Active] {
			seen[t.Active] = true
			out = append(out, t.Active)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ── delivery position seal (read-only mirror of internal/daemon/delivery_seal.go) ──────────────────
//
// A small, EXACT, read-only duplicate of the producer's seal format, so a committed segment's seals can
// be validated structurally without importing the daemon (§3.2). The running producer writes a v2
// fixed-size A/B image; a fresh segment (and a rolled-back build) writes a v1 sidecar. Both are
// supported. The constants, layout, sum domain, chain domains and bounds are mirrored byte-for-byte; an
// explicit cross-package agreement test is owed (main's delivery_readers_v6_test). See dsegRequireSeal
// for the exact validation limit (seal structure + integrity, NOT a whole-journal chain fsck).
const (
	dsealFileSize    = 2 * dsealStride
	dsealStride      = 16 << 10
	dsealSlotRegion  = 480
	dsealPrefixA     = `{"v":2,"a":`
	dsealPrefixB     = `,"b":`
	dsealSuffix      = `}`
	dsealEmpty       = `null`
	dsealPad         = ' '
	dsealSlotAOffset = len(dsealPrefixA)
	dsealSlotBOffset = dsealStride + len(dsealPrefixB)
	dsealSumDomain   = "qompack.delivery.seal.v2"

	dsealLeaseChainDomain = "qompack.delivery.lease-chain.v1"
	dsealAckChainDomain   = "qompack.delivery.ack-chain.v1"
	dsealMaxBytes         = 64 << 20
	dsealMaxEntries       = 65536
	dsealMaxLine          = 64 << 10
	dsealV1Version        = 1 // core.EvidenceVersion, the v1 sidecar's "v"
)

var (
	dsealLeaseSeed = core.HashBytes(dsealLeaseChainDomain, nil)
	dsealAckSeed   = core.HashBytes(dsealAckChainDomain, nil)
)

// dsealRecord and dsealPosition mirror the producer's records field-for-field and in order, so a
// json.Marshal round-trip reproduces the on-disk bytes and rejects unknown/reordered/duplicate keys.
type dsealRecord struct {
	Seq   uint64    `json:"seq"`
	Bytes int64     `json:"bytes"`
	Count int       `json:"count"`
	Chain core.Hash `json:"chain"`
	Sum   core.Hash `json:"sum"`
}

type dsealPosition struct {
	Version int       `json:"v"`
	Bytes   int64     `json:"bytes"`
	Count   int       `json:"count"`
	Chain   core.Hash `json:"chain"`
}

const (
	dsealSlotInvalid = iota
	dsealSlotEmpty
	dsealSlotValid
)

// dsealSlotFor mirrors slotFor: an odd seq lives in slot 'a', an even one in 'b'.
func dsealSlotFor(seq uint64) byte {
	if seq%2 == 1 {
		return 'a'
	}
	return 'b'
}

// dsealSum mirrors the producer's sealSum exactly.
func dsealSum(chainDomain string, slot byte, seq uint64, size int64, count int, chain core.Hash) core.Hash {
	input := append([]byte(chainDomain), 0, slot, 0)
	input = strconv.AppendUint(input, seq, 10)
	input = append(input, 0)
	input = strconv.AppendInt(input, size, 10)
	input = append(input, 0)
	input = strconv.AppendInt(input, int64(count), 10)
	input = append(input, 0)
	input = append(input, chain[:]...)
	return core.HashBytes(dsealSumDomain, input)
}

// dsealInBounds mirrors sealInBounds: the journal reader's exact position bounds.
func dsealInBounds(size int64, count int, chain, seed core.Hash) bool {
	return size >= 0 && size <= dsealMaxBytes && count >= 0 && count <= dsealMaxEntries &&
		(size == 0) == (count == 0) && !chain.IsZero() && (size != 0 || chain == seed)
}

// dsealAdmissible mirrors sealAdmissible: a seq>=1 with the slot's parity and an in-bounds position.
func dsealAdmissible(rec dsealRecord, slot byte, seed core.Hash) bool {
	return rec.Seq >= 1 && dsealSlotFor(rec.Seq) == slot && dsealInBounds(rec.Bytes, rec.Count, rec.Chain, seed)
}

// dsealPadded reports whether b is all padding.
func dsealPadded(b []byte) bool {
	for _, c := range b {
		if c != dsealPad {
			return false
		}
	}
	return true
}

// dsealClassifySlot mirrors classifySlot: empty (null then pad), valid (canonical in-bounds correctly
// summed record then pad), or invalid.
func dsealClassifySlot(region []byte, slot byte, chainDomain string, seed core.Hash) (dsealRecord, int) {
	if len(region) != dsealSlotRegion {
		return dsealRecord{}, dsealSlotInvalid
	}
	end := bytes.IndexByte(region, dsealPad)
	if end < 0 {
		end = len(region)
	}
	if !dsealPadded(region[end:]) {
		return dsealRecord{}, dsealSlotInvalid
	}
	body := region[:end]
	if string(body) == dsealEmpty {
		return dsealRecord{}, dsealSlotEmpty
	}
	var rec dsealRecord
	if json.Unmarshal(body, &rec) != nil {
		return dsealRecord{}, dsealSlotInvalid
	}
	canon, err := json.Marshal(rec)
	if err != nil || !bytes.Equal(canon, body) || !dsealAdmissible(rec, slot, seed) ||
		rec.Sum != dsealSum(chainDomain, slot, rec.Seq, rec.Bytes, rec.Count, rec.Chain) {
		return dsealRecord{}, dsealSlotInvalid
	}
	return rec, dsealSlotValid
}

// dsealIsImage mirrors isDeliverySealImage: the exact static v2 layout.
func dsealIsImage(img []byte) bool {
	aEnd := dsealSlotAOffset + dsealSlotRegion
	bEnd := dsealSlotBOffset + dsealSlotRegion
	suffix := dsealFileSize - len(dsealSuffix)
	return len(img) == dsealFileSize &&
		string(img[:dsealSlotAOffset]) == dsealPrefixA &&
		dsealPadded(img[aEnd:dsealStride]) &&
		string(img[dsealStride:dsealSlotBOffset]) == dsealPrefixB &&
		dsealPadded(img[bEnd:suffix]) &&
		string(img[suffix:]) == dsealSuffix
}

// dsealSelect mirrors selectSeal's accept table: slot a seq-1 with b empty, or two valid slots one seq
// apart with the older sealing strictly fewer bytes and entries. Anything else refuses.
func dsealSelect(img []byte, chainDomain string, seed core.Hash) bool {
	if !dsealIsImage(img) {
		return false
	}
	a, aState := dsealClassifySlot(img[dsealSlotAOffset:dsealSlotAOffset+dsealSlotRegion], 'a', chainDomain, seed)
	b, bState := dsealClassifySlot(img[dsealSlotBOffset:dsealSlotBOffset+dsealSlotRegion], 'b', chainDomain, seed)
	switch {
	case aState == dsealSlotValid && bState == dsealSlotEmpty && a.Seq == 1:
		return true
	case aState == dsealSlotValid && bState == dsealSlotValid:
		eff, older := a, b
		if b.Seq > a.Seq {
			eff, older = b, a
		}
		return eff.Seq-older.Seq == 1 && older.Bytes < eff.Bytes && older.Count < eff.Count
	}
	return false
}

// dsealParseV1 mirrors parseDeliveryPositionV1: a canonical, bounded v1 sidecar for the given seed.
func dsealParseV1(raw []byte, seed core.Hash) bool {
	if len(raw) > dsealMaxLine {
		return false
	}
	var p dsealPosition
	if json.Unmarshal(raw, &p) != nil || p.Version != dsealV1Version ||
		p.Bytes < 0 || p.Bytes > dsealMaxBytes || p.Count < 0 || p.Count > dsealMaxEntries ||
		(p.Bytes == 0) != (p.Count == 0) || p.Chain.IsZero() || (p.Bytes == 0 && p.Chain != seed) {
		return false
	}
	canon, err := json.Marshal(p)
	return err == nil && bytes.Equal(canon, raw)
}
