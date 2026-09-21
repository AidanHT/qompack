# V6 trust / recovery review — retrieval-authority boundary and F4-4 surfacing

Independent non-authoring reviewer (Opus 4.8, high). Route-probe canonicalModel `claude-opus-4-8`
(confirmed in `request-usage.json` for the reviewer session). Base under review: `301a8e9` (SP17
then SP18 integrated); production `internal`/`cmd`/`plugin` unchanged through `7eb390a`. Scope: the
four suspected items in the brief only — no broad audit, no suite run. This document is **findings
and recommendations**, not acceptance and not policy authority. Every shared trust-contract question
below returns to main.

> **Correction pass (second session).** Five factual errors in the first draft are corrected inline
> and flagged `[CORR]`: (1) the evidence records are **fresh, untracked** artifacts of this
> verification, not "already committed"; (2) my F-A hash-form concern is **no longer source-only — it
> was executed** against the packaged binary (real hook capture → NTFS junction escape), so the
> "unmeasured asymmetry" language is withdrawn; (3) `docs/security.md §1` was **corrected by
> `7eb390a`** and no longer overclaims, and main **rejected a documentation-only resolution** of the
> mandatory gates; (4) the F-D "dangling audit" I cited is the **test-harness** audit, not an
> existing production daemon service, so I withdraw the "wiring-only fix" recommendation as
> unsupported by source; (5) manual `fsck` detection is **not** automatic recovery and does not close
> the recovery gate. The findings themselves stand; only their evidentiary status and remedies move.

## 0. Evidence provenance (read this before weighting anything)

I ran no build, test, or binary. The records below were **produced by main during this
verification and are currently untracked** (`?? plans/sdd/V6-VERIFY/`); the regression *tests* are
committed (`ccfd4dd`) and the docs at (`7eb390a`), but the evidence JSON/logs are not. `[CORR]` — the
first draft wrongly called these "already committed."

Two evidence classes are kept apart:

- **Executed evidence** — packaged-binary records. Original scenarios ran on bundle
  `v0.2.0-640-g301a8e9` (`f171f462…`); the real-capture and corrected-fsck scenarios ran on
  `v0.2.0-640-g301a8e9-dirty` (`6811df18…`, test/doc overlay dirty, production source identical):
  - `security/posture_out_of_project_capture_is_archived.json` — `failed`
  - `security/archive_trust_parent_directory_replaced_by_link_outside.json` — `verified` (NTFS junction)
  - `security/archive_trust_captured_file_replaced_by_link_outside.json` — `skipped` (no `SeCreateSymbolicLinkPrivilege`)
  - `security/archive_trust_lexical_parent_escape_in_a_stored_record.json` — `verified`
  - `security/privacy_surface_mcp_retrieval.json` — `verified` (no credential fragment survives)
  - **`security-real-capture/v6_escape_{tool_use_id,root_hash,chunk_hash}.json`** — id `verified`
    (denied control); **both hash forms `failed`, `content(34 bytes)`, marker exposed=true**
    (`runs/security-real-capture.json`, exit 1) — this is the executed proof of F-A
  - **`security-regressions-original.json`** — `failed` (exit 1): the two intentionally-red V6
    authorization regressions, the executed proof of F-B
  - `fault-focused/capture_sidecar_stage_one_only.json` — `failed`; corrected packaged-fsck run
    `runs/fsck-packaged-tool-capture-fixed.json` — `passed` (fsck exits 1, names the defect, preserves
    bytes; recorded `explicit-incomplete`, **not** recovered)
- **Source inference** — traced by reading the files below at `301a8e9`. Marked `[SRC]` at each use.

Files read: `internal/observer/tooluse.go`, `internal/observer/signals.go` (`PathsFromInput`),
`internal/paths/norm.go`, `internal/mcp/authorize.go`, `internal/mcp/handlers_span.go`,
`internal/mcp/handlers.go` (`recall`), `internal/cli/fsck.go` (`checkCaptures`),
`test/security/denied_test.go`, `test/security/v6_authorization_test.go`,
`test/fault/v6_fsck_test.go`, `docs/security.md`, `plans/.../final-release-check.md`.

## 1. The three pieces the brief names, traced end to end

1. **Out-of-project Read → content stored, path emptied.** `[SRC]` `onToolUse`
   (`tooluse.go:87-107`) stores the body unconditionally when non-empty; `pathKey` is set only when
   `paths.Norm` succeeds (`tooluse.go:88-92`). `paths.Norm` returns an error for a path that escapes
   the root (`norm.go:55-57`), so an out-of-project Read yields `pathKey == ""` **with content
   archived**. This is exactly what `posture_out_of_project_capture_is_archived.json` records as
   executed fact ("its CONTENT archived … while its path was dropped").

2. **`authorizePath("")` returns `true`.** `[SRC]` `authorize.go:61-64`: the empty-path fast-path is
   authorized by identity alone. This is correct and necessary for genuinely pathless captures —
   Bash output, elimination evidence — and must not be removed (see §3, the compatibility floor).
   The problem is that item 1 makes an *out-of-project* capture indistinguishable from a genuinely
   pathless one at retrieval: both present `Path == ""`.

3. **The three retrieval forms do not authorize alike.** `[SRC]`
   - `expand` by `tool_use_id` → `authorizePath(rec.Path)` before the root is even looked up
     (`handlers_span.go:216`). **Authorized.**
   - `re_read` by path → `authorizePath(norm)` before any store access (`handlers_span.go:287`).
     **Authorized.**
   - `recall` → `authorizePath(hit.Path)` per hit; a failing hit is omitted and counted in `denied`
     (`handlers.go:155-158`). **Authorized**, and it does not leak the hash of a denied hit.
   - `expand` by **root hash** (`handlers_span.go:236-239`) and by **chunk hash**
     (`handlers_span.go:245-260`): `GetRoot` / `GetChunk` then return, **with no `authorizePath`
     call on either branch**. The synthesized root carries `path == ""`. **Not authorized at all.**

## 2. Findings

### F-A (EXECUTED) — the content-hash address form is not authorized; hash routes bypass the path check

`[CORR]` The first draft called this "source-demonstrated" and said the asymmetry was "unmeasured."
It has since been **executed** against the packaged binary and is a confirmed release blocker
(main's V6-AUTH-2).

`[SRC]` The root-hash and chunk-hash branches of `resolveExpandTarget` (`handlers_span.go:236-260`)
call no authorizer; only the `tool_use_id` branch (`:216`) and `re_read` (`:287`) do.

`[EXECUTED]` `TestV6_HashAddressesDoNotBypassPathAuthorization` (`test/security/v6_authorization_test.go:52`)
captures a real in-project file through the packaged hook, confirms the authorized control
(`expand{tool_use_id}` returns the marker **before** the swap, `:86`), then renames `src/` and
replaces it with an NTFS junction pointing outside the project. After the swap:
- `v6_escape_tool_use_id.json` → `denied(found=false)`, marker not exposed → **verified** (the ID
  form correctly enforces the current path).
- `v6_escape_root_hash.json` and `v6_escape_chunk_hash.json` → `content(34 bytes)`, **marker
  exposed=true** → **failed**.

So the brief's second question — *refused via ID yet served via hash?* — is answered **yes, by
execution.** The run `runs/security-real-capture.json` exits 1 on `301a8e9(-dirty)` production
source.

**Reachability, stated honestly.** The store must hold the object and the caller must supply its
root/chunk hash. A root hash is content-derived, so possessing it implies the content was seen while
authorized — and note the authorized control proves `expand` hands the model that `Hash` in its body
(`handlers_span.go:188`) before the swap. The hash form therefore **cannot materialize bytes never
captured**; it re-serves already-captured, redaction-checked bytes for an address whose path policy
would refuse today. This is a **trust-boundary bypass of the current path check**, not a disclosure
of never-seen content — but per main's ruling that distinction does **not** downgrade it below a
mandatory blocker, and I concur: "an address is not a credential" is the stated required boundary and
the hash routes break it.

`[CORR]` The doc no longer contradicts the code the way the first draft said: `7eb390a` rewrote
`docs/security.md:16-29` to state plainly that "the current implementation does not fully meet that
requirement," to name both hash routes and the empty-path route, and to call them "release blockers,
not accepted exceptions." So the doc is now **honest about the gap**, which removes the doc-vs-code
inconsistency but is explicitly **not** a remediation — main rejected any documentation-only
resolution of a mandatory authorization gate. The owning fix stays with `internal/mcp` +
`internal/observer` (provenance for every root/chunk address, same authority on every retrieval
form, legitimate pathless behavior retained).

### F-B (EXECUTED) — out-of-project captures pass authorization vacuously on every form

`[SRC]` Because item 1 leaves `Path == ""` and item 2 authorizes `""`, an out-of-project capture is
served by `expand{tool_use_id}`, by `expand{hash}`, and surfaced by `recall` (empty-path hit passes
`authorizePath`, so its `Hash`/`ToolUseID`/redacted summary are exposed — `handlers.go:162-167`).
`[CORR][EXECUTED]` Beyond source inference, `TestV6_ArchivedReadRetainsItsAuthorizationBoundary`
(`v6_authorization_test.go:21`) performs a real out-of-project hook capture and then `expand{id}`;
the run `security-regressions-original.json` **fails (exit 1)** because the server serves the
synthetic marker instead of denying. So the brief's first question — *could an outside Read with
dropped path be served by ID?* — is answered **yes, by execution** (main's V6-AUTH-1).

The coordinator ruling on `posture_out_of_project_capture_is_archived.json` says the boundary for
this class is *"retrieval-time authorization plus redaction at capture."* For this exact class,
**retrieval-time authorization does nothing** (empty path → authorized). So the operative guard is
**redaction alone**. That guard is real and measured: `privacy_surface_mcp_retrieval.json` is
`verified` — no credential literal or >8-byte fragment survives under mcp-retrieval. The finding is
that the doc/ruling wording ("authorization plus redaction") overstates what authorization
contributes here; for out-of-project captures it is redaction, full stop.

### F-C (CLOSED by main) — the original escape matrix never drove `expand` by hash; new tests now do

`[SRC]` `denied_test.go:239-245` `sharedEscapeCalls` drives only `recall{query}`,
`expand{tool_use_id}`, `re_read{path}` — the **one address form with no path authorization,
`expand{hash}` (root and chunk)**, was absent from all three shapes, which is why F-A/F-B were never
measured by the SP17-era matrix. `[CORR]` The first draft proposed adding those calls as the safe
next step; **main has since added them.** `test/security/v6_authorization_test.go:94-106` drives
`tool_use_id`, `root_hash` and `chunk_hash` against the same post-swap store, and the resulting
records are the executed evidence now cited under F-A. The coverage gap is therefore **closed at the
test level**; the underlying authorization defect it exposed remains open and blocking.

### F-D (executed) — F4-4: the stage-1-only capture is surfaced by manual `fsck` but not by any automatic surface

`fault-focused/capture_sidecar_stage_one_only.json` at `301a8e9` is `failed`: a capture with durable
bytes, `outcome ok`, and **no reference joined** (root `000000…`), yet *"nothing new on LOUD.log,
status --json, self-test --json, the day log or a DropEntry."*

`[SRC]` `fsck.go:1077-1120` (`checkCaptures`) **does** report precisely this state as a `defect`, and
`TestV6_FsckReportsUnpublishedCaptureWithoutDiscardingEvidence` (`test/fault/v6_fsck_test.go:20`,
run `fsck-packaged-tool-capture-fixed.json` = `passed`) confirms the packaged `fsck --json` exits 1,
names the `captures` check "stage 1 only", and preserves objects and sidecars. So `qompack fsck`
names it — but `fsck` is an **operator-invoked scan**, and the automatic surfaces (`status --json`,
`self-test --json`) still gain nothing. Its own record is deliberately `explicit-incomplete`, **not
recovered**.

`[CORR]` The first draft called the remedy a "wiring-only fix" of "the recovery daemon's own audit."
That was unsupported: the `dangling`/`reported` count in the fault record is computed by the
**test harness's** audit helper, **not** by a demonstrated production daemon service. I have no
source proof that any always-on production path already computes this and merely fails to print it,
so I withdraw the "minimal wiring" characterization. Whether surfacing it automatically is a small
change or requires a new daemon scan is **open and must be scoped from source by the owner
(`internal/store` + `internal/daemon`)**, not asserted here.

Fidelity note: the bytes are **not lost** — durable and evidence-class (GC cannot reach them,
`fsck.go:1789-1826`). The defect is **discoverability of an orphaned capture** through a routine
surface. `[CORR]` Manual `fsck` detection does **not** close automatic recovery, and main explicitly
rejected treating it (or a "copy the store then run fsck" flow) as operator-recovery proof.

## 3. Minimal safe correction vs. deeper authority-contract work

Per the brief I do not propose schema changes as if they were free, do not guess host permissions,
and do not deny valid pathless results. The compatibility floor is explicit: **the empty-path
fast-path must keep authorizing genuine pathless captures** (Bash, test output, elimination
evidence). Any fix that denies all `Path == ""` retrievals breaks the noisiest legitimate class and
is rejected.

**F-A (hash form).** `[CORR]` No documentation-only path exists — main has ruled that out for a
mandatory authorization gate, and the doc has already been made honest (`7eb390a`) without that
counting as remediation. The real correction is a code change owned by `internal/mcp` +
`internal/observer`:
- *Candidate, compatible, likely no schema:* on the root/chunk-hash branch, do a reverse lookup — if
  the store holds any `tool_use`/file-version record whose `Root` equals this hash, require that at
  least one associated path still `authorizePath`s (deny only when a known referencing path exists and
  all of them now resolve outside). Objects with **no** referencing path (true pathless content) stay
  served, preserving compatibility. Feasibility depends on whether a root→referrers lookup exists;
  I did not confirm one, so this must be scoped from source, not asserted. Legacy/unknown-record
  behavior must be defined before implementation (main's V6-AUTH-2 owner note).

**F-B (out-of-project empty path).** There is **no clean retrieval-time fix that preserves
compatibility**, because a dropped-out-of-project path and a genuine pathless capture are identical
(`Path == ""`) once captured. Distinguishing them requires a **capture-time marker** on the record
("path dropped as out-of-project" vs "genuinely pathless") so retrieval can refuse the former while
serving the latter — that is a schema/field addition and therefore deeper contract work, not a
minimal patch. `[CORR]` Redaction being the only guard for this class is **not** an acceptable
resolution on its own — main rejected redaction-passing as authorization for an archived read
(`docs/security.md:25-26`). So the F-B owner work is the capture-time provenance distinction (mark a
dropped-out-of-project capture distinctly from a genuinely pathless one), with compatible
legacy/unknown behavior defined first; there is no proportionate doc-only or redaction-only close.

**F-D (recovery surfacing).** `[CORR]` I do **not** have source proof that a production daemon
already computes the stage-1 dangling condition, so I cannot claim a "minimal wire-up." What the
evidence supports is only: manual `fsck` detects and preserves it; the automatic surfaces do not
account for it; manual detection is not automatic recovery. The owner (`internal/store` +
`internal/daemon`) must scope, from source, whether an always-on path can surface this or whether a
new scan/acknowledgement is required — production gap discovery, durable acknowledgement and
owner-qualified status, per main's V6-RECOVERY-1.

## 4. Residual host-policy limitation (must be stated; do not close here)

Qompack's authorization gate is a **filesystem-scope** predicate: "does this path, resolved on disk
today, land inside the project root" (`authorize.go` → `paths.ResolvesInside`, `norm.go:83-104`).
That is a **proxy** for host permission, **not the host's actual allow/deny rules**, and the two do
not coincide:

- The host (Claude Code) may **deny** an *in-project* path today — a `.env`, a path matched by a
  deny rule, or a read the user declined — that Qompack's project-root check authorizes without
  hesitation (it is inside the root). Qompack has no visibility into the host's live permission state
  (and the brief correctly forbids guessing it), so **archived content for an in-project path the
  host would now deny can still be served.** The doc headline "host permission outranks the archive"
  is only *approximated* by the root boundary; it is not enforced against host deny lists.
- Symmetrically, the host may **permit** an out-of-project read; Qompack drops that path and serves
  it on identity (F-B) — consistent with host allowance, but outside the documented "refuses when
  outside the root."

This limitation is not closeable inside this repository's retrieval layer without the host feeding
its permission decisions into capture or retrieval. It bounds every claim above: the findings are
about the **project-root boundary and the documented contract**, not about reconstructing host
policy, which Qompack cannot see.

## 5. Bearing on the V6 "cannot be waived" clause (returned to main, not decided)

The V6 user plan holds that mandatory privacy/fidelity/recovery failures cannot be waived; SP17
accepted reduced scope and left `SP17-M7-03` and `SP17-M7-04` **unverified** against `failed`
records ruled as documented behaviour (`final-release-check.md:73-74`). The reviewer's inputs to
that decision:

- **SP17-M7-03 ("no archived retrieval bypasses host permission denial").** `[CORR]` The bypass is
  now **executed**, not a source-only or documented-contract concern: `expand{hash}` on both address
  forms serves an escaped in-project capture (F-A), and `expand{id}` serves an out-of-project capture
  (F-B). Redaction being verified (`privacy_surface_mcp_retrieval`) does **not** clear this — main
  ruled redaction is not authorization. The demonstrations re-serve already-captured, host-permitted
  bytes rather than never-seen data, but on the record that does not downgrade the row below a
  **mandatory blocker**. This row is **not met**, and it is not waivable.
- **SP17-M7-04 (recovery-artifact surfacing).** The orphaned capture is **retained, not lost**, and
  **is** surfaced by manual `fsck` (verified) — but manual detection is not automatic recovery, and
  main rejected "copy the store + run fsck" as an operator-recovery proof. The automatic surfaces
  still do not account for the incomplete capture (F-D). This row is **not met**; whether the fix is
  small or a new daemon scan is unresolved and owner-scoped.

Nothing here is an acceptance or a waiver, and I make no sign-off. Both mandatory rows (M7-03, M7-04)
are **failed on executed evidence** and return to main as blockers, alongside the residual
host-policy limitation of §4 (filesystem scope ≠ host deny rules).
