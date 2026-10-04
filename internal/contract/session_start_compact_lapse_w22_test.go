package contract_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

// Audit 2's finding #9 at the assertion itself (wave 22). The daemon-level rows, which drive the
// same sequences through the checkpoint, prompt, flush and session.start routes, are in
// internal/daemon/session_start_compact_lapse_w22_test.go.

// TestSourceCompact_ASessionThatWentOnReadsNotCompleted is #9: once the compacting session prompted
// or ended after its PreCompact (CompactStartLapsed), its next start that is no compaction reads
// precompact-not-completed: OK, nothing to judge, and the obligation is resolved. A compact start
// still reads compact.
func TestSourceCompact_ASessionThatWentOnReadsNotCompleted(t *testing.T) {
	contract.DeclareProducer(contract.CSessionStartSourceCompact)
	t.Cleanup(contract.ResetProducers)

	for _, source := range []string{"resume", "startup", "clear", "compact"} {
		t.Run(source, func(t *testing.T) {
			h := &contract.SessionHistory{
				AwaitingCompactStart: true, LastPrecompactSession: "sess-x", LastPrecompactTS: 100,
			}
			require.True(t, h.NoteCompactLapse("sess-x", 101))
			r := assertionByID(t, contract.CSessionStartSourceCompact).Check(context.Background(), contract.Env{
				Clock: newFakeClock(), History: h, Event: hookio.Event{SessionID: "sess-x", Source: source},
			})
			require.True(t, r.OK)
			if source == "compact" {
				require.Equal(t, "compact", r.Observed)
			} else {
				require.Equal(t, "precompact-not-completed", r.Observed)
				require.Equal(t, contract.StandingIdle, contract.StandingOf(r))
			}
			require.False(t, h.AwaitingCompactStart)
			require.False(t, h.CompactStartLapsed, "resolving the obligation clears its lapse")
		})
	}
}

// TestNoteCompactLapse_OnlyTheCompactingSessionAfterItsPreCompact pins what lapses an obligation:
// a hook of the session that armed it, fired after its PreCompact. Another session's, an earlier
// one delivered late, one without a time, and any hook while nothing is pending change nothing.
func TestNoteCompactLapse_OnlyTheCompactingSessionAfterItsPreCompact(t *testing.T) {
	armed := func() *contract.SessionHistory {
		return &contract.SessionHistory{AwaitingCompactStart: true, LastPrecompactSession: "sess-x", LastPrecompactTS: 100}
	}
	for name, tc := range map[string]struct {
		h    *contract.SessionHistory
		sess core.SessionID
		at   core.UnixMilli
		want bool
	}{
		"the session, after":     {armed(), "sess-x", 101, true},
		"the session, at":        {armed(), "sess-x", 100, false},
		"the session, before":    {armed(), "sess-x", 99, false},
		"the session, no time":   {armed(), "sess-x", 0, false},
		"another session, after": {armed(), "sess-y", 101, false},
		"no session id":          {armed(), "", 101, false},
		"nothing pending":        {&contract.SessionHistory{LastPrecompactSession: "sess-x", LastPrecompactTS: 100}, "sess-x", 101, false},
		"already lapsed":         {&contract.SessionHistory{AwaitingCompactStart: true, CompactStartLapsed: true, LastPrecompactSession: "sess-x"}, "sess-x", 101, false},
		"nil history is a no-op": {nil, "sess-x", 101, false},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.h.NoteCompactLapse(tc.sess, tc.at))
			if tc.h != nil {
				require.Equal(t, tc.want || name == "already lapsed", tc.h.CompactStartLapsed)
			}
		})
	}
}
