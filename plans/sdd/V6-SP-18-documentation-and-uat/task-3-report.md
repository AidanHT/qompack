# Task 3 report — Commit 3 `docs(sp18): explain scoped recovery and command behavior`

Commit: `6996795`, on `feat/sp18-documentation-and-uat` (parent `4acc129`). One commit, no trailers.

## Routing

Requested: `claude-opus-4-8`, effort medium (care raised to high on supported-scope and
failure-state claims). Observed: the session reports itself as **Opus 5 (1M context), model id
`claude-opus-5[1m]`**. The requested route was therefore **not** the effective route. Per
`plans/MIGRATION-EVIDENCE.md` R1 ("a requested route is not proof of the effective route … Record
requested versus observed model/effort"), this is recorded rather than worked around. Effort is not
exposed to me, so the effort half is `not exposed`.

## What I implemented, section by section

`docs/user-guide.md` (new, 439 lines). Sections in brief order:

1. **Who this is for and what to expect.** A Claude Code session with the plugin installed; what
   Qompack adds after compaction (one bounded checkpoint-derived block through `SessionStart`
   `source=compact`, citing `docs/architecture.md` §7); three one-sentence limits — no native
   cut/eviction (`MIGRATION-EVIDENCE.md` capability decisions), no proof the model complied
   (injection "documented; installed behavior unknown"; `why`'s own "does not prove model
   compliance"), no improvement claim (`plans/V5-report.md` §26) — each pointing at
   `docs/architecture.md` §10, and closing with the plain-text pointer `planned (SP-18 Commit 5):
   docs/cannot-do.md`. Also carries the early write warning for `qompack status`.
2. **Slash commands.** One subsection per command in `docs/commands.md` order (status, recall, pin,
   checkpoint, why, dropped, eval). Each gives purpose, arguments, an anchor link into the
   generated page for the `--json` shape (never a restated schema), exit codes, and the states it
   can report. `/qompack:checkpoint` is stated as installed and **not yet routed**, with the
   `internal/cli/qompack_commands.go` nil-dependency comment and V5-report §29 item 3 as sources.
   `/qompack:dropped` is explained as qualified coverage — what "dropped" means and does not mean,
   with the mcp-tools sentence "This report does not establish what remains in native context".
   `/qompack:eval` explains the baseline as `eval.Report.Baseline`, "the Policy every Regression is
   measured against" (`internal/eval/types.go`), the three verdicts, the unjudged-metric rule, and
   that the artifact seam ships unbound so the command reports unavailable and exits 1.
3. **MCP tools.** A shared preamble carrying the ephemeral rule, the `unavailable` rule, minimal
   spans / `full` / `next_span`, and the miss-is-not-an-error rule; then one subsection per tool in
   `docs/mcp-tools.md` order (recall, expand, re_read, already_tried, record_eliminated, timeline,
   why, dropped) with purpose and when a model should call it. `record_eliminated` is marked
   durable rather than ephemeral; `expand` and `already_tried` each carry the `unavailable`
   semantics explicitly.
4. **Current vs historical reads.** `re_read` resolves `at` (empty / RFC3339 / `sha256:` /
   `turn:N`) exclusively against the store's own history, never disk, with the capture-policy
   reason from `currentVersion`'s doc comment. How to tell: the response body's `source` has exactly
   one value, `store` (`sourceStore`, "There is deliberately no 'worktree' counterpart"), beside
   `at`, `turn`, `hash`, `span`, `total_bytes`, `truncated`, `next_span`, `widened` — so no response
   shape means "read from disk just now".
5. **Fidelity, coverage and error states.** One table per enumeration with a one-line user meaning
   each, derived from `internal/core/evidence.go`; then the two explicit rules, "`unavailable` is
   not `absent`" (with ADR 0013's complete-fresh-coverage rule) and "a denied read is not an empty
   read".
6. **Current-authority corrections.** The six `Authority` values with what produced each;
   "content cannot promote its own authority"; `user_correction` outranks `hypothesis` and keeps
   doing so because a record cannot change its authority after admission (architecture §5); why
   repeated compact/resume/fork does not promote obsolete intent (ADR 0011 §10: derived id, never
   `Store.Search`; L0 wins over the checkpoint and the disagreement is Loud). The recorded partial
   is stated plainly with its source (V5-report §24 and §29 item 11).
7. **Additional context: budget and overflow.** ADR 0011 in user terms — hard cap never raised,
   importance order, prefix truncation, "never truncated means never partially emitted", whole-rule
   restoration, explicit overflow with the persisted complete drop report; config pointers; the
   budget-is-Qompack-added sentence; the injection kill switch.
8. **Operator commands.** A which-of-them-write paragraph (probe evidence below), the
   `--set` note, a table covering status, `config print [--provenance]`, `config schema`,
   `self-test`, `version`, `admin delivery-seal` (daemon must be stopped), `eval import`, `daemon`,
   `mcp`, and the slash-command binaries; a "not implemented in this build" paragraph for `doctor`,
   `fsck` **and `bench`**; and the "unavailable latency rows are honest gaps" explanation with its
   two recurring reasons from `statuscollect.go`.
9. **Usage accounting.** Subscription usage, estimated API price and a reconciled invoice as three
   separate notions, from `internal/eval/ledger.go` and V5-report §25; the four `PricingMode`
   values; cost never decides an eval verdict; no rate quoted anywhere.
10. **Where to look next.** Links to config-reference (plus its Versioned blocks, Gated switches
    and Retired-meaning keys anchors), commands, mcp-tools, architecture and the ADR index; then
    plain-text planned pointers for troubleshooting / cannot-do / uat (SP-18 Commits 4-6) and
    install / security / release (SP-17).

`test/docs/userguide_test.go` (new) and one appended line in `test/docs/owned_test.go`.

## RED / GREEN evidence

RED (page absent, tests written first):

```
--- FAIL: TestOwnedDocsExist (0.07s)
    owned_test.go:31: docs/user-guide.md: ... The system cannot find the file specified.
--- FAIL: TestUserGuideCoversEveryGeneratedCommandAndTool (0.05s)
--- FAIL: TestUserGuideStatesTheBindingQualifications (0.05s)
--- FAIL: TestUserGuideMarksCheckpointAsUnrouted (0.10s)
FAIL	github.com/qompack/qompack/test/docs	1.200s
```

First GREEN attempt caught a real defect in my own draft — a line-wrapped binding sentence:

```
--- FAIL: TestUserGuideStatesTheBindingQualifications (0.06s)
    userguide_test.go:73: docs/user-guide.md: does not state, verbatim: An ephemeral tag describes a Qompack record; it does not mean the host evicted anything.
```

GREEN after unwrapping it: `ok github.com/qompack/qompack/test/docs 1.186s`.

**Negative controls** (each reverted immediately; working tree restored and verified clean):

- renaming every `re_read` mention to `reread` →
  `userguide_test.go:52: docs/user-guide.md: no heading or code span names the MCP tool "re_read"`.
- replacing every `not yet routed` in the guide →
  `userguide_test.go:131: docs/commands.md still marks /qompack:checkpoint "not yet routed", and the guide's section does not say so`.

So neither derived test is vacuous.

## Validation before committing

Exactly the three commands in the brief, run on the final tree (no lint, no whole tree, no race,
coverage or replay):

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	1.136s
$ go vet ./test/docs/...
(no output, exit 0)
$ go run ./tools/devtool fmt-check
(no output, exit 0)
```

## Files changed

- `docs/user-guide.md` — new, 439 lines.
- `test/docs/userguide_test.go` — new, 137 lines.
- `test/docs/owned_test.go` — one line: `"docs/user-guide.md"` appended to `ownedDocs`.

Nothing else. No generated page, no README, no architecture page, no `plugin/`, `internal/`,
`.github/` or `plans/` file was touched. No `git stash`, no push.

## Claims marked unknown, and why

Only one, and it is on this report rather than on the page: the **effective model route** (above).

Two places where I deliberately said less than the brief's framing, because the tree says less:

- **`bench` is also unimplemented.** The brief's §8 listed `bench` among the operator commands and
  named only `doctor` and `fsck` as unimplemented. `internal/cli/commands.go` registers all three
  in the same `notImplemented` table, and the built binary confirms it: `qompack bench` prints
  `qompack bench: not implemented in this build` and exits 1. The page says so, and cites
  `plans/V5-report.md` §29 item 7 ("`bench` CLI survivor — SP-17 implements or deprecates"). This
  is a correction to the brief's fact list, not a disagreement with a decision.
- **`config print` does not create `.qompack/`.** The brief's fact list said running `qompack
  status` *or* `qompack config print` in a directory with no `.qompack/` creates it and starts the
  daemon. Probed: `status` does both; `config print` did neither, in a fresh empty directory. The
  page names `status` as the write and says `config print` and `version` did not, attributing it to
  the probe rather than to a source file.

## The scratch-dir probe and its cleanup

- Built `go build -o <scratchpad>/qompack.exe ./cmd/qompack` and ran it only in
  `<scratchpad>/probe` and `<scratchpad>/probe2`, both **outside** the repository
  (`C:\Users\Quant\AppData\Local\Temp\claude\...\scratchpad\`).
- Observed: `qompack help` lists the operator surface quoted on the page and creates nothing;
  `doctor`, `fsck` and `bench` each print `not implemented in this build` and exit 1;
  `qompack status --bogus` exits 2 (usage); `qompack status` created a full `.qompack/` layout
  (index, logs, metrics, spool, objects, records, state, …) in the probe directory **and started a
  daemon**; `qompack config print` in a second empty directory created nothing and started nothing.
- Cleanup: the daemon I started was PID **64244** (path under my scratchpad) and was stopped with
  `Stop-Process -Id 64244 -Force`. Two other `qompack.exe` processes on this machine belong to
  another session (`...\Temp\qompack-e2e-*\qompack.exe`) and were **not** touched. Both probe
  directories were deleted, and the scratch binary was deleted after the commit. Final
  `Get-Process qompack` shows only the other session's e2e processes.

## Self-review against the brief

- Files owned: exactly the three. ✔
- Generated pages linked, never edited; the `--json` shape is always an anchor link. ✔
- Every command and tool subsection is in the generated page's order. ✔
- Four binding sentences present verbatim (and pinned by a test). ✔
- Files that do not exist appear only as plain "planned (SP-NN)" text — verified by grep: lines 12,
  33, 436-439 are all prose, and `TestRelativeLinksResolve` passes, which it would not if one had
  become a link. ✔
- Claims policy: no latency, cost-saving, name-availability or performance guarantee; no native
  O(delta), free cut, first-turn saving, exact-native-context or Bloom safe-false-positive claim; no
  Codex planning models and no internal migration mechanics (the only migration key named is the
  injection kill switch, in user terms, as `docs/architecture.md` §7 already names it); no rate
  quoted. Every capability claim names a file, ADR, report section or package. ✔
- No fixed counts in the tests; both inventories are derived and the checkpoint condition is read
  out of `docs/commands.md`. ✔
- One commit, subject exact, no trailers (the commit-msg hook accepted it). ✔

Self-review findings I acted on during the task, rather than leaving:

1. The first draft stated exit codes only in the shared preamble and in the `status`/`eval`
   subsections; the brief asks for them per command. Added a one-line exit sentence to `recall`,
   `pin`, `checkpoint`, `why` and `dropped`, each an application of the generated page's stated
   rule rather than a new claim.
2. The recall exit line asserts that a search matching nothing exits 0. Checked before writing it:
   `internal/mcp/handlers_span.go` renders a miss as `found: false` and explicitly not as a tool
   error, and `recallBody` returns whatever the handler returns.

## Concerns

1. **Routing mismatch** (above): the work was done on Opus 5, not the requested Opus 4.8.
2. **Two brief facts did not hold on this tree** (`bench`, `config print`). The page follows the
   tree. If the coordinator intended a different disposition for `bench` — for instance leaving it
   out of the operator table entirely — that is a one-line change.
3. The page's exit-code sentences are derived from `docs/commands.md`'s stated rule rather than from
   an executed probe of each failure path; only `status --bogus` (exit 2) and
   `doctor`/`fsck`/`bench` (exit 1) were run for real.
4. `/qompack:eval`'s unavailability and `/qompack:checkpoint`'s unroutedness are both sourced from
   `internal/cli/qompack_commands.go`'s deliberate nil dependencies, not from running them in a
   session — I did not have an installed host to invoke a slash command from.

---

# Fix round 1 — three Important review findings

Commit: `ce52df3` `docs(sp18): correct the checkpoint, preamble and source claims`, on top of
`6996795`. One commit, `docs/user-guide.md` only (44 insertions, 19 deletions), no trailers. The
first attempt at this commit was rejected by `devtool check-commit-msg` for a 74-character subject;
the subject was shortened to 61 characters and re-committed unchanged otherwise.

The Minor findings are deferred to the final review, as instructed. Tests were not changed.

## Finding 1 — `/qompack:checkpoint` runs the hook and exits 0

**Confirmed before fixing.** `plugin/commands/checkpoint.md` runs `!`qompack checkpoint
$ARGUMENTS``; `internal/cli/hooks.go` registers `checkpoint` with `Hook: true` and
`doHook(hookSpec{op: ipc.OpCheckpoint, …})`; `internal/cli/dispatch.go` documents `ExitOK` as "the
ONLY code a hook subcommand may ever return"; `internal/cli/qompack_commands_test.go` asserts
`ExitOK` for `qompack checkpoint` with `"not json at all"` on stdin and requires the name to
resolve to exactly one entry, the hook. The unavailable-and-exit-1 path is
`internal/commands/cmd_checkpoint.go`, whose frontend `internal/cli/qompack_commands.go` and
`internal/cli/commands.go` both decline to register. My sentence was wrong and contradicted the
page's own operator section.

**Fixed.** The section now says the host offers the command, that the name is already the
`PreCompact` hook entry point, that invoking it runs the hook rather than writing a checkpoint on
demand, and that a hook always exits `0` whatever happens inside it — citing `internal/cli/hooks.go`,
`internal/cli/dispatch.go` and the test that pins it. It adds what actually happens when it is
typed by hand: with no hook event arriving on stdin there is nothing to classify, so it returns
empty output and does not even create the store (`internal/cli/hookclient.go`) — which is the
narrower, sourced version of "writes nothing". A closing paragraph records that the checkpoint-now
frontend exists and reports unavailable, but that nothing routes to it, so that is not the
behaviour you get from typing the command. "not yet routed" is still present, so
`TestUserGuideMarksCheckpointAsUnrouted` still holds for the right reason.

## Finding 2 — the preamble claimed three rules where the generated page states two

**Confirmed.** `docs/commands.md` lines 8-11 state the `--json`/`--help` rule and the exit-code
triple, and nothing else.

**Fixed.** The list is now "Two things … per that page's preamble". The exit-code bullet carries
the hook exception inline with its own citation (`internal/cli/dispatch.go`), which is what makes
it consistent with finding 1 rather than merely shorter. The unavailable-and-exit-1 rule became a
separate paragraph that is explicitly *not* attributed to the generated preamble, scoped to the
frontend whose source of answers is left unbound — `/qompack:eval` in this build — and explicitly
excluded for `/qompack:checkpoint`, citing `internal/commands` and
`internal/cli/qompack_commands.go`.

## Finding 3 — `source` is set by `re_read` alone

**Confirmed.** `internal/mcp/handlers_span.go`: `Source` is `json:"source,omitempty"` (:58); the
sole `Source:` assignment is in the `re_read` body (:263); `expand` passes `""` to both the body
(:156) and `spanMeta`, which drops an empty source (:434-436). So an `expand` response carries no
`source` field at all, and presenting it as the universal discriminator was wrong.

**Fixed.** The paragraph now attributes `source: store` to `re_read` specifically — the one tool
that sets it, and the one that could otherwise be mistaken for a live read — alongside its `at` and
`turn`. A second paragraph says what an `expand` response carries instead: no `source`, because it
is addressed by `hash` or `tool_use_id` rather than by a point in a file's history, and what you
get is the resolved `hash`, the `span`, and the shared `_meta.qompack` fields (`span`,
`total_bytes`, `truncated`, `next_span` where there is more, `path` where one is known,
`ephemeral`). The true conclusion is kept and sharpened: the discriminator is not one field on
every response, it is that **no** response shape means "read from disk just now".

## Covering tests, re-run after the fixes

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	1.080s
$ go run ./tools/devtool fmt-check
(no output, exit 0)
```

`git status --short` is clean at `ce52df3`. No probe was needed for this round: all three findings
were settled by reading the named source files, and no process was started or stopped.

---

# Fix round 2 — one Important defect introduced by fix round 1

Commit: `20678d8` `docs(sp18): replace the hook no-write claim with a probed result`, on top of
`ce52df3`. One commit, `docs/user-guide.md` only (+12/-5), no trailers.

## The defect

Fix round 1 replaced a wrong claim with a second wrong claim. I wrote that `qompack checkpoint`
typed by hand "returns empty output and does not even create the store
(`internal/cli/hookclient.go`)". The review is right that the cited file does not support the store
half, and I confirmed the mechanism before probing: `readHookCapture`
(`internal/cli/capture_admission.go:81`) returns `hookInput{Raw: raw}` with a **nil** error for an
empty read — only an `io` failure or an oversize payload produces one — so `hookclient.go`'s single
no-store guard, `if rerr != nil && !hookRefusalIsRecordable(root, in)`, never fires for empty stdin.
Everything downstream of it then runs.

This was my error in two ways: I reasoned from a doc comment about a branch that does not execute
on this input, and I attached a citation to a claim the cited file does not make — the exact failure
the page's own claims policy exists to prevent.

## The probe (option b)

Built `go build -o <scratchpad>/qompack.exe ./cmd/qompack` and ran, in a fresh empty
`<scratchpad>/probe3` outside the repository:

```
$ qompack checkpoint < /dev/null
exit=0
stdout: {}
stderr: (empty)
```

Observed afterwards in that directory:

- `.qompack/` created in full: `backup checkpoints dag eval{opt,replay} grammar index logs metrics
  migrate objects pins records run sketches spool state tmp`.
- Files written include `.qompack/.gitignore`, the five `index/*.jsonl`, `logs/qompack-*.log`,
  `records/eliminations.jsonl`, `sketches/tried.bloom`, `run/{daemon.hb,daemon.lock,marker.json,
  state.bin}`, the four `state/delivery-*` journals, `state/drain.json` and `state/history.json`.
- **The empty payload was recorded as a capture**:
  `.qompack/records/captures/1f/1f1e2446….json`.
- `state/history.json` shows the hook ran: `"last_precompact_ts":1789405903784`,
  `"precompact_wall_ms":[23]`, `"awaiting_compact_start":true`.
- **A daemon was started**: a new `qompack.exe` process, pid 32552, from the scratchpad binary.
- `.qompack/checkpoints` and `.qompack/spool` were **empty** — no checkpoint was written.

So the true statement is the opposite of the one I had written: it exits 0 and writes.

**Cleanup.** Pre-existing processes were recorded before the probe (one, pid 52096, another
session's `Temp\qompack-e2e-*` binary). Only pid 32552 — the daemon my probe started, running my
scratchpad binary — was stopped, by pid. `probe3` and `qompack.exe` were deleted; the scratchpad
now holds only the two excerpt files it had before. `Get-Process qompack` afterwards shows only pid
52096, untouched.

## The fix

The checkpoint section now ends the hook paragraph at the sourced part (always exits 0; checkpoints
are written at `PreCompact` from the host's event) and adds a short paragraph, **"Typing it by hand
is still a write"**, stating the probed result with "observed on this tree" wording: `{}` and exit
`0`, the full `.qompack/` layout created, the daemon started, the empty payload classified and
recorded, and `.qompack/checkpoints` empty so no checkpoint was written — an interactive invocation
is a write in that directory, not a no-op, and simply not a way to take a checkpoint.

The operator-commands write list was reconciled in the same commit. It previously said only that
the hook entry points "are invoked by Claude Code and always exit 0", which read as reassurance
next to a paragraph about which commands write. It now continues: exiting 0 is not the same as
doing nothing, `qompack checkpoint` run by hand with empty stdin created `.qompack/` and started
the daemon in the same probe, and every hook entry point should be treated as a write. The two
sections now agree.

## Covering tests, re-run after the fix

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	1.099s
$ go run ./tools/devtool fmt-check
(no output, exit 0)
```

`git status --short` clean at `20678d8`.
