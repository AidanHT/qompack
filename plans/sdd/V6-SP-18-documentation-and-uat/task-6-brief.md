# Task 6 — Commit 6 `docs(sp18): define and execute evidence-based UAT`

Plan text (verbatim): "Retain UAT-01–12, record actual future human results and rollback artifacts, preserve skips/failures."

Plan §1 row (verbatim): "docs/uat.md — UAT-01–12 with preconditions, steps, expected observable result, evidence fields and rollback/failure outcome".

Plan §3 (verbatim, the binding scenario table):

| Retained UAT ID | Revised future scenario / observable pass condition |
|---|---|
| UAT-01 | Install/version/tool discovery: actual bundle/host/OS recorded; unsupported optimization disabled |
| UAT-02 | Startup/recording: permitted capture identity/fidelity visible; gaps not hidden |
| UAT-03 | Local checkpoint: durable frontier/objects/references recover or explicit incomplete outcome |
| UAT-04 | Manual/automatic/failed compact: no optimization veto; PreCompact input semantics; no native-input shrink claim |
| UAT-05 | Rehydration: current authority and complete records under Qompack-added budget or explicit overflow |
| UAT-06 | Repeated compact/resume/fork/correction: no obsolete intent promoted, no PostCompact dependency |
| UAT-07 | Historical retrieval: exact/path/symbol/handle discoverability; unavailable not absent/current substitution |
| UAT-08 | Elimination: explicit scoped claim, observed dependencies, exact confirmation |
| UAT-09 | Changed/unknown dependency or stale index: uncertain/stale state, no filter-only prohibition |
| UAT-10 | Observation/accounting: usage categories, missing telemetry, diagnostics and uncertainty preserved |
| UAT-11 | Opt-in admission/disable: unrecognized schema or capture failure passes through within privacy policy; every pointer resolves |
| UAT-12 | Privacy/backup/upgrade/uninstall: denial before preview/expansion, bounded decoding and verified pre/post-write rollback |

Plan §3 prose (verbatim): "Run UAT only in future isolated permitted projects/disposable sessions; never destructive probes on an active user session. Each row records version/date/snapshot, command/steps, actual output, pass/fail/skip reason, evidence location and rollback. A skipped integration keeps the corresponding capability unverified."

Coordinator ruling (binding for this commit): SP-17's installable artifact does not exist on this tree, and no human UAT run is authorized in this session. Every UAT row is therefore written in full and its result block records `Result: not executed — capability unverified`, with `Snapshot: develop 9c84e31 / branch feat/sp18-documentation-and-uat`, `Date: 2026-09-14`, `Executed by: —`. Do not fabricate a run, an output, or a pass. Commit 7 and V6-VERIFY fill the rows later.

Worktree: `C:\Users\Quant\Documents\Programming\Projects\qompack-sp18`, branch `feat/sp18-documentation-and-uat`.

## Files you own

- `docs/uat.md` — new.
- `test/docs/uat_test.go` — new, in the existing `test/docs` package (reuse its helpers).
- The `ownedDocs` list in `test/docs` — append `"docs/uat.md"`. Nothing else in that file.

Do NOT edit any other file. Never `git stash`. Never push. Do not run any UAT step against a real Claude Code session; you may run the built binary only in a scratch directory outside the repository to confirm an expected output string, and must stop the daemon it spawns and delete the directory afterwards (say so in the report).

## `docs/uat.md` — required structure

Header: purpose; the isolation rule (disposable project, never an active user session); what a skip means (the capability stays unverified); the per-row record fields; a "How to run a scenario" paragraph naming the snapshot to record (`qompack version`, the git SHA of the bundle, host OS and Claude Code version), and the rollback rule (take the pre-run copy of `.qompack/` named in each row before the first write; restore it after; verify the restore by comparing the index files).

Then exactly twelve sections `## UAT-01` … `## UAT-12`, each with these labelled blocks in this order:

- **Scenario** — the plan table's text for that ID, reworded into one or two user-visible sentences without changing its meaning.
- **Preconditions** — bundle/version, host, configuration keys with the values the scenario needs (name real keys from `docs/config-reference.md`; e.g. UAT-11 uses `runtime.migration.replacement.newResult` staying `false` and the admission gate being off; UAT-04 references `runtime.migration.compaction.automaticVeto=false` and `blockManualCompact` hardwired false), and the pre-run backup of `.qompack/`.
- **Steps** — numbered, each an actual command or host action available on this tree (`qompack self-test`, `qompack status --json`, `/qompack:recall`, the MCP tools by name, `/compact`, resuming a session, editing a file, …). Where a step depends on an SP-17 artifact (installation via the bundle, upgrade, uninstall), write the step and mark it `[requires SP-17 artifact]`.
- **Expected observable result** — the exact fields/strings a human checks (e.g. self-test rows and OBSERVED values; `Fidelity`/`Coverage`/`EvidenceOutcome` values; `not yet routed` for `/qompack:checkpoint`; `unavailable` rather than absent; the rehydration block within `runtime.rehydrate.maxTokens` or an explicit overflow marker — read ADR 0011 and `internal/rehydrate` for what the overflow marker actually looks like and name it, or say the marker's text is to be confirmed at execution).
- **Evidence to record** — files/outputs to capture (command output, `.qompack/logs/…`, `.qompack/index/*.jsonl`, `--json` envelopes) and where to store them (a `uat/UAT-NN/` directory outside the repository; the location field is free text).
- **Failure and rollback outcome** — what a fail looks like, what a skip looks like, and the restore procedure for this row.
- **Result** — the record block: `Result: not executed — capability unverified`, `Snapshot: …`, `Date: 2026-09-14`, `Executed by: —`, `Evidence: —`, `Rollback verified: —`.

Required content per ID (in addition to the table text):
- UAT-01: record `qompack version`, plugin.json version, host OS, Claude Code version, the hook list from `plugin/hooks/hooks.json`, the tool list from `docs/mcp-tools.md`; every gated switch shows `false` in `qompack config print --provenance`.
- UAT-02: self-test table; first-session recording; a tool call whose result is captured with its `Fidelity`; a deliberately truncated/binary output shows a non-`exact` fidelity rather than nothing.
- UAT-03: checkpoint written at PreCompact; frontier/objects/references present after daemon stop/start; or an explicit incomplete outcome in the checkpoint status.
- UAT-04: manual `/compact` proceeds (no veto); automatic compaction proceeds; a failed compaction leaves the latest usable checkpoint; no claim of native input shrink appears anywhere in Qompack output.
- UAT-05: rehydrated block honours the budget; `user_correction` authority appears above older hypotheses; overflow is explicit.
- UAT-06: repeat compact/resume/fork; a corrected requirement is not superseded by an obsolete one; nothing in the flow depends on a PostCompact event.
- UAT-07: recall by content, by path, by symbol, by handle (`expand` by hash/tool_use_id); an unavailable original returns `unavailable`, not `absent`, and is not substituted with the current file.
- UAT-08: `record_eliminated` with evidence and depends_on files; `already_tried` returns `active` with the exact confirmation fields.
- UAT-09: change a depends_on file; `already_tried` returns `stale`/`uncertain`, and nothing prohibits the approach on filter evidence alone.
- UAT-10: `/qompack:status --json` usage categories; missing telemetry rows read `unavailable`; uncertainty preserved in the digest (note the recorded partial from plans/V5-report.md §24 as an expected known gap, not a pass).
- UAT-11: admission off; an unrecognized schema or a capture failure passes the original through; every pointer in the rehydrated block resolves via `expand`/`re_read`.
- UAT-12: a denied read yields `denied` before any preview; bounded decoding of large/binary objects (`runtime.mcp.maxResponseBytes`); pre-write backup, upgrade `[requires SP-17 artifact]`, post-write rollback restore verified, uninstall `[requires SP-17 artifact]`.

## Tests (`test/docs/uat_test.go`; RED then GREEN)

1. `TestUATRetainsAllTwelveIDs` — the page has headings `## UAT-01` through `## UAT-12`, each exactly once, in ascending order. (The twelve IDs are fixed by the plan; this is the one place a fixed inventory is the requirement, and the test says so in a comment.)
2. `TestUATSectionsHaveTheRecordShape` — every `## UAT-NN` section contains each labelled block (`**Scenario**`, `**Preconditions**`, `**Steps**`, `**Expected observable result**`, `**Evidence to record**`, `**Failure and rollback outcome**`, `**Result**`) in that order, and the Result block contains the lines `Result:`, `Snapshot:`, `Date:`, `Executed by:`, `Evidence:`, `Rollback verified:`.
3. `TestUATUnexecutedRowsSayUnverified` — every Result block whose `Result:` line is not `pass` or `fail` contains the phrase `capability unverified`.
4. `TestUATConfigKeysExist` — every `runtime.`/`checkpoint.`/`store.`/… dotted key mentioned in a code span in the page appears as a key row in `docs/config-reference.md` (derive both sets by parsing; no counts).
5. `TestRelativeLinksResolve` and `TestOwnedDocsExist` pass.

## Validation before committing

```
go test -count=1 ./test/docs/...
go vet ./test/docs/...
go run ./tools/devtool fmt-check
```

## Claims policy (binding)

No fabricated result, output, date, or executor. No performance/cost/savings claims. No native-control claim. Files that do not exist are plain "planned (SP-NN)" text. Steps that need SP-17 are marked `[requires SP-17 artifact]`.

## Commit

Exactly one commit, subject exactly `docs(sp18): define and execute evidence-based UAT`. Body: the twelve rows drafted, the record shape, the tests, and the sentence "No scenario was executed; every row records capability unverified pending SP-17's artifact and an authorized human run." No trailers.
