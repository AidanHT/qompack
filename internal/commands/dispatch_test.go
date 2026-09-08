package commands_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// fixedClock is the injected clock every deterministic case in this file uses. internal/commands
// cannot import internal/testutil — testutil is a composition root and so is commands, and an
// in-package test file gets no carve-out — so the fake lives here, which is the same local
// convention SP-13 adopted in internal/mcp.
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time                  { return c.t }
func (c fixedClock) Since(t time.Time) time.Duration { return c.t.Sub(t) }

func testDeps() commands.Deps {
	return commands.Deps{
		Cfg:   config.Defaults(),
		Clock: fixedClock{t: time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)},
	}
}

// find returns the named command from All.
func find(t *testing.T, name string) commands.Command {
	t.Helper()
	for _, c := range commands.All(testDeps()) {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("no such command %q", name)
	return nil
}

// TestRun_HelpFlagPrintsHelpAndSucceeds: --help is not a usage error. A user asking what a command
// does has not made a mistake, so it exits 0 and writes to the ordinary output stream.
func TestRun_HelpFlagPrintsHelpAndSucceeds(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-h", "--help", "-help"} {
		for _, s := range commands.Specs() {
			var out bytes.Buffer
			err := find(t, s.Name).Run(context.Background(), []string{flag}, &out)
			require.NoError(t, err, "%s %s must succeed", s.Name, flag)
			require.Contains(t, out.String(), s.Summary)
		}
	}
}

// TestRun_UnknownFlagIsAUsageError is the §2.3 exit-2 case. An unrecognized flag must not be
// swallowed as a positional argument: `qompack recall --jsonn foo` searching for "--jsonn" is
// indistinguishable, from the user's side, from recall being broken.
func TestRun_UnknownFlagIsAUsageError(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := find(t, "recall").Run(context.Background(), []string{"--jsonn", "foo"}, &out)
	require.ErrorIs(t, err, commands.ErrUsage)
	require.Equal(t, commands.ExitUsage, commands.ExitCode(err))
	require.Contains(t, err.Error(), "--jsonn")
}

// TestRun_ValueFlagRequiresItsValue keeps a dangling `--k` from being read as "k with the empty
// value", which would silently become a zero result count.
func TestRun_ValueFlagRequiresItsValue(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := find(t, "recall").Run(context.Background(), []string{"--k"}, &out)
	require.ErrorIs(t, err, commands.ErrUsage)
	require.Contains(t, err.Error(), "--k")
}

// TestRun_JSONEnvelopeCarriesTheError is the parity rule: whatever a person reads on stderr, a
// scripted caller reading stdout must be able to learn too. An unimplemented command under --json
// emits a well-formed envelope that says so — never an empty document, never a fabricated success.
func TestRun_JSONEnvelopeCarriesTheError(t *testing.T) {
	t.Parallel()

	for _, s := range commands.Specs() {
		var out bytes.Buffer
		err := find(t, s.Name).Run(context.Background(), []string{"--json"}, &out)
		require.True(t, core.IsNotImplemented(err), "%s: %v", s.Name, err)

		env, decodeErr := commands.DecodeEnvelope(out.Bytes())
		require.NoError(t, decodeErr, "%s: --json must emit a decodable envelope", s.Name)
		require.Equal(t, s.Name, env.Command)
		require.False(t, env.OK, "%s: an unimplemented command is not a success", s.Name)
		require.NotNil(t, env.Error)
		require.Equal(t, commands.ErrorKindFailed, env.Error.Kind)
		require.Contains(t, env.Error.Message, s.Name)
	}
}

// TestRun_WithoutJSONWritesNoEnvelope keeps the two renderings separate: text output must not
// carry a JSON document a person then has to read around.
func TestRun_WithoutJSONWritesNoEnvelope(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := find(t, "status").Run(context.Background(), nil, &out)
	require.True(t, core.IsNotImplemented(err))
	require.Empty(t, out.String())
}

// TestRun_ExitCodesCoverEveryCommand ties dispatch to the §2.3 table for all seven names at once.
func TestRun_ExitCodesCoverEveryCommand(t *testing.T) {
	t.Parallel()

	for _, s := range commands.Specs() {
		var out bytes.Buffer
		require.Equal(t, commands.ExitOK,
			commands.ExitCode(find(t, s.Name).Run(context.Background(), []string{"--help"}, &out)))

		out.Reset()
		require.Equal(t, commands.ExitError,
			commands.ExitCode(find(t, s.Name).Run(context.Background(), nil, &out)),
			"%s: an unimplemented command is an error, not a usage mistake", s.Name)

		out.Reset()
		require.Equal(t, commands.ExitUsage,
			commands.ExitCode(find(t, s.Name).Run(context.Background(), []string{"--not-a-flag"}, &out)))
	}
}

// TestRun_DoubleDashEndsFlagParsing lets a user search for a term that starts with a dash.
func TestRun_DoubleDashEndsFlagParsing(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := find(t, "recall").Run(context.Background(), []string{"--", "--not-a-flag"}, &out)
	require.True(t, core.IsNotImplemented(err), "the term after -- is positional, not a flag")
}

// TestEnvelope_IsIndentedWithATrailingNewline pins the on-the-wire formatting, which a golden
// fixture and a shell pipeline both depend on.
func TestEnvelope_IsIndentedWithATrailingNewline(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	_ = find(t, "status").Run(context.Background(), []string{"--json"}, &out)

	s := out.String()
	require.True(t, strings.HasSuffix(s, "\n"))
	require.Contains(t, s, "\n  \"command\": \"status\"")
	require.True(t, json.Valid(out.Bytes()))
}
