# V6 remediation — observation sidecar hardening: independent re-review

**Seat:** independent non-authoring V6 review, `claude-opus-4-8` high (prior canonical verified),
same session `788ed24f-1a30-4096-b080-f98cfd4ab92d`, no children.
**Date:** 2026-09-21.
**Nature:** read-only critical review of the CURRENT source — no source/test edits, no tests/builds
(main runs the focused tests; I ran nothing), no Git/config/permission changes. Only output: this
file. NOT a signoff. I do not carry forward any "safe" conclusion from old tests; findings are from
current source. Where source leaves a real doubt I name the exact test needed. A missing filename in
a compiler log is not proof of no type error. The 64 MiB / 1<<20 caps are an explicit lifetime
refusal, not a scaling-completion claim. Terminal/order O2/O3 are out of scope this round.

**Read (current):** `internal/store/observation_publication.go`, `observation_lookup.go`,
`observation_audit.go`, `fsstore.go` (obs fields, `mutate`/`use`/`Close`), `maintenance.go`
(`proveReader`), `readonly.go` (`AuditPublication`). Scope is the sidecar + store integration; the
capacity author's journal/generation files are not in scope.

---

## 1. Confirmed hardened since the prior review

- **F1 (scan bound) is now real.** `loadObservationsLocked` reads under `io.LimitReader(…,64MiB+1)`
  and `break`s (not "continue") on the byte/entry ceiling (`observation_publication.go:195,203-206`).
  Open-time I/O is bounded.
- **F3 (original-intent authority) largely addressed.** An observation stores its canonical wire bytes
  (`obsBinding.raw`), idempotence/conflict is a full byte-equality test (`reserveConflictLocked:409-413`),
  v2 targets carry `{ID,Root}` (`observationTarget`), and completion/recovery replay the STORED ORIGINAL
  (`b.intent`), never the caller's re-derived one (`publishObservation:583-593`, `completeIntentLocked`).
  A recorded target that vanished/changed Root → `lost` → unavailable, never forged completion
  (`observationStateLocked:336-350`).
- **F3.3 lookup precedence fixed.** `ToolUseByObservation` checks ambiguous → unavailable → committed →
  uncertain (`observation_lookup.go:47-68`); a torn/conflicting intent for a committed obs now wins.
- **F4 root confinement.** Both the read (`os.OpenRoot(Index)` + `root.OpenFile`, `:178-186`) and the
  write (`:442-454`) go through an `os.Root`, and the write path Lstat-refuses a non-regular sidecar.
- **New audit + restore proof.** `auditObservationBindings` notes uncertain/ambiguous/unavailable and
  every non-committed binding even when the capture/object scan is empty (`observation_audit.go`), and
  `proveReader` refuses a restore when it reports incomplete (`maintenance.go:428-432`); `readonly.go`
  delegates `AuditPublication`. These integration claims hold in source.

---

## 2. Substantive unresolved findings (return to main)

### H1 — buildIntentLocked normalization defeats exact-replay idempotence (HIGH)
`buildIntentLocked` (`:380-398`) builds the intent's `sup` from targets **currently live** (present +
`StatusOK`) at reserve time, and both `ReserveObservation` and `publishObservation` run it
UNCONDITIONALLY before `reserveConflictLocked`, which then compares FULL canonical bytes against the
stored original (`:576`, `:558`, `:409-413`). Consequence: after an observation's first publish marks
its target superseded, a **legitimate redelivery** of the SAME observation rebuilds a *shorter* `sup`
(the target is now omitted, `:388-390`) → different canonical bytes → `reserveConflictLocked` returns
`ErrAppendOnly "already reserved for a different intent"`. A truly-exact replay of an already-published
observation is REJECTED as a conflict. This is exactly the crash-recovery path the intent binding
exists for (capture-sidecar recognition missed → observer re-runs `RecordToolUse`), so the exact-bytes
idempotence (the prior-F3 fix) and the live-state normalization actively conflict. **Fix direction:**
for an obs that already has a binding, decide idempotence by (obs + record identity) and replay the
STORED ORIGINAL — do not re-derive `sup` from live state and byte-compare. `buildIntentLocked` should
gate only a brand-new reservation. **Exact test needed:** publish an observation whose `sup` has one
live target; then, without going through capture-sidecar recognition, call the observation-bearing
`RecordToolUse` again for the same obs/record after that target is superseded, and assert it is
idempotent (nil/committed), NOT `ErrAppendOnly`.

### H2 — no-LF complete-JSON tail loads clean, then the next append joins lines (MEDIUM)
`loadObservationsLocked` uses `bufio.Scanner` (ScanLines), which returns a final line with NO trailing
`\n`. A crash that persisted a complete intent JSON but not its terminating `\n` (`record = json+'\n'`,
one `Write`, torn before fsync) therefore **loads as a valid intent and does NOT set uncertain**. On
the next process, `appendObservationIntent` opens `O_APPEND` and writes at EOF — right after the
LF-less JSON — producing `json{…}record2{…}\n` on one physical line, which fails `decodeObservationLine`
on the following reload (→ uncertain, and the newly-appended intent is lost). No code verifies the file
ends on a `\n` boundary before appending, and `totalBytes += len(raw)+1` (`:202`) miscounts the LF-less
tail. Fail-safe (uncertain, never false publication) but a valid appended intent can be silently lost,
and the first reload does not surface the torn tail. **Fix direction:** on load, if the file's last
byte is not `\n`, treat the trailing record as incomplete (uncertain / ignore it); or verify the EOF
boundary before the first append. **Exact test:** write a complete intent line WITHOUT the trailing
`\n`, reopen (assert it does not silently become the committed authority), append a second intent,
reopen again, assert the second intent is not lost to a joined line.

### H3 — load path can BLOCK on a FIFO (MEDIUM)
The WRITE path Lstat-refuses a non-regular sidecar (`:450-453`), but the READ path opens directly:
`root.OpenFile(observationsFile, O_RDONLY)` (`:186`) with no prior `Lstat`/`IsRegular` and no
`O_NONBLOCK`. A named pipe (FIFO) planted at `index/observations.jsonl` makes `O_RDONLY` **block store
open indefinitely** waiting for a writer. `os.Root` refuses symlinks (handled) but does not refuse a
FIFO inside the root. Trusted-dir caveat, but this is a hang, not a redirect, and it is an asymmetry
with the write path. **Fix direction:** mirror the write path's `Lstat`+`IsRegular` guard before the
read `OpenFile`. **Exact test:** `mkfifo index/observations.jsonl` (POSIX; skip on Windows) and assert
`Open` returns promptly with uncertain rather than blocking.

### H4 — Close lacks an obsPubMu barrier; a publish can write after close (MEDIUM)
`publishObservation`/`ReserveObservation` take `mutate()` (which checks `closed`) and THEN `obsPubMu`
(`:549-556`, `:567-573`); `Close`→`closeBody` sets `s.closed` (`fsstore.go:454`) but does **not**
acquire/drain `obsPubMu`. A publish that passed `mutate()` an instant before `Close` proceeds:
`appendObservationIntent` opens a FRESH `os.OpenRoot(Index)` handle (not a store-held handle) and can
write an intent to disk AFTER `Close` returns, then `applyIntentLocked` mutates the `s.mu`-guarded obs
maps of a now-closed store, and the subsequent `completeIntentLocked → recordSupersedingCore →
s.tuW.write` may hit a torn-down writer. Fail-safe-ish (the intent is pending and recoverable on
reopen) but it is a genuine write-after-close / closed-store-mutation lifecycle gap and a race with
`closeBody`'s teardown. **Fix direction:** `closeBody` should take `obsPubMu` (drain in-flight
publishes) before teardown, or `appendObservationIntent`/`reserveIntentLocked` should re-check `use()`
under `obsPubMu` before writing. **Exact test (racy, use a sync seam):** block inside the `obsSyncData`
seam mid-append, call `Close` concurrently, and assert either the append observes closed and aborts, or
`Close` waits — not that an intent lands after `Close` returns.

### H5 — committed-on-load Recover skips SyncPublication (LOW–MEDIUM)
`RecoverToolUseByObservation`'s `b != nil && b.committed` branch returns `s.ToolUse(...)` WITHOUT
`SyncPublication` (`observation_publication.go:697-698`). A load-committed binding's `committed` was
re-derived from the tool_use INDEX at load (`observationStateLocked`), not by re-verifying the object
closure this process; the original commit's `SyncPublication` ran in a PRIOR process. So Recover does
not re-verify the root's object closure is still present/intact (GC bug, corruption since commit) — it
returns the record (metadata), and the loss surfaces only at later byte retrieval. Whether Recover must
re-verify closure on a load-committed binding is a contract question for main. (`ToolUse` returns the
index record, not bytes, so this is not a false-publication, only a deferred-failure question.)

---

## 3. Lower / acceptable

- **H6 (target identity, OK):** `observationTarget` is `{ID,Root}` (not full session/tool). Adequate:
  the tool_use index is append-only (an ID's Root is immutable, a different Root is `ErrAppendOnly`), so
  `{ID,Root}` fully captures a target's content identity; session/tool are irrelevant to a supersede.
  Not a defect.
- **H7 (unattributed torn line, LOW residual):** a torn line whose `obs` field itself is unreadable
  (`probeObservation`→"") sets uncertain but marks no observation; a committed obs with its own valid
  line is still returned (committed is checked before the uncertain fallback). A hidden torn
  conflict-line for a committed obs is thus missed — but a second conflicting intent can only reach disk
  via corruption/foreign write (reservation refuses conflicts and refuses while uncertain), and the
  committed obs's own line was validly decoded. Consistent with the append-once model; low.
- **H8 (accounting drift, LOW):** `obsSidecarBytes` (seeded from load `totalBytes`, `+1` per line incl.
  empties and an LF-less tail) can diverge slightly from the true file size, making the append cap
  approximate — fail-safe (overcount → refuse earlier).
- **Explicit lifetime ceiling (note, not a defect):** at 64 MiB / 1<<20 entries the sidecar refuses new
  appends (`ErrBudget`, `:435-440`) and loads past them as uncertain — an explicit non-scaling refusal,
  the same "bounded storage: no" the delivery journal has. A long-lived project eventually cannot
  publish observations; whether the sidecar needs its own rollover (parallel to the delivery-capacity
  work) is a capacity question for main, distinct from correctness.

---

## 4. Non-acceptance

Read-only critical review of the current sidecar + store integration; no signoff, no all-fixed claim.
The prior F1/F3/F3.3/F4 items are addressed and the new audit/restore-proof integration holds in
source. The unresolved items to correct or consciously accept are H1 (exact-replay idempotence
defeated by live-state normalization — the sharpest, it breaks the crash-recovery the binding exists
for), H2 (LF-less tail → join → lost append), H3 (FIFO blocks load), H4 (Close lacks the obsPubMu
barrier), and H5 (load-committed Recover skips closure re-verify). Each names an exact test because the
behaviour is not proven by the runs I can see, and a source claim that existing tests are genuine is
not evidence these paths executed. I ran nothing; uncertainty about untested paths and platform
(FIFO/fsync) gates is preserved. Capacity journal/generation files are the other author's and unreviewed.
