package commands_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/pluginmanifest"
)

// section75Names is the §7.5 list, written out rather than read back from the package so the test
// pins the surface instead of comparing it to itself.
var section75Names = []string{"status", "recall", "pin", "checkpoint", "why", "dropped", "eval"}

// TestCommandNames_MatchesSection75 is the retained wave-0 identity: SP-14 replaces bodies, never
// entries. It also pins the order, because plugin/commands/*.md is generated from this list and a
// reordering would rewrite every generated file for no reason.
func TestCommandNames_MatchesSection75(t *testing.T) {
	t.Parallel()

	require.Equal(t, section75Names, commands.Names())

	got := make([]string, 0, len(section75Names))
	for _, s := range commands.Specs() {
		got = append(got, s.Name)
	}
	require.Equal(t, section75Names, got, "Specs must agree with Names, in order")
}

// TestSpecs_PinInstalledSurface is the SP14-M7-04 gate: the help a user reads from the binary and
// the description installed into plugin/commands/*.md must be the same sentence.
//
// Specs() derives its prose from internal/pluginmanifest, so an equality assertion between the two
// would compare a list to itself and pass no matter what either side became. The expected values
// are therefore written out here. An intentional wording change updates this table in the same
// commit; an accidental one fails.
func TestSpecs_PinInstalledSurface(t *testing.T) {
	t.Parallel()

	want := []commands.Spec{
		{
			Name: "status", Subcommand: "status", ArgumentHint: "[--json]",
			Summary: "Qompack status — mode, contracts, store, latency, last decision",
		},
		{
			Name: "recall", Subcommand: "recall", ArgumentHint: "<query> [--k N]",
			Summary: "Search stored tool output and file versions by content",
		},
		{
			Name: "pin", Subcommand: "pin", ArgumentHint: "<text>",
			Summary: "Pin an invariant so it is never summarized away",
		},
		{
			Name: "checkpoint", Subcommand: "checkpoint", ArgumentHint: "[--reason <text>]",
			Summary: "Write an immutable checkpoint now",
		},
		{
			Name: "why", Subcommand: "why", ArgumentHint: "<decision-id>",
			Summary: "Explain a recorded decision and the evidence behind it",
		},
		{
			Name: "dropped", Subcommand: "dropped", ArgumentHint: "[--json]",
			Summary: "Report what the last compaction dropped and how to get it back",
		},
		{
			Name: "eval", Subcommand: "eval", ArgumentHint: "[--corpus <path>]",
			Summary: "Report the latest replay and live evaluation results",
		},
	}

	got := commands.Specs()
	require.Len(t, got, len(want))
	for i, w := range want {
		require.Equal(t, w.Name, got[i].Name)
		require.Equal(t, w.Summary, got[i].Summary, "%s: summary", w.Name)
		require.Equal(t, w.ArgumentHint, got[i].ArgumentHint, "%s: argument hint", w.Name)
		require.Equal(t, w.Subcommand, got[i].Subcommand, "%s: subcommand", w.Name)
	}

	// And the installed manifest still agrees, which is the direction that actually ships.
	installed := make(map[string]pluginmanifest.CommandDoc)
	for _, c := range pluginmanifest.Default(core.Version).Commands {
		installed[c.Name] = c
	}
	require.Len(t, installed, len(want), "the manifest must not carry a command the binary lacks")
	for _, w := range want {
		doc, ok := installed[w.Name]
		require.True(t, ok, "/qompack:%s is not in the installed manifest", w.Name)
		require.Equal(t, w.Summary, doc.Description, "%s: installed description", w.Name)
	}
}

// TestExitCode is the retained §2.3 exit-code contract: 0 success, 2 usage, 1 everything else.
// A command that cannot answer is an error, not a usage mistake — a user who typed the command
// correctly should not be told they got the syntax wrong.
func TestExitCode(t *testing.T) {
	t.Parallel()

	require.Equal(t, commands.ExitOK, commands.ExitCode(nil))
	require.Equal(t, commands.ExitUsage, commands.ExitCode(commands.ErrUsage))
	require.Equal(t, commands.ExitUsage, commands.ExitCode(commands.UsageErrorf("bad flag %q", "--nope")))
	require.Equal(t, commands.ExitError, commands.ExitCode(core.ErrNotImplemented))
	require.Equal(t, commands.ExitError, commands.ExitCode(commands.ErrUnsupported))
}

// TestUsageErrorf_WrapsErrUsage keeps the sentinel reachable through errors.Is so the CLI can map
// it to exit 2 without string matching.
func TestUsageErrorf_WrapsErrUsage(t *testing.T) {
	t.Parallel()

	err := commands.UsageErrorf("recall needs a query")
	require.ErrorIs(t, err, commands.ErrUsage)
	require.Contains(t, err.Error(), "recall needs a query")
}

// TestSpec_WriteHelp_IsDocumentedAndDeterministic covers the "documented help, no ANSI,
// deterministic ordering" half of the Produces contract.
func TestSpec_WriteHelp_IsDocumentedAndDeterministic(t *testing.T) {
	t.Parallel()

	for _, s := range commands.Specs() {
		var first, second strings.Builder
		require.NoError(t, s.WriteHelp(&first))
		require.NoError(t, s.WriteHelp(&second))
		require.Equal(t, first.String(), second.String(), "%s: help must be byte-identical across calls", s.Name)

		help := first.String()
		require.Contains(t, help, s.Name)
		require.Contains(t, help, s.Summary)
		require.NotContains(t, help, "\x1b[", "%s: help must carry no ANSI escapes", s.Name)
		require.True(t, strings.HasSuffix(help, "\n"), "%s: help must end in a newline", s.Name)

		for _, f := range s.Flags {
			require.Contains(t, help, "--"+f.Name, "%s: help must document --%s", s.Name, f.Name)
			require.Contains(t, help, f.Summary, "%s: --%s must carry its summary", s.Name, f.Name)
		}
	}
}

// TestSpecs_EveryCommandDocumentsJSON is the parity half of the envelope contract: every command
// offers the same machine-readable surface, so a caller never has to know which ones do.
func TestSpecs_EveryCommandDocumentsJSON(t *testing.T) {
	t.Parallel()

	for _, s := range commands.Specs() {
		var found bool
		for _, f := range s.Flags {
			if f.Name == "json" {
				found = true
			}
		}
		require.True(t, found, "/qompack:%s must accept --json", s.Name)
	}
}

// TestEnvelope_CarriesSchemaAndCommand pins the stable JSON document shape. Data stays raw so a
// frontend can hand through what its owning API already marshalled without a second round trip.
func TestEnvelope_CarriesSchemaAndCommand(t *testing.T) {
	t.Parallel()

	env := commands.NewEnvelope("status")
	env.Data = json.RawMessage(`{"mode":"active"}`)

	b, err := json.Marshal(env)
	require.NoError(t, err)

	var back map[string]any
	require.NoError(t, json.Unmarshal(b, &back))
	require.Equal(t, float64(commands.EnvelopeSchema), back["schema"])
	require.Equal(t, "status", back["command"])
	require.Equal(t, true, back["ok"])
	require.NotContains(t, back, "error", "a successful envelope carries no error member")
}

// TestEnvelope_ErrorKindsAreDistinct is the "add uncertainty instead of zero-filling" rule at the
// envelope level: a command that could not observe something is `unavailable`, which is not the
// same answer as `failed`, and neither may be reported as success.
func TestEnvelope_ErrorKindsAreDistinct(t *testing.T) {
	t.Parallel()

	kinds := []commands.ErrorKind{
		commands.ErrorKindUsage,
		commands.ErrorKindUnavailable,
		commands.ErrorKindUnsupported,
		commands.ErrorKindFailed,
	}
	seen := make(map[commands.ErrorKind]bool, len(kinds))
	for _, k := range kinds {
		require.NotEmpty(t, string(k))
		require.False(t, seen[k], "duplicate error kind %q", k)
		seen[k] = true
	}

	env := commands.NewEnvelope("recall")
	env.Fail(commands.ErrorKindUnavailable, "no daemon and no readable spool")
	require.False(t, env.OK)
	require.NotNil(t, env.Error)
	require.Equal(t, commands.ErrorKindUnavailable, env.Error.Kind)

	b, err := json.Marshal(env)
	require.NoError(t, err)
	require.Contains(t, string(b), `"ok":false`)
	require.Contains(t, string(b), `"kind":"unavailable"`)
}

// TestDecodeEnvelope_UnknownSchemaIsUnsupported is the "unknown schema reports unsupported, not
// fabricated success" rule. A reader from a newer build must refuse rather than interpret absent
// members as zeroes, which is exactly the old absent-on-error reading the rollout section forbids.
func TestDecodeEnvelope_UnknownSchemaIsUnsupported(t *testing.T) {
	t.Parallel()

	future := []byte(`{"schema":999,"command":"status","ok":true}`)
	_, err := commands.DecodeEnvelope(future)
	require.ErrorIs(t, err, commands.ErrUnsupported)

	current := []byte(`{"schema":1,"command":"status","ok":true}`)
	env, err := commands.DecodeEnvelope(current)
	require.NoError(t, err)
	require.Equal(t, "status", env.Command)
	require.True(t, env.OK)
}
