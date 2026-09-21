# V6-VERIFY — SP-18 docs reconciled against integrated SP-17

Author: V6-K (documentation integration). Base: `301a8e9` (integrated SP17 + SP18). No git mutation,
no test/build/generator run in this pass — main owns those and the independent review.

## 1. Why this pass exists

The focused integration at `dist/v6/focused-integration-original.log` failed one test:

```
--- FAIL: TestNoPlannedPointerToAnExistingPage (0.05s)
    15 references: README.md, docs/user-guide.md, docs/troubleshooting.md, docs/uat.md name
    docs/install.md / docs/security.md / docs/release.md as "planned (SP-17)" in plain text,
    but SP-17 has landed those files.
```

That failure is only the surface symptom. SP-18's prose was written before SP-17 merged, so it also
carried **behavioural** claims that SP-17 made stale: that `qompack doctor`/`qompack fsck` are
unimplemented, that no recovery check exists, that installed-host compatibility is unverified on
every target, and a UAT recovery model built on "compare three index files = a verified restore."
This pass converts the pointers **and** corrects the assertions, verified against source rather than
against SP-17 prose.

## 2. Ground truth used (read, not assumed)

| Claim | Verified against |
| --- | --- |
| `doctor` implemented, read-only | `internal/cli/doctor.go` (`runDoctor`, flags `--project`/`--json`, no write path); registered in `internal/cli/commands.go` `doctorCmds()` |
| `fsck` implemented, read-only by default, `--repair` needs `--yes`, five additive repairs, never deletes | `internal/cli/fsck.go` (`runFsck`; `--repair && !--yes` → usage error; exits 0/1/2); `docs/security.md` §7, `docs/release.md` §5 |
| `bench` still **not** implemented | `internal/cli/commands.go` `notImplemented = [{"bench", …}]`; `notImplementedRun` prints `qompack bench: not implemented in this build` |
| Backup/rollback API is Go-only behind a **closed** gate | `internal/store/backup.go` (`TakeBackup`/`VerifyBackup`/`RestoreBackup`/`RehearseRollback`); `internal/store/migrate.go` `NewMigrator` returns `ErrMigrationGateClosed` unless `o.Gate.Passed` |
| The gate is a **build** gate with no config key, closed in this build | `internal/config/migration.go` `LegacyImportGateKey = "store.migrate.legacyImportCutover"`, `LegacyImportGate().Passed` is the zero value (false) |
| `qompack fsck` is the operator-facing recovery **check**; restore stays operator's own copy | `docs/release.md` §5 ("restore a backup with the store API or from your own copy, run `qompack fsck` as the recovery check") |
| windows/amd64 `installed-verified`; other five `unknown`; no live-model run | `docs/release.md` §3 scope table + `commit8-install-windows-amd64/install_launcher_resolves_in_cache.json`; §7 "No workflow has ever run"; excluded row "no live model runs …, eval.LiveRunner is nil" |
| Versions on this tree | `internal/core/version.go` `Version = "0.1.0"`; `plugin/.claude-plugin/plugin.json` `0.1.0`; last version tag `v0.2.0` |

## 3. Edits made (smallest coherent set)

Exclusive-editable files only. All twelve UAT IDs and all Result records left `not executed —
capability unverified`; no scenario ID or evidence block dropped; no new product / host / live-model
pass claimed.

### README.md
- **Supported environments → "What that does not cover":** replaced the flat
  `implemented_unverified` paragraph with the actual scope — windows/amd64 `installed-verified`
  (install + launcher-in-cache), the other five `unknown`, and **no live-session/live-model run on
  any target** — linking `docs/release.md#3-supported-scope` and `docs/uat.md`.
- **Release-artifact paragraph (the 3 failing pointers):** converted to links; SP-17 packaging and
  `docs/{install,security,release}.md` now exist. States the bundle is assembled by
  `go run ./tools/devtool bundle` and that **no release has been published and no workflow has run**
  (`docs/release.md#7-not-claimed`).

### docs/user-guide.md
- Intro (pointer @12) and the closing "Planned" block (@470–473) converted to links.
- Operator-commands table: added read-only `doctor` and `fsck` rows.
- "Not implemented in this build" paragraph rewritten: `doctor`/`fsck` are now read-only diagnostics
  (linking `security.md#7-…` and `release.md#5-rollback`); only **`bench`** remains unimplemented.

### docs/troubleshooting.md
- Pointer @510 and closing block @588–590 converted to links.
- §9 reachability sentence: dropped the false "`doctor`, `fsck` … not implemented"; now names the two
  read-only diagnostics and keeps `bench` as the only unimplemented one.
- §9 "What is not": backup/restore/rollback remain Go-only behind the closed gate, but the interim
  rollback is now the documented `docs/release.md` §5 procedure (disable switch → restore from your
  own copy → `qompack fsck` recovery check → re-enable). Removed the claim that comparing index files
  is the verification.

### docs/cannot-do.md
- "### `doctor` and `fsck` are unimplemented" → **"### No operator backup, restore or rollback
  command; `bench` unimplemented"** (no inbound anchor links — verified). Now states the true
  residual limit and points at `fsck`/`doctor` as what exists.
- "### Installed-host compatibility is `implemented_unverified`" → **"### Installed-host
  compatibility is verified for windows/amd64 only"**. windows/amd64 `installed-verified`; others
  `unknown`; no live-model run. **The four not-yet-implemented contract IDs are preserved verbatim**
  (`hook.additional_context_delivered`, `precompact.has_time_to_write`,
  `precompact.custom_instructions_accepted`, `mcp.server_registered`) so
  `TestCannotDoNamesTheUnimplementedChecks` still holds; the historical `plans/V5-report.md` §24 /
  `plans/MIGRATION-EVIDENCE.md` citations stay in the section's "Recorded at" bullet.

### docs/uat.md
- Pointer @1113 and the intro/`[requires SP-17 artifact]` framing updated: packaging exists, but
  nothing is built/installed/run here → every row stays **not executed**.
- **Recovery model reconciled** (the substantive UAT change):
  - Copy is taken **while no writer is running**; restore is preceded by **stopping the daemon you
    started** (idle exit or terminating the `pid` in `.qompack/run/daemon.lock`) — no live-writer
    store is copied or restored.
  - The post-run store is **moved aside as evidence, never deleted** ("never delete a failed run's
    store"); append-only protected data is never trimmed.
  - **`qompack fsck` is the recovery check** (real, read-only, engine-supported per `release.md` §5).
    The file-by-file index/manifest comparison is demoted to an operator **sanity check** — it is
    explicitly **not** what certifies the store. This replaces the former "three index comparisons =
    a verified restore" / "the only `Rollback verified: yes` this page recognizes."
  - The `Rollback verified` field definition, "How to run" step 2 & step 5, all per-scenario
    `Rollback:` lines (UAT-01…UAT-11) and UAT-12 step 8 / expected / rollback / evidence were updated
    to this model. No CLI was invented (restore = operator's own copy; check = `qompack fsck`).
  - UAT-03: corrected the stale "`fsck` … not implemented" line to describe `fsck` re-hashing the
    checkpoint tier read-only.

## 4. Test-surface safety notes for main (test/docs)

- **TestNoPlannedPointerToAnExistingPage:** the 15 pointers are links now; the only surviving
  "planned" is the convention description in `cannot-do.md` (no path within the marker gap).
- **TestRelativeLinksResolve:** every new link's file exists and every new anchor was matched to its
  target heading's slug — `release.md#{3-supported-scope,4-switches,5-rollback,7-not-claimed}`,
  `security.md#7-what-needs-an-operator-and-how-to-find-it`, `user-guide.md#operator-commands`,
  `troubleshooting.md#9-backup-rollback-and-recovery` (all confirmed present).
- **Renamed headings** in `cannot-do.md` have no inbound anchor links (grep-verified), so no link
  breaks.
- **TestUAT{RetainsAllTwelveIDs,SectionsHaveTheRecordShape,UnexecutedRowsSayUnverified}:** no
  `## UAT-NN` heading, block label, block order, or Result field touched; all Results still contain
  `capability unverified`.
- **TestUATConfigKeysExist:** no new dotted-key code span introduced; `qompack fsck …` carries no
  dot, and reused spans (`records/eliminations.jsonl`, `config-violations.json`) already pass.
- **TestCannotDo{CoversSection12Limits,NamesTheUnimplementedChecks}:** edited sections carry none of
  the §12 nouns; the four contract IDs are preserved.
- **User-guide / troubleshooting content tests:** no generated command/tool code span, binding
  qualification, checkpoint-unrouted phrase, evidence-outcome / self-test / contract-ID code span, or
  config-reference section link removed.

## 5. Open contract issues for main (not fixed here — out of my file scope or needing a decision)

1. **Version discrepancy.** Last tag `v0.2.0`, but `internal/core.Version` and
   `plugin/.claude-plugin/plugin.json` are `0.1.0`. `docs/release.md` §1 says `release-check` refuses
   a tag whose version ≠ the constant, so a `v0.2.0` tag is inconsistent with the shipped constant.
   README's "Status: pre-release" states both numbers as-is (still literally true) and was left
   unchanged. Decision needed: bump the constant, retag, or document the pre-SP-17 tag as historical.
2. **README "Status" framing.** "Release and versioning are SP-17 deliverables and neither number is
   changed by this page" reads as pre-SP-17. Left minimal to avoid a version claim; update once (1)
   is decided.
3. **`[requires SP-17 artifact]` marker name.** SP-17's packaging now exists; the marker's literal
   name is anachronistic though its meaning is now defined accurately (a built/installed bundle +
   host run, none done here). A rename is cosmetic and was not done (referenced ~20×).
4. **Generated-vs-prose coupling.** README, cannot-do and uat now assert "windows/amd64
   installed-verified, others unknown" from `docs/release.md` §3. That table is generated
   (`devtool release-scope`); if it is regenerated with different outcomes, this prose must follow.

## 6. Checks recommended (run by main — I did not run any)

- `go test ./test/docs/...` (the whole package; the original failure and every guard above).
- Re-run the focused integration that produced `dist/v6/focused-integration-original.log`.
- Independent doc review of the reconciled recovery model in `docs/uat.md` §"How to run a scenario"
  and UAT-12, and of the `docs/cannot-do.md` section renames, against `docs/release.md` §5 / §3 and
  `docs/security.md` §7.
- Spot-check `go run ./tools/devtool release-scope --markdown` still matches `docs/release.md` §3 so
  the prose I keyed to it is not keyed to a stale table.

## Coordinator correction after author handoff

The coordinator revised the authored rollback guidance before `docs-integrated`: copying a stopped store and running fsck does not establish the engine backup/frontier/reader contract. UAT-12 recovery remains unverified while the production LegacyImportGate is closed and no operator command exposes the rehearsed engine flow. README CI rows describe configured checks; installed Windows CLI evidence does not establish a live human session. `docs/security.md` and `docs/release.md` now name the authorization failures and block release. These changes are coordinator-authored, not claimed as independently approved by this docs author. The combined test/docs and tools/devtool check passed; final independent review is recorded separately.
