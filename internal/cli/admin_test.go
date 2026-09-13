package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/qompack/qompack/internal/daemon"
	"github.com/stretchr/testify/require"
)

// TestAdminDeliverySeal_IsWiredWithItsUsageContract pins the wiring half of design §4.5: the
// offline tool is reachable as a subcommand, it reports through the writer Dispatch hands it, and
// the §2.3 exit-code policy holds for each way an operator can get the invocation wrong.
//
// What the tool DOES to a project is pinned where the project is:
// TestDeliveryOfflineTool_ConvertsAndRefusesWhileADaemonHoldsTheLock (design §6.2, T31).
func TestAdminDeliverySeal_IsWiredWithItsUsageContract(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	run := func(t *testing.T, args ...string) (int, string, string) {
		t.Helper()

		var out, errw bytes.Buffer
		argv := append([]string{"qompack", "admin", "delivery-seal", "--project", dir}, args...)
		code := Dispatch(context.Background(), All(), argv, Env{
			Getenv:  noEnv,
			Stdin:   bytes.NewReader(nil),
			Clock:   testClock(),
			HomeDir: t.TempDir(),
		}, &out, &errw)
		return code, out.String(), errw.String()
	}

	t.Run("check reports a project with no delivery journal", func(t *testing.T) {
		code, out, errw := run(t, "--check")

		require.Equal(t, ExitOK, code, "stderr=%s", errw)
		require.Contains(t, out, "no delivery journal")
	})

	t.Run("no action is a usage error", func(t *testing.T) {
		code, _, errw := run(t)

		require.Equal(t, ExitUsage, code)
		require.Contains(t, errw, "exactly one")
	})

	t.Run("both actions are a usage error", func(t *testing.T) {
		code, _, _ := run(t, "--check", "--to", "v1")

		require.Equal(t, ExitUsage, code)
	})

	t.Run("an unknown target format is a usage error", func(t *testing.T) {
		code, _, errw := run(t, "--to", "v2")

		require.Equal(t, ExitUsage, code)
		require.Contains(t, errw, "v1")
	})

	t.Run("a torn slot needs its confirmation", func(t *testing.T) {
		code, _, errw := run(t, "--check", "--accept-torn-slot")

		require.Equal(t, ExitUsage, code)
		require.Contains(t, errw, "--yes")
	})
}

// deliverySealProbe is written through the DeliverySealOptions.Out the front end handed the tool,
// so the test can prove that writer really is the one Dispatch was given rather than merely
// non-nil.
const deliverySealProbe = "the tool wrote this through o.Out\n"

// TestAdminDeliverySeal_PassesTheOperatorsFlagsThrough pins the other half of design §4.5's front
// end: which OPTIONS the operator's flags become.
//
// Its sibling above pins the usage gate, and every invocation it makes stops there or runs --check
// against an empty directory. That leaves the wiring itself — six fields assembled in one composite
// literal (admin.go) — asserted by nothing, and the most damaging edit is invisible to the library:
// AcceptTornSlot and Confirm forced to true would apply Rule R, with its consent pre-granted, to
// every run an operator makes, and DeliverySealOptions.validate refuses only AcceptTornSlot without
// Confirm, so it would let that pair through. That is §2.9's rollback window taken on an operator's
// behalf without asking.
//
// So the repairDeliverySeal seam is swapped for a capture, and the WHOLE option set is compared,
// not the fields a row happens to care about: a row that passes neither --accept-torn-slot nor
// --yes must produce false for both, which is what makes an always-on Rule R fail here. The exit
// codes an operator's script branches on are asserted in the same table, because §2.3's "1 for
// anything else" was previously pinned for no failure at all.
//
// What the tool DOES to a project stays where the project is:
// TestDeliveryOfflineTool_ConvertsAndRefusesWhileADaemonHoldsTheLock (design §6.2, T31).
func TestAdminDeliverySeal_PassesTheOperatorsFlagsThrough(t *testing.T) {
	// Not parallel, and it must not become so: it swaps a package-level seam. Go resumes paused
	// parallel tests only after every sequential test has returned, so the sibling above — which
	// calls through the same variable — can never overlap this one.
	dir := t.TempDir()
	repairErr := errors.New("the pair is half converted")

	for _, tc := range []struct {
		name string
		args []string
		// ret is what the swapped tool returns, and want the exit code that must follow it.
		ret  error
		want int
		// opts is the whole option set the front end must have assembled, Out and Clock aside:
		// those two are identity-checked separately, against the writer and clock Dispatch was
		// handed.
		opts daemon.DeliverySealOptions
	}{
		{
			name: "--check asks for a check and nothing else",
			args: []string{"--check"},
			want: ExitOK,
			opts: daemon.DeliverySealOptions{ProjectRoot: dir, Check: true},
		},
		{
			name: "--to v1 asks for the conversion and nothing else",
			args: []string{"--to", "v1"},
			want: ExitOK,
			opts: daemon.DeliverySealOptions{ProjectRoot: dir, ToV1: true},
		},
		{
			name: "rule R reaches the tool only when both its flags were typed",
			args: []string{"--accept-torn-slot", "--yes", "--to", "v1"},
			want: ExitOK,
			opts: daemon.DeliverySealOptions{
				ProjectRoot: dir, ToV1: true, AcceptTornSlot: true, Confirm: true,
			},
		},
		{
			name: "--yes alone confirms nothing, because no acceptance was asked for",
			args: []string{"--check", "--yes"},
			want: ExitOK,
			opts: daemon.DeliverySealOptions{ProjectRoot: dir, Check: true, Confirm: true},
		},
		{
			name: "a refused repair is exit 1, not exit 2 and not exit 0",
			args: []string{"--to", "v1"},
			ret:  repairErr,
			want: ExitError,
			opts: daemon.DeliverySealOptions{ProjectRoot: dir, ToV1: true},
		},
		{
			name: "a refused check is exit 1 as well",
			args: []string{"--check"},
			ret:  repairErr,
			want: ExitError,
			opts: daemon.DeliverySealOptions{ProjectRoot: dir, Check: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got daemon.DeliverySealOptions
			calls := 0
			prev := repairDeliverySeal
			t.Cleanup(func() { repairDeliverySeal = prev })
			repairDeliverySeal = func(o daemon.DeliverySealOptions) error {
				calls++
				got = o
				fmt.Fprint(o.Out, deliverySealProbe)
				return tc.ret
			}

			var out, errw bytes.Buffer
			clk := testClock()
			argv := append([]string{"qompack", "admin", "delivery-seal", "--project", dir}, tc.args...)
			code := Dispatch(context.Background(), All(), argv, Env{
				Getenv: noEnv, Stdin: bytes.NewReader(nil), Clock: clk, HomeDir: t.TempDir(),
			}, &out, &errw)

			require.Equal(t, 1, calls, "the tool is run exactly once, stderr=%s", errw.String())
			require.Equal(t, tc.want, code, "stderr=%s", errw.String())

			// The whole struct, so a field set that this row did not ask for fails here whichever
			// field it is. Out and Clock are carried over from what was captured because they are
			// interface values with no useful zero to compare against; both are then checked for
			// what they actually are.
			want := tc.opts
			want.Out, want.Clock = got.Out, got.Clock
			require.Equal(t, want, got, "the flags the operator typed are the options the tool gets")

			require.Equal(t, clk, got.Clock,
				"the tool reads the clock Dispatch was handed, never core.SystemClock()")
			require.Equal(t, deliverySealProbe, out.String(),
				"o.Out must be the stdout Dispatch was handed, so the report reaches the operator")
			if tc.ret != nil {
				require.Contains(t, errw.String(), tc.ret.Error(),
					"a refused repair reports its own reason on stderr")
				require.Contains(t, errw.String(), "qompack admin delivery-seal:",
					"and it is reported once, by the command, not twice by Dispatch as well")
			}
		})
	}
}
