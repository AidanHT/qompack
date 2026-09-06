package daemon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	Version              int                `json:"version"`
	Session              core.SessionID     `json:"session"`
	Updated              core.UnixMilli     `json:"updated"`
	SessionStartTS       core.UnixMilli     `json:"session_start_ts"`
	LastCompactionTS     core.UnixMilli     `json:"last_compaction_ts"`
	LastAPICallTS        core.UnixMilli     `json:"last_api_call_ts"`
	LastCacheWriteTS     core.UnixMilli     `json:"last_cache_write_ts"`
	LastRequestStartTS   core.UnixMilli     `json:"last_request_start_ts"`
	DeltaEWMASeconds     float64            `json:"delta_ewma_seconds"`
	DeltaSamples         int                `json:"delta_samples"`
	BurnEWMATokensPerMin float64            `json:"burn_ewma_tokens_per_min"`
	BurnSamples          int                `json:"burn_samples"`
	EffectiveWindow      core.Tokens        `json:"effective_window"`
	WindowSource         float64            `json:"window_source"`
	RegimeSource         string             `json:"regime_source"`
	ChangepointTurns     []core.TurnIndex   `json:"changepoint_turns"`
	RoundTurns           []core.TurnIndex   `json:"round_turns"`
	MaxTurn              core.TurnIndex     `json:"max_turn"`
	OpenSegmentTokens    core.Tokens        `json:"open_segment_tokens"`
	FrontierTurn         core.TurnIndex     `json:"frontier_turn"`
	ResidualTokens       core.Tokens        `json:"residual_tokens"`
	LastCheckpointSeq    core.CheckpointSeq `json:"last_checkpoint_seq"`
	LastDecision         decisionDoc        `json:"last_decision"`
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
		HazardRate: r.cfg.Scheduler.Changepoint.HazardRate,
		Features:   r.cfg.Scheduler.Changepoint.Features,
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
		Version:              stateVersion,
		Session:              r.session,
		Updated:              now,
		SessionStartTS:       r.sessionStartTS,
		LastCompactionTS:     r.lastCompactionTS,
		LastAPICallTS:        r.lastAPICallTS,
		LastCacheWriteTS:     r.lastCacheWriteTS,
		LastRequestStartTS:   r.lastRequestStartTS,
		DeltaEWMASeconds:     r.deltaEWMA,
		DeltaSamples:         r.deltaSamples,
		BurnEWMATokensPerMin: r.burnEWMA,
		BurnSamples:          r.burnSamples,
		EffectiveWindow:      r.effectiveWindow,
		WindowSource:         r.windowSource,
		RegimeSource:         r.regime.Source,
		ChangepointTurns:     slices.Clone(r.cpTurns),
		RoundTurns:           rounds,
		MaxTurn:              r.maxTurn,
		OpenSegmentTokens:    r.openSegTokens,
		FrontierTurn:         r.frontier,
		ResidualTokens:       r.residual,
		LastCheckpointSeq:    r.lastCheckpointSeq,
		LastDecision:         decisionToDoc(r.lastDecision),
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
	if raw, ok := r.readStateFile(bp); ok {
		r.restoreBOCDLocked(raw, bp)
	}
	if raw, ok := r.readStateFile(sp); ok {
		r.restoreSchedulerLocked(raw, sp)
	}
}

// readStateFile reads p, reporting false (and logging) when there is nothing usable.
func (r *schedRuntime) readStateFile(p string) ([]byte, bool) {
	raw, err := os.ReadFile(paths.Long(p))
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
	cp := r.cfg.Scheduler.Changepoint
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
	if doc.SessionStartTS > 0 {
		r.sessionStartTS = doc.SessionStartTS
	}
	r.lastCompactionTS = doc.LastCompactionTS
	r.lastAPICallTS = doc.LastAPICallTS
	r.lastCacheWriteTS = doc.LastCacheWriteTS
	r.lastRequestStartTS = doc.LastRequestStartTS
	r.deltaEWMA, r.deltaSamples = doc.DeltaEWMASeconds, doc.DeltaSamples
	r.burnEWMA, r.burnSamples = doc.BurnEWMATokensPerMin, doc.BurnSamples
	r.cpTurns = r.cpTurns[:0]
	for _, t := range doc.ChangepointTurns {
		r.recordChangepointLocked(t)
	}
	r.rounds = make(map[core.TurnIndex]struct{}, len(doc.RoundTurns)+1)
	r.rounds[0] = struct{}{}
	for _, t := range doc.RoundTurns {
		r.rounds[t] = struct{}{}
	}
	r.maxTurn = doc.MaxTurn
	r.openSegTokens = doc.OpenSegmentTokens
	// contextTokens is NOT taken from the document: BindSession recomputes it from the segment
	// log (closed segments + this open accumulator) right after the load, and re-baselines the
	// burn clock on the result. lastActivity follows the restored API-call anchor so the
	// monotone guard in NotifyActivity keeps protecting it after a restart.
	r.lastActivity = doc.LastAPICallTS
	r.frontier = doc.FrontierTurn
	r.residual = doc.ResidualTokens
	r.lastCheckpointSeq = doc.LastCheckpointSeq
	r.lastDecision = docToDecision(doc.LastDecision)
}
