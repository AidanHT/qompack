package fault

// The evidence vocabulary and the verdict, in a TEST file rather than in fault.go.
//
// That placement is forced and it is worth stating plainly, because it looks arbitrary otherwise.
// test/guards/faultenv_test.go's TestGuard_FaultEnvIsConfinedToTwoFiles forbids any NON-TEST file
// outside internal/cli/fault.go and internal/daemon/spawn.go from naming the fault-injection switch,
// and it matches on SUBSTRING: the artifacts directory variable this package reads begins with the
// switch's own spelling, so writing it out in fault.go would trip a guard about a different variable
// entirely. Test files are unrestricted there by design — the guard's own comment says so, because a
// rule that forbade naming the switch in a test would forbid testing the switch at all.
//
// Splitting the literal across a `+` to slip past the substring match was the alternative, and it
// was rejected: this machinery is test-only in every other respect too (it takes *testing.T, it
// writes into t.TempDir by default, and nothing outside a test calls any of it), so the file it
// belongs in is a test file whether or not a guard says so.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/paths"
)

// ---------------------------------------------------------------------------
// Evidence records
// ---------------------------------------------------------------------------

// Outcome is what one fault case established. The vocabulary is the package doc's, and it is
// four-valued because "the product could not reconstruct this, and it said so" is a different
// answer from both "it healed" and "it broke".
type Outcome string

// The four outcomes.
const (
	// OutcomeRecovered: the cut healed and the audit found nothing dangling that it introduced.
	OutcomeRecovered Outcome = "recovered"
	// OutcomeExplicitIncomplete: the cut did not heal and the product reported the gap.
	OutcomeExplicitIncomplete Outcome = "explicit_incomplete"
	// OutcomeFailed: the cut did not heal and nothing reported it. Reason names the owning package.
	OutcomeFailed Outcome = "failed"
	// OutcomeSkipped: the case did not run here. Reason says what was missing.
	OutcomeSkipped Outcome = "skipped"
)

// Phase is which of this package's five concerns a record belongs to — the axis the evidence table
// is grouped by, so a closed set rather than free prose.
type Phase string

// The phase axes.
const (
	// PhasePublication: a forced failure at one publication boundary (deliverable 2).
	PhasePublication Phase = "publication"
	// PhaseLifecycle: startup/resume/fork/clear, compaction, duplicate/missing/out-of-order events.
	PhaseLifecycle Phase = "lifecycle"
	// PhaseChild: a child process that died, or a hook fed nothing usable.
	PhaseChild Phase = "child"
	// PhaseResource: disk full, permission denial, lock contention.
	PhaseResource Phase = "resource"
	// PhaseHistorical: an object that was there at capture and is not there now.
	PhaseHistorical Phase = "historical"
)

// TargetInfo is the platform a record is attributed to.
type TargetInfo struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Go   string `json:"go"`
}

// BundleInfo identifies the artifact the case drove. The binary's digest is what makes a record
// re-checkable: two records naming the same version but different digests came from two builds.
type BundleInfo struct {
	Version      string `json:"version"`
	BinarySHA256 string `json:"binary_sha256"`
}

// Record is one fault case's retained artifact.
type Record struct {
	Name   string     `json:"name"`
	Phase  Phase      `json:"phase"`
	Target TargetInfo `json:"target"`
	Bundle BundleInfo `json:"bundle"`
	// Boundary names what was cut, in the matrix's own words.
	Boundary string `json:"boundary"`
	// SeedMethod says how the state got into the condition the cut needed, including whether the
	// cut was a real failure or a simulated one. "disk full" here is a fault SITE, never an ENOSPC,
	// and a record that did not say so would be claiming coverage this host cannot give.
	SeedMethod string `json:"seed_method"`
	// Outcome is required; writeRecord refuses a record that does not carry one.
	Outcome Outcome `json:"outcome"`
	// Reason is prose and it is the point: a skipped or failed case that does not say why is
	// indistinguishable from one nobody ran.
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
	// DanglingBefore and DanglingAfter are the consistency audit's counts either side of the
	// recovery. The acceptance row is about the DELTA: a reference that was already dangling before
	// the cut is not one the cut introduced.
	DanglingBefore int `json:"dangling_before"`
	DanglingAfter  int `json:"dangling_after"`
	// Artifact is a supporting file this record points at, when the case produced one.
	Artifact string `json:"artifact,omitempty"`
}

// artifactsEnvKey names the directory records are collected into. Unset (the ordinary `go test`
// case) means the test's own temp directory, so a plain run leaves nothing behind.
const artifactsEnvKey = "QOMPACK_FAULT_ARTIFACTS"

// artifactIndexName is the manifest of the records ONE run produced, written by TestMain.
//
// It exists because a record file is not self-dating: when the artifact directory points at a
// committed evidence directory and a case fatals before writing, the previous run's file survives
// and presents a stale `recovered` row that nothing distinguishes from a fresh one. Two mechanisms
// answer that together — the directory's records are pruned once at the start of a collecting run,
// and this manifest states afterwards exactly which names that run produced.
const artifactIndexName = "INDEX.json"

var (
	// collectOnce guards the single prune-and-create of a collecting run's artifact directory.
	collectOnce sync.Once
	collectDir  string
	collectErr  error

	// tempArtifactMu guards tempArtifactDirs, which caches one temp directory per test so a test
	// writing six records does not scatter them across six directories.
	tempArtifactMu   sync.Mutex
	tempArtifactDirs = map[string]string{}

	// writtenMu guards writtenRecords, the names this run wrote, in write order.
	writtenMu      sync.Mutex
	writtenRecords []string
)

// artifactDir returns where records are written: $QOMPACK_FAULT_ARTIFACTS when it is set (so a
// collecting run can be committed), else one temp directory per test.
func artifactDir(t *testing.T) string {
	t.Helper()
	if os.Getenv(artifactsEnvKey) != "" {
		collectOnce.Do(prepareCollectDir)
		if collectErr != nil {
			t.Fatalf("fault: %v", collectErr)
		}
		return collectDir
	}
	tempArtifactMu.Lock()
	defer tempArtifactMu.Unlock()
	if d, ok := tempArtifactDirs[t.Name()]; ok {
		return d
	}
	d := t.TempDir()
	tempArtifactDirs[t.Name()] = d
	return d
}

// prepareCollectDir creates the collecting directory and removes the JSON left by any earlier run.
//
// The prune is deliberately unconditional over `*.json` in that directory: pruning only the names
// this run is about to write cannot remove a record whose CASE was deleted or renamed, which is
// exactly the stale row that reads as current (test/security's prepareCollectDir, same reason).
func prepareCollectDir() {
	dir := os.Getenv(artifactsEnvKey)
	if err := os.MkdirAll(paths.Long(dir), 0o700); err != nil {
		collectErr = fmt.Errorf("creating the artifact directory %s: %w", dir, err)
		return
	}
	entries, err := os.ReadDir(paths.Long(dir))
	if err != nil {
		collectErr = fmt.Errorf("reading the artifact directory %s: %w", dir, err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if err := os.Remove(paths.Long(filepath.Join(dir, e.Name()))); err != nil {
			collectErr = fmt.Errorf("pruning the stale record %s: %w", e.Name(), err)
			return
		}
	}
	collectDir = dir
}

// writeArtifactIndex records which names this run produced, for a collecting run only. TestMain
// calls it after the last case, so a case that fatalled before writing its record is visible as an
// absence in a document written afterwards rather than as nothing at all.
func writeArtifactIndex() {
	if os.Getenv(artifactsEnvKey) == "" || collectDir == "" {
		return
	}
	// collectDir is set by primeArtifactDir before the first case, so this runs even for a run that
	// wrote no record at all — which is exactly the run whose absence needs stating.
	writtenMu.Lock()
	names := append([]string(nil), writtenRecords...)
	writtenMu.Unlock()
	sort.Strings(names)

	doc := struct {
		Target  TargetInfo `json:"target"`
		Bundle  BundleInfo `json:"bundle"`
		Records []string   `json:"records"`
	}{
		Target:  hostTarget(),
		Bundle:  BundleInfo{Version: hostBundle.Version, BinarySHA256: hostBundle.BinarySHA256},
		Records: names,
	}

	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(paths.Long(filepath.Join(collectDir, artifactIndexName)), append(b, '\n'), 0o600)
}

// hostTarget is the platform every record this run writes is attributed to.
func hostTarget() TargetInfo {
	return TargetInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version()}
}

// newRecord returns a record pre-filled with this host's target and the assembled bundle's
// identity, so no case has to remember to attribute its own evidence.
func newRecord(t *testing.T, name string) Record {
	t.Helper()
	return Record{Name: name, Target: hostTarget(), Bundle: hostBundleInfo(t)}
}

// hostBundleInfo is assembledBundle reduced to what a record carries. It lives here rather than
// beside the bundle it describes because BundleInfo does: a non-test file may not name a type
// declared in a test file, and the record vocabulary is test-only for the reason this file opens
// with.
func hostBundleInfo(t *testing.T) BundleInfo {
	t.Helper()
	b := assembledBundle(t)
	return BundleInfo{Version: b.Version, BinarySHA256: b.BinarySHA256}
}

// writeRecord persists rec as JSON under artifactDir and returns the path. The name is used as the
// file name, so it must be a single path segment: a record whose name carried a separator would
// silently write outside the artifact directory.
func writeRecord(t *testing.T, rec Record) string {
	t.Helper()

	if rec.Outcome == "" {
		t.Fatalf("fault %s: a record must carry an outcome", rec.Name)
	}
	if rec.Phase == "" {
		t.Fatalf("fault %s: a record must name the phase it is evidence about", rec.Name)
	}
	if rec.Name == "" || rec.Name != filepath.Base(rec.Name) || strings.ContainsAny(rec.Name, `/\`) {
		t.Fatalf("fault: record name %q must be a single path segment", rec.Name)
	}
	if rec.Name+".json" == artifactIndexName {
		t.Fatalf("fault: %q collides with this run's own manifest", rec.Name)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("fault: marshalling the %s record: %v", rec.Name, err)
	}
	p := filepath.Join(artifactDir(t), rec.Name+".json")
	if err := os.WriteFile(paths.Long(p), append(b, '\n'), 0o600); err != nil {
		t.Fatalf("fault: writing %s: %v", p, err)
	}
	writtenMu.Lock()
	writtenRecords = append(writtenRecords, rec.Name)
	writtenMu.Unlock()
	t.Logf("fault %s: %s — %s\n  boundary: %s\n  seed:     %s\n  dangling: %d→%d\n  detail:   %s\n  artifact: %s",
		rec.Name, rec.Outcome, rec.Reason, rec.Boundary, rec.SeedMethod,
		rec.DanglingBefore, rec.DanglingAfter, rec.Detail, p)
	return p
}

// platformSkipPrefix is the stubskips-permitted prefix for an environment-gated skip
// (tools/devtool/stubskips.go: the reason after it must be non-empty).
const platformSkipPrefix = "platform: "

// skipRecorded writes rec as `skipped` with the given reason, THEN skips.
//
// The order is the contract, exactly as test/canary's own skipRecorded documents it: a case that
// skips without leaving a record is indistinguishable from one that was never written, and the
// evidence table is assembled from the records rather than from the log.
func skipRecorded(t *testing.T, rec Record, reason string) {
	t.Helper()
	rec.Outcome = OutcomeSkipped
	rec.Reason = reason
	writeRecord(t, rec)
	t.Skip(platformSkipPrefix + rec.Name + ": " + reason)
}

// ---------------------------------------------------------------------------
// Is the gap EXPLICIT?
// ---------------------------------------------------------------------------

// §13 invariant 10: degradation is loud. A boundary whose state genuinely cannot be reconstructed
// is allowed to stay incomplete — what is not allowed is staying incomplete QUIETLY. These four
// surfaces are the ones the brief names, and a row that cannot find its gap on any of them has
// found a defect rather than a design decision.

// degradationSnapshot is everything five surfaces said at one instant. It is never a verdict on its
// own: a verdict is the DIFFERENCE between two of these, one taken before a cut and one after.
//
// Deltaing every collector is the fix for a defect this package shipped in round 1. `Drops` was
// absolute rather than deltaed, and all three checkpoint rows were promoted from `failed` to
// `explicit_incomplete` by `pointer_git_unavailable()` — a git-provenance drop the seeded checkpoint
// already carried before anything was cut, naming nothing the cut did.
type degradationSnapshot struct {
	StatusGaps       []string
	SelfTestFailures []string
	LoudLines        []string
	DayLogWarnings   []string
	Drops            []string
}

// degradationEvidence is what the product said that it had not said before the cut. Each field
// names its own surface, because R4-1 distinguishes them: a day-log Warn is explicit but weaker than
// a LOUD line or a status gap, and Task 5's doctor and Task 7's docs need to tell them apart.
type degradationEvidence struct {
	// StatusGaps are rows `qompack status --json` gained, taken WITH THE DAEMON UP.
	StatusGaps []string
	// SelfTestFailures are checks `qompack self-test --json` newly marks not-ok.
	SelfTestFailures []string
	// LoudLines are lines `.qompack/logs/LOUD.log` gained.
	LoudLines []string
	// DayLogWarnings are warn/error lines `.qompack/logs/qompack-<day>.log` gained.
	DayLogWarnings []string
	// Drops are checkpoint DropEntry kinds the artifacts gained.
	Drops []string
}

// Explicit reports whether the product said anything NEW about a degraded state.
func (e degradationEvidence) Explicit() bool { return len(e.Surfaces()) > 0 }

// Surfaces names the surfaces that carried something new, strongest first. The record quotes this
// so a reader knows WHERE the product said it, not merely that it did.
func (e degradationEvidence) Surfaces() []string {
	var out []string
	if len(e.LoudLines) > 0 {
		out = append(out, "LOUD.log")
	}
	if len(e.StatusGaps) > 0 {
		out = append(out, "status --json")
	}
	if len(e.SelfTestFailures) > 0 {
		out = append(out, "self-test --json")
	}
	if len(e.Drops) > 0 {
		out = append(out, "checkpoint DropEntry")
	}
	if len(e.DayLogWarnings) > 0 {
		out = append(out, "day log (warn)")
	}
	return out
}

// String renders the evidence for a record's Detail, naming the first line of each surface that
// carried something. A count alone would not let anyone check the claim.
func (e degradationEvidence) String() string {
	if !e.Explicit() {
		return "nothing new on LOUD.log, status --json, self-test --json, the day log or a DropEntry"
	}
	parts := []string{"surfaces " + strings.Join(e.Surfaces(), "+")}
	for label, lines := range map[string][]string{
		"loud": e.LoudLines, "status": e.StatusGaps, "self-test": e.SelfTestFailures,
		"drop": e.Drops, "daylog": e.DayLogWarnings,
	} {
		if len(lines) > 0 {
			parts = append(parts, fmt.Sprintf("%s[%d]: %s", label, len(lines), snippet(lines[0])))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// snapshotDegradation reads all five surfaces. The two CLI surfaces are asked WITH A DAEMON UP
// whenever one is up, and that is load-bearing rather than incidental: `StatusReport.Snapshot` is
// the daemon's, and with the daemon stopped internal/commands falls back to the disk source and
// leaves Snapshot nil — which made the whole Snapshot branch of statusGaps (spool files, the LOUD
// tail, every degradation counter including `store.index.badline`) dead code in all 40 rows of
// round 1, and turned "the product reported nothing" into "nobody asked while it could answer".
//
// Neither subcommand's exit code is asserted. `self-test` is the one command §2.3 lets exit
// non-zero, and a non-zero exit there is the evidence rather than a harness failure.
func snapshotDegradation(t *testing.T, b bundle, p project) degradationSnapshot {
	t.Helper()
	statusOut, _, _ := run(t, b.Bin, p.Root, []string{"status", "--json"}, nil, p.Env)
	selfOut, _, _ := run(t, b.Bin, p.Root, []string{"self-test", "--json"}, nil, p.Env)
	return degradationSnapshot{
		StatusGaps:       statusGaps(statusOut),
		SelfTestFailures: selfTestFailures(selfOut),
		LoudLines:        loudLines(p.Root),
		DayLogWarnings:   dayLogWarnings(p.Root),
		Drops:            checkpointDropKinds(t, p.Root),
	}
}

// degradationSince is the delta between two snapshots: what the product says now that it did not
// say before the cut.
func degradationSince(before, after degradationSnapshot) degradationEvidence {
	return degradationEvidence{
		StatusGaps:       newLines(before.StatusGaps, after.StatusGaps),
		SelfTestFailures: newLines(before.SelfTestFailures, after.SelfTestFailures),
		LoudLines:        newLines(before.LoudLines, after.LoudLines),
		DayLogWarnings:   newLines(before.DayLogWarnings, after.DayLogWarnings),
		Drops:            newLines(before.Drops, after.Drops),
	}
}

// newLines returns the members of after that before did not already carry.
//
// It is a MULTISET difference, not a set one: a surface that carried one `store.index.badline` line
// before the cut and two after gained one, and a set difference would report nothing at all.
func newLines(before, after []string) []string {
	seen := map[string]int{}
	for _, l := range before {
		seen[l]++
	}
	var out []string
	for _, l := range after {
		if seen[l] > 0 {
			seen[l]--
			continue
		}
		out = append(out, l)
	}
	return out
}

// statusReport is the subset of internal/commands' StatusReport this package reads. Everything else
// the document carries is internal/commands' own test's business.
type statusReport struct {
	Snapshot *struct {
		Counters   map[string]int64 `json:"counters"`
		SpoolFiles int              `json:"spool_files"`
		LoudTail   []string         `json:"loud_tail"`
	} `json:"snapshot"`
	Primary struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"primary"`
	Contract []struct {
		ID       string `json:"id"`
		OK       bool   `json:"ok"`
		Observed string `json:"observed"`
	} `json:"contract"`
}

// statusEnvelope is the document `qompack status --json` actually writes: the slash-command
// envelope {schema, command, ok, data}, with the StatusReport inside `data`.
//
// Reading the top level as the report is the mistake this type exists to stop, and it is not a
// harmless one: every field the report carries would silently be its zero value, so a project with
// an unreachable daemon and a failing contract row would present as a clean `available` status and
// this package would record "the product reported nothing" about a product that reported plenty.
type statusEnvelope struct {
	OK   bool         `json:"ok"`
	Data statusReport `json:"data"`
}

// statusGaps extracts every non-clean statement `status --json` made: an unavailable or stale
// primary source, a failing contract row, an unreplayed spool, a Loud tail, or a store/daemon
// counter naming a degradation.
func statusGaps(stdout []byte) []string {
	var env statusEnvelope
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &env); err != nil {
		// A status report this package cannot parse is itself worth surfacing, but it is NOT
		// evidence of degradation: saying so would let an unparseable report stand in for the
		// product having reported the gap.
		return nil
	}
	rep := env.Data
	var gaps []string
	if s := rep.Primary.Status; s != "" && s != "available" {
		gaps = append(gaps, "primary."+s+": "+rep.Primary.Reason)
	}
	for _, c := range rep.Contract {
		if !c.OK {
			gaps = append(gaps, "contract."+c.ID+": "+c.Observed)
		}
	}
	if rep.Snapshot == nil {
		return gaps
	}
	if rep.Snapshot.SpoolFiles > 0 {
		gaps = append(gaps, fmt.Sprintf("spool_files=%d", rep.Snapshot.SpoolFiles))
	}
	for _, line := range rep.Snapshot.LoudTail {
		gaps = append(gaps, "loud_tail: "+line)
	}
	for name, v := range rep.Snapshot.Counters {
		if v > 0 && degradationCounter(name) {
			gaps = append(gaps, fmt.Sprintf("counter %s=%d", name, v))
		}
	}
	sort.Strings(gaps)
	return gaps
}

// degradationCounterInfixes are the substrings that make a counter a statement about degradation
// rather than about throughput. Matching on a fixed list rather than on "any counter" is what keeps
// an ordinary busy session from reading as a reported gap.
var degradationCounterInfixes = []string{
	"quarantin", "badline", "badclass", "mismatch", "corrupt", "drop", "gap", "refus",
	"degrad", "unavailable", "denied", "unsynced", "unacknowledged", "unleased", "unadmitted",
	"recovery", "drift", "truncat",
}

func degradationCounter(name string) bool {
	lower := strings.ToLower(name)
	for _, infix := range degradationCounterInfixes {
		if strings.Contains(lower, infix) {
			return true
		}
	}
	return false
}

// selfTestReport is the {checks, mode, exit} document `self-test --json` writes.
type selfTestReport struct {
	Checks []struct {
		ID string `json:"id"`
		OK bool   `json:"ok"`
		// Severity is contract.Severity, which marshals as its NUMBER. Declaring it as a string
		// here would not merely lose the field: encoding/json fails the whole document on the type
		// mismatch, and every check would vanish.
		Severity int    `json:"severity"`
		Observed string `json:"observed"`
	} `json:"checks"`
	Mode string `json:"mode"`
	Exit int    `json:"exit"`
}

// selfTestFailures returns the id of every check self-test marked not-ok.
func selfTestFailures(stdout []byte) []string {
	var rep selfTestReport
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &rep); err != nil {
		return nil
	}
	var out []string
	for _, c := range rep.Checks {
		if !c.OK {
			out = append(out, fmt.Sprintf("%s(severity %d): %s", c.ID, c.Severity, c.Observed))
		}
	}
	sort.Strings(out)
	return out
}

// loudLines returns every line of .qompack/logs/LOUD.log. The file is append-only and never
// rotated, so a count taken before a cut and one taken after bound exactly what the cut produced.
func loudLines(root string) []string {
	b, err := paths.ReadFileShared(filepath.Join(paths.Of(root).Logs, "LOUD.log"))
	if err != nil {
		return nil
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// checkpointDropKinds returns every DropEntry kind recorded in the checkpoint artifacts on disk.
// A drop is the product naming what it left out, "identified well enough to be asked for again",
// which is the strongest form of explicit incompleteness the design has.
func checkpointDropKinds(t *testing.T, root string) []string {
	t.Helper()
	l := paths.Of(root)
	seen := map[string]bool{}
	for _, name := range relativeFiles(l.Checkpoints) {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := paths.ReadFileShared(filepath.Join(l.Checkpoints, name))
		if err != nil {
			continue
		}
		var doc struct {
			Drops []struct {
				Kind string `json:"kind"`
				ID   string `json:"id"`
			} `json:"dropped"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			continue
		}
		for _, d := range doc.Drops {
			seen[d.Kind+"("+d.ID+")"] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// The verdict
// ---------------------------------------------------------------------------

// judgeRecovery settles one row: it decides between the four outcomes, writes the evidence record,
// and fails the Go test for exactly one of them.
//
// The decision, in order:
//
//  1. No NEWLY dangling reference and the product kept recording => `recovered`.
//  2. No newly dangling reference and the product did NOT keep recording, but it said so =>
//     `explicit_incomplete`.
//  3. A newly dangling reference that the product REPORTS on one of the four surfaces =>
//     `explicit_incomplete`. §12.3's own answer for a corrupt object is "quarantine, Loud, continue,
//     fsck repairs": the reference is still broken and the operator has been told.
//  4. A newly dangling reference that NOTHING reports => `failed`, and the Go test fails. That is
//     SP17-M7-04 exactly, and it is the one class this package will not let pass silently.
//
// `owner` names the package a finding belongs to, because Task 6 collects them by owner and a
// finding with no owner is a complaint.
// known is the returned finding a row is expected to reproduce on today's tree: the id Task 6
// collects it under, or "" when the row is expected to recover cleanly.
//
// It is a PINNED CHARACTERIZATION, and it is what lets this package be both honest and committable.
// Defects found here are RETURNED, never fixed from here (Role D's rule), so a red suite would be a
// red suite forever — and a suite that simply never failed would not be evidence of anything. So:
//   - a row that fails with a pinned finding is RECORDED and logged as a returned finding, and the
//     Go test stays green, because that divergence is already on Task 6's desk;
//   - a row that fails with NO pin fails the Go test, because it is a regression or a defect nobody
//     has been told about — the acceptance row SP17-M7-04 in force;
//   - a row that RECOVERS while carrying a pin fails the Go test too, because a pin that no longer
//     describes the product is a claim this package is making about a defect that is gone.
func judgeRecovery(t *testing.T, rec Record, before, after auditResult, recording bool, ev degradationEvidence, owner, known string) {
	t.Helper()

	newRefs := newlyDangling(before, after)
	named := newlyReported(before, after)
	rec.Detail = strings.TrimSpace(rec.Detail + fmt.Sprintf(
		"\naudit before %s\naudit after  %s\ndegradation (new since the cut): %s\n"+
			"newly dangling: %s\nnewly reported: %s\nremaining dangling after recovery: %s",
		before, after, ev, describeRefs(newRefs), describeRefs(named), describeRefs(after.Dangling)))

	switch {
	case len(newRefs) == 0 && len(named) > 0:
		rec.Outcome = OutcomeExplicitIncomplete
		rec.Reason = fmt.Sprintf("the cut introduced no silently dangling reference; what it did "+
			"leave, the audit can name as reported: %s", describeRefs(named))
	case len(newRefs) == 0 && recording && len(after.Dangling) == 0:
		rec.Outcome = OutcomeRecovered
		rec.Reason = "the cut introduced no dangling reference, none remains anywhere under " +
			".qompack/, and the product went on recording: the recovery session's own observation " +
			"was indexed."
	case len(newRefs) == 0 && recording:
		// The brief asks for `dangling_after == 0` as well as for the delta. Recording resumed and
		// the cut introduced nothing, but something that was already there still does not resolve
		// and the audit cannot name it as reported — which is a weaker claim than `recovered` and
		// must not be recorded as one.
		rec.Outcome = OutcomeExplicitIncomplete
		rec.Reason = fmt.Sprintf("the cut introduced no dangling reference and recording resumed, "+
			"but %d reference(s) that predate the cut still do not resolve: %s",
			len(after.Dangling), describeRefs(after.Dangling))
	case len(newRefs) == 0 && ev.Explicit():
		rec.Outcome = OutcomeExplicitIncomplete
		rec.Reason = "the cut introduced no dangling reference; recording did not resume within the " +
			"bound and the product says so on " + strings.Join(ev.Surfaces(), "+") + ": " + ev.String()
	case len(newRefs) == 0:
		rec.Outcome = OutcomeFailed
		rec.Reason = fmt.Sprintf("the cut introduced no dangling reference, but the recovery session's "+
			"observation was never indexed and no surface gained anything. Owner: %s.", owner)
	case ev.Explicit():
		rec.Outcome = OutcomeExplicitIncomplete
		rec.Reason = fmt.Sprintf("%d reference(s) the cut introduced still do not resolve, and the "+
			"product reports the degradation on %s: %s",
			len(newRefs), strings.Join(ev.Surfaces(), "+"), ev.String())
	default:
		rec.Outcome = OutcomeFailed
		rec.Reason = fmt.Sprintf("%d reference(s) the cut introduced do not resolve and NO surface "+
			"gained anything about it — not LOUD.log, not status --json taken with the recovery "+
			"daemon up, not self-test --json, not the day log, not a DropEntry. Owner: %s.",
			len(newRefs), owner)
	}
	judgePin(t, &rec, owner, known)
	writeRecord(t, rec)
}

// judgePin applies R4-4: a pin must fail the test when the product changes in EITHER direction.
//
// Round 1 only had the two arms that mattered when a pinned row FAILED. That left the third case
// silent: a pinned row that came back `explicit_incomplete` — the product now reports the gap —
// recorded the improvement and said nothing, so a fix would have decayed the pin into a false claim
// with no one told. Every outcome other than the pinned `failed` is now a test failure naming what
// changed.
func judgePin(t *testing.T, rec *Record, owner, known string) {
	t.Helper()
	switch {
	case rec.Outcome == OutcomeFailed && known == "":
		t.Errorf("fault %s: SP17-M7-04 is not met and no finding is pinned for this row. Either the "+
			"product regressed or a defect nobody has been told about was just found; return it to "+
			"Task 6 with its owner (%s) and pin it.\n%s\n%s", rec.Name, owner, rec.Reason, rec.Detail)
	case rec.Outcome == OutcomeFailed:
		rec.Reason += " Returned as " + known + "."
		t.Logf("fault %s: reproduced the returned finding %s (owner %s); the record is the "+
			"deliverable and nothing here fixes it", rec.Name, known, owner)
	case known != "":
		t.Errorf("fault %s: this row came back %s while it still pins the finding %s. The product "+
			"changed — most likely it was FIXED — and a pin that no longer describes it is a false "+
			"claim in the evidence table. Delete the pin from the row, from commit4-evidence.md §6 "+
			"and from commit4-recovery-proposal.md.\n%s", rec.Name, rec.Outcome, known, rec.Detail)
	}
}

// recordOutcome writes a record whose outcome the case decided for itself — the lifecycle, child
// and resource rows, whose question is not "does a reference dangle" but "did the product answer
// correctly". It still refuses an audit that found something nothing reports.
func recordOutcome(t *testing.T, rec Record, outcome Outcome, reason string) {
	t.Helper()
	rec.Outcome = outcome
	rec.Reason = reason
	writeRecord(t, rec)
}

// requireAuditClean asserts an audit found nothing dangling, and returns what it found so the caller
// can put it in a record. It is the lighter form the non-publication rows use: they are not cutting
// a publication boundary, so anything dangling afterwards is unexpected by construction.
func requireAuditClean(t *testing.T, name string, a auditResult) {
	t.Helper()
	if len(a.Dangling) == 0 {
		return
	}
	t.Errorf("fault %s: the store audit found %d dangling reference(s) after a case that should have "+
		"left none: %s", name, len(a.Dangling), describeRefs(a.Dangling))
}

// dayLogWarnings returns every warn- or error-level line the day logs carry.
//
// It is the surface round 1 never read, and two findings turned on it: internal/store logs
// `store: skipped malformed index lines` at Warn (roots.go and the tool_use index) when a torn line
// is stepped over, so a truncated index IS reported — in the day log, and nowhere stronger.
//
// Every `qompack-*.log` in the directory is read, rotated files included: the sink rotates at 10 MB
// into `qompack-<day>.<n>.log`, and a run that happened to rotate would otherwise lose the lines it
// had already written. The level is parsed out of the `level=` field of logging's own
// `ts=… level=… msg="…"` format rather than matched anywhere in the line, so a message that merely
// contains the word does not count as one.
func dayLogWarnings(root string) []string {
	var out []string
	dir := paths.Of(root).Logs
	for _, name := range relativeFiles(dir) {
		if !strings.HasPrefix(name, dayLogNamePrefix) || !strings.HasSuffix(name, ".log") {
			continue
		}
		b, err := paths.ReadFileShared(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			switch logLevelOf(line) {
			case "warn", "error", "loud":
				out = append(out, name+": "+line)
			}
		}
	}
	sort.Strings(out)
	return out
}

// logLevelOf extracts the `level=` field of one logging line, or "" when there is none.
func logLevelOf(line string) string {
	_, rest, ok := strings.Cut(line, " level=")
	if !ok {
		return ""
	}
	lvl, _, _ := strings.Cut(rest, " ")
	return lvl
}

// primeArtifactDir performs a collecting run's prune before any case runs, so the directory is empty
// from the start of the run rather than from the first record it happens to write. TestMain calls it.
//
// It is a no-op for an ordinary `go test`, where records go to a per-test temp directory and there
// is nothing to prune.
func primeArtifactDir() {
	if os.Getenv(artifactsEnvKey) == "" {
		return
	}
	collectOnce.Do(prepareCollectDir)
	if collectErr != nil {
		fmt.Fprintf(os.Stderr, "fault: %v\n", collectErr)
		os.Exit(1)
	}
}

// namingEvidence narrows evidence to the lines that mention one of tokens, which is how a row says
// what would constitute the product reporting ITS cut rather than merely saying something.
//
// It is the day-log counterpart of the drop delta, and it was learned the same way. Deltaing the
// surfaces removed the pre-existing `pointer_git_unavailable()` drop, but a recovery session emits a
// warn of its own on every run — "store: segment closed without a tokens feature" — and three rows
// were promoted to `explicit_incomplete` by a line that has nothing to do with a truncated index or
// an unjoined capture. New and about-the-cut are different questions, and only the second one
// settles a row.
//
// The tokens are declared per row in the matrix, in the open, and the record carries BOTH the whole
// new-evidence set and this subset — so a reader can see what the product said, what of it named the
// cut, and judge the choice of tokens rather than take it on trust.
//
// An EMPTY token list is a test failure rather than a pass-through, and that is this round's fix.
// A row with no tokens accepts ANY new line as the product reporting its cut, and the five resource
// rows were exactly that: the only thing `resource_spool_readonly` gained was the universal
// segment-tokens warn, which says nothing about a denied spool directory. Declining to say what
// would count is not an option a row has.
func namingEvidence(t *testing.T, e degradationEvidence, tokens []string) degradationEvidence {
	t.Helper()
	if len(tokens) == 0 {
		t.Fatalf("fault: this row reached a verdict without declaring the tokens that would " +
			"constitute the product reporting ITS cut; with none, any new line counts and the " +
			"verdict is about whatever the run happened to log")
	}
	if !registeredNames(tokens) {
		t.Fatalf("fault: this row narrowed its evidence with a token list %v that registerNames "+
			"never saw, so TestFault_LinesNamingIgnoresTheUniversalWarn is not auditing it. Declare "+
			"the list through registerNames(<record name>, …) — an unaudited token is how `ack` "+
			"matched every day-log line in the run.", tokens)
	}
	return e.naming(tokens)
}

var (
	// namingMu guards namingRegistry, which the matrix tests write at declaration and the audit
	// test reads.
	namingMu sync.Mutex
	// namingRegistry is every token list this package declares, keyed by the record that declares
	// it. It replaces a hand-maintained list in the audit test, which tied nothing to the call
	// sites: a new row could be added, narrowed and judged without ever being audited.
	namingRegistry = map[string][]string{}
)

// registerNames records a row's token list under its record name and returns the list unchanged, so
// it can be used inline where the list is DECLARED. namingEvidence refuses a list that did not come
// through here, which is what closes the loop: a row cannot reach a verdict unaudited.
func registerNames(row string, tokens []string) []string {
	namingMu.Lock()
	defer namingMu.Unlock()
	namingRegistry[row] = tokens
	return tokens
}

// registeredNames reports whether tokens is (or begins with) a list some row registered. The prefix
// form is for the one row that appends the cut's own identifiers — a tool_use id and a chunk hash
// it cannot know until it runs — to a declared, audited base.
func registeredNames(tokens []string) bool {
	namingMu.Lock()
	defer namingMu.Unlock()
	joined := strings.Join(tokens, "\x00")
	for _, declared := range namingRegistry {
		if key := strings.Join(declared, "\x00"); joined == key || strings.HasPrefix(joined, key+"\x00") {
			return true
		}
	}
	return false
}

// naming is the filter itself.
func (e degradationEvidence) naming(tokens []string) degradationEvidence {
	return degradationEvidence{
		StatusGaps:       linesNaming(e.StatusGaps, tokens),
		SelfTestFailures: linesNaming(e.SelfTestFailures, tokens),
		LoudLines:        linesNaming(e.LoudLines, tokens),
		DayLogWarnings:   linesNaming(e.DayLogWarnings, tokens),
		Drops:            linesNaming(e.Drops, tokens),
	}
}

// linesNaming keeps the lines that contain one of tokens, case-insensitively, after the identifying
// fields are stripped.
//
// The strip is not cosmetic, and it has cost this package two rounds. A row's session id is derived
// from its own name, so the unrelated recovery-session warn line carries
// `session=sess-fault-capture-sidecar-stage-one-only-r` — and a row looking for "sidecar" matched
// its own name inside a message about segment tokens. The day-log FILE NAME is worse: every line
// dayLogWarnings returns begins `qompack-<day>.log: `, and "qompack" ends in "ack", so one token of
// three characters made a whole row's filter vacuous. Matching against the message and its
// non-identifying fields is what makes the question "did the product name this failure" rather than
// "does the line mention this test, or this program".
//
// TestFault_LinesNamingIgnoresTheUniversalWarn is the guard: it feeds the line every recovery
// session emits to every row's token list and requires no match.
func linesNaming(lines, tokens []string) []string {
	var out []string
	for _, l := range lines {
		lower := strings.ToLower(stripIdentifyingFields(l))
		for _, tok := range tokens {
			if tok != "" && strings.Contains(lower, strings.ToLower(tok)) {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

// strippedFields are the log keys whose values name this test and nothing else. A row's session id
// is derived from its own name, so `session=sess-fault-capture-sidecar-stage-one-only-r` let a row
// looking for "sidecar" match itself inside a message about segment tokens.
var strippedFields = []string{"session="}

// basenameFields are the log keys whose values are PATHS. They are reduced to their last segment
// rather than removed, which corrects the blanket strip this filter used to do.
//
// Removing them outright went too far: `store: skipped malformed index lines file=roots.jsonl` is
// the product naming the very file the cut tore, and with `file=` gone the row survived on the word
// "malformed" alone — so a row could not distinguish its own index file from any other. What has to
// go is the DIRECTORY, because that is the fixture identity: a `qompack-fault-<n>` temp root under a
// path naming this user, carrying "qompack", "index", "proj" and the run's own digits.
var basenameFields = []string{"file=", "location="}

// dayLogNamePrefix is the first component of every day-log file name, and dayLogWarnings puts that
// name in front of each line it returns.
const dayLogNamePrefix = "qompack-"

// stripIdentifyingFields reduces a line to the part that says what HAPPENED: the day-log file name
// dayLogWarnings prefixed it with is dropped, session ids go, and path values keep only their base
// name.
func stripIdentifyingFields(line string) string {
	out := stripLogFilePrefix(line)
	for _, key := range strippedFields {
		out = rewriteFieldValue(out, key, func(string) string { return "" })
	}
	for _, key := range basenameFields {
		out = rewriteFieldValue(out, key, pathBase)
	}
	return out
}

// stripLogFilePrefix removes the `qompack-<day>.log: ` that dayLogWarnings puts in front of a line.
//
// It is not cosmetic, and the defect it fixes is worth naming: "qompack" ENDS IN "ack", so the
// delivery-seal row's `ack` token matched every day-log line in the run — including the universal
// `store: segment closed without a tokens feature` warn a recovery session emits whatever was cut.
// The filename is the test harness speaking, not the product, so it is stripped before any token is
// compared. dayLogWarnings still carries it in the record, where a reader needs to know which
// rotated file a line came from.
func stripLogFilePrefix(line string) string {
	if !strings.HasPrefix(line, dayLogNamePrefix) {
		return line
	}
	_, rest, ok := strings.Cut(line, ".log: ")
	if !ok {
		return line
	}
	return rest
}

// rewriteFieldValue passes the value of every `key<value>` pair in line through rewrite.
func rewriteFieldValue(line, key string, rewrite func(string) string) string {
	out, from := line, 0
	for {
		i := strings.Index(out[from:], key)
		if i < 0 {
			return out
		}
		i += from
		value, tail := fieldValue(out[i+len(key):])
		replaced := rewrite(value)
		out = out[:i] + replaced + tail
		from = i + len(replaced)
	}
}

// fieldValue splits the text after a `key=` into that field's value and the rest of the line, in
// internal/logging's OWN format rather than an approximation of it.
//
// `writeValue` (logging/logger.go:160-166) emits `strconv.Quote(v)` for a value that is empty or
// contains a space or an `=`, and the bare value otherwise. Cutting at the first space — which this
// did — is therefore wrong for exactly the values that matter: on a host whose home directory has a
// space in it, `file="C:\\Users\\First Last\\…\\.qompack\\index\\tool_use.jsonl"` was cut after
// `First`, and the whole tail (carrying `qompack-fault-`, `index`, `objects`) stayed matchable —
// which is N1 and N6 back again, on somebody else's machine and not on this one.
func fieldValue(rest string) (value, tail string) {
	if strings.HasPrefix(rest, `"`) {
		if end := closingQuote(rest); end > 0 {
			if v, err := strconv.Unquote(rest[:end+1]); err == nil {
				return v, rest[end+1:]
			}
		}
	}
	if end := strings.IndexByte(rest, ' '); end >= 0 {
		return rest[:end], rest[end:]
	}
	return rest, ""
}

// closingQuote returns the index of the quote closing the one at s[0], honouring the backslash
// escapes strconv.Quote writes, or -1 when the line is truncated before it (snippet does truncate).
func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// pathBase is the last segment of a path-shaped value. Both separators are honoured because the
// line may carry a Windows path on this host and a POSIX one on another, and filepath.Base only
// knows the one it was compiled for.
func pathBase(value string) string {
	if i := strings.LastIndexAny(value, `/\`); i >= 0 {
		return value[i+1:]
	}
	return value
}

// ---------------------------------------------------------------------------
// The token audit
// ---------------------------------------------------------------------------

// universalWarnFor is the day-log line a recovery session emits on EVERY run whatever was cut,
// rendered exactly as dayLogWarnings returns it: the log's file name, then the line, then the row's
// own session id. Both of those non-product parts have made a row's filter vacuous — the session id
// let a row match its own name, and "qompack" ends in "ack".
func universalWarnFor(row string) string {
	return "qompack-20260915.log: ts=2026-09-15T15:34:51.8334737Z level=warn " +
		`msg="store: segment closed without a tokens feature; Segment.Tokens stays zero permanently" ` +
		"segment=2 session=" + string(sessionID(row)) + "-r"
}

// incidentalLines are lines no cut in this matrix produces, each carrying a word that a short token
// hides inside. They are the three shapes that have already gone wrong or nearly did: the program's
// own name in an error string (`ack`), a word with a token as its tail (`lease` in "released"), and
// a word with a token as its head (`seq` in "sequence").
var incidentalLines = []string{
	`qompack-20260915.log: ts=2026-09-15T15:34:51Z level=warn msg="ipc: request failed" ` +
		`err="qompack: daemon unreachable"`,
	`qompack-20260915.log: ts=2026-09-15T15:34:52Z level=warn msg="daemon: lock released early"`,
	`qompack-20260915.log: ts=2026-09-15T15:34:53Z level=warn msg="observer: sequence advanced"`,
	// The contract degradation, verbatim from lifecycle_frontier_state_is_explicit's record, which
	// cuts nothing at all and carries it five times. `state_bin_corrupt` briefly claimed it by
	// declaring `contract`, `degrad` and `passive`: the causal chain is plausible but the line is
	// not the product reporting THAT cut, and this entry is what stops any row claiming it again.
	`qompack-20260915.log: ts=2026-09-15T15:34:54Z level=loud ` +
		`msg="contract: degrading to passive recording" mode=degraded-passive ` +
		`reason="critical host-contract failure: hook.additional_context_delivered (expected ` +
		`\"additionalContext reaches the transcript\", observed \"sentinel not found after two ` +
		`chances\")" failed=hook.additional_context_delivered`,
	// The same line as a hook-side path a host with a SPACE in its home directory produces. The
	// value is quoted, which is logging's own rule for a value containing a space, and cutting it at
	// the first space leaves `Last\…\index\tool_use.jsonl` matchable.
	`qompack-20260915.log: ts=2026-09-15T15:34:55Z level=warn msg="store: opened" ` +
		`file="C:\\Users\\First Last\\AppData\\Local\\Temp\\qompack-fault-77\\proj\\.qompack\\run\\daemon.hb"`,
}

// rowNamingTokens is every declared token list, read out of the registry rather than hand-listed.
//
// The two matrix tables register their rows when they are built, so they are called first; the
// standalone lists register at package initialisation. A row whose tokens are assembled at runtime
// (the checkpoint-drop row appends the cut's own tool_use id and chunk hash) contributes its
// declared base, which is the part that can go wrong without anyone noticing.
func rowNamingTokens() map[string][]string {
	publicationBoundaries()
	resourceWriteFailures()

	namingMu.Lock()
	defer namingMu.Unlock()
	out := make(map[string][]string, len(namingRegistry))
	for row, tokens := range namingRegistry {
		out[row] = tokens
	}
	return out
}

// TestFault_LinesNamingIgnoresTheUniversalWarn is the regression guard for a defect that shipped
// twice: a token that the harness, rather than the product, puts on every line.
//
// `delivery_seal_torn_slot` declared `ack`, dayLogWarnings prefixes each line with
// `qompack-<day>.log: `, and "qompack" ends in "ack" — so that row's "of which naming this cut"
// subset was the whole day log, including the universal segment-tokens warn C2's fix exists to
// exclude. The audit is over EVERY row rather than that one, because the next such token will be in
// a different list.
func TestFault_LinesNamingIgnoresTheUniversalWarn(t *testing.T) {
	all := rowNamingTokens()
	if len(all) == 0 {
		t.Fatal("fault: no row registered any naming tokens, so this audit is auditing nothing")
	}
	// The registry is what ties a declaration to this audit, and namingEvidence refuses a list it
	// does not hold. Both halves are asserted, so neither can rot into a formality.
	for row, tokens := range all {
		if !registeredNames(tokens) {
			t.Errorf("fault %s: its own registered tokens do not read back", row)
		}
	}
	if registeredNames([]string{"a token no row declared"}) {
		t.Error("fault: registeredNames accepts an undeclared list, so namingEvidence would too " +
			"and a new row could reach a verdict without this audit ever seeing its tokens")
	}
	t.Logf("fault: auditing %d registered token list(s)", len(all))

	for row, tokens := range all {
		if len(tokens) == 0 {
			t.Errorf("fault %s: declares no naming tokens, so any new line would count as the "+
				"product reporting its cut", row)
			continue
		}
		lines := append([]string{universalWarnFor(row)}, incidentalLines...)
		if got := linesNaming(lines, tokens); len(got) > 0 {
			t.Errorf("fault %s: its tokens %v match a line no cut produced — %q. A token must be one "+
				"only the product's report of THIS failure can contain; check it against the day-log "+
				"file name (\"qompack\" contains \"ack\"), the row's own session id, and the warn a "+
				"recovery session emits whatever was cut.", row, tokens, got[0])
		}
	}
}

// TestFault_LinesNamingKeepsTheFileAValueNames is the other half, and it is the reason `file=` is
// reduced to its base name rather than removed: the product naming the very file it stepped over is
// the strongest evidence a torn-index row can get, and the blanket strip threw it away.
func TestFault_LinesNamingKeepsTheFileAValueNames(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		tokens     []string
	}{
		{
			"a bare file name survives",
			`qompack-20260915.log: ts=2026-09-15T15:34:18Z level=warn ` +
				`msg="store: skipped malformed index lines" file=roots.jsonl lines=1`,
			[]string{"roots.jsonl"},
		},
		{
			"a full path keeps its base name and loses the fixture directory",
			`qompack-20260915.log: ts=2026-09-15T15:34:21Z level=warn ` +
				`msg="store: skipped malformed tool_use index lines" ` +
				`file=C:\Users\q\AppData\Local\Temp\qompack-fault-228\proj\.qompack\index\tool_use.jsonl lines=1`,
			[]string{"tool_use.jsonl"},
		},
		{
			// A host whose home directory has a space in it. logging's writeValue quotes any value
			// containing a space, so the value must be parsed as strconv.Quote wrote it rather than
			// cut at the first space — which used to leave `Last\…\index\tool_use.jsonl` behind.
			"a QUOTED path with a space still yields its base name",
			`qompack-20260915.log: ts=2026-09-15T15:34:21Z level=warn ` +
				`msg="store: skipped malformed tool_use index lines" ` +
				`file="C:\\Users\\First Last\\AppData\\Local\\Temp\\qompack-fault-228\\proj\\.qompack\\index\\tool_use.jsonl" lines=1`,
			[]string{"tool_use.jsonl"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := linesNaming([]string{tc.line}, tc.tokens); len(got) != 1 {
				t.Errorf("fault: %v did not match %q after stripping; the product named the file "+
					"and the filter threw it away", tc.tokens, tc.line)
			}
		})
	}

	// And the directory really is gone, bare and quoted alike: a token that only the fixture path
	// carries must not match either shape.
	for name, line := range map[string]string{
		"bare": `qompack-20260915.log: ts=2026-09-15T15:34:21Z level=warn msg="store: skipped ` +
			`malformed tool_use index lines" file=C:\Users\q\Temp\qompack-fault-228\proj\.qompack\index\tool_use.jsonl`,
		"quoted": `qompack-20260915.log: ts=2026-09-15T15:34:21Z level=warn msg="store: skipped ` +
			`malformed tool_use index lines" file="C:\\Users\\First Last\\Temp\\qompack-fault-228\\proj\\.qompack\\index\\tool_use.jsonl"`,
	} {
		got := linesNaming([]string{line}, []string{"qompack-fault-", `\index\`, "First Last", "Temp"})
		if len(got) > 0 {
			t.Errorf("fault: the fixture directory is still matchable in the %s form: %q", name, got[0])
		}
	}
}
