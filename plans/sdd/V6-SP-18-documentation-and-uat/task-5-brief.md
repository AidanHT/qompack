# Task 5 — Commit 5 `docs(sp18): state capability limits and upstream proposals`

Plan text (verbatim): "Prepare limitations and issue text matched to evidence; review unsupported/native/service boundaries; do not send issues here."

Plan §1 rows (verbatim):
- "docs/cannot-do.md — Native cuts/markers/history eviction unsupported, no model-compliance/native-byte proof, no missing-original reconstruction, no universal improvement"
- "docs/upstream-issues.md — Evidence-linked proposals for host limitations; no assertion an issue was filed without an artifact"

Qompack.md v1.5 §12 "What this plugin cannot do" (verbatim, the binding source): "It cannot promise native-history cuts, marker control, deletion of already-delivered results, summarizer model substitution, exact native loaded bytes, model compliance, universal savings or complete capture of unobserved child work. The production plugin must function without native compaction request/veto. It cannot reconstruct uncaptured history by reading current files, infer cache state from arbitrary elapsed time, or turn an archive into a denied-read bypass."

Qompack.md v1.5 §12 "Upstream issues worth filing separately" (verbatim): "SP-18 may prepare issue text for missing host observability or supported controls. Sending issues requires separate authorization. No new external service or account change is authorized by this plan."

Worktree: `C:\Users\Quant\Documents\Programming\Projects\qompack-sp18`, branch `feat/sp18-documentation-and-uat`.

## Files you own

- `docs/cannot-do.md` — new.
- `docs/upstream-issues.md` — new.
- `test/docs/limits_test.go` — new, in the existing `test/docs` package (reuse its helpers).
- The `ownedDocs` list in `test/docs` — append both pages. Nothing else in that file.

Do NOT edit any other file. Never `git stash`. Never push. Never open, draft in a tracker, or send an issue anywhere; the pages are text only.

## Sources (read all; cite the file and section beside every limit and every proposal)

1. Qompack.md §12 (lines 426–466): the risk register table and the two subsections quoted above; §7.3 (line 309) hook surface; §5 (cache figures, if you cite a cache claim); Appendix A (mathematical reference: which claims are diagnostics, not guarantees).
2. plans/QOMPACK-ERRATA.md — what was confirmed, changed, and could not be verified about host and cache behaviour (grep its headings; cite the rows you use).
3. plans/V5-report.md §24 (plan-vs-code drift, `implemented_unverified` items), §29 (open items 3–7, 11–13).
4. `docs/mcp-tools.md` intro (ephemeral metadata is not a host eviction control; unavailable never establishes absence).
5. `internal/contract` package comment and `internal/cli/selftest.go` check names whose OBSERVED reads `not-yet-implemented` on this tree (the hook contracts Qompack cannot yet assert: `hook.additional_context_delivered`, `precompact.has_time_to_write`, `precompact.custom_instructions_accepted`, `mcp.server_registered` — confirm by reading the source; do not run the binary for this task).
6. `docs/config-reference.md` "Gated switches (ship off)" and the hardwired keys (`runtime.migration.compaction.blockManualCompact`, `runtime.telemetry.enabled`).
7. `docs/commands.md`: `/qompack:checkpoint` not yet routed.
8. `docs/adr/0010-wall-clock-under-coload.md` (timing under co-load), `docs/adr/0009-negative-knowledge-bloom-as-cache.md` (Bloom is a cache), `docs/adr/0013-migration-contracts.md`.

## `docs/cannot-do.md` — required content

One entry per limit, each with: **Limit** (one sentence), **Why** (the host or design reason, one or two sentences), **What Qompack does instead** (the supported behaviour), **Recorded at** (file:section). Required entries, grouped:

1. **Native context control** — no native-history cuts; no marker control; no deletion of already-delivered results; no history eviction; no native compaction request or veto (`automaticVeto` gated off; `blockManualCompact` hardwired false); an `ephemeral` tag is a Qompack record property.
2. **Proof** — no proof of model compliance with injected material; no exact native loaded bytes (`hook.additional_context_delivered` not yet asserted); no summarizer model substitution; the rehydration budget is Qompack-added material, not restored native context; no PostCompact dependency.
3. **Reconstruction** — no reconstruction of uncaptured history from current files; a missing or `redacted`/`truncated`/`binary` original stays that way; no complete capture of unobserved child work.
4. **Guarantees Qompack does not make** — no universal improvement or savings; no performance guarantee on any host (ADR 0010: wall-clock under co-load; hosted-runner timing tails); no cost/price guarantee (usage vs estimated price vs invoice); no name-availability guarantee (plugin/command/tool names may collide with other plugins; nothing reserves them); no inference of cache state from elapsed time.
5. **Trust boundary** — an archive is never a denied-read bypass; redaction is applied at capture; telemetry is hardwired off.
6. **Not in this build** — `/qompack:checkpoint` unrouted; `doctor`/`fsck` unimplemented; installed-host compatibility `implemented_unverified`; the enabled-surface matrix rows that are "mechanism only" (V5-report §24 SP-21 table).

Close with a short "How to read the rest of the docs" paragraph: a page says "planned (SP-NN)" when the artifact does not exist on this tree.

## `docs/upstream-issues.md` — required content

Header sentence: these are proposals prepared by SP-18; none has been filed; a proposal becomes an issue only with separate authorization, and this page then records the URL beside it. A **Status** column that reads `not filed` for every row on this tree.

One section per proposal, each with: **Host limitation** (what Claude Code does not expose or guarantee today, stated as observed), **Evidence** (the file:section or the self-test check name that shows Qompack cannot assert it), **Proposal** (the smallest host change that would let Qompack verify or drop a limit), **What Qompack would do with it**, **Status: not filed**. Required proposals (derive the exact wording from the sources; do not invent host behaviour):

1. Observability of delivered `additionalContext` (whether and how much of SessionStart `source=compact` additional context reached the model) — evidence: `hook.additional_context_delivered` not-yet-implemented; ADR 0011.
2. A PreCompact contract for `custom_instructions` acceptance and a time-to-write guarantee — evidence: `precompact.*` checks; Qompack.md §7.3.
3. A post-compaction event (PostCompact) or an equivalent signal, so a plugin need not infer compaction from the next SessionStart — evidence: plan's "no PostCompact dependency"; `internal/contract`.
4. Per-hook latency attribution instead of the folded `hook_controlled` aggregate — evidence: `qompack status` "no per-hook instrument" rows (quote from the status implementation source).
5. MCP server registration visibility (whether the host registered the plugin's `.mcp.json` server) — evidence: `mcp.server_registered` not-yet-implemented.
6. Usage telemetry per request category (cache read/write/input/output) exposed to plugins, so estimated price need not be inferred — evidence: V5-report §25 and Qompack.md §5/Appendix A "Request price … unknown stays unknown".
7. A supported native compaction request/veto or marker API — evidence: §12 "must function without native compaction request/veto"; gated switches; state that Qompack does not depend on it and would keep the switch gated.

Do not claim any of these was filed, discussed with, or acknowledged by anyone.

## Tests (`test/docs/limits_test.go`; RED then GREEN)

1. `TestCannotDoCoversSection12Limits` — the page contains each of these phrases (the §12 nouns, verbatim as listed here): `native-history cuts`, `marker control`, `deletion of already-delivered results`, `summarizer model substitution`, `exact native loaded bytes`, `model compliance`, `universal savings`, `unobserved child work`, `compaction request/veto`, `reconstruct uncaptured history`, `cache state from arbitrary elapsed time`, `denied-read bypass`.
2. `TestUpstreamIssuesAreAllUnfiled` — every proposal section (each `## ` heading after the header) contains `Status: not filed`, and the page contains no `https://github.com/` issue URL (a URL with `/issues/` in it fails the test — the moment one is filed, the author must flip this test deliberately with the artifact).
3. `TestCannotDoNamesTheUnimplementedChecks` — parse `internal/cli/selftest.go` for the check names whose observed value is the literal `not-yet-implemented` (find the declaration pattern; derive, do not hard-code the list) and assert each appears in `docs/cannot-do.md` or `docs/upstream-issues.md`.
4. `TestRelativeLinksResolve` and `TestOwnedDocsExist` pass.

## Validation before committing

```
go test -count=1 ./test/docs/...
go vet ./test/docs/...
go run ./tools/devtool fmt-check
```

## Claims policy (binding)

No performance/cost/savings/name-availability guarantee; no native-control claim; no "filed" claim; every limit and proposal cites its source; files that do not exist are plain "planned (SP-NN)" text; no fixed counts in tests; no external service or account is mentioned as used.

## Commit

Exactly one commit, subject exactly `docs(sp18): state capability limits and upstream proposals`. Body: the limit groups, the proposal list, the tests, and the sentence "No issue was filed or sent." No trailers.
