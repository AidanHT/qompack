# SP-18 handoff from SP-17

Formal input for SP-18 documentation and UAT. Link these pages; do not rewrite the scope
tables by hand. No publication has happened.

## Artifact

| Field | Value |
| --- | --- |
| Branch | `feat/sp17-packaging-hardening-and-release` |
| Integration HEAD at handoff | recorded in the commit that added this file |
| Gated commit | `4db5cea` — `release-check.json` and `final-release-check.md` cite this SHA |
| `core.Version` | `0.1.0` — do not bump from SP-18 |
| Newest git tag | `v0.2.0` — version agreement is SKIPPED until a tag equals `core.Version` |
| Publication | none: no tag pushed, no workflow run, no draft release |

**SP-17 owns (link, do not restyle the claim):** `docs/security.md`, `docs/install.md`,
`docs/release.md`, `CHANGELOG.md`, `.github/workflows/release.yml`, `.goreleaser.yaml`,
bundle assembly (`tools/devtool` bundle/build path, `packaging/`).

**SP-18 owns:** README, architecture, user guide, troubleshooting, cannot-do, upstream notes,
generated `docs/config-reference.md`, UAT prose.

Independent review: `combined-review.md` (CLEAN, same scope as `docs/release.md` §3, with the
caveats below).

## Evidence to read first

| Topic | Where |
| --- | --- |
| Supported targets and M7 rows | `docs/release.md` §3 (generated from committed records) |
| Install / upgrade / uninstall | `docs/install.md` — rehearsed: windows/amd64, user scope, directory install |
| Security and known limits | `docs/security.md` |
| Final gate | `final-release-check.md`, `release-check.json` |
| Per-commit records | `commit*-evidence.md` and the record trees beside them |

## Rollback (cite `docs/release.md` §5)

1. Disable the feature first — independent switches in `docs/release.md` §4; disable output
   replacement first when its recovery preconditions fail.
2. Restore a verified backup or compatible reader. The store API (`TakeBackup`,
   `VerifyBackup`, `RestoreBackup`) exists; **no operator command exposes it**.
3. Recovery check: `qompack fsck` (read-only default). `fsck --repair --yes` is five explicit
   repairs only (`docs/security.md` §7).
4. Re-enable only after that check passes.

Uninstall never deletes `<project>/.qompack/` or `~/.qompack/`. No secure-erasure promise. No
automatic downgrade.

## Do not advertise

- The five non-host targets as installed or deployment-verified (`unknown` in §3).
- SP17-M7-03 or SP17-M7-04 as verified (ruled `failed` records: S-6, F4-4).
- Live-task / live-model evaluation (`excluded`, R7-2).
- Universal performance or storage ratio; native history cuts; exact native bytes.
- A shipped release, a registry name reservation, or a version-agreement check (no tag).
- Rollback as a CLI: today it is a documented manual procedure plus a Go API.

## Residuals (report; do not close in SP-18 docs)

From `final-release-check.md` §5 and `commit6-evidence.md` §7: F4-9 false-orphan window
(`internal/checkpoint`); unknown key in a newer versioned block refuses on the capture path
(`internal/config`); `AppendFileVersion` in-memory history unsorted (`internal/store`); R5-3
negknow blind mode (`internal/negknow`); R5-4 live-vs-stale `ReadLock` on Windows
(`internal/daemon`); S-6 out-of-project capture archived (retrieval-time boundary).

## Integration

SP-17 lands first. Shared files: `tools/devtool/importrules.go` (SP-18 `test/docs` row; SP-17
`test/platform|security|fault|release` rows) and `.github/workflows/ci.yml` (SP-17 added jobs
only). SP-18 does not edit `docs/security.md`, `docs/release.md`, `docs/install.md`,
`release.yml`, `plugin/`, or `internal/cli`.
