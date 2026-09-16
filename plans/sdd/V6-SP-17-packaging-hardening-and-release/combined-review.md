# SP-17 independent release/integrity review

Read-only review of the combined artifact on `feat/sp17-packaging-hardening-and-release` at
`87966da` (base `9c84e31`). The reviewer did not author the implementation. Full text of the
close-out seat follows.

**Close-out verdict:** CLEAN.

**Ready to tick Done and hand to SP-18:** Yes, with the caveats in the scope sign-off. No
code or evidence change is required.

## Scope sign-off

The reviewer signs the same evidence-backed supported scope as `docs/release.md` §3 and
`final-release-check.md` §4, subject to:

1. Only `windows/amd64` is `installed-verified`; the other five targets compile and ship as
   `unknown` at the deployment level.
2. M7-03 and M7-04 are `unverified` on ruled, documented `failed` records (S-6 retrieval-time
   boundary; F4-4 `fsck` surface).
3. Version-agreement (SKIPPED, no tag) and package-name availability are release-time gates
   not yet run; `core.Version` is `0.1.0`.
4. The release benchmark is complete-or-unknown accounting; the live-task layer is `excluded`
   (R7-2).
5. Rollback is a Go-only API with a documented manual procedure; no operator command ships.

## Issues

### Critical

None.

### Important (documented; no code change)

- M7-08 reads `verified` while version agreement was SKIPPED and no registry name check
  exists. Defensible only as documented reduced scope (`docs/release.md`,
  `final-release-check.md` §5). Do not infer that version or name were validated.
- Rollback ships as a Go-only API. SP-18 must carry that as an open capability
  (`sp18-handoff.md`).

### Minor (accepted residuals)

Routing deviation from the 2026-09-14 Opus-4.8-only child directive is recorded in the
coordinator ledger. Version tension between git-describe evidence bundles and `0.1.0` install
records is expected for dirty test assemblies. F4-9, unknown-key capture refusal,
`AppendFileVersion` sort, R5-3, and R5-4 remain owner residuals.

## Checkbox rulings

Every Exit-criteria and Done-checklist bullet is **TICK**, with the caveats above on
delegation recording, reduced M7 scope, name/version-at-release, and the SP-18 rollback
wording.

Strengths that the verdict rests on: `release-scope` raises status only from committed
records (`familyRow` / `accountingRow` / `attachmentsRow`); the two `failed` records stay
`unverified`; S-6 is documented as retrieval-time denial; `LoadForCapture` applies the
versioned-section reset; `paths.RestoreLog` keeps restored logs appendable; `docs/release.md`
§7 disclaims native control, exact-history, and universal performance; failed gate attempts
were moved out of the evidence root.
