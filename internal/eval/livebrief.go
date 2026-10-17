package eval

import "sort"

// ── The steer arm's brief outcomes (V7 W6 item 5) ──────────────────────────────────────────────
//
// For every compaction of a steer trial the harness asks what the mod's brief came to: the stream
// says which compactions happened, and the mod's brief-outcome log (state/mod-brief-<session>.jsonl,
// read in tools/devtool with the wire contract's parser, coordinator decision D-a) says what each
// brief call came to. Each compaction is matched to its record here, from plain values. A compaction
// with no record is "missing": the brief was not called, was refused (refused calls write nothing),
// was malformed, or its child was killed at the mod's timeout (ledger ruling R20). A served record
// with no characters is "served-empty", its own class: it steered nothing, so it is never counted as
// a served brief. Every outcome is a treatment outcome, counted as assigned (intention to treat).

// The brief outcome classes of one compaction.
const (
	LiveBriefServed      = "served"
	LiveBriefServedEmpty = "served-empty"
	LiveBriefCold        = "cold"
	LiveBriefBusy        = "busy"
	LiveBriefTimeout     = "timeout"
	LiveBriefError       = "error"
	LiveBriefMissing     = "missing"
)

// LiveBriefClasses is every class in report order.
var LiveBriefClasses = []string{
	LiveBriefServed, LiveBriefServedEmpty, LiveBriefCold, LiveBriefBusy, LiveBriefTimeout, LiveBriefError, LiveBriefMissing,
}

// LiveBriefRecord is one line of a session's brief-outcome log, as plain values: the record's
// trigger, outcome (a modwire outcome name), served characters and wall time.
type LiveBriefRecord struct {
	Trigger string `json:"trigger"`
	Outcome string `json:"outcome"`
	Chars   int    `json:"chars"`
	MS      int64  `json:"ms"`
}

// LiveBriefCompaction is one compaction's brief outcome.
type LiveBriefCompaction struct {
	Trigger string `json:"trigger"`
	Outcome string `json:"outcome"`
	// Chars is the served brief's length; 0 for every other class.
	Chars int   `json:"chars,omitempty"`
	MS    int64 `json:"ms,omitempty"`
}

// LiveBriefOutcomes is a steer trial's brief outcomes.
type LiveBriefOutcomes struct {
	// Compactions is each compaction of the stream, in order, with its outcome.
	Compactions []LiveBriefCompaction `json:"compactions"`
	// Records counts the log's well-formed lines; Unmatched those no compaction claimed (a
	// precompute brief, or one whose compaction failed); Malformed the lines the wire parser refused.
	Records   int `json:"records"`
	Unmatched int `json:"unmatched,omitempty"`
	Malformed int `json:"malformed,omitempty"`
	// Problem says why the log could not be read, when it could not; every compaction is then
	// missing.
	Problem string `json:"problem,omitempty"`
	// NoServedBrief flags a trial with no non-empty served brief: every compaction unserved or
	// served-empty (reported, never excluded).
	NoServedBrief bool `json:"no_served_brief"`
}

// MatchLiveBriefs matches each compaction, in stream order, to the first unclaimed record with the
// same trigger, in log order, and classifies it.
func MatchLiveBriefs(compactions []HostCompaction, records []LiveBriefRecord) LiveBriefOutcomes {
	out := LiveBriefOutcomes{Records: len(records), Compactions: []LiveBriefCompaction{}}
	used := make([]bool, len(records))
	served := false
	for _, c := range compactions {
		bc := LiveBriefCompaction{Trigger: c.Trigger, Outcome: LiveBriefMissing}
		for i, r := range records {
			if used[i] || r.Trigger != c.Trigger {
				continue
			}
			used[i] = true
			bc.Outcome, bc.MS = liveBriefClass(r), r.MS
			if bc.Outcome == LiveBriefServed {
				bc.Chars = r.Chars
				served = true
			}
			break
		}
		out.Compactions = append(out.Compactions, bc)
	}
	for _, u := range used {
		if !u {
			out.Unmatched++
		}
	}
	out.NoServedBrief = !served
	return out
}

// liveBriefClass is a record's class: served with characters, served-empty, or its unserved reason.
// An outcome the log should never carry (refused, or an unknown name) is an error.
func liveBriefClass(r LiveBriefRecord) string {
	switch r.Outcome {
	case LiveBriefServed:
		if r.Chars > 0 {
			return LiveBriefServed
		}
		return LiveBriefServedEmpty
	case LiveBriefCold, LiveBriefBusy, LiveBriefTimeout, LiveBriefError:
		return r.Outcome
	}
	return LiveBriefError
}

// LiveBriefStats is one arm's brief outcomes over its trials (W6 item 5): the compactions of every
// trial that carries brief outcomes, counted by class, the unserved share (cold, busy, timeout,
// error and missing) with served-empty as its own share beside it, the trials with no non-empty
// served brief, and the served briefs' lengths.
type LiveBriefStats struct {
	Trials           int            `json:"trials"`
	Compactions      int            `json:"compactions"`
	Outcomes         map[string]int `json:"outcomes"`
	UnservedShare    float64        `json:"unserved_share"`
	ServedEmptyShare float64        `json:"served_empty_share"`
	NoServedTrials   int            `json:"no_served_brief_trials"`
	// ServedChars summarizes the characters of each served (non-empty) brief.
	ServedChars LiveDistribution `json:"served_chars"`
}

// LiveDistribution is a sample's size, mean, nearest-rank median and 95th percentile, and maximum.
type LiveDistribution struct {
	N    int     `json:"n"`
	Mean float64 `json:"mean"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	Max  float64 `json:"max"`
}

// distributionOf summarizes xs (in any order).
func distributionOf(xs []float64) LiveDistribution {
	if len(xs) == 0 {
		return LiveDistribution{}
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	total := 0.0
	for _, x := range s {
		total += x
	}
	return LiveDistribution{
		N: len(s), Mean: total / float64(len(s)), P50: percentile(s, 50), P95: percentile(s, 95), Max: s[len(s)-1],
	}
}

// liveBriefStats aggregates the brief outcomes of ts; nil when none carries any.
func liveBriefStats(ts []LiveTrial) *LiveBriefStats {
	var st LiveBriefStats
	var chars []float64
	for _, t := range ts {
		if t.Brief == nil {
			continue
		}
		st.Trials++
		if t.Brief.NoServedBrief {
			st.NoServedTrials++
		}
		for _, c := range t.Brief.Compactions {
			if st.Outcomes == nil {
				st.Outcomes = map[string]int{}
			}
			st.Outcomes[c.Outcome]++
			st.Compactions++
			if c.Outcome == LiveBriefServed {
				chars = append(chars, float64(c.Chars))
			}
		}
	}
	if st.Trials == 0 {
		return nil
	}
	if st.Compactions > 0 {
		served, empty := st.Outcomes[LiveBriefServed], st.Outcomes[LiveBriefServedEmpty]
		st.ServedEmptyShare = float64(empty) / float64(st.Compactions)
		st.UnservedShare = float64(st.Compactions-served-empty) / float64(st.Compactions)
	}
	st.ServedChars = distributionOf(chars)
	return &st
}

// LiveResidentDaemon is what the harness found of a daemon already resident for a trial's fresh
// project before its session (ruling R42, review M4): its lock's pid and the build version it
// recorded. A daemon of another build than the arm's bundle may predate W1a and answer every brief
// `error` ("unknown op: mod"), which the brief record cannot show, so such a daemon is a harness
// fault: the attempt is a configuration error, set aside and run again, never a scored outcome.
type LiveResidentDaemon struct {
	PID     int    `json:"pid"`
	Version string `json:"version"`
	// BundleVersion is the arm's bundle version the daemon's build was compared with.
	BundleVersion string `json:"bundle_version"`
	// Fault reports that the daemon was of another build (or its lock unreadable).
	Fault bool `json:"fault"`
}
