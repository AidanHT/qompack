package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"sync"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// This file is SP-16 §2's demand half: what is actually known about how much a piece of content is
// wanted, kept in a shape that cannot be collapsed into a single number by accident.
//
// §2's requirement is one sentence — "record usefulness separately from request frequency,
// distinct touches, failed attempts and missing telemetry" — and every design decision here
// follows from taking the word SEPARATELY literally.
//
// FREQUENCY IS NOT USEFULNESS. Content asked for fifty times that never once helped is not fifty
// times more valuable than content asked for once that unblocked the task; it is a retrieval loop.
// Requests and Useful are therefore different fields, Usefulness() reports the ratio only when
// there is usefulness telemetry to compute it from, and nothing in this file lets a request count
// stand in for one.
//
// UNKNOWN IS NOT ZERO. A key nobody instrumented has an unknown usefulness, not a zero one, and
// Usefulness returns a (value, known) pair so a caller cannot read the first without the second.
// TelemetryGaps counts the observations a recorder KNEW it was missing, which is what keeps
// M6-G16-B's "missing telemetry visible" true rather than aspirational.
//
// RECOVERY COST IS ITS OWN ARGUMENT. §2 says high recovery cost can justify keeping a complete
// span, and that is a claim about what re-deriving something would cost, not about how often
// anyone asked for it. It is recorded and reported; it is never folded into a demand score, because
// a single number would let a cheap, popular thing outrank an expensive, rare one that is the only
// reason the session can continue.
//
// This file records and reports. It ranks nothing on its own authority: Promote is SP-11 and
// SP-15's decision under their serialized budgets, and every consumer here gets the evidence
// rather than a verdict.

// demandFileName is the append-only demand log's basename under .qompack/state/.
const demandFileName = "demand.jsonl"

// demandRecordVersion is the on-disk version of one demand observation.
const demandRecordVersion = 1

// DemandKind names what kind of observation one demand record carries. It is a closed set, and an
// unrecognized kind is counted as an explicit telemetry gap rather than dropped: a record this
// build cannot interpret is evidence that something was observed, and forgetting it would make the
// gap invisible.
type DemandKind string

const (
	// DemandRequested is one request for the key: the frequency signal, and nothing more.
	DemandRequested DemandKind = "requested"
	// DemandTouched is one DISTINCT context touching the key — a turn, a session. Two requests in
	// one turn are one touch, which is what separates "wanted by the work" from "asked twice".
	DemandTouched DemandKind = "touched"
	// DemandUseful is an observation that having the key actually helped. It is recorded by
	// whatever can see the outcome, never inferred from a request.
	DemandUseful DemandKind = "useful"
	// DemandFailed is a retrieval attempt for the key that did not succeed. It is demand
	// evidence — someone wanted it — and simultaneously evidence that the demand went unmet.
	DemandFailed DemandKind = "failed"
	// DemandGap is an observation the recorder knows it could not make. It is the missing
	// telemetry, recorded explicitly so a report can say "unknown" instead of "none".
	DemandGap DemandKind = "gap"
)

// Valid reports whether k is one of the closed demand kinds.
func (k DemandKind) Valid() bool {
	switch k {
	case DemandRequested, DemandTouched, DemandUseful, DemandFailed, DemandGap:
		return true
	default:
		return false
	}
}

// DemandObservation is one line of state/demand.jsonl.
//
// It is deliberately tiny and additive: the log is append-only, so an aggregate is replayed from
// its records rather than read from a mutable row, and a field added later leaves every existing
// line readable.
type DemandObservation struct {
	V    int            `json:"v"`
	Kind DemandKind     `json:"kind"`
	Key  string         `json:"key"`
	TS   core.UnixMilli `json:"ts"`
	// Session is who observed it, so a per-scope reader can filter without a second index.
	Session core.SessionID `json:"session,omitempty"`
	// Touch identifies the distinct context a DemandTouched record belongs to — a turn id, a
	// session id, whatever the recorder counts as "one place". Aggregate deduplicates on it, which
	// is what makes DistinctTouches distinct rather than a second request counter.
	Touch string `json:"touch,omitempty"`
	// RecoveryCostMs is what re-deriving this key was measured to cost, on a record that measured
	// it. Zero means not measured, which is why Demand reports the maximum observed rather than a
	// mean: a mean over unmeasured records reads as cheap.
	RecoveryCostMs int64 `json:"recovery_cost_ms,omitempty"`
}

// Demand is the aggregate of every observation about one key.
//
// Every counter is separate and stays separate. There is no total, no score and no ranking method
// on this type, and that absence is the design: a consumer that wants to weigh these against each
// other has to say how, in its own code, where the weighting is visible.
type Demand struct {
	Key string
	// Requests is how many times the key was asked for. Frequency, and only frequency.
	Requests int
	// DistinctTouches is how many distinct contexts touched it, deduplicated on Touch.
	DistinctTouches int
	// Useful is how many times having it was OBSERVED to help.
	Useful int
	// Failed is how many retrieval attempts for it did not succeed.
	Failed int
	// TelemetryGaps is how many observations the recorder knew it was missing, plus every record
	// whose kind this build could not interpret.
	TelemetryGaps int
	// MaxRecoveryCostMs is the largest measured cost of re-deriving this key. It is the maximum
	// rather than the mean because unmeasured records carry zero, and a mean over them would
	// report an expensive thing as cheap.
	MaxRecoveryCostMs int64
	// FirstTS and LastTS bound the observation window.
	FirstTS, LastTS core.UnixMilli
}

// Usefulness reports the observed usefulness rate and whether it is known at all.
//
// known is false when nothing ever recorded a DemandUseful or a DemandFailed for this key — that
// is, when nobody instrumented the outcome. The rate is then 0, and a caller that reads it without
// the flag is reading a number that means "we never looked", not "it never helped". Making the
// two-value form the ONLY form is the point: there is no accessor that returns the bare float.
//
// The denominator is Useful+Failed, not Requests. A request that nobody classified is evidence
// about frequency and no evidence at all about outcome, and putting it in the denominator would
// silently punish keys whose instrumentation is thin.
func (d Demand) Usefulness() (rate float64, known bool) {
	den := d.Useful + d.Failed
	if den == 0 {
		return 0, false
	}
	return float64(d.Useful) / float64(den), true
}

// Instrumented reports whether every request for this key was accounted for by an outcome
// observation and no gap was recorded.
//
// It is what a report consults before quoting a usefulness rate as representative: a key with
// forty requests, two outcomes and no gaps has a rate computed from five percent of its traffic,
// and saying so is the difference between a measurement and a number.
func (d Demand) Instrumented() bool {
	return d.TelemetryGaps == 0 && d.Useful+d.Failed >= d.Requests
}

// DemandPath returns the demand log for a project root.
func DemandPath(root string) string {
	return filepath.Join(paths.Of(root).State, demandFileName)
}

// DemandLog is the append-only recorder for demand observations.
//
// It is separate from FSStore rather than a method on it because demand is optional, gated
// evidence: runtime.phase7.retrieval.demandPromotion is off until M6-G16-C passes, and a store
// that opened a file for it unconditionally would make an experiment's cost unavoidable.
type DemandLog struct {
	mu   sync.Mutex
	path string
}

// OpenDemandLog returns the demand log for root. It creates nothing: the file appears on the first
// Record, so an enabled-but-unused feature leaves no trace.
func OpenDemandLog(root string) *DemandLog {
	return &DemandLog{path: DemandPath(root)}
}

// Path returns the log file's path.
func (l *DemandLog) Path() string { return l.path }

// Record appends one observation.
//
// A record whose kind this build does not recognize is REFUSED rather than written, because the
// writer is this build and it has no business minting a kind it cannot read back. An unrecognized
// kind arriving from an older or newer file is a different matter, and Aggregate counts it as a
// telemetry gap.
func (l *DemandLog) Record(o DemandObservation) error {
	if !o.Kind.Valid() {
		return fmt.Errorf("store: refusing to record an unknown demand kind %q", string(o.Kind))
	}
	if o.Key == "" {
		return errors.New("store: a demand observation needs a key")
	}
	o.V = demandRecordVersion

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := paths.AppendJSONL(l.path, o); err != nil {
		return fmt.Errorf("store: appending to %s: %w", l.path, err)
	}
	return nil
}

// Aggregate replays the log and returns one Demand per key, sorted by key.
//
// A line that will not parse is counted as a telemetry gap against a synthetic empty key rather
// than failing the read: the log is append-only evidence, one bad line is not a reason to discard
// the rest, and a gap is exactly what an unreadable observation is. A missing file aggregates to
// nothing, which is not an error — it is a project where nothing has been observed yet.
func (l *DemandLog) Aggregate() (map[string]Demand, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := paths.OpenShared(l.path)
	if err != nil {
		return map[string]Demand{}, nil
	}
	defer func() { _ = f.Close() }()

	out := map[string]Demand{}
	touches := map[string]map[string]struct{}{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), bufio.MaxScanTokenSize)

	unreadable := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var o DemandObservation
		if err := json.Unmarshal(line, &o); err != nil || o.Key == "" {
			unreadable++
			continue
		}
		d := out[o.Key]
		d.Key = o.Key
		if d.FirstTS == 0 || o.TS < d.FirstTS {
			d.FirstTS = o.TS
		}
		if o.TS > d.LastTS {
			d.LastTS = o.TS
		}
		if o.RecoveryCostMs > d.MaxRecoveryCostMs {
			d.MaxRecoveryCostMs = o.RecoveryCostMs
		}
		switch o.Kind {
		case DemandRequested:
			d.Requests++
		case DemandTouched:
			seen := touches[o.Key]
			if seen == nil {
				seen = map[string]struct{}{}
				touches[o.Key] = seen
			}
			if _, dup := seen[o.Touch]; !dup {
				seen[o.Touch] = struct{}{}
				d.DistinctTouches++
			}
		case DemandUseful:
			d.Useful++
		case DemandFailed:
			d.Failed++
		default:
			// DemandGap and every kind this build cannot interpret. Both are the same thing to a
			// reader: an observation that exists and cannot be counted.
			d.TelemetryGaps++
		}
		out[o.Key] = d
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return out, fmt.Errorf("store: reading %s: %w", l.path, err)
	}
	if unreadable > 0 {
		d := out[""]
		d.TelemetryGaps += unreadable
		out[""] = d
	}
	return out, nil
}

// DemandReport is the evidence M6-G16-B asks for, assembled from an aggregate: what was wanted,
// what helped, and how much of it nobody measured.
type DemandReport struct {
	// Keys is every aggregated key, sorted by key so two reports of the same log compare equal.
	Keys []Demand
	// Requests, Useful and Failed are the totals, kept apart for the same reason the per-key
	// counters are.
	Requests, Useful, Failed int
	// UninstrumentedKeys is how many keys have no outcome telemetry at all. It is the headline
	// number for "how much of this report is a measurement": a report over mostly uninstrumented
	// keys is a frequency report wearing a usefulness label.
	UninstrumentedKeys int
	// TelemetryGaps is the total of every explicit gap and unreadable observation.
	TelemetryGaps int
}

// Report aggregates the log into a DemandReport.
func (l *DemandLog) Report() (DemandReport, error) {
	agg, err := l.Aggregate()
	out := DemandReport{Keys: make([]Demand, 0, len(agg))}
	for _, d := range agg {
		out.Keys = append(out.Keys, d)
		out.Requests += d.Requests
		out.Useful += d.Useful
		out.Failed += d.Failed
		out.TelemetryGaps += d.TelemetryGaps
		if _, known := d.Usefulness(); !known {
			out.UninstrumentedKeys++
		}
	}
	sort.Slice(out.Keys, func(i, j int) bool { return out.Keys[i].Key < out.Keys[j].Key })
	return out, err
}
