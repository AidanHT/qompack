# Task 4 — Commit 4 `docs(sp18): document observable failures and recovery`

Plan text (verbatim): "Cover unknown telemetry, capture/retrieval/permission/schema failures, provenance and rollback without synthetic claims."

Plan §1 row (verbatim): "docs/troubleshooting.md — Provenance-first diagnosis, unknown capability/telemetry, capture gaps, denied/unavailable evidence, schema compatibility, safe disable and recovery".

Worktree: `C:\Users\Quant\Documents\Programming\Projects\qompack-sp18`, branch `feat/sp18-documentation-and-uat`.

## Files you own

- `docs/troubleshooting.md` — new.
- `test/docs/troubleshooting_test.go` — new, in the existing `test/docs` package (read `test/docs/*.go` first; reuse `repoRoot` and the markdown helpers).
- The `ownedDocs` list in `test/docs` — append `"docs/troubleshooting.md"`. Nothing else in that file.

Do NOT edit any other file. Never `git stash`. Never push.

## Facts the coordinator verified on this tree (re-verify what you cite; cite the source, not the coordinator)

- Build the binary with `go build -o <scratch>/qompack.exe ./cmd/qompack`. Run probes ONLY inside a fresh scratch directory outside the repository (`qompack status` creates `.qompack/` in the current directory and spawns a per-project daemon; Task 3 verified that `qompack config print` does NOT; `bench` is also "not implemented in this build", same table as `doctor`/`fsck`, exit 1); when finished, stop the daemon you spawned (`Get-Process qompack | Where-Object Path -like '*<scratch>*' | Stop-Process -Force`) and delete the directory. Say in the report that you did.
- Task 3 probed (observed on this tree, recorded in docs/user-guide.md's checkpoint and operator sections): a hook entry point typed by hand with empty stdin (`qompack checkpoint </dev/null`) prints `{}`, exits 0, creates the full `.qompack/` layout, starts a daemon and records the empty payload under `records/captures`; no checkpoint is written. Treat every hook entry point as a write. Keep the troubleshooting page consistent with that wording.
- `qompack self-test` prints a CHECK/OK/SEVERITY/OBSERVED table (`--json` gives `{checks,mode,exit}`) and is the only command that may exit non-zero. In an empty directory on this tree it reports `mode: full`, exit 0, and several checks whose OBSERVED column reads `not-yet-implemented` (e.g. `hook.additional_context_delivered`, `precompact.has_time_to_write`, `precompact.custom_instructions_accepted`, `mcp.server_registered`) while OK is `ok`. The page must tell the reader that `ok` + `not-yet-implemented` means "no assertion exists yet", not "verified". Read `internal/cli/selftest.go` and `internal/contract` (package comment: the host-contract monitor of 00-ARCHITECTURE.md §5.19/§12.1, degrades loudly to passive recording when a contract does not hold) for the check names and what each observes.
- `qompack status` shows a `source:` line, a daemon line, and a per-hook latency table where rows read `unavailable` with the reason "no per-hook instrument: … folded into the hook_controlled aggregate". Read `internal/cli/qompack_commands.go` / the status implementation for the section list.
- `qompack config print --provenance` prints the effective config as JSONC with an origin comment per leaf (`// default config.Defaults()` etc.); `qompack config schema` prints the JSON Schema. `internal/cli/config.go` documents `configViolationsFile` — the §11.3 record of every leaf that fell back to its default, under the state directory (`.qompack/state/config-violations.json` per `docs/config-reference.md`'s header). Warnings are reported through the `Loud` channel; read `internal/config/load.go` and `internal/cli/config.go` for what "Loud" is and where it lands.
- Config compatibility: `docs/config-reference.md` (regenerated in Commit 2) has sections "Versioned blocks", "Gated switches (ship off)", "Retired-meaning keys": a file declaring a newer `settingsVersion` has its block reset to defaults and warns; a `true` on a pending gated switch is refused and falls back; retired-meaning keys still apply but warn. Unknown keys warn, never error.
- Operating modes: `runtime.mode` ∈ {auto, full, passive, off} (00-ARCH §12); `runtime.daemon.enabled`; `runtime.migration.reinjection.sessionStartCompact` (false disables injection without touching recording); `runtime.telemetry.enabled` is hardwired off and `runtime.migration.compaction.blockManualCompact` is hardwired false ("the key exists only to say so"). Read `internal/config` and `internal/cli/sessionstart.go` to state precisely what `passive` and `off` do, and what the contract monitor does when it degrades to passive.
- The project write-set created by a first run (observed): `.qompack/{.gitignore, backup, checkpoints, dag, eval/{opt,replay}, grammar, index/*.jsonl, logs/qompack-YYYYMMDD.log, metrics, migrate, objects, pins, records, run/{daemon.hb, daemon.lock, state.bin}, sketches, spool, state/drain.json}`. Confirm against `internal/paths`. `docs/architecture.md` (Commit 1) has a write-set section — link to it rather than duplicating the full list.
- Migration backup/rollback: `.qompack/backup` and `.qompack/migrate` exist; the legacy-import build gate `store.migrate.legacyImportCutover` is closed (Passed=false) so `store.NewMigrator` refuses to import or cut over (`internal/config/migration.go`). plans/V5-report.md §24 records SP-20 gates for resumable migration/backup/rollback as `verified_in_target` (I-06.19) and plans/MIGRATION-EVIDENCE.md lines 469–472 record a rollback rehearsal. Read `internal/store` for the migrator/backup entry points that exist and say which are reachable by an operator command today (`qompack help` lists `admin delivery-seal` only; `doctor` and `fsck` print "not implemented in this build"). Do not describe a rollback command that does not exist; describe the manual procedure (stop the daemon, copy/restore the directory) only if the code's backup layout supports it — otherwise say the operator procedure is SP-17's deliverable and mark it "planned (SP-17)".
- Retrieval outcomes: `core.EvidenceOutcome` ∈ {ok, absent, unavailable, denied, corrupt, expired, uncertain}; `core.Fidelity`, `core.Coverage` as in 00-ARCHITECTURE.md line 62. `docs/mcp-tools.md`: `unavailable` never establishes absence; `expand` fidelity/coverage may be incomplete; `re_read` is never a live disk read.
- Redaction: `runtime.redact.enabled` default true, `runtime.redact.patterns`; `internal/redact`. A `redacted` fidelity means the original was captured with redaction applied; it cannot be recovered from Qompack.

## Required content of `docs/troubleshooting.md`

Every entry has the shape: **Symptom** (what the user sees, quoting the exact output text where you verified it) → **Diagnose** (the command and the field to read) → **Meaning** → **Action** (or "no action; this is a recorded limit", with the pointer). Sections, in order:

1. **Start with provenance** — `qompack self-test` (read OK, SEVERITY, OBSERVED; `not-yet-implemented` is not verification), `qompack status` (source line, daemon line, `unavailable` rows), `qompack config print --provenance`, the config-violations file, the log file under `.qompack/logs/`. State that all of these create `.qompack/` and may start a daemon in the current directory.
2. **Unknown capability or telemetry** — a contract check that fails or reports unknown; what "degraded to passive recording" means; `unavailable` latency rows; `runtime.telemetry.enabled` is hardwired off (nothing is sent anywhere).
3. **Capture gaps** — `Fidelity` values other than `exact` and what each implies; partial/truncated/binary/failure/unknown; interrupted output; child (subagent) work only captured through the `SubagentStop` hook; no reconstruction from current files.
4. **Denied or unavailable evidence** — `denied` is not empty; `unavailable` is not absent; `expired`/`corrupt`; what `already_tried`'s `unavailable` requires of the caller.
5. **Retrieval that looks wrong** — a `re_read` that differs from disk (it is the store's version history); minimal spans (`full: true`, `next_span`); a stale elimination (`stale` vs `active` in `already_tried`; the recorded partial from plans/V5-report.md §22 items 26–27: a blind-ledger digest can render a stale record `[active]`).
6. **Configuration and schema compatibility** — the four warning classes (invalid value → default; unknown key; newer settingsVersion → block reset; retired meaning) and the gated switches that refuse `true`; what to do after a plugin downgrade/upgrade (read `docs/config-reference.md`'s new sections and link their anchors).
7. **Daemon problems** — lock held (`.qompack/run/daemon.lock`), `admin delivery-seal` refuses while a daemon owns the lock; idle exit (`runtime.daemon.idleExitSeconds`); how to stop it safely (read `internal/daemon`/`internal/cli/daemon.go` for the supported stop path; if there is none beyond process termination, say so).
8. **Safe disable** — the switch ladder from least to most invasive: `runtime.migration.reinjection.sessionStartCompact=false` (stop injecting, keep recording) → `runtime.mode=passive` → `runtime.mode=off` → `runtime.daemon.enabled=false` → removing the plugin from Claude Code (describe only the generic host mechanism; the install/uninstall guide is "planned (SP-17): docs/install.md"). State for each what keeps being written.
9. **Backup, rollback and recovery** — what exists (`.qompack/backup`, `.qompack/migrate`, the closed legacy-import gate), what is verified (V5-report §24 I-06.19; MIGRATION-EVIDENCE rollback rehearsal), what is not an operator command yet. "Do not downgrade data to match old prose."
10. **What not to conclude** — a short list: a passing self-test is not a verified host contract for `not-yet-implemented` rows; `dropped` output is qualified coverage, not proof of native eviction; an `ephemeral` tag is a Qompack record property.

## Tests (`test/docs/troubleshooting_test.go`; write first, RED, then GREEN)

1. `TestTroubleshootingNamesEveryEvidenceOutcome` — every `core.EvidenceOutcome` value name (derive the list by parsing `internal/core`'s Go source for the constants — read how `internal/core` declares them; a small regexp over the file is acceptable, importing `internal/core` from `test/docs` is NOT allowed because the package is stdlib-only by Commit 1's contract) appears in the page as a code span.
2. `TestTroubleshootingNamesEverySelfTestCheck` — every self-test check name declared in `internal/cli/selftest.go` (parse the source for the string literals of the check names — find the declaration pattern first) appears in the page. No count literal.
3. `TestTroubleshootingLinksConfigReferenceSections` — the page links `docs/config-reference.md#versioned-blocks`, `#gated-switches-ship-off` and `#retired-meaning-keys` (the anchors Commit 2's generator emits; verify the exact slugs in the regenerated page) and `TestRelativeLinksResolve` passes.
4. Existing `TestOwnedDocsExist` passes with the new entry.

## Validation before committing

```
go test -count=1 ./test/docs/...
go vet ./test/docs/...
go run ./tools/devtool fmt-check
```
Prose-plus-tests commit; no lint, whole tree, race, coverage or replay.

## Claims policy (binding)

No synthetic symptom: every quoted output line was produced by the binary you built or is quoted from the source with its file path. No performance/cost/savings claims. No native-control claims. Files that do not exist are plain "planned (SP-NN)" text. No fixed counts in tests. Nothing here prescribes a status-line replacement.

## Commit

Exactly one commit, subject exactly `docs(sp18): document observable failures and recovery`. Body: the sections, which outputs were observed on this tree (command and scratch-dir note), the tests, and the recorded limits (doctor/fsck unimplemented; operator rollback command absent). No trailers.
