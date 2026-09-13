# SP20-D1 — Final design: a group-committed durable delivery path with an in-place A/B seal

> **Committed at the V5 close-out (2026-09-13), unchanged from the text the implementation was
> built against.** It is preserved here because 30 files under `internal/daemon/`, `internal/cli/`
> and `test/` cite it 135 times by section number, and until this commit it lived only in a session
> scratch directory — so every one of those citations named a document no reader could open.
> Designs A and B, referenced in the next paragraph, were the two candidate drafts this document
> merges and supersedes; they are not committed, and nothing in the tree cites them. Where this
> text and the shipped code disagree, the code and `plans/V5-report.md` §31 are authoritative: §7.5's
> per-GOOS table is explicitly provisional, and §7.6's Q3 was an open question when this was written
> and has since been ruled on (§31.3.2).

This is the adversarial judge's merge of Design A (`sp20d1-design-A.md`) and Design B
(`sp20d1-design-B.md`). Repository read: `qompack-v5-final` @ `verify/v5-final` (`5708f38`).

Nothing was built, run or edited. Every latency below is one of two kinds:

- **measured**, with its source quoted;
- **predicted**, labelled as such.

§7.5 names the measurement that must replace every prediction before any value is written into
config. The last section, "Judge findings", lists every defect found in A and B and says how this
design resolves it.

## 0. Verdict

The final design keeps all three durability points and their order:

1. the WAL is synced;
2. then the lease journal is synced;
3. then the seal is durable;
4. only then is anything released to a caller, a job or the ACK.

It changes four things.

1. **Group commit on three independent pipelines, all built on one leader/follower queue.** The
   primitive is Design A's, with two defects fixed (J-A1, J-A2).
   - The pipelines are the WAL, the lease journal and the ack journal.
   - There are no timers, no linger and no new long-lived goroutines.
   - An isolated delivery runs exactly today's syscall sequence, inline on its own goroutine.
2. **Acknowledgements get their own pipeline off `Lock.mu`** (Design B's decoupling), so a lease
   batch never waits for an ack commit.
   - A per-journal state mutex and an in-flight counter keep `Release` from overtaking any write,
     sync or seal.
   - One shared fault poisons both pipelines, exactly as today (J-B2).
3. **The seal becomes an in-place A/B slot write** (Design B's format): a fixed 32 KiB file, held
   open for the journal's life.
   - It is read by a **strict** reader (B's Rule R is removed, J-B3).
   - Every seal is followed by a **post-seal path-identity check** before release (J-B5, new).
   - It ships as a two-step rollout, with a dual reader and a downgrade to v1 at a clean
     `Release`.
4. **Each batch runs in Design A's order:** evaluate (CPU only), one ownership check, one
   `checkFile`, one `Write`, one `Sync`, one seal plus the identity check, admission, release.
   - The gap between the check's last syscall and the append stays at microseconds (J-B4).
   - The set of leases released but not yet covered by a durable seal is empty at every instant,
     as it is today.

**Predicted latency on this Windows host (quiet AC):**

| | Today (measured) | Step 1: group commit, v1 seal | **Step 2: final, v2 seal** |
|---|---|---|---|
| Isolated leased `Accept` (`BenchmarkIngestAcceptLeased`) | 18.4–23.3 ms | 18–23 ms | **5.2–7.8 ms** (central 6.3) |
| bench-hotpath B-B p50 / p99, closed loop (deadline above p99, §7.1) | — | 20–24 / 25–35 ms | **6–8 / 9–14 ms** |
| bench-hotpath B-B p50 / p99 at today's 8 ms deadline (open loop) | 917.5 / 983.0 ms | 25–35 / 45–70 ms | 7–9 / 12–20 ms |
| 2 000 simultaneous `Accept`s, p50 / p99 (new benchmark) | ≈ 20 s / 40 s (serial) | ≈ 50 / 100 ms | **20–30 / 35–55 ms** |

On Linux the prediction is 0.8–4 ms isolated on local NVMe, with a closed-loop p99 of 2–7 ms; a CI
runner is predicted at 5–18 ms p99. macOS is unmeasured (§7.3).

**Recommended durable limits.** These are provisional until the §7.5 measurement replaces them:

| GOOS | `L0IngestMs` (B-B) | `AckDeadlineMs` |
|---|---|---|
| windows | **20** | **23** |
| linux | **15** | **17** |
| darwin | **40** | **45** (widest uncertainty) |

The rule behind them:

- `L0IngestMs = roundup5(1.25 × P)`, where `P` is the largest B-B p99 over three quiet
  bench-hotpath runs taken with the deadline high enough that the harness is closed-loop.
- `AckDeadlineMs = L0IngestMs + ceil(slack99)`, where `slack99` is measured by a new reported-only
  client-side row.

B-A, as gated, cannot see any of this (§7.4).

---

## 1. The current path, walked with file:line (every reference re-verified)

### 1.1 From the hook to the ACK byte

| # | Step | Where | Lock | Durable I/O |
|---|---|---|---|---|
| 1 | The hook stamps `req.TS` and derives its deadline from `State.AckDeadlineMs`: default 8 (`internal/config/defaults.go:118`), a `uint16` in `state.bin` (`internal/ipc/state.go:31`, `:322`, `:349`), floor 8 ms (`internal/cli/hookclient.go:111-116`, used at `:359-365`) | `internal/cli/hookclient.go` | — | — |
| 2 | `Send`: connect, `SetWriteDeadline(AckDeadline)`, write the line | `internal/ipc/client.go:215-273` (`:262`, `:265`) | — | — |
| 3 | `awaitACK`: `SetReadDeadline(AckDeadline)`, read 1 byte. On timeout, EOF or NAK it calls `spoolAndReturn` (carried defect SP05-D2) | `client.go:277-297` (`:278`, `:282-284`, `:289-293`) | — | client spool append |
| 4 | Daemon: a per-connection goroutine reads and decodes the frame | `internal/ipc/server.go:196-230` (`:208`, `:221`) | — | — |
| 5 | Dispatch | `server.go:232` → `:258-271` → `internal/daemon/handlers.go:192` `dispatchOp` | — | — |
| 6 | **`recvTS` is stamped here; B-A's clock stops** | `handlers.go:193` | — | — |
| 7 | Privacy admission, then `callHandler` → `acceptHotPathEvent`: `registry.Touch`, `ipc.EncodeRequest` | `handlers.go:203-237`, `:241`, `:439-456` | — | — |
| 8 | **`ingest.Accept`. B-B times the whole closure** | `handlers.go:457` → `internal/daemon/ingest.go:207-251`; `obs.Timed(histBB)` at `:242-243` | — | — |
| 8a | Trim the wire terminator | `ingest.go:215` | — | — |
| 8b | `appendWAL`: one mutex for every session; open or cache the segment; rotate past 64 MiB with a `Close` and no `Sync` (every earlier line was already synced one at a time); one `Write` | `ingest.go:257-296` (`:258`, `:266-270`, `:276-285`, `:287`) | `ingest.mu` | page cache |
| 8c | **Durability point 1: the WAL `Sync`** | `ingest.go:295` | `ingest.mu` | FlushFileBuffers / fsync |
| 8d | `leaseDelivery` → `i.journal()` = `daemon.deliveryJournal` → `Lock.openDeliveryJournal`, which calls `l.owned()` → `readLockFile` → `paths.ReadFileShared(run/daemon.lock)` **on every call** | `ingest.go:223`, `:544-561`; `daemon.go:297`, `:975-983`; `delivery_lease.go:81-95` (`:87`); `lock.go:239-245`, `:195-205` | `Lock.mu` | a lock-file read |
| 8e | `j.lease`: ctx; closed, fault, **`owned()` a second time** | `delivery_lease.go:158-169` (`:162`, `:164`, `:167`) | `Lock.mu` | a second lock-file read |
| 8f | validation, then **`checkFile`**. Position by path: `Lstat`, `Open`, `fstat`, `SameFile`, `ReadAll`, `Unmarshal`, canonical re-`Marshal` compare. Journal: `Lstat`, handle `fstat`, `SameFile` | `:170`, `:173` → `:362-376`, `:305-343` | `Lock.mu` | about 3 `CreateFile`s, one of them on a file created by a rename moments earlier |
| 8g | A known nonce returns the old lease after a binding check | `:177-182` | `Lock.mu` | — |
| 8h | A new nonce: bounds, arrival, `NewObservationID`, marshal, line bounds, second ctx check, `Write` | `:183-206` | `Lock.mu` | page cache |
| 8i | **Durability point 2: the journal `Sync`** | `:211` | `Lock.mu` | flush |
| 8j | chain; **durability point 3: `savePosition` → `paths.WriteAtomic`.** It runs `rootOf` twice, `MkdirAll`, `CreateTemp` in `.qompack/tmp`, `Write`, **temp `Sync`**, `Close`, `Chmod`, a POSIX-semantics replace (`replace_windows.go:93-`), **`fsyncDir`** (a no-op on Windows, `atomic.go:87-90`) and a deferred `Remove` | `:215-216` → `:294-303` → `internal/paths/atomic.go:105-140` | `Lock.mu` | temp flush, plus a directory fsync on POSIX |
| 8k | Only synced and sealed bytes enter the maps | `:220-224` | `Lock.mu` | — |
| 8l | Build the job; non-blocking ring send | `ingest.go:224-237` | — | — |
| 9 | `recordHotPathSample(req, recvTS)` | `handlers.go:246-251`, `:280-300` | — | — |
| 10 | **The ACK byte is written** | `server.go:245-251` | — | — |

**B-C contends for the same lock.** A worker runs `dispatch` → `publishCapture` → one more
`WriteAtomic` per delivery. It then runs `run`, then `commitDelivery` → `acknowledge` (`ingest.go:394-420`,
`:604-613`). `acknowledge` holds **`Lock.mu`** through `checkAckFile` (`delivery_lease.go:513`),
the ack `Write` and `Sync` (`:529-537`) and `saveAckPosition` → `WriteAtomic` (`:539`, `:631-640`).
The diagnosis records that a lease can wait behind a whole ack commit
(`plans/V2-WAVE1-carried-defects.md:238-249`).

### 1.2 What each budget actually times

- **B-B** (`l0_ingest`): gated at p99 against `L0IngestMs` = 2 (`defaults.go:177`;
  `internal/obs/budgets.go:83-87`). It times step 8 in full, including every lock wait. The row's
  text, "daemon read to WAL append returned" (`obs/budgets.go:20`,
  `internal/config/runtime.go:227`), has been stale since `f6a8691` and `9c023ac`.
- **B-A** (`hook_controlled`): gated at p99 against `HotPath.BudgetMs` = 15 (`defaults.go:122`).
  Its value is `recvTS − req.TS + hotPathTailAllowance (1 ms)` (`handlers.go:288-289`,
  `budget.go:26`), and `recvTS` is step 6.
  - bench-hotpath gates B-A on exactly this daemon histogram (`test/bench/hotpath/main.go:372-389`,
    `:439-446`).
  - **B-A contains neither `Accept` nor the ACK wait.**
  - The comment on `hotPathTailAllowance` (`budget.go:14-17`) says the 1 ms is "the ACK read plus
    process exit, after the daemon has stopped timing". That has been false since the durable
    `Accept` began to sit between the stopped clock and the ACK.
- **B-D** (`hook_wall`, reported only) is the only row that contains the ACK wait: p50 22.4 / p99
  28.8 ms over 2 000 spawns (`plans/V5-report.md:339-341`).
- **Co-load treatment.** bench-hotpath gates B-B even under `--under-coload`
  (`main.go:120-123`, `:446`). The stated reason is that B-B "contains no process spawn" and moved
  only 0.576 → 0.768 ms under co-load (`main.go:125-132`; ADR 0010 `docs/adr/0010-wall-clock-under-coload.md:18-22`).
  **That measurement predates `f6a8691`; with three flushes in the region the premise no longer
  holds** (§7.6).

### 1.3 Measured anchors

| Id | Value | Source |
|---|---|---|
| M1 | Unleased `Accept` (one append plus a flush): 1.93–2.19 ms quiet AC; 8.2–9.0 ms co-loaded | `V5-report.md:332`; `V2-WAVE1-carried-defects.md:258` |
| M2 | Leased `Accept`: 18.4–23.3 ms quiet AC; 37–42 ms co-loaded | `V5-report.md:332`; `V2-WAVE1…:242-244` |
| M3 | `BenchmarkPathsWriteAtomic_4KB`: 3.81 ms (baseline 3.1–3.2 ms) | `V5-report.md:329` |
| M4 | Co-loaded profile: three FlushFileBuffers ≈ 65 %, the position re-read ≈ 28 % | `V2-WAVE1…:243-245` |
| M5 | bench-hotpath: B-B p50 917.5 / p99 983.0 ms (n = 2064); B-A p99 3.07 ms | `V5-report.md:333` |
| M6 | bench-hotpath spawns hooks **one at a time**; each waits up to `AckDeadlineMs` and exits | `test/bench/hotpath/process.go:265-300` |

**Decomposition (derived, not measured).**

- Quiet: 3F ≈ 5.8–6.6 ms, taking F from M1.
- `WriteAtomic` minus its flush ≈ 1.2–2.4 ms (M3 − F).
- That leaves ≈ 10–15 ms unaccounted for. M4 attributes ≈ 28 % of the co-loaded cost to the
  position re-read, so the working hypothesis is that `checkFile` is dominated by opening **a file
  created by a rename moments earlier**. Likely causes are on-access scanning or NTFS metadata
  churn inflating the flushes.
- The step-2 seal removes both candidate causes, but the split must be measured first (M0, §7.5).

### 1.4 Why the burst queues today

With `AckDeadlineMs` = 8 and a leased `Accept` of about 20 ms, every hook gives up, spools and exits
(M6). The next hook arrives about 22 ms later, while `Lock.mu` is still owed a lease *and* an ack
per delivery (about 16–21 ms each). Demand exceeds the arrival rate, so the queue grows for the
whole burst: p50 1.4 s at 300 hooks and 2.6 s at 1 000 (`V2-WAVE1…:246-248`).

**The consequence that both designs missed (J-AB1).** Once `AckDeadlineMs` exceeds B-B's p99, every
hook waits for its ACK before the harness spawns the next one. The harness then becomes a
**closed loop with one outstanding delivery**, and B-B measures the service time plus interference
from the previous delivery's B-C work, not a queue.

---

## 2. The algorithm

### 2.1 Structure: goroutines, locks and channels

```
Goroutines — no new long-lived goroutine anywhere
  ipc handleConn (existing, one per connection)
      Accept → walQ.run (may lead one WAL batch) → accessor → leaseQ.run (may lead one lease batch)
  ingest workers (existing)
      dispatch → commitDelivery → ackQ.run (may lead one ack batch)
  drainer (existing, one at a time)
      leaseQ.run / ackQ.run / acknowledged()
  a WAL leader may start one short-lived goroutine per extra dirty segment for a parallel Sync,
      and joins them before its batch returns

Mutexes (acquire left before right; never the reverse)
  Lock.mu → j.st      accessor fast path, acknowledged(), closeLocked (Release and failed opens)
  ingest.mu           WAL leader for the length of its batch, CloseSession, Close; independent of the above
  ingest.syncedMu     a leaf; taken under ingest.mu by the WAL's writers, and alone by the drain's
                      SyncedWAL question. The drain's removal decisions (HoldsWAL, RemoveWAL) still
                      take ingest.mu
  q.mu                one per queue (walQ, leaseQ, ackQ); a leaf lock, never held with another
                      lock and never across I/O
  j.st                never held across I/O; no leader ever takes Lock.mu

Channels / conditions
  gcReq.done  chan struct{}    closed exactly once: "your result is final" or "you lead the next batch"
  j.idle      sync.Cond(&j.st)  broadcast when j.inflight drops to 0; closeLocked waits on it
```

### 2.2 What may overlap, and what may not

| Pair | Allowed? | Why |
|---|---|---|
| Syncs of different WAL segments in one batch | yes | fsync is per file, and sessions are unordered |
| WAL batch *k+1* ∥ lease batch *k* | yes | Different deliveries; each delivery still goes WAL-durable → lease-written |
| Lease batch ∥ ack batch | yes | An ack names only a lease sealed in an *earlier* lease batch. The caller holds a lease only after its seal, and the ack's lease match reads admitted state only (`delivery_lease.go:503-506`), so `loadAcks`' lease-exists check (`:621-627`) holds at every crash point |
| **One delivery's WAL Sync ∥ its own journal Write/Sync** | **no** | It would make reachable a durable lease whose delivery bytes never became durable (after a power loss the hook dies too). That is an orphan lease: a permanent open-lease GC root and an arrival hole. Rejected (X1). Before `515e472` the drain reached the state: it read live segments without `ingest.mu` and could lease a line whose covering Sync had not returned (review F6). Now `Accept` leases only after its WAL Sync returned (T8), the drain reads a segment the ingest holds only up to its synced size, and it syncs every other file before it consumes any of it (`TestDrainNeverLeasesAWALLineBeforeItsSyncReturns`, `TestDrainSyncsAFileTheIngestDoesNotHoldBeforeItsFirstLease`) |
| Journal Sync ∥ seal | **no** | A position ahead of its file poisons the journal (`ingest.go:187-194`); this is I3 |
| `checkFile` ∥ the same batch's WAL flush | no | It would widen the check-to-append gap from µs to one flush (X2) |
| Release of a lease ∥ its batch's seal | **no** | I3 |

### 2.3 The queue primitive (new file `internal/daemon/groupcommit.go`)

```go
type gcReq[T any] struct {
    item T
    done chan struct{} // closed once: result final, or "you lead"
    lead bool          // written under q.mu before done is closed; read after <-done
}

type groupQueue[T any] struct {
    mu       sync.Mutex // guards queue and leading ONLY; never held across I/O
    queue    []*gcReq[T]
    leading  bool
    maxN     int
    maxBytes int
    size     func(T) int // an upper-bound estimate; the head of the queue is always admitted
}

// run returns once item's result is final. The CALLER initialises every result field to a
// FAILURE before calling run; commit may set success only after durability (fix J-A2).
func (q *groupQueue[T]) run(item T, commit func([]T)) {
    r := &gcReq[T]{item: item, done: make(chan struct{})}
    q.mu.Lock()
    q.queue = append(q.queue, r)
    if q.leading {
        q.mu.Unlock()
        <-r.done
        if !r.lead {
            return
        }
        q.mu.Lock() // handed leadership: r is q.queue[0] (appenders only append; nobody else cuts)
    } else {
        q.leading = true // leading == false implies an empty queue, so r is q.queue[0]
    }
    batch := q.cutLocked() // longest FIFO prefix within maxN/maxBytes; removes it; batch[0] == r
    q.mu.Unlock()
    defer q.handoff(batch) // runs even if commit panics
    commit(itemsOf(batch))
}

func (q *groupQueue[T]) handoff(batch []*gcReq[T]) {
    q.mu.Lock()
    var next *gcReq[T]
    if len(q.queue) > 0 {
        next = q.queue[0] // two statements: `next, next.lead = q.queue[0], true` dereferences the
        next.lead = true  // OLD next (nil) in the second assignment phase, panics with q.mu held,
    } else { //              and deadlocks every later request (fix J-A1)
        q.leading = false
    }
    q.mu.Unlock()
    for _, b := range batch[1:] {
        close(b.done)
    }
    if next != nil {
        close(next.done)
    }
}
```

**Properties.**

- **Batching rule:** everything queued when the leader cuts, up to the caps. There is no wait for
  company. An isolated delivery is a batch of one, and the leader runs it inline with about a
  microsecond of bookkeeping.
- **Bounded wait:** a leader commits exactly one batch, the one containing its own request, then
  hands over to the FIFO head. A request waits for at most the in-flight batch plus the batches
  ahead of it at the caps. There is no starvation.
- **No lost request:** if `leading` is false at enqueue, the enqueuer leads. If it is true, the
  current leader's handoff wakes the head.
- **Caps:**
  - WAL: `maxN` 512 and `maxBytes` 4 MiB.
  - Leases: `maxN` 512 and 1 MiB, estimated as `6·len(session) + 400` per request, an upper bound
    on one canonical line including escapes.
  - Acks: `maxN` 512 and 1 MiB.
- **Panics:** the handoff still runs and the members keep their default failure. For the journal
  pipelines, the commit also recovers and poisons the handle (§2.5).

### 2.4 Stage W: WAL group commit (`internal/daemon/ingest.go`)

Design A's shape. `Accept` (`:207-251`) is unchanged; only `appendWAL` changes.

```go
type walItem struct {
    sess core.SessionID
    line []byte // trimmed; the WAL's one '\n' is added in the batch buffer
    err  error  // initialised to errWALNotCommitted; set to nil only after the covering Sync returned
}

func (i *ingest) appendWAL(sess core.SessionID, line []byte) error {
    it := &walItem{sess: sess, line: line, err: errWALNotCommitted}
    i.walQ.run(it, i.commitWALBatch)
    return it.err
}

func (i *ingest) commitWALBatch(batch []*walItem) {
    i.mu.Lock() // as today: serialises handles, rotation, CloseSession and Close
    defer i.mu.Unlock()
    // A segment buffer is bound to the *os.File it will be written to, never to the walFile,
    // because rotation swaps wf.w mid-batch (fix J-A7).
    type seg struct {
        f      *os.File
        wf     *walFile
        buf    []byte
        items  []*walItem
        failed error
        synced bool
    }
    // for each item, in queue order:
    //   wf := walForLocked(sess)                 // ingest.go:261-270 verbatim; an open error → it.err
    //   rotation decided per line with today's accounting (:276), counting this batch's pending
    //   bytes for wf. If it rotates:
    //     Write the old segment's pending buffer, Sync it BEFORE Close (a new durability point:
    //     those lines are not yet durable), Close, seq++, openWALLocked
    //   append line+'\n' to the current segment's buffer
    // then one Write per remaining segment (wf.bytes += n exactly as :291; a short write fails
    // that buffer's items with io.ErrShortWrite),
    // then one Sync per written, unsynced segment, in parallel when more than one (sync.WaitGroup.Go),
    // then, and only then: for every item, it.err = its segment's outcome (nil on success).
}
```

- **I1 holds:** `appendWAL` returns nil only after a completed Sync that covered that exact line.
  `Accept` returns only after `appendWAL`, and the ACK is written only after `Accept`
  (`server.go:232` → `:245`).
- **Bytes are unchanged:** the same line, the same single terminator, and the same segment
  boundaries as sequential appends. The rotation decision is made per line with the same
  accounting.
- **Failures are conservative:**
  - A write or sync failure fails every item of that segment's buffer. Each of them NAKs and its
    client spools, and the nonce-identity dedupe collapses the duplicate.
  - A later success never retroactively ACKs an earlier failure.
  - Short-write fragments that later merge with the next line are pre-existing (R17).
- **Why not B's per-file WAL:** B's leader syncs outside the lock with per-file condition
  variables. That lets writers to one session append during a sync, but it adds a new lock level,
  a `retired` loop and a rotation race (J-B6). In bench-hotpath there is one session, so A's
  simpler queue costs nothing measurable.

### 2.5 Journal state and the close protocol (`internal/daemon/delivery_lease.go`)

```go
type deliveryJournal struct {
    // Existing fields keep their names and meaning; tests read and swap them
    // (journal.writer, journal.file, journal.leases, journal.arrivals, journal.bytes, journal.chain).
    owner    *Lock
    path     string
    file     *os.File
    writer   deliveryJournalWriter
    bytes    int64
    leases   map[string]deliveryLease
    arrivals map[core.SessionID]uint64
    fault    error
    closed   bool
    chain    core.Hash
    ackPath, ackFile, ackWriter, ackBytes, ackChain, acks // unchanged

    // New.
    st       sync.Mutex    // guards fault, closed, closing, inflight and EVERY WRITE of admitted state
    idle     sync.Cond     // L = &st
    closing  bool
    inflight int
    leaseQ   groupQueue[*leaseReq]
    ackQ     groupQueue[*ackReq]
    seal     *deliverySeal // held v2 handle on delivery-lease-position.json; nil while writing v1
    ackSeal  *deliverySeal // held v2 handle on delivery-ack-position.json;   nil while writing v1
}
```

**Reading and writing rules** (these keep `-race` clean):

- The admitted lease state (`bytes`, `chain`, `leases`, `arrivals`) is written only by a lease
  leader, under `st`, at admission. Likewise the ack state (`ackBytes`, `ackChain`, `acks`) is
  written only by an ack leader.
- A leader may read its own pipeline's state without `st`. Only one leader per pipeline runs at a
  time, and the previous leader's writes happen-before the next leader's reads through `q.mu` and
  the `done` close.
- Every other reader holds `st`: the ack leader's lease lookup, `acknowledged()` and the accessor.
- Tests that poke `journal.arrivals` or swap `journal.writer` before calling `lease` are ordered by
  the queue mutex. Tests that read `j.leases` in a journal accessor
  (`delivery_publication_test.go:577`, `drain_reused_lease_test.go:85`) run on the goroutine that
  was the leader, or after its `done`.

```go
func (j *deliveryJournal) enter() error { // once per batch: today's :167 gate minus owned()
    j.st.Lock(); defer j.st.Unlock()
    if j.closing || j.closed || j.fault != nil { return deliveryJournalError() }
    j.inflight++
    return nil
}
func (j *deliveryJournal) leave() {
    j.st.Lock(); j.inflight--; if j.inflight == 0 { j.idle.Broadcast() }; j.st.Unlock()
}
func (j *deliveryJournal) poison(err error) error { // ONE fault for both pipelines (fix J-B2)
    j.st.Lock(); defer j.st.Unlock()
    if j.fault == nil { j.fault = err }
    return j.fault
}
func (j *deliveryJournal) usable() bool {
    j.st.Lock(); defer j.st.Unlock()
    return !j.closing && !j.closed && j.fault == nil
}

// closeLocked: Lock.mu held (Release, and the failed-open paths :130-152). Same contract as today.
func (j *deliveryJournal) closeLocked() error {
    j.st.Lock()
    if j.closed { j.st.Unlock(); return nil }
    j.closing = true                     // no later batch passes enter(); queued requests fail there
    for j.inflight > 0 { j.idle.Wait() } // Release never overtakes an in-flight write, sync or seal
    j.st.Unlock()
    if err := j.writer.Close(); err != nil { return j.poison(deliveryJournalError()) }   // :383-386
    if w := j.ackWriter; w != nil {                                                        // :389-395
        j.ackWriter = nil
        if err := w.Close(); err != nil { return j.poison(deliveryJournalError()) }
    }
    j.closeSeals()       // v2 handles; each set to nil after its close; idempotent
    j.downgradeIfClean() // step 2 only: fault == nil && owner.ownedByFile() → WriteAtomic v1, both seals
    j.st.Lock(); j.closed = true; j.st.Unlock()
    return nil
}
```

**Deadlock check.**

- `Release` holds `Lock.mu` and waits on `j.idle`. An in-flight leader needs only `j.st` (released
  inside `Wait`), its own I/O and a lock-file read. No leader takes `Lock.mu`.
- `acknowledged()` takes `Lock.mu` → `j.st`, the same order as `closeLocked`.
- No path takes `j.st` and then `Lock.mu`.

**Ownership.**

- `Lock.ownedByFile()` (new in `lock.go`) is `owned()` without the `l.released` read. It is safe
  inside a batch because `closing` is set before `Release` writes `released` (`lock.go:279`, `:297`).
- `owned()` itself is unchanged and is still called only under `Lock.mu`. This is fix J-B1:
  Design B called `owned()` from `j.ack.st` without `Lock.mu`, which races with `Release`.

**The accessor fast path** (`openDeliveryJournal`, `:85-95`) changes as follows. This is O1,
adopted and flagged for a countersign (Q6):

```go
l.mu.Lock(); defer l.mu.Unlock()
if l.released || l.owner == "" || l.journalOpenFault { return nil, deliveryJournalError() }
if l.journal != nil {
    if !l.journal.usable() { return nil, deliveryJournalError() }
    return l.journal, nil // no lock-FILE read here: every operation on the returned journal
}                         // re-checks file ownership per batch before it appends or releases
if !l.owned() { return nil, deliveryJournalError() } // opening: the full check, as today (:87)
// ... open path (§2.9) ...
```

**Why O1.** The accessor's per-call lock-file read runs under `Lock.mu` for every `Accept`. Under a
simultaneous burst that is a serial section of N·G, about 60–300 ms at N = 2 000 on Windows. It
sits outside any group commit, and neither design accounted for it (J-AB2).

**Why O1 keeps detection.** Every operation that uses the returned journal is `lease`, `acknowledge`
or `acknowledged`:

- the first two re-check file ownership once per batch, after every member arrived and before
  anything is appended or released;
- `acknowledged` keeps its own `owned()`.

The in-memory `released` check stays in the accessor. `TestDeliveryJournal_IsSingletonPerLockAndStopsWithItsLock`
and `…ReplacedLockCannotReleaseOrLeaseForNewOwner` pass unchanged; the second one's old journal is
refused by the per-batch check.

### 2.6 Stage L: the lease pipeline

`j.lease(ctx, delivery, session, request)` keeps its signature and blocking contract, so
`ingest.leaseDelivery` (`ingest.go:554`), `drainer.leaseDelivery` (`drain.go:756`), the benchmark
and every test call it unchanged.

```go
type leaseReq struct {
    ctx      context.Context
    delivery string
    session  core.SessionID
    request  core.Hash
    lease    deliveryLease
    err      error   // initialised to deliveryJournalError(); final when run returns
    pend     verdict // Phase 1 decision, resolved in Phase 5
}

func (j *deliveryJournal) lease(ctx context.Context, d string, s core.SessionID, h core.Hash) (deliveryLease, error) {
    if j == nil || j.owner == nil { return deliveryLease{}, deliveryJournalError() }
    r := &leaseReq{ctx: ctx, delivery: d, session: s, request: h, err: deliveryJournalError()}
    j.leaseQ.run(r, j.commitLeases)
    return r.lease, r.err
}

func (j *deliveryJournal) commitLeases(batch []*leaseReq) {
    gate := j.enter()                           // today :167, once per batch, after every member arrived
    if gate == nil {
        defer j.leave()
        if !j.owner.ownedByFile() { gate = deliveryJournalError() } // no fault, as today
    }
    defer j.recoverBatch()                      // panic → poison; unresolved members keep their default error

    // Phase 1 — PURE CPU, queue order, today's per-call order :164 → :167 → :170 → :177-205.
    size, count, chain := j.bytes, len(j.leases), j.chain
    next := map[core.SessionID]uint64{}         // arrivals assigned in this batch
    fresh := map[string]*mint{}                 // nonces minted in this batch
    var order []*mint
    var buf []byte
    var checked []*leaseReq                     // every request whose outcome is gated by Phase 2
    for _, r := range batch {
        if err := r.ctx.Err(); err != nil { r.err = err; continue }                             // :164
        if gate != nil { r.err = gate; continue }                                               // :167
        if !validDeliveryToken(r.delivery) || r.request.IsZero() || !utf8.ValidString(string(r.session)) {
            r.err = core.ErrContract; continue                                                  // :170
        }
        checked = append(checked, r)
        if old, ok := j.leases[r.delivery]; ok { r.pend = known(old); continue }                // :177-182
        if m, ok := fresh[r.delivery]; ok { r.pend = joins(m); continue }                       // concurrent redelivery
        prev, ok := next[r.session]; if !ok { prev = j.arrivals[r.session] }
        if count >= deliveryLeaseMaxEntries || prev == math.MaxUint64 { r.pend = fails(core.ErrBudget); continue } // :183
        id, err := core.NewObservationID(r.session, prev+1)
        if err != nil { r.pend = fails(core.ErrContract); continue }                           // :187-190
        l := deliveryLease{Version: core.EvidenceVersion, Delivery: r.delivery, Session: r.session,
            RequestHash: r.request, ArrivalSeq: prev + 1, ObservationID: id}
        line := canonicalLine(l)                                                                // :195-199
        if len(line) > deliveryLeaseMaxLine || size+int64(len(line)) > deliveryLeaseMaxBytes {
            r.pend = fails(core.ErrBudget); continue                                            // :200-202
        }
        if err := r.ctx.Err(); err != nil { r.pend = fails(err); continue }                    // :203
        next[r.session], count, size, chain = prev+1, count+1, size+int64(len(line)), deliveryChain(chain, line)
        buf = append(buf, line...)
        m := &mint{lease: l}; fresh[r.delivery] = m; order = append(order, m); r.pend = mints(m)
    }

    // Phase 2 — ONE checkFile, immediately before the append (today :173). Only the call itself
    // separates its last syscall from the Write (fix J-B4: B ran it BEFORE ~ms of Phase-1 CPU).
    if len(checked) > 0 {
        if err := j.checkFile(); err != nil {
            f := j.poison(err)
            for _, r := range checked { r.err = f }
            return
        }
    }

    // Phase 3 — ONE Write, ONE Sync, ONE seal (step 2: + post-seal path identity), in that order.
    if len(order) > 0 {
        if n, err := j.writer.Write(buf); err != nil || n != len(buf) { j.failCommit(checked); return } // :206-210
        if err := j.writer.Sync(); err != nil { j.failCommit(checked); return }                         // :211-214
        if err := j.sealLease(size, count, chain); err != nil { j.failCommit(checked); return }         // :216-219
        // Phase 4 — admission: only synced AND sealed rows enter the identity maps (:220-224).
        j.st.Lock()
        j.bytes, j.chain = size, chain
        for _, m := range order { j.leases[m.lease.Delivery] = m.lease; j.arrivals[m.lease.Session] = m.lease.ArrivalSeq }
        j.st.Unlock()
    }

    // Phase 5 — resolve. Nothing has been released yet; the queue wakes followers only after return.
    for _, r := range checked {
        r.lease, r.err = r.pend.resolve(r) // known: old lease, or ErrAppendOnly on a binding mismatch;
    }                                      // mints/joins: the minted lease, or ErrAppendOnly on mismatch;
}                                          // fails: its error
```

- **`failCommit(checked)`** poisons the journal with `deliveryJournalError()`. It then sets that
  fault on every member whose outcome depends on the commit: minters, joiners, and joiners with a
  binding mismatch. The sequential order would have hit the fault check at `:167` before the
  binding check at `:178`.
- **Known-nonce and budget members resolve normally.** Their answers never depended on this
  append. The linearisation places them before the failing append.
- **`sealLease`:** step 1 calls today's `savePosition(size, count, chain)` (v1, `WriteAtomic`).
  Step 2 calls `j.seal.write(…)` (§2.9).
- **`checkFile`:** step 1 runs today's body (`:362-376`) verbatim. Step 2 runs `j.seal.check(…)`
  and then today's three journal checks (`:367-374`) verbatim.

**Sequential equivalence.** For a batch that commits, two things match `lease()` calls made one at
a time in queue order:

- every per-request result (compared with `errors.Is`);
- the bytes of `delivery-leases.jsonl`.

The seal holds the same final `(bytes, count, chain)` through fewer generations. For a failing
batch, commit-dependent members fail. That is a wider availability blast radius (R5), never a
safety difference.

### 2.7 How concurrent redeliveries of one nonce join the pending batch

A second copy of one delivery can be:

- the client's spool fallback taken by a drain;
- a replayed WAL line;
- a concurrent live retry.

Every case uses the same queue:

- **J1, same batch.** The second copy finds `fresh[nonce]` and joins the mint. It adds no line and
  no arrival, and it receives the identical `deliveryLease` only in Phase 5, after the seal.
  - If the batch fails, it gets the fault.
  - A joiner with a different binding gets `ErrAppendOnly` after a successful commit, and the fault
    after a failed one. That is exactly the sequential order.
- **J2, queued while the batch carrying the original is in flight.** The copy waits in the queue.
  The next leader's batch finds the nonce in `j.leases` and answers it as a known nonce, gated by
  that batch's own `checkFile`, as today at `:173`.
  - If the in-flight batch failed, the next batch's `enter()` refuses on the fault.
  - A second identity is impossible: arrival allocation and admission belong to one leader at a
    time, and each batch observes the previous batch's admission.
- **J3, queued before the cut.** The same as J1: the leader cuts the whole FIFO prefix.
- **After a restart:** `load()` rebuilds `j.leases` from the durable journal. The copy is a known
  nonce, answered after the first batch's `checkFile`.

`TestDeliveryJournal_ConcurrentDeliveryRetriesShareOneAssignment` (8 racers, one line, eight equal
leases; `delivery_lease_failure_test.go:223-247`) exercises all three nondeterministically. T12
(§6.2) pins each case deterministically.

### 2.8 Stage K: the ack pipeline

`acknowledge(ctx, delivery, id, root)` keeps its signature. It enqueues an `ackReq`, with its error
initialised to `deliveryJournalError()`, on `j.ackQ`. `commitAcks` has the same shape as
`commitLeases`:

1. **Gate:** `enter()` and `ownedByFile()`.
2. **Phase 1, in today's order:**
   1. ctx (`:494`);
   2. gate (`:497`);
   3. validation (`:500`);
   4. lease match (`:503-506`), with `j.leases` read under `j.st`;
   5. the idempotent return for an already-committed ack (`:507-512`), resolved without a check, as
      today;
   6. a second ack for the same delivery in the batch joins the first;
   7. the entries bound (`:517`);
   8. marshal and line bounds (`:521-527`).
3. **Phase 2:** one `checkAckFile` (`:513`), if any new ack exists.
4. **Phase 3:** one ack `Write`, one `Sync`, one `sealAck`. That is today's `saveAckPosition` in
   step 1, and in step 2 the slot write plus the post-seal identity check.
5. **Phase 4:** admission under `j.st`.
6. **Phase 5:** resolve.

Any failure poisons the **shared** fault.

`acknowledged()` keeps `Lock.mu` for `owned()`, exactly as today (`:552-563`), and reads `j.acks`
under `j.st`:

```go
func (j *deliveryJournal) acknowledged(delivery string) bool {
    if j == nil || j.owner == nil { return false }
    j.owner.mu.Lock(); defer j.owner.mu.Unlock()
    if !j.owner.owned() { return false }
    j.st.Lock(); defer j.st.Unlock()
    if j.closing || j.closed || j.fault != nil { return false }
    _, ok := j.acks[delivery]
    return ok
}
```

### 2.9 The v2 seal: format, strict reader, write, check, open and Release

**Format.** Paths are unchanged: `state/delivery-lease-position.json` and
`state/delivery-ack-position.json`. v2 is a fixed **32 768-byte** file that is one valid JSON
document:

```
offset  len    content
0       11     {"v":2,"a":                                      static
11      480    slot a: canonical record or `null`, right-padded with spaces
491     15893  spaces                                            static
16384   5      ,"b":                                            static
16389   480    slot b: canonical record or `null`, right-padded with spaces
16869   15898  spaces                                            static
32767   1      }                                                 static
```

Each record sits inside one 512-byte sector and one 4 KiB block (slot a: sector 0 and block 0;
slot b: sector 32 and block 4). The two slots are in different 4 KiB and 16 KiB pages. A record is
the canonical `json.Marshal` of

```
{"seq":N,"bytes":B,"count":C,"chain":"sha256:…","sum":"sha256:…"}   // about 230 bytes
sum = core.HashBytes("qompack.delivery.seal.v2",
        chainDomain ‖ 0 ‖ slotLetter ‖ 0 ‖ seq ‖ 0 ‖ bytes ‖ 0 ‖ count ‖ 0 ‖ chain)
```

- The chain domain binds a record to its journal: lease or ack.
- The slot letter makes a record copied into the other slot invalid.
- Bounds are today's (`delivery_lease.go:331-336`): `bytes == 0 ⇔ count == 0`, and `bytes == 0 ⇒
  chain == seed`.

An old binary's `loadDeliveryPosition` parses the document, sees `"v":2`, and **refuses**: it
requires `Version == core.EvidenceVersion == 1` (`delivery_lease.go:331`, `internal/core/evidence.go:9`).
That is fail-closed, never a misread.

**Slot classification.** A slot is **valid** when all of these hold:

- it is canonical JSON followed by spaces only, up to the region end;
- its fields are in bounds;
- `sum` is correct;
- **seq parity matches the slot**: odd in `a`, even in `b`;
- every static byte of the file is exact.

It is **empty** when it holds the literal `null` followed by spaces. Anything else is **invalid**.

**Selection at open. This is the strict reader (fix J-B3):**

| Slots | Decision |
|---|---|
| `a` valid with seq 1, `b` empty | effective = `a`: a fresh or converted file |
| both valid, with \|seq_a − seq_b\| = 1 | effective = the higher seq. The older record must satisfy `older.bytes < eff.bytes` and `older.count < eff.count`, **and** it must seal a journal prefix: `load()` verifies count and chain at `older.bytes` during the same scan |
| **anything else**, including any invalid slot, `empty` beside seq ≠ 1, a seq gap or a parity violation | **refuse**, write nothing, preserve evidence |

`load()` then continues exactly as today with `position = effective` (`:233-284`). It checks the
sealed prefix, the canonical lines, the dense arrivals and the size; a complete canonical tail past
the seal is accepted and re-sealed at open. The v1 path (`loadDeliveryPositionV1`, today's function
verbatim) is chosen when the file is not exactly 32 768 bytes with exact static bytes.

**Why strict, and not B's Rule R.** Every seal write happens only after new bytes are durable, and
alternates slots by parity, so every crash-reachable v2 state is one of the first two rows. That
assumes sector-atomic writes of a 4 KiB-aligned block: an interrupted slot holds its old or its new
record, and PostgreSQL makes the same assumption for `pg_control`. An invalid slot is therefore
media damage or a foreign write.

- Rule R would accept that state whenever the journal extends past the other slot, which in steady
  state it always does. It would thereby accept "rot of the newest slot plus a later line-aligned
  truncation inside the last batch": **released identities lost silently**, where today any damage
  to the position refuses.
- On a device that does tear a single aligned block, strict refuses: the journal is unavailable,
  evidence is preserved, and deliveries become counted gaps. That is the same class as today's torn
  journal tail.
- The operator-run repair (§4.5) applies Rule R only with explicit consent.

**`deliverySeal.write(pos)`** (new file `internal/daemon/delivery_seal.go`):

```go
rec  := sealRecord{Seq: s.seq + 1, Bytes: pos.Bytes, Count: pos.Count, Chain: pos.Chain}
slot := slotFor(rec.Seq)                 // odd → a, even → b: always the slot holding seq−1
enc  := encodeSlot(rec, slot, s.domain)  // canonical JSON + sum, space-padded to 480 bytes
n, err := s.f.WriteAt(enc, slotOffset(slot)); if err != nil || n != len(enc) { return errSeal }
if err := paths.SyncData(s.f); err != nil { return errSeal } // Linux fdatasync; Windows FlushFileBuffers; darwin F_FULLFSYNC
// POST-SEAL IDENTITY (fix J-B5): the seal went into the HELD inode; release only if the PATH still
// names it, so the durable seal is the one recovery will read. A rename always lands at the path;
// an in-place write must prove it.
info, err := os.Lstat(paths.Long(s.path))
if err != nil || !info.Mode().IsRegular() || info.Size() != deliverySealFileSize || !os.SameFile(info, s.ident) {
    return errSeal
}
copy(s.image[slotOffset(slot):], enc); s.cur, s.seq = rec, rec.Seq // memory follows disk only on success
return nil
```

**`deliverySeal.check(bytes, count, chain)`**, run once per batch in `checkFile`:

1. `Lstat(path)` is regular, with size 32 768.
2. `SameFile(Lstat, s.ident)`, where `ident` is cached from `f.Stat()` at open.
3. `ReadAt` all 32 768 bytes through the held handle, and byte-compare them with `s.image`.
4. `s.cur` equals `(bytes, count, chain)`.

The journal's three checks follow, verbatim.

**Open sequence** (`openDeliveryJournal`, under `Lock.mu`; the structure of `:81-156` is kept):

| Step | Action |
|---|---|
| O1 | Existence rules unchanged (`:100-115`): both files absent → create; exactly one absent → refuse. A fresh create writes the seal in the build's write format: v2 `WriteAtomic(image{a: seq 1 {0, 0, seed}, b: null})`, or today's v1 |
| O2 | `load()` with the dual reader. **Nothing is written if it fails** (`:119-122`) |
| O3 | Open the writer, identity check, `f.Sync()` (`:123-141`, unchanged) |
| O4a | Step 2 with v1 on disk: **convert** by `WriteAtomic(image{a: seq 1 = cur, b: null})`. With v2 on disk: nothing yet |
| O4b | Step 2: open the held handle with `paths.OpenSharedRW` (read and write, sharing read, write and delete). Verify `SameFile(Lstat(path), f.Stat())` and `ReadAt == ` the expected image |
| O4c | Step 2 with v2 and `effective ≠ cur` (a recovered tail): `s.write(cur)` puts seq+1 into the other slot, followed by the post-check. Step 1: today's `savePosition(cur)`, always rewriting v1, which converts a v2 file left by a step-2 build |
| O5 | `openAckLocked`, the same way |
| O6 | Clear `journalOpenFault` (`:154`) |

**Release** (`closeLocked`, §2.5):

1. close the writers;
2. close the seal handles;
3. **downgrade** (step 2 only), when `fault == nil` **and** `ownedByFile()`: `WriteAtomic` a v1
   JSON seal for both positions from the last sealed values.

The downgrade is best effort. A failure is counted and logged, the v2 file stays valid, and
ownership release is not blocked.

The ownership condition is load-bearing. In
`TestDeliveryJournal_ReplacedLockCannotReleaseOrLeaseForNewOwner` a replaced owner releases while
the new owner holds the seal open. Without the condition it would rename a v1 file over the new
owner's held file, and the new owner's next `check` would fault.

### 2.10 Detection strength (I5 and the brief's forbidden list)

**Today's property.** Every append to `delivery-leases.jsonl`, and every return of a lease (new or
old), is preceded by a successful ownership check and a successful `checkFile`:

- both are evaluated after the request arrived and after the previous append was sealed and
  admitted;
- only in-memory work separates the check's last syscall from the `Write`.

**The final design keeps that property for every append and every return:**

- A batch performs exactly one append (one `Write` syscall) and releases its leases only after it.
  Two lines of one batch are written by one syscall, so there is no instant between them at which
  the disk could differ from memory.
- Its single ownership check and single `checkFile` run after every member arrived and after the
  previous batch's admission.
- Phase 1 runs before Phase 2, so the check-to-`Write` gap is the call itself.
- Nothing is sampled and nothing is skipped: any batch with a validated request runs the full check.

**What changes, and in which direction:**

| Check | Today | Final |
|---|---|---|
| Seal content | the parsed `(bytes, count, chain)` plus canonical form | **every byte** of the file (records, padding, static bytes) plus the in-memory `cur`: stronger |
| Seal identity | a path open, then `SameFile` | a path `Lstat` plus `SameFile` against the held inode. A same-content replacement is now detected: stronger |
| Seal location after writing | a rename always lands at the path | the post-seal identity check proves it: equal |
| Older seal | not kept | must be adjacent, parity-correct and a verified journal prefix: stronger |
| Ownership | `owned()` in the accessor and in every `lease` call | every batch, via `ownedByFile()`; the accessor keeps the in-memory `released` check (O1): equal coverage, relocated |
| Journal checks | `Lstat`, `fstat`, `SameFile`, size | unchanged |
| Torn or damaged seal at open | the v1 file cannot be torn (rename); any damage refuses | strict: any invalid slot refuses: equal |

### 2.11 Why a per-batch seal does not widen the truncation window (I3)

The `Accept` comment (`ingest.go:196-203`) forbids "sealing the position once per batch instead of
once per lease", because it widens the window in which a truncated tail is invisible. The window
it names is the set of leases **released** (handed to a job, ACKed) while not covered by a durable
seal. Here that set is **empty at every instant**, as it is today: Phase 4 and every wake-up happen
after the seal and its post-check return.

What grows, from at most one line to at most one batch, is the set of lines that are durable but
unsealed and **never released**, after a crash between the journal `Sync` and the seal.

- No caller, job or ACK ever saw those identities.
- Losing them is today's "crash before the journal sync" outcome.
- Recovering them is today's complete-tail recovery (`:135-146`).

A truncation into a sealed prefix is refused (`:234`), as today. ADR 0013's consequence paragraph
(`docs/adr/0013-migration-contracts.md:106-108`) stays literally true for every row: "the row is
synced before a canonical position record seals its byte/count/hash prefix and before the
assignment is returned".

### 2.12 Options rejected, with the reason

| Id | Option | Reason |
|---|---|---|
| X1 | Overlap one delivery's WAL Sync with its own journal Write and Sync (B's O3) | creates a new reachable crash state: a durable orphan lease with no durable bytes |
| X2 | Overlap `checkFile` with the WAL flush (A's X2) | widens the check-to-append gap |
| X3 | Release after the journal sync and seal later | the forbidden reduction; widens I3 |
| X4 | Cache the v1 position read | drops detection: a file replaced by rename must be reopened by path |
| X5 | A seal inside the journal | one file cannot order "lines, then seal" with one fsync; it would also break GC's line reader and `load`'s canonical rule |
| X6 | An append-only seal log | every append changes the size (a metadata sync), it grows without bound, and compaction needs a rename again |
| X7 | A single slot, written through | a torn write loses the seal |
| X8 | B's Rule R in the automatic reader | weakens detection (J-B3); kept only as an operator repair |
| X9 | B's dedicated committer goroutines | safe, but a lifecycle coupled to open and close failure paths, plus two handoffs per isolated delivery; the leader/follower queue needs neither |
| O2 | Write-through seal handle (`FILE_FLAG_WRITE_THROUGH` / `O_DSYNC`) | not adopted; FUA equivalence depends on the storage stack. Measure first (Q8) |

---

## 3. Crash-point table

**Steps of the new path.**

- **WAL:** W1 the leader writes the batch buffers; W2 fsyncs; a rotation syncs the old segment,
  closes it and opens the new one; W3 results are set and followers woken.
- **Lease:**
  - L1 gate, `inflight++` and `ownedByFile`;
  - L2 Phase 1;
  - L3 `checkFile`;
  - L4 journal `Write`;
  - L5 journal `Sync`;
  - L6 seal: v1 `WriteAtomic` (temp write and sync, rename, dir fsync) or v2 slot `WriteAt`;
  - L7 v2 `SyncData`;
  - L8 v2 post-seal identity;
  - L9 admission;
  - L10 results set, followers woken, job queued, `Accept` returns;
  - AK the ACK byte.
- **Ack:** K3 `checkAckFile`, K4 `Write`, K5 `Sync`, K6/K7 seal, K8 post-check, K9 admission.
- **Open:** O1–O6. **Release:** closing and wait, close, downgrade.

**Assumptions.**

- A *machine crash* loses the page cache, and the hook process dies with the machine.
- A *process crash* keeps the page cache, so every issued write persists; those rows are the
  "complete" variants.
- Media write a 4 KiB-aligned block atomically. A violation makes the reader refuse; it never makes
  it misread.
- The client gets no ACK before AK. On EOF or timeout it spools a copy with the same nonce.

| # | Crash at | WAL | Lease journal | Seal file | Recovery outcome | I1–I4 |
|---|---|---|---|---|---|---|
| 1 | before or inside W1 | none, or a prefix of the batch; at most one torn last line per segment | unchanged | unchanged | The journal opens normally. The drain leases complete lines fresh; a torn line waits (`TestDrainTrailingIncompleteLineWaitsForCompletion`) | I1: no ACK. I2: no identity existed. Same states as today, n lines instead of 1 |
| 2 | inside W2 | as 1, more complete | unchanged | unchanged | as 1 | as 1 |
| 3 | rotation: old segment synced, new one not yet opened | old segment complete and durable | unchanged | unchanged | as 1 | Sync-before-Close keeps the old segment's batch lines durable |
| 4 | W3 … L3 | durable | unchanged | unchanged | Today's "crash between `appendWAL` and lease": the drain leases fresh, and a surviving client copy joins by nonce | I2. No orphan: no journal byte of a delivery precedes a sync covering its bytes. On the `Accept` path, and for a segment the ingest holds, that sync is the WAL Sync; for any other file it is the drain's own sync before the pass (`515e472`, `c6d5c60`) |
| 5 | inside L4/L5 | durable | none, a complete prefix, or a **torn tail** of the batch | the previous seal | **Complete tail:** `load` accepts it past the seal, and O3/O4 re-sync and re-seal it before any caller; a redelivery recovers the identity. **Torn tail:** `load` refuses (`:249-266`, `:281`); the journal is unavailable, evidence is preserved, deliveries become counted unleased gaps | The same two outcomes as today's single line; tail ≤ 1 MiB. I3: nothing released |
| 6 | after L5, before L6 completes (v1: rename not durable, maybe `wa-*` debris; v2: the target slot still holds seq−1) | durable | complete, durable | the previous seal | The complete tail is recovered and re-sealed at open | I3: nothing released |
| 7 | inside L6/L7 (v2) | durable | complete, durable | the target slot holds **old** (seq−1) or **new** (seq+1); the other slot is untouched | Old: effective = the other slot (seq); the tail is recovered and re-sealed with seq+1. New: clean open. **Torn (a non-atomic device): strict refuse**, evidence preserved, operator repair (§4.5) | I3: nothing released. I4: never a misread |
| 8 | after L7, before L8/L9 | durable | durable | new seal durable | Clean open; the batch's identities were never released, and a redelivery recovers them as known nonces | — |
| 9 | after L9, before L10 | durable | durable | durable | as 8 | — |
| 10 | after L10, before AK | durable | durable | durable | No ACK, so the client spools (process crash) or died (machine crash). The drain's WAL copy and the client copy share the lease by nonce; once one is published the other is skipped (`drain.go:303-324`, fix `1c17f0d`) | today's "after lease, before ACK" |
| 11 | after AK | durable | durable | durable | Publication recovery unchanged (`TestCrashCutBetweenReferenceAndFrontierRedelivers`) | I1 met at the ACK |
| 12 | K4…K8, any subset, independent of L4…L9 | — | ack file: none, a complete tail, or a torn tail | ack seal: old or new (or torn → strict refuse) | A complete ack tail is re-sealed by `openAckLocked` (`:477-480`); a torn ack tail refuses the open (today). Every surviving ack names a lease sealed in an earlier lease batch, so `loadAcks` (`:621-627`) passes | every combination was reachable today |
| 13 | **non-crash** failure at L4, L5, L6, L7 or L8 | — | uncertain | uncertain | Shared fault set; every commit-dependent member fails; maps untouched; the handle is unusable until Release and re-acquire. The on-disk state is one of rows 5–9. A short slot `WriteAt` leaves an invalid slot, which refuses on reopen: the same class as today's short journal write (`…UncertainAppendPoisonsUntilRecovery/short write`) | only synced and sealed bytes admitted |
| 14 | O4a (v1→v2 `WriteAtomic`) | — | synced at O3 | v1 old or v2 new, sealing the same prefix; `wa-*` debris possible | Either one opens | journal sync precedes the seal |
| 15 | O4c (re-seal of a recovered tail) | — | tail durable | as row 7 | as row 7 | — |
| 16 | Release downgrade (`WriteAtomic` ×2) | — | durable | each file v1 or v2, with the same seal | A new binary opens either format; an old binary opens v1 only (fail-closed on v2) | — |
| 17 | `Release` while requests are queued (not a crash) | — | — | — | `closeLocked` waits for the in-flight batches. Queued requests reach `enter()` (closing) and fail with `deliveryJournalError`, counted by `Accept` as unleased gaps. Nothing is appended after `Release` returns | I3: `Release` never overtakes L4–L9 |
| 18 | seal path replaced during L6–L7 (external interference, not a crash) | — | durable | the new seal is in the orphaned inode; the path names a foreign file | L8 fails → poison; **nothing released**. Recovery reads whatever is at the path, as today for a replaced position file | fix J-B5 |

**GC's structural readers are unaffected.** `openLeaseLines` (`internal/store/gcrun.go:746-762`)
retains any line it cannot parse, so a pass racing a multi-line `Write` over-retains, as it can
against today's single-line write. `acknowledgedDeliveries` (`:715-739`) answers "fewer
acknowledgements" on a torn tail.

---

## 4. Compatibility and migration

### 4.1 What changes on disk

| Artifact | Step 1 | Step 2 |
|---|---|---|
| WAL segments | byte-identical: same lines, terminator and rotation boundaries | byte-identical |
| `delivery-leases.jsonl`, `delivery-acks.jsonl` | byte-identical: the same canonical lines in the same order, written with one `Write` per batch | byte-identical |
| `delivery-lease-position.json`, `delivery-ack-position.json` | v1: today's canonical JSON, about 110 B, written by `WriteAtomic` | **v2 while a daemon runs** (§2.9); v1 after a clean `Release` |

GC (`gcrun.go:605-634`, reading the `.jsonl` files only), `test/e2e/v5_x03_test.go` and
`v3_x09_test.go` are unaffected. `store.TakeBackup` copies `state/` (`internal/store/backup.go:54-56`,
`:130-165`), so a backup taken while a step-2 daemon runs holds v2.

### 4.2 Reader matrix

| On disk \ binary | pre-step-1 (today) | step 1 (dual reader, writes v1) | step 2 (writes v2 while running) |
|---|---|---|---|
| v1 (any state today produces) | ✓ | ✓ | ✓, converted to v2 at open |
| v2, clean | **fail-closed**: `loadDeliveryPosition` refuses `"v":2`; the journal is unavailable; deliveries become counted gaps; WAL and spool stay durable; **files are untouched** | ✓, rewritten as v1 at open | ✓ |
| v2, crash-reachable (the target slot holds seq−1 or seq+1) | fail-closed, as above | ✓ | ✓ |
| v2 with an invalid slot (non-atomic device, media damage) | fail-closed | **refuse** (strict); operator repair | **refuse** (strict); operator repair |

### 4.3 Rollout: two steps, with the switch as an internal constant

- **Step 1, the reader.** Ships:
  - all three group commits and the ack decoupling;
  - the dual reader, the v2 seal code and the converter;
  - `deliverySealWriteFormat = 1`.

  Every seal is today's v1 `WriteAtomic`, and a v2 file found at open is rewritten as v1. This is
  the "compatible deployed reader" that SP-20 requires before the first new-format write
  (`plans/V4-SP-20-capture-storage-and-state-remediation.md:39`; invariant 10 at `:89`; M1-04 at
  `:107`).
- **Step 2, the writer.** Flips `deliverySealWriteFormat = 2` in its own reviewed commit, together
  with:
  - the T27 and T28 evidence;
  - a rollback drill against a real v2 artifact, as SP-20 requires "after that write".
- **Where the switch lives.** A package constant pinned by T29, not a `runtime.migration.*` config
  gate. ADR 0013 D13-6's gates are feature gates for M1–M3 behaviour. Registering the switch there
  would require changing the pinned `require.Len(t, gates, 1)` (Q12).
- **Release boundary.** Whether steps 1 and 2 ship as two tags (recommended) or as one tag is the
  owner's choice (Q5). One tag would rely on SP-20's "verified backup restore" branch: the
  downgrade at `Release` plus a backup taken with the daemon stopped.

### 4.4 Rollback story

| Rollback | Outcome |
|---|---|
| step 2 → step 1 | always safe: step 1 reads v2 and rewrites v1 at open |
| step 2 → pre-step-1, after a clean stop | safe: v1 is on disk |
| step 2 → pre-step-1, after a crash or a failed `Release` | **fail-closed** (the journal degrades; nothing is corrupted or rewritten) until one of three repairs: start step 1 or 2 once and stop it cleanly; run the offline converter (§4.5); or restore a verified backup taken with the daemon stopped |
| step 1 → pre-step-1 | safe: step 1 writes v1 only |

### 4.5 The offline tool (new)

`qompack admin delivery-seal`. The name is to be settled with SP-17; the code lives in
`internal/daemon` as an exported function, wired by `internal/cli`.

- It takes the daemon lock through `AcquireLock`, so it refuses while a daemon runs.
- `--check` runs the full dual reader and `load` for both seals and reports the result.
- `--to v1` converts both seals to v1 by `WriteAtomic`, only after a successful full load.
- `--accept-torn-slot` is the only place Rule R lives. It accepts `valid + invalid` only when the
  journal holds a complete canonical tail past the valid record. It prints exactly which lines it
  accepted, and it requires an explicit confirmation flag.

### 4.6 What the migration costs

| Cost | Figure |
|---|---|
| Runtime, step 2, per daemon lifetime | 4 `WriteAtomic`s (convert lease and ack at open; downgrade both at `Release`): about 12–18 ms on this host. **Zero per delivery** |
| Disk | +2 × 32 KiB while a daemon runs |
| Code (predicted) | about 500 production lines (queue 90, seal 250, pipelines 120, WAL 60, paths 40) and about 1 100 test lines |
| Operations | one fail-closed case (a crash, then a downgrade past step 1). No data lost |

---

## 5. Change list per file

| File | Change |
|---|---|
| `internal/daemon/groupcommit.go` (new) | `groupQueue[T]`: cut, handoff (the J-A1 fix), caps, panic-safe handoff; `errNotCommitted` conventions documented |
| `internal/daemon/delivery_seal.go` (new) | Constants (`deliverySealVersion = 2`, `deliverySealFileSize = 32768`, stride 16 KiB, record max 480, sum domain, `deliverySealWriteFormat`, each with `//nomagic:allow`); `encodeSlot` / `classifySlot` (parity, sum, canonical form, padding); `loadSealAny` (the dual reader and the strict selection table); `deliverySeal{f, ident, image, cur, seq, domain, seed}` with `write` (including the post-check), `check` and `close`; v1/v2 image builders; the downgrade helper; the exported offline-tool entry point |
| `internal/daemon/delivery_lease.go` | New fields `st`, `idle`, `closing`, `inflight`, `leaseQ`, `ackQ`, `seal`, `ackSeal`. `lease` and `acknowledge` become enqueue-and-wait, with `commitLeases` and `commitAcks` (Phases 0–5). `enter`, `leave`, `poison`, `usable`, `failCommit`, `recoverBatch`. `acknowledged` gets `Lock.mu` plus `j.st`. The `openDeliveryJournal` fast path (O1) and open sequence O1–O6. `closeLocked` gets the closing protocol, seal close and downgrade. `checkFile` / `checkAckFile` branch v1/v2, and **the v1 bodies are kept verbatim**. `loadPosition` returns the effective seal through the dual reader (tests call it). `loadDeliveryPosition` becomes `loadDeliveryPositionV1` behind the dual reader. `load` / `loadAcks` get the second (older-seal) checkpoint. `savePosition` / `saveAckPosition` are kept for step 1. The frontier-section comment (`:404-423`) is updated to say "per batch" |
| `internal/daemon/ingest.go` | The `walQ` field (built in `newIngest`); `appendWAL` becomes enqueue-and-wait; `commitWALBatch` (default-fail, per-segment coalesced `Write`, parallel `Sync`, **Sync-before-Close on rotation**, buffers bound to `*os.File`); a `syncWAL` seam. **Rewrite the `Accept` doc comment `:160-206`**: three durability points per batch; on POSIX an fsync, an fsync and an fdatasync, because the directory fsync goes away when no directory entry changes per seal; release after seal; why this is not the forbidden reduction. The `Accept` body (`:207-251`), `leaseDelivery` and `commitDelivery` are unchanged |
| `internal/daemon/lock.go` | `ownedByFile()`; the comment on `mu` (`:60`) becomes "heartbeat, journal open and release" |
| `internal/paths/shared.go`, `replace_windows.go`, `replace_other.go` | `OpenSharedRW(p)`: on Windows `CreateFile(GENERIC_READ\|GENERIC_WRITE, FILE_SHARE_READ\|WRITE\|DELETE, OPEN_EXISTING)`, mirroring `openShared` (`replace_windows.go:160-`); elsewhere `os.OpenFile(O_RDWR)`, with the `IsProtected` guard. `SyncData(f)`: Linux `unix.Fdatasync` through `SyscallConn`; Windows and darwin `f.Sync()` |
| `internal/config/deadlines.go`, `defaults.go` | Per-GOOS `L0IngestMs` and `AckDeadlineMs` constants and helpers (windows, darwin, other), following `connectDeadlineMsDefault` (`deadlines.go:54-70`); values from §7.5 |
| `internal/config/runtime.go:35`, `:227`; `internal/obs/budgets.go:20` | Doc strings: "daemon read → delivery durable (WAL + lease sealed)" |
| `internal/config/defaults_test.go:57`, `:93` | Expectations follow the helpers. **A default change, not a weakened check** |
| `internal/config/schema_test.go:39-98`, `tools/devtool/genconfigdocs.go:211`, `testdata/golden/config/schema.json`, `docs/config-reference.md` | Extend the platform-specific-leaf rewrite from `connectDeadlineMs` to the two new leaves; regenerate |
| `test/bench/hotpath/report_test.go:301-306` | `TestBudgetLimit_ReadsFromConfigDefaults` pins B-B at 2 ms; its expectation follows the helper. **A default change**, missed by both designs |
| `internal/cli/hookclient.go:111-116` | `hookSendDeadlineFloor` reads the platform default from `config`, so there is one source of truth |
| `internal/daemon/budget.go:14-26` | Correct the `hotPathTailAllowance` comment (durable `Accept` and the ACK lie after `recvTS`); the value is unchanged pending Q1/Q2 |
| `test/bench/hotpath/main.go:114-135`, `report.go` | The `--under-coload` rationale for B-B is stale either way (§7.6). Comment fix always; the gate change only under Q3(b). Add a reported-only `hook_ack_rtt` row (an in-process `ipc.Client` timing `Send` of fresh-nonce `observe.tool` requests) for `slack99` |
| `test/integration/hotpath_test.go`, `test/e2e/v3_x11_test.go`, `test/guards/coload_test.go`, `ci.yml` timing lane | **Only under Q3(b)**: B-B joins B-A as a co-load yielder, judged in isolation |
| `internal/daemon/bench_test.go` | New benchmarks (§6.3); `BenchmarkIngestAccept*` unchanged |
| new `internal/daemon/*_test.go` | §6.2 |
| docs | New ADR "delivery-path group commit and the A/B seal" (§2.10, §2.11, strictness, O1); `plans/00-ARCHITECTURE.md` §2.4 B-B row; `plans/CARRIED-DEFECTS.tsv` SP20-D1 and SP05-D2 rows and their sections; regenerate `testdata/bench-baseline.txt`'s `BenchmarkIngestAccept*` rows on a quiet AC window. If `Qompack.md` states the 2 ms figure, a revision needs explicit authorization (Q11) |

---

## 6. Test plan

### 6.1 Existing tests that pin each invariant (every name verified in the tree; all pass unchanged)

| Invariant | Tests |
|---|---|
| **I1** exact bytes durable before the ACK | `internal/daemon/ingest_test.go`: `TestIngestWALIsExactBytes`, `TestAcceptWireLineWALsExactlyOneTerminator`, `TestIngestACKPrecedesProcessing`, `TestIngestRingFullWALsEveryLineAndNeverSpills`, `TestIngestWALRotates`, `TestLiveDispatchedLineIsNotRedispatchedByDrain`, `TestIngestResolvesBlobsEndToEnd`; `TestBoundedQueueDropsRatherThanBlocks` (`delivery_publication_test.go`); `TestDrainTrailingIncompleteLineWaitsForCompletion` (`drain_recovery_test.go`) |
| **I2** identity durable before a job; redelivery reuses it; synced-only admission | `delivery_lease_test.go`: `TestDeliveryJournal_EqualRequestsHaveDistinctDeliveries`, `…RetryDoesNotAppendAndTokenMustKeepItsBinding`, `…EmptySessionIsAValidIdentityScope`, `…RejectsCanceledAndInvalidLeaseInputsWithoutAppending`. `delivery_lease_failure_test.go`: `…ConcurrentDeliveryRetriesShareOneAssignment`, `…UncertainAppendPoisonsUntilRecovery`. `TestDeliveryJournal_AcceptsTheNonceTheHookClientMints`. `delivery_publication_test.go`: `TestEqualContentDeliveriesStayDistinct`, `TestRedeliveryOfOneNonceIsObservedOnce`, `TestCrashCutBetweenReferenceAndFrontierRedelivers`, `TestDeliveryJournal_AcknowledgementSurvivesReopen`. `TestDrainDoesNotRedeliverAnAcknowledgedClientCopy` (`drain_acknowledged_copy_test.go`). `TestCarriedDefect_SP08D2_ReusedLeaseRedeliveryIsNotIdempotent` |
| **I3** seal after the bytes, before use; truncation detected | `TestDeliveryJournal_OneSyncAndOneSealPerLease` (sequential, so still exactly 1:1), `…LeaseSyncSerializesWithRelease`, `…SealedPrefixCannotRewindOrChange`, `…PositionCorruptionCannotBeRepairedByAnOpenWriter`, `…FailedOpenRequiresOwnerReleaseBeforeRetry`, `…UncertainAppendPoisonsUntilRecovery/position failure` |
| **I4** torn tails, recovery, canonical lines, ack journal, GC, compatibility | `…RejectsUntrustworthyRowsWithoutChangingThem`, `…RejectsOversizeJournalAndRecord`, `…BoundsAndSequenceOverflowRefuseAdmission`, `…CloseFailureRetainsOwnership`, `…IsSingletonPerLockAndStopsWithItsLock`, `…ReplacedLockCannotReleaseOrLeaseForNewOwner`; `TestDeliveryJournal_AcknowledgementIsTheCommittedFrontier`, `…AcknowledgementAheadOfItsLeaseIsRefused`; `TestPublicationOrderIsObjectReferenceFrontier`, `TestDrainAdmitsAndAcknowledgesInheritedRecords`, `TestDrainReportsGapsInsteadOfSilentCompleteness`; the `drain_recovery_test.go` suite. `internal/store/lifecycle_test.go`: `TestGC_ReadsTheDeliveryLeaseJournalAsARetentionRoot`, `TestGC_AcknowledgedDeliveryLeaseStopsRetaining`, `TestGC_UnreadableAcknowledgementKeepsItsLeaseOpen`. `internal/store/backup_test.go`: `TestRollbackDrill_BeforeAndAfterTheFirstNewFormatWrite`, `TestRollbackDrill_RefusesTheBeforePhaseOnceANewFormatWriteExists`, `TestBackup_RestoreOpensAsARealStore`. `test/e2e`: `TestV5_HookEventToTombstoneToRetrievalAfterRestart` (x03), `TestV3_LiveSessionWriteSetAndAppendOnly` (x09), `TestV4_HotPathUnchangedWithTheFullWave3ResidentSet` (x13; R13), `TestV5_NoPackageWritesOutsideDotQompack`; `test/guards`: `TestV1_WriteSetConfinedAcrossFullHookSequence` |
| **I5** gates | `BenchmarkIngestAcceptLeased` (its b.N+1-leases assertion stays), `devtool bench-hotpath --iterations 2000 --warm-daemon`, `TestIntegration_HotPathWarmWithRealResidentState`, `TestIntegration_HotPathDegradesRatherThanBlocks`, `TestV3_HotPathUnchangedWithLedgerResident`, `TestColoadYieldersAreJudgedInIsolation` |

**Expectation updates required by any re-budget.** These are default changes, not weakened checks:

- `internal/config/defaults_test.go:57` (`AckDeadlineMs: 8`) and `:93` (`L0IngestMs: 2`);
- `test/bench/hotpath/report_test.go:304`;
- the schema golden and the config docs.

**Step-2 trace of the tests that touch the seal path.** This is why they pass unchanged; all of it
needs Windows POSIX delete semantics (R2).

- **`…PositionCorruption…`** (all seven modes).
  - The test truncates the held v2 file, with `os.WriteFile` of v1 JSON (shares are compatible), or
    removes it.
  - The next batch's `check` fails on size or identity → poison → the journal is unchanged.
  - `Release` is faulted, so there is no downgrade.
  - The reopen reads the v1 fixture, or finds the file missing, and refuses; the evidence is
    byte-identical.
- **`…UncertainAppend…/position failure`.**
  - The path is removed and replaced by a directory during the journal `Sync`.
  - The slot write lands in the unlinked held inode, and the **post-check fails** → no admission.
  - The test restores the captured v2 image (seq 1, bytes 0).
  - The reopen reads `valid(seq 1) + empty`, recovers the complete tail, re-seals seq 2 into slot
    `b`, and `Count == 1`.
- **`…LeaseSyncSerializesWithRelease`.** `Release` waits on `j.idle` for the in-flight batch, then
  downgrades to v1. `loadPosition()` reads v1 with `Count == 1`.
- **`…ReplacedLockCannotReleaseOrLeaseForNewOwner`.** The old journal's batch is refused by
  `ownedByFile`. Its `Release` does not downgrade because it no longer owns the lock. The new
  journal's `check` passes.
- **`…FailedOpen…`, `…RejectsUntrustworthyRows…`, `…AcknowledgementAheadOfItsLease…`.** Each
  writes v1 fixtures, which the dual reader accepts as input.
- **`…CloseFailureRetainsOwnership`.**
  - The first `Release` sets `closing`, the close fails and the handle is poisoned.
  - `lease` is then refused at `enter()`.
  - The second `Release` closes the handles without a downgrade, because the handle is faulted. v2
    stays on disk.

### 6.2 New tests (all under `-race -count=20`; the queue tests `-count=100`)

| Id | Name | Pins |
|---|---|---|
| T1 | `TestGroupQueue_FIFOBatchesEachRequestCommittedOnce` | FIFO prefixes containing their leader; each request committed once; one batch per leader; bounded wait |
| T2 | `TestGroupQueue_PanicInCommitHandsOffWithoutDeadlock` | J-A1 regression: a panic in commit, then 1 000 further requests all complete |
| T3 | `TestGroupQueue_UnassignedResultStaysAFailure` | J-A2 regression: a commit that returns early or panics leaves every member failed. Asserts no `Accept` returns nil |
| T4 | `TestIngest_WALGroupCommitOneSyncPerSegmentPerBatch` | The `syncWAL` seam blocks batch 1; 32 concurrent `Accept`s over two sessions; batch 2 has exactly two syncs; exact bytes; one terminator each |
| T5 | `TestIngest_AcceptNeverReturnsBeforeItsWALSync` | With the sync blocked, no `Accept` returns and no job reaches the ring |
| T6 | `TestIngest_WALBatchPreservesRotationBoundaries` | Byte-identical segments against sequential appends; the old segment is synced before `Close`; its buffer goes to the old handle (J-A7) |
| T7 | `TestIngest_WALFailureFailsExactlyItsSegment` | A write error, a short write and a sync error each fail exactly their buffer; a later success never ACKs them |
| T8 | `TestIngest_LeaseLineNeverPrecedesItsWALSync` | An event log over the WAL sync and the journal writer: every journal `Write` follows the WAL sync covering its delivery (rules out X1's state) |
| T9 | `TestDeliveryJournal_BatchCommitsOneWriteOneSyncOneSeal` | Block batch 1 at `Sync`, queue 32 nonces, release: one `Write`, one `Sync`, one seal for batch 2; arrivals dense 1..33 in queue order; slots alternate with parity |
| T10 | `TestDeliveryJournal_NoLeaseReleasedBeforeItsBatchSeal` | Block at `SyncData` (step 2) or the temp sync (step 1): no member returns, `loadPosition().Count` is unchanged, and `Release` cannot overtake |
| T11 | `TestDeliveryJournal_ConcurrentRedeliveryJoinsPendingBatch` | Deterministic J1, J2 (answered only after the next batch's `checkFile`) and J3; a binding mismatch gets `ErrAppendOnly` after a commit and the fault after a failure; exactly one line |
| T12 | `TestDeliveryJournal_BatchMatchesSequentialOutcomes` (seeded property test) | Random mixes: valid, invalid, cancelled, duplicate, mismatched, arrival = MaxUint64, the entries cap, the bytes cap mid-batch. Per-request `errors.Is` results and **journal bytes** equal a twin fed sequentially; the final seal values are equal |
| T13 | `TestDeliveryJournal_BatchFailurePoisonsEveryDependentMember` | A short write, a sync failure, a slot short write, a slot sync failure and a post-check failure, each with N = 16. Dependent members fail, known-nonce members get their sealed lease, the maps are unchanged; reopen per §3 rows 5–9 |
| T14 | `TestDeliveryJournal_CheckRunsAfterEvaluationAndImmediatelyBeforeAppend` | J-B4 regression. An event log shows ownership → Phase 1 → `checkFile` → `Write` → `Sync` → seal → post-check, with no syscall between the check and the `Write`. Every corruption mode injected between batches (plus a padding flip, a static-byte flip and a same-content replacement) is detected before the next `Write`, and the files are unchanged |
| T15 | `TestDeliveryJournal_ReleaseWaitsForInFlightBatchesAndFailsQueued` | Both pipelines; nothing appended after `Release` returns |
| T16 | `TestDeliveryJournal_AnyFaultPoisonsBothPipelines` | J-B2 regression: an uncertain ack write refuses later leases, the accessor refuses, and `acknowledged` answers false |
| T17 | `TestDeliveryJournal_AckBatchNeverDelaysALeaseBatch` | Block the ack `Sync`; a lease batch commits meanwhile |
| T18 | `TestDeliveryJournal_AckBatchMatchesSequentialOutcomes` | Includes duplicate acks in one batch and an ack for an unleased delivery |
| T19 | `TestDeliveryJournal_AcknowledgedIsRaceFreeWithRelease` | J-B1 regression: `-race` with `acknowledged()` looping against `Release` |
| T20 | `TestDeliveryJournal_AccessorOwnershipIsRecheckedPerBatch` | O1: the lock is replaced after the accessor returned; the batch refuses and nothing is appended |
| T21 | `TestDeliverySeal_V2IsOneJSONDocumentAndTheOldReaderRefusesIt` | `json.Valid`; `loadDeliveryPositionV1` refuses it; exact offsets; one record per sector and per 4 KiB block |
| T22 | `TestDeliverySeal_StrictSelectionTable` | Every row of §2.9, including a torn slot, `empty` beside seq ≠ 1, a seq gap, a parity violation, and **rot of the newest slot plus a line-aligned truncation → refuse** (the J-B3 regression). Evidence preserved |
| T23 | `TestDeliverySeal_OlderSlotMustSealAJournalPrefix` | A mismatched count or chain at `older.bytes` refuses |
| T24 | `TestDeliverySeal_PathReplacedDuringSealReleasesNothing` | J-B5 regression: a `SyncData` hook swaps the path; the batch fails and nothing is admitted |
| T25 | `TestDeliverySeal_ConversionAndDowngrade` | v1 → v2 only after a successful `load`; a failed `load` leaves the v1 bytes untouched; a clean `Release` leaves v1 at the final seal; a faulted or unowned `Release` writes nothing; a format-1 build converts v2 back |
| T26 | `TestDeliveryJournal_RollbackDrillAcrossFormats` | A crash image with v2, back up, restore, open with format 1 (identities continuous, file is v1), reopen with format 2 |
| T27 | `TestDeliverySeal_WriteFormatIsDeliberate` | Pins `deliverySealWriteFormat` (1 in step 1; step 2 changes it together with this test and cites T25/T26 evidence) |
| T28 | `TestDeliveryPath_CrashCutAtEveryStep` (table) | Seams stop at each §3 step. A machine-crash image holds only the extents seen synced, plus torn variants of the in-flight extent. A fresh lock on a copy of the image asserts the row: open or refuse, recovered identities, and the drain outcome for a WAL copy plus a client copy of one nonce |
| T29 | `TestDaemon_NoACKBeforeDurableBatch` | `dispatchOp` with the seal sync blocked produces no response; release it, then OK |
| T30 | `TestConfig_AckDeadlineDefaultCoversTheIngestBudget` | Anti-drift, per GOOS: `AckDeadlineMs ≥ L0IngestMs + 1` |
| T31 | `TestDeliveryOfflineTool_ConvertsAndRefusesWhileADaemonHoldsTheLock` | §4.5, including the consent requirement for `--accept-torn-slot` |

### 6.3 Benchmarks (the evidence for §7)

- **`BenchmarkDeliveryLeaseComponents/{accessorOwned,ownedByFile,checkFileV1,checkFileV2,journalWriteSync,sealWriteAtomic,sealSlot,postSealIdentity,walWriteSync}`.**
  This is step 0 (M0), run on the **current tree first**. It replaces the §1.3 attribution with a
  measurement.
- **`BenchmarkIngestAcceptLeased`.** Unchanged. It becomes the isolated-delivery row for the step-1
  and step-2 builds.
- **`BenchmarkIngestAcceptLeasedParallel/{1,4,16,64}`.** Uses `b.RunParallel` with fresh nonces
  over one and four sessions. It reports ms/op, and syncs and batches per delivery through the
  seams, and it keeps the leases-held assertion.
- **`BenchmarkIngestAcceptLeasedBurst/simultaneous-2000`.** It reports p50, p99 and max through
  `b.ReportMetric`. A B-C stand-in runs `publishCapture` and `acknowledge` for every lease, so device
  contention is included. It runs with and without O1. **The paced variants are gone** (`40e95fb`):
  paced-open arrivals need a wall-clock wait, which `sleepcheck` and 00-ARCHITECTURE §6.1 allow only
  under `test/bench`. Open-loop pacing is therefore measured by `devtool bench-hotpath` at an ACK
  deadline below B-B (today's 8 ms), reported beside the closed-loop runs, and a paced-closed-loop
  variant may return only if it starts each delivery when the previous one returned, with no timer.
- **`BenchmarkDeliverySealWrite`,** run beside `BenchmarkPathsWriteAtomic_4KB`.
- **Run rules** inherited from this project:
  - timing rows alone, on a quiet AC window, one process at a time;
  - `-timeout=30m`;
  - never pipe `go test` (a pipe masks the exit code);
  - confirm the test count with `-v`, because `-run` prints `ok` when its pattern matches nothing;
  - a detached launch for long runs;
  - `-race` over `internal/daemon` and `internal/paths`.

---

## 7. Predicted latency, and the recommended limits

### 7.1 Model

**Symbols.**

| Symbol | Meaning |
|---|---|
| `F_w` | WAL write and flush |
| `G` | the accessor's lock-file read (0 with O1) |
| `O` | the per-batch `ownedByFile` |
| `C` | `checkFile` |
| `F_j` | journal write and flush |
| `S` | seal |
| `P_c` | post-seal identity check |
| `m` | CPU per extra lease in a batch (marshal, SHA-256 chain, `NewObservationID`, maps): about 5–7 µs |
| `I` | interference from the previous delivery's B-C flushes (sidecar `WriteAtomic`, ack batch) |
| `slack` | transport, pre-`Accept` dispatch, ACK write and client wake-up |

**Formulas.**

- Isolated B-B = `F_w + G + O + C + F_j + S + P_c + ε`.
- **Closed loop** (`AckDeadlineMs` above B-B p99 + slack): bench-hotpath has one outstanding
  delivery (M6), so B-B ≈ isolated + `I`, where `I` ranges from 0 to one or two colliding flushes.
  **This is the regime the recommended defaults put the harness in, and the regime the limit must
  be measured in** (J-AB1).
- **Open loop** (the deadline below B-B): every hook spools and exits, and arrivals come every
  ≈ 22 ms. Service per delivery is `T_W` on the WAL pipeline and `T_L` on the lease pipeline. The
  system is stable when `max(T_W, T_L·(1+β)) < Δ`; batching absorbs any backlog.

### 7.2 Windows (Core Ultra 7 155H, NTFS, Defender default, quiet AC)

| Component | Today | Step 1 | **Step 2 (final)** | Basis |
|---|---|---|---|---|
| `F_w` | 1.9–2.2 | 1.9–2.2 | 1.9–2.2 | M1, measured |
| `G` | 0.05–0.15 | 0 (O1) | 0 (O1) | predicted |
| `O` | 0.05–0.15 (second `owned()`) | 0.05–0.15 per batch | 0.05–0.15 per batch | predicted |
| `C` | ≈ 10–12 (inferred) | ≈ 10–12 per batch | **0.1–0.4** | inferred from M2 − 3F − W′ and M4; predicted for v2 (2 `Lstat`, 2 zero-access path opens for `SameFile`, 1 `fstat`, 1 cached 32 KiB `ReadAt`) |
| `F_j` | 1.9–2.5 | 1.9–2.5 | 1.9–2.5 | same shape as M1 |
| `S` | 3.1–4.4 (`WriteAtomic`) | 3.1–4.4 | **1.0–2.2** | M3; predicted for an in-place overwrite of one non-resident page plus a flush |
| `P_c` | — | — | 0.05–0.2 | predicted |
| misc (marshal, chain, queue) | 0.1–0.3 | 0.1–0.3 | 0.1–0.3 | predicted |
| **Isolated leased `Accept`** | **18.4–23.3 (measured)** | 18–23 | **5.2–7.8 (central 6.3)** | the pessimistic case, where C's share is flush inflation that survives: **8–12** |

| Scenario | Today (measured) | Design A (its §7.2) | Design B (its §7.2) | **Final (step 2, predicted)** |
|---|---|---|---|---|
| Isolated | 18.4–23.3 | 18–23 | 5.0–7.5 | **5.2–7.8** |
| bench-hotpath B-B, closed loop, p50 / p99 | — | not modelled (≈ 20–24 / 25–35 by this model) | not modelled | **6–8 / 9–14** |
| bench-hotpath B-B at the 8 ms deadline (open loop), p50 / p99 | 917.5 / 983.0 | 22–30 / 40–55 | 6–8 / 10–16 | 7–9 / 12–20; every hook still spools (SP05-D2) |
| Co-loaded isolated (F ≈ 8–9 ms) | 37–42 | ≈ 37–42 | ≈ 24–28 | 25–29 |
| Co-loaded bench-hotpath p99, closed loop | — | — | 30–45 | 30–45 |
| 2 000 simultaneous, p50 / p99, with O1 | ≈ 20 s / 40 s | ≈ 50 / 100 | ≈ 20 / 40 | **20–30 / 35–55**: 4 lease batches of (`T_L` + 512·`m` ≈ 4–5 + 3 ms), with WAL batches overlapped |
| the same without O1 | — | + N·G | + N·G | + 60–300 ms (accessor serialisation, J-AB2) |
| Flushes per leased delivery under a burst of k | 3 | ≈ 3/k | ≈ 3/k | ≈ 3/k; acks ≈ 2/k on their own pipeline |

Step 1 alone at the 8 ms deadline: the lease pipeline's `T_L` ≈ 16–21 ms against Δ ≈ 22 ms puts ρ at
0.75–0.95 with acks decoupled. The prediction is p50 25–35 / p99 45–70 ms, and marginal under
co-load. **So the re-budget waits for step 2.**

### 7.3 Linux and macOS (predictions; the reference platform was not measurable in V5, `V5-report.md:300-302`)

- **ext4 with a volatile device cache.**
  - `fsync` of an appended file costs one jbd2 commit plus a FLUSH: about 0.3–1.5 ms on local NVMe
    and about 1–4 ms on a CI VM disk.
  - `fdatasync` of an in-place overwrite of the same size and already-allocated blocks needs no
    journal commit, only a FLUSH: about 0.1–0.8 ms.
  - Today's seal costs 2 fsyncs (temp and directory). The final seal is 1 fdatasync.
  - `C` ≈ 0.03 ms.
- **WSL2** is ext4 in a VHDX on NTFS, so it is not representative. The Linux number must come from
  the ubuntu-latest `bench-gate` job.

| Scenario | Local NVMe, today | **Local NVMe, final** | CI runner, today | **CI runner, final** |
|---|---|---|---|---|
| Isolated leased `Accept` | 1.4–6 ms (4 syncs) | **0.8–4 ms** (3 syncs, one of them fdatasync) | 4–16 ms | **2.5–11 ms** |
| bench-hotpath B-B p99, closed loop | — | **2–7 ms** | — | **5–18 ms** |

**macOS.** `File.Sync` is `F_FULLFSYNC`, which is unmeasured here. It is plausibly 3–20 ms per sync
on Apple SSDs and unknown on runner VMs, which puts the isolated `Accept` at about 9–60 ms. That is
why darwin needs its own default and its own measurement (J-B12).

### 7.4 What B-A measures, and what the ACK deadline has to cover

- **Gated B-A** is `recvTS − req.TS + 1 ms` (`handlers.go:288-289`).
  - `req.TS` is stamped in the hook before it connects.
  - `recvTS` (`handlers.go:193`) is taken before admission, before `Accept` and before the ACK
    write (`server.go:245-251`).
  - bench-hotpath gates this daemon histogram (`main.go:372-389`, `:439-446`).
  - **Raising `AckDeadlineMs` cannot move gated B-A**; it will keep reading about 3 ms (M5).
  - The spool-submode breach detector consumes the same estimate (`handlers.go:296-299`), so it
    cannot see a slow durable `Accept` either (Q2).
- **`AckDeadlineMs`** starts at `SetReadDeadline` just after the client's write (`client.go:278`).
  It must therefore cover the pipe read, `dispatchOp` up to `Accept` (admission, `Touch`,
  `EncodeRequest`), **B-B**, the ACK write and the client wake-up. That is
  `AckDeadlineMs ≥ B-B p99 + slack99`.
- **Below that, every late hook spools a second copy** (SP05-D2, currently about 100 % on Windows).
  The harness also reverts to open loop, and B-B's population changes.
- **Well above that**, a slow daemon blocks the host's hook for up to the whole deadline.
- **The contract B-A names** is "client main() entry to exit (connect + write + ACK)"
  (`obs/budgets.go:18`). With a durable ACK its true value is pre-send (about 2 ms) + ACK RTT +
  exit:
  - Windows, final: about 2 + (9–14) + 2 + 1 ≈ **14–19 ms p99**, at or over 15 ms;
  - Linux: about **5–11 ms**, inside;
  - Design A: about 30–40 ms.
- **Other rows move too.** B-D moves by about +2–6 ms at p99 and may fall at p50: the 8 ms timeout
  and the spool append disappear. B-G samples stop appearing, because there is no more fallback.

### 7.5 Recommended limits and the measurement that must confirm them

**Values** (provisional; replace them with the measurement):

| GOOS | `L0IngestMs` | `AckDeadlineMs` | Predicted `P` it rests on |
|---|---|---|---|
| windows | **20** | **23** | 9–14 ms (roundup5(1.25 × 14) = 20; slack99 ≈ 2–3 ms) |
| linux | **15** | **17** | 5–12 ms on ubuntu-latest (the weakest Linux prediction; take the measured value) |
| darwin | **40** | **45** | 15–30 ms (the least certain row) |

**Procedure.**

1. **M0** (before any code), on the current tree, on a quiet AC window: run
   `BenchmarkDeliveryLeaseComponents` with `-count=6`.
   - If `checkFileV1` or `sealWriteAtomic` do not carry the ≈ 10–15 ms non-flush remainder, step
     2's prediction moves to the pessimistic column: 8–12 ms isolated, `P` 12–18 → Windows
     `L0IngestMs` 25.
   - Record this before relying on the table above.
2. **M1**, after step 1: `BenchmarkIngestAcceptLeased -count=6` (no regression), the burst and
   parallel benchmarks, and bench-hotpath × 3 at a temporary config deadline of 60 ms. That
   deadline gives a closed loop, which proves the queue is gone. Step 1's measurement does not set
   the budget.
3. **M2**, after step 2. The window must be quiet: power AC, foreign load under 5 %, processor
   performance recorded.
   1. Set a provisional config deadline of 40 ms on Windows and 30 ms elsewhere, and verify the
      harness is closed-loop: census deferrals = 0.
   2. Run `devtool bench-hotpath --iterations 2000 --warm-daemon` three times, one process at a
      time, with `-timeout=30m`, never piped.
   3. Record B-B p50/p99/max, B-A, B-D, the census deferrals, and the new reported-only
      `hook_ack_rtt` row.
   4. `P` = the maximum B-B p99 across the three runs. `slack99` = p99(`hook_ack_rtt`) − p99(B-B),
      in the same runs.
   5. `L0IngestMs = roundup5(1.25 × P)`; `AckDeadlineMs = L0IngestMs + ceil(slack99)`.
   6. Linux and macOS: the same protocol in CI's `bench-gate` on ubuntu-latest and macos-latest.
4. **Acceptance**, at the final defaults. Re-run bench-hotpath three times and require:
   - B-B PASS in all three;
   - census deferrals (the SP05-D2 fallbacks) ≤ 0.5 % of sends, with a target of 0;
   - `BenchmarkIngestAcceptLeased` median ≤ 0.5 × `L0IngestMs`, which leaves headroom for
     interference;
   - T9, T10 and T14 green under `-race`;
   - one `--under-coload` run recorded and judged per Q3.

   Only then is `CARRIED-DEFECTS.tsv` SP20-D1 closed, together with SP05-D2 (its fallback rate is
   now measured and bounded).

### 7.6 Co-load

bench-hotpath gates B-B under `--under-coload`, on the premise that B-B contains no process spawn
(`main.go:125-132`). With three flushes at 8–9 ms each under co-load, the premise is false: B-B's
co-loaded p99 is predicted at 30–45 ms on Windows, and `TestIntegration_HotPathWarmWithRealResidentState`
runs the harness under the declaration in the whole-tree `test` job (`test/integration/hotpath_test.go:749-756`).

The owner must choose (Q3):

- **(a) One limit sized from co-load:** Windows about roundup5(1.25 × 45) = 60 ms.
  `AckDeadlineMs` stays quiet-derived at 23: missing the deadline costs a safe duplicate copy, not
  data. The quiet gate becomes about 3× looser than the service time.
- **(b) The ADR 0010 route (recommended):**
  - B-B's wall-clock row is **reported** under the declaration, like B-A and B-E's wall row, and
    judged at the quiet limit in the isolation lanes (`bench-gate`, `timing`, `test-e2e`).
  - The co-load-immune structural gates T9, T10 and T14 run in every lane: syncs per batch-of-one,
    check-then-append order, zero releases before the seal.
  - This is ADR 0010's rule applied to a row whose exemption premise changed: "a budget can be
    deferred by the whole-tree job but not lost by the pipeline"
    (`docs/adr/0010-wall-clock-under-coload.md:86-87`).
  - It still changes a gate's co-load shape, so it needs the owner's ruling.

---

## 8. Risks and open questions

### Risks

| Id | Risk | Mitigation |
|---|---|---|
| R1 | The attribution (§1.3) is unmeasured; step 2's gain may be 8–12 ms, not 5–8 | M0 before any code; the limits follow the measurement |
| R2 | Step-2 tests remove, or `mkdir` over, the path of a held seal. That needs `FILE_SHARE_DELETE` on the held handle and POSIX delete semantics for `os.Remove` (the NTFS default on current Windows 10/11 and Server 2019+) | Run `…PositionCorruption…` and `…UncertainAppend…` on `windows-latest` before step 2 lands |
| R3 | Device contention (β) from B-C: one sidecar `WriteAtomic` per delivery (`internal/store/capture_sidecar.go:191`), observer writes and ack batches | Measured in the paced burst benchmark; a sidecar group commit is store work outside SP20-D1 |
| R4 | The strict reader refuses a torn slot on a device that is not block-atomic, where Rule R would have recovered automatically | The same class as today's torn-tail refusal; evidence preserved; operator repair (§4.5) |
| R5 | One failed write, sync or seal now fails up to 512 members instead of one; each becomes a counted unleased gap | Safety unchanged; the handle is poisoned either way (T13) |
| R6 | WAL rotation now syncs before `Close` | Adds a durability point, removes none; happens once per 64 MiB |
| R7 | The `Accept` comment (`ingest.go:196-203`) forbids a per-batch seal; this design relies on reading the forbidden act as *release before seal* | SP-20 author countersign plus the ADR (Q9) |
| R8 | New concurrency in a package where the race detector found real defects late | `-race -count=20` on every new test; T19 targets the one race found in B |
| R9 | macOS `F_FULLFSYNC` | Per-GOOS default measured on macos-latest |
| R10 | A live `TakeBackup` can copy a v2 file mid-slot-write, and the invalid slot then refuses | The same class as today's inconsistent live pair; back up with the daemon stopped (ADR 0013 `:110-112`) |
| R11 | UX: the host's hook blocks up to 23 ms on Windows when the daemon is slow (today: 8 ms plus a spool append) | Owner decision already taken; T30 keeps the deadline tied to the budget |
| R12 | Rolling back past step 1 after a crash fails closed | §4.4 repairs |
| R13 | `TestV4_HotPathUnchangedWithTheFullWave3ResidentSet` folds its write set by size (`test/e2e/v4_x13_test.go:40-66`), so a fixed-size v2 seal is invisible in both arms. The test still passes symmetrically | Observability note only |
| R14 | 64 KiB-page kernels put both slots in one page | The records stay in different 4 KiB blocks and sectors, and the untouched slot is rewritten identically. Safe on block-atomic media; use a 64 KiB stride if that platform matters |
| R15 | `Release` holds `Lock.mu` while waiting for in-flight batches, so `acknowledged()` and `Heartbeat` wait | At most one batch cycle, as today |
| R16 | Pre-existing, not introduced: a new WAL segment's directory entry is never fsynced on POSIX (`ingest.go:300-319`) | One directory fsync per new segment; an I1 hardening outside SP20-D1. The drain's half is closed **for the files the drain syncs itself**: before it consumes such a file, a pass syncs the spool directory, once per pass (`c6d5c60`). A segment the ingest holds is read on the ingest's Sync alone, which does not cover a new segment's directory entry, so a lease the drain takes from a held segment carries R16 exactly as `Accept`'s does |
| R17 | Pre-existing: a short WAL write leaves a fragment that merges with the next line | Unchanged by this design; noted for SP-17 |

### Open questions for the owner

| Id | Question |
|---|---|
| Q1 | On Windows the true hook-controlled p99 with a durable ACK is about 14–19 ms against B-A's 15 ms contract. Accept a per-platform hook contract, measure O2 (write-through) to shave about 1 ms, or treat this host class as non-reference? |
| Q2 | Move the B-A sample point, and the breach detector, from `recvTS` to "ACK written" (after `callHandler`, `handlers.go:247`)? It is stricter, not a lowering, and it is the only way the gate sees the durable path |
| Q3 | Co-load treatment of B-B: (a) one co-load-sized limit, or (b) ADR 0010 reporting plus structural gates (recommended)? |
| Q4 | Per-GOOS defaults for `L0IngestMs` and `AckDeadlineMs` (recommended), or one value sized for the slowest platform? |
| Q5 | Two tags (reader, then writer; recommended), or one tag using SP-20's verified-backup branch? |
| Q6 | Countersign O1: the accessor's lock-file read relocates into the per-batch check (§2.5) |
| Q7 | Confirm the strict reader plus operator repair (recommended) over B's automatic Rule R |
| Q8 | O2, a write-through seal handle: approve for measurement only |
| Q9 | SP-20 author countersign on the release-after-seal reading of I3, the rewritten `Accept` comment, and the ADR |
| Q10 | Batch caps (512 requests; 4 MiB WAL, 1 MiB lease and ack): accept, or tune from the burst benchmark? |
| Q11 | If `Qompack.md` states B-B's 2 ms, authorise its revision; it is revisable, with a revision log |
| Q12 | Keep the write-format switch as a package constant (recommended), or register it in `migrationBuildGates`, which changes an existing `require.Len`? |

---

## Judge findings

Every file:line claim this document relies on was re-read in the source. Severity: **critical**
breaks an invariant or deadlocks; **major** weakens a check, widens a window, changes semantics or
invalidates a prediction; **minor** is an inaccuracy or incompleteness.

### Findings in Design A

| Id | Severity | Finding | Resolution in the final design |
|---|---|---|---|
| J-A1 | **critical (deadlock)** | The queue handoff `next, next.lead = q.queue[0], true` (A §2.2). Go's assignment evaluates the operand of `next.lead`'s implicit indirection in phase 1, while `next` is still nil, so phase 2 panics inside the deferred handoff **with `q.mu` locked**. The first time two deliveries overlap, every later `Accept`, lease or ack blocks forever | Two statements (§2.3); regression test T2 |
| J-A2 | **critical (I1)** | Results default to success. `walItem.err` and `leaseReq.err` are zero-valued nil, and `commitWALBatch` has no recover and assigns `it.err` only on failure. Any path that leaves an item unassigned (a panic, a future early return) wakes the follower with nil: `Accept` returns OK and the **ACK goes out for bytes that were never written**. On the lease side, it returns a zero-value "lease" with a nil error | Every result is initialised to a failure, and commit sets success only after durability (§2.3, §2.4, §2.6); T3 |
| J-A3 | major (latency) | Stage K runs the ack chain inside the lease batch under `Lock.mu`, and the leader "joins both before any admission" (A §2.5). No lease is released until the ack chain finishes, and a lease arriving during an ack-heavy batch waits a whole cycle. That puts `T_K` into B-B's p99 | Independent ack pipeline off `Lock.mu` (§2.8); T17 |
| J-A4 | major (scope) | A keeps the per-lease `WriteAtomic` seal, so an isolated delivery stays at 18–23 ms. Its own §7.2 attributes 45–55 % of that to the format, and the owner asked for the A/B slot if compatibility is handled | v2 seal with a dual reader and two-step migration (§2.9, §4) |
| J-A5 | minor (pseudo-code) | WAL rotation binds pending buffers to the `walFile`, whose handle rotation swaps mid-batch; the old segment's lines could be written to the new handle | Buffers are bound to `*os.File` (§2.4); T6 |
| J-A6 | minor | The test plan does not list `test/bench/hotpath/report_test.go:304` (pins B-B = 2 ms) among the expectations a re-budget changes | Listed (§5, §6.1) |

### Findings in Design B

| Id | Severity | Finding | Resolution |
|---|---|---|---|
| J-B1 | **major (data race, I4)** | B moves `acknowledged()` and the ack enqueue path under `j.ack.st` while keeping `owned()` (B §2.5). `owned()` reads `l.released` (`lock.go:240`), which `Release` writes under `Lock.mu` (`lock.go:279`, `:297`). Run concurrently with the drain's `acknowledged()`, `-race` flags it | `acknowledged()` keeps `Lock.mu` for `owned()`; batches use `ownedByFile()`, which never reads `released` (§2.5, §2.8); T19 |
| J-B2 | major (semantics) | B gives the ack half its own state and leaves unspecified whether an uncertain ack write poisons the lease side. Today one `j.fault` (`delivery_lease.go:531-541`) makes every later lease (`:167`) and the accessor (`:91`) refuse. Separate faults would keep leasing against an uncertain ack journal | One shared fault set by either pipeline (§2.5); T16 |
| J-B3 | **major (detection weakened, I5)** | Rule R accepts `valid + invalid` whenever the journal extends past the valid record. In steady state it always does, so the rule accepts rot of the newest slot, and **rot plus a later line-aligned truncation inside the last round silently loses released identities**, where today any position damage refuses. `valid + empty` is accepted at any seq, so a newest slot rewritten to `null` takes the same path. With 4 KiB-aligned records on block-atomic media a crash cannot produce an invalid slot, so R buys availability only on tearing media, at the price of a check | Strict reader, plus seq parity, adjacency, `empty` only beside seq 1, and a verified older prefix. Rule R exists only in the operator tool (§2.9, §4.5); T22, T23 |
| J-B4 | major (TOCTOU widened) | B runs `checkFile` at step (b), **before** the evaluation at (c). For a 512-ticket round, (c) is ≈ 2.5–5 ms of CPU (marshal, SHA-256 chain, `NewObservationID` per lease), so the check-to-`Write` gap grows from µs to ms. B's claim that "the gap between check and Write is unchanged" (B §2.8) is false for large rounds | A's order: evaluate, then check, then append (§2.6); T14 |
| J-B5 | major (I3 equivalence) | The v2 seal is written through the **held** handle, and B checks path identity only *before* the `WriteAt`. A path replaced between that check and the sync leaves the new seal in an orphaned inode, while the path names a foreign file, and the round's leases are released anyway. Today's rename always lands at the path | Post-seal `Lstat` + `SameFile` before admission; failure poisons and releases nothing (§2.9; §3 row 18); T24 |
| J-B6 | minor (WAL) | In B's per-file WAL, a writer that decides to rotate waits for `!syncing` inside `cond.Wait`, and the rotation condition is not re-evaluated on waking. Two writers can each rotate, skipping a segment and breaking the byte-identical-boundaries claim. The leader's out-of-lock `Sync` has no recover, so a panic leaves `syncing = true` and the followers hang | A's WAL under `ingest.mu` with default-fail results (§2.4) |
| J-B7 | minor (lifecycle) | Two dedicated committer goroutines per journal, whose start and stop are coupled to open-failure and `closeLocked` paths, plus two goroutine handoffs per isolated delivery | Leader/follower queue: no goroutines, and the isolated path runs inline (X9) |
| J-B8 | minor (false claim) | "No existing test needs editing" apart from `defaults_test.go:93` (B §0, §4.5). `defaults_test.go:57` (`AckDeadlineMs: 8`) and `test/bench/hotpath/report_test.go:304` (B-B = 2 ms) also pin defaults, and per-platform defaults require the schema golden and docs regeneration (`schema_test.go:39-98`) | Listed as default-change updates (§5, §6.1) |
| J-B9 | minor (predictions) | "Portable 10/12" puts darwin, whose `File.Sync` is `F_FULLFSYNC`, together with Linux | Per-GOOS values; darwin measured separately (§7.3, §7.5) |

### Findings common to both designs

| Id | Severity | Finding | Resolution |
|---|---|---|---|
| J-AB1 | **major (prediction regime)** | Both model bench-hotpath as open-loop arrivals. The harness spawns hooks **one at a time**, and each waits up to `AckDeadlineMs` for its ACK (`process.go:265-300`). Once the owner's raised deadline exceeds B-B's p99, the harness is **closed-loop with one outstanding delivery**, and B-B measures service time plus interference, not a queue. A's p50 22–30 / p99 40–55 ms is the wrong regime, and neither design states that the limit must be measured with the final deadline in force | §1.4, §7.1; the M2 protocol fixes the deadline during measurement and verifies zero deferrals |
| J-AB2 | major (burst prediction) | The accessor (`delivery_lease.go:85-88` via `daemon.go:975-983`) reads `daemon.lock` under `Lock.mu` on **every** `Accept`. That is a serial section of N·G (≈ 60–300 ms at N = 2 000 on Windows) outside any group commit. Neither design's simultaneous-burst figure includes it; B lists O1 only as a ≈ 0.1 ms per-delivery option | O1 adopted with a detection argument (§2.5), flagged Q6; T20; the burst benchmark runs with and without it |
| J-AB3 | major (gate premise) | bench-hotpath keeps B-B hard under `--under-coload` because B-B "contains no process spawn" (`main.go:125-132`). That was measured before `f6a8691` put fsyncs in the region. A keeps the co-load gate as is; B raises it only as a question | §7.6 with both options priced, and a recommendation |

### Claims in A and B that were checked and hold

The following were verified in the source:

- the step-by-step path in both §1 tables (server, handlers, ingest, lease, atomic, lock, client and
  hookclient line numbers);
- B-A's sample point (`handlers.go:193`, `:288-289`) and its gating in bench-hotpath
  (`main.go:372-389`, `:439-446`);
- the stale `hotPathTailAllowance` comment (`budget.go:14-17`);
- GC's structural readers (`gcrun.go:715-762`);
- the ack lease-exists check (`delivery_lease.go:621-627`);
- that an old binary refuses `"v":2` (`delivery_lease.go:331` with `core.EvidenceVersion = 1`);
- that B's downgrade-only-when-owned condition is what keeps `…ReplacedLock…` passing;
- that `TestIngestWALRotates` pokes `ing.wals` with a zero-value `walFile` (`ingest_test.go:166-169`),
  which both WAL designs keep valid;
- that no production code outside `internal/daemon` reads either position file (repository grep).

### What the final design takes from each

| From A | From B | New in the final design |
|---|---|---|
| The leader/follower queue (fixed); the WAL stage; Phase 1 → check → append ordering; the per-request check-order equivalence; the rejected-overlap table | Ack decoupling off `Lock.mu`; the v2 file layout, sum and dual reader; the conversion/downgrade rollout and the ownership condition on the downgrade; `SyncData`/`OpenSharedRW`; the per-test step-2 trace | Default-fail results; the shared fault; the strict reader with parity, adjacency and seq-1 empty; the post-seal identity check; O1 with its argument; the closed-loop regime and the measurement protocol; the co-load decision; per-GOOS limits; the operator repair tool |
