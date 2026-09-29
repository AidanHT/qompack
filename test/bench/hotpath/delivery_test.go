package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// spoolFixture builds a real project layout with a real ipc spool underneath it, and returns the
// project root plus the SpoolWriter's own path — exactly the two values runHarness hands
// censusDeliveries. Nothing here is a stand-in: ipc.NewSpool is the same writer
// internal/ipc/client.go's spoolAndReturn appends through in production, so the on-disk bytes
// this census reads are the bytes a degraded client really leaves behind.
func spoolFixture(t *testing.T) (root, ownPath string, sp ipc.SpoolWriter) {
	t.Helper()
	root = t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	sp, err := ipc.NewSpool(paths.Of(root).Spool)
	require.NoError(t, err)
	t.Cleanup(func() { closeSpool(sp) })
	return root, sp.Path(), sp
}

func closeSpool(sp ipc.SpoolWriter) {
	if cl, ok := sp.(interface{ Close() error }); ok {
		_ = cl.Close()
	}
}

// observeRequest is one hot-path request shaped exactly as internal/cli/hookclient.go builds it:
// Op observe.tool, Session copied off the event, an Event body carrying the tool_use_id.
func observeRequest(sess core.SessionID, id core.ToolUseID) ipc.Request {
	ev := hookio.Event{
		HookEventName: "PostToolUse",
		SessionID:     sess,
		ToolName:      "Read",
		ToolUseID:     id,
	}
	return ipc.Request{Op: ipc.OpObserveTool, Session: sess, TS: 1, Event: &ev}
}

// requestOf is observeRequest for one identity.
func requestOf(id deliveryIdentity) ipc.Request { return observeRequest(id.Session, id.ToolUse) }

// writeHookSpool leaves what one spawned hook leaves when it defers: its own client-<pid>.ndjson,
// holding its request, written by the real spool writer's line format. pid is any number other
// than this process's own.
func writeHookSpool(t *testing.T, root string, pid int, reqs ...ipc.Request) string {
	t.Helper()
	var b strings.Builder
	for _, r := range reqs {
		line, err := ipc.EncodeRequest(r)
		require.NoError(t, err)
		b.Write(line)
	}
	p := filepath.Join(paths.Of(root).Spool, "client-"+strconv.Itoa(pid)+".ndjson")
	require.NoError(t, os.WriteFile(paths.Long(p), []byte(b.String()), 0o600))
	return p
}

// writeWAL leaves a daemon WAL segment holding reqs, the way ingest.Accept appends them: the
// request line as the daemon received it.
func writeWAL(t *testing.T, root string, sess core.SessionID, reqs ...ipc.Request) string {
	t.Helper()
	var b strings.Builder
	for _, r := range reqs {
		line, err := ipc.EncodeRequest(r)
		require.NoError(t, err)
		b.Write(line)
	}
	p := filepath.Join(paths.Of(root).Spool, "wal-"+string(sess)+".ndjson")
	require.NoError(t, os.WriteFile(paths.Long(p), []byte(b.String()), 0o600))
	return p
}

// recordInStore captures ids through the REAL store, exactly as the daemon's runIngested leaves a
// published tool use: the content object, then its tool_use record. It is what a request the
// daemon replayed from a client spool leaves behind once the drain has removed the spool file.
func recordInStore(t *testing.T, root string, ids ...deliveryIdentity) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(root, config.Defaults(), store.Deps{Log: logging.Nop()})
	require.NoError(t, err)
	defer func() { require.NoError(t, st.Close()) }()
	for _, id := range ids {
		body := "package main // " + string(id.ToolUse) + "\n"
		res, err := st.PutBytes(ctx, []byte(body), store.PutOptions{Tool: "Read", Path: "src/main.go"})
		require.NoError(t, err)
		require.NoError(t, st.RecordToolUse(ctx, store.ToolUseRecord{
			ID: id.ToolUse, Session: id.Session, Tool: "Read", Path: "src/main.go",
			Root: res.Root.Hash, Bytes: int64(len(body)), Status: store.StatusOK,
		}))
	}
}

// censusOf is a census that found exactly ids, for the reconciliation tests that do not need files.
func censusOf(ids ...deliveryIdentity) deliveryCensus {
	c := deliveryCensus{Found: map[deliveryIdentity]bool{}}
	for _, id := range ids {
		c.Found[id] = true
	}
	return c
}

// without returns ids with every identity in drop removed.
func without(ids []deliveryIdentity, drop ...deliveryIdentity) []deliveryIdentity {
	gone := map[deliveryIdentity]bool{}
	for _, d := range drop {
		gone[d] = true
	}
	out := make([]deliveryIdentity, 0, len(ids))
	for _, id := range ids {
		if !gone[id] {
			out = append(out, id)
		}
	}
	return out
}

// TestClientSpoolNameShape_DerivedFromTheRealWriter pins that the census identifies client spool
// files POSITIVELY, from internal/ipc's own naming, rather than by excluding the wal- prefix.
func TestClientSpoolNameShape_DerivedFromTheRealWriter(t *testing.T) {
	_, ownPath, _ := spoolFixture(t)

	prefix, ext, err := clientSpoolNameShape(ownPath)
	require.NoError(t, err)
	require.Equal(t, "client-", prefix)
	require.Equal(t, ".ndjson", ext)
	require.Equal(t, prefix+strconv.Itoa(os.Getpid())+ext, filepath.Base(ownPath),
		"the derived shape must reconstruct the real writer's own file name")

	_, _, err = clientSpoolNameShape(filepath.Join("spool", "not-a-client-file.txt"))
	require.Error(t, err, "a path that does not carry this process's pid must fail loudly, never silently match nothing")
}

// TestSentIdentities_AreExactlyTheRequestsTheHarnessSends pins the ledger's list of identities to
// the requests themselves: one per planned send, none repeated, and each built by the same function
// the request's own payload is — B-A/B-D's spawned stdin, the warm-up generator and the
// hook_ack_rtt tranche (whose recording-client test, TestAckRTTTranche_SendsTheWarmUpsAndTimesOnlyTheSamples,
// pins the same identities in the order they go out).
func TestSentIdentities_AreExactlyTheRequestsTheHarnessSends(t *testing.T) {
	const iterations, samples = 5, 3
	for _, warm := range []bool{false, true} {
		ids := sentIdentities(iterations, warm, samples)
		require.Len(t, ids, int(expectedHotPathSends(iterations, warm)+ackRTTTrancheSends(samples)))
		require.Len(t, gatedIdentities(iterations, warm), int(expectedHotPathSends(iterations, warm)),
			"the gated window's identities are expectedHotPathSends' population")
		seen := map[deliveryIdentity]bool{}
		for _, id := range ids {
			require.False(t, seen[id], "%s is sent twice: one identity must name one request", id)
			seen[id] = true
		}
	}

	root := "/bench/project"
	for seq := 0; seq < iterations; seq++ {
		var ev hookio.Event
		require.NoError(t, json.Unmarshal(representativeObservePayload(baSessionID, root, seq), &ev))
		require.Equal(t, baToolUseID(seq), ev.ToolUseID, "spawn %d's stdin carries its ledger identity", seq)
	}
	gen := newPayloadGen(1)
	for i := 0; i < warmHotTranche; i++ {
		require.Equal(t, warmToolUseID(i), gen.next(i, warmSessionID, root).ToolUseID)
	}

	require.Len(t, ackRTTToolUsePrefix, len(baToolUsePrefix),
		"hook_ack_rtt and B-B must send the same size of event, so their prefixes are the same length")
	require.NotEqual(t, ackRTTToolUsePrefix, baToolUsePrefix,
		"the tranche's tool uses must not be B-A's: the store records a tool use once per id")
}

// TestCensusDeliveries_FindsThisRunsRequestsInEveryDurablePlace is the census's discrimination: a
// request of this harness's own sessions counts wherever it durably is — a hook's client spool, the
// daemon's WAL, the store — and nothing else that shares those places does: not an admin probe, not
// a checkpoint, not another session's request.
func TestCensusDeliveries_FindsThisRunsRequestsInEveryDurablePlace(t *testing.T) {
	root, ownPath, sp := spoolFixture(t)

	deferred := deliveryIdentity{baSessionID, baToolUseID(1)}
	warm := deliveryIdentity{warmSessionID, warmToolUseID(3)}
	accepted := deliveryIdentity{baSessionID, baToolUseID(99)}
	replayed := deliveryIdentity{baSessionID, baToolUseID(7)}

	require.NoError(t, sp.Append(requestOf(deferred)))
	require.NoError(t, sp.Append(requestOf(warm)))
	// Not this run's hot-path population:
	require.NoError(t, sp.Append(ipc.Request{Op: ipc.OpAdminPing, Session: adminPingSessionID, TS: 1}))
	require.NoError(t, sp.Append(observeRequest("some-other-session", "toolu_other_4")))
	require.NoError(t, sp.Append(ipc.Request{Op: ipc.OpCheckpoint, Session: beSessionID, TS: 1}))
	writeWAL(t, root, baSessionID, requestOf(accepted))
	recordInStore(t, root, replayed, deliveryIdentity{"some-other-session", "toolu_other_5"})

	census, err := censusDeliveries(root, ownPath, harnessHotPathSessions())
	require.NoError(t, err)
	require.Equal(t, censusOf(deferred, warm, accepted, replayed).Found, census.Found,
		"a deferral in the client spool, a live request in the WAL and a replayed one in the store; the probe, the checkpoint and the foreign session are all excluded")
	require.Zero(t, census.Unreadable)
	require.Equal(t, 1, census.ClientFiles)
	require.Equal(t, 1, census.WALSegments)
}

// TestCensusDeliveries_FindsWhatTheRealStoreRecorded pins storeToolUseIndexName and the index
// line's keys against the real store: a record the store writes is one the census finds, and a
// store the harness cannot read the same way fails here rather than in a run.
func TestCensusDeliveries_FindsWhatTheRealStoreRecorded(t *testing.T) {
	root, ownPath, _ := spoolFixture(t)
	ids := []deliveryIdentity{{baSessionID, baToolUseID(0)}, {ackRTTSessionID, ackRTTToolUseID(-1)}}
	recordInStore(t, root, ids...)

	_, err := os.Stat(paths.Long(filepath.Join(paths.Of(root).Index, storeToolUseIndexName)))
	require.NoError(t, err, "the store's tool_use index is where the census reads it")

	census, err := censusDeliveries(root, ownPath, harnessHotPathSessions())
	require.NoError(t, err)
	require.Equal(t, censusOf(ids...).Found, census.Found)
	require.Zero(t, census.Unreadable)
}

// TestCensusDeliveries_EmptyProjectFindsNothing pins the clean-run path: a project that holds
// nothing (or does not exist at all) censuses to nothing rather than erroring.
func TestCensusDeliveries_EmptyProjectFindsNothing(t *testing.T) {
	root, ownPath, _ := spoolFixture(t)
	census, err := censusDeliveries(root, ownPath, harnessHotPathSessions())
	require.NoError(t, err)
	require.Empty(t, census.Found)
	require.Zero(t, census.Unreadable)

	census, err = censusDeliveries(filepath.Join(t.TempDir(), "never-created"), ownPath, harnessHotPathSessions())
	require.NoError(t, err, "a project that never spooled or captured anything has nothing to census")
	require.Empty(t, census.Found)
}

// TestCensusDeliveries_CorruptLineIsCountedNotGuessedAt pins that a line the census cannot read
// is surfaced as Unreadable — which reconcileDelivery turns into a hard failure — rather than being
// silently skipped (which could turn a deferral into a false "lost") or silently counted (which
// could mask a real loss): in a client spool, as a complete WAL line, and in the store's index.
// The one line it does not count is an unterminated WAL or index tail, which is a line its writer
// is still appending and whose request was accounted for where it was before.
func TestCensusDeliveries_CorruptLineIsCountedNotGuessedAt(t *testing.T) {
	appendRaw := func(t *testing.T, p, s string) {
		t.Helper()
		f, err := os.OpenFile(paths.Long(p), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		require.NoError(t, err)
		_, err = f.WriteString(s)
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}
	id := deliveryIdentity{baSessionID, baToolUseID(1)}

	t.Run("client spool", func(t *testing.T) {
		root, ownPath, sp := spoolFixture(t)
		require.NoError(t, sp.Append(requestOf(id)))
		closeSpool(sp)
		appendRaw(t, ownPath, "{not json at all\n")

		census, err := censusDeliveries(root, ownPath, harnessHotPathSessions())
		require.NoError(t, err)
		require.True(t, census.Found[id])
		require.Equal(t, int64(1), census.Unreadable)

		_, rerr := reconcileDelivery([]deliveryIdentity{id}, 0, census)
		require.Error(t, rerr, "a spool line the harness cannot classify must stop the run, not be assumed benign")
		require.Contains(t, rerr.Error(), "would not decode")
	})
	t.Run("a client spool's torn tail", func(t *testing.T) {
		root, ownPath, sp := spoolFixture(t)
		require.NoError(t, sp.Append(requestOf(id)))
		closeSpool(sp)
		appendRaw(t, ownPath, `{"op":"observe.tool"`)

		census, err := censusDeliveries(root, ownPath, harnessHotPathSessions())
		require.NoError(t, err)
		require.Equal(t, int64(1), census.Unreadable, "the hook that wrote it has exited: an unterminated line is torn")
	})
	t.Run("this harness's own request with no tool use to match", func(t *testing.T) {
		root, ownPath, sp := spoolFixture(t)
		require.NoError(t, sp.Append(ipc.Request{Op: ipc.OpObserveTool, Session: baSessionID, TS: 1}))

		census, err := censusDeliveries(root, ownPath, harnessHotPathSessions())
		require.NoError(t, err)
		require.Equal(t, int64(1), census.Unreadable)
	})
	t.Run("WAL", func(t *testing.T) {
		root, ownPath, _ := spoolFixture(t)
		wal := writeWAL(t, root, baSessionID, requestOf(id))
		appendRaw(t, wal, `{"op":"observe.tool","session":"bench-b-a"`)

		census, err := censusDeliveries(root, ownPath, harnessHotPathSessions())
		require.NoError(t, err)
		require.True(t, census.Found[id])
		require.Zero(t, census.Unreadable, "an unterminated WAL tail is a line the ingest is still appending")

		appendRaw(t, wal, "\n")
		census, err = censusDeliveries(root, ownPath, harnessHotPathSessions())
		require.NoError(t, err)
		require.Equal(t, int64(1), census.Unreadable, "the same bytes as a complete line are unreadable")
	})
	t.Run("store index", func(t *testing.T) {
		root, ownPath, _ := spoolFixture(t)
		recordInStore(t, root, id)
		index := filepath.Join(paths.Of(root).Index, storeToolUseIndexName)
		appendRaw(t, index, `{"v":1,"id":"toolu_ba_2"`)

		census, err := censusDeliveries(root, ownPath, harnessHotPathSessions())
		require.NoError(t, err)
		require.True(t, census.Found[id])
		require.Zero(t, census.Unreadable, "an unterminated index tail is a record the store is still appending")

		appendRaw(t, index, "\n")
		census, err = censusDeliveries(root, ownPath, harnessHotPathSessions())
		require.NoError(t, err)
		require.Equal(t, int64(1), census.Unreadable)
	})
}

// TestCensusDeliveries_AReplayDuringTheCensusIsStillFound is the ordering half of the fix. The
// daemon's client-spool watcher can replay a deferred request and remove its spool file at any
// moment the census runs; here it does so at the worst one, right after the spool tier was read.
// The request is found anyway, in the store, which the census reads last. A drain that removed the
// file WITHOUT capturing the request is the negative control: that request is LOST, and named.
func TestCensusDeliveries_AReplayDuringTheCensusIsStillFound(t *testing.T) {
	early := deliveryIdentity{baSessionID, baToolUseID(3)}
	late := deliveryIdentity{baSessionID, baToolUseID(4)}

	t.Run("replayed into the store", func(t *testing.T) {
		root, ownPath, _ := spoolFixture(t)
		earlyFile := writeHookSpool(t, root, 910001, requestOf(early))
		lateFile := writeHookSpool(t, root, 910002, requestOf(late))

		// The watcher replayed `early` before the census started: gone from the spool, in the store.
		recordInStore(t, root, early)
		require.NoError(t, os.Remove(paths.Long(earlyFile)))

		census, err := censusDeliveriesAround(root, ownPath, harnessHotPathSessions(), func() {
			// ...and replays `late` between the census's spool read and its store read.
			recordInStore(t, root, late)
			require.NoError(t, os.Remove(paths.Long(lateFile)))
		})
		require.NoError(t, err)
		require.True(t, census.Found[early], "a request replayed before the census is in the store")
		require.True(t, census.Found[late], "a request replayed during the census was in the spool when that was read")

		ledger, err := reconcileDelivery([]deliveryIdentity{early, late}, 0, census)
		require.NoError(t, err, "a replayed deferral is not a lost event")
		require.Equal(t, deliveryLedger{Sent: 2, Delivered: 0, Deferred: 2}, ledger)
	})
	t.Run("removed without a capture", func(t *testing.T) {
		root, ownPath, _ := spoolFixture(t)
		earlyFile := writeHookSpool(t, root, 910001, requestOf(early))
		require.NoError(t, os.Remove(paths.Long(earlyFile)))

		census, err := censusDeliveries(root, ownPath, harnessHotPathSessions())
		require.NoError(t, err)
		_, err = reconcileDelivery([]deliveryIdentity{early}, 0, census)
		require.Error(t, err)
		require.Contains(t, err.Error(), "1 are LOST")
		require.Contains(t, err.Error(), early.String(), "the error names the lost request")
	})
}

// TestCensusAndReconcile_TheW9PhaseThreeRun is the end-to-end regression for the run that raised
// this: w9-testfix's full test/integration pass on Windows under Phase 3 load, where the harness
// sent 2130 hot-path requests, l0_ingest observed 1537, 575 undelivered requests were still in hook
// client spools and 18 more had already been replayed by the daemon's client-spool watcher. The
// count-based guard reported those 18 as LOST. Built on disk — live requests in the WAL and the
// store, deferrals in client spools, the replayed ones in the store only — the run must reconcile
// as 593 deferrals and no loss; the same run with one deferral missing from everywhere still fails.
func TestCensusAndReconcile_TheW9PhaseThreeRun(t *testing.T) {
	const delivered, stillSpooled, replayed = 1537, 575, 18
	sent := sentIdentities(2000, true, ackRTTSamples)
	require.Len(t, sent, 2130)

	build := func(t *testing.T, dropLast bool) (string, string) {
		t.Helper()
		root, ownPath, _ := spoolFixture(t)
		live, rest := sent[:delivered], sent[delivered:]
		published := 100 // the first few live requests are published already, the rest wait in the WAL
		recordInStore(t, root, live[:published]...)
		var walReqs []ipc.Request
		for _, id := range live[published:] {
			walReqs = append(walReqs, requestOf(id))
		}
		writeWAL(t, root, baSessionID, walReqs...)
		recordInStore(t, root, rest[:replayed]...)
		spooled := rest[replayed : replayed+stillSpooled]
		if dropLast {
			spooled = spooled[:len(spooled)-1]
		}
		for i, id := range spooled {
			writeHookSpool(t, root, 920000+i, requestOf(id))
		}
		return root, ownPath
	}

	root, ownPath := build(t, false)
	census, err := censusDeliveries(root, ownPath, harnessHotPathSessions())
	require.NoError(t, err)
	ledger, err := reconcileDelivery(sent, delivered, census)
	require.NoError(t, err, "every one of the 2130 is in a spool, the WAL or the store: nothing was lost")
	require.Equal(t, deliveryLedger{Sent: 2130, Delivered: delivered, Deferred: 593}, ledger)
	notes := buildNotes(daemon.StatusSnapshot{}, false, 2000, ledger)
	require.Contains(t, strings.Join(notes, "\n"),
		"delivery ledger: 2130 hot-path requests sent, 1537 delivered live to the daemon, 593 DEFERRED to the client spool and 0 lost.")

	root, ownPath = build(t, true)
	census, err = censusDeliveries(root, ownPath, harnessHotPathSessions())
	require.NoError(t, err)
	_, err = reconcileDelivery(sent, delivered, census)
	require.Error(t, err, "a deferral that is nowhere is still a lost event")
	require.Contains(t, err.Error(), "1 are LOST")
}

// TestRefuseInheritedIdentities_TheProjectMustNotAlreadyHoldThisRunsRequests pins the census's
// precondition: an identity found before the run sent anything would be counted as this run's own.
func TestRefuseInheritedIdentities_TheProjectMustNotAlreadyHoldThisRunsRequests(t *testing.T) {
	root, ownPath, sp := spoolFixture(t)
	require.NoError(t, refuseInheritedIdentities(root, ownPath), "a fresh project holds nothing of this run's")

	require.NoError(t, sp.Append(observeRequest("some-other-session", "toolu_other_1")))
	recordInStore(t, root, deliveryIdentity{"resident-session", "toolu_resident_1"})
	require.NoError(t, refuseInheritedIdentities(root, ownPath), "other sessions' history is not this run's")

	recordInStore(t, root, deliveryIdentity{baSessionID, baToolUseID(0)})
	err := refuseInheritedIdentities(root, ownPath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already holds 1 hot-path request")
}

// TestReconcileDelivery_ARequestThisRunNeverSentIsRefused: an identity of this harness's own
// sessions that the run did not send means another client is sending as this harness.
func TestReconcileDelivery_ARequestThisRunNeverSentIsRefused(t *testing.T) {
	sent := gatedIdentities(3, false)
	stray := deliveryIdentity{baSessionID, baToolUseID(3)}
	_, err := reconcileDelivery(sent, 3, censusOf(append(append([]deliveryIdentity(nil), sent...), stray)...))
	require.Error(t, err)
	require.Contains(t, err.Error(), "never sent")
}

// TestCensusAndReconcile_Run32296920486 is the end-to-end regression for the observed CI failure:
// 2063 of 2064 hot-path requests reached the daemon on macos-latest and the 2064th degraded to
// the spool path. Driven through the REAL spool writer and the REAL census, the run must
// reconcile as one DEFERRAL and zero losses — while the identical shortfall with an empty spool
// must still fail hard.
func TestCensusAndReconcile_Run32296920486(t *testing.T) {
	root, ownPath, sp := spoolFixture(t)
	sent := gatedIdentities(2000, true)
	require.Len(t, sent, 2064)
	last := sent[len(sent)-1]
	require.Equal(t, deliveryIdentity{baSessionID, baToolUseID(1999)}, last)

	var live []ipc.Request
	for _, id := range sent[:len(sent)-1] {
		live = append(live, requestOf(id))
	}
	writeWAL(t, root, baSessionID, live...)
	require.NoError(t, sp.Append(requestOf(last)))

	census, err := censusDeliveries(root, ownPath, harnessHotPathSessions())
	require.NoError(t, err)
	ledger, err := reconcileDelivery(sent, 2063, census)
	require.NoError(t, err, "the daemon degrading one request to the spool is documented product behaviour, not an integrity failure")
	require.Equal(t, int64(1), ledger.Deferred)
	require.Zero(t, ledger.Lost)

	// The same arithmetic with nothing on disk to account for it is still a hard failure: the
	// guard's teeth are in the evidence, not in a loosened tolerance.
	delete(census.Found, last)
	_, err = reconcileDelivery(sent, 2063, census)
	require.Error(t, err)
	require.Contains(t, err.Error(), "1 are LOST")
}
