# SP-17: production packaging, compatibility, hardening and reversible release

**Status:** future Wave 5/M7 plan, revised 2026-09-06; no implementation/release work performed here.
**Branch:** `feat/sp17-packaging-hardening-and-release` | **Wave:** 5 | **Prerequisites:** V5 and applicable M0–M6 gates, SP19 installed capability register, SP20 migration/backup contract | **Runs in parallel with:** SP18 on disjoint files; SP18 integrates after this artifact exists | **Design sections:** §§7, 11, 12 | **Gaps addressed:** G9.2, G9.3 packaging/residual reporting.

---

## Mission

Package the implemented Claude Code plugin only for environments supported by actual installation, recovery and task evidence. Maintain observation/report-only rollout, opt-in selective features, independent kill switches and a rehearsed rollback. Planning owner is the coordinator, requested GPT-6 Astra; future release owner and independent final reviewer own runtime signoff.

## Design context (verbatim from Qompack.md)

The existing template heading is retained; current v1.5 requirements are summarized without obsolete executable examples.

`internal/pluginmanifest/manifest.go`, `plugin/.claude-plugin/plugin.json`, `plugin/.mcp.json`, `internal/cli`, `internal/paths`, `internal/ipc`, `tools/devtool/pluginvalidate.go` and their existing tests establish current packaging/write-set context. The root `go.mod` declares Go 1.26/toolchain go1.26.6, go-winio and zstd; a manifest/package declaration is not evidence of the linked deployment runtime.

`V3-report.md` records local validation and waived-open CI J5. Preserve that report; later target/CI evidence must be recorded separately. Future release tasks and paths below are proposed where absent; package sizes, supported OS versions and performance remain measured outcomes, not fixed promises.

## Normative extracts from `plans/00-ARCHITECTURE.md`

Retain the Go sidecar, resident daemon/IPC, versioned contracts, product write-set policy and no unsolicited external services. Architecture §0.1/v1.5 supersedes historical native-cut/custom-instruction/metric guarantees. Product data/IPC locations remain distinct from this session's Markdown write allowlist. A hash provides identity, not authorization.

## Out of scope

No code, tests, builds, package assembly, release configuration, signing, installation/uninstallation, account change, branch/tag/push, baseline regeneration or data migration now. Future SP17 does not redesign SP20 storage, SP13 retrieval, SP10/11 lifecycle or SP15/16 policy. It reports defects to those owners and integrates minimal reviewed fixes. No runtime port, unsupported native control or unverified package-name availability claim.

## Interface contract

### Consumes

SP19 per-capability/environment evidence, SP20 versioned backup/import/rollback/GC contracts, SP13 protocol/authorization envelopes, SP10/11 recovery, SP21 default-off admission and SP14 command diagnostics. Retain existing configuration defaults/readers while planning safe versioned deprecation.

### Produces

A deterministic identifiable installed bundle; launcher/manifest/MCP discovery evidence per OS/version; version/license/dependency inventory; independent recording/reinjection/replacement/experiment disable controls; diagnostic/fsck policy; installation/upgrade/uninstall and recovery instructions; a release report whose advertised scope matches evidence. No future schema is guessed into an older writer.

## Implementation spec

### 1. Packaging and platform matrix

Retain the proposed six-target Go build convention only for supported target combinations proven by the future platform matrix. Verify version stamping, release/package identifiers, deterministic content checksums, launcher discovery and host CLI plugin validation on installed artifacts. Test Windows/POSIX/macOS path case, Unicode, spaces, long paths, shell/launcher failures and daemon IPC access. Preserve the user's existing Claude configuration and status line.

The current `go run ./tools/devtool plugin-validate` is repository validation. Future installed-host validation uses the supported Claude CLI syntax after version inspection. Never treat the in-repo JSON validator alone as the installed package gate. Check current registries before any availability/name claim; no registry mutation is authorized here.

### 2. Trust and failure audit

Test denied paths, symlink escapes, malicious archived text, secret capture/redaction, logs/indexes/checkpoints/exports/backups, malformed compressed objects and size bounds. No archived retrieval bypasses host permission denial and no retrieval replays a command. Existing no-network/telemetry posture is preserved; any future external service requires explicit user consent and a new architecture decision.

Failure coverage includes startup/resume/fork, manual/automatic/failed compaction, duplicate/missing/out-of-order events, child failure, disk full, permission errors, lock contention, partial spool/index/object/checkpoint writes and unavailable historical objects. Reuse M1–M6 tests continuously and exercise the installed package. A generic green unit suite cannot discharge a skipped host test.

### 3. Diagnostic and repair commands

`fsck` verifies identities, references, manifests, delta bases and roots without converting missing/unknown fidelity into exactness. Repairs are explicit and preserve diagnostic/quarantine evidence within retention policy; no destructive default cleanup. `doctor` reports supported/unknown capability, version, scope, recording/retrieval gaps and disabled controls. It must not call 4:1 storage ratio or a historical timing target proof of health.

### 4. Upgrade, cutover, rollback and retention

Take the engine-supported consistent snapshot; establish a stable import frontier and writer handoff; validate resumable import/reader parity and keep old object IDs resolvable. SP20 owns these contracts. Before new-format writes, rollback can restore the verified old representation/reader. After new writes, verify a compatible reader or restore the backup plus supported recovery path; do not imply automatic downgrade.

Disable output replacement first when its recovery preconditions fail; independent switches also control reinjection, experiments and recording. Recording can be disabled for privacy while permitted historical retrieval remains explicitly scoped. Uninstall removes integration/binaries according to a documented policy, retaining or explicitly deleting data by the user's choice. Do not promise secure physical erasure across backups/media.

### 5. Release evaluation and rollout

V6 requires deterministic fixed-input, closed-loop isolated-task and recovery/failure layers. Compare stock, current Qompack, corrected recovery and admission separately. Predeclare regression margins, sample rationale and held-out changing-requirement tasks. Record failed/aborted/excluded/inconclusive trials and missing usage. Cost and correctness are separate gates.

Roll out observation/report-only, then opt-in capabilities whose installed-target tests passed, then broaden only with representative evidence. Optional algorithms are not release prerequisites. Unknown host/schema versions disable unsafe optimization and report unsupported/degraded status without corrupting data.

## Test plan (TDD)

Existing future commands: `go run ./tools/devtool plugin-validate`, `go run ./tools/devtool test`, `go run ./tools/devtool test-race`, `go run ./tools/devtool build-all`, and `go run ./tools/devtool replay --ci`. Their existence is source-inspected; none ran here. Packaging/platform/security/fault/install/live-task entrypoints named by the original plan are proposed until created; inspect dispatch instead of inventing flags.

| Future ID | Observable acceptance and artifact |
|---|---|
| SP17-M7-01 | Installed launcher/manifest/tool discovery passes exact host/provider/OS/version matrix; bundle identifiers/checksums recorded |
| SP17-M7-02 | Paths-with-spaces/Unicode/case/long path and managed restrictions have correct results; unsupported optimizations disabled |
| SP17-M7-03 | Denied/symlink/secret/archive/decompression tests leak no forbidden preview or expansion and stay bounded |
| SP17-M7-04 | Forced failure at each publication boundary yields recoverable or explicit incomplete state; no newly dangling pointer |
| SP17-M7-05 | Old readers/import restart/rollback before and after new writes pass with backup and identity parity |
| SP17-M7-06 | Independent kill switches stop their feature, unknown schemas safely degrade, uninstall follows retained-data choice |
| SP17-M7-07 | Release benchmark includes primary task/constraint/recovery outcomes, uncertainty and complete-or-unknown accounting |
| SP17-M7-08 | Supported platforms/licenses/version/name checks and rollback rehearsal are attached; no unsupported feature advertised |

### Focused validation and bounded parallel runs

Apply [R2 validation scheduling](MIGRATION-EVIDENCE.md#focused-validation-and-bounded-parallel-runs) to every commit, validation-command catalog and acceptance row in this plan. Existing broad commands are available entry points, not an instruction to rerun the whole tree per edit, role or row. Use affected tests and consumers first; schedule a long run only for its named coverage obligation or a documented regression question. Preserve all test IDs, thresholds and failure evidence. No test executes in this planning pass.

**Short checks to dispatch first.** Check changed manifest/launcher/version, path/permission, fsck/doctor and archive-validation cases first. Independent platform or fault cases may run in parallel only with separate installed bundles, stores and existing available runners; inspect actual process cost before scheduling.

**When broader checks are necessary.** Reserve full build/platform/security, installed-host, crash/upgrade/rollback and release evaluation for their named commits or the frozen release artifact. Share matching SP-20/V4/V5 evidence as context, but perform the installed-artifact checks required by SP17-M7-01–08 and V6; earlier source snapshots do not certify a new bundle.

The implementation owner records selected real cases, expected runtime/resources, actual results and uncovered requirements before handing off. Reuse the existing R1 role structure and global worker limit under this plan's Opus 4.8-only subagent rule; do not spawn an expensive extra child just to wait on a command. The coordinator owns shared artifacts and final acceptance.

## Commit plan

Eight future commits retain original numbering/areas, conventional subjects and no attribution trailers. Meaningful tests precede the compatible change; each future commit retains command/result artifacts and updated rollback notes. Preserve already implemented equivalent work after reconciliation.

### Commit 1 — `build(packaging): assemble a versioned plugin bundle`

- [ ] Specify bundle/launcher/version contracts, implement deterministic assembly and retain installed validation evidence.

### Commit 2 — `test(platform): verify supported deployment environments`

- [ ] Add path/filesystem/IPC/permission cases, execute the actual supported matrix and record unsupported cases.

### Commit 3 — `test(security): verify archive trust and privacy boundaries`

- [ ] Verify permission-before-preview/expansion, redaction/retention and bounded decoding; independent trust review.

### Commit 4 — `test(fault): exercise capture and lifecycle failures`

- [ ] Reuse and extend publication/locking/lifecycle/child failure matrix on installed package; no unit-only certification.

### Commit 5 — `feat(cli): report integrity and capability diagnostics`

- [ ] Implement compatible fsck/doctor contracts, error/unknown status and explicit repair behavior; test rollback-safe diagnostics.

### Commit 6 — `fix(hardening): resolve verified release blockers`

- [ ] Integrate minimal fixes with affected owners, preserve frozen reader compatibility and repeat affected failure gates.

### Commit 7 — `ci(release): gate release on evidence and rollback`

- [ ] Prepare versioned release workflow/tooling and independent feature switches; validate package identity/licenses and rollback before future publication.

### Commit 8 — `test(e2e): rehearse installation upgrade and removal`

- [ ] Execute install/upgrade/uninstall and pre/post-write rollback; attach V6 evidence and actual supported release scope.

## Subagent strategy

Future branch/worktree: `feat/sp17-packaging-hardening-and-release`, proposed `../qompack-sp17`. Preserve original A–F roles, run only within separately authorized implementation capacity:

| Future role | Owned proposed files / boundary |
|---|---|
| A packaging | `packaging/launcher/`, bundle/version helpers, launcher fixtures; initial CHANGELOG skeleton only |
| B platform | `test/platform/`, platform helper; defects returned to package owners |
| C security | `test/security/`, bounded secret-marker fixtures, security helper; proposes audit prose |
| D fault/recovery | `test/fault/`, fault helper; proposes recovery prose |
| E diagnostics | `internal/cli/fsck.go`, `doctor.go` and tests after A's version integration |
| F release/install | release helper/install tests and `docs/release.md`, `docs/install.md`; CHANGELOG body after A |
| Main release owner | Workflows/build config, shared config/CLI, source hardening fixes, `docs/security.md` integration, commits1–8 |
| Independent reviewer | Read-only release/privacy/migration/cost evidence review before V6 |

No two roles edit `docs/security.md`; C/D return proposals to its sole main owner. SP18 links to SP17-owned security/install/release artifacts. Existing `test/replay` live-runner work is assigned to the evaluation owner and integrated sequentially with F; it cannot overwrite SP19's baseline work.

### Future model and effort assignments

Apply [R1 model/effort, availability, fallback and cost policy](MIGRATION-EVIDENCE.md#future-implementation-subagents-for-sp-14-through-sp-21). Preserve A–F, main ownership and all release gates. The role list is not six concurrent agents.

**V6 routing override — user directive, 2026-09-14.** Every subagent this plan dispatches runs Opus 4.8 (`claude-opus-4-8`) strictly; Fable 5.1 (`claude-fable-5-1`) is the main/coordinator session model and is never a child. This narrows R1's Fable rows for V6 only and leaves the shared R1 policy and the other waves unchanged. It is the shape R1 already documents for an unavailable Fable, so a mandatory independent review is satisfied by an Opus 4.8 / high reviewer in a thread that did not author the change, escalating that one seat to Opus xhigh only for a documented unresolved issue. Questions R1 would route to Fable — shared-contract conflicts, durable-data/rollback and trust-boundary decisions — return to the main session instead of spawning a premium child.

| Existing role | Requested model and effort | Reason and boundary |
|---|---|---|
| A packaging; E diagnostics; F release/install | Opus 4.8 / high | Bounded packaging, version and diagnostic contracts; main retains publishing, shared CI/config and final decisions |
| B platform | Opus 4.8 / high | Design and interpret installed-artifact/platform checks; medium only for collation of actual results |
| C security; D fault/recovery | Opus 4.8 / high | Evaluate concrete denial, crash and rollback scenarios in assigned fixtures; return unresolved cross-storage/trust questions to the Fable 5.1 main session rather than escalating a child |
| Independent release/integrity reviewer | Opus 4.8 / high | Reconcile package, privacy, migration/rollback and evidence scope before release acceptance; must be a thread that did not author the change, escalating to Opus xhigh only for a documented unresolved issue |

A establishes the artifact/version contract first. B/C/D may then occupy up to three disjoint slots using isolated disposable environments. E waits for A; F consumes the artifact and relevant results; do not race on CHANGELOG, docs/security, shared helpers or release metadata. The independent reviewer starts when the combined artifact and evidence exist and occupies one slot; no child runs Fable, and R1's one-Fable slot stays unused rather than becoming extra Opus concurrency. Reuse workers for related follow-ups and serialize quiet-run benchmarks, actual release steps and shared integration. No subagent receives authority to publish, change accounts, or contact outsiders from this planning policy.

## Exit criteria

- [ ] R2 run map distinguishes focused checks, parallel isolated groups and justified long gates; every required case has current evidence or an explicitly accepted blocked/disabled disposition, with no timeout, zero-test run or old-tip result counted as a pass.
- [ ] Future delegation follows R1 and this plan's role/effort table: record requested/observed routing or its explicit fallback, enforce ownership/concurrency, review the first slice, and retain required independent review and available usage evidence.

- [ ] SP17-M7-01–08 and V6 primary quality/recovery gates have actual evidence or a documented reduced release scope.
- [ ] Migration and rollback are rehearsed before/after new-format writes.
- [ ] Installed host validation covers claimed environments; skipped tests leave capability unverified.
- [ ] Independent switches and privacy denial behave as documented.
- [ ] Supported artifact identifiers, dependency licenses and name checks are current at release.

## Done checklist

- [ ] Eight conventional future commits retain original IDs and no attribution trailers.
- [ ] Release/upgrade owner and independent reviewer sign the same evidence-backed scope.
- [ ] No unverified native controls, universal performance or exact-history claims appear in package/help.
- [ ] SP18 receives the real supported package and rollback instructions before final UAT documentation.

### Rollout, rollback and blockers

Installed host versions/permissions, actual package checks, migration backups and cross-platform CI remain future verification actions (SP17-M7-01/05/08). J5 historical waiver stays recorded; it does not waive new release gates. A failed gate keeps the affected feature disabled or narrows support. The rollback sequence is feature disablement, verified compatible reader/backup restoration, recovery check, then only separately approved reenabling. No release, tag, installation or rollback is executed here.
