package daemon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/scheduler"
)

// The scheduler runtime's two state files, both under <root>/.qompack/state/ (00-ARCHITECTURE
// §3.3: daemon-persisted, NOT append-only, so paths.WriteAtomic replaces them whole and
// paths.AppendOnly is never used here). Both are session-scoped: they carry the bound session id
// and a document from another session is discarded on load — carrying a posterior, changepoint
// turns, frontier or EWMAs across sessions would be the cross-session warm start (O4) the plan's
// out-of-scope table assigns to SP-16.
const (
	stateVersion       = 1
	stateFileBOCD      = "bocd.json"
	stateFileScheduler = "scheduler.json"
)

// The load-path log lines, fixed so tests can assert on them.
const (
	msgStateOtherSession      = "scheduler: state from another session discarded"
	msgStateModelShapeChanged = "scheduler: detector state discarded, model shape changed"
	msgStateUnreadable        = "scheduler state unreadable, restarting detector"
	msgStateRepaired          = "scheduler: persisted state out of range, repaired on load"
)

// bocdStateDoc is state/bocd.json. State is the standard-base64 encoding of
// Detector.MarshalBinary(); hazard_rate and features pin the model shape the posterior was
// computed under, and a mismatch with the running config discards it.
type bocdStateDoc struct {
	Version    int            `json:"version"`
	Session    core.SessionID `json:"session"`
	Updated    core.UnixMilli `json:"updated"`
	HazardRate float64        `json:"hazard_rate"`
	Features   []string       `json:"features"`
	State      string         `json:"state"`
}

// decisionDoc is the persisted shape of one scheduler.Decision. The p_* fields exist only on a
// decision that fired (Evaluate fills P in its default branch, so a should_compact:false document
// carries a zero P and omits them); every other field is written unconditionally.
type decisionDoc struct {
	ShouldCompact     bool                       `json:"should_compact"`
	Reasons           []scheduler.TriggerReason  `json:"reasons"`
	PPos              int                        `json:"p_pos,omitempty"`
	PTurn             core.TurnIndex             `json:"p_turn,omitempty"`
	PSegment          core.SegmentID             `json:"p_segment,omitempty"`
	PRoundBoundary    bool                       `json:"p_round_boundary,omitempty"`
	PReclaimable      core.Tokens                `json:"p_reclaimable_tokens,omitempty"`
	PCoupling         int                        `json:"p_coupling,omitempty"`
	PScore            float64                    `json:"p_score,omitempty"`
	Urgency           scheduler.Urgency          `json:"urgency"`
	TTL               scheduler.TTLState         `json:"ttl"`
	YoungDalySeconds  float64                    `json:"young_daly_seconds"`
	SoftFloorTokens   core.Tokens                `json:"soft_floor_tokens"`
	HardCeilingTokens core.Tokens                `json:"hard_ceiling_tokens"`
	Background        []scheduler.BackgroundTask `json:"background,omitempty"`
	Breakdown         map[string]float64         `json:"breakdown"`
}

// schedulerStateDoc is state/scheduler.json: the plan's fields plus regime_source (the seat
// contract), and the additive crash-recovery fields last_request_start_ts, max_turn,
// open_segment_tokens and last_checkpoint_seq. Unknown keys are ignored on load (forward
// compatibility, matching the config loader's posture).
type schedulerStateDoc struct {
	Version          int            `json:"version"`
	Session          core.SessionID `json:"session"`
	Updated          core.UnixMilli `json:"updated"`
	SessionStartTS   core.UnixMilli `json:"session_start_ts"`
	LastCompactionTS core.UnixMilli `json:"last_compaction_ts"`
	// LastLocalCheckpointTS is the CADENCE seal, kept apart from last_compaction_ts on disk for
	// the same reason it is kept apart in memory (see schedRuntime.lastLocalCheckpointTS). It is
	// omitempty and additive: a state file written before this key existed loads as zero, which is
	// exactly "no local checkpoint recorded", so the version stays 1.
	LastLocalCheckpointTS core.UnixMilli     `json:"last_local_checkpoint_ts,omitempty"`
	LastAPICallTS         core.UnixMilli     `json:"last_api_call_ts"`
	LastCacheWriteTS      core.UnixMilli     `json:"last_cache_write_ts"`
	LastRequestStartTS    core.UnixMilli     `json:"last_request_start_ts"`
	DeltaEWMASeconds      float64            `json:"delta_ewma_seconds"`
	DeltaSamples          int                `json:"delta_samples"`
	BurnEWMATokensPerMin  float64            `json:"burn_ewma_tokens_per_min"`
	BurnSamples           int                `json:"burn_samples"`
	EffectiveWindow       core.Tokens        `json:"effective_window"`
	WindowSource          float64            `json:"window_source"`
	RegimeSource          string             `json:"regime_source"`
	ChangepointTurns      []core.TurnIndex   `json:"changepoint_turns"`
	RoundTurns            []core.TurnIndex   `json:"round_turns"`
	MaxTurn               core.TurnIndex     `json:"max_turn"`
	OpenSegmentTokens     core.Tokens        `json:"open_segment_tokens"`
	FrontierTurn          core.TurnIndex     `json:"frontier_turn"`
	ResidualTokens        core.Tokens        `json:"residual_tokens"`
	LastCheckpointSeq     core.CheckpointSeq `json:"last_checkpoint_seq"`
	LastDecision          decisionDoc        `json:"last_decision"`
	// LastAppliedObservation is the observation identity of the last delivery of this session the
	// tap applied to the account this document carries (schedRuntime.applied), written from the same
	// snapshot as the account, so the two always agree: a replay of that delivery after a restart is
	// already in open_segment_tokens and is not folded again. It is omitempty and additive, like
	// last_local_checkpoint_ts: a document written before it existed loads with no identity, which is
	// exactly the behaviour before it existed, so the version stays 1.
	LastAppliedObservation core.ObservationID `json:"last_applied_observation,omitempty"`
}

// stateFiles is one Persist's encoded payload: built under the runtime lock, written without it.
type stateFiles struct {
	bocdPath, schedulerPath string
	bocd, scheduler         []byte
}

// encodeBOCDState renders a bocd.json document.
func encodeBOCDState(doc bocdStateDoc) ([]byte, error) {
	return json.MarshalIndent(doc, "", "  ")
}

// decodeBOCDState parses a bocd.json document, rejecting an unknown version.
func decodeBOCDState(b []byte) (bocdStateDoc, error) {
	var doc bocdStateDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return bocdStateDoc{}, fmt.Errorf("%s: %w", stateFileBOCD, err)
	}
	if doc.Version != stateVersion {
		return bocdStateDoc{}, fmt.Errorf("%s: unsupported version %d", stateFileBOCD, doc.Version)
	}
	return doc, nil
}

// encodeSchedulerState renders a scheduler.json document with both turn lists capped at
// maxTurnHistory entries (oldest dropped). The caller's slices are not modified.
func encodeSchedulerState(doc schedulerStateDoc) ([]byte, error) {
	doc.ChangepointTurns = capTurns(doc.ChangepointTurns)
	doc.RoundTurns = capTurns(doc.RoundTurns)
	return json.MarshalIndent(doc, "", "  ")
}

// decodeSchedulerState parses a scheduler.json document, rejecting an unknown version and
// re-applying the turn-list cap so an oversized document from an older writer is bounded too.
func decodeSchedulerState(b []byte) (schedulerStateDoc, error) {
	var doc schedulerStateDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return schedulerStateDoc{}, fmt.Errorf("%s: %w", stateFileScheduler, err)
	}
	if doc.Version != stateVersion {
		return schedulerStateDoc{}, fmt.Errorf("%s: unsupported version %d", stateFileScheduler, doc.Version)
	}
	doc.ChangepointTurns = capTurns(doc.ChangepointTurns)
	doc.RoundTurns = capTurns(doc.RoundTurns)
	return doc, nil
}

// capTurns keeps the newest maxTurnHistory entries of an ascending turn list. It slices rather
// than copies, so the caller's backing array is shared but never modified.
func capTurns(ts []core.TurnIndex) []core.TurnIndex {
	if len(ts) <= maxTurnHistory {
		return ts
	}
	return ts[len(ts)-maxTurnHistory:]
}

// decisionToDoc flattens a Decision into its persisted shape.
func decisionToDoc(d scheduler.Decision) decisionDoc {
	return decisionDoc{
		ShouldCompact:     d.ShouldCompact,
		Reasons:           d.Reasons,
		PPos:              d.P.Pos,
		PTurn:             d.P.Turn,
		PSegment:          d.P.SegmentID,
		PRoundBoundary:    d.P.RoundBoundary,
		PReclaimable:      d.P.ReclaimableTokens,
		PCoupling:         d.P.Coupling,
		PScore:            d.PScore,
		Urgency:           d.Urgency,
		TTL:               d.TTL,
		YoungDalySeconds:  d.YoungDalySeconds,
		SoftFloorTokens:   d.SoftFloorTokens,
		HardCeilingTokens: d.HardCeilingTokens,
		Background:        d.Background,
		Breakdown:         d.Breakdown,
	}
}

// docToDecision is decisionToDoc's inverse.
func docToDecision(doc decisionDoc) scheduler.Decision {
	return scheduler.Decision{
		ShouldCompact: doc.ShouldCompact,
		Reasons:       doc.Reasons,
		P: scheduler.Candidate{
			Pos: doc.PPos, Turn: doc.PTurn, SegmentID: doc.PSegment, RoundBoundary: doc.PRoundBoundary,
			ReclaimableTokens: doc.PReclaimable, Coupling: doc.PCoupling,
		},
		PScore:            doc.PScore,
		Breakdown:         doc.Breakdown,
		Urgency:           doc.Urgency,
		TTL:               doc.TTL,
		YoungDalySeconds:  doc.YoungDalySeconds,
		Background:        doc.Background,
		SoftFloorTokens:   doc.SoftFloorTokens,
		HardCeilingTokens: doc.HardCeilingTokens,
	}
}

// statePaths returns the two file paths for this runtime's project root.
func (r *schedRuntime) statePaths() (bocd, sched string) {
	dir := paths.Of(r.root).State
	return filepath.Join(dir, stateFileBOCD), filepath.Join(dir, stateFileScheduler)
}

// saveStateLocked encodes both documents from the current state. It assumes r.mu is held and
// touches no file: Persist writes the result after releasing the lock.
func (r *schedRuntime) saveStateLocked() (stateFiles, error) {
	raw, err := r.det.MarshalBinary()
	if err != nil {
		return stateFiles{}, fmt.Errorf("marshal detector: %w", err)
	}
	now := r.nowMS()
	bocd, err := encodeBOCDState(bocdStateDoc{
		Version:    stateVersion,
		Session:    r.session,
		Updated:    now,
		HazardRate: r.conf().Scheduler.Changepoint.HazardRate,
		Features:   r.conf().Scheduler.Changepoint.Features,
		State:      base64.StdEncoding.EncodeToString(raw),
	})
	if err != nil {
		return stateFiles{}, fmt.Errorf("encode %s: %w", stateFileBOCD, err)
	}
	rounds := make([]core.TurnIndex, 0, len(r.rounds))
	for t := range r.rounds {
		rounds = append(rounds, t)
	}
	slices.Sort(rounds)
	sched, err := encodeSchedulerState(schedulerStateDoc{
		Version:               stateVersion,
		Session:               r.session,
		Updated:               now,
		SessionStartTS:        r.sessionStartTS,
		LastCompactionTS:      r.lastCompactionTS,
		LastLocalCheckpointTS: r.lastLocalCheckpointTS,
		LastAPICallTS:         r.lastAPICallTS,
		LastCacheWriteTS:      r.lastCacheWriteTS,
		LastRequestStartTS:    r.lastRequestStartTS,
		DeltaEWMASeconds:      r.deltaEWMA,
		DeltaSamples:          r.deltaSamples,
		BurnEWMATokensPerMin:  r.burnEWMA,
		BurnSamples:           r.burnSamples,
		EffectiveWindow:       r.effectiveWindow,
		WindowSource:          r.windowSource,
		RegimeSource:          r.regime.Source,
		ChangepointTurns:      slices.Clone(r.cpTurns),
		RoundTurns:            rounds,
		MaxTurn:               r.maxTurn,
		OpenSegmentTokens:     r.openSegTokens,
		FrontierTurn:          r.frontier,
		ResidualTokens:        r.residual,
		LastCheckpointSeq:     r.lastCheckpointSeq,
		LastDecision:          decisionToDoc(r.lastDecision),

		LastAppliedObservation: r.applied[r.session].obs,
	})
	if err != nil {
		return stateFiles{}, fmt.Errorf("encode %s: %w", stateFileScheduler, err)
	}
	bp, sp := r.statePaths()
	return stateFiles{bocdPath: bp, schedulerPath: sp, bocd: bocd, scheduler: sched}, nil
}

// loadStateLocked restores both files for the bound session, if present. It assumes r.mu is
// held and the session-scoped fields have just been reset. A missing file is the normal first
// start; a document from another session is discarded (Info); a detector document whose
// hazard_rate/features differ from the running config is discarded (Warn, both shapes logged);
// an unreadable document — JSON, base64, or the detector's own CRC — is discarded (Loud). Nothing
// is ever deleted: the store is untouched, so no data is lost, and the next Persist overwrites.
func (r *schedRuntime) loadStateLocked() {
	bp, sp := r.statePaths()
	if raw, ok := r.loadStateFile(bp); ok {
		r.restoreBOCDLocked(raw, bp)
	}
	if raw, ok := r.loadStateFile(sp); ok {
		r.restoreSchedulerLocked(raw, sp)
	}
}

// readStateFile reads one of the two state files.
//
// The read is shared (paths.ReadFileShared). BindSession reads under r.mu, while Persist writes
// both files with paths.WriteAtomic under persistMu only, after releasing r.mu, so an idle-tick
// persist and another session's bind are not ordered; on Windows an ordinary handle would fail that
// replace and be refused while one is finishing (test/guards' sharedReaders). seedApplied reads at
// construction, before any persist can run, and takes the same handle all the same.
func readStateFile(p string) ([]byte, error) {
	return paths.ReadFileShared(p)
}

// seedApplied records the applied identity state/scheduler.json carries for its session, for a
// runtime constructed unbound: the restarted daemon's startup drain replays what its predecessor
// left before any hook binds the runtime, and a delivery the predecessor applied and persisted but
// never committed must be recognized there, or the unbound runtime folds it into what it hands the
// bind (bindUnboundLocked) on top of the restored account that already holds it. It binds nothing
// and logs nothing: a missing document is a first start, and an unreadable or malformed one is
// reported by the bind that reads it (loadStateLocked).
func (r *schedRuntime) seedApplied() {
	_, sp := r.statePaths()
	raw, err := readStateFile(sp)
	if err != nil {
		return
	}
	doc, err := decodeSchedulerState(raw)
	if err != nil || doc.Session == "" || doc.LastAppliedObservation == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applied[doc.Session] = appliedDelivery{obs: doc.LastAppliedObservation}
}

// loadStateFile reads p for a bind, reporting false (and logging) when there is nothing usable.
func (r *schedRuntime) loadStateFile(p string) ([]byte, bool) {
	raw, err := readStateFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			r.log.Debug("scheduler: no persisted state", "path", p)
		} else {
			r.log.Loud(msgStateUnreadable, "path", p, "err", err.Error())
		}
		return nil, false
	}
	return raw, true
}

// restoreBOCDLocked applies a bocd.json document under the three discard rules.
func (r *schedRuntime) restoreBOCDLocked(raw []byte, p string) {
	doc, err := decodeBOCDState(raw)
	if err != nil {
		r.log.Loud(msgStateUnreadable, "path", p, "err", err.Error())
		return
	}
	if doc.Session != r.session {
		r.log.Info(msgStateOtherSession, "path", p, "file_session", string(doc.Session), "session", string(r.session))
		return
	}
	cp := r.conf().Scheduler.Changepoint
	if doc.HazardRate != cp.HazardRate || !slices.Equal(doc.Features, cp.Features) {
		r.log.Warn(msgStateModelShapeChanged, "path", p,
			"file_hazard_rate", doc.HazardRate, "hazard_rate", cp.HazardRate,
			"file_features", doc.Features, "features", cp.Features)
		return
	}
	payload, err := base64.StdEncoding.DecodeString(doc.State)
	if err != nil {
		r.log.Loud(msgStateUnreadable, "path", p, "err", err.Error())
		return
	}
	det := scheduler.NewBOCD(cp.HazardRate, cp.Features)
	if err := det.UnmarshalBinary(payload); err != nil {
		r.log.Loud(msgStateUnreadable, "path", p, "err", err.Error())
		return
	}
	r.det = det
}

// restoreSchedulerLocked applies a scheduler.json document. The window and the cache regime are
// deliberately NOT restored: both are re-resolved from the environment at every bind, and the
// persisted values exist for /qompack:status, not for recovery.
func (r *schedRuntime) restoreSchedulerLocked(raw []byte, p string) {
	doc, err := decodeSchedulerState(raw)
	if err != nil {
		r.log.Loud(msgStateUnreadable, "path", p, "err", err.Error())
		return
	}
	if doc.Session != r.session {
		r.log.Info(msgStateOtherSession, "path", p, "file_session", string(doc.Session), "session", string(r.session))
		return
	}
	// Ruling R59: a document that parses is not a document that is sane. A stamp later than the
	// clock — a wall clock stepped back, a machine restored from a snapshot, a .qompack tree
	// copied between hosts, a hand-edited file — would make NotifyActivity's monotone guard
	// drop every real activity, hold IdleSince at "never idle", pin the TTL gap at 0 (always
	// warm) and keep the burn clock from starting until real time caught up with the stamp.
	// Every restored timestamp is therefore CLAMPED to now rather than dropped: the fact the
	// stamp carries ("recently active") survives, the anchors keep their ordering, and the
	// first activity after the restart — later than now — is accepted and restarts the clocks.
	// Negative counters are clamped to zero. An EWMA that is not finite, not positive, or
	// disagrees with its sample count is restored as unmeasured — the state a fresh session has
	// (deltaPtr answers nil, a zero burn disables mtbfSeconds) — because Evaluate would sanitise
	// the number to 0 anyway while /qompack:status would present it as a measurement. One Warn
	// per restore names what was repaired, at the level the neighbouring self-heal branches use.
	now := r.nowMS()
	var futureStamps, negativeCounters, unmeasuredEWMAs int
	clampTS := func(ts core.UnixMilli) core.UnixMilli {
		if ts > now {
			futureStamps++
			return now
		}
		return ts
	}
	if doc.SessionStartTS > 0 {
		r.sessionStartTS = clampTS(doc.SessionStartTS)
	}
	r.lastCompactionTS = clampTS(doc.LastCompactionTS)
	r.lastLocalCheckpointTS = clampTS(doc.LastLocalCheckpointTS)
	r.lastAPICallTS = clampTS(doc.LastAPICallTS)
	r.lastCacheWriteTS = clampTS(doc.LastCacheWriteTS)
	r.lastRequestStartTS = clampTS(doc.LastRequestStartTS)
	r.deltaEWMA, r.deltaSamples = restoredEWMA(doc.DeltaEWMASeconds, doc.DeltaSamples, &unmeasuredEWMAs)
	r.burnEWMA, r.burnSamples = restoredEWMA(doc.BurnEWMATokensPerMin, doc.BurnSamples, &unmeasuredEWMAs)
	r.cpTurns = r.cpTurns[:0]
	for _, t := range doc.ChangepointTurns {
		r.recordChangepointLocked(t)
	}
	r.rounds = make(map[core.TurnIndex]struct{}, len(doc.RoundTurns)+1)
	r.rounds[0] = struct{}{}
	for _, t := range doc.RoundTurns {
		r.rounds[t] = struct{}{}
	}
	if doc.MaxTurn < 0 {
		negativeCounters++
		doc.MaxTurn = 0
	}
	if doc.OpenSegmentTokens < 0 {
		negativeCounters++
		doc.OpenSegmentTokens = 0
	}
	if doc.FrontierTurn < 0 {
		negativeCounters++
		doc.FrontierTurn = 0
	}
	if doc.ResidualTokens < 0 {
		negativeCounters++
		doc.ResidualTokens = 0
	}
	r.maxTurn = doc.MaxTurn
	r.openSegTokens = doc.OpenSegmentTokens
	// contextTokens is NOT taken from the document: BindSession recomputes it from the segment
	// log (closed segments + this open accumulator) right after the load, and re-baselines the
	// burn clock on the result. lastActivity follows the restored (clamped) API-call anchor so
	// the monotone guard in NotifyActivity keeps protecting it after a restart.
	r.lastActivity = r.lastAPICallTS
	r.frontier = doc.FrontierTurn
	r.residual = doc.ResidualTokens
	r.lastCheckpointSeq = doc.LastCheckpointSeq
	r.lastDecision = docToDecision(doc.LastDecision)
	// The document's applied identity fills a session this runtime has none for. One it has is its
	// own, and newer: this process applied it after whatever process wrote the document.
	if d := r.applied[r.session]; d.obs == "" && doc.LastAppliedObservation != "" {
		d.obs = doc.LastAppliedObservation
		r.applied[r.session] = d
	}
	if futureStamps+negativeCounters+unmeasuredEWMAs > 0 {
		r.log.Warn(msgStateRepaired, "path", p, "future_timestamps", futureStamps,
			"negative_counters", negativeCounters, "unmeasured_ewmas", unmeasuredEWMAs)
	}
}

// restoredEWMA returns (v, samples) when the pair describes a measurement — a finite positive
// value with a positive sample count — or the unmeasured pair (0, 0) it was persisted as. Any
// other pair (a value with no samples, samples with no value, a negative count, NaN, ±Inf) is
// inconsistent: it is counted on *repaired and restores as unmeasured. Both EWMAs only ever
// fold positive samples (NotifyActivity, RecordCompactionCost, refreshDeltaTask), so a
// measured value is positive by construction.
func restoredEWMA(v float64, samples int, repaired *int) (float64, int) {
	measured := samples > 0 && v > 0 && !math.IsInf(v, 0)
	if measured || (samples == 0 && v == 0) {
		return v, samples
	}
	*repaired++
	return 0, 0
}
