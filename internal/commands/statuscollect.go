package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// StatusSchema is the version of the status.full document. It is separate from EnvelopeSchema:
// the envelope is the same for every command and changes rarely; this one changes whenever
// status gains or loses a section.
const StatusSchema = 1

// DaemonStatus mirrors internal/daemon.StatusSnapshot, the ipc.OpStatus payload.
//
// It is a mirror rather than the type itself because internal/commands does not import
// internal/daemon: the frontends render what the daemon already computed and own no daemon
// logic, and pulling the package in would make that boundary invisible. The cost of a mirror is
// that it can fall behind, so TestStatus_DaemonPayloadMirrorIsCurrent compares the two member for
// member and fails the moment the daemon's own payload changes.
//
// Sessions is []json.RawMessage rather than a second mirror: status renders session rows through
// the daemon's own JSON and never inspects their internals, so copying that struct too would add a
// thing to keep current for no reading this package does.
type DaemonStatus struct {
	Mode       string                      `json:"mode"`
	Contract   []contract.Result           `json:"contract"`
	Hot        string                      `json:"hot"`
	Sessions   []json.RawMessage           `json:"sessions"`
	Latency    map[string]obs.HistSnapshot `json:"latency"`
	Budgets    []obs.BudgetBreach          `json:"budgets"`
	Counters   map[string]int64            `json:"counters"`
	SpoolFiles int                         `json:"spool_files"`
	LoudTail   []string                    `json:"loud_tail"`
	Extra      json.RawMessage             `json:"extra,omitempty"`
}

// Source names where an observation came from.
type Source string

const (
	// SourceDaemon is a live answer from the resident daemon over ipc.OpStatus.
	SourceDaemon Source = "daemon"
	// SourceDisk is the metrics file the daemon last persisted.
	SourceDisk Source = "disk"
	// SourceNone is no observation at all.
	SourceNone Source = "none"
)

// Availability is whether one observation could be made.
type Availability string

const (
	// AvailabilityOK: observed, and the value may be displayed.
	AvailabilityOK Availability = "available"
	// AvailabilityUnavailable: nothing observed this. There is no value, and none is invented.
	AvailabilityUnavailable Availability = "unavailable"
	// AvailabilityError: something answered and the answer could not be used.
	AvailabilityError Availability = "error"
)

// Measure says whether a number was measured or derived from one.
type Measure string

const (
	// MeasureObserved is a value some instrument actually recorded.
	MeasureObserved Measure = "observed"
	// MeasureEstimated is a value computed from an observation, e.g. B-A, which the daemon records
	// as the observed lower bound plus a fixed tail allowance.
	MeasureEstimated Measure = "estimated"
)

// Provenance travels with every displayed value: where it came from, whether it is there at all,
// how old it is, and — when it is missing — why.
type Provenance struct {
	Source Source       `json:"source"`
	Status Availability `json:"status"`
	// AgeMS is how stale the observation is, in milliseconds. It is a pointer because an unknown
	// age must serialize as null: a zero would read as "collected just now", which is the exact
	// confusion between an absent value and an observed one that this whole type exists to stop.
	AgeMS  *int64 `json:"age_ms"`
	Reason string `json:"reason,omitempty"`
}

// Latency is one histogram's displayable reading. Durations are microseconds because the
// underlying histogram buckets microseconds, so a millisecond field would round away the
// resolution the instrument actually has.
type Latency struct {
	Hist    string  `json:"hist"`
	N       int64   `json:"n"`
	P50US   int64   `json:"p50_us"`
	P95US   int64   `json:"p95_us"`
	P99US   int64   `json:"p99_us"`
	MaxUS   int64   `json:"max_us"`
	Measure Measure `json:"measure"`
}

// HookRow is one installed hook entry point's latency row.
type HookRow struct {
	Event          string `json:"event"`
	Subcommand     string `json:"subcommand"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	// Budget is the §2.4 budget that covers this entry point.
	Budget string `json:"budget"`
	// Latency is this hook's OWN timing, or nil when no instrument measures this hook alone.
	// It is never filled from an aggregate that mixes this hook with others.
	Latency    *Latency   `json:"latency"`
	Provenance Provenance `json:"provenance"`
}

// BudgetRow is one §2.4 budget's row.
type BudgetRow struct {
	ID   obs.BudgetID `json:"id"`
	Hist string       `json:"hist"`
	// Gated is whether breaching this budget actually changes behaviour.
	Gated bool `json:"gated"`
	// Aggregate is whether this row's number is attributable to exactly one hook entry point.
	// It is true both for a histogram that mixes several of them (B-A) and for one that is not a
	// hook budget at all (B-B, B-F): in neither case may a reader attribute the figure to one
	// caller. Only a row with exactly one entry in Covers is a single-source measurement.
	Aggregate bool `json:"aggregate"`
	// Covers names what an aggregate row mixes, so a reader can see what the number is an average
	// over rather than guessing.
	Covers     []string   `json:"covers,omitempty"`
	Measure    Measure    `json:"measure"`
	Latency    *Latency   `json:"latency"`
	Provenance Provenance `json:"provenance"`
}

// StatusReport is the status.full document: the original OpStatus payload, plus the qualified
// per-hook and per-budget views SP-14 adds on top of it.
type StatusReport struct {
	Schema        int           `json:"schema"`
	CollectedAtMS int64         `json:"collected_at_ms"`
	Primary       Provenance    `json:"primary"`
	Snapshot      *DaemonStatus `json:"snapshot"`
	Hooks         []HookRow     `json:"hooks"`
	Budgets       []BudgetRow   `json:"budgets"`
}

// StatusSources are the two places a status observation can come from, in preference order.
//
// They are function fields rather than concrete readers so this collector is exercised without a
// daemon, a socket or a temporary project root. internal/cli binds the real ones (SP-14 commit 7);
// a nil member means that source is not available in this build, which is a legitimate answer and
// not an error.
type StatusSources struct {
	// Daemon returns the live payload and the time it describes.
	Daemon func(context.Context) (DaemonStatus, time.Time, error)
	// Disk returns the last persisted metrics snapshot, whose own TS carries its age.
	Disk func(context.Context) (obs.Snapshot, error)
	// Refused, when non-nil, is why neither source may be asked: owner decision D18 refuses a
	// project root that is the user's home directory, so there is no daemon to ask and no project
	// metrics file to read, and asking would touch the user-global layer's directory. The report
	// then carries nothing observed, with this as its one reason.
	Refused error
}

// perHookHists maps a hook entry point to the histogram that measures THAT HOOK ALONE.
//
// Only PreCompact has one. Every other entry point is recorded by the daemon's
// recordHotPathSample, which runs on delivery without distinguishing which hook delivered, so its
// hook_controlled histogram is a single distribution over all of them. An entry point absent here
// therefore has no per-hook timing, and the row says so rather than borrowing the aggregate's
// numbers — six identical figures presented as six measurements would be six claims nobody made.
var perHookHists = map[string]obs.BudgetID{
	"PreCompact": obs.BE,
}

// hookBudgets maps every entry point to the §2.4 budget that covers it, per-hook or not.
var hookBudgets = map[string]obs.BudgetID{
	"PostToolUse":      obs.BA,
	"UserPromptSubmit": obs.BA,
	"SessionStart":     obs.BA,
	"PreCompact":       obs.BE,
	"Stop":             obs.BA,
	"SubagentStop":     obs.BA,
	"SessionEnd":       obs.BA,
}

// estimatedBudgets are the budgets whose recorded value is derived rather than measured.
//
// B-A is recorded as the observed lower bound plus a fixed tail allowance (internal/daemon's
// recordHotPathSample), so calling it a measurement overstates it by exactly that allowance. The
// measured lower bound is kept separately under hook_controlled_observed.
var estimatedBudgets = map[obs.BudgetID]bool{obs.BA: true}

// uninstrumentedBudgets are declared in the §2.4 table and observed by nothing in the tree.
//
// B-D is the host's own process-creation cost. Nothing in this repository can see it — the process
// whose creation it measures is not running yet — so its row is permanently unavailable, which is
// a different statement from "no samples yet" and must not be rendered as an empty histogram.
var uninstrumentedBudgets = map[obs.BudgetID]string{
	obs.BD: "no instrument records this histogram in this build; it measures host process creation, " +
		"which the created process cannot observe",
}

// CollectStatus assembles the status.full report from whichever source answers.
//
// It never fails: a status command that cannot reach anything still has something true to say, and
// "unavailable, because ..." is that thing. Every row it cannot fill carries its own reason.
func CollectStatus(ctx context.Context, src StatusSources, now time.Time) StatusReport {
	rep := StatusReport{
		Schema:        StatusSchema,
		CollectedAtMS: now.UnixMilli(),
	}

	snap, hists, prov := resolve(ctx, src, now)
	rep.Primary = prov
	rep.Snapshot = snap
	rep.Hooks = hookRows(hists, prov)
	rep.Budgets = budgetRows(hists, prov)
	return rep
}

// resolve tries the daemon, then the persisted metrics file, and reports which one answered.
func resolve(ctx context.Context, src StatusSources, now time.Time) (*DaemonStatus, map[string]obs.HistSnapshot, Provenance) {
	if src.Refused != nil {
		return nil, nil, Provenance{
			Source: SourceNone,
			Status: AvailabilityUnavailable,
			Reason: src.Refused.Error(),
		}
	}
	var reasons []string

	if src.Daemon != nil {
		snap, at, err := callDaemon(ctx, src.Daemon)
		if err == nil {
			return &snap, snap.Latency, Provenance{
				Source: SourceDaemon,
				Status: AvailabilityOK,
				AgeMS:  ageMS(at, now),
			}
		}
		reasons = append(reasons, "daemon: "+err.Error())
	} else {
		reasons = append(reasons, "daemon: no client bound in this build")
	}

	if src.Disk != nil {
		persisted, err := callDisk(ctx, src.Disk)
		if err == nil {
			return nil, persisted.Hists, Provenance{
				Source: SourceDisk,
				Status: AvailabilityOK,
				AgeMS:  ageMS(persisted.TS.Time(), now),
				Reason: strings.Join(reasons, "; "),
			}
		}
		reasons = append(reasons, "disk: "+err.Error())
		return nil, nil, Provenance{
			Source: SourceNone,
			Status: AvailabilityError,
			Reason: strings.Join(reasons, "; "),
		}
	}
	reasons = append(reasons, "disk: no metrics file read in this build")

	return nil, nil, Provenance{
		Source: SourceNone,
		Status: AvailabilityUnavailable,
		Reason: strings.Join(reasons, "; "),
	}
}

// callDaemon invokes the daemon source behind a panic barrier.
//
// The sources are bound by internal/cli to real transport and decoding code, and a panic in one of
// them must not become the status command's answer: status exits 0 in every case (statusBody), and
// a panic that escaped here would reach runGuarded, which maps a non-hook panic to a failure exit.
// So a panicking source is treated exactly like one that returned an error, with the panic value
// as the error text, and the fallback order continues as if it had (V5-VERIFY I-14.6). This
// package has no logging seam to report through; the reason lands in the report's provenance,
// which is the one place a status reader looks.
func callDaemon(ctx context.Context, f func(context.Context) (DaemonStatus, time.Time, error),
) (snap DaemonStatus, at time.Time, err error) {
	defer recoverSource(&err)
	return f(ctx)
}

// callDisk invokes the disk source behind the same panic barrier as callDaemon.
func callDisk(ctx context.Context, f func(context.Context) (obs.Snapshot, error),
) (persisted obs.Snapshot, err error) {
	defer recoverSource(&err)
	return f(ctx)
}

// recoverSource converts a recovered panic into *err. It must be the deferred function itself,
// not called from one, for recover to see the panic.
func recoverSource(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("panic recovered: %v", r)
	}
}

// ageMS returns the milliseconds between at and now, or nil when at is unknown.
//
// An observation stamped AFTER now is as fresh as the report itself and reads as 0, never as a
// negative age. That ordering is the live daemon path's normal case, not an anomaly: Invocation.Now
// is read before the body runs, and the daemon assembles its answer during the round trip that
// follows, so a live answer is always a few milliseconds younger than the report's own clock
// reading. A reader shown "-1 ms old" learns nothing true from it (V5-VERIFY §4.1).
func ageMS(at, now time.Time) *int64 {
	if at.IsZero() {
		return nil
	}
	ms := max(now.Sub(at).Milliseconds(), 0)
	return &ms
}

// hookRows builds one row per installed hook entry point, in hooks.json order.
func hookRows(hists map[string]obs.HistSnapshot, prov Provenance) []HookRow {
	entries := pluginmanifest.HookEntryPoints()
	rows := make([]HookRow, 0, len(entries))

	for _, e := range entries {
		row := HookRow{
			Event:          e.Event,
			Subcommand:     e.Subcommand,
			TimeoutSeconds: e.TimeoutSeconds,
			Budget:         string(hookBudgets[e.Event]),
		}

		own, hasOwn := perHookHists[e.Event]
		if !hasOwn {
			row.Provenance = Provenance{
				Source: prov.Source,
				Status: AvailabilityUnavailable,
				AgeMS:  prov.AgeMS,
				Reason: fmt.Sprintf(
					"no per-hook instrument: %s is folded into the %s aggregate, which mixes every "+
						"delivering hook and cannot be attributed to one",
					e.Event, histFor(hookBudgets[e.Event])),
			}
			rows = append(rows, row)
			continue
		}

		name := histFor(own)
		snap, ok := hists[name]
		if !ok || snap.N == 0 {
			row.Provenance = Provenance{
				Source: prov.Source,
				Status: AvailabilityUnavailable,
				AgeMS:  prov.AgeMS,
				Reason: unobservedReason(prov, name),
			}
			rows = append(rows, row)
			continue
		}

		row.Latency = latencyOf(name, snap, measureOf(own))
		row.Provenance = Provenance{Source: prov.Source, Status: AvailabilityOK, AgeMS: prov.AgeMS}
		rows = append(rows, row)
	}
	return rows
}

// budgetRows builds one row per §2.4 budget, in B-A..B-G order.
func budgetRows(hists map[string]obs.HistSnapshot, prov Provenance) []BudgetRow {
	budgets := obs.Budgets()
	rows := make([]BudgetRow, 0, len(budgets))

	for _, b := range budgets {
		row := BudgetRow{
			ID:        b.ID,
			Hist:      b.Hist,
			Gated:     b.Gated,
			Aggregate: len(coveredBy(b.ID)) != 1,
			Covers:    coveredBy(b.ID),
			Measure:   measureOf(b.ID),
		}

		if reason, uninstrumented := uninstrumentedBudgets[b.ID]; uninstrumented {
			row.Provenance = Provenance{
				Source: prov.Source,
				Status: AvailabilityUnavailable,
				AgeMS:  prov.AgeMS,
				Reason: reason,
			}
			rows = append(rows, row)
			continue
		}

		snap, ok := hists[b.Hist]
		if !ok || snap.N == 0 {
			row.Provenance = Provenance{
				Source: prov.Source,
				Status: AvailabilityUnavailable,
				AgeMS:  prov.AgeMS,
				Reason: unobservedReason(prov, b.Hist),
			}
			rows = append(rows, row)
			continue
		}

		row.Latency = latencyOf(b.Hist, snap, row.Measure)
		row.Provenance = Provenance{Source: prov.Source, Status: AvailabilityOK, AgeMS: prov.AgeMS}
		rows = append(rows, row)
	}
	return rows
}

// unobservedReason distinguishes "nothing was reachable" from "something answered and had no
// samples for this histogram". Both leave the row empty; only the second says anything about the
// instrument.
func unobservedReason(prov Provenance, hist string) string {
	if prov.Status != AvailabilityOK {
		return prov.Reason
	}
	return fmt.Sprintf("%s: no samples recorded in the observed window", hist)
}

// coveredBy returns the hook entry points a budget's histogram mixes, in hooks.json order.
func coveredBy(id obs.BudgetID) []string {
	var out []string
	for _, e := range pluginmanifest.HookEntryPoints() {
		if hookBudgets[e.Event] == id {
			out = append(out, e.Event)
		}
	}
	return out
}

// histFor returns a budget's histogram name.
func histFor(id obs.BudgetID) string {
	for _, b := range obs.Budgets() {
		if b.ID == id {
			return b.Hist
		}
	}
	return ""
}

// measureOf reports whether a budget's recorded value is measured or derived.
func measureOf(id obs.BudgetID) Measure {
	if estimatedBudgets[id] {
		return MeasureEstimated
	}
	return MeasureObserved
}

// latencyOf converts a histogram snapshot into its displayable form.
func latencyOf(name string, s obs.HistSnapshot, m Measure) *Latency {
	return &Latency{
		Hist:    name,
		N:       s.N,
		P50US:   s.P50.Microseconds(),
		P95US:   s.P95.Microseconds(),
		P99US:   s.P99.Microseconds(),
		MaxUS:   s.Max.Microseconds(),
		Measure: m,
	}
}
