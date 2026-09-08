package commands_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/pins"
)

// fakePins is an append-only pins.Store, which is the property the tests here care about: Remove
// appends a tombstone rather than deleting, so the add record and who made it survive.
type fakePins struct {
	log   []string
	live  map[string]pins.Invariant
	addWr error
}

func newFakePins(seed ...pins.Invariant) *fakePins {
	f := &fakePins{live: map[string]pins.Invariant{}}
	for _, s := range seed {
		f.live[s.ID] = s
		f.log = append(f.log, "add "+s.ID+" "+s.Source)
	}
	return f
}

func (f *fakePins) Add(_ context.Context, inv pins.Invariant) error {
	if f.addWr != nil {
		return f.addWr
	}
	f.log = append(f.log, "add "+inv.ID+" "+inv.Source)
	// pins.Store.Add is idempotent by id: re-adding a live invariant appends nothing and, in
	// particular, does not rewrite the recorded source.
	if _, ok := f.live[inv.ID]; ok {
		return nil
	}
	f.live[inv.ID] = inv
	return nil
}

func (f *fakePins) Remove(_ context.Context, id string) error {
	if _, ok := f.live[id]; !ok {
		return core.ErrNotFound
	}
	f.log = append(f.log, "tombstone "+id)
	delete(f.live, id)
	return nil
}

func (f *fakePins) All(context.Context) ([]pins.Invariant, error) {
	out := make([]pins.Invariant, 0, len(f.live))
	for _, v := range f.live {
		out = append(out, v)
	}
	// Deterministic order, matching the real store's Pinned-then-ID rule closely enough for these
	// cases, which never seed two pins with the same timestamp.
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].ID < out[i].ID {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func (f *fakePins) Materialize(context.Context) error { return nil }

func depsWithPins(p pins.Store) commands.Deps {
	d := testDeps()
	d.Pins = p
	return d
}

// TestPin_AddRecordsUserAuthorityByDefault: the slash command is something a person types, so its
// default authority is the user's.
func TestPin_AddRecordsUserAuthorityByDefault(t *testing.T) {
	t.Parallel()

	p := newFakePins()
	out, err := runWith(t, depsWithPins(p), "pin", "the API is versioned")
	require.NoError(t, err)
	require.Contains(t, out, "source: user")

	all, _ := p.All(context.Background())
	require.Len(t, all, 1)
	require.Equal(t, "user", all[0].Source)
	require.Equal(t, "the API is versioned", all[0].Text)
	require.Equal(t, pins.MintID("the API is versioned"), all[0].ID)
}

// TestPin_AgentClaimIsNotUpgradedToAUserInstruction is the authority rule.
//
// §8.3 lets a user correction outrank an agent claim. If re-running the command could rewrite an
// agent pin's source to "user", an agent that shelled out twice would be able to promote its own
// authority — the one upgrade this frontend must never perform.
func TestPin_AgentClaimIsNotUpgradedToAUserInstruction(t *testing.T) {
	t.Parallel()

	const text = "retries are capped at three"
	p := newFakePins(pins.Invariant{ID: pins.MintID(text), Text: text, Source: "agent"})

	out, err := runWith(t, depsWithPins(p), "pin", text)
	require.NoError(t, err)
	require.Contains(t, out, "already pinned")
	require.Contains(t, out, "source stands")

	all, _ := p.All(context.Background())
	require.Len(t, all, 1)
	require.Equal(t, "agent", all[0].Source, "the recorded authority must not have been raised")
}

// TestPin_SourceFlagIsValidated keeps an unrecognized authority out of the log entirely, rather
// than storing a third value the §8.3 ordering has no rule for.
func TestPin_SourceFlagIsValidated(t *testing.T) {
	t.Parallel()

	p := newFakePins()
	_, err := runWith(t, depsWithPins(p), "pin", "--source", "root", "x")
	require.ErrorIs(t, err, commands.ErrUsage)
	require.Empty(t, p.log)

	_, err = runWith(t, depsWithPins(p), "pin", "--source", "agent", "x")
	require.NoError(t, err)
}

// TestPin_ListReportsSourcePerInvariant keeps the authority visible where a user reads it, not
// only where the store keeps it.
func TestPin_ListReportsSourcePerInvariant(t *testing.T) {
	t.Parallel()

	p := newFakePins(
		pins.Invariant{ID: "inv_aaa", Text: "from the user", Source: "user"},
		pins.Invariant{ID: "inv_bbb", Text: "from the agent", Source: "agent"},
	)

	out, err := runWith(t, depsWithPins(p), "pin", "--list")
	require.NoError(t, err)
	require.Contains(t, out, "inv_aaa")
	require.Contains(t, out, "user")
	require.Contains(t, out, "inv_bbb")
	require.Contains(t, out, "agent")

	jsonOut, err := runWith(t, depsWithPins(p), "pin", "--list", "--json")
	require.NoError(t, err)
	env, err := commands.DecodeEnvelope([]byte(jsonOut))
	require.NoError(t, err)

	var list commands.PinList
	require.NoError(t, json.Unmarshal(env.Data, &list))
	require.Len(t, list.Invariants, 2)
	require.Equal(t, "user", list.Invariants[0].Source)
}

// TestPin_RemoveAppendsATombstoneAndKeepsTheAddRecord is the add/remove history requirement.
func TestPin_RemoveAppendsATombstoneAndKeepsTheAddRecord(t *testing.T) {
	t.Parallel()

	p := newFakePins(pins.Invariant{ID: "inv_aaa", Text: "x", Source: "user"})

	out, err := runWith(t, depsWithPins(p), "pin", "--remove", "inv_aaa")
	require.NoError(t, err)
	require.Contains(t, out, "retained")

	require.Equal(t, []string{"add inv_aaa user", "tombstone inv_aaa"}, p.log,
		"the add record survives the removal")
}

// TestPin_RemoveUnknownIDSaysSo keeps a typo from reading as a successful withdrawal.
func TestPin_RemoveUnknownIDSaysSo(t *testing.T) {
	t.Parallel()

	_, err := runWith(t, depsWithPins(newFakePins()), "pin", "--remove", "inv_nope")
	require.Error(t, err)
	require.Contains(t, err.Error(), "inv_nope")
	require.Equal(t, commands.ExitError, commands.ExitCode(err))
}

// TestPin_ModesAreExclusive: two modes at once would silently pick one.
func TestPin_ModesAreExclusive(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--list", "--remove", "inv_a"},
		{"--list", "--eliminated"},
		{"--list", "some text"},
	} {
		_, err := runWith(t, depsWithPins(newFakePins()), "pin", args...)
		require.ErrorIs(t, err, commands.ErrUsage, "%v", args)
	}
}

// TestPin_NoStoreIsUnavailable keeps a missing store from reading as an empty pin set.
func TestPin_NoStoreIsUnavailable(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"text"}, {"--list"}, {"--remove", "inv_a"}} {
		_, err := runWith(t, testDeps(), "pin", args...)
		require.ErrorIs(t, err, commands.ErrUnavailable, "%v", args)
	}
}

// TestPin_EliminationRequiresItsDependencies is the staleness guard.
//
// §8.3 expires an elimination when the files it rests on change. One recorded with no dependencies
// can never expire, so it goes on blocking an approach long after that approach has become
// viable. The command refuses to record one rather than making it convenient.
func TestPin_EliminationRequiresItsDependencies(t *testing.T) {
	t.Parallel()

	var rec recorder
	d := depsWith(serverWith(t, mcp.ToolRecordEliminated, &rec, mcp.Response{}, nil))

	_, err := runWith(t, d, "pin", "--eliminated",
		"--target", "the parser", "--approach", "regex", "--reason", "nested quotes")
	require.ErrorIs(t, err, commands.ErrUsage)
	require.Contains(t, err.Error(), "never go stale")
	require.Empty(t, rec.seen, "an elimination that cannot expire must not reach the ledger")
}

// TestPin_EliminationGoesThroughTheSP13Handler keeps the write side on one implementation, so the
// validation and the dependency resolution the handler already does are not duplicated here.
func TestPin_EliminationGoesThroughTheSP13Handler(t *testing.T) {
	t.Parallel()

	var rec recorder
	d := depsWith(serverWith(t, mcp.ToolRecordEliminated, &rec, mcp.Response{
		Content: []mcp.Content{{Type: "text", Text: `{"recorded":true}`}},
	}, nil))

	_, err := runWith(t, d, "pin", "--eliminated",
		"--target", "the parser", "--approach", "regex", "--reason", "nested quotes",
		"--depends-on", "internal/parse/lex.go, internal/parse/lex_test.go",
		"--scope", "project")
	require.NoError(t, err)

	require.Len(t, rec.seen, 1)
	var args mcp.RecordEliminatedArgs
	require.NoError(t, json.Unmarshal(rec.seen[0].Args, &args))
	require.Equal(t, "the parser", args.Target)
	require.Equal(t, "regex", args.Approach)
	require.Equal(t, "nested quotes", args.Reason)
	require.Equal(t, "project", args.Scope)
	require.Equal(t, []string{"internal/parse/lex.go", "internal/parse/lex_test.go"}, args.DependsOn,
		"the list is split and trimmed, not passed through as one string")
}

// TestPin_EliminationRequiresEveryClaimField keeps a half-stated elimination out of the ledger.
func TestPin_EliminationRequiresEveryClaimField(t *testing.T) {
	t.Parallel()

	base := []string{"--eliminated", "--depends-on", "a.go"}
	for _, missing := range []struct {
		name string
		args []string
	}{
		{"target", []string{"--approach", "a", "--reason", "r"}},
		{"approach", []string{"--target", "t", "--reason", "r"}},
		{"reason", []string{"--target", "t", "--approach", "a"}},
	} {
		var rec recorder
		d := depsWith(serverWith(t, mcp.ToolRecordEliminated, &rec, mcp.Response{}, nil))
		_, err := runWith(t, d, "pin", append(append([]string{}, base...), missing.args...)...)
		require.ErrorIs(t, err, commands.ErrUsage, "missing --%s", missing.name)
		require.Contains(t, err.Error(), missing.name)
	}
}

// TestCheckpoint_NeverRequestsNativeCompaction is the SP14-M3-01 gate.
//
// §7.1 makes Qompack a sidecar: it observes the host's compaction and never drives it. The result
// records that explicitly so an audit can confirm it from the record rather than from the code.
func TestCheckpoint_NeverRequestsNativeCompaction(t *testing.T) {
	t.Parallel()

	var gotReason string
	d := testDeps()
	d.CheckpointNow = func(_ context.Context, reason string) (commands.CheckpointResult, error) {
		gotReason = reason
		return commands.CheckpointResult{Outcome: commands.CheckpointSealed, Seq: 12}, nil
	}

	out, err := runWith(t, d, "checkpoint", "--reason", "before the refactor", "--json")
	require.NoError(t, err)
	require.Equal(t, "before the refactor", gotReason)

	env, err := commands.DecodeEnvelope([]byte(out))
	require.NoError(t, err)

	var res commands.CheckpointResult
	require.NoError(t, json.Unmarshal(env.Data, &res))
	require.False(t, res.RequestedNativeCompaction)
	require.Equal(t, commands.CheckpointSealed, res.Outcome)
	require.Equal(t, core.CheckpointSeq(12), res.Seq)
	require.Equal(t, "before the refactor", res.Reason)
}

// TestCheckpoint_TextOutputSaysItDroveNothing puts the same fact where a person reads it.
func TestCheckpoint_TextOutputSaysItDroveNothing(t *testing.T) {
	t.Parallel()

	d := testDeps()
	d.CheckpointNow = func(context.Context, string) (commands.CheckpointResult, error) {
		return commands.CheckpointResult{Outcome: commands.CheckpointSealed, Seq: 3}, nil
	}

	out, err := runWith(t, d, "checkpoint")
	require.NoError(t, err)
	require.Contains(t, out, "sealed checkpoint 3")
	require.Contains(t, out, "no native compaction was requested")
}

// TestCheckpoint_OutcomesStayDistinct keeps the lifecycle and overflow diagnostics explicit. A
// truncated checkpoint is not a clean one, an empty attempt is not a failure, and a failure is
// not a silent success.
func TestCheckpoint_OutcomesStayDistinct(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		res     commands.CheckpointResult
		err     error
		wantErr bool
		wants   []string
	}{
		{
			name:  "truncated",
			res:   commands.CheckpointResult{Outcome: commands.CheckpointTruncated, Seq: 9, Dropped: []string{"tier 3: 41 tool results"}},
			wants: []string{"dropped to fit", "tier 3: 41 tool results"},
		},
		{
			name:  "empty",
			res:   commands.CheckpointResult{Outcome: commands.CheckpointEmpty},
			wants: []string{"nothing to seal"},
		},
		{
			name:    "failed",
			res:     commands.CheckpointResult{Outcome: commands.CheckpointFailed, Detail: "segment already encoded"},
			wantErr: true,
			wants:   []string{"no checkpoint was written", "segment already encoded"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := testDeps()
			d.CheckpointNow = func(context.Context, string) (commands.CheckpointResult, error) {
				return tc.res, tc.err
			}

			out, err := runWith(t, d, "checkpoint")
			if tc.wantErr {
				require.Error(t, err)
				require.Equal(t, commands.ExitError, commands.ExitCode(err))
			} else {
				require.NoError(t, err)
			}
			for _, w := range tc.wants {
				require.Contains(t, out, w)
			}
		})
	}
}

// TestCheckpoint_TransportFailureStillReportsAndDoesNotClaimSuccess covers the case where the
// request itself could not be delivered.
func TestCheckpoint_TransportFailureStillReportsAndDoesNotClaimSuccess(t *testing.T) {
	t.Parallel()

	d := testDeps()
	d.CheckpointNow = func(context.Context, string) (commands.CheckpointResult, error) {
		return commands.CheckpointResult{Outcome: commands.CheckpointFailed}, errors.New("daemon closed the connection")
	}

	_, err := runWith(t, d, "checkpoint")
	require.Error(t, err)
	require.Contains(t, err.Error(), "daemon closed the connection")
}

// TestCheckpoint_WithoutARouteIsUnavailable is the H3 state: the frontend exists and its route
// does not, so it says so rather than reaching for the PreCompact path, which would record a claim
// that the HOST is about to compact when nothing of the sort happened.
func TestCheckpoint_WithoutARouteIsUnavailable(t *testing.T) {
	t.Parallel()

	out, err := runWith(t, testDeps(), "checkpoint")
	require.ErrorIs(t, err, commands.ErrUnavailable)
	require.Contains(t, err.Error(), "no checkpoint was requested")
	require.Empty(t, out)
}

// TestCheckpoint_RejectsPositionalArguments keeps `qompack checkpoint before the refactor` from
// looking like it recorded a reason it silently discarded.
func TestCheckpoint_RejectsPositionalArguments(t *testing.T) {
	t.Parallel()

	d := testDeps()
	d.CheckpointNow = func(context.Context, string) (commands.CheckpointResult, error) {
		t.Fatal("must not reach the route")
		return commands.CheckpointResult{}, nil
	}

	_, err := runWith(t, d, "checkpoint", "before", "the", "refactor")
	require.ErrorIs(t, err, commands.ErrUsage)
	require.Contains(t, err.Error(), "--reason")
}
