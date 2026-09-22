# V6 remediation — independent authority / trust review

**Seat:** independent non-authoring V6 trust review. Route requested and route-probe observed
`claude-opus-4-8` high; session `788ed24f-1a30-4096-b080-f98cfd4ab92d`.
**Date:** 2026-09-21 (America/Toronto authoring day).
**Nature:** source review only. No runtime acceptance is granted; nothing here clears a gate. No
children, no Git/config/permission/auth/billing mutation, no tests, builds or source edits were
performed. This document is the review's only output.

**Sources read (uncommitted + historical):**
`internal/store/provenance.go`, `internal/mcp/authorize.go`, `internal/mcp/handlers.go`,
`internal/mcp/handlers_span.go`, `internal/mcp/authorize_v6_test.go`,
`internal/store/capture_sidecar.go`, `internal/observer/tooluse.go`,
`internal/observer/identity.go`, `internal/observer/signals.go` (PathsFromInput),
`internal/paths/norm.go`, `internal/daemon/ingest.go`, `internal/daemon/handlers.go`
(admitDelivery), `plans/V6-report.md`, `plans/sdd/V6-remediation/implementation-contract.md`,
`plans/sdd/V6-remediation/recovery-design.md`. Host capability: independently fetched
`https://code.claude.com/docs/en/permissions` on 2026-09-21 (see §5).

Publication-scan and recovery code (`internal/store/publication_audit.go`,
`internal/daemon/publication_audit.go`, `recovery-design.md`) are sibling owners' scope and were
read only for boundary awareness; no finding below edits or depends on them.

---

## 1. Verdict at a glance

| V6 finding | Main's change | Does it close the finding? |
|---|---|---|
| V6-AUTH-1 (dropped path → pathless authority) | `authorizeOrigin` default-denies empty-path `Read`/`Edit`/unknown; `recall`/`expand`/`re_read` gate before materialization | **Retrieval side: yes.** **Capture side: no** — out-of-project bytes are still persisted at rest in the object store *and* the capture sidecar (B1, §3.1). |
| V6-AUTH-2 (hash/chunk address bypasses path check) | `authorizeHash` resolves *every* origin via `ContentOrigins` before any byte materializes; dedup cannot launder a restricted hash; `ResolvesInside` runs on id, root and chunk forms | **Yes**, for the retrieval boundary, subject to the tool-name-allowlist limit (B2, §3.2). |
| V6-HOST-1 (host native deny policy unavailable) | Code explicitly states containment ≠ host permission; no PASS claimed | **Unresolved by design.** No supported mechanism reproduces host policy; a product decision remains for main (§5). |

The retrieval-authority correction is well-shaped and internally consistent: an address is treated
as an address, provenance is resolved fail-closed, and the frozen wire/interface contracts are
untouched. It is **necessary but not sufficient** — it protects the MCP retrieval surface, not the
bytes at rest, and it cannot answer the host-policy question at all.

Concrete residual bypasses are B1–B4 (§6). Recommended minimum compatible corrections are §7.

---

## 2. What the retrieval correction gets right (confirmed by reading)

- **Address ≠ credential, enforced before materialization.** `expand` (both forms), `re_read`, and
  every `recall` hit run authorization *before* a preview or byte is produced
  (`handlers_span.go:216,236,290`; `handlers.go:155`). A well-formed hash or stored path is never
  itself proof.
- **All origins, no launder.** `authorizeHash` iterates the complete origin set and denies on the
  first restricted one (`authorize.go:106-110`); `ContentOrigins` returns *all* recorded origins
  after dedup including restrictive ones (`provenance.go:26-27,51-57`). The dedup-launder attack
  (V6-AUTH-2's "second permissive origin") is closed, and `TestV6_DedupDoesNotLaunderARestrictedHash`
  pins it.
- **Missing capability is not permission.** A store without `ProvenanceReader`, a cancelled context,
  a store error, or a bounded-incomplete scan all fail closed to `unavailable`
  (`authorize.go:98-105`; `provenance.go:44,92-94`); `TestV6_HashRefusesStoreWithoutProvenance`
  pins it. This matches the contract's "missing/unknown/bounded-incomplete fails closed."
- **Containment is real, not lexical.** `authorizePath` pairs `paths.Norm` (lexical + inward-only
  symlink) with `paths.ResolvesInside` (component-wise junction/mount-aware resolution,
  `norm.go:83-164`), which is the junction-swap defense V6-AUTH-2 needed. It runs on the
  `tool_use_id` path, the recall hit path, and (via `authorizeHash`→`authorizeOrigin`) every
  hash/chunk origin. There is no retrieval form that reaches bytes without it.
- **Frozen shapes preserved.** `ContentOrigin`/`ProvenanceReader` are additive; `ToolUseRecord`,
  `Root`, `Store` are untouched; `CaptureSidecar` remains the additive carrier. Good.

---

## 3. Residual concerns in the retrieval correction

### 3.1 The root cause is at capture, not retrieval — bytes persist at rest (RA-1 / bypass B1)

The retrieval gate refuses to *serve* an out-of-project capture, but the capture still happens and
is durable in two places:

1. **Object store.** `observer/tooluse.go:87-92` derives `pathKey` and **silently swallows the
   `paths.Norm` error**: a `Read` whose target escapes the root normalises to an error, the `if …
   err == nil` guard leaves `pathKey == ""`, and execution proceeds to `PutBytes` (`tooluse.go:100`)
   and a `ToolUseRecord{Path:"", Tool:"Read"}`. An out-of-scope file's bytes enter the store exactly
   as if they were pathless Bash output. This is the precise mechanism behind V6-AUTH-1 — a *lost*
   path and a *genuinely absent* path are made indistinguishable at the moment of capture.
2. **Capture sidecar.** `CaptureSidecar.Bytes` holds "the permitted host payload verbatim"
   (`capture_sidecar.go:48-49,68`) and is written at publication **stage 1**
   (`ingest.go:669-677`, `publishCapture`→`WriteCaptureSidecar`), which runs *before* the observer's
   path logic in stage 2 (`run`, `ingest.go:681`). The sidecar is deliberately outside `objects/`
   and is declared a GC **retention root** (`capture_sidecar.go:197-205`), so it survives even when
   no index record points at it. The out-of-project bytes therefore sit at rest, GC-protected,
   regardless of any retrieval decision.

**Consequence.** `authorizeOrigin`/`authorizeHash` are a read-time filter over data that should never
have been retained in that form. The report's own framing ("Pathless output remains untrusted archive
data, with current redaction still required") is honest for genuinely pathless producers, but for a
*file* read whose path was dropped it understates the problem: the bytes of an out-of-scope file are
persisted, not merely un-served.

### 3.2 Tool-name allowlist cannot distinguish genuine-pathless from dropped-path (RA-2 / bypass B2)

`authorizeOrigin` permits an empty path only for `Bash`, `PowerShell`, `UserPromptSubmit`,
`SubagentStop`, `mcp__qompack__record_eliminated` (`authorize.go:87-92`). This is the correct
*shape* — default-deny, named exceptions — and "unknown → denied" honours the contract. Two limits
remain:

- **Bash/PowerShell are inherently pathless in the record model.** `PathsFromInput`
  (`signals.go:211-237`) reads `file_path`/`path`/`notebook_path`/`edits[].file_path` only; a shell
  tool's `command` is never a structured path, so a `Bash` step that `cat`s an out-of-project file
  produces an empty path *legitimately* and is permitted. The tool-name allowlist cannot tell
  "Bash that ran `go test`" from "Bash that read `/etc/shadow`". This is the "legacy Bash/custom
  results whose explicit path got dropped" concern, and it is **structural**: tool name alone can
  never carry per-path scope for a pathless producer.
- **Compensating control is redaction, and it is real but bounded.** With no retrieval-side
  `Redactor`, `expand`/`re_read` return `unavailable` and `recall` withholds every summary
  (`handlers_span.go:181-184,315-318`; `handlers.go:142-146`, `redactor.go:40`) — so pathless bytes
  are *not* served in the clear in that build. **With** a redactor wired, pathless content **is**
  served after redaction. So B2's only defense in a production build is redaction *quality*, not
  authorization. That is acceptable *if and only if* it is stated as the boundary it is; it should
  not be presented as though authorization covers pathless content.

**This is why a capture-time decision is required (§4):** authority for pathless-but-scoped content
cannot be reconstructed at read time from a frozen record that dropped the path.

### 3.3 Metadata/index tools bypass the origin+redaction gate by design (RA-3 / bypass B3)

`timeline` returns segment metadata only (no content) — fine. `why` returns checkpoint decision text
(`What`, `Why`, `AlternativesRejected`) plus an evidence **hash pointer** and, via
`h.store.GetRoot`, the evidence object's **byte size** (`handlers.go:763-780`). It does *not*
materialize evidence bytes (it tells the model to call `expand`, which is gated). Two observations:

- The decision text is human-authored tier-2 checkpoint content, served with no `authorizeOrigin`
  and no `redactForRetrieval` pass. `dropped` likewise returns rehydrator entries directly.
- `why` discloses the existence and size of an evidence object regardless of that object's path
  scope.

Neither serves arbitrary file bytes, so this is **lower severity** than B1/B2. But the boundary
should be *stated*, not left implicit: "index/metadata tools (`timeline`, `why`, `dropped`) return
pointers and checkpoint-authored summaries outside the origin/redaction gate; this is acceptable only
because checkpoint decision text and object sizes are treated as non-sensitive." If checkpoint
summaries can contain quoted file content, that assumption needs its own decision.

### 3.4 Minor / acceptable

- **Bounded-incomplete cliff.** `maxProvenanceEntries = 65536` is a single budget spanning *all*
  roots, their chunk lists, tool-use records and file-history versions (`provenance.go:22-24,
  58-91`). A large store can exhaust it and render a legitimate hash `unavailable`. It fails **closed**
  (safe), but it is an availability cliff, not just a work bound — worth a comment and a metric so an
  operator can distinguish "denied" from "store too big to prove provenance."
- **`fileHist` synthesizes `Tool:"Read"`** for any path whose version references a matching root
  (`provenance.go:82-90`). Harmless for authorization (the path check governs), but note it labels
  Write/Edit-produced versions as `Read`; do not let any future consumer treat that `Tool` as
  ground truth.
- **Orphan chunk narrowing.** Because `authorizeHash` runs before the chunk-hash fallback in
  `resolveExpandTarget` (`handlers_span.go:236` vs `248-263`), a chunk not referenced by any indexed
  root now resolves to `unavailable` rather than being served. This is a small, correct
  fail-closed narrowing, not a regression to flag — noted for completeness.

---

## 4. Is an additive capture-authority sidecar needed?

**Not a new sidecar schema — but a capture-time authority *decision* is needed, and the existing
sidecar already has the slots to record it.** The gap is a missing *policy*, not a missing field.

- The retrieval layer cannot recover the distinction it needs (genuine-pathless vs dropped-path)
  because `ToolUseRecord` is frozen with `Path:""` in both cases and `ContentOrigins` reads only
  `rootIndex`/`toolUse`/`fileHist` — never the sidecar. Teaching `ContentOrigins` to join the sidecar
  for authority would be a larger, riskier change and would still leave the bytes at rest (§3.1).
- The existing `CaptureSidecar` already models a refusal: `Outcome` (`OutcomeDenied`/
  `OutcomeUnavailable`), `CaptureError`, `Redacted`, and an optional-`Bytes` shape
  (`capture_sidecar.go:61-68`). The admission gate already produces exactly these verdicts
  (`admitDelivery`, `handlers.go:1178-1274`). So a path-scope refusal can reuse the *denied /
  evidence-only* path that exists today: record the delivery as denied with `Bytes` empty, preserving
  a failure artifact without a schema rewrite — which is precisely what the implementation contract
  asks ("Retain original objects and capture evidence"; "no silent schema rewrite").

**Recommendation:** do **not** add a parallel authority sidecar. Add a capture-time path-scope rule
that emits the existing denied/evidence verdict, so nothing servable is ever persisted for an
out-of-scope file capture. The retrieval allowlist then stays a genuine *last resort* for producers
that are pathless by nature, never a laundering path for a dropped file path.

---

## 5. Where out-of-project capture must be refused, and the exact source boundary

The bytes reach durable state along one funnel with two persistence points; the refusal belongs at
the admission choke point, with the observer as defense-in-depth.

- **Primary boundary — `admitDelivery` (`daemon/handlers.go:1221`).** Its own doc comment states it
  runs "before anything is persisted" and that both ways in (direct IPC via `dispatchOp`, and
  spool/WAL via `drain`) reach it (`handlers.go:1146-1161`). It already returns
  `Denied`→persist-nothing / `OutcomeOK` / `Degraded`(evidence) / `Failed`(gap). **But it runs only
  the content *redaction* policy — there is no path-scope check anywhere in it.** This is the correct
  single place to add one: for a file-producing tool whose structured path input escapes `Norm` or
  fails `ResolvesInside`, return `Denied` (or an evidence-only degraded verdict), so `publishCapture`
  is never called and no sidecar bytes, `PutBytes`, or `ToolUseRecord` are written. The path input is
  present in the Event/Raw the gate already reconstructs (`reconstructedPayload`, `handlers.go:1299`).
- **Defense-in-depth — `observer/tooluse.go:87-92`.** Change the `pathKey` derivation to stop
  coercing an escaping path to `""`: distinguish "no path input at all" (pathless producer) from
  "path input present but out of scope" (refuse / mark, do not `PutBytes`). This is *not* sufficient
  alone — it is stage 2, after the sidecar's stage-1 write — but it closes the object-store leak and
  removes the silent laundering.
- **Spool caveat (be honest about the strongest guarantee).** `Accept` appends the *raw received
  line* to the WAL/spool **before** admission runs (`ingest.go:266-268`). The WAL is the transport
  durability boundary and is transient (drained, then removed), but the raw bytes do touch the spool
  transiently. To guarantee out-of-project bytes never reach the spool at all, the path-scope decision
  must also live in the **client/hook capture policy** that mints `req.Capture`
  (`ipc.WithCapture`/`hookio.CaptureHook`), before `ipc.Client.Send`. The durable *evidence* copy
  (the sidecar) is fully protected by the admission boundary alone; the spool-level guarantee needs
  the client-side decision as well. Main should decide which guarantee it is promising and say so.

**Preserve failure artifacts.** A refused out-of-project capture should leave an evidence-only sidecar
(`Outcome=Denied`, `CaptureError` set, `Bytes` empty) so the gap is auditable — matching UAT-12 /
troubleshooting discipline and the contract's "preserve original objects and capture evidence." Do
not silently drop the delivery.

---

## 6. V6-HOST-1 — the most important unresolved boundary

**Independent finding (official source).** I fetched `https://code.claude.com/docs/en/permissions`
on 2026-09-21 and independently confirm the following about the host's *native* file-Read policy:

- Effective Read authorization is a **layered, multi-source, partly-runtime** evaluation: managed
  (enterprise, non-overridable) settings, user settings, project settings, the `--disallowedTools` /
  `--allowedTools` CLI flags, session `/permissions` changes, and **PreToolUse hooks that evaluate
  permissions at runtime** ("Claude Code hooks … evaluate permissions at runtime … can deny the tool
  call, force a prompt, or skip the prompt"). Precedence is deny → ask → allow, deny-first, and a
  managed deny cannot be carved out by a project `!` rule ("The carve-out reaches only rules from the
  same source"). `Read` deny rules also govern Edit/Write on the same path.
- **MCP permission is server/tool-scoped**, keyed by `mcp__<server>__<tool>` (optionally a tool
  name), and is *separate* from per-path native Read. Nothing on the page exposes an API for an MCP
  server to **query** "would a native Read of path P be allowed right now?"
- **`requiresUserInteraction`** forces the host to *prompt the user* before an MCP tool's result is
  used ("MCP tools marked `requiresUserInteraction` also still prompt …"). It is a human-interaction
  gate, **not** a policy evaluation, and cannot reproduce a deny decision.

**Conclusion.** There is **no supported protocol mechanism** by which the Qompack MCP server can
reproduce or query the host's effective native Read deny policy. Static settings reconstruction
*provably* cannot capture CLI flags, managed-layer precedence, live session revocations, or
hook-runtime decisions. Any "host authorization PASS" built on settings parsing or on project
containment would be **fabricated**. What Qompack *does* enforce — current filesystem project
containment (`Norm` + `ResolvesInside`) plus recorded provenance — is a **narrower, different**
guarantee, and the code comments already say exactly this (`authorize.go:37-38`, `norm.go:61-71`).
That honesty is correct and should be preserved.

**Product decisions that remain for main (I do not decide these).** The affected surface is
path-addressed *historical* retrieval (`re_read`, and `expand`/`recall` of file-content origins):
content legitimately captured in-project can be re-served from history even if the host would *now*
deny a native Read of that path (live/managed/flag/hook policy). Concrete options, granular before
reflexive:

- **Option A — declare the scoped capability (document the divergence, keep the surface).** Formally
  state, in a user-visible place, that Qompack retrieval enforces project containment + recorded
  provenance and **does not** reproduce the host's live per-path Read policy; keep redaction as the
  content control. This is a *capability declaration*, **not** a waiver — do not phrase it as "MCP
  allow overrides native deny."
- **Option B — human-in-the-loop.** Mark the path-addressed / opaque-hash retrieval tools
  `requiresUserInteraction` so the host prompts before serving. Cost: interaction on every such
  retrieval; still not a policy evaluation, and it cannot silently enforce a managed deny.
- **Option C — restrict scope.** Disable path-addressed historical retrieval where host deny-policy
  fidelity is mandatory (e.g., under managed settings), retaining only genuinely-pathless/redacted
  retrieval. Granular, avoids disabling the whole product.
- **Explicitly rejected — Option D.** Do **not** attempt to reproduce host policy by parsing settings
  files, and do **not** override a native deny with an MCP allow or a docs-only waiver. The docs above
  show why any such reconstruction is incomplete by construction.

Per the implementation contract (§"Current host permission decisions are a separate boundary"), the
chosen option and its **target evidence** must be recorded *before* the V6-HOST-1 gate may be claimed.
This review recommends main select among A/B/C on product risk tolerance and register the decision;
until then V6-HOST-1 stays an open, honestly-stated unsupported boundary, not a PASS.

---

## 7. Minimum compatible correction (recommended exact contracts)

1. **Keep** the frozen `Store`/`Root`/`ToolUseRecord` wire and interface shapes and the additive
   `ProvenanceReader` (already done). Do not extend `ContentOrigins` to trust the sidecar for
   authority; keep it fail-closed.
2. **Add a capture-time path-scope refusal** at `admitDelivery` (`handlers.go:1221`): a
   file-producing tool (`Read`/`Edit`/`Write`/`NotebookRead`/`NotebookEdit`/`Grep`/`Glob` with a
   structured path target) whose target fails `Norm` **or** `ResolvesInside` → `OutcomeDenied` with
   an evidence-only sidecar (`Bytes` empty). Nothing servable is persisted; the gap is auditable.
   Reuse the existing denied/evidence verdict — **no new schema**.
3. **Stop the silent laundering** at `observer/tooluse.go:87-92`: distinguish "no path input" from
   "path input present but out of scope"; do not `PutBytes` an out-of-scope file capture. Defense in
   depth for the object store; not a substitute for #2.
4. **Decide and state the spool guarantee** (§5): if out-of-project bytes must never touch the spool,
   add the same scope decision to the client-side capture policy before `ipc.Client.Send`.
5. **Keep `authorizeOrigin` default-deny for unknown** and treat the pathless allowlist as a
   last-resort for producers pathless by nature; document that pathless content's only content
   control is redaction (B2), and require a retrieval-side `Redactor` in any build that serves
   content.
6. **State the metadata boundary** (B3): `timeline`/`why`/`dropped` return pointers and
   checkpoint-authored summaries outside the origin/redaction gate; make the "non-sensitive" premise
   explicit or gate them.
7. **Add a metric/comment** distinguishing the `ContentOrigins` bounded-incomplete `unavailable`
   (§3.4) from a policy denial, so the availability cliff is observable.
8. **V6-HOST-1:** register the A/B/C product decision and its target evidence before any host-policy
   PASS.

---

## 8. Recommended additional tests (design-only; none written here)

- Capture-time refusal: an out-of-project `Read`/`Edit` is `OutcomeDenied` at admission — **no**
  object `PutBytes`, **no** `ToolUseRecord`, and either no sidecar or an evidence-only sidecar with
  empty `Bytes`. (Directly targets B1; nothing today asserts the at-rest side.)
- Observer non-laundering: a `Read` with an escaping path does not reach `PutBytes` and does not
  produce a `Path:"", Tool:"Read"` record.
- `ContentOrigins` bounded-incomplete → `authorizeHash` returns `unavailable` (fail-closed) rather
  than serving a partial origin set. (Contract says so; no test pins the cliff.)
- Metadata boundary: `why`/`timeline` explicitly documented and, if gated, tested to withhold
  path-scoped detail.
- The existing `authorize_v6_test.go` trio (lost-path, dedup-no-launder, store-without-provenance) is
  good coverage of the *retrieval* side and should stay.

---

## 9. Non-acceptance

This is a source review. No V6-AUTH or V6-HOST gate is cleared by it. The intentionally-red
regressions remain the record of the live defects. V6-AUTH-1's at-rest capture leak (B1) and the
V6-HOST-1 product decision are **not** resolved by the retrieval correction alone and must not be
marked PASS until the capture-time refusal (§7 #2–#4) lands with passing tests and main registers the
host-policy decision (§6). The retrieval-authority change is a sound, contract-respecting step; it is
the read-time half of a fix whose durable half is at capture.

### Bypass register

- **B1 — at-rest out-of-project capture.** Bytes of an out-of-scope file persist in the object store
  and the GC-protected capture sidecar despite retrieval refusal. Root cause at capture
  (`tooluse.go:87-92`, `ingest.go:669-677`). **Owner: capture/admission + observer.**
- **B2 — pathless laundering.** Out-of-scope content captured under a permitted pathless tool name
  (Bash/PowerShell) is servable; redaction is the only remaining control. Structural to tool-name
  authority. **Owner: capture-time scope decision + redaction posture.**
- **B3 — metadata disclosure.** `why`/`timeline`/`dropped` return checkpoint-authored text and
  evidence-object sizes outside the origin/redaction gate. Lower severity; needs an explicit boundary
  statement. **Owner: MCP.**
- **B4 — host policy not reproduced.** In-project content can be re-served even where the host would
  now deny a native Read (live/managed/flag/hook). Unresolvable in-protocol; product decision
  required (§6). **Owner: main (A/B/C).**
