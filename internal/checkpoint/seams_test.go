package checkpoint_test

// Branch coverage for the seam functions' failure paths. The happy paths live in schema_test.go,
// migrate_test.go and source_test.go; what is asserted here is what those functions do when the
// input is wrong, which is the half that only matters in production.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
	"github.com/stretchr/testify/require"
)

// TestSourceSetValidateNamesEveryMissingField walks the whole field list, not just the first one.
//
// Naming the field is the entire value of this function: a SourceSet is assembled by the daemon
// from seven independently-constructed seams, and "SourceSet is incomplete" would send a reader
// looking at all seven. The walk is asserted field by field because a Validate that returned early
// on Store -- or that named the wrong field -- would pass a test that only ever omitted one.
func TestSourceSetValidateNamesEveryMissingField(t *testing.T) {
	full := checkpoint.SourceSet{
		Store:    stubStoreForValidate{},
		Segments: stubSegmentsForValidate{},
		Ledger:   stubLedgerForValidate{},
		Pins:     stubPinsForValidate{},
		Graph:    stubGraphForValidate{},
		Grammar:  grammar.New(),
		Tokens:   stubTokensForValidate{},
	}
	require.NoError(t, full.Validate(), "fixture sanity: the complete set validates")

	for _, tc := range []struct {
		field string
		blank func(s *checkpoint.SourceSet)
	}{
		{"Store", func(s *checkpoint.SourceSet) { s.Store = nil }},
		{"Segments", func(s *checkpoint.SourceSet) { s.Segments = nil }},
		{"Ledger", func(s *checkpoint.SourceSet) { s.Ledger = nil }},
		{"Pins", func(s *checkpoint.SourceSet) { s.Pins = nil }},
		{"Graph", func(s *checkpoint.SourceSet) { s.Graph = nil }},
		{"Grammar", func(s *checkpoint.SourceSet) { s.Grammar = nil }},
		{"Tokens", func(s *checkpoint.SourceSet) { s.Tokens = nil }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			s := full
			tc.blank(&s)
			err := s.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.field,
				"Validate must name the field that is missing, not merely report that one is")
		})
	}
}

func TestMigrateRejectsMalformedJSON(t *testing.T) {
	_, err := checkpoint.Migrate([]byte("{this is not json"))
	require.Error(t, err)
	require.ErrorIs(t, err, core.ErrContract)
	// A file that is not JSON at all and a JSON document with no version key are different
	// corruptions with different causes, and a log that called both "missing version" would send
	// the reader looking for a key in a file that has no keys.
	require.Contains(t, err.Error(), "malformed JSON")
	require.NotContains(t, err.Error(), "missing version")
}

func TestMigrateRejectsVersionBelowOne(t *testing.T) {
	for _, v := range []int{0, -1} {
		raw, err := json.Marshal(map[string]any{"version": v})
		require.NoError(t, err)

		_, mErr := checkpoint.Migrate(raw)
		require.Error(t, mErr)
		require.ErrorIs(t, mErr, core.ErrContract)
		require.Contains(t, mErr.Error(), "invalid version",
			"a present-but-nonsensical version is neither missing nor from the future")
	}
}

func TestUnmarshalRejectsAMigrationFailure(t *testing.T) {
	// Unmarshal runs Migrate first, so every Migrate rejection has to survive the wrapper rather
	// than being swallowed into a generic decode error.
	_, err := checkpoint.Unmarshal([]byte(`{"version": 9999}`))
	require.Error(t, err)
	require.ErrorIs(t, err, core.ErrContract)
	require.Contains(t, err.Error(), "newer plugin")
}

func TestUnmarshalToleratesUnknownFields(t *testing.T) {
	// A document written by a newer plugin must degrade to a partial read, not an error: refusing
	// it would turn a forward-compatible schema change into a session that cannot resume at all.
	raw := []byte(`{"version":1,"session":"s","seq":3,"a_field_from_the_future":{"x":1}}`)
	cp, err := checkpoint.Unmarshal(raw)
	require.NoError(t, err)
	require.Equal(t, core.CheckpointSeq(3), cp.Seq)
	require.Equal(t, core.SessionID("s"), cp.Session)
}

func TestBeginSetsAsideADraftWhoseSeqIsAlreadyClaimed(t *testing.T) {
	f := newFx(t)
	f.prompt(0, "Start the investigation.", true)
	f.closedSeg(1, 0, 3)
	d := f.begin()
	f.advance(d, 1)
	ref, err := f.w.Finalize(f.ctx(), d, finalizeBudget)
	require.NoError(t, err)

	// Retire the successor Finalize opened FIRST -- Abort deletes state/draft-<session>.json, so
	// writing the file before it would simply hand the test's own fixture to the unlink.
	wire, _ := f.persisted()
	require.NoError(t, f.w.Abort(f.w.DraftFor(f.sess)))

	// Now plant a draft re-claiming the sequence number that was just sealed. Resuming it would
	// produce a second checkpoint numbered the same as an immutable one already on disk.
	wire.Seq = ref.Seq
	raw, err := json.Marshal(wire)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(f.draftPath()), raw, 0o600))

	resumed, err := f.w.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(t, err)
	require.Greater(t, int(resumed.Seq()), int(ref.Seq),
		"a claimed seq is never resumed; the draft takes the next free number")

	stale := filepath.Join(paths.Of(f.p.Root).State, "draft-"+string(f.sess)+".stale.json")
	require.FileExists(t, paths.Long(stale),
		"the displaced draft is set aside rather than deleted, so its content is still recoverable")
}

func TestBeginIgnoresADraftFileItCannotDecode(t *testing.T) {
	f := newFx(t)
	require.NoError(t, os.MkdirAll(paths.Long(paths.Of(f.p.Root).State), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(f.draftPath()), []byte("{not json"), 0o600))

	// An undecodable draft is a fresh start, not a failure: refusing to open one would mean a
	// single corrupt scratch file stopped the session from ever checkpointing again.
	d, err := f.w.Begin(f.ctx(), f.sess, 0, f.src)
	require.NoError(t, err)
	require.NotNil(t, d)
	require.Equal(t, core.TurnIndex(0), d.Frontier())
}

func TestBeginRejectsAnIncompleteSourceSet(t *testing.T) {
	f := newFx(t)
	_, err := f.w.Begin(f.ctx(), f.sess, 0, checkpoint.SourceSet{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Store")
}

func TestAbortIsIdempotent(t *testing.T) {
	f := newFx(t)
	f.closedSeg(1, 0, 3)
	d := f.begin()

	require.NoError(t, f.w.Abort(d))
	require.NoFileExists(t, paths.Long(f.draftPath()))
	// A second Abort must not error: the caller that aborts on a shutdown path cannot know whether
	// an earlier one already ran, and an error there would be reported as a shutdown failure.
	require.NoError(t, f.w.Abort(d))
}

// The stubs below exist only to give SourceSet.Validate seven distinct non-nil fields. They are
// never called: Validate checks for nil and nothing else.

type stubStoreForValidate struct{ store.Store }

type stubSegmentsForValidate struct{ store.SegmentLog }

type stubLedgerForValidate struct{ negknow.Ledger }

type stubPinsForValidate struct{ pins.Store }

type stubGraphForValidate struct{ dag.Graph }

type stubTokensForValidate struct{ tokens.Estimator }
