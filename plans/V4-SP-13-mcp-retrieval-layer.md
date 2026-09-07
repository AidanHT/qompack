# SP-13: L6 retrieval — stdio MCP and eight evidence-qualified tools

**Status:** original Wave 3 implementation user-reported complete; M2 amendments remain planned, no migration gate newly passed. SP-19 M0-00 first integrates completed SP-10–13 and accepts M0-G0 before its remaining tasks and corrective follow-ups. **Planning owner/model:** writer A, requested gpt-5.6-terra medium; coordinator consolidation; review in [ledger](MIGRATION-EVIDENCE.md).

**Branch:** existing `feat/sp13-mcp-retrieval-layer` | **Wave:** 3 (V4) | **Prerequisites:** completed SP-01/05/06/09 preserved; SP-19/M0 reconciliation, SP-20/M1 publication and M2 state/trust contracts | **Dependents:** SP-10/11 M3 recovery enablement, SP-21 M4 replacement | **Design:** Qompack.md §§8.3/8.7, 10–12 | **Gap:** G6.2 with residuals in TRACEABILITY.

## Mission

Make retained evidence discoverable and recoverable through the installed Claude Code plugin. Keep eight tool identities and compatible negotiated MCP versions while distinguishing historical evidence, current files, absence, unavailability, denial, ambiguity and expiry. A URI-looking string or passing handler mock does not establish installed-agent retrieval.

The initial sibling snapshot `../qompack-sp13` at `21e481e` contains hand-rolled stdio MCP work; M0-00 refreshes the final delivery tip before integrating it. Preserve that work and seven commit identifiers; subsequent SP-19 reconciliation determines the corrective remainder. Root `internal/mcp/tools.go`, `types.go`, `server.go` and `mcptest` are contracts, not absence-of-work evidence. The inspected snapshot's `handlers.go` returns absent with degraded status on a query error; confirm its current disposition rather than repeating an already-completed fix or interpreting an error as nothing tried.

## Design context (verbatim from Qompack.md)

The existing heading is retained; this section summarizes the v1.5 contract without reproducing obsolete code/schema examples. [Qompack.md](../Qompack.md) §8.7, [architecture §0.1](00-ARCHITECTURE.md), ledger E06/E07 and A06/A10/A11 govern recovery and trust.

Existing `negknow.Descriptor.MatchKey` excludes reason; `internal/negknow/ledger.go` and `bloom.go` already confirm positives exactly. Reuse them. `staleness.go` documents SP05-D1's missing-history risk; SP-20 repairs ingestion coverage before applicability claims. Preserve negotiated 2025-06-18, 2025-03-26 and 2024-11-05 behavior observed in the sibling unless target negotiation justifies a compatible extension. No mandatory latest-protocol or SQLite/FTS rewrite.

## Out of scope

Automatic command replay, native-history eviction, treating ephemeral metadata as deletion, native replacement before SP-21, path-pointer-driven instruction injection, unauthorized external search/services, or a Codex runtime port. Parser failure cannot justify fabricating semantic spans.

## Interface contract

### Consumes

SP-20 durable event/object identities, fidelity, transformations, coverage, exact/indexed lookup and current state; checkpoint reader/decision provenance from SP-10 and qualified coverage reporter from SP-11. Core archive retrieval is independently testable before checkpoint/rehydration enablement; checkpoint-dependent tools can report unavailable while those readers are unfinished. This avoids a circular requirement that M3 must be enabled before M2 can be verified.

### Produces

A versioned response envelope carrying evidence identity, source type, scope/authorization, validity/applicability, capture fidelity, requested/returned span, omissions, pagination and structured error status. Preserve displayed and structured semantics. Distinguish absent, unavailable, uncertain, denied, expired/deleted and corrupt. Existing three-way and any boolean callers need explicit adapters/deprecation, not a silent type/meaning change.

| Stable tool | Future behavior and acceptance gate |
|---|---|
| `recall` | Authorized exact/path/symbol/event/decision and sufficient full-text search over retained evidence; T13-SEARCH/T13-TRUST |
| `expand` | Resolve stored identity/tool-use handle, qualified span or full captured artifact under budget; T13-HANDLE/T13-HISTORY |
| `re_read` | Captured historical file version; latest recorded is not current disk, and history never falls back to disk; T13-HISTORY |
| `already_tried` | Exact applicability with coverage/uncertainty and unavailable query errors; T13-STATE |
| `record_eliminated` | Record scoped attributed claims/candidates and dependency coverage, reason outside lookup identity; T13-STATE |
| `timeline` | Observed event order plus arrival/relationship gaps, not invented total history; T13-LIFE |
| `why` | Attributed decision/evidence provenance, conflicts and supersession; unavailable reader remains explicit; T13-STATE |
| `dropped` | Compatibility name for qualified coverage/recovery report; no assertion of complete native eviction; T13-COVERAGE |

A current-file read remains the host's separately authorized operation. If Qompack later exposes one, its contract/name must explicitly distinguish it from historical `re_read`; no ninth tool is silently added here.

## Implementation spec

### 1. Protocol and compatibility

Reconcile existing JSON-RPC framing, panic/error paths, registration, initialization, input validation and output schema support against each negotiated protocol. Preserve old callers using explicit compatible envelopes/adapters. An unsupported schema/version produces safe degraded/error status, not guessed optimization. Inspect installed packaging and tools/list/tools/call behavior in disposable sessions under SP-19.

### 2. Indexes and spans

Start with the current store's lookup/search facilities; inspect `internal/store/query.go`, `search.go`, `tooluseindex.go`, `files.go` and sibling MCP handlers. Fill only demonstrated gaps in identity, path, symbol, event, decision and text search. If FTS5 is chosen later, specify backfill/rebuild consistency and the SP-20 optional engine gate; a current engine sufficiency decision also satisfies the branch.

Test punctuation, identifiers, spaces, malformed queries, unsupported language parsers, no-hit and stale-index behavior. Separate semantic chunks from physical storage chunks, recording parser/version/coverage and deterministic textual fallback. Exact historical claims are relative to retained captured bytes; partial Read, redacted/normalized-only legacy or missing originals never become full source reconstruction.

### 3. Recovery and state

Verify every advertised handle resolves under the supported agent operation and policy after compaction. Pagination and minimal spans preserve omissions and fidelity. Errors remain errors; a missing object is not no-match and a current file is not a historical replacement.

Keep immutable observations distinct from the SP-20 current-state view. User requirements, decisions, agent hypotheses, tool observations and extracted candidates retain authority, scope, dependencies, conflict and supersession. Unknown dependency coverage yields uncertain. Exact records control eliminations; positives are confirmed, stale/incomplete-filter negatives are bypassed, and heuristic failure patterns cannot prohibit a viable action.

### 4. Trust and resource limits

Authorize before search previews and before expansion. A hash is not permission. Validate paths/symlinks/encodings, object size and decompression bounds. Archived reads cannot bypass host-denied paths. Treat malicious stored text as data with provenance, never promoted instruction. Secrets/retention policy covers logs, indexes, checkpoints, exports and backups; no secure-erasure guarantee across media.

Privacy-denied evidence follows denial/redaction policy even on optimization failure. No automatic replay, external credentials or service use as a retrieval fallback. Disk-full, permission, lock and decode failure are explicit outcomes.

### 5. Own-result metadata and shared composition

Mark already-processed wrappers so they are not recursively captured as independent primary evidence; references retain their original provenance. Optional demand tracking measures frequency separately from usefulness and changes only future Qompack representations (SP-16). No same-epoch residency assumption.

Preserve the existing Wave 3 bootstrap handoff: SP-11 owns initial resident handles; SP-13 main owns MCP/reporter extension and checks one intended store/ledger/graph per daemon process. Serialize composition-root changes with SP-10/11/12 owners; do not copy sibling stand-ins over their current code.

## Test plan (TDD)

No execution now. Existing future commands: `go test ./internal/mcp/...`, `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, `go run ./tools/devtool plugin-validate`. Reconcile sibling MCP/daemon/e2e test definitions first. Installed archive-recovery, schema-version and trust canaries are future entry points to add if absent; skipped cases leave the capability unverified.

| Gate | Observable future acceptance / artifact |
|---|---|
| T13-PROTOCOL | Known/older/unknown protocol and malformed request/output cases preserve schema/error semantics; versioned stdio and packaged-host transcript |
| T13-SEARCH | Exact identifier/path/symbol/event/decision/text, punctuation/no-hit/stale-index/backfill cases; query parity/index-coverage manifest |
| T13-HANDLE | Installed agent discovers and resolves handles after compact, with bounded pagination and explicit unavailable objects; integration transcript |
| T13-HISTORY | Historical/current/partial/legacy/redacted/corrupt cases never substitute disk data or claim missing originals; fidelity/span manifest |
| T13-STATE | Correction/conflict/hypothesis/stale elimination/filter rebuild/query-error cases preserve authority/uncertainty and caller compatibility; state lineage artifact |
| T13-TRUST | Denied previews/expansion, symlink escape, malicious text, secret/size/decompression bounds; authorization and bounded-processing audit |
| T13-LIFE | Duplicates, arrival-order gaps, parallel/failed child and branch/worktree changes remain scoped; event relationship trace |
| T13-COVERAGE | Included/archive-only/native-load-observed/expired-or-deleted/unknown remain qualified with epochs and fidelity; report fixture |
| T13-ROLLBACK | Old reader/caller compatibility and unavailable/error behavior before/after new-format writes; verified backup/restore or compatible-reader drill |

B-F latency is a measurement target. Record real parser/startup/I/O/locking, payload size and tail behavior; do not infer universal 15 ms or prompt savings.

## Commit plan

Retain seven numbered identifiers; reconcile existing completion through SP-19, then make only the necessary future amendments. Contract/tests and compatibility accompany changes.

### Commit 1 — `fix(mcp): reconcile stdio protocol and error contracts`

- [ ] Inspect the existing server, add missing T13-PROTOCOL cases and retain negotiated-version compatibility; no SDK rewrite by default.

### Commit 2 — `feat(mcp): version evidence-qualified tool envelopes`

- [ ] Preserve eight tool identities, document/add explicit old-caller adapters, fixtures and schema capability gates.

### Commit 3 — `fix(mcp): distinguish historical spans from current files`

- [ ] Integrate SP-20 publication/fidelity with exact lookup and span fallback; verify T13-HANDLE/HISTORY including unavailable originals.

### Commit 4 — `fix(mcp): preserve state authority and retrieval uncertainty`

- [ ] Reconcile recall/already_tried/record_eliminated/timeline/why/dropped, exact filter/error semantics and authorization before previews.

### Commit 5 — `fix(mcp): qualify own-result and demand metadata`

- [ ] Preserve provenance/idempotence without native-eviction claims; test bounded demand records and own-wrapper feedback.

### Commit 6 — `feat(cli): integrate compatible MCP daemon recovery`

- [ ] Main integrates resident handles and discoverable packaged stdio tools, with degraded/no-reader paths and SP-11 handoff.

### Commit 7 — `test(mcp): verify archive recovery trust and rollback`

- [ ] Add missing installed-host/failure/migration tests, future generated tool docs and quiet-run latency evidence; retain all gate artifacts and independent review.

## Subagent strategy

Future A/B/C/D-1/D-2 roles preserve the 2026-08-26 independent-authoring partition. Current planning agents are separate. Main agrees shared contracts/fixtures before future authoring; integrations and seven commits remain sequential. Absent names below are proposed files, not assertions of missing sibling work.

| Future role | Exclusive ownership |
|---|---|
| Main | MCP shared types/tool schemas/handlers_common/shared fixtures, composition-root extension, integration and commits |
| A protocol | MCP jsonrpc/server/schema files and protocol-specific tests |
| B spans | MCP span/handlers_span and span/history tests |
| C knowledge/index tools | MCP handlers and state/search/coverage tests; no shared preamble |
| D-1 metadata/docs | MCP ephemeral/promote/observable and tool-doc generator/tests |
| D-2 wiring | Daemon mcpop, CLI cmd_mcp/mcpwire, MCP e2e/bench tests; waits for protocol contract |
| Independent trust/recovery reviewer | Read-only target negotiation, privacy, history, error and rollback artifacts |

SP-20 owns storage/state internals; this plan owns MCP consumers. Shared CLI/bootstrap edits are main-only after SP-11 hands off. D-1 may draft with A/B/C once schemas are agreed; D-2 consumes the real server. Benchmarks are recorded after the future fan drains on a quiet machine.

## Exit criteria

- [ ] T13-PROTOCOL through T13-ROLLBACK have actual versioned artifacts.
- [ ] Installed archive lookup/expansion works before M3 recovery or M4 replacement enablement.
- [ ] Historical/current, absence/error and evidence/authority remain distinct.
- [ ] Denied content cannot leak in snippets; unknown coverage cannot become a prohibition.
- [ ] Old callers/readers remain supported or explicit version/degraded paths and verified restoration are available.

## Done checklist

- [ ] Future seven-commit remainders, live conformance and V4 integration are accepted with actual results.
- [ ] SP-20/SP-11 interfaces and composition-root ownership reviewed before integration.
- [ ] Planning-only review recorded separately in MIGRATION-EVIDENCE.md.

**Rollout/rollback:** core exact retrieval first, then integrated qualified coverage; output replacement stays SP-21 opt-in. Disable affected retrieval/optimization paths independently while preserving evidence and explicit unavailable responses. Roll back using compatible reader/protocol adapters or verified backup after incompatible writes; never silently reinterpret new records.

**Blockers:** SP-19 protocol/installed-host inventory (owner host tester, canaries; no target certification until passed); SP-20 durable capture/import and coverage (owner storage, crash/backup drill; no exact recovery claim); SP-13 search sufficiency (retrieval owner, query/parity corpus; add indexing only if insufficient); SP-13 trust/old-caller compatibility (independent reviewer, T13-STATE/TRUST/ROLLBACK; keep affected features disabled).
