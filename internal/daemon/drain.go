package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
)

// drainLineDeadline bounds how long Drain waits for a single dispatched request to return
// (task-3-spec.md drain.go: "a per-line context deadline of 5s").
const drainLineDeadline = 5 * time.Second

// passBudgetKey carries a budgeted pass's budget on its context (withPassBudget).
type passBudgetKey struct{}

// passBudget is a budgeted pass's end, whether the pass has consumed a line yet, and, once the budget
// has stopped the pass, the spool files it left unfinished (notePassLeft).
type passBudget struct {
	end      time.Time
	consumed atomic.Bool
	// left holds the base names of the spool files the pass had not finished when its budget stopped
	// it: the one it stopped in or before, and every one after it. It is written by the pass and read
	// by whoever made the budget once the pass has returned (leftUnfinished).
	left map[string]bool
}

// leftUnfinished reports whether the pass the budget belonged to stopped on it before it had finished
// the spool file base. A pass that finished, or stopped for any other reason, left none.
func (b *passBudget) leftUnfinished(base string) bool {
	return b.left[base]
}

// errPassBudgetSpent ends a budgeted pass between two lines once its budget is spent. It wraps
// context.DeadlineExceeded, so every caller that stops on a spent context stops on it too, and a
// caller that asks again after a pass its budget cut short (drainOnRequest) sees one.
var errPassBudgetSpent = fmt.Errorf("daemon: drain: the pass's budget is spent: %w", context.DeadlineExceeded)

// withPassBudget returns ctx carrying a budget for the pass it is handed to: once budget has passed
// and the pass has made progress, it starts no further line; a line it has started keeps its own
// drainLineDeadline. Progress (notePassConsumed) is a spool's consumed front advancing, or the pass
// publishing or retiring a line itself. A deadline on the pass's context cancelled the line in flight
// instead, so a line whose publication took longer than the budget (a capture on a host with a deep
// fsync queue) was cancelled by every pass and published by none
// (TestDeliveryOrder_ARequestedPassFinishesALineSlowerThanItsBudget), and a pass whose own
// bookkeeping outlasted the budget on such a host (listing, progress state and the spool syncs
// before its first line) reached no line at all, however often it was asked again. A budgeted pass
// therefore always makes progress when it can.
//
// What bounds a pass is therefore not the clock alone. Once its budget is spent and it has made
// progress, it starts no new line. It overruns the budget by the line in flight when the budget ran out
// or, when the budget ran out before any progress, by every line up to and including the one that made
// it, plus the bookkeeping that closes the file it is in. Of a line's work, drainLineDeadline bounds
// only the dispatch to the handler: its admission, lease, journal checks and capture publication run
// under no deadline of the pass's. Until it has made progress the budget does not stop it: a pass that
// only consumes again what an earlier pass consumed behind a spool's waiting head has made no progress,
// and it reads on until it does or reaches the end of the spool. While consuming such a line counted,
// every pass on a slow host stopped inside that spool, and no budgeted pass reached a spool after it
// (V6 close-out C1.13). Such a pass is bounded by the spool instead. It reads each file's unconsumed
// bytes once. It admits again, and asks the committed frontier again about, each line still waiting on
// an earlier arrival of its session. A line an earlier pass of this daemon consumed behind such a head
// costs it that line's read and nothing more, for up to orderingProcessedCap such lines per file; past
// that bound the line is consumed again in full, though not counted or announced again unless a pass
// left an unleased line ahead of it unannounced (spoolMemo says what else the memo leaves out). A file
// unchanged since this daemon synced it is not synced, nor its progress rewritten, again. And while a
// cleanup intent waits, the pass reads each file's unconsumed lines once more, at its start, for
// references to the intent's blob (cleanupAcknowledged).
//
// Cancelling ctx still ends the pass, and the line in it, at once.
// The budget is on real time, as a context deadline is, never on the daemon's clock. It is the pass's
// alone: what the pass hands a line to runs without it (withoutPassBudget).
func withPassBudget(ctx context.Context, budget time.Duration) context.Context {
	pass, _ := newPassBudget(ctx, budget)
	return pass
}

// newPassBudget is withPassBudget that also returns the budget, for a caller that reads what the
// pass left unfinished once it has returned (passBudget.leftUnfinished: the client-spool watcher).
func newPassBudget(ctx context.Context, budget time.Duration) (context.Context, *passBudget) {
	b := &passBudget{end: time.Now().Add(budget)}
	return context.WithValue(ctx, passBudgetKey{}, b), b
}

// passStopped reports why a pass must start no further line: ctx's own error, or errPassBudgetSpent
// once a budget withPassBudget set has passed and the pass has made progress (notePassConsumed). Once
// it answers non-nil it never answers nil again.
func passStopped(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b, _ := ctx.Value(passBudgetKey{}).(*passBudget); b != nil && b.consumed.Load() && !time.Now().Before(b.end) {
		return errPassBudgetSpent
	}
	return nil
}

// notePassConsumed records that the pass ctx carries has made progress, which lets its budget end it
// (passStopped). A pass without a budget ignores it. Only progress counts: drainFile calls it when a
// spool's consumed front advances, and when the pass itself publishes or retires a line ahead of that
// front. A line consumed out of order stays ahead of the front, so every later pass reads it and
// consumes it again while the head ahead of it waits (at the cost of its read alone, within the bounds
// spoolMemo states); absorbing or skipping it again is not progress, and does not count.
func notePassConsumed(ctx context.Context) {
	if b, _ := ctx.Value(passBudgetKey{}).(*passBudget); b != nil {
		b.consumed.Store(true)
	}
}

// notePassLeft records on the budget ctx carries, when err says that budget stopped the pass, the
// spool files the pass left unfinished: files, the one it stopped in or before and every one after it
// in pass order (passBudget.leftUnfinished). Any other stop records nothing.
func notePassLeft(ctx context.Context, err error, files []string) {
	b, _ := ctx.Value(passBudgetKey{}).(*passBudget)
	if b == nil || !errors.Is(err, errPassBudgetSpent) {
		return
	}
	b.left = make(map[string]bool, len(files))
	for _, path := range files {
		b.left[filepath.Base(path)] = true
	}
}

// withoutPassBudget returns ctx with no pass budget on it, keeping its cancellation and every other
// value. A budget belongs to the pass that set it and ends at the pass's boundary, where the pass hands
// a line on (dispatchPending): the handler that publishes the line runs under the line's own
// drainLineDeadline, and the session end a replayed flush starts runs under the ends' lifetime with no
// budget but its own. A context value outlives the context.WithoutCancel that detaches a session end
// from the pass (launchSessionEnd), and by the time the end drains the budget is spent and the pass
// has consumed a line, so an end started from a budgeted pass ran its settle and final drains under
// that spent budget: both stopped at their first spool file, the flush's spool was never absorbed and
// its recovery marker stayed at stage "drain"
// (TestDrain_AFlushEndedFromABudgetedPassRunsItsDrainsWithoutThatBudget).
func withoutPassBudget(ctx context.Context) context.Context {
	if b, _ := ctx.Value(passBudgetKey{}).(*passBudget); b == nil {
		return ctx
	}
	return context.WithValue(ctx, passBudgetKey{}, (*passBudget)(nil))
}

// drainReadBufferBytes sizes the buffered reader Drain scans each spool file with.
const drainReadBufferBytes = 64 << 10 // 64 KiB

// The WAL segment name's parts, which walSessionID takes apart to find the segment's session. Which
// family a spool file is in is ipc.SpoolFileKindOf's to say. (The blob-descriptor field name and
// shape live in blob.go, shared with ingest.go's dispatch path.)
const (
	drainWalPrefix = "wal-"
	drainFileExt   = ".ndjson"
)

// isClientSpoolName reports whether base names a hook's client spool (ipc's client-<pid>.ndjson), as
// opposed to one of the ingest's WAL segments or anything else in the spool directory.
func isClientSpoolName(base string) bool {
	return ipc.SpoolFileKindOf(base) == ipc.SpoolFileClient
}

// drainDeferral says why the drain left a line it read for a later attempt instead of consuming it.
type drainDeferral int

const (
	// deferNot: the line was consumed, or the pass ended on it.
	deferNot drainDeferral = iota
	// deferOrdering: an earlier leased arrival of its session is not yet on the committed frontier
	// (delivery-order-decision.md). Counted in l0_drain_ordering_deferred.
	deferOrdering
	// deferSessionEnd: the line is a flush whose session end is running right now (session_end.go).
	deferSessionEnd
	// deferInFlight: the line is a hook's client-spool copy of a delivery the daemon is publishing
	// right now — the hook spooled it because its ACK came too late, and the live copy is still with
	// its worker. The next pass finds it acknowledged and absorbs it (spool_watch.go, C1.13).
	deferInFlight
)

// drainStateFile is state/drain.json's filename.
const drainStateFile = "drain.json"

// drainFileState is one spool file's persisted progress. Each field carries an invariant a later
// pass, in this process or the next one, acts on:
//
//   - Size is the durable bound the drain has read this file to, and once this code has written the
//     record it never falls: a pass records max(the bound it read to, the bound the record already
//     held). The bound one pass reads to is the file's stat size, except for a WAL segment the
//     ingest held, where it is that segment's synced size (durableEnd) — and THAT bound is not
//     monotonic across passes, because ingest.holdSynced enters every segment the ingest opens into
//     ingest.synced at 0, so a segment reopened over bytes an earlier pass consumed answers 0 until
//     its next Sync returns. Keeping the bound the record already holds is what stops such a pass
//     from forgetting durable bytes an earlier pass recorded. Every byte below Size was on disk when
//     some pass of THIS code read it, so a file that comes back shorter than Size has lost bytes that
//     were durable, or is another file under the same name, and validateProgress refuses the spool
//     over it. The weaker cases are both a record this code did not write: the floor cannot go under
//     such a record's Offset, and a mark of this code's can come to rest on that same Offset rather
//     than on a Sync of its own. SizeIsRawStat says what each costs and what it does not.
//   - Offset is how much of the file this drain has consumed: every record below it was dispatched
//     and acknowledged, or accounted for as a gap, and none of it is ever delivered again. Size is
//     never recorded below it, so Offset never passes Size, and loadState's refusal of Offset > Size
//     describes a record no pass of this code can write.
//   - Done says the file was consumed to the stat size the pass saw AND that the bound the record
//     holds is that same size, so Offset == Size. It authorizes removeCompletedFile to unlink the
//     file, never a delivery.
//   - SizeIsRawStat says this record's Size may be a RAW STAT rather than a durable bound: it is
//     what a binary before this one recorded, a held segment's unsynced tail included. Such a Size
//     must not be kept — keeping it would inherit a bound a legitimate machine crash falls below,
//     which is the wedge drainFile describes — so the first pass of this code over the file lowers
//     it to that pass's own durable bound, floored at the consumed offset. That floor is not itself
//     a durable bound: a binary with no durableEnd (develop's drain.go has none) read a held segment
//     to EOF, so the Offset it recorded can name bytes no Sync ever returned for, and loadState
//     forbids a Size below Offset. No floor this code can choose lowers the record past that
//     residual, and the crash that takes those bytes wedges the spool for that one record exactly as
//     it wedged the binary that wrote it. What the pass does NOT do is call such a Size durable: the
//     mark is written only when the pass's own bound carries the recorded Size, so the claim is
//     never laundered, and it is made the first time a bound of this code's own reaches the offset.
//     "The pass's own bound" is durableEnd's, and that is a Sync of this code's everywhere except
//     for a file with nothing unread (size <= offset), where durableEnd returns the stat and syncs
//     nothing: an inherited record whose Offset already equals the stat is therefore marked with no
//     Sync of this code behind it, on a bound that rests on the inherited offset. It costs nothing
//     and changes no refusal — the mark can only be written that way where Size == Offset, and
//     validateProgress compares BOTH against the stat, so the refusal is identical either way and
//     the two floors coincide from then on.
//     SizeIsRawStat is the NEGATION of the mark on disk (drainFileRecord.DurableSize), so that the
//     zero value is a record this code wrote and a record carrying no mark — every record older than
//     the mark itself — is the conservative case.
type drainFileState struct {
	// The on-disk field names live on drainFileRecord, which MarshalJSON and UnmarshalJSON below
	// convert to and from; this struct is never serialized field by field.
	Size   int64
	Offset int64
	Done   bool
	// PendingBlobs is persisted with the acknowledged offset before any deletion is attempted. It names
	// each blob once (notePendingBlob).
	PendingBlobs  []string
	SizeIsRawStat bool
}

// drainState is state/drain.json's on-disk shape, keyed by spool file base name.
type drainState map[string]*drainFileState

// drainFileRecord is one record's shape inside state/drain.json. It exists for DurableSize, the mark
// this code writes and no binary before it could: a record carrying the mark holds a Size that names
// durable bytes only, which is what lets a later pass keep that bound instead of lowering it.
//
// The mark is written POSITIVELY, and its absence — not a second value — means "this size may be a
// raw stat". That is what makes it honest in both directions of an upgrade. Every record written
// before the mark existed lacks it, and lacking it is exactly the claim that record supports. And an
// older binary reading one of these records ignores the field (its loadState is a plain
// json.Unmarshal, which drops unknown fields) and drops it again when it rewrites the record — so a
// downgrade returns the record to the raw-stat meaning that binary's own Size has, rather than
// leaving a mark behind that would outlive the code that honoured it.
//
// What that costs is worth meeting here rather than inferring: the FORMAT survives a downgrade
// unchanged, and the PROTECTION does not. The record an older binary rewrites is byte-identical to a
// genuine pre-mark one, so the next pass of this code reads it as a raw stat and floors at the
// consumed offset — and the recorded bound then falls to that offset the first time a reopened
// segment answers a lower durableEnd. For that record the truncation window this mark closes is open
// again, until a bound of this code's own reaches its offset and re-marks it: one pass by an older
// binary surrenders the protection, and the defect it repairs reopens for as long as the record
// stays unmarked. Nothing distinguishes the two records, and trusting an unmarked Size is the wedge
// the mark exists to prevent, so the downgrade-then-upgrade cycle is accepted at that price.
type drainFileRecord struct {
	Size   int64 `json:"size"`
	Offset int64 `json:"offset"`
	Done   bool  `json:"done"`
	// DurableSize marks a Size this code wrote. omitempty is deliberate: the field is written only
	// when it is true, so the records without it are exactly those no pass of this code has written.
	DurableSize  bool     `json:"durable_size,omitempty"`
	PendingBlobs []string `json:"pending_blobs,omitempty"`
}

// MarshalJSON writes the record with the durable-size mark, unless it still carries the raw stat a
// binary before the mark recorded.
func (fs drainFileState) MarshalJSON() ([]byte, error) {
	return json.Marshal(drainFileRecord{
		Size: fs.Size, Offset: fs.Offset, Done: fs.Done,
		DurableSize: !fs.SizeIsRawStat, PendingBlobs: fs.PendingBlobs,
	})
}

// UnmarshalJSON reads a record this code wrote or one any binary before it wrote. A record with no
// durable-size mark is one whose Size may be a raw stat (drainFileState.SizeIsRawStat).
func (fs *drainFileState) UnmarshalJSON(b []byte) error {
	var rec drainFileRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return err
	}
	*fs = drainFileState{
		Size: rec.Size, Offset: rec.Offset, Done: rec.Done,
		PendingBlobs: rec.PendingBlobs, SizeIsRawStat: !rec.DurableSize,
	}
	return nil
}

// DrainConfig is drainer's dependency set.
type DrainConfig struct {
	Root    string
	Log     logging.Logger
	Metrics obs.Registry
	Clock   core.Clock
	// Dispatch is the same handler the worker pool uses to process a request.
	Dispatch func(ctx context.Context, req ipc.Request) ipc.Response
	// Seen shares in-flight ownership and successful handling within this daemon lifetime. It
	// is not a durable delivery identity: restart may redeliver, and equal-content identity is
	// a remaining SP-20 lease migration requirement.
	Seen *seenSet
	// Admit is the daemon-side privacy gate applied to every inherited spool record before it is
	// dispatched or persisted. A nil Admit admits everything — the pre-gate behaviour, kept so a
	// bare drainer fixture with no daemon behind it still works.
	Admit func(ipc.Request) admissionVerdict
	// Journal resolves the held delivery journal, so a drained line can take back the identity its
	// original delivery was assigned and can write the committed-frontier record that releases it.
	Journal func() (*deliveryJournal, error)
	// IsLive reports whether sess is still a live session; its WAL is kept (offset-marked, not
	// deleted) rather than removed once fully drained. A nil IsLive treats every session as ended,
	// so a bare drainer with no wired registry still deletes fully-drained files.
	IsLive func(sess core.SessionID) bool
	// RemoveWAL removes a fully drained wal-* segment on the drainer's behalf and reports whether it
	// did. The daemon wires the ingest's removeDrainedWAL, which refuses a segment the ingest holds
	// open for appending and one whose size no longer equals drained, deciding both under the mutex
	// every append takes. A nil RemoveWAL (a bare drainer with no ingest behind it) falls back to
	// removeIfUnchanged: the same size check, without that exclusion.
	RemoveWAL func(path string, drained int64) (removed bool, err error)
	// HoldsWAL reports whether the ingest holds the wal-* segment at path open for appending, the
	// first of RemoveWAL's two refusals. The drainer asks before it forgets a finished segment's
	// progress, and leaves a held one alone. Without the question, every pass over such a segment
	// (each idle tick, for a straggler after SessionEnd or an EndAbandoned session whose handle
	// stays cached) forgot its progress, had the removal refused and put the progress back: two
	// extra state/drain.json writes, and each time a window in which a crash leaves the file with
	// no entry. The answer is advisory. A segment that becomes held after it is still refused by
	// RemoveWAL, which decides under the lock every append takes, and its progress is put back. A
	// nil HoldsWAL skips the question.
	HoldsWAL func(path string) bool
	// SyncedWAL reports whether the ingest holds the wal-* segment at path open for appending and, if
	// it does, the segment's synced size: how much of it a Sync of the ingest's own handle returned nil
	// for (ingest.syncedWAL). The drain reads, leases and dispatches nothing of a held segment past
	// that size. A line a WAL batch has written but whose Sync has not returned is left for a later
	// pass, like a trailing incomplete line, so no lease becomes durable before its delivery's bytes.
	// It must answer without waiting for that batch. That frees the question from the batch, not the
	// whole pass: HoldsWAL and RemoveWAL decide under the mutex a batch holds through its Sync, so a
	// pass that reaches a finished segment of an ended session still waits for a batch in flight, as
	// long as that batch's Sync takes. Every file SyncedWAL does not report held is synced by the
	// drain itself before any of it is consumed (drainer.durableEnd). A nil SyncedWAL treats every
	// file as not held.
	SyncedWAL func(path string) (synced int64, held bool)
	// Released is told, once per pass and only when the pass is over, every session of which the
	// pass itself published a leased line or retired one by a proven denial. The daemon wires the
	// ingest's wakeSession: a live successor the worker pool parked behind such a delivery
	// (delivery_order.go's lanes) is then run again at once rather than at the next drain. Not
	// mid-pass: a lane woken then would dispatch the session's next queued job while this pass was
	// still to read that job's line, and the pass would meet it in progress and stop. Not for a line
	// the pass merely found already acknowledged, retired or complete: whoever settled it told the
	// lane then, and a spool file whose offset waits on another session is re-read by every pass, so
	// releasing its settled lines again would wake a parked lane on every pass — and a parked lane
	// asks for a drain (ingest.requestDrain), so a session whose head keeps failing would drain
	// forever. It is called with the drain's mutex held, so it must not block or drain. A nil
	// Released tells nobody.
	Released func(sess core.SessionID)
	// EndSession, when set, may take a leased flush line off the pass instead of Dispatch: it is
	// asked once the line has passed the ordering gate and the pass holds its in-process ownership
	// (Seen), and it reports true when it has started the line's session end on a goroutine of its
	// own. That end then owns the line's Seen entry and acknowledges the flush once SessionEnd has
	// run, and the pass leaves the line for a later pass, which absorbs it (deferSessionEnd). false
	// replays the line through Dispatch as before. The daemon wires endDrainedFlush (session_end.go).
	// It is called with the drain's mutex held, so it must not block or drain. A nil EndSession, or a
	// nil Seen, replays every flush through Dispatch.
	EndSession func(ctx context.Context, req ipc.Request, key core.Hash, lease deliveryLease) bool
	// ClientSpoolRemoving is told the base name of every hook client spool a pass is about to remove
	// once it was fully replayed (removeCompletedFile), and the drain calls the done it returns once the
	// removal has returned, whatever its outcome. The daemon wires its spool index's removing
	// (spool_heads.go): a hook whose pid was reused can write the same name again at the same size and
	// time the instant the unlink returns, and the index must read that file, not serve the removed
	// one's heads, so its entry must be gone from before the unlink to after it. Both calls are made
	// with the drain's mutex held, so neither may block or drain. A nil ClientSpoolRemoving tells
	// nobody.
	ClientSpoolRemoving func(base string) (done func())
	// SpooledPromptSettled is told every observe.prompt a pass consumes through its delivery stages
	// (absorbed, replayed or retired) from a hook's client spool. A later pass that consumes the line
	// again, ahead of a front that waits, skips it while this daemon remembers it (spoolMemo): past the
	// memo's bound of orderingProcessedCap such lines per file, or in a new daemon, the line is consumed
	// again and told again, as every pass told it before the memo. A hook spools a reply request only
	// when no reply reached it, so a warning the daemon's live reply to that nonce carried never
	// reached the host; the daemon wires settleSpooledPrompt, which has the observer re-arm it. It is
	// called with the drain's mutex held: it may wait for the session's observer lock, as Dispatch
	// does, but must not drain. A nil SpooledPromptSettled tells nobody.
	SpooledPromptSettled func(req ipc.Request)
}

// errSessionEndStarted is dispatchPending's answer for a leased flush EndSession took off the pass.
var errSessionEndStarted = errors.New("daemon: drain: the flush's session end runs on its own")

// drainer is a standalone drain engine (task-3-spec.md drain.go's algorithm), independent of the
// Daemon interface: Task 4 wires it into Daemon.Drain by constructing one from the running
// daemon's own dependencies and dispatch function.
type drainer struct {
	cfg DrainConfig
	mu  sync.Mutex // serializes concurrent Drain calls (idle tick vs. admin.drain) against one drain.json

	// syncFile makes a spool file's bytes durable before a pass consumes any of them (durableEnd).
	// Outside tests it is syncSpoolFileWith, issuing its fsync through syncHandle. Tests set it to
	// observe or fail the sync as a whole.
	syncFile func(path string) error
	// syncHandle is the fsync syncFile issues on the handle it opened for the file: (*os.File).Sync
	// outside tests, which set it to see that the fsync happens, on which file, and when.
	syncHandle func(*os.File) error
	// syncDir makes the spool directory's entries durable: paths.SyncDir outside tests, which set it to
	// observe or fail it. durableEnd issues it once per pass, after the pass's first file sync.
	syncDir func(dir string) error
	// removeSpool removes a fully replayed spool file that is not a WAL segment the ingest removes
	// (DrainConfig.RemoveWAL): removeIfUnchanged outside tests, which set it to act on the spool
	// directory between the unlink and the rest of removeCompletedFile.
	removeSpool func(path string, drained int64) (bool, error)
	// scanBlobRefs reads one spool file's unconsumed lines for the blobs they reference, which
	// cleanupAcknowledged must not remove: scanPendingBlobs outside tests, which set it to count the
	// scans a pass makes.
	scanBlobRefs func(path string, fs *drainFileState, pending, refs map[string]bool) error
	// readBlobBody reads the blob a line externalized its tool response to, for the line's publication
	// (dispatchPending): readBlob outside tests, which set it to count the blob bodies a pass reads. It is
	// the only way the drain reads a blob's body; naming a consumed line's blob reads none
	// (pendingBlobOf).
	readBlobBody func(root string, req ipc.Request) (ipc.Request, string, error)
	// dirSynced is set once the pass in progress has synced the spool directory. Drain clears it, under
	// mu, before the pass's first file.
	dirSynced bool
	// unsyncedNoted names each spool file whose sync has failed since its last one that succeeded, so
	// that noteUnsynced announces the failure Loud once, not on every pass. Guarded by mu.
	unsyncedNoted map[string]bool
	// releasedSessions collects the sessions DrainConfig.Released is told about when the pass in
	// progress ends. Guarded by mu.
	releasedSessions map[core.SessionID]struct{}
	// wedgeNoted records that this drainer has already announced a refused progress state, so the
	// Loud below fires once for a wedge rather than on every idle tick. Cleared by the first pass
	// that gets past validateProgress, so a wedge that returns later is announced again. Guarded
	// by mu, which Drain holds for the whole pass.
	wedgeNoted bool
	// memo is what this drainer's passes remember about each spool file that still has bytes past its
	// consumed front, so that a later pass does not do again what an earlier one did with the same
	// bytes (spoolMemo). Guarded by mu.
	memo map[string]*spoolMemo
	// stateOnDisk is state/drain.json's content as this drainer last wrote it, nil when that is not
	// known to be what the file holds. saveState writes nothing when the state marshals to exactly
	// these bytes, and a pass that loads any other progress (stateIsOwn) forgets every memo, whose lines'
	// cleanup intents and offsets live in those bytes. Guarded by mu.
	stateOnDisk []byte

	gapMu sync.Mutex
	gaps  DrainGapState
}

// newDrainer returns a drainer over cfg, filling in nil-safe defaults.
func newDrainer(cfg DrainConfig) *drainer {
	if cfg.Log == nil {
		cfg.Log = logging.Nop()
	}
	if cfg.Clock == nil {
		cfg.Clock = core.SystemClock()
	}
	if cfg.Dispatch == nil {
		cfg.Dispatch = func(context.Context, ipc.Request) ipc.Response { return ipc.Response{} }
	}
	if cfg.IsLive == nil {
		cfg.IsLive = func(core.SessionID) bool { return false }
	}
	dr := &drainer{
		cfg: cfg, syncHandle: (*os.File).Sync, syncDir: paths.SyncDir, unsyncedNoted: map[string]bool{},
		removeSpool: removeIfUnchanged, scanBlobRefs: scanPendingBlobs, readBlobBody: readBlob,
	}
	dr.syncFile = func(path string) error { return syncSpoolFileWith(path, dr.syncHandle) }
	return dr
}

// Drain replays every spool-tier file under root's spool directory (task-3-spec.md drain.go):
// wal-* first, lexically sorted as ipc.SpoolFiles lists them, then client-* in host order, by the
// req.TS of the first record each file still has to replay (orderClientSpoolsByHostTS,
// SP08-D3/D35).
// It is idempotent (state/drain.json records consumed offsets) and resumable: a cancelled Drain
// persists its progress and returns (n, ctx.Err()), so the next call picks up exactly where it
// stopped. Per-file errors are logged, counted, and never abort the rest of the drain.
func (dr *drainer) Drain(ctx context.Context) (int, error) {
	return dr.pass(ctx, false)
}

// DrainClientSpools is one pass over the hooks' client spools alone (client-<pid>.ndjson), the pass
// the client-spool watcher runs while sessions are active (spool_watch.go, C1.13). Every line it reads
// goes through exactly what Drain does with it: admission, lease, the ordering gate, publication and
// the committed frontier, under the same mutex. It reads no WAL segment: those are the worker pool's,
// publishing them right now, and a pass that met one of their lines in flight would stop that file
// with "delivery still in progress". Nor does it publish the drain's gap state (DrainGaps): a pass
// that looked at part of the spool cannot say the whole of it is complete, so the state the last full
// pass published stands until the next one.
func (dr *drainer) DrainClientSpools(ctx context.Context) (int, error) {
	return dr.pass(ctx, true)
}

// DrainClientSpoolsWithin is DrainClientSpools for a caller that may wait no longer than ctx allows,
// including for the drain's mutex: the PreCompact route's settle (precompact_settle.go, D53(c)). The
// other passes take the mutex without watching any context, and one of them may hold it for its own
// budget and a line's drainLineDeadline, so a plain DrainClientSpools could keep the route waiting
// past its bound before its own pass had begun. When ctx ends first, nothing is read and ctx's error
// is returned.
//
// only, when it is not nil, restricts the pass to the client spools it names by base name: the files
// holding the compacting session's captures, so another session's backlog, older in host order, does
// not spend the settle's bound. Skipping a file reorders nothing within a session: every file that
// holds one of the session's unconsumed lines is in only, and the pass keeps their host order.
//
// ctx's deadline is a hard one, unlike a pass budget (withPassBudget): when it expires the line in
// flight is cancelled, not finished. That is deliberate. A pass budget lets the line it started run
// for up to its own drainLineDeadline, which would put the PreCompact's settle past B-E by seconds,
// and the settle's bound exists to keep it inside B-E. The cost lands on the slowest disks: a line
// whose publication takes longer than the bound is cancelled by every settle and published by none
// of them, and its capture is named in the checkpoint's drop report instead. Nothing is lost: the
// client-spool watcher and the idle drain, which use pass budgets, finish and publish it.
func (dr *drainer) DrainClientSpoolsWithin(ctx context.Context, only map[string]bool) (int, error) {
	if !dr.lockWithin(ctx) {
		return 0, ctx.Err()
	}
	defer dr.mu.Unlock()
	return dr.passLocked(ctx, true, only)
}

// lockWithin takes dr.mu, giving up when ctx ends first. A Lock still pending then is completed and
// released by its own goroutine as soon as the pass holding the mutex ends: it reads and writes
// nothing, so it can outlive the caller harmlessly.
func (dr *drainer) lockWithin(ctx context.Context) bool {
	if dr.mu.TryLock() {
		return true
	}
	locked := make(chan struct{})
	go func() {
		dr.mu.Lock()
		close(locked)
	}()
	select {
	case <-locked:
		if ctx.Err() == nil {
			return true
		}
		dr.mu.Unlock()
		return false
	case <-ctx.Done():
		go func() {
			<-locked
			dr.mu.Unlock()
		}()
		return false
	}
}

// pass is Drain's body; clientOnly restricts it to the client spools (DrainClientSpools).
func (dr *drainer) pass(ctx context.Context, clientOnly bool) (int, error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	return dr.passLocked(ctx, clientOnly, nil)
}

// passLocked is pass with dr.mu already held by the caller. A non-nil only restricts it further, to
// the spool files it names by base name (DrainClientSpoolsWithin).
func (dr *drainer) passLocked(ctx context.Context, clientOnly bool, only map[string]bool) (int, error) {
	defer dr.releaseSessions() // however the pass ends, and before mu is released
	dr.dirSynced = false       // a file created since the last pass has an entry that pass's sync missed

	spoolDir := paths.Of(dr.cfg.Root).Spool
	files, err := ipc.SpoolFiles(spoolDir)
	if err != nil {
		return 0, err
	}

	gaps := &gapRecorder{}
	st, err := dr.loadState()
	if err != nil || !dr.stateIsOwn(st) {
		// Progress this drainer did not write itself: whatever its memos say about a file's lines rests on
		// the offsets and cleanup intents it last wrote, so none of them stands (spoolMemo).
		dr.forgetMemos()
	}
	if err != nil {
		gaps.add("", DrainGapProgressUnreadable, "drain progress state is unreadable")
		dr.publishGaps(gaps.state(dr.cfg.Clock, 0))
		return 0, err // preserve corrupt progress for diagnosis; never authorize deletion from it
	}
	if err := dr.validateProgress(files, st); err != nil {
		gaps.add("", DrainGapProgressUnreadable, "drain progress no longer matches the spool")
		dr.publishGaps(gaps.state(dr.cfg.Clock, 0))
		dr.noteWedged(len(files), err)
		return 0, err
	}
	// Past the gate: a wedge that returns later is a new one and gets announced again.
	dr.wedgeNoted = false
	// Client spools in host order, so a spooled prompt's turn follows the order its host sent it.
	// Each is placed by the record it replays next, at its validated consumed offset.
	files = orderClientSpoolsByHostTS(files, st)
	listed := make(map[string]bool, len(files))
	for _, path := range files {
		listed[filepath.Base(path)] = true
	}
	for base := range dr.memo {
		if !listed[base] {
			delete(dr.memo, base) // gone: a file that comes back under its name is another file
		}
	}
	if err := dr.forgetReleased(st, listed); err != nil {
		return dr.unpersisted(gaps, err)
	}
	total := 0
	stopErr := dr.cleanupAcknowledged(st)
	// The cleanup can consume the last intent of an entry whose file is already gone; that entry
	// is released now, and forgotten before the first file is drained like the rest.
	if err := dr.forgetReleased(st, listed); err != nil {
		return dr.unpersisted(gaps, errors.Join(stopErr, err))
	}

	for i, path := range files {
		if clientOnly && !isClientSpoolName(filepath.Base(path)) {
			continue
		}
		if only != nil && !only[filepath.Base(path)] {
			continue
		}
		if err := passStopped(ctx); err != nil {
			stopErr = errors.Join(stopErr, err)
			notePassLeft(ctx, err, files[i:])
			break
		}
		n, ferr := dr.drainFile(ctx, path, st, gaps)
		total += n
		if ferr == nil {
			continue
		}
		if errors.Is(ferr, context.Canceled) || errors.Is(ferr, context.DeadlineExceeded) {
			stopErr = errors.Join(stopErr, ferr)
			notePassLeft(ctx, ferr, files[i:])
			break
		}
		if dr.cfg.Metrics != nil {
			dr.cfg.Metrics.Counter(counterDrainFileError).Add(1)
		}
		dr.cfg.Log.Warn("daemon: drain: file error", "path", path, "err", ferr)
		stopErr = errors.Join(stopErr, ferr)
	}

	if serr := dr.saveState(st); serr != nil {
		dr.cfg.Log.Warn("daemon: drain: failed to persist state", "err", serr)
		stopErr = errors.Join(stopErr, serr)
	}
	pending := int64(0)
	for base, fs := range st {
		// A file's unread bytes are those its progress records, plus those the pass read no further
		// than because they are not durable yet and its progress therefore does not name (gaps.hold).
		// Counted together, one file is still one pending gap.
		//
		// Size can also stand ABOVE the file's stat, for a file that shrank under the pass (drainFile's
		// floor says how). The bytes between them are gone rather than unread, and this counts them
		// pending deliberately: they were durable when a pass read them, nothing has replayed them, and
		// the next pass refuses the spool over exactly those bytes (validateProgress). Clamping the
		// count at the stat would drop that hole out of the one signal this drain has for reporting it.
		if remaining := fs.Size - fs.Offset + gaps.withheld[base]; remaining > 0 {
			pending += remaining
			gaps.add(base, DrainGapPending, "spool bytes not yet replayed")
		}
	}
	if !clientOnly {
		dr.publishGaps(gaps.state(dr.cfg.Clock, pending+gaps.unsynced))
	}
	return total, stopErr
}

// drainFile drains one spool file, updating st in place. It returns the number of requests
// dispatched and, if ctx was cancelled mid-file, ctx.Err() — otherwise nil, even when individual
// corrupt lines were skipped (those are reported via the metrics/log side channel, not the
// returned error, so one bad line never aborts the rest of the file).
func (dr *drainer) drainFile(ctx context.Context, path string, st drainState, gaps *gapRecorder) (int, error) {
	base := filepath.Base(path)

	fi, err := os.Stat(paths.Long(path))
	if err != nil {
		if os.IsNotExist(err) {
			delete(dr.memo, base)
			// Listed, then gone before this stat: something other than the drainer deleted it. Its
			// entry goes with it unless it still carries cleanup intents, the only record of blobs
			// cleanupAcknowledged must still remove; forgetReleased keeps such an entry too.
			if gone := st[base]; gone == nil || len(gone.PendingBlobs) == 0 {
				delete(st, base)
			}
			return 0, nil
		}
		return 0, err
	}
	size := fi.Size()

	fs, ok := st[base]
	if !ok {
		fs = &drainFileState{}
		st[base] = fs
	}
	if fs.Done && fs.Size == size {
		return 0, dr.removeCompletedFile(path, base, fs, st)
	}
	fs.Done = false
	// Only a pass that moves this file's front can release a cleanup intent (cleanupAcknowledged after
	// the file, below).
	offsetBefore := fs.Offset

	// What an earlier pass of this drainer did with this very file, if it is still that file and no
	// shorter; unchanged says it has not changed at all.
	memo, unchanged := dr.memoOf(base, fi)
	// Nothing the pass consumes may be less durable than what the pass makes of it: a lease is a
	// durable journal line, and the offset persisted below names the bytes read. durableEnd bounds
	// the pass to bytes already on disk, syncing the file first where it has to; a sync that fails
	// leaves the whole file for a later pass.
	end, synced, err := dr.durableEnd(path, base, size, fs.Offset, unchanged && memo.synced)
	if err != nil {
		delete(dr.memo, base)
		// The stat size is not recorded: progress naming bytes the pass could not make durable would
		// outlive a machine crash that takes them, and validateProgress would then refuse every later
		// Drain. The file's unread bytes are pending all the same. Those up to the recorded size are
		// in the pending total already, so the pass adds the rest, in memory only.
		gaps.add(base, DrainGapUnsynced, "spool bytes could not be made durable")
		gaps.unsynced += max(size-fs.Size, 0)
		dr.noteUnsynced(path, base, err)
		return 0, err
	}
	delete(dr.unsyncedNoted, base) // durable now: a later failure is a new one, announced again

	f, err := os.Open(paths.Long(path))
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }() // also releases the handle if a callback panics or exits its goroutine
	// Also close explicitly before a possible os.Remove: Windows cannot delete an open file.
	// The file the pass reads, which is what a memo of this pass describes (rememberFile).
	opened, _ := f.Stat()

	if fs.Offset > 0 {
		if _, err := f.Seek(fs.Offset, io.SeekStart); err != nil {
			_ = f.Close()
			return 0, err
		}
	}

	// The pass reads the file as it stood at the stat above and no further, and of that only up to
	// end. Bytes appended after the stat — by the ingest to a WAL segment it holds, by a hook process
	// to its own client spool — belong to the next pass. Reading them too let the consumed offset
	// overtake the size recorded below, and loadState refuses exactly that as inconsistent progress,
	// so every later Drain failed before reading a single spool file. The bytes of a held segment past
	// its synced size wait the same way, as a trailing incomplete line does: unconsumed, and pending.
	r := bufio.NewReaderSize(io.LimitReader(f, max(end-fs.Offset, 0)), drainReadBufferBytes)
	// The durable OFFSET advances only over the contiguous consumed prefix from the front; the READ
	// position runs ahead of it. Bounded leased-delivery ordering (delivery-order-decision.md): a
	// leased line whose earlier same-session arrival is not yet acknowledged is DEFERRED — its bytes
	// and blob are left intact and the offset never passes it — while the pass looks ahead (bounded)
	// for the line that holds the missing predecessor, which a restart can place LATER in the same
	// file because WAL fsync order and lease-arrival order are separate batches. `processed` records
	// lines consumed out of order so the front can roll forward over them once the deferred prefix
	// clears; a still-deferred prefix at end of pass is left for a later scheduled pass (cross-file
	// predecessors), never spun on and never advanced over.
	offset := fs.Offset
	readPos := fs.Offset
	count := 0
	canceled := false
	var readErr error
	processed := map[int64]consumedLine{}
	var deferred []deferredLine
	// The lines an earlier pass of this drainer consumed out of order in this very file, behind a front
	// that has not reached them: already consumed, so this pass consumes each again without doing any
	// of it again (spoolMemo). announced is how far into the file earlier passes have read since the
	// memo began: they counted and announced every corrupt or unadmitted line before it.
	var memoLines map[int64]consumedLine
	announced := int64(0)
	if memo != nil {
		memoLines, announced = memo.consumed, memo.readTo
	}
	// firstLeft is where the first line starts that the pass read and left unconsumed under no lease the
	// journal holds, or -1. A later pass can still consume such a line as unadmitted, and nothing has
	// announced it, so the memo's readTo stops there (rememberFile). A leased line cannot be consumed that
	// way: a refused line whose lease the journal holds stays where it is, and a lease, once written, is
	// held for good.
	firstLeft := int64(-1)
	leave := func(start int64) {
		if firstLeft < 0 {
			firstLeft = start
		}
	}

	// consume counts as the pass consuming a line (notePassConsumed) only when the front advances. A
	// line consumed out of order is remembered in processed for the rest of this call and in this
	// drainer's memo of the file for the passes after it (rememberFile); every later pass reads it and
	// consumes it again while the prefix ahead of it waits, and that is no progress. A line the pass
	// itself publishes or retires out of order is progress, and its caller counts it (C1.13, D31). line
	// is what is remembered of the line: where the next one starts, its bytes' sum and the gaps that
	// consuming it left.
	consume := func(start int64, line consumedLine) {
		if start != offset {
			// Consumed out of order; the front rolls over it later. Bounded (item 3): past the roll-
			// forward memory cap we stop recording it — the line is already dispatched and ACKED, so a
			// later pass re-reads it, finds it on the committed frontier, and absorbs it there. This is
			// what keeps the map from growing without bound behind a stuck prefix.
			if len(processed) < orderingProcessedCap {
				processed[start] = line
			}
			return
		}
		notePassConsumed(ctx)
		offset = line.next
		for {
			n, ok := processed[offset]
			if !ok {
				break
			}
			delete(processed, offset)
			offset = n.next
		}
		fs.Offset = offset
	}

	// processOne runs one line through the frontier, ordering, seen and dispatch stages. done means
	// the line was consumed (roll the offset); deferIt says why a line is left for later instead —
	// blocked on an unacknowledged predecessor, or a flush whose session end is running now (both
	// retryable); dispatched means a real publication happened, so it counts; changed means this pass
	// published the line or retired it by a proven denial, which is what DrainConfig.Released reports;
	// a hard error ends the pass. It serves a freshly read line and a deferred re-attempt alike.
	processOne := func(dl deferredLine) (done bool, deferIt drainDeferral, dispatched, changed bool, hardErr error) {
		if dl.leased {
			retired, err := terminalForDelivery(dr.cfg.Journal, dl.lease)
			if err != nil {
				return false, deferNot, false, false, err
			}
			if retired {
				gaps.add(base, DrainGapDenied, "replay retired by policy denial")
				notePendingBlob(fs, pendingBlobOf(dr.cfg.Root, dl.req))
				return true, deferNot, false, false, nil
			}
		}
		if dl.leased && dr.acknowledgedDelivery(dl.lease, dl.leased) {
			notePendingBlob(fs, pendingBlobOf(dr.cfg.Root, dl.req))
			return true, deferNot, false, false, nil
		}
		if !dr.leasedPredecessorsReady(dl.lease, dl.leased) {
			return false, deferOrdering, false, false, nil
		}
		if dr.cfg.Seen != nil {
			completed, acquired := dr.cfg.Seen.begin(dl.key)
			if completed {
				if dl.leased && !dr.acknowledgedDelivery(dl.lease, dl.leased) {
					gaps.add(base, DrainGapUnacknowledged, "in-memory completion has no frontier record")
				}
				notePendingBlob(fs, pendingBlobOf(dr.cfg.Root, dl.req))
				return true, deferNot, false, false, nil
			}
			if !acquired {
				if !dl.req.Op.HotPath() {
					// A flush whose session end is running right now owns its line from the moment it
					// was acknowledged until the end has finished (session_end.go), which takes
					// seconds by design: meeting it is not a failure, only a line for a later pass.
					return false, deferSessionEnd, false, false, nil
				}
				if isClientSpoolName(base) {
					// A hook spools its delivery when the ACK comes too late, so a client spool can
					// hold a copy of a delivery a worker is publishing right now. That is the normal
					// shape of a late ACK, not a failure of this file: leave the copy for the pass
					// that finds it acknowledged (C1.13; spool_watch.go passes such a spool again).
					return false, deferInFlight, false, false, nil
				}
				return false, deferNot, false, false, fmt.Errorf("daemon: drain: delivery still in progress")
			}
		}
		blob, dispatchErr := dr.dispatchPending(ctx, dl.req, dl.lease, dl.leased, dl.key)
		if errors.Is(dispatchErr, errSessionEndStarted) {
			// The flush's session end runs on its own now, and owns the line's Seen entry: it is not
			// the pass's to finish. A later pass absorbs the line once the end has acknowledged it.
			return false, deferSessionEnd, false, false, nil
		}
		if dr.cfg.Seen != nil {
			dr.cfg.Seen.finish(dl.key, dispatchErr == nil)
		}
		if errors.Is(dispatchErr, errReplayDenied) {
			gaps.add(base, DrainGapDenied, "policy denied before replay publication")
			notePendingBlob(fs, blob)
			return true, deferNot, false, dl.leased, nil // dispatchPending retired a leased one
		}
		if dispatchErr != nil {
			gaps.add(base, DrainGapUnacknowledged, "publication did not reach the frontier")
			return false, deferNot, false, false, dispatchErr
		}
		notePendingBlob(fs, blob)
		return true, deferNot, true, true, nil
	}

	// settledSpooledPrompt reports a consumed client-spool prompt (DrainConfig.SpooledPromptSettled).
	settledSpooledPrompt := func(req ipc.Request) {
		if dr.cfg.SpooledPromptSettled != nil && req.Op == ipc.OpObservePrompt && isClientSpoolName(base) {
			dr.cfg.SpooledPromptSettled(req)
		}
	}

	// reattempt re-runs the deferred lines of sess, or every deferred line when all is set, after a
	// consume may have acknowledged a predecessor, to a fixpoint. It never blocks: a line that is still
	// deferred is simply kept for a later pass. A line's ordering gate waits only on earlier arrivals of
	// its own session (predecessorsAcknowledged), so a line consumed in sess can release only sess's
	// deferred lines, and each of those only the others of sess. Re-running every deferred line after
	// every line consumed cost a file of L consumed lines and D deferred ones L x D gate queries, three
	// journal queries each, whatever their sessions (V6 close-out C1.13, wave 20).
	reattempt := func(sess core.SessionID, all bool) error {
		for {
			if err := passStopped(ctx); err != nil {
				return err
			}
			progressed := false
			for idx := 0; idx < len(deferred); {
				if !all && deferred[idx].req.Session != sess {
					idx++
					continue
				}
				if err := passStopped(ctx); err != nil {
					return err
				}
				gaps.startLine()
				done, _, dispatched, changed, err := processOne(deferred[idx])
				if err != nil {
					return err
				}
				if !done {
					idx++
					continue
				}
				dl := deferred[idx]
				if changed {
					notePassConsumed(ctx) // published or retired by this pass, in order or not
				}
				consume(dl.start, consumedLine{next: dl.next, sum: dl.sum, gaps: slices.Concat(dl.gaps, gaps.takeLine())})
				settledSpooledPrompt(dl.req)
				if changed && dl.leased {
					dr.released(dl.lease)
				}
				if dispatched {
					count++
				}
				if dr.cfg.Metrics != nil {
					dr.cfg.Metrics.Counter(counterDrainOrderingResolved).Add(1)
				}
				deferred = append(deferred[:idx], deferred[idx+1:]...)
				progressed = true
			}
			if !progressed {
				return nil
			}
		}
	}

readLoop:
	for {
		if passStopped(ctx) != nil {
			canceled = true
			break readLoop
		}

		raw, err := r.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break readLoop // a trailing partial line, if any, is left unconsumed
			}
			readErr = err
			break readLoop
		}
		lineStart := readPos
		nextOffset := readPos + int64(len(raw))
		readPos = nextOffset
		gaps.startLine()
		sum := spoolLineSum(raw)

		if known, ok := memoLines[lineStart]; ok && known.next == nextOffset && known.sum == sum {
			// An earlier pass of this drainer consumed this very line, and the front has not reached it
			// since: what consuming it did is done and durable — its publication or retirement, its
			// cleanup intent, its count and its announcement. This pass consumes it again without any of
			// that, which is no progress, and reports again the gaps it left in the replay, as every pass
			// reports what it still finds there. Nothing else about it can have changed: a decision once
			// consumed is final, as it is for a line the front has passed.
			for _, g := range known.gaps {
				gaps.add(base, g.kind, g.reason)
			}
			consume(lineStart, known)
			continue
		}

		line := bytes.TrimSuffix(raw, []byte{'\n'})
		if len(bytes.TrimSpace(line)) == 0 {
			consume(lineStart, consumedLine{next: nextOffset, sum: sum})
			continue // lenient to blank lines, though the writer never emits them
		}

		req, decErr := ipc.DecodeRequest(line)
		if decErr != nil {
			if lineStart >= announced { // else an earlier pass counted and announced it (spoolMemo.readTo)
				if dr.cfg.Metrics != nil {
					dr.cfg.Metrics.Counter(counterDrainFileError).Add(1)
				}
				dr.cfg.Log.Warn("daemon: drain: corrupt line", "path", path, "err", decErr)
			}
			gaps.add(base, DrainGapCorruptLine, "line did not decode")
			consume(lineStart, consumedLine{next: nextOffset, sum: sum, gaps: gaps.takeLine()})
			continue
		}

		// Privacy admission for an INHERITED record. A record that already carries a decision keeps
		// it — re-deciding could only work from the derived Event, which cannot restore what the
		// first policy removed. A record that carries none is decided here, before it is dispatched
		// and therefore before anything it would cause can be persisted.
		verdict := dr.admitLine(req)
		// A previous lease can survive a later policy change. A proven denial
		// retires it durably before the offset; uncertainty retains the source.
		var retired deliveryLease
		retiredHere := false
		if verdict.Denied || verdict.Failed {
			lease, held, err := dr.existingLease(req)
			if err == nil && held && verdict.Denied {
				// Only a retirement this pass makes is news to the session's lane (DrainConfig.Released).
				already, terr := terminalForDelivery(dr.cfg.Journal, lease)
				retiredHere = terr != nil || !already
				err = dr.retireDeniedDelivery(ctx, lease)
			}
			if err != nil || (held && verdict.Failed) {
				if dr.cfg.Metrics != nil {
					dr.cfg.Metrics.Counter(counterDrainLeasedDenyPending).Add(1)
				}
				gaps.add(base, DrainGapUnadmitted, "refused replay has unresolved delivery identity or policy")
				if !held {
					leave(lineStart) // the lookup failed: a later pass may find no lease, and skip the line
				}
				continue
			}
			retired = lease
		}
		switch {
		case verdict.Denied:
			gaps.add(base, DrainGapDenied, verdict.Reason)
			notePendingBlob(fs, pendingBlobOf(dr.cfg.Root, req))
			if retiredHere {
				notePassConsumed(ctx) // retired by this pass, in order or not
			}
			consume(lineStart, consumedLine{next: nextOffset, sum: sum, gaps: gaps.takeLine()})
			if retiredHere {
				dr.released(retired)
			}
			if err := reattempt(req.Session, false); err != nil {
				readErr = err
				break readLoop
			}
			continue
		case verdict.Failed:
			// A never-leased failure is terminal FOR THIS RECORD, and the offset advances past it; losing
			// it LOUDLY is correct while losing everything behind it silently is not. The refusal is not
			// always one the spooled bytes decide: a policy this daemon cannot compile, or a path whose
			// scope it cannot prove, is a condition of this process that can clear. The record is lost all
			// the same, as it is in order, and a line consumed out of order behind a waiting head is as
			// final (spoolMemo), so the announcement says what happened to it. Once per line, though: a
			// line an earlier pass consumed and announced, behind a front that has not reached it, is not
			// lost again by the next pass to read it.
			if lineStart >= announced {
				if dr.cfg.Metrics != nil {
					dr.cfg.Metrics.Counter(counterDrainUnadmitted).Add(1)
				}
				dr.cfg.Log.Loud("daemon: drain: capture not admitted; record skipped",
					"path", path, "reason", verdict.Reason)
			}
			gaps.add(base, DrainGapUnadmitted, verdict.Reason)
			consume(lineStart, consumedLine{next: nextOffset, sum: sum, gaps: gaps.takeLine()})
			continue
		case verdict.Degraded:
			// The policy decided and the decision is degraded. It is admitted exactly as the live
			// path admits it: dispatchPending publishes the sidecar, and runIngested withholds
			// only the Event it never had. Falling through is the whole point.
		}
		req = verdict.Request

		lease, leased := dr.leaseDelivery(ctx, req)
		if !leased {
			gaps.add(base, DrainGapUnleased, "delivery has no durable identity")
			if req.Nonce != "" && dr.cfg.Journal != nil {
				dr.cfg.Log.Loud("daemon: drain: delivery identity unavailable; spool retained for recovery")
				leave(lineStart)
				readErr = core.ErrDegraded
				break readLoop
			}
		}

		// The frontier, ordering, seen and dispatch stages, all through processOne. An unleased line
		// is never ordering-gated (leasedPredecessorsReady is true for it) and follows its existing
		// qualified path. See the frontier/seen rationale preserved in processOne.
		dl := deferredLine{
			req: req, lease: lease, leased: leased,
			key: deliveryIdentityKey(lease, leased, line), start: lineStart, next: nextOffset, sum: sum,
		}
		done, deferIt, dispatched, changed, err := processOne(dl)
		if !done && !leased {
			leave(lineStart) // deferred, or a hard error, with no lease to hold it
		}
		if err != nil {
			readErr = err
			break readLoop
		}
		if deferIt != deferNot {
			if deferIt == deferOrdering && dr.cfg.Metrics != nil {
				dr.cfg.Metrics.Counter(counterDrainOrderingDeferred).Add(1)
			}
			// Item 3: do NOT stop at the buffer bound — stopping re-reads the same prefix every pass
			// and never reaches a predecessor deeper in the file. Buffer this line's request only while
			// under the memory cap; past it, drop the buffered copy (no full-request allocation per
			// unbounded record) but KEEP SCANNING. The overflow line is not consumed — the offset never
			// passes an unresolved record — so a later pass re-reads it; meanwhile a ready predecessor
			// found further on IS dispatched and acknowledged this pass, which is what unblocks the
			// prefix on the next pass. Liveness is claimed only within these demonstrated bounds.
			if len(deferred) < orderingLookaheadBound {
				dl.gaps = gaps.takeLine()
				deferred = append(deferred, dl)
			}
			continue
		}
		if done {
			if changed {
				notePassConsumed(ctx) // published or retired by this pass, in order or not
			}
			consume(lineStart, consumedLine{next: nextOffset, sum: sum, gaps: gaps.takeLine()})
			settledSpooledPrompt(dl.req)
			if changed && dl.leased {
				dr.released(dl.lease)
			}
			if dispatched {
				count++
			}
			// Absorbing a line whose delivery another handler settled can release its session's
			// deferred lines as well as publishing or retiring it here: the settlement may be newer
			// than their deferral.
			if err := reattempt(dl.req.Session, false); err != nil {
				readErr = err
				break readLoop
			}
		}
	}
	// A deferred line can be released after the last line the pass consumes in its file, by an earlier
	// arrival of its session another handler publishes meanwhile (a live worker, a session end), and
	// nothing the pass consumes after it then re-attempts it. One more re-attempt of every deferred line
	// at the end of the file publishes it in this pass instead of leaving it to a later one, which, for
	// a client spool, the watcher would have taken for unconsumable and backed off.
	if !canceled && readErr == nil && len(deferred) > 0 {
		if err := reattempt("", true); err != nil {
			readErr = err
		}
	}
	_ = f.Close() // must happen before the delete-if-drained check below (Windows cannot remove an open file)

	// The progress names the bound the pass actually read to, never the stat, floored at what the drain
	// has already consumed. The bound and the stat differ for a WAL segment the ingest holds: the bytes
	// past its synced size are not on disk yet, and progress that named them outlived the machine crash
	// that took them. validateProgress then found a recorded size past the end of the file and refused
	// EVERY later Drain, for the WHOLE spool — healthy files included — with nothing an operator could
	// do about it.
	//
	// The floor is what keeps that same bound from wedging the spool the other way round. It is not
	// monotonic per file name: ingest.holdSynced enters EVERY segment the ingest opens into
	// ingest.synced at 0, including one already holding bytes an earlier pass consumed and recorded,
	// and a Sync that fails freezes it there for the life of that handle. Recording that 0 over an
	// Offset of N wrote {Size: 0, Offset: N}, which loadState refuses outright — so the very next pass
	// failed before it read a single spool file, permanently, and no machine crash was needed to get
	// there.
	//
	// The floor is the bound the record ALREADY HOLDS, not merely the offset the pass consumed. Those
	// differ whenever a pass stopped short of the bound — a handler NAK, a cancel, the idle budget —
	// and the segment was then reopened. Falling back to the offset there discarded a claim about
	// bytes that WERE durable when an earlier pass recorded them: validateProgress stopped refusing
	// when they went missing, so a truncation drew no refusal, the entry flipped to Done at the
	// shortened size, and bytes written over durable ones were delivered as the file's continuation.
	// A record no pass of this code has written is the one exception (drainFileState.SizeIsRawStat):
	// its Size is a raw stat that may name a held segment's unsynced tail, and keeping THAT would
	// inherit a bound the machine crash above legitimately falls below, re-creating the wedge. Such a
	// record is floored at the consumed offset instead. That offset is NOT itself a durable bound: a
	// binary with no durableEnd (develop's drain.go has none) read a held segment to EOF and recorded
	// what it consumed, so its Offset can name bytes no Sync ever returned for. Nothing here cures
	// that — loadState refuses a Size below Offset, so no floor can go lower — and the crash that
	// takes those bytes wedges the spool for that record exactly as it wedged the binary that wrote
	// it. What this pass can do is not LAUNDER the claim: the mark it writes says the recorded Size
	// names durable bytes, so it is set only where the pass's OWN bound carries that Size. A Size
	// resting on the offset floor of an unmarked record keeps that record's provenance instead, is
	// therefore still lowerable rather than frozen, and is marked by the first pass whose durable
	// bound reaches the offset. That bound is durableEnd's, which syncs nothing for a file with
	// nothing unread, so the mark is also written where the pass's bound merely EQUALS the inherited
	// offset; drainFileState.SizeIsRawStat says why that case costs nothing.
	//
	// The bytes above the recorded size are pending all the same, so the pass counts them in memory,
	// as it counts the unread bytes of a file it could not sync.
	floor := fs.Offset
	if !fs.SizeIsRawStat {
		floor = fs.Size // never below fs.Offset: loadState refuses any record where it is
	}
	fs.Size = max(end, floor)
	// end < fs.Size says the floor won, so this Size is not a bound the pass read to: an unmarked
	// record keeps its provenance, and a marked one was already durable and stays marked.
	fs.SizeIsRawStat = fs.SizeIsRawStat && end < fs.Size
	// Negative for a file that shrank under the pass: hold drops it, and Drain's end-of-pass loop
	// reports those bytes as pending from the record instead, which is where that case is answered.
	gaps.hold(base, size-fs.Size)
	// Before any save: a save that fails forgets every memo, this one included, since the cleanup
	// intents its lines left would then not be on disk (saveState).
	dr.rememberFile(base, fi, opened, synced, processed, memo, readPos, firstLeft, fs.Offset < size)
	if canceled {
		return count, passStopped(ctx)
	}
	if readErr != nil {
		// Persist the progress this pass DID make before surfacing the error. fs.Offset advances
		// only past a record that was fully dispatched and acknowledged (or explicitly accounted
		// for as a gap), so saving here can never release an undelivered record — while NOT
		// saving re-dispatches every line ahead of the failure on the next pass, forever.
		if serr := dr.saveState(st); serr != nil {
			dr.cfg.Log.Warn("daemon: drain: failed to persist progress after a read error", "err", serr)
		}
		return count, readErr
	}

	// Done must also imply Offset == Size, which loadState requires and removeCompletedFile acts on.
	// Consuming to the stat no longer establishes that on its own: the floor above can hold Size ABOVE
	// the stat this pass saw, for a file that shrank between validateProgress and that stat. Set from
	// the offset alone, such a pass would write {Done, Offset < Size} — a record no later pass could
	// load — and, worse, would call the file finished at a size below the durable bound its own record
	// names and hand it to removeCompletedFile, which unlinks it. The file stays unfinished instead;
	// its recorded bound still names bytes the file no longer has, so the next pass's validateProgress
	// refuses it, and that refusal clears if the file comes back.
	fs.Done = offset == size && fs.Size == offset // incomplete trailing bytes remain pending, even for ended sessions
	// Persisted here — per file, on EOF, before the cleanup and the remove — not just once at the
	// end of Drain (task-3-spec.md drain.go step 4's exact ordering): a crash anywhere after this
	// file's last line must not lose its recorded completion or its cleanup intents, which is what
	// makes "Drain is idempotent and resumable" true across a crash, not only across a clean
	// cancel. The completion is given up only for a removal, and removeCompletedFile persists that
	// before it unlinks.
	if serr := dr.saveState(st); serr != nil {
		dr.cfg.Log.Warn("daemon: drain: failed to persist state", "err", serr)
		return count, serr
	}
	// A pass that did not move this file's front cannot have released a cleanup intent anywhere: the
	// spool's references from the fronts on are what they were at the cleanup the pass began with, or at
	// the one after the last file whose front it moved. An intent it added here without moving the front
	// is a line's ahead of that front, which references it still. Cleaning up regardless read and decoded
	// every spool file again after each file, while any intent waited: F+1 reads of the spool per pass
	// over F files whose heads wait.
	if fs.Offset != offsetBefore {
		if err := dr.cleanupAcknowledged(st); err != nil {
			return count, err
		}
	}
	return count, dr.removeCompletedFile(path, base, fs, st)
}

// durableEnd returns how far into the file at path, which the stat at the top of the pass found size
// bytes long, the pass may read, once every byte before that point is durable. A machine crash after
// the pass leased a delivery whose bytes were not on disk yet would leave an orphan lease: a
// permanent open-lease GC root and an arrival hole, for a delivery whose hook died with the machine.
//
// For a wal-* segment the ingest holds, the point is the segment's synced size
// (DrainConfig.SyncedWAL), and the drain issues no I/O of its own: the ingest's Syncs cover the
// segment up to there, and a Sync still in flight covers nothing yet. Every other file is synced
// once, before the pass consumes any of it, and the sync covers everything the stat counted: a
// segment rotated away or closed by this daemon, one a crashed process left with lines it never
// synced still in the page cache, or a client-*.ndjson spool, which the hook client never fsyncs. The
// pass's first such sync is followed by one of the spool directory, whose entries a file's fsync does
// not cover on POSIX. A file with nothing unread is not synced at all: the stat is returned as it
// stands, so a pass over an INHERITED record whose Offset already equals the stat writes the durable
// mark over a bound no Sync of this code covered (drainFileState.SizeIsRawStat says why that is
// harmless — it happens only where Size == Offset, which validateProgress checks either way). An
// error means the file's sync or the directory's failed, and nothing of the file may be consumed.
//
// The bound returned is not by itself what the pass records as the file's size: drainFileState.Size
// is the larger of this bound and the one the record already holds, so its progress names only bytes
// that were durable when some pass read them AND never gives up such a claim. That floor is not
// cosmetic: this bound can FALL between passes, because a segment the ingest reopens enters
// ingest.synced at 0 (drainFile says what both ways of mishandling that cost).
//
// durable says an earlier pass of this drainer synced this very file at this size and nothing has
// written to it since (spoolMemo: the same file, by identity, with the same size and modification
// time). Its bytes are on disk already, so the pass syncs neither it nor the directory again: the
// earlier pass's directory sync covered its entry. Without that, a spool whose head waits cost a sync
// on every pass, however often the pass found nothing new in it. synced reports that the bound
// returned is one a sync of this drainer covers, which a later pass may rely on the same way: false
// for a held segment, whose bound the ingest's Syncs cover, and for a file with nothing unread.
func (dr *drainer) durableEnd(path, base string, size, offset int64, durable bool) (end int64, synced bool, err error) {
	if _, isWAL := walSessionID(base); isWAL && dr.cfg.SyncedWAL != nil {
		if ingestSynced, held := dr.cfg.SyncedWAL(path); held {
			return min(size, ingestSynced), false, nil
		}
	}
	if size <= offset {
		return size, false, nil
	}
	if durable {
		return size, true, nil
	}
	if err := dr.syncFile(path); err != nil {
		return 0, false, fmt.Errorf("daemon: drain: spool file not durable: %w", err)
	}
	if !dr.dirSynced {
		// The hook that created a client spool never synced the entry naming it, and the ingest does
		// not sync a segment's either (design R16). One sync of the directory covers every file the
		// pass listed, since each existed before the listing.
		if err := dr.syncDir(filepath.Dir(path)); err != nil {
			return 0, false, fmt.Errorf("daemon: drain: spool directory not durable: %w", err)
		}
		dr.dirSynced = true
	}
	return size, true, nil
}

// spoolMemo is what one drainer remembers, between its passes, about a spool file that still has bytes
// past its consumed front (V6 close-out C1.13, wave 20). A budgeted pass that has made no progress is
// not stopped by its budget (withPassBudget), so it reads every spool to its end, and behind a head
// that waits on an earlier arrival of its session every pass read the same lines again. The lines an
// earlier pass consumed out of order were consumed again in full: admitted, their leases looked up,
// the delivery journal asked about them, their blobs read for their names and their names appended to
// the cleanup intents again, a corrupt or unadmitted line counted and announced again; and every
// waiting line was re-attempted after each. The file was synced and the progress rewritten each time.
// The pre-freeze audit measured such a pass, over 300 waiting lines and 600 consumed ones, at 102.9 s.
// With the memo a line already consumed costs a later pass its read and a lookup, and an unchanged file
// is not synced again, so a pass that makes no progress costs the lines still waiting and the reading.
//
// What it does not cover is said here once, and the docs that cite it say it too:
//   - It holds at most orderingProcessedCap lines per file, the bound drainFile keeps on the lines its
//     front may roll over. A line consumed out of order past that bound is consumed again in full by
//     every pass that reads it (admitted, its lease looked up, the journal asked), exactly as before
//     the memo, but it is not counted or announced again, unless a pass left a line ahead of it read,
//     unconsumed and unleased (readTo).
//   - A line consumed on an in-memory completion whose acknowledgement had not reached the committed
//     frontier (DrainGapUnacknowledged) is not remembered: the gap it reports can close, so every pass
//     consumes it again until it is consumed without one.
//   - It is in memory only: a new daemon reads every file in full once.
//
// A memo describes one file: it is used only while the file has the same identity (os.SameFile) and is
// no shorter than at the pass that wrote it. A spool file is only ever appended to, so a file that grew
// keeps the lines it had, and a line in the memo is skipped only when the bytes read at its offset end
// where it ended and have its sum, which also covers a line rewritten in place. A file replaced, shrunk,
// removed or rotated away gets no memo, and is read in full as before. Every memo is forgotten when
// state/drain.json holds anything other than what this drainer last wrote there, or a write of it
// fails, since the offsets and cleanup intents the memo relies on live in those bytes.
type spoolMemo struct {
	// stat is the file as the pass that wrote the memo found it, its identity loaded.
	stat os.FileInfo
	// synced says that at stat's size every byte of the file is on disk by a sync of this drainer
	// (durableEnd), so a pass that finds the file unchanged — same size and modification time — need
	// not sync it again.
	synced bool
	// consumed holds the lines the passes consumed out of order and left ahead of the front, by start
	// offset: what drainFile's processed map held at the end of the pass, with the lines an earlier
	// pass remembered past where this one stopped reading, bounded the same way (orderingProcessedCap).
	consumed map[int64]consumedLine
	// readTo is how far into the file the passes since the memo began have read, short of the first line
	// one of them read and left unconsumed under no lease the journal holds (drainFile's firstLeft). Each
	// pass counted and announced every corrupt or unadmitted line it consumed, so a line that starts before
	// readTo is not counted or announced again, whether the memo holds it or the cap left it out. A line
	// a pass read and left — its lease lookup failed, its lease could not be taken, it waited or failed
	// with no lease — was announced by none, and a later pass can consume it as unadmitted: readTo stops
	// short of it, so that pass announces it, and lines past it the cap left out are announced again. A
	// line rewritten in place before readTo is consumed afresh, by its sum, but not announced: no writer
	// does that.
	readTo int64
}

// consumedLine is what a pass remembers of a line it consumed out of order (drainFile's consume).
type consumedLine struct {
	// next is where the line after it starts.
	next int64
	// sum is spoolLineSum of the line's bytes, its newline included.
	sum uint64
	// gaps are the gaps consuming the line added to the pass's account, which every pass that consumes
	// it again adds again: DrainGaps reports what each pass finds in the replay.
	gaps []gapNote
}

// settled reports whether consuming the line again can only find what consuming it found. A line
// absorbed on an in-memory completion whose acknowledgement was not on the committed frontier yet
// (DrainGapUnacknowledged) is not: once the acknowledgement lands, consuming it reports no gap.
func (l consumedLine) settled() bool {
	for _, g := range l.gaps {
		if g.kind == DrainGapUnacknowledged {
			return false
		}
	}
	return true
}

// gapNote is one gap a line's consumption added to a pass's account (gapRecorder.add).
type gapNote struct {
	kind   DrainGapKind
	reason string
}

// memoOf returns this drainer's memo of the spool file base, if fi, the pass's stat of it, is the file
// the memo describes: the same file, no shorter. unchanged says the file also has the size and
// modification time the memo recorded. A memo that no longer describes the file is dropped.
func (dr *drainer) memoOf(base string, fi os.FileInfo) (memo *spoolMemo, unchanged bool) {
	m := dr.memo[base]
	if m == nil {
		return nil, false
	}
	if fi.Size() < m.stat.Size() || !os.SameFile(m.stat, fi) {
		delete(dr.memo, base)
		return nil, false
	}
	return m, fi.Size() == m.stat.Size() && fi.ModTime().Equal(m.stat.ModTime())
}

// rememberFile records, for the passes after this one, what the pass did with the spool file base:
// fi is the pass's stat of it and opened the stat of the handle it read, synced whether durableEnd's
// bound rests on a sync of this drainer, consumed the lines it consumed out of order, prev the memo the
// pass began with (memoOf), readPos where it stopped reading, and firstLeft where the first line starts
// that it read and left unconsumed under no lease (-1 for none). Only a file that still has bytes past
// its front (unconsumed) is remembered, and only when the file the pass read is the one it stat'd;
// anything else drops the memo. A pass that stopped short of the end — its budget spent after it made
// progress, a hard error — read none of prev's lines past readPos, and they stay remembered: the file
// is the same file, so they are as consumed as they were.
func (dr *drainer) rememberFile(base string, fi, opened os.FileInfo, synced bool, consumed map[int64]consumedLine,
	prev *spoolMemo, readPos, firstLeft int64, unconsumed bool,
) {
	if !unconsumed || opened == nil || !os.SameFile(fi, opened) {
		delete(dr.memo, base)
		return
	}
	lines := make(map[int64]consumedLine, len(consumed))
	for start, line := range consumed {
		if line.settled() {
			lines[start] = line
		}
	}
	readTo := readPos
	if prev != nil {
		for start, line := range prev.consumed {
			if start >= readPos && len(lines) < orderingProcessedCap {
				lines[start] = line
			}
		}
		readTo = max(readTo, prev.readTo)
	}
	if firstLeft >= 0 {
		// A line read and left unannounced, which a later pass may consume as unadmitted: readTo must not
		// pass it, even where an earlier pass read further, or that consumption is announced by no pass.
		readTo = min(readTo, firstLeft)
	}
	if dr.memo == nil {
		dr.memo = map[string]*spoolMemo{}
	}
	dr.memo[base] = &spoolMemo{stat: fi, synced: synced, consumed: lines, readTo: readTo}
}

// forgetMemos drops every spool memo, and what this drainer knows of state/drain.json's content.
func (dr *drainer) forgetMemos() {
	dr.memo = nil
	dr.stateOnDisk = nil
}

// spoolLineSum is the sum a memo keeps of a spool line's bytes (consumedLine.sum).
func spoolLineSum(line []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(line)
	return h.Sum64()
}

// notePendingBlob adds blob to fs's cleanup intents, unless it is "" or one of them already. Every pass
// that consumed again a blob line behind a waiting head appended its name again, and cleanupAcknowledged
// keeps every intent whose line is still ahead of the front, so state/drain.json grew by one entry per
// such line on every pass for as long as the head waited.
func notePendingBlob(fs *drainFileState, blob string) {
	if blob == "" || slices.Contains(fs.PendingBlobs, blob) {
		return
	}
	fs.PendingBlobs = append(fs.PendingBlobs, blob)
}

// pendingBlobOf names the blob req's descriptor externalizes, which is the cleanup intent consuming req
// leaves, or "" when req names none readBlob would accept. It checks what readBlob checks before it
// reads (a descriptor of the tool response, an event, a safe name, a regular file of the descriptor's
// size) and reads none of the blob. A line consumed without being published (absorbed, retired or
// denied) needs its blob's name, not its bytes; reading the whole response only to learn the name cost
// each such line a read of the blob, and a denied line a read of what the policy refused.
func pendingBlobOf(root string, req ipc.Request) string {
	if len(req.Raw) == 0 || req.Event == nil {
		return ""
	}
	var ref blobRef
	if json.Unmarshal(req.Raw, &ref) != nil || ref.Blob == "" || ref.Field != drainBlobToolResponse ||
		!safeBlobName(ref.Blob) {
		return ""
	}
	fi, err := os.Lstat(paths.Long(filepath.Join(paths.Of(root).Spool, ref.Blob)))
	if err != nil || !fi.Mode().IsRegular() || ref.Bytes < 0 || fi.Size() != int64(ref.Bytes) {
		return ""
	}
	return ref.Blob
}

// noteUnsynced announces, Loud, a spool file the drain could not make durable, the first time it fails
// since its last sync that succeeded. Drain warns of the file error on every pass. But a file the
// drain can read and cannot sync, such as one it may not open for writing, stays undrained for good,
// and so does its session's recovery marker: a hole that must not hide among those warnings. mu must
// be held.
func (dr *drainer) noteUnsynced(path, base string, err error) {
	if dr.unsyncedNoted[base] {
		return
	}
	dr.unsyncedNoted[base] = true
	dr.cfg.Log.Loud("daemon: drain: spool file cannot be made durable; none of it drains until it can",
		"path", path, "err", err)
}

// removeCompletedFile removes a fully drained file whose cleanup intents are all consumed, if
// shouldDelete allows it — and only if the file still ends where the drain stopped. fs.Offset is
// the consumed offset just persisted; the file's size at removal time is re-read rather than taken
// from the stat at the top of the pass, because a file can grow between that stat and here. A file
// that has grown, or a WAL segment the ingest still holds, is left for a later pass. Neither is an
// error: nothing is wrong with it, it simply is not finished yet.
//
// The file's progress entry is forgotten, durably, BEFORE the unlink. Forgetting it afterwards, in
// memory for the next saveState, let a crash in between keep {Done, Offset: S, Size: S} on disk for
// a file that was gone, and nothing retired it: loadState accepts it, the pass visits only files
// that exist, and every later save wrote it back. Once the name came back (the ingest reopening
// wal-<session>.ndjson after a restart mid-session, a straggler after SessionEnd reopening segment
// 0), a smaller file failed every Drain for the whole spool, one of the same size was removed
// undrained, and a larger one lost its first S bytes. Forgetting first moves the crash window to
// the safe side: a file with no entry, which the next pass reads again from offset zero. That is
// at-least-once, which the drain already collapses for every leased line through the committed
// frontier, and which the handlers tolerate for the rest. A removal that does not happen — refused
// or failed — puts the entry back and persists it again before the pass goes on.
//
// A WAL segment the ingest holds is left before any of that (DrainConfig.HoldsWAL). RemoveWAL would
// refuse it, and forgetting it only to put it back cost two state/drain.json writes on every pass
// over it and reopened, each time, the window in which a crash leaves the file with no entry.
func (dr *drainer) removeCompletedFile(path, base string, fs *drainFileState, st drainState) error {
	if !fs.Done || len(fs.PendingBlobs) > 0 || !dr.shouldDelete(base) {
		return nil
	}
	_, isWAL := walSessionID(base)
	if isWAL && dr.cfg.HoldsWAL != nil && dr.cfg.HoldsWAL(path) {
		return nil // RemoveWAL would refuse it: nothing to forget, nothing to put back
	}
	remove := dr.removeSpool
	if isWAL && dr.cfg.RemoveWAL != nil {
		remove = dr.cfg.RemoveWAL
	}
	delete(st, base)
	if err := dr.saveState(st); err != nil {
		st[base] = fs // nothing is removed until its forgetting is on disk
		return err
	}
	removed, err := dr.removeSpoolFile(remove, path, base, fs.Offset)
	if removed || errors.Is(err, os.ErrNotExist) {
		delete(dr.memo, base)
		return nil // gone, and already forgotten on disk
	}
	st[base] = fs
	if serr := dr.saveState(st); serr != nil {
		return errors.Join(err, serr)
	}
	return err
}

// removeSpoolFile runs remove on the spool file path, named base, drained to drained, and brackets it
// in DrainConfig.ClientSpoolRemoving when base is a hook client spool.
func (dr *drainer) removeSpoolFile(remove func(string, int64) (bool, error), path, base string, drained int64) (bool, error) {
	if isClientSpoolName(base) && dr.cfg.ClientSpoolRemoving != nil {
		defer dr.cfg.ClientSpoolRemoving(base)()
	}
	return remove(path, drained)
}

// removeIfUnchanged removes path only if its size still equals drained, so bytes appended after a
// pass read to EOF are not deleted along with the file. With no writer-side exclusion it narrows the
// window rather than closing it: an append landing between the stat and the unlink is still lost on
// POSIX, while on Windows a writer that still holds the file makes the remove fail and a later pass
// retries. For a client-<pid>.ndjson file that residual is a hook process appending in that instant,
// or appending again after its earlier lines were drained (and, on POSIX, into a file already
// unlinked under it); the ingest's WAL segments do not rely on it (ingest.removeDrainedWAL).
func removeIfUnchanged(path string, drained int64) (bool, error) {
	fi, err := os.Stat(paths.Long(path))
	if err != nil {
		return false, err
	}
	if fi.Size() != drained {
		return false, nil
	}
	if err := os.Remove(paths.Long(path)); err != nil {
		return false, err
	}
	return true, nil
}

// syncSpoolFile is syncSpoolFileWith issuing the real fsync: what a drainer's syncFile does when
// nothing replaces its syncHandle, and what a test that replaces syncFile calls to keep the real sync.
func syncSpoolFile(path string) error { return syncSpoolFileWith(path, (*os.File).Sync) }

// syncSpoolFileWith makes every byte of the spool file at path durable, whichever process wrote it,
// by issuing fsync on a handle of its own: an fsync covers the file, not the handle it is issued on.
// It opens that handle for appending, creating and truncating nothing, because Windows refuses
// FlushFileBuffers on a handle without write access, and a read-only handle is all the pass reads
// with. The handle shares reading and writing, so a hook process still appending to its client spool,
// or the ingest to a segment it reopened, is not disturbed, and it is closed before the pass can reach
// a removal: Windows cannot delete a file that has a handle open.
func syncSpoolFileWith(path string, fsync func(*os.File) error) error {
	f, err := paths.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	if err := fsync(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// cleanupAcknowledged consumes only cleanup intents from a successfully persisted state. Every
// failed deletion remains in that state for the next drain/restart, even after the source ended.
func (dr *drainer) cleanupAcknowledged(st drainState) error {
	pending := false
	for _, fs := range st {
		pending = pending || len(fs.PendingBlobs) > 0
	}
	if !pending {
		return nil
	}
	// A lost transport ACK can leave the same descriptor in both WAL and client fallback.
	// A cleanup intent proves one consumed reference, not that every other reference is gone.
	referenced, err := dr.pendingBlobReferences(st)
	if err != nil {
		return err // incomplete/unreadable input cannot authorize collection
	}
	var result error
	for _, fs := range st {
		var remaining []string
		for _, blob := range fs.PendingBlobs {
			if referenced[blob] {
				remaining = append(remaining, blob)
				continue
			}
			if err := removeBlob(dr.cfg.Root, blob); err != nil {
				remaining = append(remaining, blob)
				result = errors.Join(result, err)
			}
		}
		fs.PendingBlobs = remaining
	}
	return result
}

// pendingBlobReferences checks the current spool snapshot only. A future delivery/lease ledger
// must also protect descriptors that a live client can publish after this scan.
func (dr *drainer) pendingBlobReferences(st drainState) (map[string]bool, error) {
	files, err := ipc.SpoolFiles(paths.Of(dr.cfg.Root).Spool)
	if err != nil {
		return nil, err
	}
	pending := make(map[string]bool)
	for _, fs := range st {
		for _, blob := range fs.PendingBlobs {
			pending[blob] = true
		}
	}
	refs := make(map[string]bool)
	for _, path := range files {
		if err := dr.scanBlobRefs(path, st[filepath.Base(path)], pending, refs); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

// scanPendingBlobs adds to refs every blob a line of the spool file at path references from its consumed
// offset (fs's, or the start of a file with no progress) on. pending names the cleanup intents waiting
// on the scan, which is all a line that does not decode is checked against.
func scanPendingBlobs(path string, fs *drainFileState, pending, refs map[string]bool) error {
	f, err := os.Open(paths.Long(path))
	if err != nil {
		return fmt.Errorf("daemon: drain: blob reference source unavailable")
	}
	defer func() { _ = f.Close() }()
	if fs != nil {
		if _, err := f.Seek(fs.Offset, io.SeekStart); err != nil {
			return err
		}
	}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, drainReadBufferBytes), ipc.MaxLineBytes+1)
	s.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if atEOF && len(data) > 0 && !bytes.ContainsRune(data, '\n') {
			return 0, nil, fmt.Errorf("daemon: drain: incomplete blob reference source")
		}
		return bufio.ScanLines(data, atEOF)
	})
	for s.Scan() {
		if len(bytes.TrimSpace(s.Bytes())) == 0 {
			continue
		}
		req, err := ipc.DecodeRequest(s.Bytes())
		if err != nil {
			// The drain consumes a line that does not decode as a corrupt line and dispatches nothing of
			// it. Failing the scan on it failed every pass's cleanup while any intent waited, counting and
			// logging a file error for each file the pass finished, and collected nothing for as long as
			// the line stood ahead of a front (behind a waiting head, for good). It still holds back each
			// pending blob whose name it carries, as an encoder writes the name: a binary that decodes the
			// line may yet replay it.
			for blob := range pending {
				if bytes.Contains(s.Bytes(), []byte(blob)) || bytes.Contains(s.Bytes(), encodedBlobName(blob)) {
					refs[blob] = true
				}
			}
			continue
		}
		var ref blobRef
		if json.Unmarshal(req.Raw, &ref) == nil && ref.Blob != "" && ref.Field == drainBlobToolResponse {
			refs[ref.Blob] = true
		}
	}
	return s.Err()
}

// encodedBlobName is name as a JSON string's content, which escapes a character the plain name carries
// as itself (encoding/json escapes <, > and & by default).
func encodedBlobName(name string) []byte {
	b, err := json.Marshal(name)
	if err != nil || len(b) < 2 {
		return []byte(name)
	}
	return b[1 : len(b)-1]
}

// validateProgress refuses a pass whose progress no longer describes the spool: a file shorter than
// the bytes its entry names. Both comparisons are against durable bytes only, for every record this
// code wrote: Size is the largest durable bound one of its passes has read the file to, and every
// byte below it was on disk when that pass read it, so a file that comes back shorter either lost
// bytes it had made durable or is a different file under the same name — neither is progress to act
// on. A record carrying no durable-size mark is the residual (drainFileState.SizeIsRawStat): its
// Offset, which its Size can rest on, may name bytes a binary with no durable bound of its own read
// before any Sync returned for them, and a crash that takes those refuses here as a loss. That
// refusal is what the binary that wrote such a record already did, and it clears if the bytes return. The unsynced tail a machine
// crash takes is not in Size, which is what keeps that crash from being read as either (SP20-D1, R9);
// and Size does not fall back to the consumed offset when a reopened segment's bound does, which is
// what keeps a truncation of the durable bytes between them from being read as neither.
//
// The Offset comparison is defence in depth, not a second rule: drainFile never records a Size below
// Offset and loadState refuses any record with Offset > Size, so every record that reaches here has
// Offset <= Size, and the Size comparison has already covered it. A file truncated to strictly
// between the two is unwritable while that invariant holds, which is itself the argument. It is kept
// because it is the comparison that fires first on a record whose Size had fallen below its Offset —
// the shape that wedged the spool before the floor — and because it states the narrower claim
// outright: nothing this drain already consumed may be gone.
//
// One such file refuses the whole pass, the spool's healthy files included: progress that no longer
// matches the spool cannot authorize an offset or a deletion anywhere in it.
func (dr *drainer) validateProgress(files []string, st drainState) error {
	for _, path := range files {
		fs := st[filepath.Base(path)]
		if fs == nil {
			continue
		}
		fi, err := os.Stat(paths.Long(path))
		if err != nil || fs.Size > fi.Size() || fs.Offset > fi.Size() {
			return fmt.Errorf("daemon: drain: progress no longer matches spool")
		}
	}
	return nil
}

// noteWedged announces a refused drain-progress state, once per wedge.
//
// It is finding F4-7. A stale state/drain.json wedges the spool: validateProgress refuses, Drain
// returns without consuming a byte, and every spooled delivery behind it stays stranded — reported
// only as a day-log Warn and as spool_files on the LIVE status snapshot, both of which vanish with
// the daemon. §13 invariant 10 wants it Loud and durable, because the operator action here is real:
// nothing will move until state/drain.json is dealt with.
//
// Once per wedge, not once per pass: the idle tick calls Drain repeatedly, and a Loud per tick would
// bury LOUD.log in one repeated fact. The flag clears on the first pass that gets through, so a
// wedge that comes back is announced again.
func (dr *drainer) noteWedged(files int, err error) {
	if dr.wedgeNoted {
		return
	}
	dr.wedgeNoted = true
	dr.cfg.Log.Loud("daemon: drain refused; spooled deliveries are stranded until state/drain.json "+
		"is replaced or removed",
		"spool_files", files, "err", err.Error())
}

// forgetReleased drops the progress entry of every released file — finished (Done), no cleanup
// intent left — whose base name is not in listed, the pass's spool listing, and persists st if it
// dropped one. Such an entry describes a file that is gone. removeCompletedFile forgets a file
// before it unlinks it, but state already on disk may predate that ordering: its crash window left
// an entry whose file was unlinked and whose save never happened, and the entry was then applied to
// whatever file later took the same name. A name missing from the listing proves the entry's file
// is gone even if the name comes back a moment later, which a stat taken now could not tell apart.
// An entry that still carries cleanup intents stays: they are the only record of blobs
// cleanupAcknowledged must still remove. So does an unfinished one, whose file the drain never
// removes.
func (dr *drainer) forgetReleased(st drainState, listed map[string]bool) error {
	forgot := false
	for base, fs := range st {
		if fs.Done && len(fs.PendingBlobs) == 0 && !listed[base] {
			delete(st, base)
			forgot = true
		}
	}
	if !forgot {
		return nil
	}
	return dr.saveState(st)
}

// unpersisted ends a pass that could not persist its own progress before draining a single file.
// Like progress it cannot read, that is a gap in the whole replay.
func (dr *drainer) unpersisted(gaps *gapRecorder, err error) (int, error) {
	gaps.add("", DrainGapProgressUnreadable, "drain progress could not be persisted")
	dr.publishGaps(gaps.state(dr.cfg.Clock, 0))
	return 0, err
}

// dispatchPending leaves the record and any externalized bytes available until handling and
// acknowledgement persistence succeed. A NAK, panic, or canceled handler cannot consume it.
// It enforces publication order for a drained record exactly as the ingest worker does for a live
// one: durable capture, then the reference the dispatch writes, then the committed frontier. The
// offset in drainFile advances only when this returns nil, so a delivery that did not reach the
// frontier is redelivered rather than silently released. A leased flush EndSession takes off the pass
// answers errSessionEndStarted, with its Seen entry (key) handed to the session end it started.
func (dr *drainer) dispatchPending(ctx context.Context, req ipc.Request, lease deliveryLease, leased bool,
	key core.Hash,
) (blob string, err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("daemon: drain: handler panicked")
		}
	}()
	resolved, blob, err := dr.readBlobBody(dr.cfg.Root, req)
	if err != nil {
		return "", err
	}
	verdict := dr.admitLine(resolved)
	if verdict.Denied {
		if leased {
			if err := retireDelivery(dr.cfg.Journal, ctx, lease); err != nil {
				return "", err
			}
		}
		return blob, errReplayDenied
	}
	if verdict.Failed {
		return "", core.ErrDegraded
	}
	resolved = verdict.Request
	// The pass's budget stays with the pass (withoutPassBudget): what the line is handed to below, a
	// session end or the handler, runs under its own bounds.
	lineCtx := withoutPassBudget(ctx)
	if leased && resolved.Op == ipc.OpFlush && dr.cfg.Seen != nil && dr.cfg.EndSession != nil &&
		dr.cfg.EndSession(lineCtx, resolved, key, lease) {
		return "", errSessionEndStarted
	}
	// Only an observation has a capture to publish. A control line — a session start, checkpoint or
	// SessionEnd flush whose hook fell back to its client spool, or a flush the daemon accepted into
	// its WAL — is leased like any delivery (the hook client mints a nonce for every hook) and reaches
	// the committed frontier below, but it is not an observation and nothing ever references a
	// sidecar for it. Publishing one left a sidecar the publication audit and fsck could only call
	// "unrecognized" for the life of the project (store.IsControlCaptureOp keeps the ones already on
	// disk as the legacy evidence they are).
	if leased && resolved.Op.HotPath() {
		if err := publishCapture(dr.cfg.Root, resolved, lease); err != nil {
			return "", fmt.Errorf("daemon: drain: capture not durable: %w", err)
		}
	}
	dctx, cancel := context.WithTimeout(lineCtx, drainLineDeadline)
	defer cancel()
	if leased {
		// A replayed flush settles only the arrivals before its own (sessionEndArrival).
		dctx = withReplayedDelivery(dctx, lease)
	}
	resp := dr.cfg.Dispatch(observer.WithObservation(dctx, lease.ObservationID), resolved)
	if !resp.OK || resp.Err != "" {
		if dctx.Err() != nil {
			return "", dctx.Err()
		}
		return "", fmt.Errorf("daemon: drain: handler did not acknowledge delivery")
	}
	if err := dr.commitDelivery(ctx, lease, leased); err != nil {
		return "", err
	}
	return blob, nil
}

// shouldDelete reports whether a fully-drained file should be removed: a client-*.ndjson fallback
// file always may be; a wal-<session>*.ndjson file may be only once IsLive reports the session is
// no longer live.
func (dr *drainer) shouldDelete(base string) bool {
	if sess, ok := walSessionID(base); ok {
		return !dr.cfg.IsLive(sess)
	}
	return true
}

// walSessionID extracts the session id from a wal-<session>.ndjson or
// wal-<session>.<rotationSeq>.ndjson base name. ok is false for anything else (a client-*.ndjson
// file, or an unrecognized name).
func walSessionID(base string) (core.SessionID, bool) {
	if !strings.HasPrefix(base, drainWalPrefix) || !strings.HasSuffix(base, drainFileExt) {
		return "", false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(base, drainWalPrefix), drainFileExt)
	if idx := strings.LastIndex(mid, "."); idx >= 0 {
		if _, err := strconv.Atoi(mid[idx+1:]); err == nil {
			mid = mid[:idx]
		}
	}
	if mid == "" {
		return "", false
	}
	return core.SessionID(mid), true
}

// drainStatePath returns <root>/.qompack/state/drain.json.
func drainStatePath(root string) string {
	return filepath.Join(paths.Of(root).State, drainStateFile)
}

// loadState distinguishes a first drain from unreadable or inconsistent progress. Corrupt state
// requires an explicit recovery decision; treating it as success could delete unread records.
//
// "No state yet" is ENOENT and also ENOTDIR: a state path whose parent is not a directory holds
// no progress record, and the platforms disagree about which errno that is — Windows reports a
// path through a regular file as not found, POSIX as ENOTDIR. Reading both as a first drain keeps
// the two on one sequence: the drain dispatches, saveState reports the real failure, the spool
// stays, and the acknowledged line is redelivered — which is what
// TestDrainStatePersistenceFailurePreservesSpool pins, and what the V5 close-out's first Linux and
// macOS run found only Windows doing.
func (dr *drainer) loadState() (drainState, error) {
	b, err := os.ReadFile(paths.Long(drainStatePath(dr.cfg.Root)))
	if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
		return drainState{}, nil
	}
	if err != nil {
		return nil, err
	}
	var st drainState
	if err := json.Unmarshal(b, &st); err != nil || st == nil {
		return nil, fmt.Errorf("daemon: drain: invalid progress state")
	}
	for _, fs := range st {
		if fs == nil || fs.Size < 0 || fs.Offset < 0 || fs.Offset > fs.Size || (fs.Done && fs.Offset != fs.Size) {
			return nil, fmt.Errorf("daemon: drain: inconsistent progress state")
		}
		for _, blob := range fs.PendingBlobs {
			if !safeBlobName(blob) {
				return nil, fmt.Errorf("daemon: drain: invalid cleanup intent")
			}
		}
	}
	return st, nil
}

// stateIsOwn reports whether st, the progress a pass loaded, is the progress this drainer last wrote:
// it marshals to the bytes of that write (drainer.stateOnDisk). Anything else was written, removed or
// replaced by something other than this drainer since.
func (dr *drainer) stateIsOwn(st drainState) bool {
	if dr.stateOnDisk == nil {
		return false
	}
	b, err := json.Marshal(st)
	return err == nil && bytes.Equal(b, dr.stateOnDisk)
}

// saveState persists st to state/drain.json via paths.WriteAtomic. WriteAtomic renames its temp
// file onto the destination, which requires the destination's parent directory to already exist —
// a fresh project that has never had .qompack/state/ created (paths.EnsureLayout not yet run)
// would otherwise fail the rename silently on every call.
//
// It writes nothing when st marshals to exactly the bytes this drainer last wrote there, and the pass
// found that progress there (drainer.stateOnDisk, stateIsOwn): it is on disk already, made durable by
// that write. A pass that changed no progress therefore rewrites none, where it used to rewrite the
// file once per spool file it finished and once more at its end, a write and its syncs each. A write
// that fails forgets what the drainer knows of the file's content, so the next save writes, and every
// memo, whose lines' cleanup intents that write was to carry (spoolMemo).
func (dr *drainer) saveState(st drainState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if dr.stateOnDisk != nil && bytes.Equal(b, dr.stateOnDisk) {
		return nil
	}
	p := drainStatePath(dr.cfg.Root)
	if err := os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700); err != nil {
		dr.forgetMemos()
		return err
	}
	if err := paths.WriteAtomic(p, b, 0o600); err != nil {
		dr.forgetMemos()
		return err
	}
	dr.stateOnDisk = b
	return nil
}

// ---------------------------------------------------------------------------
// Drain gap state (M2-02's "index/drain gaps", produced here)
//
// internal/negknow can answer "absent" only for a question whose evidence it actually has. It
// deliberately does not model index/drain gaps, because the gap lives here: a spool file the drain
// has not finished, a line it could not admit, a delivery it could not lease or acknowledge. This
// is the producer side of that fact, exposed so a caller can tell "nothing was recorded" from "we
// cannot currently tell".

// counterDrainUnadmitted counts spool records the drain skipped because privacy admission could
// not decide them. It is declared here, beside the gap vocabulary it accompanies, because the two
// are read together: the counter says how often, the gap says which file and why.
const counterDrainUnadmitted = "drain_unadmitted"

// DrainGapKind is the closed set of reasons a drain cannot account for part of the spool.
type DrainGapKind string

const (
	// DrainGapCorruptLine is a spool line that would not decode. It was skipped; its content is
	// unrecoverable and its delivery is unaccounted for.
	DrainGapCorruptLine DrainGapKind = "corrupt_line"
	// DrainGapUnadmitted is a line privacy admission could not decide. Nothing was persisted and
	// nothing will be: the record carries its own capture, so no later pass sees anything
	// different. The offset advances past it so the records behind it are still delivered.
	DrainGapUnadmitted DrainGapKind = "unadmitted"
	// DrainGapDenied is a line privacy policy refused. Nothing was persisted and nothing will be:
	// this is a decision, not an outage, and it is reported so it is never read as coverage.
	DrainGapDenied DrainGapKind = "denied"
	// DrainGapUnleased is a delivery that reached the drain with no durable identity — no nonce,
	// or no journal to lease from.
	DrainGapUnleased DrainGapKind = "unleased"
	// DrainGapUnacknowledged is a delivery whose publication did not reach the committed frontier.
	// Its spool offset was not advanced, so it will be redelivered.
	DrainGapUnacknowledged DrainGapKind = "unacknowledged"
	// DrainGapPending is a spool file with bytes still unread when the pass ended.
	DrainGapPending DrainGapKind = "pending"
	// DrainGapUnsynced is a spool file whose unread bytes could not be made durable: the sync the
	// drain issues before it consumes a file the ingest does not hold failed. Nothing of the file was
	// leased, dispatched or consumed in that pass, and the next pass syncs it again.
	DrainGapUnsynced DrainGapKind = "unsynced"
	// DrainGapProgressUnreadable is drain state this process refused to act on at all. It is the
	// strongest form of "cannot currently tell": no file was consulted.
	DrainGapProgressUnreadable DrainGapKind = "progress_unreadable"
)

// DrainGap is one accounted-for hole in the replay.
type DrainGap struct {
	File   string       `json:"file,omitempty"`
	Kind   DrainGapKind `json:"kind"`
	Count  int          `json:"count"`
	Reason string       `json:"reason,omitempty"`
}

// DrainGapState is the drain's own answer to "is the record complete?".
//
// Observed distinguishes the two cases callers must never merge: false means no drain has run in
// this process, so the answer is UNKNOWN and no absence claim may rest on it; true with no gaps and
// Complete set means the spool was fully replayed and acknowledged.
type DrainGapState struct {
	Observed     bool           `json:"observed"`
	Complete     bool           `json:"complete"`
	PendingBytes int64          `json:"pending_bytes"`
	Gaps         []DrainGap     `json:"gaps,omitempty"`
	UpdatedAt    core.UnixMilli `json:"updated_at,omitempty"`
}

// GapReporter is the optional seam a caller uses to read DrainGapState off a running Daemon. It is
// deliberately NOT a method on the Daemon interface: every existing implementation of that
// interface would otherwise have to grow one, and this is a diagnostic, not part of the contract a
// daemon must satisfy to run. Use the same guarded assertion observer_ops.go uses for Persister.
type GapReporter interface {
	// DrainGaps reports what the most recent replay could and could not account for.
	DrainGaps() DrainGapState
}

// gapRecorder accumulates one pass's gaps.
type gapRecorder struct {
	gaps map[DrainGap]int
	// unsynced counts the unread bytes the pass found in files it could not make durable, past the
	// size their persisted progress records. The pass does not record their stat size (drainFile says
	// why), so the progress it persists does not show these bytes, yet they are pending all the same.
	unsynced int64
	// withheld counts, per file, the bytes the pass read no further than because they are not durable
	// yet: a held WAL segment's tail past its synced size. The progress the pass persists names the
	// durable bound it read to and not those bytes (drainFile says why), and they are just as pending.
	withheld map[string]int64
	// line collects the gaps added since startLine: the ones the line drainFile is on adds (takeLine).
	line []gapNote
}

// startLine begins collecting the gaps the next line adds.
func (g *gapRecorder) startLine() { g.line = nil }

// takeLine returns the gaps added since startLine, and collects no more of them.
func (g *gapRecorder) takeLine() []gapNote {
	line := g.line
	g.line = nil
	return line
}

// hold counts n of file's bytes as pending without persisting them. The pass could not read them, so
// its progress must not name them; that they are unread is a fact about the spool all the same.
func (g *gapRecorder) hold(file string, n int64) {
	if n <= 0 {
		return
	}
	if g.withheld == nil {
		g.withheld = map[string]int64{}
	}
	g.withheld[file] += n
}

func (g *gapRecorder) add(file string, kind DrainGapKind, reason string) {
	if g.gaps == nil {
		g.gaps = map[DrainGap]int{}
	}
	g.gaps[DrainGap{File: file, Kind: kind, Reason: reason}]++
	g.line = append(g.line, gapNote{kind: kind, reason: reason})
}

func (g *gapRecorder) state(clk core.Clock, pending int64) DrainGapState {
	st := DrainGapState{Observed: true, PendingBytes: pending, UpdatedAt: core.NowMilli(clk)}
	for k, n := range g.gaps {
		k.Count = n
		st.Gaps = append(st.Gaps, k)
	}
	sort.Slice(st.Gaps, func(a, b int) bool {
		if st.Gaps[a].File != st.Gaps[b].File {
			return st.Gaps[a].File < st.Gaps[b].File
		}
		if st.Gaps[a].Kind != st.Gaps[b].Kind {
			return st.Gaps[a].Kind < st.Gaps[b].Kind
		}
		return st.Gaps[a].Reason < st.Gaps[b].Reason
	})
	st.Complete = len(st.Gaps) == 0 && pending == 0
	return st
}

// GapState returns the most recent pass's accounting. A drainer that has never run answers
// Observed:false, which is the honest "cannot currently tell" — not an empty set of gaps.
func (dr *drainer) GapState() DrainGapState {
	dr.gapMu.Lock()
	defer dr.gapMu.Unlock()
	st := dr.gaps
	st.Gaps = append([]DrainGap(nil), dr.gaps.Gaps...)
	return st
}

func (dr *drainer) publishGaps(st DrainGapState) {
	dr.gapMu.Lock()
	defer dr.gapMu.Unlock()
	dr.gaps = st
}

// admitLine applies the daemon-side privacy gate to an inherited spool record. A drainer with no
// Admit function (a bare test drainer) admits everything, which is the behaviour that existed
// before this gate and keeps a fixture that never had a capture working unchanged.
func (dr *drainer) admitLine(req ipc.Request) admissionVerdict {
	if dr.cfg.Admit == nil {
		return admissionVerdict{Request: req}
	}
	return dr.cfg.Admit(req)
}

// leaseDelivery mirrors ingest.leaseDelivery: the SAME nonce takes back the SAME lease, which is
// what makes a redelivered record reuse its original identity instead of acquiring a second one.
func (dr *drainer) leaseDelivery(ctx context.Context, req ipc.Request) (deliveryLease, bool) {
	if dr.cfg.Journal == nil || req.Nonce == "" {
		return deliveryLease{}, false
	}
	j, err := dr.cfg.Journal()
	if err != nil || j == nil {
		return deliveryLease{}, false
	}
	lease, err := j.lease(ctx, req.Nonce, req.Session, deliveryRequestHash(req))
	if err != nil {
		dr.cfg.Log.Warn("daemon: drain: delivery lease unavailable", "err", err)
		return deliveryLease{}, false
	}
	return lease, true
}

// released records that the pass published or retired a line of lease's session, for
// releaseSessions to report when the pass ends. mu must be held.
func (dr *drainer) released(lease deliveryLease) {
	if dr.cfg.Released == nil {
		return
	}
	if dr.releasedSessions == nil {
		dr.releasedSessions = map[core.SessionID]struct{}{}
	}
	dr.releasedSessions[lease.Session] = struct{}{}
}

// releaseSessions tells DrainConfig.Released every session the finished pass recorded, once each,
// and forgets them. mu must be held.
func (dr *drainer) releaseSessions() {
	for sess := range dr.releasedSessions {
		dr.cfg.Released(sess)
	}
	dr.releasedSessions = nil
}

// commitDelivery writes the committed-frontier record for a drained delivery.
func (dr *drainer) commitDelivery(ctx context.Context, lease deliveryLease, leased bool) error {
	if !leased || dr.cfg.Journal == nil {
		return nil
	}
	j, err := dr.cfg.Journal()
	if err != nil || j == nil {
		return fmt.Errorf("daemon: drain: delivery journal unavailable for acknowledgement")
	}
	return j.acknowledge(ctx, lease.Delivery, lease.ObservationID, core.Hash{})
}

// acknowledgedDelivery reports whether the committed frontier already names this delivery.
func (dr *drainer) acknowledgedDelivery(lease deliveryLease, leased bool) bool {
	if !leased || dr.cfg.Journal == nil {
		return false
	}
	j, err := dr.cfg.Journal()
	if err != nil || j == nil {
		return false
	}
	return j.acknowledged(lease.Delivery)
}
