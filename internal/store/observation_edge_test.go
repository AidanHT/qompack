package store

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Edge rows for observation intents that the publication tests leave unexecuted: the input checks
// every intent passes before anything is written, the preview redaction an intent shares with the
// index, and what recovery answers for a request it cannot resume — an ended caller, an id that is
// not one, an observation two intents claim, and an unknown one while completeness is unproved
// (w16b-cover, C3.6).

// TestReserveObservation_RefusesAnInvalidIntentBeforeWritingIt: a non-canonical observation id, a
// record with no id, more supersede targets than the format bounds, and a caller that gave up are all
// refused, and the sidecar is never created.
func TestReserveObservation_RefusesAnInvalidIntentBeforeWritingIt(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "intent content")
	obs := obsID(t, 1)
	rec := recWithObs("toolu_01OBSVALIDATEAAAAAAAAA", root, obs)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	tooMany := make([]core.ToolUseID, maxObservationSupersedes+1)

	require.ErrorIs(t, tp.Store.ReserveObservation(ctx, "not-an-observation", rec, nil), core.ErrContract)
	noID := rec
	noID.ID = ""
	require.ErrorIs(t, tp.Store.ReserveObservation(ctx, obs, noID, nil), core.ErrContract)
	require.ErrorIs(t, tp.Store.ReserveObservation(ctx, obs, rec, tooMany), core.ErrContract)
	require.ErrorIs(t, tp.Store.ReserveObservation(cancelled, obs, rec, nil), context.Canceled)
	require.NoFileExists(t, obsPath(tp.Root), "a refused intent writes nothing")
}

// TestReserveObservation_RedactsTheArgsPreview: an intent line carries the record's preview, so it is
// redacted exactly as the index line is; the secret never reaches the sidecar.
func TestReserveObservation_RedactsTheArgsPreview(t *testing.T) {
	const secret = "not-a-real-credential-0001"
	tp := newTestStore(t, withRedactor(fixedRedactor{secret: secret}))
	ctx := context.Background()
	root := putRoot(t, tp, "redacted intent content")
	obs := obsID(t, 1)
	rec := recWithObs("toolu_01OBSREDACTAAAAAAAAAAA", root, obs)
	rec.ArgsPreview = "deploy --token " + secret

	require.NoError(t, tp.Store.ReserveObservation(ctx, obs, rec, nil))
	b, err := os.ReadFile(paths.Long(obsPath(tp.Root)))
	require.NoError(t, err)
	require.NotContains(t, string(b), secret)
	require.Contains(t, string(b), "redacted:testdouble")
}

// TestRecoverToolUseByObservation_RefusesWhatItCannotResume: recovery resumes one stored intent. An
// ended caller and a malformed id resume nothing; an observation two intents claim is ambiguous for
// every reader and refuses a new reservation; and an observation the sidecar does not name is
// unavailable, not absent, while the sidecar's completeness is unproved.
func TestRecoverToolUseByObservation_RefusesWhatItCannotResume(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "recovery content")
	obs := obsID(t, 1)
	require.NoError(t, tp.Store.RecordToolUse(ctx, recWithObs("toolu_01OBSRECOVERAAAAAAAAAA", root, obs)))

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := tp.Store.RecoverToolUseByObservation(cancelled, obs)
	require.ErrorIs(t, err, context.Canceled)
	_, err = tp.Store.RecoverToolUseByObservation(ctx, "not-an-observation")
	require.ErrorIs(t, err, core.ErrNotFound)

	// A second canonical intent for the same observation, naming another record: two claims.
	b, err := os.ReadFile(paths.Long(obsPath(tp.Root)))
	require.NoError(t, err)
	var w observationIntentWire
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(b), &w))
	w.Rec.ID = "toolu_01OBSRIVALAAAAAAAAAAAA"
	rival, err := json.Marshal(w)
	require.NoError(t, err)
	require.NoError(t, tp.Store.Close())
	appendObsLine(t, tp.Root, string(rival))
	reopened := openOver(t, tp.project)

	_, err = reopened.Store.RecoverToolUseByObservation(ctx, obs)
	require.ErrorIs(t, err, core.ErrDegraded)
	require.ErrorContains(t, err, "more than one record")
	_, err = reopened.Store.ToolUseByObservation(ctx, obs)
	require.ErrorIs(t, err, core.ErrDegraded)
	err = reopened.Store.ReserveObservation(ctx, obs, recWithObs("toolu_01OBSTHIRDAAAAAAAAAAAA", root, obs), nil)
	require.ErrorIs(t, err, core.ErrDegraded)

	// A line no build reads makes completeness unprovable: an unknown observation is unavailable.
	require.NoError(t, reopened.Store.Close())
	appendObsLine(t, tp.Root, `{"v":999}`)
	uncertain := openOver(t, tp.project)
	_, err = uncertain.Store.RecoverToolUseByObservation(ctx, obsID(t, 42))
	require.ErrorIs(t, err, core.ErrDegraded)
	require.ErrorContains(t, err, "completeness unproved")
}
