package daemon

// The plan's five scheduler_state_test.go cases. TestStateCodec_SchedulerRoundTrip carries the
// plan's own state/scheduler.json document and re-derives it through scheduler.Evaluate — the
// consistency proof V4-VERIFY row V4-SP12-16 cites — rather than merely re-reading it.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/scheduler"
)

func TestStateCodec_BOCDRoundTrip(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults().Scheduler.Changepoint
	det := scheduler.NewBOCD(cfg.HazardRate, cfg.Features)
	for _, f := range rtStepSeries(60, rtStepShift) {
		det.Observe(f)
	}
	raw, err := det.MarshalBinary()
	require.NoError(t, err)

	doc := bocdStateDoc{
		Version: stateVersion, Session: "sess-7f3a", Updated: 1_730_000_000_000,
		HazardRate: cfg.HazardRate, Features: cfg.Features, State: base64.StdEncoding.EncodeToString(raw),
	}
	b, err := encodeBOCDState(doc)
	require.NoError(t, err)
	p := filepath.Join(t.TempDir(), ".qompack", "state", stateFileBOCD)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, b, 0o600))

	read, err := os.ReadFile(p)
	require.NoError(t, err)
	got, err := decodeBOCDState(read)
	require.NoError(t, err)
	require.Equal(t, doc, got)
	require.Equal(t, cfg.HazardRate, got.HazardRate)
	require.Equal(t, cfg.Features, got.Features)
	require.Equal(t, core.SessionID("sess-7f3a"), got.Session)

	payload, err := base64.StdEncoding.DecodeString(got.State)
	require.NoError(t, err)
	require.Equal(t, raw, payload, "the state field decodes to byte-identical MarshalBinary output")
	restored := scheduler.NewBOCD(cfg.HazardRate, cfg.Features)
	require.NoError(t, restored.UnmarshalBinary(payload))
	require.Equal(t, det.State(), restored.State())
}

// planSchedulerStateDoc is the document printed in the plan's scheduler_state.go section, with
// the three keys the seat contract adds (regime_source, window_source, effective_window — the
// last two were already present) and the breakdown abridged to the four keys the arithmetic is
// checked against.
const planSchedulerStateDoc = `{
  "version": 1,
  "session": "sess-7f3a",
  "updated": 1730000000000,
  "session_start_ts": 1729996400000,
  "last_compaction_ts": 1729999000000,
  "last_api_call_ts": 1730000000000,
  "last_cache_write_ts": 1729999985000,
  "delta_ewma_seconds": 20.0,
  "delta_samples": 3,
  "burn_ewma_tokens_per_min": 800.0,
  "burn_samples": 41,
  "effective_window": 180000,
  "window_source": 1,
  "regime_source": "force_5m",
  "changepoint_turns": [0, 14, 33],
  "round_turns": [0, 3, 7, 14, 19, 33],
  "frontier_turn": 33,
  "residual_tokens": 9120,
  "last_decision": {
    "should_compact": true,
    "reasons": ["soft_floor", "young_daly"],
    "p_pos": 118230,
    "p_turn": 33,
    "p_score": -5379.3,
    "urgency": 1,
    "ttl": "warm",
    "young_daly_seconds": 268.3281573,
    "soft_floor_tokens": 99000,
    "hard_ceiling_tokens": 147000,
    "breakdown": { "context_tokens": 123000, "reclaimable": 600, "rewrite": 5962.5, "distortion": 16.8 }
  }
}`

func TestStateCodec_SchedulerRoundTrip(t *testing.T) {
	t.Parallel()
	doc, err := decodeSchedulerState([]byte(planSchedulerStateDoc))
	require.NoError(t, err)

	// Every field round-trips: decode → encode → decode is the identity, and the values are the
	// document's.
	b, err := encodeSchedulerState(doc)
	require.NoError(t, err)
	again, err := decodeSchedulerState(b)
	require.NoError(t, err)
	require.Equal(t, doc, again)
	require.Equal(t, stateVersion, doc.Version)
	require.Equal(t, core.SessionID("sess-7f3a"), doc.Session)
	require.Equal(t, core.UnixMilli(1_730_000_000_000), doc.Updated)
	require.Equal(t, core.UnixMilli(1_729_996_400_000), doc.SessionStartTS)
	require.Equal(t, core.UnixMilli(1_729_999_000_000), doc.LastCompactionTS)
	require.Equal(t, core.UnixMilli(1_730_000_000_000), doc.LastAPICallTS)
	require.Equal(t, core.UnixMilli(1_729_999_985_000), doc.LastCacheWriteTS)
	require.Equal(t, 20.0, doc.DeltaEWMASeconds)
	require.Equal(t, 3, doc.DeltaSamples)
	require.Equal(t, 800.0, doc.BurnEWMATokensPerMin)
	require.Equal(t, 41, doc.BurnSamples)
	require.Equal(t, core.Tokens(180_000), doc.EffectiveWindow)
	require.Equal(t, 1.0, doc.WindowSource)
	require.Equal(t, "force_5m", doc.RegimeSource)
	require.Equal(t, []core.TurnIndex{0, 14, 33}, doc.ChangepointTurns)
	require.Equal(t, []core.TurnIndex{0, 3, 7, 14, 19, 33}, doc.RoundTurns)
	require.Equal(t, core.TurnIndex(33), doc.FrontierTurn)
	require.Equal(t, core.Tokens(9_120), doc.ResidualTokens)
	ld := doc.LastDecision
	require.True(t, ld.ShouldCompact)
	require.Equal(t, []scheduler.TriggerReason{scheduler.TriggerSoftFloor, scheduler.TriggerYoungDaly}, ld.Reasons)
	require.Equal(t, 118_230, ld.PPos)
	require.Equal(t, core.TurnIndex(33), ld.PTurn)
	require.Equal(t, -5379.3, ld.PScore)
	require.Equal(t, scheduler.UrgencyAdvisory, ld.Urgency)
	require.Equal(t, scheduler.TTLWarm, ld.TTL)
	require.Equal(t, 268.3281573, ld.YoungDalySeconds)
	require.Equal(t, core.Tokens(99_000), ld.SoftFloorTokens)
	require.Equal(t, core.Tokens(147_000), ld.HardCeilingTokens)
	require.Equal(t, map[string]float64{"context_tokens": 123_000, "reclaimable": 600, "rewrite": 5962.5, "distortion": 16.8}, ld.Breakdown)

	// And the document is re-derived, not just re-read: its own fields fed back through the pure
	// scheduler.Evaluate reproduce it. The document assumes the KNOWN five-minute regime (w 1.25,
	// r 0.1 from Appendix C) and λ 0.4, so the Inputs pin Regime to that regime built from cfg.
	cfg := config.Defaults()
	delta := doc.DeltaEWMASeconds
	in := scheduler.Inputs{
		Now:                  doc.Updated,
		ContextTokens:        core.Tokens(ld.Breakdown["context_tokens"]),
		EffectiveWindow:      doc.EffectiveWindow,
		MaxOutputTokens:      scheduler.HostDefaultMaxOutput,
		LastAPICallTS:        doc.LastAPICallTS,
		LastCacheWriteTS:     doc.LastCacheWriteTS,
		BurnRateTokensPerMin: doc.BurnEWMATokensPerMin,
		MeasuredDeltaSeconds: &delta,
		Candidates: []scheduler.Candidate{{
			Pos: ld.PPos, Turn: ld.PTurn, RoundBoundary: true,
			ReclaimableTokens: core.Tokens(ld.Breakdown["reclaimable"] / cfg.Scheduler.Cache.ReadMultiplier),
			Coupling:          int(ld.Breakdown["distortion"] / cfg.Selection.Submodular.Lambda),
		}},
		FrontierTurn:            doc.FrontierTurn,
		ResidualTokens:          doc.ResidualTokens,
		Cfg:                     cfg.Scheduler,
		LastCompactionTS:        doc.LastCompactionTS,
		CouplingLambda:          cfg.Selection.Submodular.Lambda,
		Regime:                  knownRegimeFor(cfg.Scheduler, doc.RegimeSource),
		ExpiringTriggerFraction: cfg.Runtime.Scheduler.Cache.ExpiringTriggerFraction,
		AssumeMaxTTLSeconds:     cfg.Runtime.Scheduler.Cache.AssumeMaxTTLSeconds,
	}
	require.Equal(t, core.Tokens(6_000), in.Candidates[0].ReclaimableTokens)
	require.Equal(t, 42, in.Candidates[0].Coupling)

	d := scheduler.Evaluate(in)
	require.Equal(t, ld.Reasons, d.Reasons)
	require.True(t, d.ShouldCompact)
	require.Equal(t, ld.Urgency, d.Urgency)
	require.Equal(t, ld.TTL, d.TTL)
	require.InDelta(t, ld.YoungDalySeconds, d.YoungDalySeconds, 1e-6)
	require.Equal(t, ld.SoftFloorTokens, d.SoftFloorTokens)
	require.Equal(t, ld.HardCeilingTokens, d.HardCeilingTokens)
	require.Equal(t, ld.PPos, d.P.Pos)
	require.Equal(t, ld.PTurn, d.P.Turn)
	require.InDelta(t, ld.PScore, d.PScore, 1e-9)
	require.Equal(t, d.Breakdown["reclaimable"]-d.Breakdown["rewrite"]-d.Breakdown["distortion"], d.PScore)
	for k, v := range ld.Breakdown {
		require.InDelta(t, v, d.Breakdown[k], 1e-9, "breakdown[%s]", k)
	}

	// The document also survives the runtime's own decision codec both ways.
	require.Equal(t, ld, decisionToDoc(docToDecision(ld)))
}

// knownRegimeFor is the known five-minute regime a persisted regime_source of force_5m names,
// built from cfg exactly as scheduler.ResolveCacheRegime builds it (never from literals).
func knownRegimeFor(cfg config.SchedulerCfg, source string) scheduler.CacheRegime {
	return scheduler.CacheRegime{
		TTLMinSeconds:   cfg.Cache.TTLSeconds,
		TTLMaxSeconds:   cfg.Cache.TTLSeconds,
		ReadMultiplier:  cfg.Cache.ReadMultiplier,
		WriteMultiplier: cfg.Cache.WriteMultiplier,
		Source:          source,
	}
}

func TestStateCodec_UnknownFieldsIgnored(t *testing.T) {
	t.Parallel()
	a := newRTFixture(t)
	a.bind(rtSession)
	rtExercise(t, a)
	require.NoError(t, a.rt.Persist(context.Background()))

	addFutureKey := func(p string) {
		raw, err := os.ReadFile(p)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		m["future_key"] = 1
		out, err := json.Marshal(m)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, out, 0o600))
	}
	addFutureKey(a.statePath(stateFileBOCD))
	addFutureKey(a.statePath(stateFileScheduler))

	b := newRTFixture(t, withRoot(a))
	b.bind(rtSession)
	require.Equal(t, a.rt.det.State(), b.rt.det.State(), "the state loaded despite the unknown key")
	require.Equal(t, a.rt.cpTurns, b.rt.cpTurns)
	require.Equal(t, a.rt.deltaEWMA, b.rt.deltaEWMA)
	require.Zero(t, b.log.count(logWarn))
	require.Zero(t, b.log.count(logError))
	require.Zero(t, b.log.count(logLoud))
	require.NotContains(t, b.log.msgs(logInfo), msgStateOtherSession)
}

func TestStateCodec_WritesAtomicallyNotAppendOnly(t *testing.T) {
	t.Parallel()
	fx := newRTFixture(t)
	fx.bind(rtSession)
	ctx := context.Background()
	tmpDir := filepath.Join(fx.root, ".qompack", "tmp")
	_, err := os.Stat(tmpDir)
	require.True(t, os.IsNotExist(err), "the fixture root starts without a staging directory")

	fx.rt.NoteAPIRound(3)
	require.NoError(t, fx.rt.Persist(ctx))
	first, err := os.ReadFile(fx.statePath(stateFileScheduler))
	require.NoError(t, err)
	firstBOCD, err := os.ReadFile(fx.statePath(stateFileBOCD))
	require.NoError(t, err)

	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err, "WriteAtomic stages under .qompack/tmp, which the first write creates")
	require.Empty(t, entries, "every temp file was renamed onto its target")

	fx.rt.NoteAPIRound(9)
	fx.feed(rtStepSeries(8, 0))
	require.NoError(t, fx.rt.Persist(ctx))
	second, err := os.ReadFile(fx.statePath(stateFileScheduler))
	require.NoError(t, err)
	secondBOCD, err := os.ReadFile(fx.statePath(stateFileBOCD))
	require.NoError(t, err)

	require.NotEqual(t, first, second)
	require.NotEqual(t, firstBOCD, secondBOCD)
	require.False(t, bytes.HasPrefix(second, first), "the second write replaced the first rather than appending to it")
	for _, raw := range [][]byte{second, secondBOCD} {
		dec := json.NewDecoder(bytes.NewReader(raw))
		var v map[string]any
		require.NoError(t, dec.Decode(&v))
		require.False(t, dec.More(), "exactly one JSON document per file")
	}
	doc, err := decodeSchedulerState(second)
	require.NoError(t, err)
	require.Equal(t, []core.TurnIndex{0, 3, 9}, doc.RoundTurns)
	entries, err = os.ReadDir(tmpDir)
	require.NoError(t, err)
	require.Empty(t, entries)

	// The codec and the persist path never reach for the append-only writer; Persist (in
	// scheduler_runtime.go) is the one place the files are written, and it uses WriteAtomic.
	for _, name := range []string{"scheduler_state.go", "scheduler_runtime.go"} {
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		require.NotContains(t, string(src), "paths.AppendOnly(", "%s: state/ is daemon-persisted, not append-only (00-ARCHITECTURE §3.3)", name)
		require.NotContains(t, string(src), "paths.AppendJSONL(", name)
	}
	runtimeSrc, err := os.ReadFile("scheduler_runtime.go")
	require.NoError(t, err)
	require.Contains(t, string(runtimeSrc), "paths.WriteAtomic(")
}

func TestStateCodec_TurnListsCapped(t *testing.T) {
	t.Parallel()
	const n = 10_000
	turns := make([]core.TurnIndex, n)
	for i := range turns {
		turns[i] = core.TurnIndex(i + 1)
	}
	doc := schedulerStateDoc{Version: stateVersion, Session: rtSession, ChangepointTurns: turns, RoundTurns: turns}
	b, err := encodeSchedulerState(doc)
	require.NoError(t, err)
	got, err := decodeSchedulerState(b)
	require.NoError(t, err)
	require.Len(t, got.ChangepointTurns, maxTurnHistory)
	require.Len(t, got.RoundTurns, maxTurnHistory)
	require.Equal(t, core.TurnIndex(n-maxTurnHistory+1), got.ChangepointTurns[0], "oldest dropped")
	require.Equal(t, core.TurnIndex(n), got.ChangepointTurns[maxTurnHistory-1])
	require.Equal(t, got.ChangepointTurns, got.RoundTurns)
	require.Equal(t, turns, doc.ChangepointTurns, "the caller's slice is not truncated in place")

	// The runtime's own save path applies the same cap to a map-backed round set.
	fx := newRTFixture(t)
	fx.bind(rtSession)
	for i := 1; i <= n; i++ {
		fx.rt.NoteAPIRound(core.TurnIndex(i))
	}
	fx.rt.mu.Lock()
	files, err := fx.rt.saveStateLocked()
	fx.rt.mu.Unlock()
	require.NoError(t, err)
	saved, err := decodeSchedulerState(files.scheduler)
	require.NoError(t, err)
	require.Len(t, saved.RoundTurns, maxTurnHistory)
	require.Equal(t, core.TurnIndex(n), saved.RoundTurns[maxTurnHistory-1], "round_turns is written ascending")
	require.True(t, strings.HasSuffix(files.schedulerPath, stateFileScheduler))
}

// TestLocalCheckpointIsNeverConflatedWithHostCompaction is §8.5's two-clause distinction held as
// an invariant of the runtime's own state: a checkpoint QOMPACK sealed on its cadence and a
// compaction the HOST performed are different events, recorded in different fields, under
// different counters, and they survive a persist/reload round trip apart.
//
// The consequence of conflating them is concrete: lastCompactionTS is what every Young-Daly
// cadence decision is measured from, so a cadence seal that moved it would restart the clock on
// our own action and suppress the next real trigger.
func TestLocalCheckpointIsNeverConflatedWithHostCompaction(t *testing.T) {
	fx := newRTFixture(t)
	fx.bind(rtSession)

	// BindSession seeds the host clock from the session start, so the baseline is not zero; what
	// matters is that a cadence seal does not MOVE it.
	fx.rt.mu.Lock()
	baseline := fx.rt.lastCompactionTS
	fx.rt.mu.Unlock()
	require.NotZero(t, baseline)
	fx.clock.Advance(30 * time.Second)

	fx.rt.NoteLocalCheckpoint(7)
	fx.rt.mu.Lock()
	localTS, compactTS := fx.rt.lastLocalCheckpointTS, fx.rt.lastCompactionTS
	seq, samples := fx.rt.lastCheckpointSeq, fx.rt.deltaSamples
	fx.rt.mu.Unlock()

	require.NotZero(t, localTS, "the cadence seal is recorded")
	require.Equal(t, core.CheckpointSeq(7), seq)
	require.Equal(t, baseline, compactTS, "and it is NOT a compaction: the host clock has not moved")
	require.NotEqual(t, baseline, localTS, "the two stamps are genuinely different values")
	require.Zero(t, samples, "nor is it a measured compaction cost")
	require.Equal(t, int64(1), fx.counter(counterLocalCheckpoint))
	require.Zero(t, fx.counter(counterHostCompaction), "reporting keeps them apart too")

	// Now the host really does compact.
	fx.clock.Advance(90 * time.Second)
	fx.rt.RecordCompactionCost(12)
	fx.rt.mu.Lock()
	afterLocal, afterCompact := fx.rt.lastLocalCheckpointTS, fx.rt.lastCompactionTS
	fx.rt.mu.Unlock()
	require.Equal(t, localTS, afterLocal, "a host compaction does not restamp our cadence")
	require.NotZero(t, afterCompact)
	require.NotEqual(t, afterLocal, afterCompact)
	require.Equal(t, int64(1), fx.counter(counterHostCompaction))
	require.Equal(t, int64(1), fx.counter(counterLocalCheckpoint))

	// The distinction is durable, not just in-memory.
	require.NoError(t, fx.rt.Persist(context.Background()))
	next := newRTFixture(t, withRoot(fx))
	next.bind(rtSession)
	next.rt.mu.Lock()
	defer next.rt.mu.Unlock()
	require.Equal(t, afterLocal, next.rt.lastLocalCheckpointTS)
	require.Equal(t, afterCompact, next.rt.lastCompactionTS)
	require.Equal(t, core.CheckpointSeq(7), next.rt.lastCheckpointSeq)
}
