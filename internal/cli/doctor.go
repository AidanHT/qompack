package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// `qompack doctor` reports what this installation IS, what it can do here, and what it cannot
// currently record or retrieve. It is a report, not a test: like `status` it always exits 0 except
// for a malformed invocation, because "nothing could be reached" is one of its answers rather than
// a failure to produce one.
//
// Four rules shape it:
//
//  1. It never probes the host. §7.5 is explicit that repository JSON parsing alone does not
//     validate an installed plugin, so every capability row cites the evidence register and the
//     observation ledger rather than asserting an installed-host fact, and the Claude CLI row says
//     plainly that it was not probed. A ledger entry the session's start left pending is read
//     against state/history.json, which records the MCP handshake and the probe's delivery or its
//     spent chances after the start (D50, contract.RefreshObservation); that too is a record the
//     daemon kept, not a probe.
//  2. It never creates a project. An absent `.qompack` is a REPORTED FACT
//     (qompack_commands.go's READ-ONLY DISCIPLINE block), which is also why it loads configuration
//     through config.Load directly rather than LoadConfigAndReport: that helper persists the
//     violation list, and a diagnostic that wrote to state/ would be the one command a broken
//     project could not survive.
//  3. It uses the TOLERANT config path for everything it reports about the configuration. The hook
//     path's config.LoadForCapture still refuses a whole delivery over a structural problem (a
//     config file that is not strict JSONC, any problem inside runtime.redact); if doctor loaded
//     through it, that config would silence the one command that could explain it. Here a
//     violation is a row — and so is the hook path's own verdict (captureConfigRow), asked
//     separately and read-only.
//  4. It never calls a ratio or a latency figure proof of health. Storage and timing numbers are
//     labelled observations where they appear at all, and `status` owns them.

// doctorSchema is the version of the `--json` document.
const doctorSchema = 1

// The four status values a row can carry. They are a closed vocabulary because a reader branches
// on them: "unknown" is never a licence to act (contract/capability.go:105-106), and it is a
// different answer from "disabled", which is a decision somebody made.
const (
	doctorOK       = "ok"
	doctorDegraded = "degraded"
	doctorUnknown  = "unknown"
	doctorDisabled = "disabled"
)

// doctorGate is one gated switch's MigrationGate row, rendered for an operator.
type doctorGate struct {
	Owner  string `json:"owner"`
	Gate   string `json:"gate"`
	Passed bool   `json:"passed"`
}

// doctorRow is one reported fact.
type doctorRow struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Observed string `json:"observed"`
	Detail   string `json:"detail,omitempty"`
	// Coverage is the observation ledger's newest entry's, or its refresh from state/history.json
	// (D50, contract.RefreshObservation), derived by contract.ObservationsOf's rules either way; it
	// is never "complete".
	Coverage string      `json:"coverage,omitempty"`
	Gate     *doctorGate `json:"gate,omitempty"`
}

// doctorSectionDoc is one section of the report.
type doctorSectionDoc struct {
	ID    string      `json:"id"`
	Title string      `json:"title"`
	Rows  []doctorRow `json:"rows"`
}

// doctorReport is `--json`'s top-level document.
type doctorReport struct {
	Schema   int                `json:"schema"`
	Project  string             `json:"project"`
	Sections []doctorSectionDoc `json:"sections"`
	Exit     int                `json:"exit"`
}

// doctorCmds is the doctor subcommand table, following adminCmds' pattern.
func doctorCmds() []Cmd {
	return []Cmd{{
		Name:    "doctor",
		Summary: "report version, scope, per-capability evidence, disabled controls and gaps",
		Run:     runDoctor,
	}}
}

// runDoctor implements `qompack doctor [--project <root>] [--json]`.
func runDoctor(ctx context.Context, env Env, args []string, out, errw io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(errw)
	project := fs.String("project", "",
		"project root (defaults to QOMPACK_PROJECT_ROOT or the process cwd's nearest .git)")
	asJSON := fs.Bool("json", false, "emit the report as JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return errUsageReported // flag has already written its complaint and the usage to errw.
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errw, "qompack doctor: unexpected argument %q\n", fs.Arg(0))
		return errUsageReported
	}

	root := *project
	if root == "" {
		root = resolveProjectRoot(env, nil)
	}
	clk := env.Clock
	if clk == nil {
		clk = core.SystemClock()
	}

	report := collectDoctorReport(ctx, root, env, clk)
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(report)
	}
	writeDoctorTable(out, report)
	return nil
}

// doctorState is what the sections share: the layout, whether the project exists at all, the
// tolerant configuration load, and the persisted contract memory.
type doctorState struct {
	ctx         context.Context
	root        string
	l           paths.Layout
	env         Env
	clk         core.Clock
	established bool // .qompack is there
	cfg         config.Config
	prov        config.Provenance
	warnings    []config.Warning
	history     *contract.SessionHistory
	ledger      *contract.ObservationLedger
	register    contract.CapabilityRegister
	lockInfo    daemon.LockInfo
	lockHeld    bool
	lockAlive   bool
	// refused is the D18 refusal when root is the home directory. The report then reads nothing
	// under root: its .qompack is the user-global layer, not a project store, so there is no
	// history, ledger, lock, spool or store of a project there to report on, and every section that
	// would describe one states the refusal instead.
	refused error
}

// collectDoctorReport assembles every section.
func collectDoctorReport(ctx context.Context, root string, env Env, clk core.Clock) doctorReport {
	s := &doctorState{ctx: ctx, root: root, env: env, clk: clk, register: contract.DefaultCapabilityRegister()}
	if root != "" {
		s.refused = refuseHomeRoot(env, root)
	}
	switch {
	case s.refused != nil:
		// The configuration rows still describe what a project below this home would inherit: the
		// user-global layer, with no project layer.
		cfg, prov, warns, err := loadUserGlobalConfig(env)
		s.cfg, s.prov, s.warnings = cfg, prov, warns
		if err != nil {
			s.cfg, s.prov = config.Defaults(), config.Provenance{}
		}
		s.history = &contract.SessionHistory{}
		s.ledger = &contract.ObservationLedger{}
	case root != "":
		s.l = paths.Of(root)
		s.established = projectEstablished(s.l)
		cfg, prov, warns, err := config.Load(config.Env{
			ProjectRoot: root, HomeDir: homeDir(env), Getenv: env.Getenv, Flags: env.Set,
		})
		s.cfg, s.prov, s.warnings = cfg, prov, warns
		if err != nil {
			s.cfg, s.prov = config.Defaults(), config.Provenance{}
			s.warnings = append(s.warnings, config.Warning{
				Message: "configuration could not be loaded at all: " + err.Error(),
			})
		}
		s.history = contract.LoadHistory(contract.HistoryPath(root))
		s.ledger = contract.LoadObservationLedger(contract.ObservationLedgerPath(root))
		s.lockInfo, s.lockHeld, s.lockAlive = fsckDaemonLiveness(root)
	default:
		s.cfg, s.prov = config.Defaults(), config.Provenance{}
		s.history = &contract.SessionHistory{}
		s.ledger = &contract.ObservationLedger{}
	}

	return doctorReport{
		Schema:  doctorSchema,
		Project: root,
		Sections: []doctorSectionDoc{
			{ID: "version", Title: "version and identity", Rows: s.versionRows()},
			{ID: "host", Title: "host", Rows: s.hostRows()},
			{ID: "scope", Title: "scope", Rows: s.scopeRows()},
			{ID: "capabilities", Title: "capabilities", Rows: s.capabilityRows()},
			{ID: "controls", Title: "controls", Rows: s.controlRows()},
			{ID: "recording", Title: "recording gaps", Rows: s.recordingRows()},
			{ID: "retrieval", Title: "retrieval gaps", Rows: s.retrievalRows()},
			{ID: "status", Title: "agreement with qompack status", Rows: s.statusRows()},
		},
		Exit: ExitOK,
	}
}

// ── 1. version and identity ────────────────────────────────────────────────────────────────────

// versionRows reports what this binary is: the linked-in version, the Go build info, the bundle it
// sits in when it sits in one, and what CLAUDE_PLUGIN_ROOT points at.
func (s *doctorState) versionRows() []doctorRow {
	rows := []doctorRow{{
		ID: "version.plugin", Status: doctorOK, Observed: core.Version,
		Detail: "internal/core.Version, which the release build stamps with -ldflags -X",
	}}

	if info, ok := debug.ReadBuildInfo(); ok {
		rows = append(rows, doctorRow{
			ID: "version.buildinfo", Status: doctorOK,
			Observed: doctorFirstNonEmpty(info.Main.Version, "(devel)"),
			Detail:   "main module " + info.Main.Path + "; " + doctorBuildSettings(info),
		})
	} else {
		rows = append(rows, doctorRow{
			ID: "version.buildinfo", Status: doctorUnknown, Observed: "no build info",
			Detail: "this binary carries no runtime/debug build information",
		})
	}

	rows = append(rows, s.bundleRow(), s.pluginRootRow())
	return rows
}

// doctorBuildSettings renders the build settings that matter for reproducing a binary.
func doctorBuildSettings(info *debug.BuildInfo) string {
	var parts []string
	for _, st := range info.Settings {
		switch st.Key {
		case "GOOS", "GOARCH", "vcs.revision", "vcs.modified", "-trimpath", "CGO_ENABLED":
			parts = append(parts, st.Key+"="+st.Value)
		}
	}
	if len(parts) == 0 {
		return "no recorded build settings"
	}
	return strings.Join(parts, " ")
}

// doctorBundle is the subset of BUNDLE.json doctor reports (packaging/README.md §2).
type doctorBundle struct {
	Version string `json:"version"`
	Target  struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
	} `json:"target"`
	Source struct {
		Commit string `json:"commit"`
		Dirty  bool   `json:"dirty"`
	} `json:"source"`
}

// bundleRow reports the bundle identity when this executable sits at <bundle>/bin/qompack[.exe]
// beside a <bundle>/BUNDLE.json, and "unknown" otherwise.
//
// It is derived from Env.Self rather than os.Executable() for the same reason every other use of
// that field is: Self is injected by the real entry point alone, so a test — and any other
// composition root — reports "unknown" rather than describing the go test binary's own directory.
func (s *doctorState) bundleRow() doctorRow {
	if s.env.Self == "" {
		return doctorRow{
			ID: "version.bundle", Status: doctorUnknown, Observed: "not running from a bundle",
			Detail: "this process was not told its own path, so no <bundle>/BUNDLE.json was looked for",
		}
	}
	dir := filepath.Dir(s.env.Self)
	if !strings.EqualFold(filepath.Base(dir), "bin") {
		return doctorRow{
			ID: "version.bundle", Status: doctorUnknown,
			Observed: "not running from a bundle",
			Detail:   s.env.Self + " does not sit at <bundle>/bin/qompack",
		}
	}
	p := filepath.Join(filepath.Dir(dir), "BUNDLE.json")
	raw, err := os.ReadFile(paths.Long(p)) //nolint:gosec // a path derived from this process's own location
	if err != nil {
		return doctorRow{
			ID: "version.bundle", Status: doctorUnknown, Observed: "no BUNDLE.json beside bin/",
			Detail: "looked at " + p,
		}
	}
	var b doctorBundle
	if jsonErr := json.Unmarshal(raw, &b); jsonErr != nil {
		return doctorRow{
			ID: "version.bundle", Status: doctorDegraded, Observed: "BUNDLE.json does not parse",
			Detail: jsonErr.Error(),
		}
	}
	status := doctorOK
	if b.Source.Dirty {
		status = doctorDegraded
	}
	return doctorRow{
		ID: "version.bundle", Status: status,
		Observed: fmt.Sprintf("%s %s/%s", b.Version, b.Target.OS, b.Target.Arch),
		Detail: fmt.Sprintf("source commit %s, dirty=%t (a bundle assembled from a dirty tree "+
			"cannot be reproduced from its commit)", doctorFirstNonEmpty(b.Source.Commit, "unknown"),
			b.Source.Dirty),
	}
}

// pluginRootRow reports CLAUDE_PLUGIN_ROOT's three outcomes: unset, set and resolving to a binary,
// or set with no binary under it. The third is the one that breaks every hook silently, because the
// host spawns ${CLAUDE_PLUGIN_ROOT}/bin/qompack (bin/qompack.exe on windows) directly — exec form,
// the exact path, no shell to resolve an extension (C1.11) — and a missing file is the host's
// error, not ours. The path checked here is that exact path for this platform.
func (s *doctorState) pluginRootRow() doctorRow {
	get := s.env.Getenv
	if get == nil {
		get = func(string) string { return "" }
	}
	v := get("CLAUDE_PLUGIN_ROOT")
	if v == "" {
		return doctorRow{
			ID: "version.pluginRoot", Status: doctorUnknown, Observed: "unset",
			Detail: "the host sets it when it invokes a hook; an unset value here says nothing " +
				"about an installed plugin",
		}
	}
	bin := filepath.Join(v, "bin", "qompack"+doctorExeSuffix())
	if fi, err := os.Stat(paths.Long(bin)); err == nil && fi.Mode().IsRegular() {
		// A binary with no execute bit fails every hook exactly as a missing one does. It is the
		// one symptom C7.5 leaves open — whether the host keeps bin/qompack's 0755 when it extracts
		// a release zip on linux/darwin — so it gets its own answer, with the workaround.
		if !pluginBinaryExecutable(runtime.GOOS, fi.Mode()) {
			return doctorRow{
				ID: "version.pluginRoot", Status: doctorDegraded, Observed: "set but the binary is not executable",
				Detail: "every hook the host runs is " + bin + ", which has no execute permission; " +
					"`chmod +x " + bin + "` restores it (docs/install.md §9)",
			}
		}
		return doctorRow{
			ID: "version.pluginRoot", Status: doctorOK, Observed: "set and resolves",
			Detail: bin,
		}
	}
	return doctorRow{
		ID: "version.pluginRoot", Status: doctorDegraded, Observed: "set but no binary under it",
		Detail: "every hook the host runs is " + bin + ", which is not there",
	}
}

// pluginBinaryExecutable reports whether the host could spawn a regular file of mode m on goos.
// linux/darwin need an execute bit; windows spawns a .exe by its extension and os.Stat reports no
// execute bit there at all, so every regular file passes.
func pluginBinaryExecutable(goos string, m fs.FileMode) bool {
	if goos == "windows" {
		return true
	}
	return m.Perm()&0o111 != 0
}

// doctorExeSuffix is the executable extension for this platform.
func doctorExeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// doctorFirstNonEmpty returns the first non-empty string.
func doctorFirstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// ── 2. host ────────────────────────────────────────────────────────────────────────────────────

// hostRows report the platform this process is on and, deliberately, refuse to claim anything about
// the Claude CLI. §7.5: "Repository JSON parsing alone does not validate the installed plugin", and
// nothing short of a real session against a real host validates one either.
func (s *doctorState) hostRows() []doctorRow {
	rows := []doctorRow{
		{
			ID: "host.platform", Status: doctorOK,
			Observed: runtime.GOOS + "/" + runtime.GOARCH,
			Detail:   "the GOOS/GOARCH this binary is running on",
		},
		{
			ID: "host.go", Status: doctorOK, Observed: runtime.Version(),
			Detail: "the Go runtime this binary was built with",
		},
		{
			ID: "host.claudeCLI", Status: doctorUnknown, Observed: "not probed",
			Detail: "doctor never shells out to the host; an installed-plugin claim needs a real " +
				"session, which is test/canary's job",
		},
	}

	target := s.ledger.Target
	if target.Zero() {
		rows = append(rows, doctorRow{
			ID: "host.target", Status: doctorUnknown, Observed: target.String(),
			Detail: "no observation ledger entry names a host this build gathered evidence against",
		})
		return rows
	}
	rows = append(rows, doctorRow{
		ID: "host.target", Status: doctorOK, Observed: target.String(),
		Detail: "the host the observation ledger's evidence was gathered against",
	})
	return rows
}

// ── 3. scope ───────────────────────────────────────────────────────────────────────────────────

// scopeRows report what project this is, whether it exists, who holds the daemon lock, and the
// contract memory the last sessions left behind.
func (s *doctorState) scopeRows() []doctorRow {
	if s.root == "" {
		return []doctorRow{{
			ID: "scope.root", Status: doctorUnknown, Observed: "no project root resolved",
			Detail: "neither QOMPACK_PROJECT_ROOT nor an enclosing .git marker named one",
		}}
	}

	if s.refused != nil {
		// A decision (D18), so disabled rather than degraded. No scope.established row: the
		// .qompack here is the user-global layer, and calling it an established project is exactly
		// the confusion the refusal exists to prevent.
		return []doctorRow{{
			ID: "scope.root", Status: doctorDisabled, Observed: s.root, Detail: s.refused.Error(),
		}, s.modeRow()}
	}

	rows := []doctorRow{{
		ID: "scope.root", Status: doctorOK, Observed: s.root,
		Detail: "resolved from --project, QOMPACK_PROJECT_ROOT or the nearest enclosing .git",
	}}

	if s.established {
		rows = append(rows, doctorRow{
			ID: "scope.established", Status: doctorOK, Observed: s.l.Dot,
			Detail: "this directory has been used with Qompack before",
		})
	} else {
		rows = append(rows, doctorRow{
			ID: "scope.established", Status: doctorUnknown, Observed: "no .qompack here",
			Detail: "nothing has been recorded in this directory, and doctor does not create one",
		})
	}

	rows = append(rows, s.daemonRow(), s.modeRow())
	rows = append(rows, s.contractRows()...)
	return rows
}

// daemonRow reports the lock's three states: nobody holds it, a holder whose listener answers, and
// a holder whose listener does not — which is the state the staleness protocol reclaims.
func (s *doctorState) daemonRow() doctorRow {
	if !s.lockHeld {
		return doctorRow{
			ID: "scope.daemon", Status: doctorOK, Observed: "no lock",
			Detail: "no daemon.lock, or one this build cannot parse; nothing is serving this project",
		}
	}
	if s.lockAlive {
		return doctorRow{
			ID: "scope.daemon", Status: doctorOK,
			Observed: fmt.Sprintf("held by pid %d, reachable", s.lockInfo.PID),
			Detail: fmt.Sprintf("daemon %s at %s since %d",
				s.lockInfo.Version, s.lockInfo.Addr, s.lockInfo.Started),
		}
	}
	return doctorRow{
		ID: "scope.daemon", Status: doctorDegraded,
		Observed: fmt.Sprintf("held by pid %d, not reachable", s.lockInfo.PID),
		Detail: "the lock file names a process whose listener did not answer; the staleness " +
			"protocol reclaims such a lock rather than blocking forever",
	}
}

// modeRow reports runtime.mode with the provenance layer that put it there.
func (s *doctorState) modeRow() doctorRow {
	src := s.prov["runtime.mode"]
	status := doctorOK
	if s.cfg.Runtime.Mode == "off" || s.cfg.Runtime.Mode == "passive" {
		status = doctorDisabled
	}
	return doctorRow{
		ID: "scope.mode", Status: status, Observed: s.cfg.Runtime.Mode,
		Detail: fmt.Sprintf("from the %s layer%s", src.Origin, doctorLocation(src)),
	}
}

// doctorLocation renders a provenance Source's location when it has one.
func doctorLocation(src config.Source) string {
	if src.Location == "" {
		return ""
	}
	return " (" + src.Location + ")"
}

// contractRows report the persisted SessionHistory: the mode the last session ended in, why it
// degraded, since when, and how many clean runs have followed.
func (s *doctorState) contractRows() []doctorRow {
	h := s.history
	status := doctorOK
	if h.Mode != "" && h.Mode != contract.ModeFull.String() {
		status = doctorDegraded
	}
	rows := []doctorRow{{
		ID: "scope.contractMode", Status: status, Observed: doctorFirstNonEmpty(h.Mode, doctorUnknown),
		Detail: fmt.Sprintf("%d session(s) recorded, %d clean run(s) since the last degradation",
			h.SessionCount, h.CleanRuns),
	}}
	if h.DegradedReason != "" {
		rows = append(rows, doctorRow{
			ID: "scope.degradedReason", Status: doctorDegraded, Observed: h.DegradedReason,
			Detail: fmt.Sprintf("degraded since %d", h.DegradedSince),
		})
	}
	return rows
}

// ── 4. capabilities ────────────────────────────────────────────────────────────────────────────

// capabilityRows join the evidence register to the observation ledger, one row per capability, in
// the ledger's normative order.
//
// Get's TWO-VALUE form is used deliberately: "this build has no opinion about c" and "c is off" are
// different answers and a bare zero value would destroy the distinction (§12.1). A capability with
// no observation says so; it never borrows another capability's evidence, and it never renders a
// coverage claim the ledger did not store.
func (s *doctorState) capabilityRows() []doctorRow {
	rows := make([]doctorRow, 0, len(contract.Capabilities()))
	for _, c := range contract.Capabilities() {
		rec, known := s.register.Get(c)
		if !known {
			rows = append(rows, doctorRow{
				ID: string(c), Status: doctorUnknown, Observed: "this build's register has no record",
				Detail: "unknown target; no observation",
			})
			continue
		}
		rows = append(rows, s.capabilityRow(c, rec))
	}
	return rows
}

// capabilityRow renders one capability.
func (s *doctorState) capabilityRow(c contract.Capability, rec contract.CapabilityRecord) doctorRow {
	status := doctorDisabled
	switch {
	case rec.Status == contract.StatusUnsupported, rec.Status == contract.StatusUnknown:
		status = doctorUnknown
	case rec.Enabled:
		status = doctorOK
	}

	detail := fmt.Sprintf("class %s; mechanism %s; evidence %s; target %s; fallback %s",
		rec.Class, rec.Mechanism, rec.Status, rec.Target.String(),
		doctorFirstNonEmpty(rec.Fallback, "none stated"))

	row := doctorRow{
		ID: string(c), Status: status,
		Observed: fmt.Sprintf("%s, enabled=%t", rec.Status, rec.Enabled),
	}
	if obsv, found := doctorNewestObservation(s.ledger, c); found {
		// The ledger is written at SessionStart, before the MCP handshake and the probe's delivery
		// can be seen; history.json records both when they happen, and the newest word is that (D50).
		obsv, fromHistory := contract.RefreshObservation(obsv, s.history, s.register, s.clk)
		row.Coverage = obsv.Coverage
		detail += fmt.Sprintf("; newest observation: outcome %s, scope %q, ts %d",
			obsv.Outcome, obsv.Scope, obsv.TS)
		if fromHistory {
			detail += " (read from state/history.json; the ledger's entry from the session's start was not_observed)"
		}
	} else {
		detail += "; no observation"
	}
	row.Detail = detail
	return row
}

// doctorNewestObservation returns the newest ledger entry for c. The ledger keeps arrival order,
// newest last, so the last match is the newest one.
func doctorNewestObservation(l *contract.ObservationLedger, c contract.Capability) (contract.Observation, bool) {
	if l == nil {
		return contract.Observation{}, false
	}
	for i := len(l.Observations) - 1; i >= 0; i-- {
		if l.Observations[i].Capability == c {
			return l.Observations[i], true
		}
	}
	return contract.Observation{}, false
}

// ── 5. controls ────────────────────────────────────────────────────────────────────────────────

// controlRows report every switch an operator can reach, its effective value, the layer that put it
// there, and — for a gated one — the gate that must pass before a true value would be honoured.
//
// A switch whose gate is still pending is labelled REFUSED-BY-VALIDATE rather than "disables
// something": Validate rejects a true value in this build, Load restores the default and warns, so
// setting it changes nothing at all. Calling that "disabled" would imply a knob that works.
func (s *doctorState) controlRows() []doctorRow {
	rows := []doctorRow{s.configViolationsRow()}

	for _, g := range config.MigrationGates() {
		rows = append(rows, s.switchRow(g.Key, &doctorGate{Owner: g.Owner, Gate: g.Gate, Passed: g.Passed}))
	}
	for _, key := range []string{
		"runtime.mode",
		"runtime.migration.settingsVersion",
		"runtime.migration.compaction.blockManualCompact",
		"runtime.phase7.settingsVersion",
		"runtime.telemetry.enabled",
		"runtime.redact.enabled",
		"runtime.daemon.enabled",
		"runtime.selection.submodularEnabled",
		"runtime.selection.loopWarningsEnabled",
	} {
		rows = append(rows, s.switchRow(key, nil))
	}

	lg := config.LegacyImportGate()
	rows = append(rows, doctorRow{
		ID: "store.migrate.legacyImportCutover", Status: doctorDisabled,
		Observed: fmt.Sprintf("build gate, passed=%t", lg.Passed),
		Detail: fmt.Sprintf("owner %s; gate %s; it has no config leaf on purpose, so no file can "+
			"turn it on", lg.Owner, lg.Gate),
	})
	rows = append(rows, doctorRow{
		ID: "admission", Status: doctorDisabled,
		Observed: "disabled: mirrors runtime.migration.replacement.newResult; host allowlist empty",
		Detail: "admission.Gate.Admits answers (false, disabled) while the mirror is off and " +
			"(false, unknown target) once it is on with no target evidence",
	})
	recording := doctorRow{
		ID: "runtime.recording", Status: s.recordingStatus(),
		Observed: s.cfg.Runtime.Mode,
		Detail: "recording has no switch of its own: runtime.mode passive or off IS the recording " +
			"control (config/runtime.go:104-116)",
	}
	if s.refused != nil {
		// Whatever the mode says, a refused root records nothing (D18).
		recording.Status = doctorDisabled
		recording.Detail = "nothing is recorded whatever runtime.mode says: " + s.refused.Error()
	}
	return append(rows, recording)
}

// recordingStatus maps runtime.mode onto whether this build records at all.
func (s *doctorState) recordingStatus() string {
	switch s.cfg.Runtime.Mode {
	case "off":
		return doctorDisabled
	case "passive":
		return doctorDegraded
	default:
		return doctorOK
	}
}

// switchRow renders one dotted configuration leaf.
func (s *doctorState) switchRow(key string, gate *doctorGate) doctorRow {
	value, known := s.cfg.Get(key)
	if !known {
		return doctorRow{
			ID: key, Status: doctorUnknown, Observed: "no such leaf in this build",
			Detail: "the control table names a key this build's schema does not carry",
		}
	}
	src := s.prov[key]
	row := doctorRow{
		ID: key, Status: doctorOK, Observed: fmt.Sprintf("%v", value),
		Detail: fmt.Sprintf("from the %s layer%s", src.Origin, doctorLocation(src)),
		Gate:   gate,
	}
	if gate != nil && !gate.Passed {
		row.Status = doctorDisabled
		row.Detail += fmt.Sprintf("; refused-by-validate while gate %q (owner %s) is pending",
			gate.Gate, gate.Owner)
	}
	if key == "runtime.telemetry.enabled" {
		row.Status = doctorDisabled
		row.Detail += "; hardwired off — a true value is a Validate violation, not a setting"
	}
	return row
}

// configViolationsRow reports every leaf Validate refused, from this run's own tolerant load and
// from the list a previous run persisted.
func (s *doctorState) configViolationsRow() doctorRow {
	live := config.ViolationsFromWarnings(s.warnings)
	// Only a project's own state/ holds a persisted list. With no root, or a refused one (D18), the
	// layout is empty and its State would be a path relative to the working directory.
	var persisted []config.Violation
	if s.l.State != "" {
		persisted = doctorPersistedViolations(s.l)
	}

	if len(live) == 0 && len(persisted) == 0 {
		return doctorRow{
			ID: "config.violations", Status: doctorOK, Observed: "none",
			Detail: "every configured leaf is within its domain",
		}
	}
	keys := make([]string, 0, len(live)+len(persisted))
	for _, v := range live {
		keys = append(keys, fmt.Sprintf("%s (got %v, want %v)", v.Key, v.Got, v.Want))
	}
	for _, v := range persisted {
		keys = append(keys, v.Key+" (from state/config-violations.json)")
	}
	return doctorRow{
		ID: "config.violations", Status: doctorDegraded,
		Observed: fmt.Sprintf("%d leaf/leaves fell back to the default", len(live)+len(persisted)),
		Detail:   strings.Join(keys, "; "),
	}
}

// doctorPersistedViolations reads state/config-violations.json, the §11.3 record a previous load
// wrote. doctor never writes it: see this file's header, rule 2.
func doctorPersistedViolations(l paths.Layout) []config.Violation {
	raw, err := paths.ReadFileShared(filepath.Join(l.State, configViolationsFile))
	if err != nil {
		return nil
	}
	var out []config.Violation
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}

// ── 6. recording gaps ──────────────────────────────────────────────────────────────────────────

// recordingRows report every way this project might be failing to record, INCLUDING the ones that
// live only in a day log or a live daemon counter today.
//
// The writability probe is the reason this section exists in the shape it does. A read-only
// .qompack degrades every writer quietly, and the only trace is a Warn in a file nothing points a
// user at (Task 2's F-2). One throwaway file under tmp/ answers it, and it is always removed.
func (s *doctorState) recordingRows() []doctorRow {
	if s.refused != nil {
		return []doctorRow{{
			ID: "store.writable", Status: doctorDisabled, Observed: "not probed: nothing is recorded here",
			Detail: s.refused.Error(),
		}}
	}
	var rows []doctorRow
	if s.root != "" {
		// Before any .qompack test: a configuration the hook path refuses is precisely what leaves a
		// project with a .qompack holding nothing but its config file, or none at all.
		rows = append(rows, s.captureConfigRow())
	}
	if s.root == "" || !s.established {
		return append(rows, doctorRow{
			ID: "store.writable", Status: doctorUnknown, Observed: "no .qompack to probe",
			Detail: "doctor does not create a project to find out whether it could write to one",
		})
	}

	rows = append(rows, s.writableRow())
	rows = append(rows, s.spoolRow(), s.drainRow(), s.negknowRow(), s.unpublishedCapturesRow())
	rows = append(rows, s.deliveryRolloverRow())
	rows = append(rows, s.assertionRows()...)
	return rows
}

// writableRow probes the project store for writability, which no other row can infer.
//
// It creates NOTHING (ruling R5-A). The obvious probe — write a file under tmp/ and delete it — was
// what the first version did, and it created `.qompack/tmp/` on a project that had none, which is
// the same class of change this whole section exists to detect. What it does instead is open a file
// that is ALREADY THERE for writing and close it without writing a byte: on Windows and on POSIX
// that is exactly the call a read-only store, a read-only directory or a denied ACL refuses.
//
// When there is no such file — a bare `.qompack/` with nothing in it — the honest answer is that the
// question was not asked, not a guess in either direction.
func (s *doctorState) writableRow() doctorRow {
	for _, p := range []string{
		filepath.Join(s.l.Dot, ".gitignore"),
		filepath.Join(s.l.State, storeStateFileName),
		filepath.Join(s.l.Index, "roots.jsonl"),
	} {
		if fi, err := os.Stat(paths.Long(p)); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		f, err := paths.OpenFile(p, os.O_WRONLY, 0)
		if err != nil {
			return doctorRow{
				ID: "store.writable", Status: doctorDegraded, Observed: "not writable",
				Detail: "every writer degrades quietly on a read-only .qompack, and this is the " +
					"only row that says so: opening " + p + " for writing failed: " + err.Error(),
			}
		}
		_ = f.Close()
		return doctorRow{
			ID: "store.writable", Status: doctorOK, Observed: "writable",
			Detail: "an existing file (" + p + ") was opened for writing and closed untouched; " +
				"no file was created to find out",
		}
	}
	return doctorRow{
		ID: "store.writable", Status: doctorUnknown, Observed: "not probed",
		Detail: ".qompack holds no file this probe may open for writing, and doctor does not " +
			"create one to find out",
	}
}

// captureConfigRow reports whether the HOOK path can load this project's configuration, which the
// tolerant load behind every other configuration row cannot say (V6 close-out item C1.8: SP-18 found
// a project recording nothing while every configuration row read clean). It is read-only:
// config.LoadForCapture persists nothing.
func (s *doctorState) captureConfigRow() doctorRow {
	// `--project .` is doctor's own spelling; a hook resolves an absolute root (paths.Resolve), and
	// the loader refuses a relative one, so ask it the question the hooks would ask.
	root := s.root
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	_, _, violations, warnings, err := config.LoadForCapture(config.Env{
		ProjectRoot: root, HomeDir: homeDir(s.env), Getenv: s.env.Getenv, Flags: s.env.Set,
	})
	switch {
	case err != nil:
		return doctorRow{
			ID: "config.capture", Status: doctorDegraded, Observed: "refused: every hook admits nothing",
			Detail: err.Error() + "; hooks still exit 0 with empty output, and nothing is recorded " +
				"until the configuration is repaired",
		}
	case len(violations)+len(warnings) > 0:
		return doctorRow{
			ID: "config.capture", Status: doctorDegraded,
			Observed: captureConfigDegradedSummary(violations, warnings),
			Detail:   captureConfigKeys(violations, warnings),
		}
	default:
		return doctorRow{
			ID: "config.capture", Status: doctorOK, Observed: "applied as written",
			Detail: "the hook path's own loader (config.LoadForCapture) accepts every layer unchanged",
		}
	}
}

// storeStateFileName is state/store.json's basename, one of the existing files writableRow may
// open. internal/store owns the name and does not export it.
const storeStateFileName = "store.json"

// spoolRow reports the unreplayed spool: bytes the client wrote that no drain has consumed. They
// are not lost — the next drain replays them — which is why this is a gap and not a defect.
//
// An ABSENT spool directory and an UNREADABLE one are two answers, not one (ruling R5-D). Collapsing
// both into "no spool directory" reported "nothing has been spooled here" for a spool nobody could
// read, which is the one case where the count matters most. The read goes through fsckReadDir so a
// path replaced by a regular file is a named error rather than Windows' fs.ErrNotExist.
func (s *doctorState) spoolRow() doctorRow {
	if _, statErr := os.Lstat(paths.Long(s.l.Spool)); errors.Is(statErr, fs.ErrNotExist) {
		return doctorRow{
			ID: "spool.pending", Status: doctorUnknown, Observed: "no spool directory",
			Detail: "nothing has been spooled in this project",
		}
	}
	entries, err := fsckReadDir(s.l.Spool)
	if err != nil {
		return doctorRow{
			ID: "spool.pending", Status: doctorUnknown, Observed: "unreadable",
			Detail: "the spool directory could not be read, so the number of unreplayed files is " +
				"unknown rather than zero: " + s.l.Spool + ": " + err.Error(),
		}
	}
	files, wal, bytes := 0, 0, int64(0)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, statErr := e.Info()
		if statErr != nil {
			continue
		}
		files++
		if strings.HasPrefix(e.Name(), doctorWALSpoolPrefix) {
			wal++
		}
		bytes += info.Size()
	}
	status := doctorOK
	detail := "spool files the daemon has not drained; an observation, not a latency or health verdict"
	if files > 0 {
		status = doctorDegraded
		if ipc.ReadState(s.root, s.cfg).Hot == ipc.HotSpool {
			if s.lockAlive {
				// Spool submode with a daemon serving: the files are the designed path, replayed by
				// the daemon, and nothing is lost (D53(c)). Informational, and still shown.
				status = doctorOK
				detail = "the hot path is in spool submode: " + obs.SpoolSubmodeWhat + ". It lasts until " +
					obs.SpoolSubmodeUntil + ". To tune it: " + obs.SpoolSubmodeTune
			} else {
				detail = "state.bin still says spool submode but no daemon is serving, so hooks spool without " +
					"starting one and nothing replays these files until a new session starts in this project"
			}
		} else if s.lockAlive {
			// Sync submode with a daemon serving keeps its verdict: the daemon replays these files
			// within seconds, so files that stay here are the one sign the replay is not keeping up.
			// What the user reads names the ordinary cause of each kind first (D53(c)), and does not
			// call the daemon's own WAL segments client spools (D55, wave 16b).
			detail = syncSpoolDetail(files-wal, wal)
		}
	}
	return doctorRow{
		ID: "spool.pending", Status: status,
		Observed: fmt.Sprintf("%d file(s), %d byte(s)", files, bytes),
		Detail:   detail,
	}
}

// doctorWALSpoolPrefix names the daemon's own WAL segments in the spool directory (wal-<session>.ndjson,
// internal/ipc SpoolFiles); every other file there is a hook client spool (client-<pid>.ndjson).
const doctorWALSpoolPrefix = "wal-"

// syncSpoolDetail is spool.pending's detail in sync submode with a daemon serving, for client hook
// client spool files and wal daemon WAL segments. The two kinds have different causes and are replayed by
// different parts of the daemon, so each is counted and explained on its own.
func syncSpoolDetail(client, wal int) string {
	var parts []string
	if client > 0 {
		parts = append(parts, fmt.Sprintf("%d hook client spool(s) the running daemon has not replayed yet: on a "+
			"slow disk a hook that waits out its ACK deadline hands its capture to its client spool, and the "+
			"daemon's client-spool watcher replays it within seconds", client))
	}
	if wal > 0 {
		parts = append(parts, fmt.Sprintf("%d daemon WAL segment(s) the running daemon has not finished "+
			"publishing: captures it accepted and logged before publishing them, which its worker pool and "+
			"drains replay", wal))
	}
	return strings.Join(parts, "; ") + "; either way nothing is lost: run doctor again, and if the files stay " +
		"the replay is not keeping up. To tune it: " + obs.SpoolSubmodeTune
}

// drainRow reports whether the drain's own progress document can be read at all. An unreadable one
// wedges recording until someone looks, which is exactly what this row is for.
func (s *doctorState) drainRow() doctorRow {
	raw, err := paths.ReadFileShared(filepath.Join(s.l.State, "drain.json"))
	if err != nil {
		return doctorRow{
			ID: "drain.progress", Status: doctorUnknown, Observed: "no drain.json",
			Detail: "no drain pass has recorded progress in this project yet",
		}
	}
	var state map[string]json.RawMessage
	if jsonErr := json.Unmarshal(raw, &state); jsonErr != nil {
		return doctorRow{
			ID: "drain.progress", Status: doctorDegraded,
			Observed: string(daemon.DrainGapProgressUnreadable),
			Detail:   "state/drain.json does not parse: " + jsonErr.Error(),
		}
	}
	return doctorRow{
		ID: "drain.progress", Status: doctorOK,
		Observed: fmt.Sprintf("%d spool file(s) tracked", len(state)),
		Detail:   "state/drain.json parses; `qompack fsck` compares it against the spool itself",
	}
}

// negknowRow reports blind mode and the filter's load, read from the files rather than by opening
// the ledger: negknow.Open lays out the project and takes an append handle, and a report may not
// write to what it reports on.
func (s *doctorState) negknowRow() doctorRow {
	p := filepath.Join(s.l.Records, "eliminations.jsonl")
	if _, err := os.Stat(paths.Long(p)); err != nil {
		return doctorRow{
			ID: "negknow.ledger", Status: doctorUnknown, Observed: "no elimination log",
			Detail: "nothing has been eliminated in this project",
		}
	}
	if _, err := paths.ReadFileShared(p); err != nil {
		return doctorRow{
			ID: "negknow.ledger", Status: doctorDegraded, Observed: "blind",
			Detail: "records/eliminations.jsonl exists and cannot be read, so every already_tried " +
				"answer degrades to unavailable: " + err.Error(),
		}
	}
	return doctorRow{
		ID: "negknow.ledger", Status: doctorOK, Observed: "readable",
		Detail: "`qompack fsck` classifies the tried.bloom cache over it",
	}
}

// unpublishedCapturesRow counts the capture sidecars that are a real gap: a delivery whose outcome
// is ok and whose bytes are durable with no reference joined (store.CaptureRequiresReference).
//
// It asks the one definition fsck's publication row and the daemon's startup accounting both use,
// store.PublicationAuditor, through the same read-only open. It used to run fsck's captures walk on
// a scan that had loaded no index, so every published sidecar's root looked unresolvable and counted
// as a gap: the Phase 4 live lane read "5 gap(s) across 7 sidecar(s)" on a store fsck certified
// (install D5). Whether a published sidecar's root still resolves is fsck's captures row's question,
// which needs the whole index; this row reports the unpublished ones only, as its name says.
func (s *doctorState) unpublishedCapturesRow() doctorRow {
	const id = "captures.unpublished"
	detail := "a sidecar is a gap only for a delivery whose outcome is ok and whose bytes are durable " +
		"with no reference joined; sidecars are evidence and are never swept. The count is the " +
		"publication audit's, which `qompack fsck`'s publication row reports too"
	opened, err := store.OpenReadOnly(s.root, config.Defaults(), store.Deps{Log: logging.Nop()})
	if err != nil {
		return doctorRow{
			ID: id, Status: doctorUnknown, Observed: "publication evidence could not be opened read-only",
			Detail: detail + ": " + err.Error(),
		}
	}
	defer func() { _ = opened.Close() }()
	auditor, ok := opened.(store.PublicationAuditor)
	if !ok {
		return doctorRow{ID: id, Status: doctorUnknown, Observed: "publication audit is unavailable", Detail: detail}
	}
	audit, err := auditor.AuditPublication(s.ctx, store.DefaultPublicationScanCap())
	observed := fmt.Sprintf("%d gap(s) across %d sidecar(s)", audit.UnpublishedCaptures, audit.CapturesScanned)
	switch {
	case audit.UnpublishedCaptures > 0:
		return doctorRow{ID: id, Status: doctorDegraded, Observed: observed, Detail: detail}
	case err != nil || (audit.Incomplete && !audit.IncompleteOnlyForNewerSchemas()):
		return doctorRow{
			ID: id, Status: doctorUnknown, Observed: observed,
			Detail: detail + "; the audit is incomplete, so zero observed gaps cannot certify completeness",
		}
	case audit.Incomplete:
		return doctorRow{
			ID: id, Status: doctorOK, Observed: observed,
			Detail: fmt.Sprintf("%s; %d sidecar(s) written by a newer build are not classified by this one, "+
				"a support gap rather than damage", detail, audit.NewerSchemaCaptures),
		}
	}
	return doctorRow{ID: id, Status: doctorOK, Observed: observed, Detail: detail}
}

// deliveryRolloverRow reports segmented rollover (owner decision D6, 2026-09-23). Two things about it
// matter to an operator. Whether the store has rotated: after its first rotation a build that predates
// segments refuses the journal, so a backup taken before that rotation is the only way back to one
// (docs/backup.md). And what the last daemon counted: its rotations and the time every lease and
// acknowledgement waited for them, rotations that failed (the journal then refuses every delivery until
// a restart), and store GC passes halted because the carried leases passed their harvest bound (nothing
// is collected while they are). A failure or a halt makes the row degraded; the pause alone does not,
// because D6 accepted it.
//
// The active segment is the authority head's own claim, read the way fsck's delivery row reads it (the
// authority's full validation is `qompack fsck --seal-check`'s). The counters are the last daemon
// run's, as it persisted them to metrics/latency.json, so a restart resets them; LOUD.log keeps a line
// for every rotation, failure and first halt.
func (s *doctorState) deliveryRolloverRow() doctorRow {
	const id = "delivery.rollover"
	active, headErr := readDeliveryActiveSegment(s.l)
	var observed []string
	status := doctorOK
	switch {
	case errors.Is(headErr, fs.ErrNotExist):
		status = doctorUnknown
		observed = append(observed, "no segment authority")
	case headErr != nil:
		status = doctorUnknown
		observed = append(observed, "segment authority head unreadable")
	case active == 0:
		observed = append(observed, "never rotated")
	default:
		observed = append(observed, fmt.Sprintf("rotated %d time(s), segment %d active", active, active))
	}

	detail := "a store that has never rotated rotates by itself at 65,536 deliveries; after that a build " +
		"older than segmented rollover refuses the journal, and a backup taken before the first rotation " +
		"is the only way back to one (docs/backup.md); `qompack fsck --seal-check` validates the authority"
	if headErr != nil && !errors.Is(headErr, fs.ErrNotExist) {
		detail = "state/delivery-journal.json could not be read (" + headErr.Error() + "); " + detail
	}
	snap, err := readPersistedMetrics(s.l)
	if err != nil {
		observed = append(observed, "no persisted daemon counters")
		return doctorRow{
			ID: id, Status: status, Observed: strings.Join(observed, "; "),
			Detail: detail + "; no daemon has persisted metrics/latency.json here, so the last run's " +
				"rotations, pauses, failures and GC halts are unknown rather than zero (LOUD.log has them)",
		}
	}
	c := snap.Counters
	failed := c[daemon.CounterDeliveryRotationFailures]
	halted := c[store.CounterGCDeliveryCarryOverBound]
	observed = append(observed, fmt.Sprintf("last daemon: %d rotation(s), %d ms paused, %d failed, "+
		"%d GC pass(es) halted on the carry bound", c[daemon.CounterDeliveryRotations],
		c[daemon.CounterDeliveryRotationPauseMS], failed, halted))
	if failed > 0 || halted > 0 {
		status = doctorDegraded
	}
	if c[daemon.CounterDeliveryRotationCarryOverBound] > 0 {
		detail = "a rotation was refused because the archived leases still waiting for an acknowledgement " +
			"passed the carried-lease file's 64 MiB bound: the journal refuses every delivery " +
			"(docs/troubleshooting.md); " + detail
	}
	if c[daemon.CounterDeliveryFirstRotationBackupAdvised] > 0 && active == 0 && headErr == nil {
		detail = "the last daemon warned that this store's first rotation is near; " + detail
	}
	return doctorRow{
		ID: id, Status: status, Observed: strings.Join(observed, "; "),
		Detail: detail + "; counters are the last daemon run's, from metrics/latency.json " +
			"(" + doctorMetricsAge(snap.TS, s.clk) + "), and LOUD.log keeps every occurrence",
	}
}

// readDeliveryActiveSegment reads the active segment the delivery segment authority's head names
// (state/delivery-journal.json). An absent head is fs.ErrNotExist; a head that does not read or names
// no segment is another error. It is a classification aid for the read-only rows (fsck's delivery row,
// doctor's delivery.rollover); the authority's full validation is --seal-check's.
func readDeliveryActiveSegment(l paths.Layout) (uint64, error) {
	raw, err := paths.ReadFileShared(filepath.Join(l.State, "delivery-journal.json"))
	if err != nil {
		return 0, err
	}
	var head struct {
		Active *uint64 `json:"active"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return 0, err
	}
	if head.Active == nil {
		return 0, errors.New("the head names no active segment")
	}
	return *head.Active, nil
}

// doctorMetricsAge says how old a persisted metrics snapshot is.
func doctorMetricsAge(ts core.UnixMilli, clk core.Clock) string {
	age := clk.Now().Sub(ts.Time())
	if ts == 0 || age < 0 {
		return "age unknown"
	}
	return "persisted " + age.Round(time.Second).String() + " ago"
}

// assertionRows report the contract assertions the last session failed. history.Last is the
// persisted record of that run, and it is the only place a degradation from a previous session
// survives.
func (s *doctorState) assertionRows() []doctorRow {
	var rows []doctorRow
	for _, r := range s.history.Last {
		if r.OK {
			continue
		}
		rows = append(rows, doctorRow{
			ID: "assertion." + string(r.ID), Status: doctorDegraded, Observed: r.Observed,
			Detail: fmt.Sprintf("expected %s (severity %s)", r.Expected, severityLabel(r.Severity)),
		})
	}
	if len(rows) == 0 {
		rows = append(rows, doctorRow{
			ID: "assertions.last", Status: doctorOK, Observed: "no failed assertion recorded",
			Detail: fmt.Sprintf("%d assertion result(s) persisted from the last session",
				len(s.history.Last)),
		})
	}
	return rows
}

// ── 7. retrieval gaps ──────────────────────────────────────────────────────────────────────────

// retrievalRows report what can and cannot be retrieved. Every number here is an OBSERVATION and is
// labelled one: a dedup ratio is not a health verdict and a latency figure is not a promise, which
// is why neither appears.
func (s *doctorState) retrievalRows() []doctorRow {
	if s.refused != nil {
		return []doctorRow{{
			ID: "store.open", Status: doctorDisabled, Observed: "not opened: nothing is recorded here",
			Detail: s.refused.Error() + "; every MCP tool answers with that refusal",
		}}
	}
	if s.root == "" || !s.established {
		return []doctorRow{{
			ID: "store.open", Status: doctorUnknown, Observed: "no .qompack to open",
			Detail: "doctor does not create a project to find out whether it could open one",
		}}
	}

	rows := []doctorRow{s.storeOpenRow(), s.checkpointRow(), s.mcpToolsRow()}
	rows = append(rows, doctorRow{
		ID: "integrity", Status: doctorUnknown, Observed: "not checked here",
		Detail: "run `qompack fsck` for identities, references, manifests, delta bases and roots",
	})
	return rows
}

// storeOpenRow reports whether the store opens and what it holds, as counts and never as health.
func (s *doctorState) storeOpenRow() doctorRow {
	counts, err := doctorStoreCounts(s.ctx, s.root)
	if err != nil {
		return doctorRow{
			ID: "store.open", Status: doctorDegraded, Observed: "will not open",
			Detail: err.Error(),
		}
	}
	return doctorRow{
		ID: "store.open", Status: doctorOK, Observed: counts,
		Detail: "counts are observations of what is stored, not a verdict about efficiency or health",
	}
}

// doctorStoreCounts opens the store, reads Stats and closes it again.
func doctorStoreCounts(ctx context.Context, root string) (string, error) {
	opened, err := store.OpenReadOnly(root, config.Defaults(), store.Deps{Log: logging.Nop()})
	if err != nil {
		return "", err
	}
	defer func() { _ = opened.Close() }()

	st, statsErr := opened.Stats(ctx)
	if statsErr != nil {
		return "", statsErr
	}
	return fmt.Sprintf("%d object(s), %d tool use(s), %d file(s), %d segment(s)",
		st.Objects, st.ToolUses, st.Files, st.Segments), nil
}

// checkpointRow reports whether the newest checkpoint of the last recorded session resolves.
func (s *doctorState) checkpointRow() doctorRow {
	entries, err := paths.ReadManifest(s.l)
	switch {
	case err != nil:
		return doctorRow{
			ID: "checkpoint.latest", Status: doctorDegraded, Observed: "manifest unreadable",
			Detail: err.Error(),
		}
	case len(entries) == 0:
		return doctorRow{
			ID: "checkpoint.latest", Status: doctorUnknown, Observed: "none recorded",
			Detail: "no checkpoint has been finalized in this project",
		}
	}
	newest := entries[0].Seq
	for _, e := range entries {
		if e.Seq > newest {
			newest = e.Seq
		}
	}
	reader, err := checkpoint.OpenReader(s.root, logging.Nop(), nil)
	if err != nil {
		return doctorRow{
			ID: "checkpoint.latest", Status: doctorDegraded, Observed: "reader refused",
			Detail: err.Error(),
		}
	}
	if _, _, getErr := reader.Get(s.ctx, newest); getErr != nil {
		return doctorRow{
			ID: "checkpoint.latest", Status: doctorDegraded,
			Observed: fmt.Sprintf("%04d does not resolve", int(newest)),
			Detail:   getErr.Error(),
		}
	}
	return doctorRow{
		ID: "checkpoint.latest", Status: doctorOK,
		Observed: fmt.Sprintf("%04d of %d resolves", int(newest), len(entries)),
		Detail:   "the artifact re-hashes to the digest checkpoints/MANIFEST.jsonl recorded",
	}
}

// mcpToolsRow reports the retrieval surface this build declares. It does not call a tool: a
// declaration is what the host reads, and whether a call would succeed is a session's question.
func (s *doctorState) mcpToolsRow() doctorRow {
	names := mcp.ToolNames()
	status := doctorOK
	detail := "declared by internal/mcp and byte-compared against plugin/ by devtool plugin-validate"
	if !s.cfg.Runtime.Daemon.Enabled {
		status = doctorDegraded
		detail += "; runtime.daemon.enabled is off, so every one of them reports unavailable"
	}
	return doctorRow{
		ID: "mcp.tools", Status: status,
		Observed: fmt.Sprintf("%d tool(s): %s", len(names), strings.Join(names, ", ")),
		Detail:   detail,
	}
}

// ── 8. agreement with qompack status ───────────────────────────────────────────────────────────

// statusRows embed commands.CollectStatus over the SAME StatusSources the `status` command wires,
// so doctor and status cannot drift: they are two renderings of one collector rather than two
// collectors that happen to agree today. TestDoctor_AgreesWithStatusOnModeAndProvenance asserts it
// for a fixture with and without a daemon.
func (s *doctorState) statusRows() []doctorRow {
	if s.root == "" {
		return []doctorRow{{
			ID: "status.primary", Status: doctorUnknown, Observed: string(commands.SourceNone),
			Detail: "no project root, so there is nothing to collect status for",
		}}
	}

	var rep commands.StatusReport
	if s.refused != nil {
		// The same sources `qompack status` binds for a refused root (buildCommandDeps), so the two
		// still agree, and no client is built for the home directory.
		rep = commands.CollectStatus(s.ctx, commands.StatusSources{Refused: s.refused}, s.clk.Now())
	} else {
		client := newCommandClient(s.root, s.cfg, doctorNoSpawnEnv(s.env), logging.Nop(), obs.New(s.clk), s.clk)
		defer func() { _ = client.Close() }()
		rep = commands.CollectStatus(s.ctx, commandStatusSources(s.ctx, s.root, client), s.clk.Now())
	}

	mode := doctorUnknown
	if rep.Snapshot != nil && rep.Snapshot.Mode != "" {
		mode = rep.Snapshot.Mode
	}
	primaryStatus := doctorOK
	if rep.Primary.Status != commands.AvailabilityOK {
		primaryStatus = doctorUnknown
	}
	return []doctorRow{
		{
			ID: "status.primary", Status: primaryStatus, Observed: string(rep.Primary.Source),
			Detail: fmt.Sprintf("availability %s%s; doctor never SPAWNS a daemon (it strips Self "+
				"before building the client), so it agrees with `qompack status` for the disk and "+
				"probe sources and for a daemon that is already running — a `status` run that "+
				"lazily spawns one can report source=daemon where this row reports disk",
				rep.Primary.Status, doctorReason(rep.Primary.Reason)),
		},
		{
			ID: "status.mode", Status: doctorStatusForMode(mode), Observed: mode,
			Detail: "the same snapshot `qompack status` renders, collected through the same sources",
		},
		{
			ID: "status.schema", Status: doctorOK, Observed: fmt.Sprintf("%d", rep.Schema),
			Detail: "commands.StatusSchema, so a reader of both documents compares one number",
		},
		doctorHotPathRow(rep),
	}
}

// doctorHotPathRow reports the hot path's submode from the same snapshot `qompack status` renders.
// Spool submode is informational, never degraded: on a slow disk it is the designed behaviour of a
// long session and loses nothing (D53(c)), so the row says what happened, what ends it and what
// tunes it.
func doctorHotPathRow(rep commands.StatusReport) doctorRow {
	hot := ""
	if rep.Snapshot != nil {
		hot = rep.Snapshot.Hot
	}
	switch hot {
	case "sync":
		return doctorRow{
			ID: "status.hotPath", Status: doctorOK, Observed: hot,
			Detail: "hooks hand their captures to the daemon and wait for its acknowledgement",
		}
	case "spool":
		return doctorRow{
			ID: "status.hotPath", Status: doctorOK, Observed: hot,
			Detail: obs.SpoolSubmodeWhat + ". It lasts until " + obs.SpoolSubmodeUntil + ". To tune it: " +
				obs.SpoolSubmodeTune,
		}
	default:
		return doctorRow{
			ID: "status.hotPath", Status: doctorUnknown, Observed: doctorFirstNonEmpty(hot, doctorUnknown),
			Detail: "no running daemon reported its hot-path submode",
		}
	}
}

// doctorNoSpawnEnv strips Self so the status client can never spawn a daemon: doctor observes, it
// does not start anything.
func doctorNoSpawnEnv(env Env) Env {
	env.Self = ""
	return env
}

// doctorReason renders a provenance reason when there is one.
func doctorReason(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

// doctorStatusForMode maps a contract mode string onto a row status.
func doctorStatusForMode(mode string) string {
	switch mode {
	case contract.ModeFull.String():
		return doctorOK
	case doctorUnknown, "":
		return doctorUnknown
	default:
		return doctorDegraded
	}
}

// writeDoctorTable renders the report the way writeSelfTestTable renders self-test's.
func writeDoctorTable(out io.Writer, r doctorReport) {
	fmt.Fprintf(out, "project: %s\n", doctorFirstNonEmpty(r.Project, "(none resolved)"))
	for _, sec := range r.Sections {
		fmt.Fprintf(out, "\n%s\n", strings.ToUpper(sec.Title))
		fmt.Fprintf(out, "%-42s %-9s %s\n", "ROW", "STATUS", "OBSERVED")
		for _, row := range sec.Rows {
			fmt.Fprintf(out, "%-42s %-9s %s\n", row.ID, row.Status, row.Observed)
			if row.Gate != nil {
				fmt.Fprintf(out, "%-42s %-9s gate %q (owner %s) passed=%t\n",
					"", "", row.Gate.Gate, row.Gate.Owner, row.Gate.Passed)
			}
			if row.Detail != "" {
				fmt.Fprintf(out, "%-42s %-9s %s\n", "", "", row.Detail)
			}
		}
	}
	fmt.Fprintf(out, "\nexit: %d\n", r.Exit)
}
