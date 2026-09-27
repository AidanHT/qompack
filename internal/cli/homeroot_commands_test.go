package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/paths"
)

// The operator half of D18 (homeroot_test.go has the fixture and the session half): status and
// doctor say why a home-directory root records nothing, config print shows the user-global layer
// alone, and every command that would open, scan, repair, lock, back up or query a store refuses
// with a non-zero exit — and none of them creates, removes or rewrites anything under the home.

// TestHomeRoot_StatusSaysWhy: status in a refused session still exits 0 — it reports, it does not
// fail — and its report, in both forms, names the refusal. It contacts no daemon and lays nothing
// out.
func TestHomeRoot_StatusSaysWhy(t *testing.T) {
	f := newHomeFixture(t, false)
	f.seal(t)

	var out, errw bytes.Buffer
	require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "status"},
		homeCommandEnv(t, f, ""), &out, &errw), "stderr=%s", errw.String())
	require.Contains(t, out.String(), "the project root is the home directory", "status says why:\n%s", out.String())

	out.Reset()
	require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "status", "--json"},
		homeCommandEnv(t, f, ""), &out, &errw), "stderr=%s", errw.String())
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Primary struct {
				Source string `json:"source"`
				Reason string `json:"reason"`
			} `json:"primary"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "status --json: %s", out.String())
	require.True(t, env.OK, "status reports; it does not fail")
	require.Equal(t, "none", env.Data.Primary.Source, "nothing was observed")
	require.Contains(t, env.Data.Primary.Reason, "the project root is the home directory")
	f.requireUntouched(t, "qompack status")
}

// TestHomeRoot_DoctorSaysWhy: doctor in a refused session exits 0, as it always does, and its
// scope.root row is disabled with the refusal as its detail. It reads no project state from the
// user-global layer's directory and writes nothing.
func TestHomeRoot_DoctorSaysWhy(t *testing.T) {
	f := newHomeFixture(t, false)
	f.seal(t)

	var out, errw bytes.Buffer
	require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "doctor", "--json"},
		homeCommandEnv(t, f, ""), &out, &errw), "stderr=%s", errw.String())
	var rep doctorReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &rep), "doctor --json: %s", out.String())

	rows := map[string]doctorRow{}
	for _, sec := range rep.Sections {
		for _, r := range sec.Rows {
			rows[r.ID] = r
		}
	}
	root, ok := rows["scope.root"]
	require.True(t, ok, "doctor reports scope.root")
	require.Equal(t, doctorDisabled, root.Status, "the refusal is a decision (D18), reported as disabled")
	require.Contains(t, root.Detail, "the project root is the home directory")
	require.NotContains(t, rows, "scope.established", "the user-global layer's directory is not a project store")
	require.Equal(t, doctorDisabled, rows["store.writable"].Status, "no store is probed in the home directory")
	require.Equal(t, doctorDisabled, rows["store.open"].Status, "no store is opened in the home directory")
	require.Contains(t, rows["status.primary"].Detail, "the project root is the home directory",
		"doctor agrees with status on why")

	out.Reset()
	require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "doctor"},
		homeCommandEnv(t, f, ""), &out, &errw), "stderr=%s", errw.String())
	require.Contains(t, out.String(), "the project root is the home directory", "the table says why too")
	f.requireUntouched(t, "qompack doctor")
}

// TestHomeRoot_StoreCommandsRefuseWithANonZeroExit: every command that would open, scan, repair,
// lock, back up or query a store refuses in a refused session with exit 1 and a message saying
// why, whether the root came from the override, from `--project` or from the working directory.
func TestHomeRoot_StoreCommandsRefuseWithANonZeroExit(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"fsck", []string{"fsck"}},
		{"fsck --json", []string{"fsck", "--json"}},
		{"fsck --seal-check", []string{"fsck", "--seal-check"}},
		{"fsck --repair --yes", []string{"fsck", "--repair", "--yes"}},
		{"fsck --project <home>", []string{"fsck", "--project", "<home>"}},
		{"backup create", []string{"backup", "create", "--id", "b1"}},
		{"backup verify", []string{"backup", "verify", "--id", "b1"}},
		{"backup create --project <home>", []string{"backup", "create", "--id", "b1", "--project", "<home>"}},
		{"admin delivery-seal --check", []string{"admin", "delivery-seal", "--check"}},
		{"admin delivery-seal --project <home> --check", []string{"admin", "delivery-seal", "--project", "<home>", "--check"}},
		{"self-test", []string{"self-test"}},
		{"self-test --json", []string{"self-test", "--json"}},
		{"recall", []string{"recall", "pool timeout"}},
		{"why", []string{"why", "src/a.go"}},
		{"dropped", []string{"dropped"}},
		{"pin", []string{"pin", "keep the port at 8443"}},
		{"pin --list", []string{"pin", "--list"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHomeFixture(t, false)
			f.seal(t)
			argv := []string{"qompack"}
			for _, a := range tc.argv {
				if a == "<home>" {
					a = f.home
				}
				argv = append(argv, a)
			}
			var out, errw bytes.Buffer
			code := Dispatch(context.Background(), All(), argv, homeCommandEnv(t, f, ""), &out, &errw)
			require.Equal(t, ExitError, code, "%v must refuse with exit 1; stdout=%s stderr=%s", argv, out.String(), errw.String())
			require.Contains(t, out.String()+errw.String(), "the project root is the home directory",
				"%v must say why; stdout=%s stderr=%s", argv, out.String(), errw.String())
			f.requireUntouched(t, strings.Join(tc.argv, " "))
		})
	}
}

// TestHomeRoot_ARelativeProjectFlagTypedInHomeIsRefused is `qompack fsck --project .` typed in the
// home directory: the relative spelling names the home directory and is refused like any other.
func TestHomeRoot_ARelativeProjectFlagTypedInHomeIsRefused(t *testing.T) {
	f := newHomeFixture(t, false)
	f.seal(t)
	t.Chdir(f.home)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), []string{"qompack", "fsck", "--project", "."},
		Env{Getenv: noEnv, Clock: testClock(), HomeDir: f.home}, &out, &errw)
	require.Equal(t, ExitError, code, "stdout=%s stderr=%s", out.String(), errw.String())
	require.Contains(t, errw.String(), "the project root is the home directory")
	f.requireUntouched(t, "qompack fsck --project .")
}

// TestHomeRoot_RecallJSONReportsTheRefusalAsUnavailable: a scripted caller reading the --json
// envelope learns what a person reading stderr would, as an "unavailable" error.
func TestHomeRoot_RecallJSONReportsTheRefusalAsUnavailable(t *testing.T) {
	f := newHomeFixture(t, false)
	f.seal(t)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), []string{"qompack", "recall", "--json", "pool timeout"},
		homeCommandEnv(t, f, ""), &out, &errw)
	require.Equal(t, ExitError, code, "stderr=%s", errw.String())
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "recall --json: %s", out.String())
	require.False(t, env.OK)
	require.Equal(t, "unavailable", env.Error.Kind)
	require.Contains(t, env.Error.Message, "the project root is the home directory")
	f.requireUntouched(t, "qompack recall --json")
}

// TestHomeRoot_BackupRestoreRefusesTheHomeDirectoryAsItsDestination: a restore whose destination is
// the home directory would make the user-global layer's directory a project store just as surely as
// a session there, so it is refused before the source is even locked.
func TestHomeRoot_BackupRestoreRefusesTheHomeDirectoryAsItsDestination(t *testing.T) {
	f := newHomeFixture(t, false)
	source := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(source)))
	f.seal(t)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(),
		[]string{"qompack", "backup", "restore", "--id", "b1", "--project", source, "--destination", f.home},
		Env{Getenv: noEnv, Clock: testClock(), HomeDir: f.home}, &out, &errw)
	require.Equal(t, ExitError, code, "stdout=%s stderr=%s", out.String(), errw.String())
	require.Contains(t, out.String()+errw.String(), "the project root is the home directory")
	_, err := os.Stat(paths.Long(daemon.LockPath(source)))
	require.ErrorIs(t, err, fs.ErrNotExist, "the source must not even be locked")
	f.requireUntouched(t, "qompack backup restore --destination <home>")
}

// TestHomeRoot_ConfigPrintShowsOnlyTheUserGlobalLayer: `config print` in the home directory keeps
// working — it is how a user inspects the user-global layer — but applies that file once, as the
// user layer, and never persists its violations under <home>/.qompack/state as if the home were a
// project.
func TestHomeRoot_ConfigPrintShowsOnlyTheUserGlobalLayer(t *testing.T) {
	f := newHomeFixture(t, false)
	f.seal(t)

	var out, errw bytes.Buffer
	require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "config", "print", "--provenance"},
		homeCommandEnv(t, f, ""), &out, &errw), "stderr=%s", errw.String())
	requireUserLayerApplied(t, out.String(), f.home)
	require.NotContains(t, out.String(), "// project ", "the home directory has no project layer")
	require.Contains(t, errw.String(), "the project root is the home directory", "stderr says why no project layer applies")

	// The schema is a property of the type, so it prints wherever the command runs.
	out.Reset()
	require.Equal(t, ExitOK, Dispatch(context.Background(), All(), []string{"qompack", "config", "schema"},
		homeCommandEnv(t, f, ""), &out, &errw), "stderr=%s", errw.String())
	require.True(t, json.Valid(out.Bytes()), "config schema prints its JSON Schema: %s", out.String())
	f.requireUntouched(t, "qompack config print and config schema")
}
