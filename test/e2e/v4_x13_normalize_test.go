// The specification of x13v4Normalize, and the standing proof that narrowing three of its folds
// STRENGTHENED §4.13's write-set comparison instead of merely rearranging it.
//
// x13v4Normalize is not a formatting helper. It decides what
// TestV4_HotPathUnchangedWithTheFullWave3ResidentSet is allowed to notice, so every fold in it is a
// claim that two daemon arms may legitimately differ in that component. A fold that reaches past its
// claim does not make that row FLAKY — it makes it pass when it should fail, which is strictly worse,
// because that row's set comparison is the evidence that the wave-3 residents add no hot-path work.
// Three folds did reach past it:
//
//   - spool/ collapsed the daemon's own WAL, the hook's fallback client spool and a client
//     externalized blob into one token, so an arm that DEGRADED and an arm that did not compared
//     equal. That fallback is what carried defect SP05-D2 is about.
//   - logs/ collapsed LOUD.log — a fixed name with nothing per-run in it — with the dated day log
//     and the dated hook-quiet record, so an arm that went Loud compared equal to one that did not.
//   - state/ keyed on the draft- prefix alone, so draft-<session>.stale.json (the draft a losing
//     writer sets aside, internal/checkpoint/writer.go setAsideStaleDraft) compared equal to the
//     live draft-<session>.json.
//
// The tests below pin the new mappings and then demonstrate, at the write-set level the real row
// uses, that each of those three cases is now visible where it previously was not.
package e2e

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// x13v4NormalizeBeforeTightening is x13v4Normalize exactly as it stood before the three folds were
// narrowed. It is a WITNESS and nothing else: it makes "this change strengthens the comparison" a
// checked claim rather than a sentence in a commit message, by showing which pairs of real file
// names it maps together that the shipped fold now keeps apart.
//
// No production path and no other test calls it. If a fold is ever narrowed further, extend the pair
// table in TestV4_X13NormalizeSeparatesWhatItUsedToConflate; if one is ever WIDENED back, that test
// goes red, which is the whole point of keeping this copy.
func x13v4NormalizeBeforeTightening(name string) string {
	switch {
	case len(name) > 7 && name[:7] == "objects":
		return "objects/<shard>/<object>"
	case len(name) > len(x13v4CapturePrefix) && name[:len(x13v4CapturePrefix)] == x13v4CapturePrefix:
		return x13v4CapturePrefix + "<shard>/<observation>.json"
	case len(name) > 5 && name[:5] == "logs/":
		return "logs/<log>"
	case len(name) > 6 && name[:6] == "state/":
		base := filepath.Base(name)
		for _, pfx := range []string{"draft-", "rehydrate-"} {
			if len(base) > len(pfx) && base[:len(pfx)] == pfx {
				return "state/" + pfx + "<session>.json"
			}
		}
		return name
	case len(name) > 6 && name[:6] == "spool/":
		return "spool/<wal>"
	case len(name) > 4 && name[:4] == "tmp/":
		return "tmp/<staging>"
	default:
		return name
	}
}

// TestV4_X13NormalizeFoldsOnlyThePerRunComponent pins what x13v4Normalize maps every name the
// product actually writes to. Each case names the writer, because a fold is only defensible against
// the shape the writer emits — the spellings here were read off those writers, not guessed.
func TestV4_X13NormalizeFoldsOnlyThePerRunComponent(t *testing.T) {
	for _, tc := range []struct {
		name, in, want, writer string
	}{
		// Content-addressed trees: the whole name is per-run.
		{
			"object", "objects/ab/abcd1234ef", "objects/<shard>/<object>",
			"internal/store, content-addressed object shards",
		},
		{
			"capture sidecar", "records/captures/ab/abcd1234.json",
			x13v4CapturePrefix + "<shard>/<observation>.json",
			"internal/store/capture_sidecar.go, WriteCaptureSidecar",
		},

		// logs/: one fixed name, two dated families, and the dated families stay apart.
		{
			"LOUD is verbatim", x13v4LoudLog, x13v4LoudLog,
			"internal/logging/logger.go, loudFileName — opened once, never rotated",
		},
		{
			"day log", "logs/qompack-20260913.log", x13v4DayLogPrefix + "<date>.log",
			"internal/logging/logger.go, sink.currentPath",
		},
		{
			"rotated day log", "logs/qompack-20260913.1.log", x13v4DayLogPrefix + "<date>.log",
			"internal/logging/logger.go, sink.rotatedPath",
		},
		{
			"hook-quiet record", "logs/hook-quiet-20260913.jsonl",
			x13v4HookQuietPrefix + "<date>.jsonl", "internal/cli/hookclient.go, logQuiet",
		},

		// state/: the session id folds, the suffix that says WHICH file this is does not.
		{
			"live draft", "state/draft-sess-e2e-v4-x13.json", x13v4DraftPrefix + "<session>.json",
			"internal/checkpoint/writer.go, draftPathFor",
		},
		{
			"set-aside draft", "state/draft-sess-e2e-v4-x13.stale.json",
			x13v4DraftPrefix + "<session>" + x13v4StaleSuffix,
			"internal/checkpoint/writer.go, setAsideStaleDraft",
		},
		{
			"rehydrate drops", "state/rehydrate-sess-e2e-v4-x13.json",
			x13v4RehydratePrefix + "<session>.json", "internal/rehydrate/drops.go, dropsPath",
		},
		{
			"drain state is fixed", "state/drain.json", "state/drain.json",
			"internal/daemon/drain.go, drainStateFile",
		},
		{
			"retention roots are fixed", "state/retention-roots.jsonl", "state/retention-roots.jsonl",
			"internal/store/lifecycle.go, retentionRootsFile",
		},
		{
			"a bare draft- prefix is not folded", "state/draft-.json", "state/draft-.json",
			"no writer emits this; an unrecognized shape stays verbatim rather than being guessed at",
		},

		// spool/: three families that live in one directory and mean three different things.
		{
			"WAL segment", "spool/wal-sess-e2e-v4-x13.ndjson", "spool/<wal>",
			"internal/daemon/ingest.go, walPath at seq 0",
		},
		{
			"rotated WAL segment", "spool/wal-sess-e2e-v4-x13.2.ndjson", "spool/<wal>",
			"internal/daemon/ingest.go, walPath at seq > 0",
		},
		{
			"client fallback spool", "spool/client-4242.ndjson", "spool/<client>",
			"internal/ipc/spool.go, newSpool — written only when the ACK wait gave up",
		},
		{
			"client blob", "spool/blob-4242-0.bin", "spool/<blob>",
			"internal/ipc/client.go, externalize",
		},

		// Everything else.
		{
			"staging file", "tmp/wa-0a1b2c3d", "tmp/<staging>",
			"internal/paths/atomic.go, tempFilePrefix",
		},
		{
			"index is fixed", "index/tool_use.jsonl", "index/tool_use.jsonl",
			"internal/store/tooluseindex.go",
		},
		{"run state is fixed", "run/state.bin", "run/state.bin", "internal/ipc/state.go, stateFileName"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, x13v4Normalize(tc.in),
				"%s is written by %s; the fold must erase its per-run component and nothing else",
				tc.in, tc.writer)
		})
	}
}

// x13v4ConflatedPair is one pair of real file names that the old fold mapped to a single token and
// the shipped fold keeps apart, plus what an arm that wrote the second one has actually done.
type x13v4ConflatedPair struct {
	name, baseline, planted, meaning string
}

// x13v4ConflatedPairs is the tightening's whole subject: every case §4.13's comparison used to be
// blind to. Both tests below drive it — one at the normalizer, one at the write-set level.
var x13v4ConflatedPairs = []x13v4ConflatedPair{
	{
		name:     "the hook's fallback spool beside the daemon's WAL",
		baseline: "spool/wal-sess-e2e-v4-x13.ndjson",
		planted:  "spool/client-4242.ndjson",
		meaning: "the client gave up waiting for the ACK and spooled the delivery itself — the arm " +
			"DEGRADED (carried defect SP05-D2)",
	},
	{
		name:     "a client-externalized blob beside the daemon's WAL",
		baseline: "spool/wal-sess-e2e-v4-x13.ndjson",
		planted:  "spool/blob-4242-0.bin",
		meaning:  "a request was too large for a frame and its payload went to a side file",
	},
	{
		name:     "a LOUD line beside the day log",
		baseline: "logs/qompack-20260913.log",
		planted:  x13v4LoudLog,
		meaning:  "something in the arm was Loud — §12.3's loudest signal",
	},
	{
		name:     "a quiet hook failure beside the day log",
		baseline: "logs/qompack-20260913.log",
		planted:  "logs/hook-quiet-20260913.jsonl",
		meaning:  "a hook could not reach the daemon at all and recorded that quietly",
	},
	{
		name:     "a set-aside draft beside the live one",
		baseline: "state/draft-sess-e2e-v4-x13.json",
		planted:  "state/draft-sess-e2e-v4-x13.stale.json",
		meaning:  "a draft lost its sequence to another writer and was set aside",
	},
}

// TestV4_X13NormalizeSeparatesWhatItUsedToConflate is the strengthening proof at the normalizer.
//
// For each pair it requires BOTH halves: the old fold really did map the two names onto one token
// (so the pair is a genuine blind spot and not a strawman), and the shipped fold maps them apart.
func TestV4_X13NormalizeSeparatesWhatItUsedToConflate(t *testing.T) {
	for _, p := range x13v4ConflatedPairs {
		t.Run(p.name, func(t *testing.T) {
			require.Equal(t, x13v4NormalizeBeforeTightening(p.baseline),
				x13v4NormalizeBeforeTightening(p.planted),
				"this pair is only worth a test because the OLD fold mapped %s and %s onto one token; "+
					"if it no longer does, the pair belongs somewhere else", p.baseline, p.planted)

			require.NotEqual(t, x13v4Normalize(p.baseline), x13v4Normalize(p.planted),
				"%s and %s must fold apart: the second means %s, and §4.13's comparison is the evidence "+
					"that no such thing happened on the wave-3 arm", p.baseline, p.planted, p.meaning)
		})
	}
}

// TestV4_X13WriteSetSeesWhatItUsedToConflate is the same proof at the level §4.13 actually compares
// at: two project trees walked by the real x13v4WriteSet, differing by exactly one planted file.
//
// This is the half the normalizer test cannot show. x13v4WriteSet builds a SET, so a planted file
// whose token collides with a file already present disappears entirely — the two arms come back
// byte-identical and require.Equal passes on a difference that is really there. Every pair below is
// asserted twice over: equal under the old fold (the failure that would have been missed) and
// unequal under the shipped one, with the planted file's new token named.
func TestV4_X13WriteSetSeesWhatItUsedToConflate(t *testing.T) {
	for _, p := range x13v4ConflatedPairs {
		t.Run(p.name, func(t *testing.T) {
			baseline := x13v4WriteSet(t, x13v4PlantTree(t, p.baseline), nil)
			planted := x13v4WriteSet(t, x13v4PlantTree(t, p.baseline, p.planted), nil)

			require.Equal(t, x13v4FoldAll(baseline, x13v4NormalizeBeforeTightening),
				x13v4FoldAll(planted, x13v4NormalizeBeforeTightening),
				"refolding both arms the old way must hide %s again — that is the defect this row "+
					"documents; arms: %v vs %v", p.planted, baseline, planted)

			require.NotEqual(t, baseline, planted,
				"an arm that wrote %s must not compare equal to one that did not: %s\nbaseline: %v\nplanted:  %v",
				p.planted, p.meaning, baseline, planted)
			want := x13v4Normalize(p.planted)
			require.Contains(t, planted, want,
				"the planted %s must reach the write set as %q", p.planted, want)
			require.NotContains(t, baseline, want,
				"the arm that wrote no %s must not carry %q", p.planted, want)
		})
	}
}

// x13v4PlantTree builds a throwaway project whose .qompack holds exactly the named slash-relative
// files and nothing else, and returns its root. The files are written directly rather than through
// a daemon because the subject here is the FOLD, not the writers — the writers were read to get
// these names right, and TestV4_X13NormalizeFoldsOnlyThePerRunComponent records which one each came
// from.
func x13v4PlantTree(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	dot := paths.Of(root).Dot
	for _, n := range names {
		p := filepath.Join(dot, filepath.FromSlash(n))
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(p), []byte(n+"\n"), 0o600))
	}
	return root
}

// x13v4FoldAll re-folds an already-normalized write set through fold and returns the resulting set,
// deduplicated and sorted the way x13v4WriteSet builds its own. Folding a folded name is safe for
// the pairs above: the shipped fold leaves every token it produces for them in a shape the old fold
// treats the same way it treated the original name.
func x13v4FoldAll(names []string, fold func(string) string) []string {
	seen := map[string]bool{}
	for _, n := range names {
		seen[fold(n)] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// TestV4_X13NormalizeIsIdempotent guards the one property x13v4FoldAll leans on and that a reader of
// a failure message leans on too: a token the fold produces must be a fixed point, or a name could
// be folded twice into something neither writer ever emits.
func TestV4_X13NormalizeIsIdempotent(t *testing.T) {
	var tokens []string
	for _, p := range x13v4ConflatedPairs {
		tokens = append(tokens, x13v4Normalize(p.baseline), x13v4Normalize(p.planted))
	}
	tokens = append(tokens, x13v4Normalize("objects/ab/abcd1234ef"),
		x13v4Normalize("records/captures/ab/abcd1234.json"), x13v4Normalize("tmp/wa-0a1b2c3d"),
		x13v4Normalize("state/rehydrate-sess-e2e-v4-x13.json"))
	slices.Sort(tokens)
	for _, tok := range slices.Compact(tokens) {
		require.Equal(t, tok, x13v4Normalize(tok), "the fold must be a fixed point on its own output")
	}
}
