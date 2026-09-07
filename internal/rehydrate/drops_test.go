package rehydrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The Reporter is the only part of L5 that outlives the injection it describes. Section 7 of the
// payload is budgeted and may be cut to a counted line; this file never is, and the `dropped`
// retrieval tool reads it. Everything below is about the difference between "the agent can see
// what it lost" and "the agent can ask what it lost".

// stateFileOf is the path the reporter writes for sess, spelled independently of statePath so a
// change to the naming scheme fails here rather than passing by construction.
func stateFileOf(t *testing.T, root string, sess core.SessionID) string {
	t.Helper()
	return filepath.Join(paths.Of(root).State, "rehydrate-"+sanitizeSession(sess)+".json")
}

func TestReporter_RecordThenCurrentDrops(t *testing.T) {
	root := t.TempDir()
	log := &spyLogger{}
	r := NewReporter(root, log)
	ctx := context.Background()

	want := []checkpoint.DropEntry{
		{Kind: "path_rule", ID: ".claude/rules/testing-conventions.md", Detail: "matched src/auth.ts; did not fit the rehydration budget"},
		{Kind: "skill", ID: "why-flaky", Detail: "not in the compact skill index (budget 450 tokens)"},
	}
	require.NoError(t, r.Record(ctx, "sess_a", State{
		Session: "sess_a", Seq: 7, Emitted: core.UnixMilli(1767225600000),
		Tokens: 8034, Budget: 12000, Dropped: want,
	}))

	got, err := r.CurrentDrops(ctx, "sess_a")
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Zero(t, log.loud, "a healthy round trip must be silent")

	// The file is written under .qompack/state with owner-only permissions, and it is indented
	// JSON with a trailing newline — it is read by humans during an incident as often as by the
	// `dropped` tool.
	b, err := os.ReadFile(paths.Long(stateFileOf(t, root, "sess_a")))
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(string(b), "}\n"), "state file must end in exactly one newline")
	require.Contains(t, string(b), "\n  \"seq\": 7,", "state file must be indented")
}

func TestReporter_NeverRehydratedIsNotAnError(t *testing.T) {
	log := &spyLogger{}
	r := NewReporter(t.TempDir(), log)

	got, err := r.CurrentDrops(context.Background(), "sess_unknown")
	require.NoError(t, err, "a session that has never been rehydrated is an answer, not a failure")
	require.Nil(t, got)
	require.Zero(t, log.loud, "an absent state file is the ordinary first-compaction case")
}

func TestReporter_EmptySlicesMarshalAsArrays(t *testing.T) {
	root := t.TempDir()
	r := NewReporter(root, &spyLogger{})
	require.NoError(t, r.Record(context.Background(), "sess_empty", State{Session: "sess_empty"}))

	b, err := os.ReadFile(paths.Long(stateFileOf(t, root, "sess_empty")))
	require.NoError(t, err)
	// A consumer ranging over these must not have to distinguish null from []; the golden is
	// byte-compared, so the choice is part of the file's shape rather than a formatting detail.
	require.Contains(t, string(b), `"items": []`)
	require.Contains(t, string(b), `"dropped": []`)
	require.NotContains(t, string(b), "null")
}

func TestReporter_CorruptStateIsDeletedAndLoudOnce(t *testing.T) {
	root := t.TempDir()
	log := &spyLogger{}
	r := NewReporter(root, log)
	ctx := context.Background()

	p := stateFileOf(t, root, "sess_bad")
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), []byte("{not json"), 0o600))

	got, err := r.CurrentDrops(ctx, "sess_bad")
	require.NoError(t, err, "a corrupt state file is a degraded observable, not a failure")
	require.Nil(t, got)
	require.Equal(t, 1, log.loud, "exactly one Loud for a corrupt state file")
	require.NoFileExists(t, paths.Long(p),
		"the corrupt file must be deleted so the next build rewrites a good one")

	// A repeatedly polled `dropped` tool must not turn one corrupt file into an unbounded log.
	require.NoError(t, os.WriteFile(paths.Long(p), []byte("{still not json"), 0o600))
	_, err = r.CurrentDrops(ctx, "sess_bad")
	require.NoError(t, err)
	require.Equal(t, 1, log.loud, "the Loud is once per session, not once per read")

	// Reset clears the once-marker as well as the file: a session that was reset has no history
	// for the suppression to be suppressing.
	require.NoError(t, r.Reset(ctx, "sess_bad"))
	require.NoError(t, os.WriteFile(paths.Long(p), []byte("{nope"), 0o600))
	_, err = r.CurrentDrops(ctx, "sess_bad")
	require.NoError(t, err)
	require.Equal(t, 2, log.loud, "after a Reset the next corruption is Loud again")
}

func TestReporter_ResetDeletesAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	r := NewReporter(root, &spyLogger{})
	ctx := context.Background()

	require.NoError(t, r.Record(ctx, "sess_c", State{
		Session: "sess_c", Dropped: []checkpoint.DropEntry{{Kind: "skill", ID: "onboard"}},
	}))
	require.FileExists(t, paths.Long(stateFileOf(t, root, "sess_c")))

	// This is the /clear branch: the next compact injection must be a fresh full payload rather
	// than one carrying the previous session's drop report.
	require.NoError(t, r.Reset(ctx, "sess_c"))
	require.NoFileExists(t, paths.Long(stateFileOf(t, root, "sess_c")))
	got, err := r.CurrentDrops(ctx, "sess_c")
	require.NoError(t, err)
	require.Nil(t, got)

	require.NoError(t, r.Reset(ctx, "sess_c"), "a missing state file is a successful reset")
	require.NoError(t, r.Reset(ctx, "sess_never_seen"))
}

func TestReporter_CancelledContextIsReported(t *testing.T) {
	r := NewReporter(t.TempDir(), &spyLogger{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.ErrorIs(t, r.Record(ctx, "sess_x", State{}), context.Canceled)
	_, err := r.CurrentDrops(ctx, "sess_x")
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, r.Reset(ctx, "sess_x"), context.Canceled)
}

func TestSanitizeSession(t *testing.T) {
	// A session id is host-supplied, so it is untrusted input on a path. The mapping is
	// deliberately lossy and deliberately not reversible: a collision costs one stale drop
	// report, where admitting a separator would cost a write outside .qompack/.
	require.Equal(t, "sess_01J8ZQ5R7N3K", sanitizeSession("sess_01J8ZQ5R7N3K"))
	require.Equal(t, "a.b-c_d", sanitizeSession("a.b-c_d"))
	// '.' survives, so ".." does too — and that is safe rather than sloppy: what makes a traversal
	// is the SEPARATOR, and both separators are replaced. ".._.._etc" is one file name inside the
	// state directory, not a path out of it.
	require.Equal(t, ".._.._etc", sanitizeSession("../../etc"))
	require.NotContains(t, sanitizeSession("../../etc"), "/")
	require.Equal(t, "a_b", sanitizeSession("a/b"))
	require.Equal(t, "a_b", sanitizeSession(`a\b`))
	require.Equal(t, "_", sanitizeSession(""), "an empty id must still name a file")
	require.Equal(t, "__x", sanitizeSession("é☃x"),
		"a multi-byte rune is replaced by ONE underscore, not by one per byte")

	long := core.SessionID(strings.Repeat("z", maxSessionFileRunes+40))
	require.Len(t, sanitizeSession(long), maxSessionFileRunes, "the name is bounded")
}

func TestNewReporter_NilLoggerIsTolerated(t *testing.T) {
	root := t.TempDir()
	r := NewReporter(root, nil)
	require.NotNil(t, r)
	require.NotPanics(t, func() {
		_ = r.Record(context.Background(), "sess_nil", State{Session: "sess_nil"})
		_, _ = r.CurrentDrops(context.Background(), "sess_nil")
	})
}

// TestItemStats_OneRowPerEmittedItem pins the projection Result.Tokens's identity depends on:
// exactly one row per emitted Item and no synthetic overhead row, so that the sum over rows in the
// state file equals the total exactly as it does in the Result.
func TestItemStats_OneRowPerEmittedItem(t *testing.T) {
	items := []Item{
		{Kind: ItemInvariants, Rank: 0, Tokens: 112},
		{Kind: ItemDropReport, Rank: 1, Tokens: 294, Truncated: true},
	}
	rows := itemStats(items,
		map[ItemKind]int{ItemInvariants: 2, ItemDropReport: 12},
		map[ItemKind]int{ItemInvariants: 2, ItemDropReport: 12})

	require.Len(t, rows, len(items))
	var sum core.Tokens
	for i, r := range rows {
		require.Equal(t, items[i].Kind.String(), r.Kind)
		require.Equal(t, items[i].Rank, r.Rank)
		require.Equal(t, items[i].Truncated, r.Truncated)
		sum += r.Tokens
	}
	require.Equal(t, core.Tokens(112+294), sum)
}
