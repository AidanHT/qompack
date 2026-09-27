package commands_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/obs"
)

// errHomeRefusal stands in for the D18 refusal internal/cli binds: its own sentinel, so a row can
// check that the refusal keeps its identity under errors.Is on the way out.
var errHomeRefusal = errors.New("qompack: the project root is the home directory")

// refusedAt is every refused row's clock reading.
var refusedAt = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

// refusedDeps is a Deps whose project was refused, with status sources that fail the test if the
// collector asks them anyway: a refused root has no daemon to ask and no metrics file to read.
func refusedDeps(t *testing.T) commands.Deps {
	t.Helper()
	return commands.Deps{
		Refused: errHomeRefusal,
		Status: commands.StatusSources{
			Daemon: func(context.Context) (commands.DaemonStatus, time.Time, error) {
				t.Error("a refused status asked the daemon")
				return commands.DaemonStatus{}, time.Time{}, nil
			},
			Disk: func(context.Context) (obs.Snapshot, error) {
				t.Error("a refused status read the metrics file")
				return obs.Snapshot{}, nil
			},
		},
		Clock: fixedClock{t: refusedAt},
	}
}

// TestRefused_EveryProjectCommandReportsTheRefusalAsUnavailable: with Deps.Refused set, every command
// that reads or writes a project answers with the refusal instead of running, classified as
// unavailable in the --json envelope, and keeps the refusal's own identity for errors.Is.
func TestRefused_EveryProjectCommandReportsTheRefusalAsUnavailable(t *testing.T) {
	t.Parallel()

	for _, c := range commands.All(refusedDeps(t)) {
		if c.Name() == "status" || c.Name() == "eval" {
			continue
		}
		t.Run(c.Name(), func(t *testing.T) {
			var out bytes.Buffer
			err := c.Run(context.Background(), []string{"--json", "some argument"}, &out)
			require.ErrorIs(t, err, commands.ErrUnavailable)
			require.ErrorIs(t, err, errHomeRefusal, "the refusal keeps its identity")

			env, derr := commands.DecodeEnvelope(out.Bytes())
			require.NoError(t, derr, "--json still writes an envelope: %s", out.String())
			require.False(t, env.OK)
			require.Equal(t, commands.ErrorKindUnavailable, env.Error.Kind)
			require.Contains(t, env.Error.Message, errHomeRefusal.Error())
		})
	}
}

// TestRefused_HelpStillAnswers: help describes the command, not the project, so a refused root
// does not take it away.
func TestRefused_HelpStillAnswers(t *testing.T) {
	t.Parallel()

	for _, c := range commands.All(refusedDeps(t)) {
		var out bytes.Buffer
		require.NoError(t, c.Run(context.Background(), []string{"--help"}, &out), c.Name())
		require.Contains(t, out.String(), "usage:", c.Name())
	}
}

// TestRefused_StatusReportsTheRefusalAsItsOneReason: status still exits 0 and still renders a full
// report, with nothing observed and the refusal as the reason, in both forms, without asking either
// source.
func TestRefused_StatusReportsTheRefusalAsItsOneReason(t *testing.T) {
	t.Parallel()

	status := commandNamed(t, commands.All(refusedDeps(t)), "status")

	var text bytes.Buffer
	require.NoError(t, status.Run(context.Background(), nil, &text))
	require.Contains(t, text.String(), "source: none (unavailable")
	require.Contains(t, text.String(), errHomeRefusal.Error())

	var js bytes.Buffer
	require.NoError(t, status.Run(context.Background(), []string{"--json"}, &js))
	env, err := commands.DecodeEnvelope(js.Bytes())
	require.NoError(t, err)
	require.True(t, env.OK, "status reports; it does not fail")
	var rep commands.StatusReport
	require.NoError(t, json.Unmarshal(env.Data, &rep))
	require.Equal(t, commands.SourceNone, rep.Primary.Source)
	require.Equal(t, commands.AvailabilityUnavailable, rep.Primary.Status)
	require.Equal(t, errHomeRefusal.Error(), rep.Primary.Reason)
	require.Nil(t, rep.Snapshot)
	require.NotEmpty(t, rep.Hooks, "every hook row still renders")
}

// TestRefused_CollectStatusAsksNoSource is the collector half, which doctor uses directly.
func TestRefused_CollectStatusAsksNoSource(t *testing.T) {
	t.Parallel()

	src := refusedDeps(t).Status
	src.Refused = errHomeRefusal
	rep := commands.CollectStatus(context.Background(), src, refusedAt)
	require.Equal(t, commands.SourceNone, rep.Primary.Source)
	require.Equal(t, commands.AvailabilityUnavailable, rep.Primary.Status)
	require.Equal(t, errHomeRefusal.Error(), rep.Primary.Reason)
	require.NotEmpty(t, rep.Budgets, "the budget rows still render, each unavailable")
	for _, b := range rep.Budgets {
		require.NotEqual(t, commands.AvailabilityOK, b.Provenance.Status, "budget %s", b.ID)
	}
}

// TestRefused_EvalStillReadsItsArtifacts: eval reads a completed evaluation's artifacts and never a
// project, so the refusal does not gate it. With no artifacts bound it gives its own answer.
func TestRefused_EvalStillReadsItsArtifacts(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := commandNamed(t, commands.All(refusedDeps(t)), "eval").Run(context.Background(), nil, &out)
	require.ErrorIs(t, err, commands.ErrUnavailable)
	require.NotErrorIs(t, err, errHomeRefusal, "eval answered for itself")
}

// commandNamed returns the command called name from cmds.
func commandNamed(t *testing.T, cmds []commands.Command, name string) commands.Command {
	t.Helper()
	for _, c := range cmds {
		if c.Name() == name {
			return c
		}
	}
	require.FailNow(t, "no command named "+name)
	return nil
}
