package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Durable observation binding through the versioned sidecar index/observations.jsonl (superseding
// coordinator decision 2026-09-21; 00-ARCHITECTURE.md §0.2). The tool_use wire — exported AND private
// tuRec — stays frozen; the observation↔record join is an append-only, versioned publication INTENT
// written BEFORE the legacy record.
//
// The normal path is wired into RecordToolUse/RecordToolUseSuperseding: an observation-bearing record
// fsyncs its intent, writes the single record-plus-marks batch, syncs the publication and commits. A
// single Write is NOT crash-atomic (main adjudication); the fsynced intent plus precondition-guarded
// recovery covers the crash cut. Completion and idempotence always replay the STORED ORIGINAL intent,
// never a caller-modified one. Until an intent's publication is durable and complete it is UNAVAILABLE
// to the committed lookup — never a published record, never a false absence.
//
// Fail-closed posture (final review F2, coordinator): a torn/over-capacity/unreadable sidecar, or a
// runtime intent-write/sync failure, sets obsSidecarUncertain for the PROCESS LIFETIME. There is no
// runtime clear, no automatic truncation and no fsck repair: a full store reopen re-derives the state,
// and torn or conflicting evidence needs a verified backup/recovery. While uncertain, an UNKNOWN
// observation and any new reservation are unavailable. This file claims no ACK authority.

const (
	observationsFile         = "observations.jsonl"
	observationIntentVersion = 3 // v3 freezes the requested target list as well as selected identities (not shipped)
	maxObservationLine       = 1 << 20
	maxObservationSupersedes = 4096 //nomagic:allow publication intent format bound, independent of configuration defaults.
	// Lifetime capacity: append is refused past these, and load past them is uncertain — an explicit
	// bound, not an unbounded-support claim.
	maxObservationSidecarBytes   = 64 << 20
	maxObservationSidecarEntries = 1 << 20
)

// ObservationReserver writes the durable publication intent for an observation. The ordinary
// RecordToolUse* path uses it internally; a caller may also reserve without publishing.
type ObservationReserver interface {
	ReserveObservation(ctx context.Context, obs core.ObservationID, rec ToolUseRecord, supersedes []core.ToolUseID) error
}

// ObservationRecovery resumes a reserved intent and completes its legacy publication, returning the
// record only once it is durable and complete. Separate from the pure committed lookup.
type ObservationRecovery interface {
	RecoverToolUseByObservation(ctx context.Context, obs core.ObservationID) (ToolUseRecord, error)
}

var (
	_ ObservationReserver = (*FSStore)(nil)
	_ ObservationRecovery = (*FSStore)(nil)
)

// reservedIdentity is the immutable metadata a reservation binds a tool_use id to.
type reservedIdentity struct {
	Root    core.Hash
	Session core.SessionID
	Tool    string
	Path    string
}

func identityOf(rec ToolUseRecord) reservedIdentity {
	return reservedIdentity{Root: rec.Root, Session: rec.Session, Tool: rec.Tool, Path: rec.Path}
}

// observationTarget is one selected supersede target with the identity it had when it was selected, so
// recovery can tell an original no-op (absent at reserve, never recorded) from a LOST record (recorded
// then vanished) and refuse to forge completion over corruption.
type observationTarget struct {
	ID       core.ToolUseID `json:"id"`
	Root     core.Hash      `json:"root"`
	Identity core.Hash      `json:"identity"`
}

// observationIntent is one reserved binding, held with its canonical bytes.
type observationIntent struct {
	obs   core.ObservationID
	rec   ToolUseRecord
	sup   []observationTarget
	asked []core.ToolUseID
}

// obsBinding is the stored ORIGINAL for an observation.
type obsBinding struct {
	intent    observationIntent
	raw       []byte // canonical wire bytes: exact-equality is the conflict/idempotence test
	committed bool
}

// observationIntentWire is the on-disk shape. Rec is the frozen private tuRec.
type observationIntentWire struct {
	V     int                 `json:"v"`
	Obs   core.ObservationID  `json:"obs"`
	Rec   tuRec               `json:"rec"`
	Sup   []observationTarget `json:"sup,omitempty"`
	Asked []core.ToolUseID    `json:"asked,omitempty"`
}

func (s *FSStore) observationsPath() string { return filepath.Join(s.l.Index, observationsFile) }

func recToTU(rec ToolUseRecord) tuRec {
	return tuRec{
		V: indexRecordVersion, ID: rec.ID, S: rec.Session, Turn: rec.Turn, TS: rec.TS,
		Tool: rec.Tool, ArgD: rec.ArgsDigest, ArgP: rec.ArgsPreview, Root: rec.Root, Path: rec.Path,
		Bytes: rec.Bytes, Tokens: rec.Tokens, Sig: tuSignature(rec.Signature),
		St: rec.Status, By: rec.SupersededBy, Eph: rec.Ephemeral, Sub: rec.Subagent,
	}
}

func tuToRec(r tuRec) ToolUseRecord {
	return ToolUseRecord{
		ID: r.ID, Session: r.S, Turn: r.Turn, TS: r.TS, Tool: r.Tool,
		ArgsDigest: r.ArgD, ArgsPreview: r.ArgP, Root: r.Root, Path: r.Path,
		Bytes: r.Bytes, Tokens: r.Tokens, Signature: tuSignatureFrom(r.Sig),
		Status: r.St, SupersededBy: r.By, Ephemeral: r.Eph, Subagent: r.Sub,
	}
}

// validObservationID requires the canonical sha256 form core.NewObservationID yields.
func validObservationID(id core.ObservationID) bool {
	if id == "" {
		return false
	}
	h, err := core.ParseHash(string(id))
	return err == nil && !h.IsZero() && h.String() == string(id)
}

// canonicalIntent renders in's exact wire bytes WITHOUT a trailing newline — the single comparison and
// storage form, so a stored original and a fresh reservation compare byte-for-byte. The newline is
// added only at write time. Marshalling deterministically is what makes byte-equality a valid
// same-intent test.
func canonicalIntent(in observationIntent) ([]byte, error) {
	w := observationIntentWire{V: observationIntentVersion, Obs: in.obs, Rec: recToTU(in.rec), Sup: in.sup, Asked: in.asked}
	line, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("store: encode observation intent: %w", err)
	}
	return line, nil
}

func (s *FSStore) ensureObsMaps() {
	if s.obsBindings == nil {
		s.obsBindings = make(map[core.ObservationID]*obsBinding)
	}
	if s.obsAmbiguous == nil {
		s.obsAmbiguous = make(map[core.ObservationID]struct{})
	}
	if s.obsUnavailable == nil {
		s.obsUnavailable = make(map[core.ObservationID]struct{})
	}
	if s.obsReserved == nil {
		s.obsReserved = make(map[core.ToolUseID]reservedIdentity)
	}
}

// markObservationUnavailableLocked records that obs's intent cannot be honoured, so the lookup and
// recovery report it degraded rather than absent, with PRIORITY over any committed binding.
func (s *FSStore) markObservationUnavailableLocked(obs core.ObservationID) {
	if obs == "" {
		return
	}
	s.ensureObsMaps()
	s.obsUnavailable[obs] = struct{}{}
}

// ── load (F1 real bound, F4 root-confined) ─────────────────────────────────────────────────────

// loadObservationsLocked replays index/observations.jsonl AFTER the tool_use index, under a root-
// confined handle (so a swapped symlink cannot redirect the read) and a real byte/entry ceiling (so a
// hostile/large sidecar cannot force unbounded open-time I/O). Any unreadable/oversize/over-capacity
// condition marks the sidecar uncertain; attributable bad lines mark their observation unavailable.
func (s *FSStore) loadObservationsLocked() {
	root, err := s.openObservationIndex()
	if err != nil {
		if !os.IsNotExist(err) {
			s.obsSidecarUncertain = true
		}
		return
	}
	defer func() { _ = root.Close() }()
	info, statErr := root.Lstat(observationsFile)
	if os.IsNotExist(statErr) {
		return
	}
	if statErr != nil || !info.Mode().IsRegular() {
		s.obsSidecarUncertain = true
		return
	}
	f, err := root.OpenFile(observationsFile, os.O_RDONLY, 0)
	if err != nil {
		if !os.IsNotExist(err) {
			s.obsSidecarUncertain = true // a symlink/dir/special path, or an I/O error, is not "no bindings"
		}
		return
	}
	defer func() { _ = f.Close() }()
	opened, statErr := f.Stat()
	if statErr != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		s.obsSidecarUncertain = true
		return
	}

	sc := bufio.NewScanner(io.LimitReader(f, maxObservationSidecarBytes+1))
	sc.Buffer(make([]byte, 0, 64<<10), maxObservationLine)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if end := bytes.IndexByte(data, '\n'); end >= 0 {
			return end + 1, data[:end], nil
		}
		if atEOF && len(data) > 0 {
			s.markObservationUnavailableLocked(probeObservation(data))
			return 0, nil, fmt.Errorf("unterminated observation intent")
		}
		return 0, nil, nil
	})
	var totalBytes int64
	var entries int
	bad := 0
	for sc.Scan() {
		raw := sc.Bytes()
		totalBytes += int64(len(raw)) + 1
		if entries+1 > maxObservationSidecarEntries || totalBytes > maxObservationSidecarBytes {
			s.obsSidecarUncertain = true
			break // a REAL stop, not merely "stop applying" (F1)
		}
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		entries++
		w, ok := decodeObservationLine(line)
		if !ok {
			bad++
			s.obsSidecarUncertain = true
			s.markObservationUnavailableLocked(probeObservation(line))
			continue
		}
		s.applyIntentLocked(observationIntent{obs: w.Obs, rec: tuToRec(w.Rec), sup: w.Sup, asked: w.Asked}, append([]byte(nil), line...))
	}
	if serr := sc.Err(); serr != nil {
		s.obsSidecarUncertain = true // an over-long line or a read error
	}
	s.obsSidecarBytes = totalBytes
	s.obsSidecarEntries = entries
	if bad > 0 {
		s.count("store.observations.badline", int64(bad))
		s.log.Warn("store: unreadable observation intents", "file", s.observationsPath(), "lines", bad)
	}
}

// decodeObservationLine accepts a line only as a canonical, known-version intent that round-trips
// byte-for-byte (rejecting duplicate/reordered/noncanonical fields).
func decodeObservationLine(line []byte) (observationIntentWire, bool) {
	var w observationIntentWire
	if json.Unmarshal(line, &w) != nil {
		return observationIntentWire{}, false
	}
	if w.V != observationIntentVersion || w.Rec.V != indexRecordVersion || !validObservationID(w.Obs) || w.Rec.ID == "" ||
		len(w.Sup) > maxObservationSupersedes || len(w.Asked) > maxObservationSupersedes {
		return observationIntentWire{}, false
	}
	canonical, err := json.Marshal(w)
	if err != nil || !bytes.Equal(canonical, line) {
		return observationIntentWire{}, false
	}
	return w, true
}

// probeObservation extracts the top-level "obs" value from a line the full decode refused, tolerating
// a truncated tail so a torn intent is attributable and reported unavailable rather than lost.
func probeObservation(line []byte) core.ObservationID {
	dec := json.NewDecoder(bytes.NewReader(line))
	if tok, err := dec.Token(); err != nil {
		return ""
	} else if d, ok := tok.(json.Delim); !ok || d != '{' {
		return ""
	}
	for {
		keyTok, err := dec.Token()
		if err != nil {
			return ""
		}
		key, ok := keyTok.(string)
		if !ok {
			return ""
		}
		valTok, err := dec.Token()
		if err != nil {
			return ""
		}
		if key == "obs" {
			if sv, ok := valTok.(string); ok && validObservationID(core.ObservationID(sv)) {
				return core.ObservationID(sv)
			}
			return ""
		}
		if d, ok := valTok.(json.Delim); ok && (d == '{' || d == '[') {
			if err := skipNestedToken(dec); err != nil {
				return ""
			}
		}
	}
}

func skipNestedToken(dec *json.Decoder) error {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

// applyIntentLocked folds one parsed intent (with its canonical bytes) into the observation maps. A
// second intent for one observation whose canonical bytes DIFFER is ambiguous; the same id under a
// different reserved identity is ambiguous. Otherwise the stored original's completeness derives
// committed / pending / lost, and a lost target makes the observation unavailable with priority.
func (s *FSStore) applyIntentLocked(in observationIntent, raw []byte) {
	s.ensureObsMaps()
	if b, ok := s.obsBindings[in.obs]; ok && !bytes.Equal(b.raw, raw) {
		s.obsAmbiguous[in.obs] = struct{}{}
		return
	}
	if got, ok := s.obsReserved[in.rec.ID]; ok && got != identityOf(in.rec) {
		s.obsAmbiguous[in.obs] = struct{}{}
		return
	}
	s.obsReserved[in.rec.ID] = identityOf(in.rec)
	published, lost := s.observationStateLocked(in)
	s.obsBindings[in.obs] = &obsBinding{intent: in, raw: raw, committed: published}
	if lost {
		s.markObservationUnavailableLocked(in.obs)
	}
}

// observationStateLocked classifies an intent against the current tool_use index (three-way):
//   - published: the record is present with the reserved Root and every recorded target is satisfied
//     (superseded by us or by a later record — later supersession preserved);
//   - lost: a recorded target has VANISHED or its Root no longer matches (corruption/incomplete
//     history), so completion must not be forged;
//   - otherwise pending (a still-live target needs its mark, the completable case).
//
// It never equates an arbitrary Status != OK with "done" beyond a genuine supersession: a missing
// record is lost, not done.
func (s *FSStore) observationStateLocked(in observationIntent) (published, lost bool) {
	rec, ok := s.toolUse[in.rec.ID]
	recordOK := ok && observationRecordDigest(*rec) == observationRecordDigest(in.rec)
	if ok && !recordOK {
		return false, true
	}
	incomplete := false
	for _, tgt := range in.sup {
		cur, ok := s.toolUse[tgt.ID]
		switch {
		case !ok || cur.Root != tgt.Root || observationRecordDigest(*cur) != tgt.Identity:
			return false, true // a recorded target lost or its identity changed
		case cur.Status != StatusOK && (cur.Status != StatusSuperseded || cur.SupersededBy == ""):
			return false, true
		case cur.Status == StatusOK:
			incomplete = true // a required mark is still missing
		}
	}
	return recordOK && !incomplete, false
}

// observationKnownLocked reports whether obs is named by any binding state.
func (s *FSStore) observationKnownLocked(obs core.ObservationID) bool {
	if _, ok := s.obsBindings[obs]; ok {
		return true
	}
	if _, ok := s.obsAmbiguous[obs]; ok {
		return true
	}
	_, ok := s.obsUnavailable[obs]
	return ok
}

// ── conflict / reservation ─────────────────────────────────────────────────────────────────────

// reservedIdentityConflictLocked refuses a non-reserver writer that would land a reserved id under
// different immutable metadata. Called by the plain legacy cores under s.mu (both they and the
// reservation path run under obsPubMu, so the check and the reserved-map install cannot interleave).
func (s *FSStore) reservedIdentityConflictLocked(rec ToolUseRecord) error {
	if got, ok := s.obsReserved[rec.ID]; ok && got != identityOf(rec) {
		return fmt.Errorf("%w: tool_use %s is reserved for a different identity", core.ErrAppendOnly, rec.ID)
	}
	return nil
}

// buildIntentLocked normalises the caller's supersede list to the targets that are CURRENTLY LIVE
// (present and StatusOK), recording each one's identity. A caller target that is absent or already
// superseded is omitted (a legitimate no-op at reserve), so recovery can tell that omission from a
// LOST target later.
func (s *FSStore) buildIntentLocked(obs core.ObservationID, rec ToolUseRecord, supersedes []core.ToolUseID) observationIntent {
	if original := s.obsBindings[obs]; original != nil {
		in := original.intent
		in.rec = rec
		in.asked = append([]core.ToolUseID(nil), supersedes...)
		return in
	}
	// Another delivery may repeat the same host tool ID. Preserve the original
	// record and the legacy duplicate-ID no-op semantics; a fresh observation
	// must not append fresh marks or rewrite its original turn/time/provenance.
	if existing := s.toolUse[rec.ID]; existing != nil && identityOf(*existing) == identityOf(rec) {
		rec = *existing
		rec.Observation = obs
		rec.Status, rec.SupersededBy = StatusOK, ""
		return observationIntent{obs: obs, rec: rec, asked: append([]core.ToolUseID(nil), supersedes...)}
	}
	sup := make([]observationTarget, 0, len(supersedes))
	seen := make(map[core.ToolUseID]bool, len(supersedes))
	for _, id := range supersedes {
		if id == rec.ID || seen[id] {
			continue
		}
		cur, ok := s.toolUse[id]
		if !ok || cur.Status != StatusOK {
			continue // absent or already superseded: a no-op, omitted from the intent
		}
		seen[id] = true
		sup = append(sup, observationTarget{ID: id, Root: cur.Root, Identity: observationRecordDigest(*cur)})
	}
	if len(sup) == 0 {
		sup = nil
	}
	return observationIntent{obs: obs, rec: rec, sup: sup, asked: append([]core.ToolUseID(nil), supersedes...)}
}

// reserveConflictLocked (obsPubMu held) decides whether in may be reserved, comparing the FULL
// canonical bytes against any stored original. It reports whether the reservation is an exact
// idempotent repeat.
func (s *FSStore) reserveConflictLocked(in observationIntent, raw []byte) (idempotent bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, amb := s.obsAmbiguous[in.obs]; amb {
		return false, fmt.Errorf("%w: observation %s is already ambiguous", core.ErrDegraded, in.obs)
	}
	if _, unavailable := s.obsUnavailable[in.obs]; unavailable {
		return false, core.ErrDegraded
	}
	if b, ok := s.obsBindings[in.obs]; ok {
		if !bytes.Equal(b.raw, raw) {
			return false, fmt.Errorf("%w: observation %s already reserved for a different intent", core.ErrAppendOnly, in.obs)
		}
		return true, nil // exact idempotent repeat (committed or pending)
	}
	if got, ok := s.obsReserved[in.rec.ID]; ok && got != identityOf(in.rec) {
		return false, fmt.Errorf("%w: tool_use %s already reserved for a different identity", core.ErrAppendOnly, in.rec.ID)
	}
	if cur, ok := s.toolUse[in.rec.ID]; ok && identityOf(*cur) != identityOf(in.rec) {
		return false, fmt.Errorf("%w: tool_use %s already recorded", core.ErrAppendOnly, in.rec.ID)
	}
	if s.obsSidecarUncertain {
		return false, fmt.Errorf("%w: observation sidecar completeness is unproved; reservation refused", core.ErrDegraded)
	}
	return false, nil
}

// ── intent write (F4 root-confined, finding 4 lifetime cap, finding 3 flag under s.mu) ─────────

// appendObservationIntent writes one intent line through a root-confined handle (so a swapped symlink
// cannot redirect it), enforcing the lifetime capacity before the write and fsyncing it plus the index
// directory. Any write/sync/close failure poisons the sidecar under s.mu.
func (s *FSStore) appendObservationIntent(line []byte) error {
	record := append(append([]byte(nil), line...), '\n')
	s.mu.RLock()
	overCap := s.obsSidecarBytes+int64(len(record)) > maxObservationSidecarBytes ||
		s.obsSidecarEntries+1 > maxObservationSidecarEntries
	s.mu.RUnlock()
	if overCap {
		return fmt.Errorf("%w: observation sidecar is at its capacity limit", core.ErrBudget)
	}

	root, err := s.openObservationIndex()
	if err != nil {
		s.poisonSidecar()
		return fmt.Errorf("%w: open index root: %v", core.ErrDegraded, err)
	}
	defer func() { _ = root.Close() }()
	// A non-regular sidecar (a directory, or a symlink/reparse alias — os.Root refuses to traverse one
	// on the OpenFile below in any case) is a corrupt path: fail closed rather than open it for write.
	if fi, lerr := root.Lstat(observationsFile); lerr == nil && !fi.Mode().IsRegular() {
		s.poisonSidecar()
		return fmt.Errorf("%w: observation sidecar is not a regular file", core.ErrDegraded)
	}
	f, err := root.OpenFile(observationsFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		s.poisonSidecar()
		return fmt.Errorf("%w: open observation sidecar: %v", core.ErrDegraded, err)
	}
	if n, werr := f.Write(record); werr != nil || n != len(record) {
		if werr == nil {
			werr = io.ErrShortWrite
		}
		_ = f.Close()
		s.poisonSidecar()
		return fmt.Errorf("store: write observation intent: %w", werr)
	}
	if serr := s.syncData()(f); serr != nil {
		_ = f.Close()
		s.poisonSidecar()
		return fmt.Errorf("store: sync observation intent: %w", serr)
	}
	if cerr := f.Close(); cerr != nil {
		s.poisonSidecar()
		return fmt.Errorf("store: close observation sidecar: %w", cerr)
	}
	if derr := paths.SyncDir(s.l.Index); derr != nil {
		s.poisonSidecar()
		return fmt.Errorf("store: sync observation directory: %w", derr)
	}
	s.mu.Lock()
	s.obsSidecarBytes += int64(len(record))
	s.obsSidecarEntries++
	s.mu.Unlock()
	return nil
}

// syncData is the intent fsync, overridable by a test seam (nil → paths.SyncData).
func (s *FSStore) syncData() func(*os.File) error {
	if s.obsSyncData != nil {
		return s.obsSyncData
	}
	return paths.SyncData
}

// poisonSidecar marks completeness unprovable, under s.mu, so a concurrent lookup reader never sees a
// torn flag.
func (s *FSStore) poisonSidecar() {
	s.mu.Lock()
	s.obsSidecarUncertain = true
	s.mu.Unlock()
}

// reserveIntentLocked (obsPubMu held) validates, refuses conflicts and — unless idempotent — durably
// writes the intent and installs the stored original.
func (s *FSStore) reserveIntentLocked(in observationIntent) error {
	raw, err := canonicalIntent(in)
	if err != nil {
		return err
	}
	if len(raw)+1 >= maxObservationLine {
		return fmt.Errorf("%w: observation intent is %d bytes, over the %d bound", core.ErrContract, len(raw), maxObservationLine)
	}
	idempotent, err := s.reserveConflictLocked(in, raw)
	if err != nil {
		return err
	}
	if idempotent {
		return nil
	}
	if err := s.appendObservationIntent(raw); err != nil {
		return err
	}
	s.mu.Lock()
	s.applyIntentLocked(in, raw)
	s.mu.Unlock()
	return nil
}

// validateIntentInput checks the common preconditions and redacts the preview. Callers have already
// taken the write guard (mutate) at the public boundary.
func (s *FSStore) validateIntentInput(ctx context.Context, obs core.ObservationID, rec *ToolUseRecord, supersedes []core.ToolUseID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validObservationID(obs) {
		return fmt.Errorf("%w: observation id is not a canonical hash", core.ErrContract)
	}
	if rec.ID == "" {
		return fmt.Errorf("%w: reserved record has no id", core.ErrContract)
	}
	if len(supersedes) > maxObservationSupersedes {
		return fmt.Errorf("%w: intent supersedes %d targets, over the %d bound", core.ErrContract, len(supersedes), maxObservationSupersedes)
	}
	if red, matches := s.deps.Redact.Redact([]byte(rec.ArgsPreview)); len(matches) > 0 {
		rec.ArgsPreview = previewString(string(red))
	}
	return nil
}

// ReserveObservation durably reserves obs's intent without publishing the record.
func (s *FSStore) ReserveObservation(ctx context.Context, obs core.ObservationID, rec ToolUseRecord, supersedes []core.ToolUseID) error {
	if err := s.mutate(); err != nil {
		return err
	}
	if err := s.validateIntentInput(ctx, obs, &rec, supersedes); err != nil {
		return err
	}
	s.obsPubMu.Lock()
	defer s.obsPubMu.Unlock()
	if err := s.mutate(); err != nil {
		return err
	}
	s.mu.RLock()
	in := s.buildIntentLocked(obs, rec, supersedes)
	s.mu.RUnlock()
	return s.reserveIntentLocked(in)
}

// ── publish (the wired normal path) ──────────────────────────────────────────────────────────

// publishObservation is the observation-bearing RecordToolUse[Superseding] path: reserve the intent,
// then complete the STORED ORIGINAL's publication.
func (s *FSStore) publishObservation(ctx context.Context, rec ToolUseRecord, older []core.ToolUseID) ([]core.ToolUseID, bool, error) {
	obs := rec.Observation
	if err := s.validateIntentInput(ctx, obs, &rec, older); err != nil {
		return nil, false, err
	}
	s.obsPubMu.Lock()
	defer s.obsPubMu.Unlock()
	if err := s.use(); err != nil {
		return nil, false, err
	}

	s.mu.RLock()
	in := s.buildIntentLocked(obs, rec, older)
	s.mu.RUnlock()

	if err := s.reserveIntentLocked(in); err != nil {
		return nil, false, err
	}

	s.mu.RLock()
	b := s.obsBindings[obs]
	_, amb := s.obsAmbiguous[obs]
	s.mu.RUnlock()
	if amb || b == nil {
		return nil, false, fmt.Errorf("%w: observation %s is ambiguous", core.ErrDegraded, obs)
	}
	return s.completeIntentLocked(ctx, b.intent)
}

// completeIntentLocked (obsPubMu held) makes the STORED ORIGINAL intent's publication durable and
// complete: verify+sync the record's root, replay the record and only the still-missing marks (a LOST
// target makes the observation unavailable, not success), sync the appended index, and commit only
// when observationStateLocked reports published.
func (s *FSStore) completeIntentLocked(ctx context.Context, in observationIntent) ([]core.ToolUseID, bool, error) {
	if err := s.SyncPublication(ctx, in.rec.Root); err != nil {
		return nil, false, fmt.Errorf("%w: observation %s: original root not durable: %v", core.ErrDegraded, in.obs, err)
	}

	s.mu.RLock()
	_, recordPresent := s.toolUse[in.rec.ID]
	toMark := make([]core.ToolUseID, 0, len(in.sup))
	lost := false
	for _, tgt := range in.sup {
		cur, ok := s.toolUse[tgt.ID]
		switch {
		case !ok || cur.Root != tgt.Root || observationRecordDigest(*cur) != tgt.Identity:
			lost = true
		case cur.Status != StatusOK && (cur.Status != StatusSuperseded || cur.SupersededBy == ""):
			lost = true
		case cur.Status == StatusOK:
			toMark = append(toMark, tgt.ID)
		}
	}
	s.mu.RUnlock()
	if lost {
		s.mu.Lock()
		s.markObservationUnavailableLocked(in.obs)
		s.mu.Unlock()
		return nil, false, fmt.Errorf("%w: observation %s: a recorded target is lost", core.ErrDegraded, in.obs)
	}

	var marked []core.ToolUseID
	var recorded bool
	if !recordPresent {
		var err error
		if marked, recorded, err = s.recordSupersedingCore(ctx, in.rec, toMark); err != nil {
			return nil, false, err
		}
	} else if len(toMark) > 0 {
		var err error
		if marked, err = s.completeSupersedeMarks(ctx, in.rec.ID, toMark); err != nil {
			return nil, false, err
		}
	}

	if err := s.SyncPublication(ctx, in.rec.Root); err != nil {
		return nil, false, fmt.Errorf("%w: observation %s: publication sync failed: %v", core.ErrDegraded, in.obs, err)
	}

	s.mu.Lock()
	published, stillLost := s.observationStateLocked(in)
	switch {
	case stillLost:
		s.markObservationUnavailableLocked(in.obs)
		s.mu.Unlock()
		return nil, false, fmt.Errorf("%w: observation %s: a recorded target is lost", core.ErrDegraded, in.obs)
	case published:
		if b := s.obsBindings[in.obs]; b != nil {
			b.committed = true
		}
	}
	s.mu.Unlock()
	if !published {
		return nil, false, fmt.Errorf("%w: observation %s could not be completed", core.ErrDegraded, in.obs)
	}
	return marked, recorded, nil
}

// RecoverToolUseByObservation resumes obs's stored intent and completes its publication, returning the
// record once durable. Ambiguous / unavailable win over a stale committed binding; an unknown
// observation is a miss, except while the sidecar is uncertain, when it is unavailable, not absent.
func (s *FSStore) RecoverToolUseByObservation(ctx context.Context, obs core.ObservationID) (ToolUseRecord, error) {
	if err := s.mutate(); err != nil {
		return ToolUseRecord{}, err
	}
	if err := ctx.Err(); err != nil {
		return ToolUseRecord{}, err
	}
	if !validObservationID(obs) {
		return ToolUseRecord{}, fmt.Errorf("%w: observation %s", core.ErrNotFound, obs)
	}

	s.obsPubMu.Lock()
	defer s.obsPubMu.Unlock()
	if err := s.mutate(); err != nil {
		return ToolUseRecord{}, err
	}

	s.mu.RLock()
	_, ambiguous := s.obsAmbiguous[obs]
	_, unavailable := s.obsUnavailable[obs]
	uncertain := s.obsSidecarUncertain
	known := s.observationKnownLocked(obs)
	var b *obsBinding
	if got, ok := s.obsBindings[obs]; ok {
		clone := *got
		b = &clone
	}
	s.mu.RUnlock()

	switch {
	case ambiguous:
		return ToolUseRecord{}, fmt.Errorf("%w: observation %s is bound to more than one record", core.ErrDegraded, obs)
	case unavailable:
		return ToolUseRecord{}, fmt.Errorf("%w: observation %s intent is unavailable", core.ErrDegraded, obs)
	case b != nil:
		if _, _, err := s.completeIntentLocked(ctx, b.intent); err != nil {
			return ToolUseRecord{}, err
		}
		return s.ToolUse(ctx, b.intent.rec.ID)
	case uncertain && !known:
		return ToolUseRecord{}, fmt.Errorf("%w: observation %s: sidecar completeness unproved", core.ErrDegraded, obs)
	default:
		return ToolUseRecord{}, fmt.Errorf("%w: observation %s", core.ErrNotFound, obs)
	}
}
