# V6 final independent review — report & artifact integrity

Independent non-authoring reviewer (Opus 4.8 high; reviewer session canonicalModel
`claude-opus-4-8`, confirmed in `request-usage.json`). Read-only inspection only: no build, test,
git mutation, or edit outside my two assigned reports (`trust-review.md`, this file). Main owns
final acceptance. I do **not** sign off and I do **not** waive any mandatory failure.

This document reviews the integrity of `plans/V6-report.md` and its linked artifacts, and states
whether the recorded **NO-GO decision** and the **scoped evidence** are supportable. It complements
`trust-review.md` (the deep authority/recovery analysis), which I corrected in this same session.

## 1. Verdict

- **The NO-GO / BLOCKED decision is supportable and I concur.** Two retrieval-authorization
  bypasses and the publication/recovery gaps are reproduced on the packaged binary against unchanged
  production source. Under the V6 "mandatory privacy/fidelity/recovery failures cannot be waived"
  rule, these block completion, merge to `develop`, tag, push, and any rollout — exactly what the
  report declares.
- **The recorded evidence is supportable *as scoped evidence*, not as a release sign-off.** Every
  claim I spot-checked is either backed by a committed artifact or explicitly labelled incomplete.
  The report is unusually disciplined about separating "command exited 0" from "capability verified,"
  and about naming what was *not* run.
- **No unsupported acceptance or compatibility claim was found.** The report asserts no passing
  release gate, no compatible-reader proof, no operator-recovery proof, and no UAT — each gap is
  stated rather than glossed.

## 2. Claims verified against artifacts

| Report claim | Check I ran | Result |
|---|---|---|
| Production source unchanged; "tested production source remains `301a8e9`" (§1) | `git diff --name-status 301a8e9 7eb390a` | **Confirmed.** Only `README.md`, `docs/*` (7 files) and two new `test/**` files change. `git diff --stat … -- internal cmd plugin go.mod go.sum` is empty. |
| Commit attributions: tests `ccfd4dd`, docs `7eb390a` (§1) | `git show --stat ccfd4dd` | **Confirmed.** `ccfd4dd` = only the two test files (233 insertions); `7eb390a` = only docs. |
| 304 distinct IDs, explicit result for every ID (§2) | `execution.tsv`: 305 lines (1 header + 304 `1.x.y` rows); 0 blank `result` cells | **Confirmed.** |
| V6-AUTH-2 executed: ID denied, both hash forms serve archived bytes (§5) | `security-real-capture/*.json` + `runs/security-real-capture.json` | **Confirmed.** `v6_escape_tool_use_id` = `denied(found=false)` (control); `v6_escape_root_hash` and `v6_escape_chunk_hash` = `content`, marker exposed=true → failed; run exit 1. |
| V6-AUTH-1 executed: out-of-project read served by ID (§5) | `runs/security-regressions-original.json` | **Confirmed** failed (exit 1). |
| V6-RECOVERY-1: manual fsck reports stage-one, automatic surfaces do not (§5) | `fault-focused/capture_sidecar_stage_one_only.json` (failed) + `runs/fsck-packaged-tool-capture-fixed.json` (passed) + `test/fault/v6_fsck_test.go` | **Confirmed.** fsck exits 1, names it, preserves bytes; record is `explicit-incomplete`, not recovered. |
| fsck fixture-error saga honestly recorded (§4) | three `fsck-packaged*` run records + `v6_fsck_test.go:75-92` | **Confirmed.** First run picked an unpublished prompt sidecar (fixture error, not a defect); second failed to compile on `ObservationID.String`; final selects a published `observe.tool` sidecar by operation and passes. |
| Preservation: original root and all historical reports/plan docs untouched (§1) | `preservation.json` | **Confirmed** for the 19 listed files (before==after; `original_root_head == expected 7f92af5…`). See §3 nit. |
| Routing: all children measured `claude-opus-4-8`; coordinator not proven Fable (§1, §8) | `request-usage.json` | **Confirmed.** Seven child records all `canonicalModel: claude-opus-4-8`, `provider: firstParty`. Coordinator identity is disclosed as unavailable/Codex; no alias accepted as proof. |
| docs corrected to state the bypass honestly (§5) | `docs/security.md:16-29` | **Confirmed.** §1 now says "the current implementation does not fully meet that requirement," names both hash routes and the empty-path route, and labels them "release blockers, not accepted exceptions." |

## 3. Integrity findings (prioritized; none reverse the NO-GO)

1. **`preservation.json` leaves the production-source field blank — the strong claim is true but the
   artifact does not itself carry the proof.** The file lists 19 docs/plan/report files (all
   unchanged) but `"unchanged_production_source": ""` and `"historical_reports_and_carries_diff": ""`
   are empty strings. The report's central claim "no files in `internal`, `cmd`, `plugin`, `go.mod`
   or `go.sum` changed" (§1) is **verifiable by git and I confirmed it holds**, but a reader relying
   on `preservation.json` alone cannot see that. *Recommend* main populate those two fields (e.g. the
   empty `git diff --stat 301a8e9 7eb390a -- internal cmd plugin go.mod go.sum`) so the artifact is
   self-supporting. Low severity — claim correct, evidence-packaging gap only.

2. **"33 archived bytes" vs. the recorded `content(34 bytes)`.** §4 and §5 say the hash forms
   "return 33 archived bytes"; the artifacts (`v6_escape_root_hash.json`,
   `v6_escape_chunk_hash.json`) record `content(34 bytes)`. The marker
   `V6-CAPTURED-PATH-CONTENT-541dc897` is 33 characters; the captured body is that marker **plus a
   newline** = 34 bytes. The report counted the marker string, the envelope counted the served body.
   Trivial; *recommend* aligning the prose to the artifact's 34 to avoid a reader thinking they
   disagree.

3. **Some terminal outputs were not retained as run artifacts (self-disclosed).** §1: the focused
   devtool merge checks "passed on both sides of the final merge; those terminal outputs were not
   saved as standalone run artifacts," and no start time was recorded for the initial combined check.
   This is honestly flagged and does not touch a mandatory gate, but it is a real
   evidence-retention gap in the integration narrative. No action required beyond the existing
   disclosure.

4. **Bundle `-dirty` on the strongest regressions is correctly explained but worth a second look.**
   V6-AUTH-2 / fsck-fixed records ran on `v0.2.0-640-g301a8e9-dirty` (`6811df18…`). The dirty state
   is the test/doc overlay only; I confirmed (finding §2 row 1) that production source is byte-identical
   to `301a8e9`. The report says this; the claim is sound. No change needed — noting it so the
   `-dirty` tag is not later mistaken for a production-code delta.

## 4. Mandatory-failure handling — correct, no waiver

The report keeps all four blockers enabled and red, and explicitly refuses to treat any of the
following as a resolution: passing diagnostics/docs checks, redaction coverage, documentation
honesty, manual `fsck`, or a "copy the store + fsck" flow (§5). This is the correct posture and it
matches the corrections applied to `trust-review.md`:

- **V6-AUTH-1 / V6-AUTH-2** (empty-path and hash-route authorization bypasses) — executed blockers;
  owners `internal/mcp` + `internal/observer` (with legacy/unknown-record behavior defined first).
- **V6-RECOVERY-1** (no automatic accounting for a stage-one publication gap) — manual fsck ≠
  automatic recovery; owner-scoped from source (no production auto-surface proven).
- **V6-RECOVERY-2** (operator backup/restore handoff incomplete) — `LegacyImportGate` closed, no
  supported operator CLI, fixture-gated engine rollback is partial only; the report does not claim
  released-reader compatibility.
- **V6-HOST-1** (filesystem scope ≠ host deny rules) — preserved as an unverified mandatory trust
  boundary; the report does not equate project containment with host authorization.

The report does not overstate the *nature* of the leak: it states plainly that the demonstrations
re-serve previously captured synthetic bytes and do not read uncaptured data. I concur, and per
main's ruling that framing does not downgrade the rows below mandatory blockers.

## 5. Residual limitations of this review

- I inspected artifacts and re-ran only two read-only git queries and one `execution.tsv` tally. I
  did **not** re-execute any Go test, re-derive any bundle hash, or reproduce any packaged run; the
  `passed`/`failed` verdicts above are read from the committed run records and their embedded exit
  codes, not independently reproduced.
- Structural completeness of `inventory.md`/`execution.tsv` was checked for ID count and non-empty
  results only, not for the correctness of every per-ID assertion mapping.
- The host-policy limitation (§4, V6-HOST-1) is not closeable inside this repository's retrieval
  layer; it bounds every authorization claim and must be carried forward, not resolved here.

## 6. Bottom line

The NO-GO decision is supportable and the scoped evidence is trustworthy for what it claims to be —
a focused verification that **found blockers**, not a release qualification. The integrity findings
in §3 are packaging/wording nits that do not affect the decision. V6 remains **BLOCKED**; no
completion, merge, tag, push, or rollout is warranted, and the mandatory failures are not waived.
Independent sign-off remains withheld pending the owner remediations and the deferred gates the
report enumerates.
