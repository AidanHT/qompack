# Task 6 report — Commit 6 `docs(sp18): define and execute evidence-based UAT`

**Routing.** Requested: model `claude-opus-4-8`, effort high. Observed for myself: the model id this
session reports is `claude-opus-5[1m]` ("Opus 5 (1M context)"). The requested id is not the one I
observe; recorded here rather than assumed to be equivalent.

**No scenario was executed; every row records capability unverified.**

Commit: `0621f39 docs(sp18): define and execute evidence-based UAT` (3 files, +1364).

## Files changed

- `docs/uat.md` — new, 1113 lines.
- `test/docs/uat_test.go` — new, 250 lines, in the existing `test/docs` package, reusing
  `repoRoot`, `readLines`, `defenced`, `inlineCodeRe`.
- `test/docs/owned_test.go` — one line: `"docs/uat.md"` appended to `ownedDocs`. Nothing else.

No other file was touched. No `git stash`, no push.

## The page

Header carries: purpose; the isolation rule (disposable project, one project per scenario, never an
active user session, terminate only the daemon you started by the `pid` in `.qompack/run/daemon.lock`);
what a skip costs ("a skipped integration leaves the corresponding capability unverified", and the
row must name which capability); the six record fields as a table; a "How to run a scenario"
procedure naming the snapshot to record (`qompack version`, the bundle's git SHA, host OS, Claude
Code version); and the rollback rule — take the copy of `.qompack/` before the first write, restore
it after, verify by comparing `.qompack/index/roots.jsonl`, `.qompack/index/tool_use.jsonl` and
`.qompack/checkpoints/MANIFEST.jsonl` line for line against the copy.

Then twelve `## UAT-NN` sections, each with **Scenario**, **Preconditions**, **Steps**, **Expected
observable result**, **Evidence to record**, **Failure and rollback outcome**, **Result**, in that
order.

## Row by row: what is runnable today, what needs SP-17, what is confirmed

Marked `[requires SP-17 artifact]` throughout: installation, any step performed inside a live Claude
Code session, upgrade, uninstall. Confirmed = read from a named source file on this tree, or probed
(see "Probes" below). TBC = written as "to be confirmed at execution".

- **UAT-01** (install/version/tool discovery). Runnable today: `qompack version`,
  `qompack config print --provenance`, `qompack self-test`, reading the committed
  `plugin/.claude-plugin/plugin.json` and `plugin/hooks/hooks.json`. Needs SP-17: install, and the
  host's own hook/tool listings. Confirmed: `0.1.0` from `qompack version` (probed) and from
  `core.Version`; plugin.json `version: 0.1.0`; the seven hook events; the eight MCP tools; the ten
  gated switches all `false`, printed as JSONC leaves in their blocks (`"newResult": false  //
  default config.Defaults()`, probed) rather than as dotted lines; the self-test row inventory and
  the `not-yet-implemented` / no-observation semantics. TBC: what the contract assertions observe
  against a real installed host.
- **UAT-02** (startup/recording, fidelity). Needs SP-17 for the session; `self-test` and `status` run
  today. Confirmed: the seven self-checks + nine contract assertions; `status`'s provenance line and
  the `unavailable`-with-a-reason rule; the eight `Fidelity` values from `internal/core/evidence.go`.
  TBC: which non-`exact` fidelity an oversized or binary capture actually takes on a live host.
- **UAT-03** (local checkpoint durability). Needs SP-17 for the compaction. Confirmed: the artifact
  name shape `NNNN.json` and `checkpoints/MANIFEST.jsonl` (`internal/paths/manifest.go`); the
  checkpoint fields that carry the frontier and the references (`encoded_segments`,
  `pointers.files`, `pointers.tools`, from `internal/checkpoint/types.go`); that
  `runtime.migration.publication.durableFrontier` is gated off so durable publication is out of
  scope for a pass. **Finding, written into the row:** the explicit incomplete outcome exists as
  `checkpoint.Resolution{FellBack, Skipped, Reason}` and `ChainResolution{Truncated, MissingFrom}`
  with the reason string `checkpoint NNNN does not verify; rolled back to NNNN`
  (`internal/checkpoint/resolve.go`), but a repo-wide grep finds callers only in that package's own
  tests — **no production caller**. The row says the user-visible form is unknown and gives the
  probe that would settle it.
- **UAT-04** (manual/automatic/failed compact). Needs SP-17 for every compaction. Confirmed: no veto
  path exists; `blockManualCompact` is hardwired false and "the key exists only to say so";
  `automaticVeto` gated off; a hook always exits `0` and prints `{}`; the failed-compaction lever
  (an unknown key making `config.LoadForCapture` refuse) is the one recorded in troubleshooting §6;
  `custom_instructions` is PreCompact input (retired-meaning table). The "no native input shrink"
  check is a search step over the run's own captures.
- **UAT-05** (rehydration budget, authority, overflow). Needs SP-17 for the session. Confirmed: the
  injection tags `<!-- qompack:injected seq=%d ver=%d -->` / `<!-- /qompack:injected -->`; the fixed
  section order; the hard-cap rule (ADR 0011); the newest-first evolution build order that makes a
  correction survive truncation (`internal/rehydrate/items.go`); the `intent_mismatch` drop kind;
  **both overflow markers by their exact text** — kind `overflow` id `payload` with detail
  `OVERFLOW: the injection wrapper alone (… tokens) exceeds the rehydration budget (… tokens);
  nothing was injected` (`build.go`), and the tier-1 entry id `tier1` with detail `OVERFLOW: tier-1
  material did not fit the rehydration budget and is emitted whole or not at all` (`budget.go`);
  `degraded: true` and the counted tail `- … and N more; call dropped()`; the state file
  `.qompack/state/rehydrate-<session>.json`. Honest caveat recorded: the injected payload prints **no
  per-line authority label** — the `user_correction` vocabulary is `internal/core/evidence.go`'s and
  is read through records, so the observable here is ordering plus `intent_mismatch`.
- **UAT-06** (repeat compact/resume/fork/correction). Needs SP-17 throughout. Confirmed: section 2 is
  verbatim L0 and intent is resolved by derived id, not relevance search; `why` is attribution, not
  compliance; no PostCompact dependency, the one tested adapter being `SessionStart source=compact`.
  TBC: whether any field other than seq/timestamps differs across a fork.
- **UAT-07** (historical retrieval). Needs SP-17 for the session/MCP client. Confirmed: the three
  `recall` selectors; `expand` by `hash` or `tool_use_id` and its `_meta.qompack` fields with **no**
  `source`; `re_read`'s `source: store` / `at` / `turn`; the no-live-disk rule (step 6 edits the file
  and expects the captured version back); `found: false` for a miss vs `unavailable` for a failure.
  TBC: which of those two a never-stored hash yields on a live daemon.
- **UAT-08** (elimination, exact confirmation). Needs an MCP client. Confirmed field-by-field from
  `internal/mcp/handlers.go` and `internal/mcp/tools.go`: the acknowledgement's `id`, `descriptor`,
  `scope`, `evidence`, `depends_on`, `depends_on_unresolved`, `status`, `warnings`; `active`'s
  `reason`, `evidence`, `scope`, `recorded_at`, `depends_on`, and `stale_because` absent; the
  `_meta.qompack.bloom_only` unbacked-hit case.
- **UAT-09** (changed dependency / stale / unavailable). Needs an MCP client. Confirmed: `stale`
  with `stale_because`; `staleResponse: drop` mapping to `uncertain` **not** `absent` (the
  `renderAnswer` comment is explicit); `unavailable` with `degraded: true`, a reason and a recovery
  note, disclosing nothing about a record; the no-prohibition rule. TBC: the exact `reason`/`note`
  constants the `drop` path substitutes. The V5-report §22 item 26 blind-ledger `[active]` partial is
  written in as a known gap, not a pass.
- **UAT-10** (observation/accounting). Runnable today for `status`; `eval` runnable today too.
  Confirmed: the envelope shape; the provenance quartet (`source`/`status`/`age_ms` null when
  unknown/`measure`); the `unavailable`-with-reason latency rows and the B-D reason; the six usage
  categories `input, output, cache_read, cache_write_5m, cache_write_1h, thinking_output` in
  canonical order with `{"known":false}` for unknown and a `missing` list
  (`internal/eval/ledger.go`). **Correction to the brief's framing, recorded in the row:**
  `qompack status` does not print usage categories — no production caller writes the eval ledger —
  and `qompack eval` reports `no evaluation artifacts are readable in this build` and exits `1`
  (`internal/commands/cmd_eval.go`). The row therefore expects exactly that message and exit status,
  and says that a usage table appearing there would mean the seam was bound and the row needs
  rewriting. The V5-report §24 uncertainty `partial` is a known gap, not a pass.
- **UAT-11** (opt-in admission / pass-through / pointers). Steps 1–4 are runnable today against a
  locally built binary in a scratch directory; steps 5–6 need SP-17. Confirmed: `newResult` `false`
  at origin `default` (probed); the refusal recorded as `invalid value, using default: true not in
  false`; the newer-`settingsVersion` block reset; the capture loader's no-fallback behaviour and the
  probed table in troubleshooting §6 (hook prints `{}`, exits `0`, `.qompack/` not created, host
  payload untouched — that is what pass-through means here, and nothing entered the store so the
  privacy policy holds); section 6's heading saying contents are not restored and `expand`/`re_read`
  are how you get them.
- **UAT-12** (privacy/backup/upgrade/uninstall). Steps 1–5 and 8 runnable with a session; 6, 9, 10
  need SP-17. Confirmed: `denied` is not empty and not `absent`, and the archive is never a
  denied-read bypass; `runtime.mcp.maxResponseBytes` default `262144`, minimal chunk-aligned spans,
  `full: true` = whole *available* object, `next_span` paging, `binary` fidelity; the upgrade's three
  compatibility classes; that an uninstall deletes nothing under `.qompack/`. Recorded prominently:
  **there is no operator backup/restore command** — `TakeBackup`/`VerifyBackup`/`RestoreBackup`/
  `RehearseRollback` sit behind the closed `store.migrate.legacyImportCutover` build gate — so the
  pre-run copy and the index comparison are the operator's own, and step 8 is the only
  `Rollback verified: yes` the page recognizes. TBC: the host's exact denied-read behaviour and the
  exact upgrade warnings.

Every Result block is byte-identical in shape:

```text
Result: not executed — capability unverified
Snapshot: develop 9c84e31 / branch feat/sp18-documentation-and-uat
Date: 2026-09-14
Executed by: —
Evidence: —
Rollback verified: —
```

## RED / GREEN evidence

RED (tests and `ownedDocs` in place, `docs/uat.md` absent):

```
--- FAIL: TestOwnedDocsExist (0.08s)
    owned_test.go:35: docs/uat.md: ... The system cannot find the file specified.
--- FAIL: TestUATRetainsAllTwelveIDs (0.12s)
--- FAIL: TestUATSectionsHaveTheRecordShape (0.07s)
--- FAIL: TestUATUnexecutedRowsSayUnverified (0.08s)
--- FAIL: TestUATConfigKeysExist (0.09s)
FAIL	github.com/qompack/qompack/test/docs	2.818s
```

First GREEN attempt failed on two real false positives in my own key parser —
`records/eliminations.jsonl` and `eval.json` were being read as configuration keys because their
first segment collides with a config section. Fixed by rejecting a candidate preceded by a path
separator and one whose trailing segment is a file extension (`uatIsConfigKeyMention`), both
documented in the test.

GREEN: `ok  github.com/qompack/qompack/test/docs  2.813s`.

**Mutation checks** (each reverted immediately; `git status` after the last one showed only the three
intended files):

- appended `` `runtime.rehydrate.notAKey` `` and `` `store.chunk.target` `` →
  `TestUATConfigKeysExist` failed on the first and ignored the second (`uat_test.go:245:
  runtime.rehydrate.notAKey is not a key row in docs/config-reference.md`). The test is not vacuous
  after the false-positive fix.
- renamed one `**Evidence to record**` and stripped one `capability unverified` →
  `TestUATSectionsHaveTheRecordShape` reported `UAT-01: missing **Evidence to record**` and
  `TestUATUnexecutedRowsSayUnverified` reported `Result is "not executed", which is neither pass nor
  fail, and the record does not say "capability unverified"`.

## Validation before committing (run once, in this order)

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	2.813s

$ go vet ./test/docs/...
(no output, exit 0)

$ go run ./tools/devtool fmt-check
(no output, exit 0)

$ git status --short
 M test/docs/owned_test.go
?? docs/uat.md
?? test/docs/uat_test.go
```

`TestRelativeLinksResolve` and `TestOwnedDocsExist` are in that package run and passed, so every
relative link and heading anchor on the new page resolves.

## Probes and their cleanup

Pre-existing `qompack` processes before any probe: **none** (`Get-Process | ? ProcessName -like
'*qompack*'` returned nothing). Two probes, both outside the repository, in
`…\scratchpad\uatprobe\proj`:

1. `go build -o …\scratchpad\uatprobe\qompack.exe ./cmd/qompack`, then `qompack version` in an empty
   directory → printed `0.1.0`, exit `0`, and `ls -a` showed the directory still contained only `.`
   and `..` — no `.qompack/`, no daemon.
2. `qompack config print --provenance` in the same directory → exit `0`, empty stderr, no `.qompack/`
   created; the gated leaves printed as e.g. `"newResult": false  // default config.Defaults()`,
   confirming the ten switches are `false` at origin `default` and confirming the *form* an operator
   reads them in.

Cleanup: re-checked for `qompack` processes — none, so **no daemon was started and nothing was
terminated**; I killed no process at all. The whole `uatprobe` directory, including the built
binary, was deleted (`Test-Path` afterwards returned `False`). No UAT step was run against a real
Claude Code session.

## Self-review findings

- Every required content item in the brief's per-ID list is present; I re-read the brief against the
  diff row by row.
- Claims policy: no fabricated result, output, date or executor; no performance, cost or savings
  claim; no native-control claim; files that do not exist (`docs/install.md`, `docs/security.md`,
  `docs/release.md`, the bundle) appear only as plain "planned (SP-17)" text, never as links; every
  step needing the bundle carries `[requires SP-17 artifact]`.
- Every citation says what is attributed to it (a file name plus the thing read from it, or
  "observed on this tree" with the probe), and three behaviours are explicitly marked unknown with
  the action that would settle them: the user-visible form of a checkpoint fallback (UAT-03), the
  `staleResponse: drop` strings (UAT-09), and the host's denied-read/upgrade-warning behaviour
  (UAT-12).
- Two places where I did not simply transcribe the brief, both in the direction of honesty, both
  stated in the page itself: UAT-10's usage categories are not on `status` and are unreachable
  through `eval` in this build; UAT-05's payload carries no authority label.
- Commit subject is 48 characters; no trailers (the repo's `check-commit-msg` hook accepted it).

## Concerns

1. **Two shipped pages now under-describe the tree.** `docs/user-guide.md` and
   `docs/troubleshooting.md` both still end with `planned (SP-18 Commit 6): docs/uat.md`, which is
   now stale — the file exists. I was forbidden to edit them, so Commit 7 (or the reviewer) should
   turn those two lines into links to `uat.md`. Nothing fails today: they are plain text, not links,
   so the link test is silent about them.
2. **UAT-03's incomplete-outcome expectation is a probe, not an assertion.** `ResolveLatest` and
   `ResolveChain` have no production caller. If V6-VERIFY wants a pass condition rather than an
   investigation, that is a code question (wire the resolver into the rehydration path), not a
   documentation one.
3. **UAT-10's usage categories are unreachable in this build.** The row's pass is "eval says no
   artifacts are readable and exits 1", which verifies honesty rather than accounting. Real usage
   accounting stays unverified until `Deps.EvalArtifacts` is bound (V5-report §29 item 3).
4. **Ten of the twelve rows cannot be executed at all** until SP-17 ships the bundle; UAT-11 steps
   1–4 and parts of UAT-01/UAT-10 are the only work runnable today. The page is therefore a
   liability register as much as a test plan, and it says so.
