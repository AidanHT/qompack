package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// doctorJSON runs `qompack doctor --json --project <root>` and decodes the report generically —
// the same reasoning as fsckJSON: the document is the contract.
func doctorJSON(t *testing.T, root string) (int, map[string]any, string) {
	t.Helper()

	code, out, errw := fsckDispatch(t, "doctor", "--project", root, "--json")
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &doc), "stdout=%s stderr=%s", out, errw)
	return code, doc, errw
}

// doctorSection returns the section rows under id, failing when the section is absent.
func doctorSection(t *testing.T, doc map[string]any, id string) []map[string]any {
	t.Helper()

	sections, _ := doc["sections"].([]any)
	for _, s := range sections {
		sec, _ := s.(map[string]any)
		if sec["id"] != id {
			continue
		}
		raw, _ := sec["rows"].([]any)
		out := make([]map[string]any, 0, len(raw))
		for _, r := range raw {
			row, _ := r.(map[string]any)
			out = append(out, row)
		}
		return out
	}
	t.Fatalf("no doctor section %q in %v", id, doc)
	return nil
}

// doctorFindRow returns the row with id inside section.
func doctorFindRow(t *testing.T, doc map[string]any, section, id string) map[string]any {
	t.Helper()

	for _, row := range doctorSection(t, doc, section) {
		if row["id"] == id {
			return row
		}
	}
	t.Fatalf("no row %q in doctor section %q", id, section)
	return nil
}

// doctorText flattens the whole report for the assertions that are about what it must NEVER say.
func doctorText(t *testing.T, doc map[string]any) string {
	t.Helper()

	b, err := json.Marshal(doc)
	require.NoError(t, err)
	return string(b)
}

// TestDoctor_AlwaysExitsZeroAndNeverCreatesAProject pins doctor's report semantics: like status, it
// answers rather than fails, and like every other read-only command it may not bring a project into
// existence (qompack_commands.go's READ-ONLY DISCIPLINE block).
func TestDoctor_AlwaysExitsZeroAndNeverCreatesAProject(t *testing.T) {
	t.Parallel()

	t.Run("an empty directory", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		code, doc, errw := doctorJSON(t, dir)
		require.Equal(t, ExitOK, code, "stderr=%s", errw)

		row := doctorFindRow(t, doc, "scope", "scope.established")
		require.Equal(t, "unknown", row["status"], "row=%v", row)
		observed, ok := row["observed"].(string)
		require.True(t, ok, "observed must be a string, row=%v", row)
		require.Contains(t, strings.ToLower(observed), "no .qompack")

		_, err := os.Stat(filepath.Join(dir, ".qompack"))
		require.True(t, os.IsNotExist(err), "doctor must not create .qompack: %v", err)
	})

	t.Run("a seeded project", func(t *testing.T) {
		t.Parallel()

		p := seedFsckProject(t)
		code, doc, errw := doctorJSON(t, p.Root)
		require.Equal(t, ExitOK, code, "stderr=%s", errw)
		require.EqualValues(t, 1, doc["schema"])
	})

	t.Run("a project whose store is damaged", func(t *testing.T) {
		t.Parallel()

		p := seedFsckProject(t)
		flipOneBit(t, objectFile(t, p.Layot, p.Chunk))
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(p.Layot.State, "drain.json")),
			[]byte("not json at all"), 0o600))

		code, _, errw := doctorJSON(t, p.Root)
		require.Equal(t, ExitOK, code, "a report never fails over what it reports; stderr=%s", errw)
	})

	t.Run("an unknown flag is the one usage error", func(t *testing.T) {
		t.Parallel()

		code, _, _ := fsckDispatch(t, "doctor", "--wat")
		require.Equal(t, ExitUsage, code)
	})
}

// TestDoctor_ReportsCapabilityEvidenceWithoutInventingIt is V6-VERIFY row 3.7's half that lives
// here: every capability row carries the register's own class, mechanism, evidence status and
// disposition, an UNKNOWN target because this build has gathered none, and "no observation" where
// the ledger is empty. Coverage is rendered as stored and is never "complete" (§12.1).
func TestDoctor_ReportsCapabilityEvidenceWithoutInventingIt(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	_, doc, errw := doctorJSON(t, p.Root)

	rows := doctorSection(t, doc, "capabilities")
	require.Len(t, rows, len(contract.Capabilities()), "stderr=%s", errw)

	seen := map[string]bool{}
	for _, row := range rows {
		id, _ := row["id"].(string)
		seen[id] = true
		observed, _ := row["observed"].(string)
		detail, _ := row["detail"].(string)
		require.Contains(t, detail, "unknown target",
			"capability %q must not claim a target this build never gathered: %v", id, row)
		require.Contains(t, detail, "no observation",
			"capability %q must say the ledger is empty rather than implying evidence: %v", id, row)
		require.NotEmpty(t, observed)
		require.NotEqual(t, "complete", row["coverage"],
			"coverage is never rendered complete (contract/observation.go coverageOf): %v", row)
	}
	for _, c := range contract.Capabilities() {
		require.True(t, seen[string(c)], "capability %s has no row", c)
	}

	// The two verdicts plan §3 forbids doctor from reaching for.
	text := doctorText(t, doc)
	require.NotContains(t, text, "dedup_ratio")
	require.NotContains(t, text, "DedupRatio")
	require.NotContains(t, strings.ToLower(text), "4:1")
}

// TestDoctor_EveryMigrationGateIsPendingInThisBuild mirrors config's own
// TestMigrationGates_AllPendingInThisBuild at the operator surface: a gated switch is reported with
// its owner and its gate, and none of them has passed, so "unsupported controls remain disabled" is
// something a user can read rather than something only a test knows.
func TestDoctor_EveryMigrationGateIsPendingInThisBuild(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	_, doc, _ := doctorJSON(t, p.Root)

	rows := doctorSection(t, doc, "controls")
	gated := 0
	for _, row := range rows {
		gate, ok := row["gate"].(map[string]any)
		if !ok {
			continue
		}
		gated++
		require.Equal(t, false, gate["passed"], "gate row %v claims a gate this build has not passed", row)
		require.NotEmpty(t, gate["owner"], "row=%v", row)
		require.NotEmpty(t, gate["gate"], "row=%v", row)
	}
	require.Equal(t, len(config.MigrationGates()), gated,
		"every gated leaf gets a row; got %d", gated)
}

// TestDoctor_ShowsAConfigViolationRatherThanRefusing is Task 3's S-7 answered at the operator
// surface: config.LoadForCapture refuses a whole delivery over any Validate violation, so doctor
// must take the TOLERANT path — clamp, warn, report — or the one command that could explain a bad
// config would be the one command a bad config silences.
func TestDoctor_ShowsAConfigViolationRatherThanRefusing(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	// A gated switch set true: Validate refuses it in this build, Load restores the default and
	// warns (config/runtime.go:104-116).
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(p.Layot.Dot, "config.json")),
		[]byte(`{"runtime":{"migration":{"replacement":{"newResult":true}}}}`), 0o600))

	code, doc, errw := doctorJSON(t, p.Root)
	require.Equal(t, ExitOK, code, "stderr=%s", errw)

	row := doctorFindRow(t, doc, "controls", "config.violations")
	require.Equal(t, "degraded", row["status"], "row=%v", row)
	require.Contains(t, row["detail"], "runtime.migration.replacement.newResult")

	effective := doctorFindRow(t, doc, "controls", "runtime.migration.replacement.newResult")
	require.Equal(t, "false", effective["observed"],
		"the refused value must not be reported as in effect: %v", effective)
}

// TestDoctor_ReportsTheProjectStoreWritabilityProbe covers Task 2's F-2: a read-only .qompack is
// otherwise invisible to a user, because every writer degrades quietly and the only record is a day
// log nothing points at.
//
// The probe itself creates NOTHING (ruling R5-A). The first version wrote and deleted a file under
// tmp/, which created `.qompack/tmp/` on a project that had none — the same class of change this
// row exists to detect. It now opens an existing file for writing and closes it untouched, and says
// "not probed" when there is no such file rather than guessing in either direction.
func TestDoctor_ReportsTheProjectStoreWritabilityProbe(t *testing.T) {
	t.Parallel()

	t.Run("a project with files to open", func(t *testing.T) {
		t.Parallel()

		p := seedFsckProject(t)
		_, doc, _ := doctorJSON(t, p.Root)

		row := doctorFindRow(t, doc, "recording", "store.writable")
		require.Equal(t, "ok", row["status"], "row=%v", row)
		observed, ok := row["observed"].(string)
		require.True(t, ok, "observed must be a string, row=%v", row)
		require.Contains(t, strings.ToLower(observed), "writable")
		require.Contains(t, row["detail"], "no file was created to find out")
	})

	t.Run("a bare .qompack has nothing to open", func(t *testing.T) {
		t.Parallel()

		dir := bareQompackProject(t)
		_, doc, _ := doctorJSON(t, dir)

		row := doctorFindRow(t, doc, "recording", "store.writable")
		require.Equal(t, "unknown", row["status"], "row=%v", row)
		require.Contains(t, row["observed"], "not probed")
	})
}

// TestDoctor_AgreesWithStatusOnModeAndProvenance is V6-VERIFY row 3.7's "doctor and SP-14 status
// agree", enforced by construction: doctor embeds commands.CollectStatus over the same
// StatusSources the status command wires, so the two documents cannot drift. Both the
// daemon-absent and the daemon-present cases are asserted, because the two answers differ and a
// test that only ran one would not notice an agreement that holds in one of them.
func TestDoctor_AgreesWithStatusOnModeAndProvenance(t *testing.T) {
	// Not parallel: the second half takes the project's singleton lock.
	p := seedFsckProject(t)

	agree := func(t *testing.T) {
		t.Helper()

		_, doctorDoc, derr := doctorJSON(t, p.Root)
		primary := doctorFindRow(t, doctorDoc, "status", "status.primary")
		mode := doctorFindRow(t, doctorDoc, "status", "status.mode")

		code, out, serr := fsckDispatchIn(t, p.Root, "status", "--json")
		require.Equal(t, ExitOK, code, "stderr=%s", serr)
		var env struct {
			Data struct {
				Primary  map[string]any `json:"primary"`
				Snapshot map[string]any `json:"snapshot"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &env), "stdout=%s", out)

		require.Equal(t, env.Data.Primary["source"], primary["observed"],
			"doctor and status must report the same primary provenance; doctor stderr=%s", derr)
		// The agreement is conditional and the row says so (ruling R5-E): doctor strips Self, so it
		// never SPAWNS a daemon, while `status` wires a lazily-spawning client. Both processes here
		// run with Self empty — every Env a test builds does — so the condition holds by
		// construction, and what is being asserted is the collector, not the spawn policy.
		require.Contains(t, primary["detail"], "never SPAWNS a daemon",
			"the row must state the condition under which the agreement holds")
		wantMode := "unknown"
		if m, ok := env.Data.Snapshot["mode"].(string); ok && m != "" {
			wantMode = m
		}
		require.Equal(t, wantMode, mode["observed"],
			"doctor and status must report the same snapshot mode")
	}

	t.Run("no daemon", func(t *testing.T) { agree(t) })

	t.Run("a daemon holds the lock", func(t *testing.T) {
		addr, err := ipc.Resolve(p.Root)
		require.NoError(t, err)
		lock, err := daemon.AcquireLock(p.Root, addr, testClock())
		require.NoError(t, err)
		t.Cleanup(func() { _ = lock.Release() })

		row := func() map[string]any {
			_, doc, _ := doctorJSON(t, p.Root)
			return doctorFindRow(t, doc, "scope", "scope.daemon")
		}()
		require.Contains(t, row["observed"], "held")
		agree(t)
	})
}

// TestDoctor_NamesTheVersionAndDoesNotProbeTheHost pins §7.5's "repository JSON parsing alone does
// not validate the installed plugin": doctor states what it read and says plainly that the host was
// not probed, rather than implying an installed-host fact it has no evidence for.
func TestDoctor_NamesTheVersionAndDoesNotProbeTheHost(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	_, doc, _ := doctorJSON(t, p.Root)

	version := doctorFindRow(t, doc, "version", "version.plugin")
	require.Equal(t, core.Version, version["observed"])

	bundle := doctorFindRow(t, doc, "version", "version.bundle")
	require.Equal(t, "unknown", bundle["status"], "row=%v", bundle)

	plugin := doctorFindRow(t, doc, "version", "version.pluginRoot")
	require.Contains(t, plugin["observed"], "unset")

	host := doctorFindRow(t, doc, "host", "host.claudeCLI")
	require.Contains(t, host["observed"], "not probed")
	require.Contains(t, host["detail"], "test/canary")
}

// TestDoctor_TableModeWritesAReadableSummary asserts the default rendering names every section, the
// gate rows and the per-row detail. The table is what a person reads when something is wrong, and a
// report nobody can read is not a report.
func TestDoctor_TableModeWritesAReadableSummary(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	code, out, errw := fsckDispatch(t, "doctor", "--project", p.Root)

	require.Equal(t, ExitOK, code, "stderr=%s", errw)
	for _, want := range []string{
		"VERSION AND IDENTITY", "HOST", "SCOPE", "CAPABILITIES", "CONTROLS",
		"RECORDING GAPS", "RETRIEVAL GAPS", "AGREEMENT WITH QOMPACK STATUS",
		"version.plugin", "host.claudeCLI", "observation", "store.writable", "mcp.tools",
		"passed=false", "exit: 0",
	} {
		require.Contains(t, out, want)
	}
}

// TestDoctor_ReportsTheThreePluginRootOutcomes pins the contract assertion's own three answers at
// the operator surface. The middle one is the dangerous one: CLAUDE_PLUGIN_ROOT set to a directory
// with no binary under it makes every hook the host runs fail as the HOST's error, and nothing else
// in the product would say so.
func TestDoctor_ReportsTheThreePluginRootOutcomes(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	run := func(t *testing.T, value string) map[string]any {
		t.Helper()

		var out, errw bytes.Buffer
		code := Dispatch(context.Background(), All(),
			[]string{"qompack", "doctor", "--project", p.Root, "--json"}, Env{
				Getenv:  envWith(map[string]string{"CLAUDE_PLUGIN_ROOT": value}),
				Stdin:   bytes.NewReader(nil),
				Clock:   testClock(),
				HomeDir: t.TempDir(),
			}, &out, &errw)
		require.Equal(t, ExitOK, code, "stderr=%s", errw.String())
		var doc map[string]any
		require.NoError(t, json.Unmarshal(out.Bytes(), &doc), "stdout=%s", out.String())
		return doc
	}

	t.Run("unset", func(t *testing.T) {
		t.Parallel()
		row := doctorFindRow(t, run(t, ""), "version", "version.pluginRoot")
		require.Equal(t, "unknown", row["status"])
		require.Contains(t, row["observed"], "unset")
	})

	t.Run("set but no binary under it", func(t *testing.T) {
		t.Parallel()
		row := doctorFindRow(t, run(t, t.TempDir()), "version", "version.pluginRoot")
		require.Equal(t, "degraded", row["status"], "row=%v", row)
		require.Contains(t, row["observed"], "no binary")
	})

	t.Run("set and resolves", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o700))
		name := "qompack"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", name), []byte("binary"), 0o600))

		row := doctorFindRow(t, run(t, dir), "version", "version.pluginRoot")
		require.Equal(t, "ok", row["status"], "row=%v", row)
		require.Contains(t, row["observed"], "resolves")
	})
}

// TestDoctor_AnUnreadableSpoolIsItsOwnRow is fix round 2's finding N-4.
//
// spoolRow collapsed every os.ReadDir failure into "no spool directory / nothing has been spooled in
// this project", so a spool nobody could read reported as a project that had never spooled anything
// — the one case where the unreplayed count matters most. "The count is unknown" and "the count is
// zero" are different answers (ruling R5-D), and only one of them may be inferred from a failed read.
func TestDoctor_AnUnreadableSpoolIsItsOwnRow(t *testing.T) {
	t.Parallel()

	t.Run("absent", func(t *testing.T) {
		t.Parallel()

		p := seedFsckProject(t)
		require.NoError(t, os.RemoveAll(paths.Long(p.Layot.Spool)))

		_, doc, errw := doctorJSON(t, p.Root)
		row := doctorFindRow(t, doc, "recording", "spool.pending")
		require.Equal(t, doctorUnknown, row["status"], "stderr=%s", errw)
		require.Equal(t, "no spool directory", row["observed"])
	})

	t.Run("unreadable", func(t *testing.T) {
		t.Parallel()

		p := seedFsckProject(t)
		blockAsFile(t, p.Layot.Spool)

		_, doc, errw := doctorJSON(t, p.Root)
		row := doctorFindRow(t, doc, "recording", "spool.pending")
		require.Equal(t, doctorUnknown, row["status"],
			"an unreadable spool is never ok; stderr=%s", errw)
		require.Equal(t, "unreadable", row["observed"],
			"and never 'no spool directory': the directory is there, it cannot be read")
		detail, _ := row["detail"].(string)
		require.Contains(t, detail, p.Layot.Spool, "the row names the path it could not read")
		require.Contains(t, detail, "unknown rather than zero")
	})
}

// TestDoctor_ReportsTheSegmentsTheProjectHolds is fix round 3's finding 1.
//
// `store.OpenReadOnly` did not load index/segments.jsonl — openSegLog creates its file, and asking a
// project a question may not create one — so `Stats.Segments` came back 0 and doctor printed
// "0 segment(s)" with status ok for a project whose log held three records. A confident zero from a
// source that was never read is the same defect the spool row was corrected for, and it is worse
// here because the row reads `ok`.
func TestDoctor_ReportsTheSegmentsTheProjectHolds(t *testing.T) {
	// Not parallel: it opens a real store, which seeds through t.Setenv-free helpers but writes.
	p := seedFsckProject(t)
	ctx := context.Background()

	s, err := store.Open(p.Root, config.Defaults(), store.Deps{Clock: testClock()})
	require.NoError(t, err)
	for turn := 0; turn < 3; turn++ {
		id, openErr := s.Segments().Open(ctx, store.Segment{
			Session: core.SessionID("s-fsck"), StartTurn: core.TurnIndex(turn),
		})
		require.NoError(t, openErr)
		require.NoError(t, s.Segments().Close(ctx, id, core.TurnIndex(turn),
			map[string]float64{"tokens": 1}))
	}
	require.NoError(t, s.Flush(ctx))
	require.NoError(t, s.Close())

	_, doc, errw := doctorJSON(t, p.Root)
	row := doctorFindRow(t, doc, "retrieval", "store.open")
	observed, _ := row["observed"].(string)
	require.Contains(t, observed, "3 segment(s)",
		"doctor reports the segments the log holds, never a confident zero; stderr=%s", errw)
	require.NotContains(t, observed, "0 segment(s)")
}
