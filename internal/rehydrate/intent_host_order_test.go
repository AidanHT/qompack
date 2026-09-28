package rehydrate

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// SP08-D3, owner decision D35: prompt_<s>_0 is the session's first PUBLISHED prompt. A prompt that
// reached only a hook's client spool can be published after one its host sent later, and D35 rules
// that race out of the host-order guarantee without re-numbering published turns. So item 2 keeps
// injecting prompt_<s>_0, and when the store shows a later turn of the session with an earlier host
// timestamp, it says so: a Warn and a drop entry naming both records, never a silent substitution.

// orderedFakeStore is fakeStore plus the store.PromptOrder capability, answering from the scripted
// records exactly as the real index scan does: the session's UserPromptSubmit record with the
// lowest TS, the lower turn on a tie.
type orderedFakeStore struct {
	*fakeStore
	err error
}

var _ store.PromptOrder = orderedFakeStore{}

func (f orderedFakeStore) EarliestPrompt(_ context.Context, s core.SessionID) (store.ToolUseRecord, error) {
	if f.err != nil {
		return store.ToolUseRecord{}, f.err
	}
	var best store.ToolUseRecord
	found := false
	for _, rec := range f.records {
		if rec.Session != s || rec.Tool != "UserPromptSubmit" {
			continue
		}
		if !found || rec.TS < best.TS || (rec.TS == best.TS && rec.Turn < best.Turn) {
			best, found = rec, true
		}
	}
	if !found {
		return store.ToolUseRecord{}, core.ErrNotFound
	}
	return best, nil
}

// withStampedPrompt is withPrompt with the record's host timestamp.
func withStampedPrompt(f *fakeStore, sess core.SessionID, turn core.TurnIndex, ts core.UnixMilli, body string) {
	id := core.ToolUseID("prompt_" + string(sess) + "_" + itoa(int(turn)))
	f.withPrompt(id, sess, turn, body)
	rec := f.records[id]
	rec.TS = ts
	f.records[id] = rec
}

// TestUserIntent_HostEarlierLaterTurnIsNamed: the host's second prompt took turn 0 and the host-first
// prompt was captured at turn 3. Item 2 still injects prompt_<s>_0 verbatim (turns are not
// re-numbered), and reports the substitution by name.
func TestUserIntent_HostEarlierLaterTurnIsNamed(t *testing.T) {
	cp := ckMinimal()
	r := requestFor(t, cp, generousTestBudget)
	fs := newFakeStore()
	withStampedPrompt(fs, cp.Session, 0, 2_000, cp.UserIntent.Original)
	withStampedPrompt(fs, cp.Session, 3, 1_000, "the prompt the host sent first")
	withStampedPrompt(fs, "another-session", 0, 1, "another session's prompt")

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = orderedFakeStore{fakeStore: fs}

	got := buildUserIntent(bg(), r, d)

	require.Equal(t, "> "+cp.UserIntent.Original+"\n", got.units[0].text,
		"prompt_<s>_0 is still what item 2 injects: published turns are not re-numbered")
	id0 := "prompt_" + string(cp.Session) + "_0"
	id3 := "prompt_" + string(cp.Session) + "_3"
	require.Empty(t, cmp.Diff([]checkpoint.DropEntry{{
		Kind: dropKindUserIntentSource, ID: "host_order",
		Detail: id0 + " is this session's first captured prompt, not the first its host sent: " + id3 +
			" carries an earlier host timestamp and was captured after it, so the original request " +
			"may be that one; restore: expand(tool_use_id=" + id3 + ")",
	}}, got.drops, dropCmp))
	require.Equal(t, 1, log.warn, "a substituted original is a Warn")
	require.Zero(t, log.loud)
}

// TestUserIntent_HostOrderAgreementReportsNothing: when turn 0 is also the earliest-stamped prompt,
// or ties with a later turn, item 2 is exactly what it was.
func TestUserIntent_HostOrderAgreementReportsNothing(t *testing.T) {
	for name, laterTS := range map[string]core.UnixMilli{"later": 3_000, "tie": 2_000} {
		t.Run(name, func(t *testing.T) {
			cp := ckMinimal()
			r := requestFor(t, cp, generousTestBudget)
			fs := newFakeStore()
			withStampedPrompt(fs, cp.Session, 0, 2_000, cp.UserIntent.Original)
			withStampedPrompt(fs, cp.Session, 1, laterTS, "a later prompt")

			log := &spyLogger{}
			d := depsWith(log)
			d.Store = orderedFakeStore{fakeStore: fs}

			got := buildUserIntent(bg(), r, d)

			require.Equal(t, "> "+cp.UserIntent.Original+"\n", got.units[0].text)
			require.Empty(t, got.drops)
			require.Zero(t, log.warn)
		})
	}
}

// TestUserIntent_HostOrderUnverifiableIsWarnedNotGuessed: a store that cannot answer the order
// question (a bounded scan past its limit, a closed store) leaves item 2 as it was and says in the
// log that the check did not run, rather than inventing a substitution.
func TestUserIntent_HostOrderUnverifiableIsWarnedNotGuessed(t *testing.T) {
	cp := ckMinimal()
	r := requestFor(t, cp, generousTestBudget)
	fs := newFakeStore()
	withStampedPrompt(fs, cp.Session, 0, 2_000, cp.UserIntent.Original)

	log := &spyLogger{}
	d := depsWith(log)
	d.Store = orderedFakeStore{fakeStore: fs, err: errors.New("scan past its bound")}

	got := buildUserIntent(bg(), r, d)

	require.Equal(t, "> "+cp.UserIntent.Original+"\n", got.units[0].text)
	require.Empty(t, got.drops)
	require.Equal(t, 1, log.warn, "an unverifiable host order is logged")
}
