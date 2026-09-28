package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The observation publication path after SP08-D1's pass removal, step by step (publicationStep):
//
//	SyncPublication(root) → root-synced → intent fsync → intent-durable → record+marks write →
//	record-written → SyncPublication(root) → publication-synced → commit
//
// These tests pin the ORDER and COUNT of the two remaining passes, and then stop the path at every
// step exactly where a crash would (obsPubFault), reopen the store over what is on disk, and redeliver
// the way the observer does (recover first; publish afresh only when nothing was reserved). Every cut
// must end in ONE record, ONE of each mark it authors, and a committed binding — never a duplicate,
// never a lost mark, never a false publication. The record-written cut is run twice: once as a process
// crash (the page cache keeps the unsynced batch) and once as a power loss (the batch is gone).

var errInjectedCrash = errors.New("injected crash")

// tuLines parses index/tool_use.jsonl into (op, id, by) triples.
func tuLines(t *testing.T, root string) (records map[core.ToolUseID]int, marks map[[2]core.ToolUseID]int) {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Index, "tool_use.jsonl")))
	require.NoError(t, err)
	records, marks = map[core.ToolUseID]int{}, map[[2]core.ToolUseID]int{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		var l struct {
			Op string         `json:"op"`
			ID core.ToolUseID `json:"id"`
			By core.ToolUseID `json:"by"`
		}
		require.NoError(t, json.Unmarshal(sc.Bytes(), &l))
		if l.Op == "supersede" {
			marks[[2]core.ToolUseID{l.ID, l.By}]++
			continue
		}
		records[l.ID]++
	}
	require.NoError(t, sc.Err())
	return records, marks
}

func fileSize(t *testing.T, p string) int64 {
	t.Helper()
	info, err := os.Stat(paths.Long(p))
	require.NoError(t, err)
	return info.Size()
}

// TestObservationPublish_FreshPublishSyncsTheRootBeforeTheIntentAndTheIndexAfterTheWrite pins the
// fresh path's two passes in order: the root is proven before any line names it, and nothing is
// re-proven between the intent and the record write; the index is synced after the write.
func TestObservationPublish_FreshPublishSyncsTheRootBeforeTheIntentAndTheIndexAfterTheWrite(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "ordered publication content")
	obs := obsID(t, 702)
	rec := recWithObs("toolu_ordered_publication", root, obs)
	tuPath := filepath.Join(paths.Of(tp.Root).Index, "tool_use.jsonl")

	var seen []string
	tp.Store.obsPubFault = func(step publicationStep) error {
		intent, err := os.ReadFile(paths.Long(obsPath(tp.Root)))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		index, err := os.ReadFile(paths.Long(tuPath))
		if err != nil {
			return err
		}
		seen = append(seen, fmt.Sprintf("%s passes=%d intent=%t record=%t", step,
			tp.counter(CounterPublicationSync), bytes.Contains(intent, []byte(obs)),
			bytes.Contains(index, []byte(rec.ID))))
		return nil
	}
	_, recorded, err := tp.Store.RecordToolUseSuperseding(ctx, rec, nil)
	require.NoError(t, err)
	require.True(t, recorded)
	require.Equal(t, []string{
		"root-synced passes=1 intent=false record=false",
		"intent-durable passes=1 intent=true record=false",
		"record-written passes=1 intent=true record=true",
		"publication-synced passes=2 intent=true record=true",
	}, seen)
	got, err := tp.Store.ToolUseByObservation(ctx, obs)
	require.NoError(t, err)
	require.Equal(t, rec.ID, got.ID)
}

// TestObservationPublish_RecoveryReprovesTheRootBeforeTheRecord pins recovery as unchanged: a
// reserved intent completed after a restart re-proves its root before the record is written, then
// syncs the index — two passes, the first of which the fresh path no longer repeats.
func TestObservationPublish_RecoveryReprovesTheRootBeforeTheRecord(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	root := putRoot(t, tp, "recovered publication content")
	obs := obsID(t, 703)
	rec := recWithObs("toolu_recovered_publication", root, obs)
	require.NoError(t, tp.Store.ReserveObservation(ctx, obs, rec, nil))
	require.NoError(t, tp.Store.Close())

	re := openOver(t, tp.project)
	var seen []string
	re.Store.obsPubFault = func(step publicationStep) error {
		seen = append(seen, fmt.Sprintf("%s passes=%d", step, re.counter(CounterPublicationSync)))
		return nil
	}
	got, err := re.Store.RecoverToolUseByObservation(ctx, obs)
	require.NoError(t, err)
	require.Equal(t, rec.ID, got.ID)
	require.Equal(t, []string{"record-written passes=1", "publication-synced passes=2"}, seen,
		"recovery proves the root before writing the record, and syncs the index after")
}

// TestObservationPublish_CrashAtEveryStepPublishesOnce stops a fresh publication at every step,
// reopens over what is on disk, and redelivers.
func TestObservationPublish_CrashAtEveryStepPublishesOnce(t *testing.T) {
	cases := []struct {
		step      publicationStep
		powerLoss bool
	}{
		{step: pubStepRootSynced},
		{step: pubStepIntentDurable},
		{step: pubStepRecordWritten},
		{step: pubStepRecordWritten, powerLoss: true},
		{step: pubStepPublicationSynced},
	}
	for _, c := range cases {
		name := string(c.step)
		if c.powerLoss {
			name += "/power loss drops the unsynced batch"
		}
		t.Run(name, func(t *testing.T) {
			tp := newTestStore(t)
			ctx := context.Background()
			target := core.ToolUseID("toolu_crash_target")
			require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
				ID: target, Session: "sess-obs", Turn: 0, TS: 0, Tool: "FileRead",
				Root: putRoot(t, tp, "the earlier read"), Path: "src/x.ts",
			}))
			require.NoError(t, tp.Store.Flush(ctx))
			obs := obsID(t, 704)
			rec := recWithObs("toolu_crash_record", putRoot(t, tp, "the later read"), obs)
			tuPath := filepath.Join(paths.Of(tp.Root).Index, "tool_use.jsonl")
			synced := fileSize(t, tuPath)

			tp.Store.obsPubFault = func(step publicationStep) error {
				if step == c.step {
					return errInjectedCrash
				}
				return nil
			}
			_, _, err := tp.Store.RecordToolUseSuperseding(ctx, rec, []core.ToolUseID{target})
			require.ErrorIs(t, err, errInjectedCrash)
			tp.Store.obsPubFault = nil
			require.NoError(t, tp.Store.Close())
			if c.powerLoss {
				require.Greater(t, fileSize(t, tuPath), synced, "fixture: the cut left an unsynced batch to lose")
				require.NoError(t, os.Truncate(paths.Long(tuPath), synced))
			}

			re := openOver(t, tp.project)
			got, err := re.Store.RecoverToolUseByObservation(ctx, obs)
			if errors.Is(err, core.ErrNotFound) {
				require.Equal(t, pubStepRootSynced, c.step, "only a cut before the intent leaves nothing to recover")
				_, recorded, perr := re.Store.RecordToolUseSuperseding(ctx, rec, []core.ToolUseID{target})
				require.NoError(t, perr)
				require.True(t, recorded)
			} else {
				require.NoError(t, err, "a reserved intent is recoverable from every later cut")
				require.Equal(t, rec.ID, got.ID)
			}
			require.Equal(t, int64(2), re.counter(CounterPublicationSync),
				"the redelivery proves the root once before the record and syncs the index once after")

			bound, err := re.Store.ToolUseByObservation(ctx, obs)
			require.NoError(t, err, "the binding is committed")
			require.Equal(t, rec.ID, bound.ID)
			records, marks := tuLines(t, tp.Root)
			require.Equal(t, 1, records[rec.ID], "one record for one delivery, whatever the cut")
			require.Equal(t, 1, marks[[2]core.ToolUseID{target, rec.ID}], "the mark it authors lands exactly once")
			prior, err := re.Store.ToolUse(ctx, target)
			require.NoError(t, err)
			require.Equal(t, rec.ID, prior.SupersededBy)

			before, err := os.ReadFile(paths.Long(tuPath))
			require.NoError(t, err)
			_, err = re.Store.RecoverToolUseByObservation(ctx, obs)
			require.NoError(t, err)
			after, err := os.ReadFile(paths.Long(tuPath))
			require.NoError(t, err)
			require.Equal(t, before, after, "a second redelivery appends nothing")
		})
	}
}
