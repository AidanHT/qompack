package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// Monitor is the §12.1 contract monitor. Every SessionStart, before any other work, RunAll executes
// the registered assertions; a SevCritical failure degrades the session to passive recording, and
// two consecutive clean runs restore it. Nothing it does is silent.
type Monitor interface {
	// Register adds a to the set RunAll executes. It reports an error for an assertion that could
	// never produce a usable Result — an empty ID or a nil Check — rather than accepting it and
	// failing at run time.
	Register(a Assertion) error
	// RunAll executes every registered assertion in registration order and returns their results
	// together with the mode now in force. It applies §12.1's transitions itself: a SevCritical
	// failure calls Degrade, and the second consecutive clean run while degraded calls Restore.
	RunAll(ctx context.Context, e Env) ([]Result, Mode)
	// Mode returns the mode currently in force, which is what every caller that decides whether
	// to ACT (inject additionalContext, emit customInstructions, schedule a checkpoint) reads.
	Mode() Mode
	// Degrade logs LOUD, writes .qompack/logs/LOUD.log, sets mode, and persists the reason so
	// /qompack:status and the next SessionStart both surface it. NEVER silent.
	Degrade(reason string, results []Result)
	// Restore returns the monitor to ModeFull, logged just as loudly as the degradation (§12.1).
	Restore(reason string)
	// Report returns the results of the most recent RunAll, or the results persisted by the run
	// that degraded a previous session, whichever is more recent.
	Report() []Result
}

// cleanRunsToRestore is §12.1's recovery rule: "Two consecutive clean runs call Monitor.Restore."
// It is a protocol constant of the degradation doctrine, not a tunable — Appendix C and §11.5 give
// it no key, and inventing one would let an operator configure the monitor into never recovering.
const cleanRunsToRestore = 2

// statePerm is the permission state/contract.json is written with: owner-only, like every other
// file under .qompack/.
const statePerm = 0o600

// state is the on-disk shape of state/contract.json (00-ARCHITECTURE.md §12.1). Mode is persisted
// as Mode.String's spelling rather than its numeric value so the file stays readable and survives a
// reordering of the Mode constants.
type state struct {
	Mode      string         `json:"mode"`
	Reason    string         `json:"reason,omitempty"`
	Since     core.UnixMilli `json:"since"`
	CleanRuns int            `json:"cleanRuns"`
	Results   []Result       `json:"results,omitempty"`
}

// monitor is the real §12.1 state machine. SP-05 replaces the assertion observations; the mechanics
// here are SP-01's and are tested for real.
type monitor struct {
	log       logging.Logger
	metrics   obs.Registry
	statePath string

	mu         sync.Mutex
	assertions []Assertion
	mode       Mode
	reason     string
	since      core.UnixMilli
	cleanRuns  int
	last       []Result
}

// NewMonitor returns a Monitor persisting its state to statePath, which is
// <root>/.qompack/state/contract.json in every real caller. An empty statePath disables
// persistence entirely (the mode still changes, and Degrade is still LOUD) — that is the honest
// behaviour for a caller with no project layout, not an error worth refusing to construct over.
//
// A nil log is replaced by logging.Nop, whose Loud calls still reach the process-wide ring
// logging.LastLoud reads; a nil metrics registry is simply not recorded to. Neither turns a
// degradation into a silent one.
//
// The persisted state is read back at construction, because §12.1 requires a degradation to
// survive into "the next SessionStart". A missing, unreadable or unparseable file leaves the
// monitor at ModeFull: a fresh build reports ModeFull, which is exactly what the CI guard asserts.
func NewMonitor(log logging.Logger, m obs.Registry, statePath string) Monitor {
	if log == nil {
		log = logging.Nop()
	}
	mon := &monitor{log: log, metrics: m, statePath: statePath, mode: ModeFull}
	mon.load()
	return mon
}

// load reads statePath into the monitor, best-effort. Every failure path is deliberate silence
// followed by ModeFull: §12.3's "everything else fails toward do nothing" applies to the monitor's
// own state file too, and a monitor that refused to start because its state file was corrupt would
// be a worse outcome than one that starts clean and re-degrades on the next failed assertion.
func (m *monitor) load() {
	if m.statePath == "" {
		return
	}
	b, err := os.ReadFile(paths.Long(m.statePath))
	if err != nil {
		return
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return
	}
	mode, ok := parseMode(st.Mode)
	if !ok {
		return
	}
	m.mode = mode
	m.reason = st.Reason
	m.since = st.Since
	m.cleanRuns = st.CleanRuns
	m.last = st.Results
}

// persist writes st to statePath through paths.WriteAtomic, creating the containing directory if
// needed. It reports an error only so callers can log one; no caller treats a failed persist as
// fatal, because the LOUD line has already been written by then.
func (m *monitor) persist(st state) error {
	if m.statePath == "" {
		return nil
	}
	b, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("contract: marshalling %s: %w", m.statePath, err)
	}
	if err := os.MkdirAll(paths.Long(filepath.Dir(m.statePath)), 0o700); err != nil {
		return fmt.Errorf("contract: mkdir for %s: %w", m.statePath, err)
	}
	if err := paths.WriteAtomic(m.statePath, b, statePerm); err != nil {
		return fmt.Errorf("contract: writing %s: %w", m.statePath, err)
	}
	return nil
}

// stateLocked builds the persistable snapshot for mode. The caller must hold m.mu.
func (m *monitor) stateLocked(mode Mode) state {
	return state{
		Mode:      mode.String(),
		Reason:    m.reason,
		Since:     m.since,
		CleanRuns: m.cleanRuns,
		Results:   append([]Result(nil), m.last...),
	}
}

func (m *monitor) Register(a Assertion) error {
	if a.ID == "" {
		return errors.New("contract: Register: assertion has no ID")
	}
	if a.Check == nil {
		return fmt.Errorf("contract: Register: assertion %s has no Check", a.ID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assertions = append(m.assertions, a)
	return nil
}

func (m *monitor) RunAll(ctx context.Context, e Env) ([]Result, Mode) {
	// A nil clock would make every Check that timestamps its Result panic, including
	// StandardAssertions'. Substituting the system clock keeps a half-filled Env — which is all a
	// caller outside a hook has — usable, and is the monitor's business rather than each Check's.
	if e.Clock == nil {
		e.Clock = core.SystemClock()
	}
	if e.Log == nil {
		e.Log = m.log
	}

	m.mu.Lock()
	assertions := append([]Assertion(nil), m.assertions...)
	m.mu.Unlock()

	results := make([]Result, 0, len(assertions))
	critical := false
	for _, a := range assertions {
		r := a.Check(ctx, e)
		results = append(results, r)
		if !r.OK && r.Severity == SevCritical {
			critical = true
		}
	}

	m.mu.Lock()
	m.last = results
	prev := m.mode
	if critical {
		m.cleanRuns = 0
	} else if prev == ModeDegradedPassive {
		m.cleanRuns++
	}
	cleanRuns := m.cleanRuns
	m.mu.Unlock()

	// ModeOff is an operator decision (runtime.mode = "off"), not a state the monitor may enter or
	// leave on its own, so no transition is applied while it is in force. The results are still
	// returned, so /qompack:status can show what the assertions would have said.
	if prev != ModeOff {
		switch {
		case critical:
			// Called on EVERY critical run, not only on the transition into degraded: §12.1's
			// whole point is that a broken host contract is never silent, so a session that
			// starts broken and stays broken says so every time.
			m.Degrade(degradeReason(results), results)
		case prev == ModeDegradedPassive && cleanRuns >= cleanRunsToRestore:
			m.Restore(fmt.Sprintf("%d consecutive clean contract runs", cleanRuns))
		}
	}

	return results, m.Mode()
}

func (m *monitor) Mode() Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

func (m *monitor) Degrade(reason string, results []Result) {
	// §12.1's order: LOUD first, so the degradation is on the record even if persisting it fails.
	m.log.Loud("contract: degrading to passive recording",
		"mode", ModeDegradedPassive.String(), "reason", reason, "failed", failedSummary(results))

	m.mu.Lock()
	m.reason = reason
	m.cleanRuns = 0
	if results != nil {
		m.last = append([]Result(nil), results...)
	}
	if m.mode != ModeDegradedPassive {
		m.since = latestTS(m.last)
	}
	// The mode is set under the same lock that builds the snapshot, so a concurrent Mode() can
	// never observe a mode the file on disk contradicts.
	m.mode = ModeDegradedPassive
	st := m.stateLocked(ModeDegradedPassive)
	m.mu.Unlock()

	if err := m.persist(st); err != nil {
		m.log.Error("contract: persisting degraded state", "err", err.Error())
	}
	m.record("contract.degrade", ModeDegradedPassive)
}

func (m *monitor) Restore(reason string) {
	m.log.Loud("contract: restoring full mode",
		"mode", ModeFull.String(), "reason", reason)

	m.mu.Lock()
	m.reason = reason
	m.cleanRuns = 0
	m.since = latestTS(m.last)
	m.mode = ModeFull
	st := m.stateLocked(ModeFull)
	m.mu.Unlock()

	if err := m.persist(st); err != nil {
		m.log.Error("contract: persisting restored state", "err", err.Error())
	}
	m.record("contract.restore", ModeFull)
}

func (m *monitor) Report() []Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Result(nil), m.last...)
}

// record increments counter and publishes the current mode as a gauge, when a registry was
// supplied. §12.1 requires the transition to be recorded in obs as well as logged.
func (m *monitor) record(counter string, mode Mode) {
	if m.metrics == nil {
		return
	}
	m.metrics.Counter(counter).Add(1)
	m.metrics.Gauge("contract.mode").Set(int64(mode))
}

// degradeReason renders the one-line reason persisted to state/contract.json and printed in the
// LOUD line: every critical failure, named, with what was expected and what was observed. IDs are
// sorted so the same failure set always produces the same reason string, whatever order the
// assertions happened to be registered in.
func degradeReason(results []Result) string {
	var parts []string
	for _, r := range results {
		if r.OK || r.Severity != SevCritical {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (expected %q, observed %q)", r.ID, r.Expected, r.Observed))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "no critical assertion failed"
	}
	return "critical host-contract failure: " + strings.Join(parts, "; ")
}

// failedSummary lists every failed assertion's ID and observed severity for the LOUD line's kv,
// including SevWarn failures: those do not degrade the session, but a reader of LOUD.log wants to
// see them next to the one that did.
func failedSummary(results []Result) string {
	var parts []string
	for _, r := range results {
		if r.OK {
			continue
		}
		parts = append(parts, string(r.ID)+"/"+r.Severity.label())
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// latestTS returns the newest timestamp in results, which is when the transition being recorded
// was actually observed. Reading it from the results rather than from a clock keeps the monitor
// free of its own time source: every Result is already stamped by the Check that produced it,
// using the Clock the caller supplied (§6.1 — time enters through a seam, never through
// time.Now).
func latestTS(results []Result) core.UnixMilli {
	var newest core.UnixMilli
	for _, r := range results {
		if r.TS > newest {
			newest = r.TS
		}
	}
	return newest
}
