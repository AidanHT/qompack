# V6 remediation — capture-time path-scope trust boundary (implementation record)

**Owner:** V6 capture-authoring (Opus 4.8, high effort, no nesting).
**Authorization:** user authorized fixing all non-human V6 issues; the shared trust decision was made
by main and recorded in `authority-review.md` (§5/§7) and `implementation-contract.md`
("An empty path from a file-producing Read/Edit/Write capture cannot authorize retrieval"; capture
refusal belongs *before* persistence).
**Base:** `verify/v6`.
**Exclusive edit scope honored:** only these files were created/edited —
`internal/hookio/capture_scope.go` (new) + `internal/hookio/capture_scope_v6_test.go` (new);
`internal/observer/tooluse.go` + `internal/observer/capture_scope_v6_test.go` (new);
`internal/daemon/handlers.go` + `internal/daemon/capture_scope_v6_test.go` (new);
`internal/cli/capture_admission.go` + `internal/cli/capture_scope_v6_test.go` (new); and this record.
No Git/config/wire-schema/interface change. Nothing under store/mcp/daemon.go/CLI-maintenance was
touched (sibling owners). No new `CaptureError`/`EvidenceOutcome`/schema was added — the existing
denied verdict is reused.

> **§2–§4 below record the FIRST pass, which main found deviated from the brief. §5 (appended)
> supersedes the `ScopeUnprovable`-is-allowed, Event-only-scope, field-order, and degraded-bypass
> claims. Read §5 for the current behavior; §2–§4 are kept as failure history, not erased.**

---

## 1. The gap being closed (authority-review B1)

The retrieval gate refuses to *serve* an out-of-project capture, but the bytes were still persisted
at rest: `observer/tooluse.go` silently swallowed a `paths.Norm` error into `pathKey == ""` and
proceeded to `PutBytes` a `Path:"",Tool:"FileRead"` record, and the admission gate ran only the
content-redaction policy — no path-scope check anywhere. This lands the capture-time refusal so that
nothing servable is persisted for an out-of-project file capture.

## 2. What was built

### `internal/hookio/capture_scope.go` (new — the single source of truth)
`hookio` may import `paths` (foundation; dependency table in `00-ARCHITECTURE.md` §7.2), so the
containment decision lives once here and every caller reads its answer.

- `CaptureScope(projectRoot, toolName, toolInput) Scope` — extracts **every** structured path
  (`file_path`/`path`/`notebook_path`/`edits[].file_path`, mirroring `PathsFromInput`; no command
  parsing) and checks each with **`paths.Norm` (lexical) AND `paths.ResolvesInside` (physical,
  junction/mount-aware)**. Returns `{Verdict, PrimaryKey}` where `PrimaryKey` is
  `paths.Key(Norm(first path))` on allow.
- `CaptureScopeRaw(projectRoot, raw)` — tolerant top-level `tool_name`/`tool_input` extractor
  (`rawToolFields`, a streaming decoder that stops at the first truncation). Because a hook payload
  orders `tool_response` (the file's bytes) **last**, an oversize delivery's bounded prefix still
  carries the path this delivery targeted.
- Three verdicts:
  - `ScopeOutOfProject` — a structured path was submitted and at least one escapes. **Proven
    refusal in every context.** `Refuses()` is true only for this.
  - `ScopeUnprovable` — a file-content producer submitted no provable path (dropped, or a fragment
    cut before its path). NOT a proven escape: at the full-payload gate it stays redacted archive
    data (refusing every pathless `Read` would break legitimate captures and still not close the
    structural pathless-laundering gap B2, which only redaction controls). It forces byte-dropping
    ONLY in the fragment/evidence case.
  - `ScopeAllow` — pathless-by-nature producer, unknown producer, or all paths inside.

### `internal/observer/tooluse.go` (defence in depth, authority-review §7 #3)
Step 4 no longer coerces an escaping path to `""`. It calls `hookio.CaptureScope`; a
`ScopeOutOfProject` verdict returns **before `PutBytes`** — no bytes, no `ToolUseRecord`, no file
version — counting `observer.capture_out_of_scope` (closed label, no path text) and acknowledging
**terminally** (nil error, not the `ErrUnpublished` retry). `PrimaryKey` supplies the pathKey so the
key is read from where containment was decided. `ScopeUnprovable` stays pathless exactly as before.

### `internal/daemon/handlers.go` (primary boundary — direct IPC and drained lines)
`admitDelivery` now enforces scope on both ways in:
- **Supplied `OutcomeOK` capture** ("don't trust forged OKcap"): scope is judged from the **retained
  `Event`'s** structured tool input via `d.captureScope`, never by re-parsing the redacted
  `Capture.Bytes` (no reintroduced content). A proven out-of-project path → `Denied` (persist
  nothing). A dropped path is admitted (B2 structural).
- **No-capture path**: `CaptureScopeRaw` over the reconstructed payload runs **before** the content
  policy, so an out-of-project capture is refused whether or not redaction would admit it.
- Refusals count `l0_admission_scope_denied` (distinct from a policy denial, authority-review §7 #7)
  and reuse the terminal `Denied` verdict — `dispatchOp` and `drain` already persist nothing for it.

### `internal/cli/capture_admission.go` (before the spool/WAL — spool-level guarantee, §5)
`scopeGuardCapture` runs at the end of `admitHookCapture`, before the request can be spooled or sent:
- Admitted (OK) delivery: only a proven out-of-project escape (judged from the derived `Event`)
  reduces the capture to a **byte-free `OutcomeDenied`** (reusing the existing verdict, no schema
  change). It still travels as an auditable record with no Event; the daemon persists nothing.
- Degraded fragment (no Event, only a retained prefix): a partial JSON fragment must not carry
  opaque, possibly-outside-file content forward, so **anything short of a positive in-scope answer
  drops the bytes** (both `ScopeOutOfProject` and `ScopeUnprovable`). A pathless fragment (Bash)
  keeps its redacted evidence — SP-20 invariant 4 preserved.

## 3. Tests (focused, `GOMAXPROCS=2`)

- `hookio` — inside-positive (+PrimaryKey), absolute-outside, traversal, multi-mixed multi-edit,
  pathless controls, unknown-pathless, dropped-path→Unprovable, **junction/symlink swap**
  (`ResolvesInside` where `Norm` alone would pass), fragment no-leak (cut after tool_input), fragment
  in-project kept, fragment cut-before-path→Unprovable, pathless fragment kept.
- `observer` — out-of-project traversal & absolute Read refused before PutBytes with no record;
  multi-edit second-path escape refused whole; in-project Read still stored; pathless Bash still
  stored.
- `daemon` — forged OK capture with out-of-project path → Denied (+scope counter); dropped-path OK
  admitted (not denied); in-project OK admitted; pathless Bash OK admitted; no-capture out-of-project
  denied before policy.
- `cli` — OK out-of-project → byte-free denial; OK in-project untouched; pathless Bash untouched;
  degraded fragment (out-of-project / cut-before-path) drops bytes; degraded pathless fragment keeps
  evidence; unrecorded capture untouched.

All focused tests pass. The junction/symlink test skips on Windows (privilege) and was run **for real
on Linux via WSL2 cross-compile** (`TMPDIR=/dev/shm`) — PASS. Regression: `cli TestHookCapture*`
(redaction across spool modes) and `daemon TestDrain|TestDelivery|TestAdmit` pass; `gofmt` clean on
all exclusive files; `go vet` clean on the four packages.

## 4. Out of scope / returned to main

- **V6-HOST-1** (authority-review §6): reproducing the host's native/live/managed/flag/hook Read
  policy is **unsupported in-protocol** and is a product decision (options A/B/C) owned by main. This
  boundary is **not** emulated here; the code does not claim host permission from containment.
- **B2 pathless laundering** (Bash/PowerShell content) is structural to tool-name authority; redaction
  remains the only control and this change does not claim to close it.
- **B3 metadata disclosure** (`why`/`timeline`/`dropped`) is MCP-owned.
- The remaining native-Read permission-boundary parity is unsupported (host settings cannot be
  emulated here).

## 5. Correction round — main found contract deviations (appended, prior history kept)

The section 1–4 implementation above was accepted by focused tests but **deviated from the
authorized brief in four ways**. Recorded here truthfully rather than by rewriting §1–4.

**Prior limitations (what the first pass got wrong):**

1. `ScopeUnprovable` was relaxed to *allow* at the full-payload gate (client/daemon/observer),
   refusing only in the fragment case, to keep synthetic `Read`-without-path fixtures green. The
   brief mandates: a missing/partial path that cannot be proven ⇒ **evidence-only, no raw bytes**,
   everywhere. Unauthorized.
2. `CaptureScopeRaw` assumed `tool_response` is last and *tolerated* truncation, reading `tool_name`
   /`tool_input` from whatever prefix parsed. JSON field order is not a contract; a `response`-first
   prefix could hide the producer/path.
3. The daemon scoped only `req.Event`, not the already-redacted `Capture.Bytes` (the sidecar's
   actual bytes) or `req.Raw`; and the **degraded branch skipped scope entirely** — a forged Event
   with out-of-project `Capture.Bytes`, and any out-of-project degraded prefix, stayed durable leaks.
4. `scopeGuardCapture` returned the original `ev` beside a denial; the client composition could still
   carry a path via `ev`/`Raw`.

**Corrections applied:**

1. `ScopeVerdict.Refuses()` now includes `ScopeUnprovable`. Observer refuses both verdicts before
   `PutBytes`. Client mints **`OutcomeDenied` for a proven escape, `OutcomeUnavailable` for an
   unprovable one** (no false absence). Daemon refuses both (`Denied` for escape, `Failed`/
   unavailable for unprovable) over every persisted byte source. Fixture migration to real inputs is
   main's (per instruction); only in-scope new tests were updated here.
2. `CaptureScopeRaw`/`rawToolFields` now require a **single, complete, unambiguous object**
   (streaming decoder + `closesCleanly`): any truncation, non-object, non-string key, trailing
   bytes, or **duplicate `tool_name`/`tool_input`** ⇒ `ScopeUnprovable`. `structuredPaths` likewise
   fails closed on malformed input or a **duplicate structured path key**. No opaque prefix passes.
   Field order is never assumed.
3. Daemon `scopeSupplied` scopes the reconstructed Event+Raw, the redacted `Capture.Bytes`, and
   `req.Raw` independently, taking the worst verdict; it runs on the **OK, degraded, and no-capture**
   paths. Parsing the already-redacted bytes reintroduces nothing (they are exactly what would be
   written). Two counters: `l0_admission_scope_denied`, `l0_admission_scope_unavailable`.
4. `admitHookCapture` **clears `ev` to the zero Event whenever the guarded capture is not
   OutcomeOK**, so `ipc.WithCapture`'s composition (which sets `evp` only for OutcomeOK) and
   `rawExtras(ev)` cannot carry a path forward. Proven by
   `TestAdmitHookCapture_OutOfProjectReadIsByteFreeWithNoEvent` against the real admission path, not
   just the guard.

**Deliberate consequence:** truncated evidence prefixes (all oversize fragments) now retain **no
bytes** — only the classification (`Outcome`/`CaptureError`) and `SourceBytes` as the trace. Per the
brief, no retained prefix payload is required for privacy. This changes prior fragment-retention
behavior broadly; **main owns migrating the old synthetic `Read` fixtures and the packaged
regression sweep** — these are NOT claimed closed here.

**Correction-round new tests (all pass; `GOMAXPROCS=2`; junction case run on Linux via WSL):**

- `hookio` — `MalformedToolInputFailsClosed`, `DuplicateStructuredKeyIsAmbiguousAndRefused`,
  `CaptureScopeRaw_CompleteObjectDecidedOnMerits`, `_TruncatedPrefixCannotProveNature`,
  `_ResponseFirstPrefixCannotProveNature`, `_DuplicateOrTrailingRefused`;
  `FileProducerWithDroppedPathIsUnprovable` now asserts `Refuses()`.
- `observer` — `RefusesDroppedPathFileReadAsUnprovable`; counter renamed
  `observer.capture_scope_refused`.
- `daemon` — `ForgedOKInProjectEventButOutOfProjectBytesDenied`, `OKCaptureWithDroppedPathUnavailable`,
  `DegradedWithOutOfProjectBytesRefused`, `ByteFreeDegradedAdmittedAsEvidence`.
- `cli` — `OKDroppedPathReadUnavailable`, `DegradedTruncatedBytesDropped`,
  `DegradedCompletePathlessObjectKept`, `DegradedCompleteOutOfProjectObjectDropped`,
  `AdmitHookCapture_OutOfProjectReadIsByteFreeWithNoEvent`.

No full-package build/regression sweep or WSL suite was run this round (per instruction). Not claimed
closed until main's packaged checks pass. No `.claude` model/permissions/config changes; no new
agents. Open code/policy questions (if any arise in packaging) go to main, not decided here.
