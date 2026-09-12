package cli

import (
	"bytes"
	"context"
	"testing"

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
