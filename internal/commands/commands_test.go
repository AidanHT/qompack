package commands_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// wantCommands is the §5.17 list. It is written out here rather than read from commands.Names()
// so the test actually pins the surface: comparing a list to itself would pass no matter what
// SP-14 adds or drops.
var wantCommands = []string{"status", "recall", "pin", "checkpoint", "why", "dropped", "eval"}

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
func TestAll_SurvivesEntirelyNilDeps(t *testing.T) {
	t.Parallel()

	cmds := commands.All(commands.Deps{})
	require.Len(t, cmds, len(wantCommands))

	for _, c := range cmds {
		var out bytes.Buffer
		err := c.Run(context.Background(), nil, &out)
		require.True(t, core.IsNotImplemented(err),
			"/qompack:%s must report ErrNotImplemented, got %v", c.Name(), err)
		require.Contains(t, err.Error(), c.Name(),
			"the error must name the command so a user can tell which one is missing")
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
