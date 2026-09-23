package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
)

// `qompack fsck` is the integrity command 00-ARCHITECTURE.md §12.3 names: it verifies identities,
// references, manifests, delta bases and roots, and it does so WITHOUT converting a missing or
// unknown fidelity into an exact one.
//
// Three rules shape the whole file, and each was learned by measurement rather than reasoning
// (plans/sdd/V6-SP-17-packaging-hardening-and-release/commit4-evidence.md §8):
//
//  1. A hash resolves ON DISK, never through store.Has. FSStore.Has answers from an in-memory
//     chunkSet built from index/roots.jsonl at Open and never stats a file, so an object deleted
//     while its index line survives reads as held — and every row that depended on it becomes
//     vacuous. fsckHashHeld therefore stats the object file, falling back to the root's own chunk
//     list parsed from the same index line.
//  2. The index files are scanned LINE BY LINE by this file rather than through the store's
//     loaders. store.loadRoots collapses every malformed line into two counters (store.index.badline
//     and store.index.badclass); fsck has to NAME the line.
//  3. Evidence is never swept. Capture sidecars, quarantined objects and sketch `.corrupt.<ms>`
//     files are reported and left exactly where they are, and the default run repairs nothing at
//     all — plan §3's "no destructive default cleanup".
//
// It reads with the daemon RUNNING as well as stopped. When a daemon holds the lock the answer is
// a snapshot of a moving target and the report says so, because refusing to run is a worse answer
// than a qualified one for a user who is trying to find out what is wrong.

// fsckSchema is the version of the `--json` document. A reader's only question is "can I interpret
// this?", so it is an integer to compare, matching commands.EnvelopeSchema's own reasoning.
const fsckSchema = 1

// fsckMaxDetail bounds one row's detail list. A store with a hundred thousand damaged objects
// produces a report a person can still read, and the row's Count carries the total that the list
// does not.
const fsckMaxDetail = 20

// fsckMaxLineBytes bounds one line of an index/journal file fsck scans, so a single unterminated
// line cannot be read into memory without limit. It is a LINE bound and not an object bound: the
// per-object limits are the store's own (fsckObjectSizeLimit).
const fsckMaxLineBytes = 2 * store.MaxPutBytes

// fsckObjectSizeLimit is the size past which the STORE'S OWN reader refuses the object file named
// name, so that the objects row names the limit that actually bites rather than a bound fsck
// invented. readObjectFile (internal/store/objects.go) applies exactly this split: the encoder's
// worst-case encoded size for a .zst candidate, and the plaintext MaxPutBytes for a bare one.
//
// Calling a file acceptable that the store would then refuse is a defect the report does not have,
// which is the failure the old single generous bound produced for every compressed object between
// the encoded limit and twice MaxPutBytes.
func fsckObjectSizeLimit(name string) int64 {
	if strings.HasSuffix(name, fsckObjectSuffix) {
		return store.EncodedObjectLimit()
	}
	return int64(store.MaxPutBytes)
}

// fsckKnownRecordVersion is the version this build's readers accept in the migrate records and in
// index/files.json's view header (internal/store/migrate.go's six *Version constants and
// internal/store/fsstore.go's indexRecordVersion are all 1). They are unexported there;
// TestFsck_TheViewVersionMatchesTheStoresOwn pins that this value still agrees with what the store
// actually writes, so a bump cannot pass silently.
const fsckKnownRecordVersion = 1

// fsckObjectSuffix is the extension a compressed object carries (internal/store/objects.go:23).
const fsckObjectSuffix = ".zst"

// fsckCheck is one row of the report: what was checked, whether it holds, how badly it fails, how
// many of the population it scanned, how many defects it found and a bounded sample of them.
type fsckCheck struct {
	ID       string            `json:"id"`
	OK       bool              `json:"ok"`
	Severity contract.Severity `json:"severity"`
	// Scanned is the population this check walked. It is a pointer so a check with no population
	// (the daemon row) omits it rather than claiming it scanned nothing: a zero-defect result over
	// a zero-sized store must be distinguishable from a real pass.
	Scanned *int     `json:"scanned,omitempty"`
	Count   int      `json:"count"`
	Detail  []string `json:"detail,omitempty"`
}

// fsckRepair is one explicit repair, with what it found and what it left.
type fsckRepair struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// fsckQuarantineInventory is the evidence tally. It is reported and never touched.
type fsckQuarantineInventory struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// fsckReport is `--json`'s top-level document.
type fsckReport struct {
	Schema        int    `json:"schema"`
	Project       string `json:"project"`
	DaemonRunning bool   `json:"daemon_running"`
	// ReadOnly reports that this run wrote nothing at all. It is false under --repair, which
	// writes, AND under --seal-check, which does not write the store but DOES acquire the daemon
	// lock — and acquiring it creates .qompack/run/. A run that creates a directory is not
	// read-only, whatever it then does with it.
	ReadOnly bool `json:"read_only"`
	// Repairing is --repair: this run writes the repairs listed below. It is its own field because
	// an empty Repairs list means "a repair run found nothing to repair", which is not the same
	// document as a scan that never intended to write.
	Repairing bool `json:"repairing"`
	// AttemptsDaemonLock says the run ATTEMPTS the lock, not that it holds one. It is set before
	// the attempt and the attempt can fail — --seal-check yields with a reported row when a daemon
	// owns the lock — so the past tense it used to carry was a claim this field cannot make.
	// Acquiring the lock is what creates .qompack/run/, which is why read_only turns false with it.
	AttemptsDaemonLock bool                    `json:"attempts_daemon_lock"`
	Checks             []fsckCheck             `json:"checks"`
	Repairs            []fsckRepair            `json:"repairs"`
	Fidelity           map[string]int          `json:"fidelity"`
	Quarantine         fsckQuarantineInventory `json:"quarantine"`
	Exit               int                     `json:"exit"`
}

// fsckCmds is the fsck subcommand table, following adminCmds' pattern.
func fsckCmds() []Cmd {
	return []Cmd{{
		Name:    "fsck",
		Summary: "verify store, index, checkpoint and backup integrity; repair only what is explicit",
		Run:     runFsck,
	}}
}

// fsckRepairOptions is one repair run. It is the option set the front end assembles from the
// operator's flags, and nothing else: the repair re-reads the project under the lock rather than
// carrying the read-only scan's findings across, because the scan ran before the lock was taken and
// a finding from before a lock is a finding about a project somebody else may still have been
// writing.
type fsckRepairOptions struct {
	ProjectRoot string
	// Confirm is --yes. The library refuses without it for the same reason
	// DeliverySealOptions.validate does: the requirement belongs to the tool, so every caller that
	// can reach a write comes through one gate.
	Confirm bool
	Out     io.Writer
	Clock   core.Clock
}

// runFsckRepairs is the repair entry point this front end wires its flags to.
//
// It is a variable for exactly the reason admin.go's repairDeliverySeal is one: the wiring is what
// this file OWNS and nothing else in the package can observe it, so without a seam the only edit
// that matters — a front end that passed Confirm: true for every run, granting an operator's
// consent on their behalf — would be invisible to every test the package can afford to write.
// TestFsck_RepairSeamGetsTheOperatorsOptions swaps this for a capture and compares the whole set.
var runFsckRepairs = performFsckRepairs

// runFsck implements `qompack fsck [--project <root>] [--json] [--repair] [--yes]`.
//
// Exit 0 when no defect was found, 1 when one was (or when an I/O failure prevented a check, which
// is itself reported as a row), 2 for an invocation the operator got wrong. It is not a hook, so
// §2.3 permits it to fail loudly.
func runFsck(ctx context.Context, env Env, args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("fsck", flag.ContinueOnError)
	fs.SetOutput(errw)
	project := fs.String("project", "",
		"project root (defaults to QOMPACK_PROJECT_ROOT or the process cwd's nearest .git)")
	asJSON := fs.Bool("json", false, "emit the report as JSON instead of a table")
	repair := fs.Bool("repair", false,
		"perform the five explicit additive repairs under the daemon lock; needs --yes")
	yes := fs.Bool("yes", false, "confirm --repair, which writes to the project")
	sealCheck := fs.Bool("seal-check", false,
		"also run the full dual-reader delivery-seal check, which ACQUIRES the daemon lock "+
			"(the default run never does) and yields with a reported row if a daemon owns it")
	if err := fs.Parse(args); err != nil {
		return errUsageReported // flag has already written its complaint and the usage to errw.
	}

	switch {
	case fs.NArg() > 0:
		fmt.Fprintf(errw, "qompack fsck: unexpected argument %q\n", fs.Arg(0))
		return errUsageReported
	case *repair && !*yes:
		fmt.Fprintln(errw, "qompack fsck: --repair writes to the project; pass --yes to confirm it")
		return errUsageReported
	case *repair && *sealCheck:
		// Both want the daemon lock, and the repair holds it for its whole run, so a nested
		// acquisition inside the scan would refuse itself.
		fmt.Fprintln(errw, "qompack fsck: --seal-check and --repair both take the daemon lock; "+
			"run them separately")
		return errUsageReported
	}

	root := *project
	if root == "" {
		root = resolveProjectRoot(env, nil)
	}
	if root == "" {
		fmt.Fprintln(errw, "qompack fsck: could not resolve a project root")
		return errAlreadyReported
	}
	clk := env.Clock
	if clk == nil {
		clk = core.SystemClock()
	}

	report := fsckScanProject(ctx, root, *repair, *sealCheck)

	var repairErr error
	if *repair {
		report.Repairs, repairErr = runFsckRepairs(fsckRepairOptions{
			ProjectRoot: root, Confirm: *yes, Out: out, Clock: clk,
		})
	}
	if report.Repairs == nil {
		report.Repairs = []fsckRepair{}
	}
	if repairErr != nil {
		report.Exit = ExitError
	}

	if err := writeFsckReport(out, report, *asJSON); err != nil {
		return err
	}
	if repairErr != nil {
		fmt.Fprintf(errw, "qompack fsck: %v\n", repairErr)
		return errAlreadyReported
	}
	if report.Exit != ExitOK {
		// The report itself already names every defect, so this is one line saying how many there
		// were rather than a second copy of them — and it goes to stderr so a script that pipes
		// stdout into a JSON reader still sees it.
		n := fsckDefectCount(report)
		fmt.Fprintf(errw, "qompack fsck: %d defective check(s); see the report above\n", n)
		return fmt.Errorf("%w: fsck found %d defective check(s)", errAlreadyReported, n)
	}
	return nil
}

// fsckDefectCount is how many rows reported a defect, for the one line the operator sees on stderr.
func fsckDefectCount(r fsckReport) int {
	n := 0
	for _, c := range r.Checks {
		if !c.OK {
			n++
		}
	}
	return n
}

// writeFsckReport renders the report, as JSON or as the fixed-width table self-test established.
func writeFsckReport(out io.Writer, r fsckReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(r)
	}
	writeFsckTable(out, r)
	return nil
}

// writeFsckTable renders the report the way writeSelfTestTable renders self-test's.
func writeFsckTable(out io.Writer, r fsckReport) {
	fmt.Fprintf(out, "project: %s\n", r.Project)
	if r.DaemonRunning {
		fmt.Fprintln(out, "daemon running: results are a snapshot of a moving target")
	}
	// Said out loud rather than left to the JSON: the default run creates nothing, and the two
	// flags that change that are the ones an operator most needs told about. They change it for
	// DIFFERENT reasons, so they get different sentences — under --repair the lock is the smaller
	// half of what this run does to the project.
	switch {
	case r.Repairing:
		fmt.Fprintln(out, "not read-only: --repair writes the repairs listed below, and takes the "+
			"daemon lock first, which creates .qompack/run/")
	case r.AttemptsDaemonLock:
		fmt.Fprintln(out, "not read-only: this run attempts the daemon lock, and acquiring it "+
			"creates .qompack/run/")
	}
	fmt.Fprintf(out, "%-20s %-5s %-9s %-9s %-9s %s\n",
		"CHECK", "OK", "SEVERITY", "SCANNED", "DEFECTS", "DETAIL")
	for _, c := range r.Checks {
		ok := "ok"
		if !c.OK {
			ok = "FAIL"
		}
		first := ""
		if len(c.Detail) > 0 {
			first = c.Detail[0]
		}
		// A row with no population prints "-" rather than 0: "it scanned nothing" and "it has
		// nothing to scan" are different answers and a bare zero would merge them.
		scanned := "-"
		if c.Scanned != nil {
			scanned = fmt.Sprintf("%d", *c.Scanned)
		}
		fmt.Fprintf(out, "%-20s %-5s %-9s %-9s %-9d %s\n",
			c.ID, ok, severityLabel(c.Severity), scanned, c.Count, first)
		for i := 1; i < len(c.Detail); i++ {
			fmt.Fprintf(out, "%-20s %-5s %-9s %-9s %-9s %s\n", "", "", "", "", "", c.Detail[i])
		}
	}
	for _, rep := range r.Repairs {
		fmt.Fprintf(out, "repair %-16s %s: %s -> %s\n", rep.Kind, rep.Target, rep.Before, rep.After)
	}
	fmt.Fprintf(out, "\nfidelity: %s\n", fsckFidelityLine(r.Fidelity))
	fmt.Fprintf(out, "quarantine: %d file(s), %d byte(s) preserved as evidence\n",
		r.Quarantine.Files, r.Quarantine.Bytes)
	fmt.Fprintf(out, "exit: %d\n", r.Exit)
}

// fsckFidelityLine renders the fidelity tally in a stable order.
func fsckFidelityLine(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	if len(parts) == 0 {
		return "no live roots"
	}
	return strings.Join(parts, " ")
}

// fsckRowBuilder accumulates one check's findings.
type fsckRowBuilder struct {
	id       string
	severity contract.Severity
	scanned  int
	counted  bool
	count    int
	detail   []string
	notes    []string
}

// newFsckRow starts a row at the severity a DEFECT on it carries. A row that finds nothing is
// reported at SevInfo whatever this says, because a clean check is not a warning.
func newFsckRow(id string, sev contract.Severity) *fsckRowBuilder {
	return &fsckRowBuilder{id: id, severity: sev}
}

// scan records that n members of this check's population were walked.
func (b *fsckRowBuilder) scan(n int) { b.scanned, b.counted = n, true }

// defect names one finding. The list is bounded; the count is not.
func (b *fsckRowBuilder) defect(format string, args ...any) {
	b.count++
	if len(b.detail) < fsckMaxDetail {
		b.detail = append(b.detail, fmt.Sprintf(format, args...))
	}
}

// note records something the check OBSERVED without calling it a defect — a check that could not
// run, or a state the product accounts for. A check that silently did not run is a check nobody may
// cite, which is why a note is rendered even on a passing row.
func (b *fsckRowBuilder) note(format string, args ...any) {
	b.notes = append(b.notes, fmt.Sprintf(format, args...))
}

// build renders the row.
func (b *fsckRowBuilder) build() fsckCheck {
	c := fsckCheck{ID: b.id, OK: b.count == 0, Count: b.count, Severity: b.severity}
	if c.OK {
		c.Severity = contract.SevInfo
	}
	if b.counted {
		n := b.scanned
		c.Scanned = &n
	}
	c.Detail = append(append([]string{}, b.detail...), b.notes...)
	if b.count > len(b.detail) {
		c.Detail = append(c.Detail,
			fmt.Sprintf("... and %d more of the same class", b.count-len(b.detail)))
	}
	if len(c.Detail) == 0 {
		c.Detail = nil
	}
	return c
}

// fsckScan is one walk of a project's .qompack, carrying what each check learned for the next.
type fsckScan struct {
	ctx    context.Context
	root   string
	l      paths.Layout
	report *fsckReport
	// roots is every content record index/roots.jsonl carries, by hash text. It is this file's own
	// parse rather than the store's, so a hash resolves without opening a second writer.
	roots map[string]fsckRootLine
	// tombstoned is the set of roots a `gc` record retired: a collected root's absence is REPORTED,
	// not dangling.
	tombstoned map[string]bool
	// sidecarBytes is every bytes_hash a capture sidecar carries. An evidence-class retention root
	// names one of these and never an object (commit4-evidence.md §8, calibration rule 3).
	sidecarBytes map[string]bool
	// manifestSeqs is every checkpoint seq checkpoints/MANIFEST.jsonl records.
	manifestSeqs map[core.CheckpointSeq]bool
	// sealCheck is --seal-check: the one opt-in that lets this scan acquire the daemon lock.
	sealCheck bool
}

// fsckRootLine is the subset of index/roots.jsonl's wire shape fsck resolves. The writer's full
// shape is store.rootWire; reading it here is deliberate — see rule 2 in this file's header.
type fsckRootLine struct {
	V      int    `json:"v"`
	Op     string `json:"op"`
	Root   string `json:"root"`
	Tool   string `json:"tool"`
	Chunks []struct {
		H string `json:"h"`
		N *int64 `json:"n"`
	} `json:"chunks"`
	Class  *int   `json:"class"`
	Deltas string `json:"deltas"`
	Base   string `json:"base"`
	Orig   string `json:"orig"`
	Eph    bool   `json:"eph"`
}

// fsckScanProject walks root's .qompack and builds the whole report.
//
// It never creates the project. `projectEstablished` is the same test every other read-only
// command makes (qompack_commands.go's READ-ONLY DISCIPLINE block): a directory that has never been
// used with Qompack is a reported fact, not a defect and not a reason to lay out a store.
func fsckScanProject(ctx context.Context, root string, repairing, sealCheck bool) fsckReport {
	l := paths.Of(root)
	rep := fsckReport{
		Schema:  fsckSchema,
		Project: root,
		// --seal-check acquires the daemon lock, and daemon.Acquire creates .qompack/run/ to put
		// the lock file in. That is a created directory, so the run is NOT read-only and must not
		// claim to be — the claim is the whole point of the field.
		ReadOnly:           !repairing && !sealCheck,
		Repairing:          repairing,
		AttemptsDaemonLock: repairing || sealCheck,
		Fidelity:           map[string]int{},
		Repairs:            []fsckRepair{},
	}

	daemonRow := newFsckRow("daemon", contract.SevInfo)
	info, held, alive := fsckDaemonLiveness(root)
	switch {
	case held && alive:
		rep.DaemonRunning = true
		daemonRow.note("daemon running: results are a snapshot of a moving target (pid %d, since %d)",
			info.PID, info.Started)
	case held:
		daemonRow.note("a lock file names pid %d whose listener does not answer: the lock is STALE, "+
			"and the staleness protocol reclaims such a lock rather than blocking forever", info.PID)
	default:
		daemonRow.note("no daemon holds the lock; this project is quiet")
	}

	storeRow := newFsckRow("store", contract.SevInfo)
	if !projectEstablished(l) {
		storeRow.note("no store: %s has never been used with Qompack, and fsck does not create one", root)
		rep.Checks = []fsckCheck{storeRow.build(), daemonRow.build()}
		rep.Exit = ExitOK
		return rep
	}
	storeRow.note("store present at %s", l.Dot)

	s := &fsckScan{
		ctx: ctx, root: root, l: l, report: &rep,
		roots:        map[string]fsckRootLine{},
		tombstoned:   map[string]bool{},
		sidecarBytes: map[string]bool{},
		manifestSeqs: map[core.CheckpointSeq]bool{},
		sealCheck:    sealCheck,
	}

	rep.Checks = append(rep.Checks,
		storeRow.build(),
		daemonRow.build(),
		s.checkObjects(),
		s.checkRoots(),
		s.checkToolUse(),
		s.checkFiles(),
		s.checkSegments(),
		s.checkCaptures(),
		s.checkPublication(),
		s.checkCheckpoints(),
		s.checkPins(),
		s.checkNegativeKnowledge(),
		s.checkDelivery(),
		s.checkSpool(),
		s.checkRetention(),
		s.checkMigrateAndBackups(),
		s.checkFidelity(),
		s.checkQuarantine(),
	)

	rep.Exit = ExitOK
	for _, c := range rep.Checks {
		if !c.OK {
			rep.Exit = ExitError
		}
	}
	return rep
}

// ── shared readers ─────────────────────────────────────────────────────────────────────────────

// fsckReadLines returns every non-empty line of a JSONL file, read through paths.ReadFileShared so
// a daemon that is still unwinding cannot make the read fail on Windows.
func fsckReadLines(path string) ([][]byte, error) {
	b, err := paths.ReadFileShared(path)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), fsckMaxLineBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		out = append(out, append([]byte(nil), line...))
	}
	return out, sc.Err()
}

// fsckObjectPath returns the on-disk path backing h, trying both spellings the store's own reader
// tries (internal/store/objects.go objectCandidates: the compressed `.zst` and the bare one).
func fsckObjectPath(l paths.Layout, h core.Hash) (string, bool) {
	hx := strings.TrimPrefix(h.String(), "sha256:")
	if len(hx) < 4 {
		return "", false
	}
	dir := filepath.Join(l.Objects, hx[0:2], hx[2:4])
	for _, name := range []string{hx + fsckObjectSuffix, hx} {
		p := filepath.Join(dir, name)
		if fi, err := os.Lstat(paths.Long(p)); err == nil && fi.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}

// fsckHashHeld reports whether h can still be materialized: its bytes are on disk, or it names a
// root whose every chunk's bytes are on disk. This is finalize.go's own root-or-chunks rule,
// resolved against the filesystem rather than through store.Has — see rule 1 in the file header.
func (s *fsckScan) fsckHashHeld(text string) bool {
	h, err := core.ParseHash(text)
	if err != nil {
		return false
	}
	if _, ok := fsckObjectPath(s.l, h); ok {
		return true
	}
	rl, ok := s.roots[h.String()]
	if !ok {
		return false
	}
	for _, c := range rl.Chunks {
		ch, cerr := core.ParseHash(c.H)
		if cerr != nil {
			return false
		}
		if _, held := fsckObjectPath(s.l, ch); !held {
			return false
		}
	}
	return true
}

// fsckShortHash renders a hash briefly enough to read in a report line.
func fsckShortHash(h string) string {
	trimmed := strings.TrimPrefix(h, "sha256:")
	switch {
	case trimmed == "":
		return "<empty>"
	case len(trimmed) > 12:
		return trimmed[:12]
	}
	return trimmed
}

// fsckIsZeroHash reports whether h is the empty/zero hash a record carries when it names nothing.
func fsckIsZeroHash(h string) bool {
	trimmed := strings.TrimPrefix(h, "sha256:")
	return trimmed == "" || strings.Trim(trimmed, "0") == ""
}

// ── 1. objects ─────────────────────────────────────────────────────────────────────────────────

// checkObjects verifies every file under objects/**: the name is a content address, the physical
// size is within bound, the bytes decode, and the plaintext re-hashes to the name.
//
// It deliberately does NOT go through the store's own getObject, which quarantines what it rejects
// (internal/store/objects.go:344-369). A read-only integrity scan that moved files as a side effect
// of looking at them would make the answer depend on whether anyone had looked before.
func (s *fsckScan) checkObjects() fsckCheck {
	row := newFsckRow("objects", contract.SevCritical)
	seen := 0

	err := filepath.WalkDir(paths.Long(s.l.Objects), func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return s.ctx.Err()
		}
		seen++
		name := d.Name()
		hx := strings.TrimSuffix(name, fsckObjectSuffix)
		h, parseErr := core.ParseHash(hx)
		if parseErr != nil {
			row.defect("objects/%s: the file name is not a content address", name)
			return nil
		}

		info, statErr := d.Info()
		switch {
		case statErr != nil:
			row.defect("objects/%s: unreadable: %v", name, statErr)
			return nil
		case !info.Mode().IsRegular():
			row.defect("objects/%s: not a regular file (mode %s)", name, info.Mode())
			return nil
		case info.Size() > fsckObjectSizeLimit(name):
			// Past the limit readObjectFile itself refuses for this candidate spelling
			// (internal/store/objects.go:256-266), so the store cannot read the file either. It is
			// also why nothing below reads it: the bound is what keeps this walk from allocating
			// a file of arbitrary size.
			row.defect("objects/%s: %d bytes is past the %d-byte limit the store's own reader "+
				"refuses", name, info.Size(), fsckObjectSizeLimit(name))
			return nil
		}
		raw, readErr := paths.ReadFileShared(p)
		if readErr != nil {
			row.defect("objects/%s: unreadable: %v", name, readErr)
			return nil
		}
		plain := raw
		if strings.HasSuffix(name, fsckObjectSuffix) {
			decoded, decErr := store.Decode(raw)
			if decErr != nil {
				row.defect("objects/%s: does not decode: %v", name, decErr)
				return nil
			}
			plain = decoded
		}
		if got := core.HashBytes(core.DomainChunk, plain); got != h {
			row.defect("objects/%s: does not re-hash to its own name (got %s)", name, got.Short())
		}
		return nil
	})
	switch {
	case err != nil && errors.Is(err, fs.ErrNotExist):
		row.note("objects/ does not exist yet")
	case err != nil:
		row.defect("objects/ could not be walked: %v", err)
	}
	row.scan(seen)
	return row.build()
}

// ── 2. roots index ─────────────────────────────────────────────────────────────────────────────

// checkRoots scans index/roots.jsonl line by line: the record version and op are ones this build
// knows, every hash parses, every chunk of every live root is held, every delta/base/orig pointer
// resolves, and no tombstoned root is still referenced.
//
// Two passes, because a `gc` record can follow the content record it retires.
func (s *fsckScan) checkRoots() fsckCheck {
	row := newFsckRow("index.roots", contract.SevCritical)
	path := filepath.Join(s.l.Index, "roots.jsonl")

	lines, err := fsckReadLines(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			row.note("index/roots.jsonl does not exist yet")
			row.scan(0)
			return row.build()
		}
		row.defect("index/roots.jsonl is unreadable, so every root it recorded is lost to a reader: %v", err)
		return row.build()
	}

	parsed := make([]fsckRootLine, 0, len(lines))
	for i, raw := range lines {
		var rl fsckRootLine
		if jsonErr := json.Unmarshal(raw, &rl); jsonErr != nil {
			row.defect("index/roots.jsonl:%d does not parse as a roots record: %v", i+1, jsonErr)
			continue
		}
		if rl.V != fsckKnownRecordVersion && rl.V != fsckKnownRecordVersion+1 {
			row.defect("index/roots.jsonl:%d carries record version %d, which this build does not read", i+1, rl.V)
			continue
		}
		if _, hErr := core.ParseHash(rl.Root); hErr != nil {
			row.defect("index/roots.jsonl:%d names a root that is not a hash: %v", i+1, hErr)
			continue
		}
		switch rl.Op {
		case "gc":
			s.tombstoned[fsckCanonHash(rl.Root)] = true
			continue
		case "":
			parsed = append(parsed, rl)
			s.roots[fsckCanonHash(rl.Root)] = rl
		default:
			row.defect("index/roots.jsonl:%d carries op %q, which this build does not know; "+
				"the store skips such a line rather than publishing a phantom root", i+1, rl.Op)
		}
	}
	row.scan(len(parsed))

	for _, rl := range parsed {
		if s.ctx.Err() != nil {
			row.note("the scan was cancelled before every root was resolved")
			break
		}
		s.resolveRootLine(row, rl)
	}
	return row.build()
}

// fsckCanonHash normalizes a hash's text form so two spellings of the same digest are one key.
func fsckCanonHash(text string) string {
	if h, err := core.ParseHash(text); err == nil {
		return h.String()
	}
	return text
}

// resolveRootLine resolves one content record's chunks and recovery pointers.
func (s *fsckScan) resolveRootLine(row *fsckRowBuilder, rl fsckRootLine) {
	retired := s.tombstoned[fsckCanonHash(rl.Root)]
	for _, c := range rl.Chunks {
		if c.N == nil || *c.N < 0 || *c.N > store.MaxPutBytes {
			row.defect("root %s names chunk %s with a length this build coerces to unknown, "+
				"so only the checksum stands", fsckShortHash(rl.Root), fsckShortHash(c.H))
		}
		if s.fsckHashHeld(c.H) {
			continue
		}
		if retired {
			row.note("root %s names chunk %s, which a gc tombstone accounts for",
				fsckShortHash(rl.Root), fsckShortHash(c.H))
			continue
		}
		row.defect("root %s names chunk %s, which the object store does not hold",
			fsckShortHash(rl.Root), fsckShortHash(c.H))
	}
	for label, ptr := range map[string]string{"deltas": rl.Deltas, "base": rl.Base, "orig": rl.Orig} {
		if ptr == "" || fsckIsZeroHash(ptr) || s.fsckHashHeld(ptr) {
			continue
		}
		if retired || s.tombstoned[fsckCanonHash(ptr)] {
			row.note("root %s's %s pointer %s is accounted for by a gc tombstone",
				fsckShortHash(rl.Root), label, fsckShortHash(ptr))
			continue
		}
		row.defect("root %s's %s pointer %s does not resolve",
			fsckShortHash(rl.Root), label, fsckShortHash(ptr))
	}
	if retired && len(rl.Chunks) > 0 && s.fsckHashHeld(rl.Root) {
		row.note("root %s is tombstoned and its bytes are still on disk", fsckShortHash(rl.Root))
	}
}

// ── 3. tool_use index ──────────────────────────────────────────────────────────────────────────

// fsckToolUseLine is the subset of index/tool_use.jsonl's frozen wire shape fsck resolves.
type fsckToolUseLine struct {
	ID           string `json:"id"`
	Session      string `json:"session"`
	Turn         int64  `json:"turn"`
	Tool         string `json:"tool"`
	Root         string `json:"root"`
	Path         string `json:"path"`
	Status       int    `json:"status"`
	SupersededBy string `json:"superseded_by"`
}

// checkToolUse resolves every tool_use record's root by finalize.go's OWN rule — the object is
// held, or the root resolves and every chunk it names is held — because that is exactly what
// `expand(hash)` needs. Asking Has alone would call the whole tier unresolvable; asking GetRoot
// alone would call a root whose chunks were collected resolvable.
//
// It also checks the two orderings the contract states: a supersession link names a record this
// file carries, and a session's turns never go backwards.
func (s *fsckScan) checkToolUse() fsckCheck {
	row := newFsckRow("index.tool_use", contract.SevCritical)
	path := filepath.Join(s.l.Index, "tool_use.jsonl")

	lines, err := fsckReadLines(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			row.note("index/tool_use.jsonl does not exist yet")
			row.scan(0)
			return row.build()
		}
		row.defect("index/tool_use.jsonl is unreadable: %v", err)
		return row.build()
	}

	records := make([]fsckToolUseLine, 0, len(lines))
	ids := make(map[string]bool, len(lines))
	for i, raw := range lines {
		var tu fsckToolUseLine
		if jsonErr := json.Unmarshal(raw, &tu); jsonErr != nil {
			row.defect("index/tool_use.jsonl:%d does not parse as a tool_use record: %v", i+1, jsonErr)
			continue
		}
		if tu.ID == "" {
			row.defect("index/tool_use.jsonl:%d carries no tool_use id", i+1)
			continue
		}
		ids[tu.ID] = true
		records = append(records, tu)
	}
	row.scan(len(records))

	lastTurn := map[string]int64{}
	for _, tu := range records {
		if prev, seen := lastTurn[tu.Session]; seen && tu.Turn < prev {
			row.defect("tool_use %s reports turn %d after turn %d in session %s; turns are monotone",
				tu.ID, tu.Turn, prev, tu.Session)
		}
		lastTurn[tu.Session] = tu.Turn

		if tu.SupersededBy != "" && !ids[tu.SupersededBy] {
			row.defect("tool_use %s is superseded by %s, which this index does not record",
				tu.ID, tu.SupersededBy)
		}
		if tu.Root == "" || fsckIsZeroHash(tu.Root) || s.fsckHashHeld(tu.Root) {
			continue
		}
		if s.tombstoned[fsckCanonHash(tu.Root)] {
			row.note("tool_use %s points at root %s, which a gc tombstone accounts for",
				tu.ID, fsckShortHash(tu.Root))
			continue
		}
		row.defect("tool_use %s points at root %s, which cannot be materialized",
			tu.ID, fsckShortHash(tu.Root))
	}
	return row.build()
}

// ── 4. files log and its derived view ──────────────────────────────────────────────────────────

// checkFiles parses the append-only log and compares the derived view against it. The LOG is the
// truth: a crash between the last append and the next Flush leaves files.json stale, which is why a
// disagreement here is a DERIVED-VIEW defect and one of the five things --repair may regenerate.
func (s *fsckScan) checkFiles() fsckCheck {
	row := newFsckRow("index.files", contract.SevWarn)

	view, log, err := fsckReadFilesState(s.l)
	if err != nil {
		row.defect("index/files.jsonl is unreadable: %v", err)
		return row.build()
	}
	row.scan(len(log))
	// The log's own bad lines are real defects and are reported whatever state the view is in;
	// an unreadable view then ends the row, because comparing the log against a document that
	// declared nothing would only add phantom "version 0" and "omits" defects.
	for _, bad := range view.badLines {
		row.defect("index/files.jsonl:%d does not parse as a file-version record: %s", bad.line, bad.why)
	}
	if view.viewErr != nil {
		row.defect("index/files.json:0 does not parse as a file-version record: %s", view.viewErr)
		return row.build()
	}
	if !view.present {
		if len(log) > 0 {
			row.defect("index/files.json is absent while its log carries %d path(s); the view is "+
				"derived and --repair regenerates it", len(log))
		}
		return row.build()
	}
	if view.doc.Version != store.FilesViewVersion {
		row.defect("index/files.json declares view version %d, which this build does not read",
			view.doc.Version)
	}
	for path, want := range log {
		got, ok := view.doc.Files[path]
		if !ok {
			row.defect("index/files.json omits %q, which the log records %d version(s) of",
				path, len(want))
			continue
		}
		if len(got) != len(want) {
			row.defect("index/files.json records %d version(s) of %q and the log records %d",
				len(got), path, len(want))
		}
	}
	for path := range view.doc.Files {
		if _, ok := log[path]; !ok {
			row.defect("index/files.json records %q, which the log never mentions", path)
		}
	}
	return row.build()
}

// fsckFilesBadLine is one unparseable line of the files log.
type fsckFilesBadLine struct {
	line int
	why  string
}

// fsckFilesState is what checkFiles and the view repair both need: the parsed view, whether it is
// there at all, and the log's own bad lines.
type fsckFilesState struct {
	doc      store.FilesView
	present  bool
	viewErr  error
	badLines []fsckFilesBadLine
}

// fsckReadFilesState reads index/files.json and replays index/files.jsonl through internal/store's
// own exported readers (SP-17 R5-1).
//
// It used to carry a copy of the view's shape, of its version constant and of storeKey's path
// normalization, because the store exported none of the three. It now asks the package that writes
// the document what the document is, so a schema change reaches this check by failing to compile
// rather than by silently disagreeing.
func fsckReadFilesState(l paths.Layout) (fsckFilesState, map[string][]store.FileVersion, error) {
	var state fsckFilesState

	doc, present, err := store.ReadFilesView(l)
	state.present = present
	if err != nil {
		// A view that is there and unreadable — unparseable, or a path that is not a file — is a
		// defect of the VIEW, reported against index/files.json. It is not a reason to abandon
		// the check: the LOG is the truth here, and the comparison against it is the row's
		// whole content.
		state.viewErr = err
	} else {
		state.doc = doc
	}

	log, defects, err := store.ReplayFilesLog(l)
	if err != nil {
		return state, nil, err
	}
	for _, d := range defects {
		state.badLines = append(state.badLines, fsckFilesBadLine{line: d.Line, why: d.Why})
	}
	return state, log, nil
}

// ── 5. segments and the DPI guard's seq references ─────────────────────────────────────────────

// fsckSegmentLine is the subset of index/segments.jsonl every record shape shares, plus the encode
// record's seq.
type fsckSegmentLine struct {
	V   int    `json:"v"`
	Op  string `json:"op"`
	ID  int64  `json:"id"`
	Seq int64  `json:"seq"`
}

// checkSegments verifies that every `encode` record — the DPI guard's durable claim that a segment
// was encoded into a named checkpoint — points at a checkpoint the manifest actually records. This
// is finalize.go's seq-reference-drift case (counter checkpoint.seq_reference_drift): an O_EXCL
// retry can bump a writer past a seq a MarkEncoded already claimed, and MarkEncoded is one-way.
//
// A `bloom` record with no file is deliberately NOT a defect: internal/store parses that record and
// never writes one (SP-16 owns the writer), so a missing per-segment filter is a reservation.
func (s *fsckScan) checkSegments() fsckCheck {
	row := newFsckRow("index.segments", contract.SevWarn)
	lines, err := fsckReadLines(filepath.Join(s.l.Index, "segments.jsonl"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			row.note("index/segments.jsonl does not exist yet")
			row.scan(0)
			return row.build()
		}
		row.defect("index/segments.jsonl is unreadable: %v", err)
		return row.build()
	}

	known := map[string]bool{"open": true, "close": true, "encode": true, "bloom": true}
	seqs := s.manifestSeqSet()
	encodes := 0
	for i, raw := range lines {
		var rec fsckSegmentLine
		if jsonErr := json.Unmarshal(raw, &rec); jsonErr != nil {
			row.defect("index/segments.jsonl:%d does not parse as a segment record: %v", i+1, jsonErr)
			continue
		}
		if rec.V != fsckKnownRecordVersion {
			row.defect("index/segments.jsonl:%d carries record version %d, which this build does not read",
				i+1, rec.V)
			continue
		}
		if !known[rec.Op] {
			row.defect("index/segments.jsonl:%d carries op %q, which this build does not know", i+1, rec.Op)
			continue
		}
		if rec.Op != "encode" {
			continue
		}
		encodes++
		if !seqs[core.CheckpointSeq(rec.Seq)] {
			row.defect("segment %d records that it was encoded into checkpoint %04d, which "+
				"checkpoints/MANIFEST.jsonl does not record", rec.ID, rec.Seq)
		}
	}
	row.scan(encodes)
	return row.build()
}

// manifestSeqSet is the set of checkpoint sequence numbers the manifest records, loaded once.
func (s *fsckScan) manifestSeqSet() map[core.CheckpointSeq]bool {
	if len(s.manifestSeqs) > 0 {
		return s.manifestSeqs
	}
	entries, err := paths.ReadManifest(s.l)
	if err != nil {
		return s.manifestSeqs // a malformed manifest is checkCheckpoints' finding, not this row's
	}
	for _, e := range entries {
		s.manifestSeqs[e.Seq] = true
	}
	return s.manifestSeqs
}

// ── 6. capture sidecars ────────────────────────────────────────────────────────────────────────

// fsckSidecarLine is the subset of a capture sidecar fsck reads. It is read from the file rather
// than through store.ReadCaptureSidecar so a sidecar a crash left unparseable is still reportable.
type fsckSidecarLine struct {
	Version       int    `json:"v"`
	ObservationID string `json:"observation_id"`
	Op            string `json:"op"`
	ToolUseID     string `json:"tool_use_id"`
	Root          string `json:"root"`
	Published     bool   `json:"published"`
	Outcome       string `json:"outcome"`
	BytesHash     string `json:"bytes_hash"`
	Bytes         []byte `json:"bytes,omitempty"`
}

// checkCaptures walks records/captures/** for the two states that are gaps, and records every
// sidecar's bytes_hash for the retention check.
//
// `published:false` on its own is NOT a gap and treating it as one fires on every ordinary turn: a
// capture whose outcome is not ok admitted no bytes, and a PROMPT delivery produces no
// ToolUseRecord and so is permanently unpublished by design. The one reportable state is a TOOL
// delivery whose outcome is ok and whose bytes are durable with no reference ever joined to it
// (commit4-evidence.md §8, calibration rule 2). Sidecars are EVIDENCE: nothing here ever repairs
// or sweeps one.
func (s *fsckScan) checkCaptures() fsckCheck {
	row := newFsckRow("captures", contract.SevWarn)
	dir := filepath.Join(s.l.Records, "captures")
	seen := 0

	err := filepath.WalkDir(paths.Long(dir), func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		seen++
		raw, readErr := paths.ReadFileShared(p)
		if readErr != nil {
			row.defect("capture sidecar %s is unreadable: %v", d.Name(), readErr)
			return nil
		}
		var sc fsckSidecarLine
		if jsonErr := json.Unmarshal(raw, &sc); jsonErr != nil {
			row.defect("capture sidecar %s does not parse: %v", d.Name(), jsonErr)
			return nil
		}
		if sc.BytesHash != "" && !fsckIsZeroHash(sc.BytesHash) {
			s.sidecarBytes[fsckCanonHash(sc.BytesHash)] = true
		}
		switch {
		case sc.Version > store.CaptureSidecarVersion:
			row.note("capture sidecar %s is version %d: newer than this build, a support gap rather than damage",
				d.Name(), sc.Version)
		case sc.Version != store.CaptureSidecarVersion:
			row.defect("capture sidecar %s declares version %d, which this build does not read",
				d.Name(), sc.Version)
		}
		if !sc.Published {
			required, known := store.CaptureRequiresReference(sc.Op, sc.Bytes)
			if !known && sc.Outcome == string(core.OutcomeOK) && sc.BytesHash != "" && !fsckIsZeroHash(sc.BytesHash) {
				row.defect("capture publication requirement is unknown")
			}
			if required && sc.Outcome == string(core.OutcomeOK) &&
				sc.BytesHash != "" && !fsckIsZeroHash(sc.BytesHash) {
				row.defect("capture sidecar %s for a %s delivery is at stage 1 only: outcome %q with "+
					"bytes %s durable and no reference joined to it",
					fsckFirstNonEmpty(sc.ObservationID, d.Name()), sc.Op, sc.Outcome,
					fsckShortHash(sc.BytesHash))
			}
			return nil
		}
		if sc.Root != "" && !fsckIsZeroHash(sc.Root) && !s.fsckHashHeld(sc.Root) {
			row.defect("published capture sidecar %s names root %s, which does not resolve",
				fsckFirstNonEmpty(sc.ToolUseID, sc.ObservationID, d.Name()), fsckShortHash(sc.Root))
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		row.defect("records/captures could not be walked: %v", err)
	}
	row.scan(seen)
	return row.build()
}

// fsckFirstNonEmpty returns the first non-empty string, for identifying a record by whichever of
// its several names survived.
func fsckFirstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return "<unnamed>"
}

// ── 7. checkpoints ─────────────────────────────────────────────────────────────────────────────

// fsckCheckpointArtifact is what the manifest scan and the orphan scan both learn about one file.
type fsckCheckpointArtifact struct {
	Name    string
	Path    string
	Seq     core.CheckpointSeq
	Bytes   int64
	SHA256  string
	Version int
	// ParseErr is non-empty when the artifact is not a checkpoint document at all.
	ParseErr string
}

// checkCheckpoints asks internal/checkpoint's own Reader the two questions §5.14 already names as
// fsck's — Verify re-hashes every manifest entry, Chain refuses a broken ancestry outright — and
// adds the two the Reader cannot ask: a manifest line that does not parse, and an ARTIFACT with no
// manifest line (finalize.go's orphan, "qompack fsck reconciles an orphan by re-hashing it").
//
// An artifact written by a NEWER schema version is reported as exactly that and never as
// corruption. checkpoint/reader_test.go:515 already pins that the reader keeps the two apart, and
// §7.1 requires an unknown schema to report unsupported rather than guess.
func (s *fsckScan) checkCheckpoints() fsckCheck {
	row := newFsckRow("checkpoints", contract.SevCritical)

	recorded := map[string]bool{}
	entries := s.scanManifestLines(row, recorded)
	row.scan(len(entries))

	reader, err := checkpoint.OpenReader(s.root, logging.Nop(), nil)
	if err != nil {
		row.defect("checkpoint.OpenReader refused: %v", err)
		return row.build()
	}
	if bad, verifyErr := reader.Verify(s.ctx); verifyErr != nil {
		row.defect("checkpoint Verify refused, so no manifest entry could be re-hashed: %v", verifyErr)
	} else {
		for _, seq := range bad {
			row.defect("checkpoint %04d: the artifact is missing or does not re-hash to the digest "+
				"checkpoints/MANIFEST.jsonl recorded", int(seq))
		}
	}

	artifacts, artErr := fsckCheckpointArtifactsOnDisk(s.l)
	if artErr != nil {
		row.defect("checkpoints/ could not be listed, so no orphan could be found: %v", artErr)
	}
	for _, art := range artifacts {
		switch {
		case art.ParseErr != "" && !recorded[art.Name]:
			row.defect("checkpoints/%s is present with no MANIFEST line (orphan) and does not parse "+
				"as a checkpoint: %s", art.Name, art.ParseErr)
		case art.Version > checkpoint.SchemaVersion:
			row.note("checkpoints/%s declares schema version %d: written by a newer plugin (this "+
				"build reads %d), a support gap rather than damage", art.Name, art.Version, checkpoint.SchemaVersion)
		case !recorded[art.Name]:
			row.defect("checkpoints/%s is present with no checkpoints/MANIFEST.jsonl line (orphan); "+
				"it re-hashes to %s and --repair appends its line", art.Name, fsckShortHash(art.SHA256))
		}
	}

	s.checkCheckpointPointers(row, reader, entries)
	s.checkCheckpointChain(row, reader, entries)
	return row.build()
}

// checkCheckpointPointers resolves every pointer a VERIFYING checkpoint carries into the store,
// by finalize.go's own root-or-chunks rule (commit4-evidence.md §8's checkpoint_pointer class).
//
// A pointer the writer already dropped is not here to be found — ValidatePointers' DropEntry is the
// explicit report for that — so this is the pointer that survived into the artifact and stopped
// resolving afterwards, which is the state rehydration meets and nothing else names. An artifact the
// manifest records but that does not verify is skipped: Verify has already reported it, and naming
// it twice would double-count one defect.
func (s *fsckScan) checkCheckpointPointers(row *fsckRowBuilder, reader checkpoint.Reader, entries []paths.ManifestEntry) {
	for _, e := range entries {
		if s.ctx.Err() != nil {
			return
		}
		cp, _, err := reader.Get(s.ctx, e.Seq)
		if err != nil {
			continue
		}
		for _, fp := range cp.Pointers.Files {
			if fsckIsZeroHash(fp.Hash.String()) || s.fsckHashHeld(fp.Hash.String()) {
				continue
			}
			row.defect("checkpoint %04d points at file %s (%s), which does not resolve",
				int(e.Seq), fp.Path, fsckShortHash(fp.Hash.String()))
		}
		for _, tp := range cp.Pointers.Tools {
			if fsckIsZeroHash(tp.Hash.String()) || s.fsckHashHeld(tp.Hash.String()) {
				continue
			}
			row.defect("checkpoint %04d points at tool result %s (%s), which does not resolve",
				int(e.Seq), tp.ToolUseID, fsckShortHash(tp.Hash.String()))
		}
	}
}

// scanManifestLines reads checkpoints/MANIFEST.jsonl line by line, naming every line that does not
// parse. paths.ReadManifest hard-fails on the first bad line, which is right for a reader and wrong
// for a report: fsck has to say WHICH line.
func (s *fsckScan) scanManifestLines(row *fsckRowBuilder, recorded map[string]bool) []paths.ManifestEntry {
	lines, err := fsckReadLines(paths.ManifestPath(s.l))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			row.defect("checkpoints/MANIFEST.jsonl is unreadable: %v", err)
		}
		return nil
	}
	entries := make([]paths.ManifestEntry, 0, len(lines))
	for i, raw := range lines {
		var e paths.ManifestEntry
		if jsonErr := json.Unmarshal(raw, &e); jsonErr != nil {
			row.defect("checkpoints/MANIFEST.jsonl:%d does not parse: %v", i+1, jsonErr)
			continue
		}
		if _, hErr := core.ParseHash(e.SHA256); hErr != nil {
			row.defect("checkpoints/MANIFEST.jsonl:%d records a digest that does not parse: %q",
				i+1, e.SHA256)
			continue
		}
		recorded[filepath.Base(paths.CheckpointPath(s.l, e.Seq))] = true
		s.manifestSeqs[e.Seq] = true
		entries = append(entries, e)
	}
	return entries
}

// checkCheckpointChain walks back from the newest checkpoint the reader can load. Chain refuses a
// broken ancestry outright (internal/checkpoint/resolve.go:20-22 says that is the right answer for
// fsck), so its refusal IS the finding — the lenient rehydrator forms are deliberately not used.
func (s *fsckScan) checkCheckpointChain(row *fsckRowBuilder, reader checkpoint.Reader, entries []paths.ManifestEntry) {
	if len(entries) == 0 {
		row.note("no checkpoint has been written yet")
		return
	}
	newest := entries[0].Seq
	for _, e := range entries {
		if e.Seq > newest {
			newest = e.Seq
		}
	}
	for seq := newest; ; seq-- {
		if _, _, err := reader.Get(s.ctx, seq); err != nil {
			if seq == 0 {
				row.note("no checkpoint verifies, so no chain could be walked")
				return
			}
			continue
		}
		chain, err := reader.Chain(s.ctx, seq)
		switch {
		case errors.Is(err, checkpoint.ErrChainTruncated):
			row.note("the chain from %04d is longer than this build walks; the newest %d links verify",
				int(seq), len(chain))
		case err != nil:
			row.defect("the chain from the newest verifying checkpoint %04d is broken: %v", int(seq), err)
		default:
			row.note("the chain from %04d verifies across %d checkpoint(s)", int(seq), len(chain))
		}
		return
	}
}

// fsckCheckpointArtifactsOnDisk lists every *.json artifact under checkpoints/, re-hashed and
// version-probed. MANIFEST.jsonl is not one of them.
func fsckCheckpointArtifactsOnDisk(l paths.Layout) ([]fsckCheckpointArtifact, error) {
	entries, err := fsckReadDir(l.Checkpoints)
	if err != nil {
		return nil, err
	}
	out := make([]fsckCheckpointArtifact, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.EqualFold(name, "MANIFEST.jsonl") {
			continue
		}
		p := filepath.Join(l.Checkpoints, name)
		raw, readErr := paths.ReadFileShared(p)
		if readErr != nil {
			out = append(out, fsckCheckpointArtifact{Name: name, Path: p, ParseErr: readErr.Error()})
			continue
		}
		art := fsckCheckpointArtifact{
			Name: name, Path: p, Bytes: int64(len(raw)),
			SHA256: core.Hash(sha256.Sum256(raw)).String(),
		}
		var probe struct {
			Version *int               `json:"version"`
			Seq     core.CheckpointSeq `json:"seq"`
		}
		switch {
		case json.Unmarshal(raw, &probe) != nil:
			art.ParseErr = "the artifact is not JSON"
		case probe.Version == nil:
			art.ParseErr = "the artifact carries no schema version"
		default:
			art.Version, art.Seq = *probe.Version, probe.Seq
		}
		out = append(out, art)
	}
	return out, nil
}

// ── 8. pins ────────────────────────────────────────────────────────────────────────────────────

// fsckPinRecord is one line of pins/invariants.jsonl. One type spells both verbs: an add omits
// "id", a tombstone omits "invariant" (internal/pins/store.go's record type).
type fsckPinRecord struct {
	Op        string `json:"op"`
	TS        int64  `json:"ts"`
	Invariant *struct {
		ID     string `json:"id"`
		Text   string `json:"text"`
		Source string `json:"source"`
		Pinned int64  `json:"pinned"`
	} `json:"invariant,omitempty"`
	ID string `json:"id,omitempty"`
}

// checkPins replays pins/invariants.jsonl and checks the three things the log itself promises: an
// id is minted from its own text, a tombstone names an add somebody recorded, and the
// pins/invariants.json view agrees with the live set. The view is derived, so a disagreement is one
// of the five things --repair regenerates; the LOG is never touched.
func (s *fsckScan) checkPins() fsckCheck {
	row := newFsckRow("pins", contract.SevWarn)

	lines, err := fsckReadLines(filepath.Join(s.l.Pins, "invariants.jsonl"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			row.note("pins/invariants.jsonl does not exist yet")
			row.scan(0)
			return row.build()
		}
		row.defect("pins/invariants.jsonl is unreadable, which pins.OpenWith treats as an error "+
			"rather than an empty store: %v", err)
		return row.build()
	}

	live := map[string]bool{}
	added := map[string]bool{}
	for i, raw := range lines {
		var rec fsckPinRecord
		if jsonErr := json.Unmarshal(raw, &rec); jsonErr != nil {
			row.defect("pins/invariants.jsonl:%d does not parse: %v", i+1, jsonErr)
			continue
		}
		s.applyPinRecord(row, i+1, rec, added, live)
	}
	row.scan(len(live))
	s.comparePinsView(row, live)
	return row.build()
}

// applyPinRecord folds one log record into the live set, naming what it cannot accept.
func (s *fsckScan) applyPinRecord(row *fsckRowBuilder, line int, rec fsckPinRecord, added, live map[string]bool) {
	switch rec.Op {
	case "add":
		if rec.Invariant == nil {
			row.defect("pins/invariants.jsonl:%d is an add carrying no invariant", line)
			return
		}
		if want := pins.MintID(rec.Invariant.Text); want != rec.Invariant.ID {
			row.defect("pins/invariants.jsonl:%d records id %q for text that mints %q",
				line, rec.Invariant.ID, want)
		}
		added[rec.Invariant.ID] = true
		live[rec.Invariant.ID] = true
	case "remove":
		if !added[rec.ID] {
			row.defect("pins/invariants.jsonl:%d is a tombstone for %q, which no add record in "+
				"this log ever recorded", line, rec.ID)
			return
		}
		delete(live, rec.ID)
	default:
		row.defect("pins/invariants.jsonl:%d carries verb %q, which this build does not know",
			line, rec.Op)
	}
}

// comparePinsView compares the derived view against the live set the log produced.
func (s *fsckScan) comparePinsView(row *fsckRowBuilder, live map[string]bool) {
	viewIDs, present, viewErr := fsckReadPinsView(s.l)
	switch {
	case viewErr != nil:
		row.defect("pins/invariants.json does not parse; the view is derived and --repair "+
			"regenerates it: %v", viewErr)
		return
	case !present:
		if len(live) > 0 {
			row.defect("pins/invariants.json is absent while the log carries %d live invariant(s); "+
				"the view is derived and --repair regenerates it", len(live))
		}
		return
	}
	for id := range live {
		if !viewIDs[id] {
			row.defect("pins/invariants.json omits live invariant %q; the view is stale", id)
		}
	}
	for id := range viewIDs {
		if !live[id] {
			row.defect("pins/invariants.json still carries %q, which the log has retired", id)
		}
	}
}

// fsckReadPinsView reads pins/invariants.json's id set. An absent view is a state, not a failure.
func fsckReadPinsView(l paths.Layout) (ids map[string]bool, present bool, err error) {
	raw, readErr := paths.ReadFileShared(filepath.Join(l.Pins, "invariants.json"))
	if readErr != nil {
		if errors.Is(readErr, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, true, readErr
	}
	var view []struct {
		ID string `json:"id"`
	}
	if jsonErr := json.Unmarshal(raw, &view); jsonErr != nil {
		return nil, true, jsonErr
	}
	ids = make(map[string]bool, len(view))
	for _, v := range view {
		ids[v.ID] = true
	}
	return ids, true, nil
}

// ── 9. negative knowledge ──────────────────────────────────────────────────────────────────────

// fsckEliminationRecord is the subset of one records/eliminations.jsonl line fsck reads.
type fsckEliminationRecord struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// checkNegativeKnowledge reports the elimination log's own health and classifies the tried.bloom
// load, WITHOUT opening negknow.Ledger.
//
// That omission is deliberate and load-bearing: negknow.Open calls paths.EnsureLayout and takes an
// O_CREATE append handle on records/eliminations.jsonl before it reads anything, so a read-only
// integrity scan that used it would write to the project it is inspecting. The ledger's own rebuild
// IS used by --repair, which holds the daemon lock and is allowed to write.
//
// Blind mode is what negknow calls a log that exists and cannot be READ (ledger.go's reasonBlind):
// under it every already_tried answer degrades to unavailable, so it is a first-class finding
// rather than a missing cache.
func (s *fsckScan) checkNegativeKnowledge() fsckCheck {
	row := newFsckRow("negknow", contract.SevWarn)

	active, stale, total := 0, 0, 0
	lines, err := fsckReadLines(filepath.Join(s.l.Records, "eliminations.jsonl"))
	switch {
	case err != nil && errors.Is(err, fs.ErrNotExist):
		row.note("records/eliminations.jsonl does not exist yet: nothing has been eliminated")
	case err != nil:
		row.defect("the elimination ledger is BLIND: records/eliminations.jsonl exists and cannot "+
			"be read, so every already_tried answer degrades to unavailable: %v", err)
	default:
		for i, raw := range lines {
			var rec fsckEliminationRecord
			if jsonErr := json.Unmarshal(raw, &rec); jsonErr != nil {
				row.defect("records/eliminations.jsonl:%d does not parse: %v", i+1, jsonErr)
				continue
			}
			total++
			switch negknow.Status(rec.Status) {
			case negknow.StatusActive:
				active++
			case negknow.StatusStale:
				stale++
			}
		}
	}
	row.scan(total)
	row.note("eliminations: %d record(s), %d active, %d stale", total, active, stale)

	s.classifyTriedBloom(row, active)
	return row.build()
}

// classifyTriedBloom loads sketches/tried.bloom and tells absence, corruption, truncation and an
// unsupported version apart through errors.Is on sketch's own sentinels. Load maps every failure to
// core.ErrNotFound at the boundary, and the sentinel underneath is the only thing that separates a
// cold start from bit rot.
func (s *fsckScan) classifyTriedBloom(row *fsckRowBuilder, activeRecords int) {
	p := filepath.Join(s.l.Sketches, sketch.TriedBloomBase)
	b := &sketch.Bloom{}
	err := sketch.LoadWithLog(p, b, logging.Nop())
	switch {
	case err == nil:
		m, k := b.Bits()
		row.note("tried.bloom loads: %d bit(s), k=%d, fill %.4f, estimated fp %.4f, saturated=%t",
			m, k, b.FillRatio(), b.EstimatedFPRate(), b.Saturated())
	case errors.Is(err, fs.ErrNotExist):
		if activeRecords > 0 {
			row.defect("sketches/tried.bloom is absent while %d active elimination record(s) exist; "+
				"--repair rebuilds it from those records", activeRecords)
			return
		}
		row.note("sketches/tried.bloom is absent, which is an ordinary cold start")
	case errors.Is(err, sketch.ErrCorrupt):
		row.defect("sketches/tried.bloom fails its CRC (bit rot, not a cold start); --repair "+
			"rebuilds it from the active records: %v", err)
	case errors.Is(err, sketch.ErrTruncated):
		row.defect("sketches/tried.bloom is truncated; --repair rebuilds it from the active "+
			"records: %v", err)
	case errors.Is(err, sketch.ErrUnsupportedVersion):
		row.note("sketches/tried.bloom declares a format version newer than this build reads, "+
			"which is a support gap rather than damage: %v", err)
	default:
		row.defect("sketches/tried.bloom will not load: %v", err)
	}

	switch seq, ok, seqErr := paths.HighestBloomBackupSeq(s.l); {
	case seqErr != nil:
		row.note("the surviving tried.bloom backups could not be listed: %v", seqErr)
	case ok:
		row.note("filter generation %d (the highest surviving tried.bloom.<n>.bak)", seq)
	}
	evidence, evidenceErr := fsckSketchEvidence(s.l)
	if evidenceErr != nil {
		row.defect("sketches/ could not be listed, so preserved corruption evidence was not "+
			"inventoried: %v", evidenceErr)
	}
	for _, name := range evidence {
		row.note("sketches/%s is preserved evidence of an earlier corruption and is never swept", name)
	}
}

// fsckSketchEvidence lists the quarantined sketch files sketch.Quarantine left behind.
func fsckSketchEvidence(l paths.Layout) ([]string, error) {
	entries, err := fsckReadDir(l.Sketches)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.Contains(e.Name(), ".corrupt.") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// ── 10. delivery journals and their seals ──────────────────────────────────────────────────────

// fsckDeliveryJournalBound mirrors internal/daemon's unexported deliveryLeaseMaxBytes: the size at
// which the journal's own loader refuses the file rather than reading it.
const fsckDeliveryJournalBound = 64 << 20

// fsckDeliveryEntryBound mirrors internal/daemon's deliveryLeaseMaxEntries.
const fsckDeliveryEntryBound = 65536

// checkDelivery inspects the two delivery journals and their position seals.
//
// In read-only mode it asks only what can be answered WITHOUT the lock: the journal lines parse and
// the file is within the bounds its own loader enforces, and the position file is either a v1 JSON
// document or the v2 binary A/B image. The full dual-reader check is daemon.RepairDeliverySeal's,
// which takes the daemon lock for the duration, so it runs only when no daemon holds it — and when
// one does, this row says which question was not asked.
//
// It never edits a journal. That rule is delivery_seal_tool.go's and it is inherited whole: the
// journals are the evidence, and every repair either tool can make is to a derived position file.
func (s *fsckScan) checkDelivery() fsckCheck {
	row := newFsckRow("delivery", contract.SevWarn)
	scanned := 0

	for _, name := range []string{"delivery-leases.jsonl", "delivery-acks.jsonl"} {
		path := filepath.Join(s.l.State, name)
		info, statErr := os.Stat(paths.Long(path))
		if statErr != nil {
			if !errors.Is(statErr, fs.ErrNotExist) {
				row.defect("state/%s cannot be stat'd, so this check did not run: %v", name, statErr)
			}
			continue
		}
		if info.Size() > fsckDeliveryJournalBound {
			row.defect("state/%s is %d bytes, past the %d-byte bound its own loader refuses",
				name, info.Size(), int64(fsckDeliveryJournalBound))
			continue
		}
		lines, err := fsckReadLines(path)
		if err != nil {
			row.defect("state/%s is unreadable; GC treats an unreadable lease set as a reason to "+
				"collect nothing: %v", name, err)
			continue
		}
		if len(lines) > fsckDeliveryEntryBound {
			row.defect("state/%s carries %d entries, past the %d its own loader reads",
				name, len(lines), fsckDeliveryEntryBound)
		}
		for i, raw := range lines {
			var probe map[string]json.RawMessage
			if jsonErr := json.Unmarshal(raw, &probe); jsonErr != nil {
				row.defect("state/%s:%d does not parse: %v", name, i+1, jsonErr)
				continue
			}
			scanned++
		}
	}
	row.scan(scanned)
	s.checkDeliveryPositions(row)
	return row.build()
}

// deliveryActiveSegment reads the active segment the delivery segment authority's head names
// (state/delivery-journal.json), and reports false when there is no head or it does not read. It is a
// classification aid for the read-only row only; the authority's full validation is --seal-check's.
func (s *fsckScan) deliveryActiveSegment() (uint64, bool) {
	raw, err := paths.ReadFileShared(filepath.Join(s.l.State, "delivery-journal.json"))
	if err != nil {
		return 0, false
	}
	var head struct {
		Active *uint64 `json:"active"`
	}
	if json.Unmarshal(raw, &head) != nil || head.Active == nil {
		return 0, false
	}
	return *head.Active, true
}

// checkDeliveryPositions classifies each position seal and, when the project is quiet, runs the
// offline tool's own full check.
func (s *fsckScan) checkDeliveryPositions(row *fsckRowBuilder) {
	for _, name := range []string{"delivery-lease-position.json", "delivery-ack-position.json"} {
		raw, err := paths.ReadFileShared(filepath.Join(s.l.State, name))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				row.defect("state/%s is unreadable, so its seal could not be classified: %v", name, err)
			}
			continue
		}
		var probe struct {
			V *int `json:"v"`
		}
		if jsonErr := json.Unmarshal(raw, &probe); jsonErr != nil {
			// A v2 seal is a binary A/B image, not JSON. Its slots are the offline tool's to read.
			row.note("state/%s is not JSON, which is what a v2 sealed position looks like; "+
				"its slots are read below", name)
			continue
		}
		if sealed, entries, frozen := daemon.FrozenDeliverySeal(name, raw); frozen {
			// The old-reader barrier: once the store has rotated, segment 0's seals are frozen so a build
			// that predates segments refuses the journal. It is only ever legitimate beside an authority
			// naming a later segment.
			if active, ok := s.deliveryActiveSegment(); ok && active >= 1 {
				row.note("state/%s is the frozen seal of the archived legacy segment (%d entries, %d bytes); "+
					"the store has rotated to segment %d, whose journals --seal-check reads", name, entries, sealed, active)
			} else {
				row.defect("state/%s is a frozen legacy-segment seal, but no readable segment authority "+
					"names a later segment: either a rotation stopped between freezing segment 0 and "+
					"committing its transition, which the daemon finishes at its next start when the window "+
					"was archived, or damage", name)
			}
			continue
		}
		switch {
		case probe.V == nil:
			row.defect("state/%s parses as JSON and carries no version", name)
		case *probe.V != 1 && *probe.V != 2:
			row.defect("state/%s declares version %d; this build's reader takes 1 and 2", name, *probe.V)
		default:
			row.note("state/%s is a v%d position document", name, *probe.V)
		}
	}

	if !s.sealCheck {
		row.note("the full dual-reader seal check was NOT run: it acquires the daemon lock, which " +
			"the default run never does; pass --seal-check, or run " +
			"`qompack admin delivery-seal --check` on a stopped project")
		return
	}
	var sink bytes.Buffer
	if err := repairDeliverySeal(daemon.DeliverySealOptions{
		ProjectRoot: s.root, Check: true, Out: &sink,
	}); err != nil {
		if errors.Is(err, daemon.ErrLockHeld) {
			// It yields rather than competing: the check needs the lock and a daemon owns it, so
			// the row says which question was not asked instead of blocking or failing the run.
			row.note("--seal-check yielded: %v", err)
			return
		}
		row.defect("the delivery seals do not check out: %v", err)
		return
	}
	row.note("the full dual-reader seal check passed")
}

// ── 11. spool and drain ────────────────────────────────────────────────────────────────────────

// fsckDrainRecord is one entry of state/drain.json (internal/daemon's drainFileRecord wire shape).
type fsckDrainRecord struct {
	Size        int64    `json:"size"`
	Offset      int64    `json:"offset"`
	Done        bool     `json:"done"`
	DurableSize bool     `json:"durable_size,omitempty"`
	Pending     []string `json:"pending_blobs,omitempty"`
}

// checkSpool compares state/drain.json with the spool it describes, in internal/daemon's own
// DrainGapKind vocabulary.
//
// What fsck absorbs is the REFUSAL case — a progress document that disagrees with its spool, which
// wedges recording until someone looks. Unreplayed spool bytes are a RECORDING GAP rather than a
// defect and belong to `qompack doctor`; they are reported here as an observation.
func (s *fsckScan) checkSpool() fsckCheck {
	row := newFsckRow("spool", contract.SevWarn)

	raw, err := paths.ReadFileShared(filepath.Join(s.l.State, "drain.json"))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			row.defect("%s: state/drain.json is unreadable: %v", daemon.DrainGapProgressUnreadable, err)
		} else {
			row.note("state/drain.json does not exist yet: no drain has run")
		}
		row.scan(0)
		return row.build()
	}
	var state map[string]fsckDrainRecord
	if jsonErr := json.Unmarshal(raw, &state); jsonErr != nil {
		row.defect("%s: state/drain.json does not parse: %v", daemon.DrainGapProgressUnreadable, jsonErr)
		return row.build()
	}
	row.scan(len(state))

	for base, rec := range state {
		s.checkOneDrainRecord(row, base, rec)
	}
	return row.build()
}

// checkOneDrainRecord names the three disagreements the drainer itself would report.
func (s *fsckScan) checkOneDrainRecord(row *fsckRowBuilder, base string, rec fsckDrainRecord) {
	if rec.Offset > rec.Size {
		row.defect("%s: drain progress for %s records offset %d past size %d, which loadState refuses",
			daemon.DrainGapProgressUnreadable, base, rec.Offset, rec.Size)
		return
	}
	info, statErr := os.Stat(paths.Long(filepath.Join(s.l.Spool, base)))
	if statErr != nil {
		if rec.Offset < rec.Size {
			row.defect("%s: drain progress claims %d unconsumed byte(s) of spool file %s, which is gone",
				daemon.DrainGapProgressUnreadable, rec.Size-rec.Offset, base)
		}
		return
	}
	if info.Size() < rec.Size {
		row.defect("%s: spool file %s is %d bytes, below the %d-byte durable bound the drain "+
			"recorded; validateProgress refuses the spool over this",
			daemon.DrainGapProgressUnreadable, base, info.Size(), rec.Size)
		return
	}
	if info.Size() > rec.Offset {
		row.note("%s: %s has %d spool byte(s) not yet replayed, which the next drain consumes",
			daemon.DrainGapPending, base, info.Size()-rec.Offset)
	}
}

// ── 12. retention roots and pending markers ────────────────────────────────────────────────────

// fsckRetentionLine is one line of state/retention-roots.jsonl (store.RetentionRoot's wire shape).
type fsckRetentionLine struct {
	Hash   string `json:"hash"`
	Class  string `json:"class"`
	Reason string `json:"reason"`
}

// checkRetention resolves every hash a producer asked GC to retain, and parses the pending markers.
//
// An `evidence`-class root does NOT name an object. It names a capture sidecar's bytes_hash, whose
// bytes live under records/captures/ precisely so GC cannot reach them, so resolving one against
// objects/ would report every ordinary capture on a healthy project as dangling
// (commit4-evidence.md §8, calibration rule 3).
//
// A line that does not parse is worth naming even though it does not stop collection: gcrun.go's
// declaredRetentionLine retains EVERYTHING on a line it cannot read, under the blanket rollback
// class, so a rotted file over-retains and carries a claim nobody can check (calibration rule 4).
func (s *fsckScan) checkRetention() fsckCheck {
	row := newFsckRow("retention", contract.SevWarn)

	path := store.RetentionRootsPath(s.root)
	lines, err := fsckReadLines(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			row.note("state/retention-roots.jsonl does not exist yet")
		} else {
			row.defect("state/retention-roots.jsonl is unreadable, so every claim it carried is "+
				"lost to the reader that enforces it: %v", err)
		}
		row.scan(0)
		s.checkPendingMarkers(row)
		return row.build()
	}
	row.scan(len(lines))

	for i, raw := range lines {
		var rl fsckRetentionLine
		if jsonErr := json.Unmarshal(raw, &rl); jsonErr != nil {
			row.defect("state/retention-roots.jsonl:%d does not parse, so GC over-retains "+
				"everything on it under the blanket rollback class: %v", i+1, jsonErr)
			continue
		}
		if rl.Hash == "" || fsckIsZeroHash(rl.Hash) || s.fsckHashHeld(rl.Hash) {
			continue
		}
		if rl.Class == string(store.RetentionEvidence) && s.sidecarBytes[fsckCanonHash(rl.Hash)] {
			continue
		}
		row.defect("retention root (class %q, %q) names %s, which is not held",
			rl.Class, rl.Reason, fsckShortHash(rl.Hash))
	}
	s.checkPendingMarkers(row)
	return row.build()
}

// checkPendingMarkers parses every state/pending/*.json marker.
func (s *fsckScan) checkPendingMarkers(row *fsckRowBuilder) {
	dir := filepath.Join(s.l.State, "pending")
	entries, err := fsckReadDir(dir)
	if err != nil {
		row.defect("state/pending could not be listed, so no pending marker was checked: %v", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, readErr := paths.ReadFileShared(filepath.Join(dir, e.Name()))
		if readErr != nil {
			row.defect("state/pending/%s is unreadable: %v", e.Name(), readErr)
			continue
		}
		var probe map[string]json.RawMessage
		if jsonErr := json.Unmarshal(raw, &probe); jsonErr != nil {
			row.defect("state/pending/%s does not parse: %v", e.Name(), jsonErr)
		}
	}
}

// ── 13. migration records and backups ──────────────────────────────────────────────────────────

// checkMigrateAndBackups uses the supported read-only backup verifier and keeps
// migration schema inspection separate from import/cutover enablement.
func (s *fsckScan) checkMigrateAndBackups() fsckCheck {
	row := newFsckRow("migrate", contract.SevWarn)

	row.note("backups are checked by the read-only maintenance verifier; legacy import/cutover is separate")

	backups := 0
	entries, err := fsckReadDir(s.l.Backup)
	if err != nil {
		row.defect("backup/ could not be listed, so no backup was verified: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), ".certification-pending") {
			row.defect("a backup has pending writer lease certification; retain the failed attempt and create a new backup")
			continue
		}
		if !e.IsDir() {
			continue
		}
		backups++
		s.verifyOneBackup(row, e.Name())
	}
	row.scan(backups)
	s.checkMigrateRecords(row)
	return row.build()
}

// verifyOneBackup applies VerifyBackup's own rule to one backup directory: the manifest is a
// version this build reads, and every file it names is present at the recorded size and digest.
func (s *fsckScan) verifyOneBackup(row *fsckRowBuilder, id string) {
	if _, err := store.VerifyBackupAt(s.ctx, s.root, id); err != nil {
		row.defect("backup %q is not a verified recovery path: %v", id, err)
	}
}

// fsckRawDigest is the bare sha256 a backup manifest and a checkpoint manifest both record.
func fsckRawDigest(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// checkMigrateRecords parses the six migrate/ records and checks that each declares a version this
// build reads. Three are whole documents and three are per-line logs, which is the append-only
// distinction internal/store/migrate.go draws.
func (s *fsckScan) checkMigrateRecords(row *fsckRowBuilder) {
	for _, name := range []string{"cursor.json", "handoff.json", "parity.json"} {
		raw, err := paths.ReadFileShared(filepath.Join(s.l.Migrate, name))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				row.defect("migrate/%s is unreadable, so its version could not be checked: %v", name, err)
			}
			continue
		}
		var probe struct {
			Version *int `json:"version"`
		}
		switch {
		case json.Unmarshal(raw, &probe) != nil:
			row.defect("migrate/%s does not parse", name)
		case probe.Version == nil || *probe.Version != fsckKnownRecordVersion:
			row.defect("migrate/%s declares a version this build does not read", name)
		}
	}
	for _, name := range []string{"mapping.jsonl", "rollback.jsonl", "newformat.jsonl"} {
		lines, err := fsckReadLines(filepath.Join(s.l.Migrate, name))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				row.defect("migrate/%s is unreadable, so its records could not be checked: %v", name, err)
			}
			continue
		}
		for i, raw := range lines {
			var probe struct {
				Version *int `json:"version"`
			}
			switch {
			case json.Unmarshal(raw, &probe) != nil:
				row.defect("migrate/%s:%d does not parse", name, i+1)
			case probe.Version == nil || *probe.Version != fsckKnownRecordVersion:
				row.defect("migrate/%s:%d declares a version this build does not read", name, i+1)
			}
		}
	}
}

// ── 14. fidelity summary ───────────────────────────────────────────────────────────────────────

// checkFidelity counts what RestoreOriginal says about every live root, VERBATIM.
//
// This is plan §3's "without converting missing/unknown fidelity into exactness", and it is the one
// check that needs a real store, because only the store knows whether a root carried a verbatim
// claim or a retained original. FidelityCanonical with readable bytes is the normal non-error
// state: the bytes read back perfectly and they are still not the original bytes, so a tool that
// inferred exactness from a successful read would be wrong on the majority of a healthy store.
//
// It is the one check that needs a store handle, and it takes a READ-ONLY one
// (store.OpenReadOnly, ruling R5-A): the ordinary Open runs EnsureLayout and takes O_CREATE handles
// on five index files, and a diagnostic that manufactured empty index files inside a
// half-restored project would make the next pass report a clean empty index instead of a missing
// one. The read-only store also never quarantines what it rejects, so a root whose bytes are
// damaged is reported as corrupt rather than being silently relocated by the act of asking.
func (s *fsckScan) checkFidelity() fsckCheck {
	row := newFsckRow("fidelity", contract.SevWarn)

	if len(s.roots) == 0 {
		row.scan(0)
		row.note("no live root to restore")
		return row.build()
	}

	opened, err := store.OpenReadOnly(s.root, config.Defaults(), store.Deps{Log: logging.Nop()})
	if err != nil {
		row.defect("the store will not open read-only, so no fidelity could be resolved: %v", err)
		return row.build()
	}
	defer func() { _ = opened.Close() }()

	resolved := 0
	for text := range s.roots {
		if s.ctx.Err() != nil {
			row.note("the scan was cancelled after %d root(s)", resolved)
			break
		}
		if s.tombstoned[text] {
			continue
		}
		h, parseErr := core.ParseHash(text)
		if parseErr != nil {
			continue
		}
		_, fidelity, restoreErr := opened.RestoreOriginal(s.ctx, h)
		resolved++
		s.report.Fidelity[string(fidelity)]++
		if fidelity == store.FidelityCorrupt {
			row.defect("root %s restores as %s: %v", fsckShortHash(text), fidelity, restoreErr)
		}
	}
	row.scan(resolved)
	row.note("fidelity is counted as the store reports it; a canonical or unavailable root is " +
		"never promoted to exact")
	return row.build()
}

// ── 15. quarantine inventory ───────────────────────────────────────────────────────────────────

// checkQuarantine tallies tmp/quarantine/**. Everything under it is EVIDENCE: §12.3 puts a rejected
// object there so a person can look at it, backup.go excludes tmp/ so it exists nowhere else, and
// obj-* staging debris shares the directory — so a naive sweep would delete the evidence along with
// the litter. Nothing here ever removes a file.
func (s *fsckScan) checkQuarantine() fsckCheck {
	row := newFsckRow("quarantine", contract.SevInfo)
	dir := filepath.Join(s.l.Tmp, "quarantine")

	err := filepath.WalkDir(paths.Long(dir), func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil //nolint:nilerr // a vanished entry is an observation, not a failure
		}
		s.report.Quarantine.Files++
		s.report.Quarantine.Bytes += info.Size()
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		row.note("tmp/quarantine could not be walked: %v", err)
	}
	row.scan(s.report.Quarantine.Files)
	row.note("%d quarantined file(s), %d byte(s), preserved as evidence and never swept",
		s.report.Quarantine.Files, s.report.Quarantine.Bytes)
	return row.build()
}

// ── the five explicit repairs ──────────────────────────────────────────────────────────────────

// The repair surface is deliberately tiny, and every member of it is either ADDITIVE or a
// regeneration of a derived file from the append-only log that is its truth:
//
//  1. index/files.json      — regenerated from index/files.jsonl
//  2. pins/invariants.json  — regenerated from pins/invariants.jsonl, through pins' own Materialize
//  3. checkpoints/MANIFEST.jsonl — one appended line for an orphan artifact that re-hashes cleanly
//     and parses at a known schema version, through paths.AppendManifest, the only legal writer
//  4. sketches/tried.bloom  — rebuilt from ACTIVE elimination records through the ledger's own
//     RebuildBloom, which replaces generationally and bumps the sequence
//  5. tmp/quarantine/       — an object that fails verification is moved there through the store's
//     own quarantine path, which is §12.3's behaviour and keeps the bytes as evidence
//
// Nothing else. It never deletes, never edits a journal, never rewrites a roots/tool_use/segments
// line, never touches a capture sidecar, a checkpoint artifact or a backup, and never runs GC —
// `--gc-all`, which internal/store's gcrun.go mentions as the way to disable retention, is NOT
// implemented by this command and no flag here reaches a GCPolicy.
//
// After a repair, everything an older reader reads is unchanged. That is the rollback-safety claim
// and TestFsck_RepairPerformsOnlyTheFiveAdditiveKinds proves it by snapshot diff.

// performFsckRepairs takes the daemon lock and performs the five repairs.
//
// The lock comes first and is held for the whole run, exactly as daemon.RepairDeliverySeal does, so
// a project a daemon is serving is refused and nothing is read or written. The refusal's message
// shape is that tool's, because an operator meets both of them in the same situation.
func performFsckRepairs(o fsckRepairOptions) ([]fsckRepair, error) {
	switch {
	case o.Out == nil:
		return nil, errors.New("fsck: no writer for the repair report")
	case o.ProjectRoot == "":
		return nil, errors.New("fsck: no project root")
	case !o.Confirm:
		return nil, errors.New("fsck: --repair writes to the project and needs explicit confirmation")
	}

	addr, err := ipc.Resolve(o.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("fsck: resolving the daemon endpoint: %w", err)
	}
	lock, err := daemon.AcquireLock(o.ProjectRoot, addr, o.Clock)
	if err != nil {
		if errors.Is(err, daemon.ErrLockHeld) {
			return nil, fmt.Errorf("fsck: a daemon is running in %s and owns the project; "+
				"stop it first: %w", o.ProjectRoot, err)
		}
		return nil, fmt.Errorf("fsck: taking the daemon lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	// A repair is an operator action on a stopped project, not a request inside a turn: it has no
	// deadline of its own, and the lock is what bounds concurrency.
	ctx := context.Background()
	l := paths.Of(o.ProjectRoot)
	clk := o.Clock
	if clk == nil {
		clk = core.SystemClock()
	}

	var out []fsckRepair
	out = append(out, fsckRepairFilesView(l, clk)...)
	out = append(out, fsckRepairPinsView(ctx, o.ProjectRoot, clk)...)
	out = append(out, fsckRepairOrphanManifestLines(l, clk)...)
	out = append(out, fsckRepairTriedBloom(ctx, o.ProjectRoot, l)...)
	out = append(out, fsckRepairQuarantineObjects(ctx, o.ProjectRoot, l)...)
	return out, nil
}

// fsckFileDigest renders a file's current state for a repair's before/after column: its digest, or
// why it could not be read.
func fsckFileDigest(p string) string {
	raw, err := paths.ReadFileShared(p)
	if err != nil {
		return "absent"
	}
	return fmt.Sprintf("%s (%d bytes)", hex.EncodeToString(fsckRawDigest(raw))[:12], len(raw))
}

// fsckRepairFilesView regenerates index/files.json from index/files.jsonl when the two disagree.
//
// The regeneration itself belongs to internal/store — RegenerateFilesView is the exported name of
// the operation Flush performs, sharing one writer and one view shape with it (SP-17 R5-1) — so
// what is left here is the repair's own reporting: the before and after digests an operator reads,
// and the rule that a regeneration which did not happen is not a repair row.
func fsckRepairFilesView(l paths.Layout, clk core.Clock) []fsckRepair {
	p := filepath.Join(l.Index, "files.json")
	before := fsckFileDigest(p)
	wrote, err := store.RegenerateFilesView(l, clk)
	switch {
	case err != nil:
		return []fsckRepair{{
			Kind: "files.view", Target: "index/files.json",
			Before: before, After: "unchanged: " + err.Error(),
		}}
	case !wrote:
		// Either the view already describes the log, or an absent view stands over an empty log.
		// Neither is a defect checkFiles reports, and a repair surface wider than the defect
		// surface is how "no destructive default cleanup" starts to erode.
		return nil
	}
	return []fsckRepair{{
		Kind: "files.view", Target: "index/files.json",
		Before: before, After: fsckFileDigest(p),
	}}
}

// fsckRepairPinsView regenerates pins/invariants.json through pins' own Materialize, which is the
// only writer of that projection and goes through paths.ReplacePinsView's sanctioned door.
func fsckRepairPinsView(ctx context.Context, root string, clk core.Clock) []fsckRepair {
	l := paths.Of(root)
	p := filepath.Join(l.Pins, "invariants.json")

	// Only when the scan would have reported it: an absent view over a log with no live invariant
	// is not a defect, and regenerating it would write into a project that had nothing wrong.
	viewIDs, present, viewErr := fsckReadPinsView(l)
	live := fsckLivePinIDs(l)
	if viewErr == nil && fsckPinViewAgrees(viewIDs, present, live) {
		return nil
	}

	before := fsckFileDigest(p)
	ps, err := pins.OpenWith(root, logging.Nop(), obs.New(clk), clk)
	if err != nil {
		return []fsckRepair{{
			Kind: "pins.view", Target: "pins/invariants.json",
			Before: before, After: "unchanged: " + err.Error(),
		}}
	}
	if err := ps.Materialize(ctx); err != nil {
		return []fsckRepair{{
			Kind: "pins.view", Target: "pins/invariants.json",
			Before: before, After: "unchanged: " + err.Error(),
		}}
	}
	after := fsckFileDigest(p)
	if after == before {
		return nil
	}
	return []fsckRepair{{
		Kind: "pins.view", Target: "pins/invariants.json", Before: before, After: after,
	}}
}

// fsckRepairOrphanManifestLines appends one manifest line per orphan checkpoint artifact that
// re-hashes cleanly and parses at a schema version this build reads.
//
// It goes through paths.AppendManifest because that is the ONLY legal writer of
// checkpoints/MANIFEST.jsonl (paths.IsProtected covers the whole checkpoints/ subtree). The
// artifact itself is never rewritten: finalize.go left it on disk precisely so a session would not
// be lost, and reconciling it is an append.
func fsckRepairOrphanManifestLines(l paths.Layout, clk core.Clock) []fsckRepair {
	entries, err := paths.ReadManifest(l)
	if err != nil {
		return nil // a manifest that will not read is a defect to report, never one to append to
	}
	recorded := make(map[string]bool, len(entries))
	for _, e := range entries {
		recorded[filepath.Base(paths.CheckpointPath(l, e.Seq))] = true
	}

	artifacts, artErr := fsckCheckpointArtifactsOnDisk(l)
	if artErr != nil {
		return []fsckRepair{{
			Kind: "checkpoint.manifest", Target: "checkpoints/",
			Before: "could not be listed", After: "unchanged: " + artErr.Error(),
		}}
	}
	var out []fsckRepair
	for _, art := range artifacts {
		switch {
		case recorded[art.Name], art.ParseErr != "":
			continue
		case art.Version < 1 || art.Version > checkpoint.SchemaVersion:
			continue // a newer plugin's artifact is reported, never indexed by this build
		}
		entry := paths.ManifestEntry{
			Seq: art.Seq, SHA256: art.SHA256, Bytes: art.Bytes, Created: core.NowMilli(clk),
		}
		after := fmt.Sprintf("manifest line seq %04d sha %s", int(art.Seq), fsckShortHash(art.SHA256))
		if appendErr := paths.AppendManifest(l, entry); appendErr != nil {
			after = "unchanged: " + appendErr.Error()
		}
		out = append(out, fsckRepair{
			Kind: "checkpoint.manifest", Target: "checkpoints/" + art.Name,
			Before: "orphan: present with no MANIFEST line", After: after,
		})
	}
	return out
}

// fsckRepairTriedBloom rebuilds sketches/tried.bloom from the ACTIVE elimination records, through
// negknow's own RebuildBloom — never from a checkpoint and never from context (§13 invariant 2).
//
// It runs only when the filter is actually broken or missing while records exist. Rebuilding a
// healthy filter would bump the generation for nothing and consume the one backup generation
// ReplaceGenerational keeps.
func fsckRepairTriedBloom(ctx context.Context, root string, l paths.Layout) []fsckRepair {
	p := filepath.Join(l.Sketches, sketch.TriedBloomBase)
	loadErr := sketch.LoadWithLog(p, &sketch.Bloom{}, logging.Nop())
	if loadErr == nil {
		return nil
	}
	if errors.Is(loadErr, fs.ErrNotExist) && !fsckHasActiveEliminations(l) {
		return nil // an ordinary cold start
	}

	before := fsckFileDigest(p)
	ledger, err := negknow.Open(root, config.Defaults(), nil, negknow.Deps{
		Log: logging.Nop(), Clock: core.SystemClock(),
	})
	if err != nil {
		return []fsckRepair{{
			Kind: "negknow.bloom", Target: "sketches/tried.bloom",
			Before: before, After: "unchanged: " + err.Error(),
		}}
	}
	defer func() { _ = ledger.Close() }()

	_, health, rebuildErr := ledger.RebuildBloom(ctx)
	after := fsckFileDigest(p)
	if rebuildErr != nil {
		after = "unchanged: " + rebuildErr.Error()
	}
	return []fsckRepair{{
		Kind: "negknow.bloom", Target: "sketches/tried.bloom", Before: before,
		After: fmt.Sprintf("%s, rebuilt from %d active record(s), generation %d",
			after, health.Active, health.FilterGeneration),
	}}
}

// fsckHasActiveEliminations reports whether any elimination record is still active, which is what
// makes an absent filter a defect rather than a cold start.
func fsckHasActiveEliminations(l paths.Layout) bool {
	lines, err := fsckReadLines(filepath.Join(l.Records, "eliminations.jsonl"))
	if err != nil {
		return false
	}
	for _, raw := range lines {
		var rec fsckEliminationRecord
		if json.Unmarshal(raw, &rec) == nil && negknow.Status(rec.Status) == negknow.StatusActive {
			return true
		}
	}
	return false
}

// fsckRepairQuarantineObjects moves every object that fails verification into tmp/quarantine,
// through the STORE's own quarantine path rather than a rename of this command's own.
//
// It calls store.Quarantiner directly. It used to call store.GetChunk and rely on getObject's
// SIDE EFFECT to perform the move, which is R5-2: the repair's behaviour depended on a read path's
// incidental consequence, so a read that stopped quarantining — or one that rejected for a reason
// getObject does not check, which is most of what fsckFailingObjects finds — silently stopped
// repairing. Asking for the operation by name means the moved file lands where every other rejected
// object lands, with the store's own reason string and counters (§12.3), and the bytes are kept as
// evidence rather than deleted.
//
// A store that cannot be type-asserted to Quarantiner is reported, not worked around: a repair that
// quietly did nothing would be worse than one that says it could not.
func fsckRepairQuarantineObjects(ctx context.Context, root string, l paths.Layout) []fsckRepair {
	bad := fsckFailingObjects(l)
	if len(bad) == 0 {
		return nil
	}
	opened, err := store.Open(root, config.Defaults(), store.Deps{Log: logging.Nop()})
	if err != nil {
		return []fsckRepair{{
			Kind: "object.quarantine", Target: "objects/",
			Before: fmt.Sprintf("%d object(s) fail verification", len(bad)),
			After:  "unchanged: the store will not open: " + err.Error(),
		}}
	}
	defer func() { _ = opened.Close() }()

	q, ok := opened.(store.Quarantiner)
	if !ok {
		return []fsckRepair{{
			Kind: "object.quarantine", Target: "objects/",
			Before: fmt.Sprintf("%d object(s) fail verification", len(bad)),
			After:  "unchanged: this store does not offer the quarantine operation",
		}}
	}

	out := make([]fsckRepair, 0, len(bad))
	for _, h := range bad {
		if ctx.Err() != nil {
			// A cancelled repair reports what it did and stops. The moves already made are durable
			// and are already in `out`; inventing rows for the rest would claim work never done.
			break
		}
		p, present := fsckObjectPath(l, h)
		before := "fails verification at " + filepath.Base(p)
		if !present {
			continue
		}
		moveErr := q.Quarantine(h, "fsck --repair: object fails verification")
		after := "still in objects/"
		if _, stillThere := fsckObjectPath(l, h); !stillThere {
			after = "moved to tmp/quarantine/ as evidence"
		}
		if moveErr != nil {
			after += fmt.Sprintf(" (the store reported %v)", moveErr)
		}
		out = append(out, fsckRepair{
			Kind: "object.quarantine", Target: "objects/" + filepath.Base(p),
			Before: before, After: after,
		})
	}
	return out
}

// fsckFailingObjects re-runs the object check's own verification and returns the hashes that fail.
func fsckFailingObjects(l paths.Layout) []core.Hash {
	var bad []core.Hash
	_ = filepath.WalkDir(paths.Long(l.Objects), func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil //nolint:nilerr // a vanished entry is an observation, not a failure
		}
		name := d.Name()
		h, parseErr := core.ParseHash(strings.TrimSuffix(name, fsckObjectSuffix))
		if parseErr != nil {
			return nil
		}
		// The same bound checkObjects applies, for the same reason: a repair must not read an
		// unbounded file into memory to decide whether to move it, and a file the store's own
		// reader refuses is exactly what a repair is being asked about.
		if info, infoErr := d.Info(); infoErr != nil || info.Size() > fsckObjectSizeLimit(name) {
			bad = append(bad, h)
			return nil
		}
		raw, readErr := paths.ReadFileShared(p)
		if readErr != nil {
			bad = append(bad, h)
			return nil
		}
		plain := raw
		if strings.HasSuffix(name, fsckObjectSuffix) {
			decoded, decErr := store.Decode(raw)
			if decErr != nil {
				bad = append(bad, h)
				return nil
			}
			plain = decoded
		}
		if core.HashBytes(core.DomainChunk, plain) != h {
			bad = append(bad, h)
		}
		return nil
	})
	return bad
}

// fsckDaemonLiveness decides whether a daemon is actually SERVING this project (ruling R5-C).
//
// daemon.ReadLock answers a narrower question than it looks like it does: whether a lock FILE
// exists and parses. A hand-written or abandoned lock naming a dead pid satisfies it, and treating
// that as "a daemon is running" made two read-only checks skip themselves and still report ok —
// so a stale lock could hide a real defect behind a clean exit 0. The staleness protocol's own
// decisive step is the liveness dial (lock.go's step 2), so that is what both commands ask.
//
// held says a lock file is there; alive says its listener answered. A held-but-not-alive lock is
// reported as stale and every check that needs a quiet project still runs.
func fsckDaemonLiveness(root string) (info daemon.LockInfo, held, alive bool) {
	info, held = daemon.ReadLock(root)
	if !held {
		return info, false, false
	}
	addr, err := ipc.Resolve(root)
	if err != nil {
		return info, true, false
	}
	if info.Addr != "" {
		addr = ipc.Addr{Kind: addr.Kind, Path: info.Addr}
	}
	return info, true, ipc.Probe(addr, selfTestProbeTimeout)
}

// fsckLivePinIDs replays pins/invariants.jsonl into the set of live invariant ids, applying the
// same add/tombstone rules checkPins does. It is what makes the pins repair fire only for the
// disagreement the scan would have reported.
func fsckLivePinIDs(l paths.Layout) map[string]bool {
	live := map[string]bool{}
	added := map[string]bool{}
	lines, err := fsckReadLines(filepath.Join(l.Pins, "invariants.jsonl"))
	if err != nil {
		return live
	}
	for _, raw := range lines {
		var rec fsckPinRecord
		if json.Unmarshal(raw, &rec) != nil {
			continue
		}
		switch {
		case rec.Op == "add" && rec.Invariant != nil:
			added[rec.Invariant.ID] = true
			live[rec.Invariant.ID] = true
		case rec.Op == "remove" && added[rec.ID]:
			delete(live, rec.ID)
		}
	}
	return live
}

// fsckPinViewAgrees reports whether the materialized view already describes the live set.
func fsckPinViewAgrees(viewIDs map[string]bool, present bool, live map[string]bool) bool {
	if !present {
		return len(live) == 0
	}
	if len(viewIDs) != len(live) {
		return false
	}
	for id := range live {
		if !viewIDs[id] {
			return false
		}
	}
	return true
}

// fsckReadDir lists a directory, telling "it is not there" apart from "it could not be read".
//
// os.ReadDir alone cannot: on Windows, listing a path that is a regular FILE comes back as
// ERROR_PATH_NOT_FOUND, which Go maps to fs.ErrNotExist — so a guard that treated ErrNotExist as
// "absent, nothing to check" reported a clean row for a directory that had been replaced by a file.
// An absent directory returns (nil, nil); anything else returns the error, and ruling R5-D makes
// every caller report it.
func fsckReadDir(p string) ([]os.DirEntry, error) {
	fi, err := os.Lstat(paths.Long(p))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", p)
	}
	return os.ReadDir(paths.Long(p))
}
