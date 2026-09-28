package commands_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/config"
)

// wantCommands is the shipped §5.17 list. It is written out here rather than read from
// commands.Names() so the test actually pins the surface: comparing a list to itself would pass no
// matter what SP-14 adds or drops. checkpoint is not in it: its only route is the PreCompact hook
// entry point, so the command is not shipped (internal/pluginmanifest's commandSpecs says why).
var wantCommands = []string{"status", "recall", "pin", "why", "dropped", "eval"}

// TestAll_CoversEverySlashCommand checks the table is complete from wave 0. Completeness matters
// because plugin/commands/*.md is generated from a typed source and diffed in CI: a missing entry
// here becomes a missing markdown shell, and the slash command silently does not exist.
func TestAll_CoversEverySlashCommand(t *testing.T) {
	t.Parallel()

	got := make([]string, 0, len(wantCommands))
	for _, c := range commands.All(commands.Deps{Cfg: config.Defaults()}) {
		got = append(got, c.Name())
	}

	require.Equal(t, wantCommands, got, "§5.17 names, in order")
	require.Equal(t, wantCommands, commands.Names(), "Names must agree with All")
}

// TestAll_SurvivesEntirelyNilDeps is the property that lets a half-built tree still answer.
//
// Every Deps member is nil during waves 1–2. If All panicked or refused, the plugin would have no
// working commands during precisely the period when asking it what state it is in matters most.
//
// The assertion is that every command gives a CLASSIFIED answer — not that it gives one specific
// error. It used to require core.ErrNotImplemented from every command, which was exactly right
// while all of them were stubs and stops being right as SP-14 fills them in: an implemented command run
// with no dependencies reports unavailable, and one run without a required argument reports a
// usage error. Both are honest, and neither is the panic or the silent empty success this test
// exists to prevent.
func TestAll_SurvivesEntirelyNilDeps(t *testing.T) {
	t.Parallel()

	cmds := commands.All(commands.Deps{})
	require.Len(t, cmds, len(wantCommands))

	for _, c := range cmds {
		var out bytes.Buffer
		err := c.Run(context.Background(), nil, &out)

		// status is the exception, and the exception is the design: its job is to report what
		// could be observed, so "nothing could be reached" is one of its answers rather than a
		// failure to produce one. It must still say so in the output.
		if c.Name() == "status" {
			require.NoError(t, err)
			require.Contains(t, out.String(), "unavailable")
			continue
		}

		require.Error(t, err, "/qompack:%s must not report success with no dependencies", c.Name())

		kind := commands.KindOf(err)
		require.Contains(t,
			[]commands.ErrorKind{commands.ErrorKindUnavailable, commands.ErrorKindUsage},
			kind, "/qompack:%s answered %q: %v", c.Name(), kind, err)

		if kind == commands.ErrorKindUnavailable {
			require.Contains(t, err.Error(), c.Name(),
				"the error must name the command so a user can tell which one could not answer")
		}
	}
}

// TestNames_ReturnsACopy guards against a caller mutating the package's own table through the
// slice it was handed.
func TestNames_ReturnsACopy(t *testing.T) {
	t.Parallel()

	first := commands.Names()
	first[0] = "mutated"
	require.Equal(t, wantCommands[0], commands.Names()[0])
}
