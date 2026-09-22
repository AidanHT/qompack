# Documentation closeout resolution

Date: 2026-09-22. Branch `verify/v6`, source HEAD `36205d1`
(`fix(daemon): verify every archived journal during offline checks`).

Scope: the six operator-facing docs pages an earlier authorized pass left dirty
(`docs/backup.md`, `docs/release.md`, `docs/security.md`, `docs/troubleshooting.md`,
`docs/uat.md`, `docs/user-guide.md`). This closeout preserved and finished that work, corrected the
one concrete false claim called out for correction, and repaired one truncated sentence. No
product, test, config, or generated-doc file was touched. Shared trust/contract decisions and the
release/tag/merge/publish/UAT gates remain out of scope and unchanged.

## What was already correct in the dirty tree (verified against source, left as-is)

Each of these was re-read against current source and needed no edit:

- **Exit codes** (`docs/troubleshooting.md` §1, `docs/user-guide.md` operator table). `doctor` always
  sets `Exit: ExitOK` and `runDoctor` returns nil — it reports diagnostic/capability states in its
  output and exits 0 (`internal/cli/doctor.go:200`, run path). `fsck` returns exit 1 for a defect or
  an incomplete (I/O-prevented) check (`internal/cli/fsck.go:186`, `report.Exit = ExitError`).
  `self-test` returns exit 1 on a critical failed check (`internal/cli/selftest.go`, `runSelfTest`).
  The user-guide's old "only command that may exit non-zero" line was already corrected in the dirty
  tree; the stale `commands.go:31` source comment saying the same is main's to fix, not in doc scope.
- **Delivery-journal admission bound** (`docs/backup.md`). "65,536-entry and 64 MiB admission bound"
  matches `deliveryLeaseMaxBytes = 64 << 20` and `deliveryLeaseMaxEntries = 65536`
  (`internal/daemon/delivery_lease.go:30-31`).
- **Rollover default-off / unverified** (`docs/backup.md`). "Automatic journal rollover is not yet
  enabled or release-verified" matches `var enableDeliveryGenerations = false`
  (`internal/daemon/delivery_generation.go:141`), whose comment states the 65536/64MiB budget
  reclamation "is deliberately NOT enabled here" (`delivery_generation.go:130-135`). This is a
  build-time var, not a config key, so the page correctly does not present a settable key. Its
  compatibility/cost dimensions remain unverified.
- **Old-release rollback not certified / no automatic downgrade.** Stated in `docs/release.md` §5,
  `docs/backup.md`, and every `docs/uat.md` rollback block; `RehearseRollback`'s drill record field
  `AutomaticDowngrade` is always false.
- **Native host permission parity + human UAT unverified.** `docs/security.md` §1 and §8 ("Current
  native Read authorization is a separate host boundary"), and `docs/uat.md` — every Result block
  reads `not executed — capability unverified`, UAT-01/UAT-12 explicitly leave installed-host
  permission and upgrade/uninstall recovery to a human run. No pass is inferred.

## Edits applied

### 1. `docs/release.md` §7 — corrected the false "no workflow has ever run" claim

The bullet asserted **"No workflow has ever run."** That is false: a nightly workflow has run.
Evidence `plans/sdd/V6-remediation/github-nightly-35704111100-jobs.json` records `total_count: 29`
jobs with conclusions 26 `success` / 3 `failure` (`race-windows`, `bench-deep (windows-latest)`,
`bench-deep (ubuntu-latest)`).

The bullet's supporting sentences are specifically about the **release** workflow (the goreleaser
draft split and the host-validation upload, both tag-triggered), which genuinely has never run — no
tag has been pushed. Only the headline was overbroad. The fix narrows the headline to the release
workflow and adds the historical-CI note, evidence-qualified: that nightly run was on a historical
source (`9c84e31d`), not this candidate tree, so it certifies nothing about the current source.

- Header changed from "No workflow has ever run." to "The release workflow has never run."
- Appended: the nightly evidence file, the 29-job / 26-3 breakdown, and that the run was on
  `9c84e31d` and does not certify the current tree.

The added text uses only backticked (inline-code) paths, not markdown links, so
`TestRelativeLinksResolve` (which scrubs inline code before scanning links) is unaffected.

### 2. `docs/uat.md` — completed a truncated trailing sentence

The page ended with a dangling fragment present in HEAD `36205d1`:

> The packaged bundle itself is assembled by `go run ./tools/devtool bundle`; none is committed on
> this tree.
>   every `[requires SP-17 artifact]` step above depends on

Completed to: "… none is committed on this tree. Every `[requires SP-17 artifact]` step above
depends on that assembled bundle and on a host to install, upgrade or uninstall it against." The
fragment sat after the final `---` and the "Where to look next" list, outside any `## UAT-NN`
section, so the `uat_test.go` structural checks are unaffected.

## Run

`python dist/v6-remediation/run.py closeout-docs-current go test ./test/docs -count=1`
→ `{"id": "closeout-docs-current", "status": "passed", "exit_code": 0, "elapsed_seconds": 3.507}`,
`ok  github.com/qompack/qompack/test/docs 1.913s`. Evidence:
`plans/sdd/V6-remediation/runs/closeout-docs-current.{log,json,source.json}`. The harness is
reachability/shape only (link resolution, owned-page shape, generated-inventory coverage, UAT
retention/shape, source-derived troubleshooting inventories); it does not judge claim truth, which
is why the claim corrections above were verified against source by hand.

## Unresolved docs dependencies (return to main / other owners)

- **Version-tag discrepancy.** Docs record `core.Version` / `plugin.json` `0.1.0` alongside a
  disagreeing last git tag, deliberately unreconciled (README status, UAT-01 step 4). No public
  release version is selected; picking one and reconciling the tag is a release-gate decision, not a
  doc edit.
- **Stale source comment** `internal/cli/commands.go:31` ("self-test … the only command that may
  exit non-zero") contradicts `fsck`/`bench` also exiting non-zero. Product-code scope (main already
  corrected the selftest prose/comment elsewhere); left untouched here.
- **Rollover enablement** (`enableDeliveryGenerations`) stays default-off and unverified for
  compatibility and cost; docs must not describe it as available until that gate is decided.
- **Release/CI certification of the current tree.** The only recorded workflow run is the historical
  nightly on `9c84e31d`; no CI run certifies HEAD `36205d1`, and the release workflow has still never
  run. Re-running CI on the current source and the actual release/tag/publish and human UAT remain
  separate gates owned outside this pass.
