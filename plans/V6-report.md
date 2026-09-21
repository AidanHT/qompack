# V6 — production readiness and UAT verification

**Decision: BLOCKED / NOT RELEASE READY. Checkpoint incomplete.** Focused verification found two retrieval authorization bypasses and reproduced publication/recovery gaps. No release, tag, push, broader rollout or merge of `verify/v6` back into `develop` is authorized or performed by this report. Passing diagnostics and documentation checks do not waive these failures.

Execution date: **2026-09-20 America/Toronto** (some evidence records use **2026-09-21 UTC**). Coordinator: this Codex session; effective model metadata is unavailable. The requested Fable 5.1 coordinator identity is **not established**. All dispatched children were explicitly requested as Opus 4.8 high and their reported canonical route was checked before substantive work; no model alias was accepted as proof. See [routing and usage](sdd/V6-VERIFY/request-usage.json).

## 1. Candidate, integration and preserved baseline

The user's follow-up authorized SP17 → SP18 integration and this isolated checkpoint. The original dirty worktree was preserved. Integration used existing branches, incoming-branch conflict resolution, and non-fast-forward merges:

| Item | Identity / consequence |
|---|---|
| Original root, untouched | `7f92af5353a6bd084aa49043351e4183ecbcb2ad`; 17 dirty tracked files and three historical report hashes still match the preflight snapshot |
| Previous integrated develop | `9c84e31d596ff2357cceeed42af240c8b6642dd5` |
| SP17 delivery | `6efe2239e0cff556029bb248faa37127f29714fc` |
| SP17 integration | `dfc4104e9a93133d2d2e17550ab5201ca6353104` |
| SP18 original delivery | `34d058e9ebbc2f59df41987dce69f47256400274` |
| SP18 incoming reconciliation | `8e9dcd5`; retained both CI additions and the union of import-policy roots |
| SP18 integration / V6 base | `301a8e9da53a567ec4d42209b74e71a04e26a1e6` |
| Verification worktree | `../qompack-v6`, branch `verify/v6`; clean at creation |
| Test additions | `ccfd4dd`; intentionally failing authorization regressions and passing packaged fsck diagnostic |
| Tested documentation committed | `7eb390a`; documentation changes only, after the recorded `docs-integrated` run |

SP18 had been authored before SP17 integration. The authorized reconciliation preserved that history; it did not recreate or destructively re-merge deliveries. Its integration consequences were stale planned-document pointers and conflicting CI/import-policy additions, now corrected. The focused devtool merge checks passed on both sides of the final merge; those terminal outputs were not saved as standalone run artifacts. The original combined docs/devtool check failed on 15 stale planned pointers; its [original log](sdd/V6-VERIFY/runs/focused-integration-original.log) is retained. No precise start time was recorded for that initial check.

The tested production source remains `301a8e9`: no files in `internal`, `cmd`, `plugin`, `go.mod` or `go.sum` changed during V6. Test and documentation overlays were dirty during their runs and are now committed as above. Run JSON records distinguish the source HEAD and dirty paths at each invocation. Later report-only commits do not certify a newly built release artifact. [Preservation check](sdd/V6-VERIFY/preservation.json), [pre-integration snapshot](sdd/V6-VERIFY/pre-integration-snapshot.json).

[Tested-source hashes](sdd/V6-VERIFY/tested-source.json) retain the final test/documentation bytes. Intermediate uncommitted fixture revisions were not separately snapshotted: their original failure logs and outcome records survive, while the committed final fixtures and corresponding later runs are the reproducible authority. This limits reconstruction of those preliminary attempts and does not erase their failures.

**Environment:** Windows 11 Home 10.0.26200, windows/amd64; Intel Core Ultra 7 155H, 22 logical CPUs; Go 1.26.6; Claude Code CLI 2.1.263. `GOMAXPROCS=2` for recorded focused runs. These were shared authoring runners, not quiet performance runners. No product provider/model task trial was executed. Source/plugin declared version 0.1.0 and latest historical tag v0.2.0 do not establish release tag agreement. Individual schema checks are recorded by their tests; no blanket cross-schema compatibility claim is made.

| Artifact | Binary SHA256 | Scope |
|---|---|---|
| `v0.2.0-640-g301a8e9` | `f171f46200b93a5cc476b12a41c9f623656f34c82da0cfd3394ae245f53a3ef7` | Original security/publication scenarios, with separate disposable stores and IPC |
| `v0.2.0-640-g301a8e9-dirty` | `6811df18dc0e8583a5e2184e44b2f64891d19f9b8de5ba51e3d5bdf16c13ab51` | Real-capture hash regression and corrected packaged fsck scenario |
| Install rehearsal versions `0.1.0` and `0.1.1` | Per-record bundle hashes in [install artifacts](sdd/V6-VERIFY/install/INDEX.json) | Different version stamps built from the same source, not two historical released binaries |

A separately assembled [local forensic bundle](sdd/V6-VERIFY/forensic-artifact.json) retains the second binary hash, [BUNDLE.json](sdd/V6-VERIFY/forensic-BUNDLE.json) and [checksums](sdd/V6-VERIFY/forensic-checksums.txt). The directory is in ignored `dist/v6/candidate/`; it is neither a published release nor a substitution for the bundles installed by earlier tests. Temp test installations/stores were cleaned by their harnesses; reconstruction uses the committed synthetic fixtures and recorded commands. Whole-bundle byte reproducibility was not re-certified.

## 2. Cumulative inventory and exit criteria

The [source reconciliation](sdd/V6-VERIFY/inventory.md) retains all **304 distinct original IDs**, from 1.1.1 through 1.18.14, with old-to-new assertions, retirements and unresolved mappings. [execution.tsv](sdd/V6-VERIFY/execution.tsv) supplies an explicit result and scoped supporting run for every ID. All eighteen original SP families remain present; SP19/20/21 additions are additive. Structural completeness is checked in [inventory-completeness.json](sdd/V6-VERIFY/inventory-completeness.json), not counted as runtime acceptance.

| Families | Current disposition |
|---|---|
| SP01–04 foundation, accounting, sketches, chunk/canon/symbols | Source mapped; full current assertions and measured budgets unverified |
| SP05–09 daemon, store, DAG, observer, negative knowledge | Source mapped; focused publication and authorization findings block acceptance |
| SP10–12 checkpoints, rehydration, scheduler | Source mapped; installed compaction/current-authority recovery not verified |
| SP13 retrieval | `1.13.4` fails current authorization boundary; broader protocol/quality rows unverified |
| SP14–16 commands, selection, reuse | Source mapped; enabled consumer and held-out task evidence not rerun |
| SP17 packaging | Partial Windows CLI/install/diagnostic evidence; privacy/recovery and platform obligations incomplete |
| SP18 docs/UAT | Current docs/devtool checks pass; all twelve human UAT scenarios remain unverified |

Inventory symbol mapping still contains partial/unknown rows. Its guard-rename expectation is retired in favor of the current capability criterion; fixed counts, old performance guarantees and native eviction/veto assertions are not reinstated. The nine degradation obligations still require individual current-failure mappings. The integrated `Qompack.md` is v1.5 while the supplied planning baseline names v1.4; that discrepancy is recorded without rewriting either historical source or declaring a new authorized baseline.

| Exit obligation | Result |
|---|---|
| R2 focused run map, failures and candidate identities | Documented below; broader release obligations explicitly incomplete |
| Every original row and migration addition reconciled | IDs accounted for; unresolved mappings and runtime assertions remain incomplete |
| SP17-M7-01–08 / SP18-M7-01–07 / UAT-01–12 inspected on shipped package | **Not met**; no shipped V6 package or human UAT |
| Enabled M0–M6 target/failure/compatibility evidence | **Not met**; source/historical evidence cannot replace missing live host gates |
| Compatible readers, backup/frontier and pre/post-write rollback | Partial test-level rehearsal; supported operator flow and actual old-release matrix unverified |
| No unresolved mandatory privacy/fidelity/recovery/quality blocker | **Failed**; blockers below are not waived |

## 3. Cross-component integration identifiers

Original proposed test names are retained in the V6 plan and mapped in inventory §D; this table records their current execution disposition.

| ID | Result / artifact scope |
|---|---|
| 3.1 real packaged session | Unknown; CLI installation is not an interactive session |
| 3.2 launcher semantics | Partial Windows installed launcher evidence; full path/managed/competing-hook platform matrix not run |
| 3.3 documented commands | Docs/devtool checks and selected CLI diagnostics only; all commands on installed host not run |
| 3.4 stale/elimination MCP after upgrade | Unknown; no new full upgrade-survival scenario |
| 3.5 ephemeral eviction | Native assertion retired; future representation-policy consumer not evaluated |
| 3.6 fsck repair/preservation | Selected in-process repair tests pass; packaged stage-one audit preserves bytes and reports incomplete; automatic recovery still fails |
| 3.7 doctor/status agreement | Selected in-process checks pass; actual live capability evidence incomplete |
| 3.8 binary/config reference | Source-derived docs/devtool tests pass; not all installed-binary command semantics verified |
| 3.9 install/upgrade/uninstall | Windows disposable CLI scenarios pass; same-source stamped versions and fixture-gated rollback only |
| 3.10 degraded passive | Selected packaged unknown-schema cases pass; full degraded/privacy matrix not complete |
| 3.11 privacy/network | Focused redaction/telemetry controls pass; **authorization fails**, full live-session gate blocked |
| 3.12 checkpoint/rehydration | Test-driven rollback includes sealed checkpoint restoration; repeated/failed/missing live compaction contract unverified |
| 3.13 reproducible artifacts | Devtool assembler unit tests pass and local identity retained; supported-target artifact reproduction, licensing/name/tag checks not completed |
| 3.14 resident hot path | Not run; candidate fails prerequisites and runner was not quiet |

## 4. Commands, outcomes and bounded run allocation

Every structured run record under [runs](sdd/V6-VERIFY/runs/) contains actual argv, environment overrides, source/dirty state, start/end times, exit code and raw-log SHA256. The retained [recording helper](sdd/V6-VERIFY/run-recording.py) can reproduce this recording convention; it refuses to overwrite an existing run. The table separates command exit from capability verdict. A known-failure Go test returning zero does not pass a release gate. [All individual outcome artifacts](sdd/V6-VERIFY/artifact-outcomes.json) retain original failed/skipped records.

The final [evidence-integrity check](sdd/V6-VERIFY/evidence-integrity.json) found no missing inventory IDs, linked reports, run artifacts or log-hash mismatches. The staged Git bytes were also checked against all 15 log hashes, the saved forensic bundle identity and nine committed tested-source file hashes. Evidence-local attributes preserve raw log/identity bytes across line-ending conversion. These are integrity checks, not additional product passes.

| Run ID (JSON and same-name log) | Command outcome | Capability consequence / reason to run |
|---|---|---|
| [security-focused](sdd/V6-VERIFY/runs/security-focused.json) | Go pass | Four targeted security tests; out-of-project capture artifact **failed**; junction/lexical controls verified; leaf symlink skipped for missing privilege; credential/telemetry controls pass only in their scope |
| [security-regressions-original](sdd/V6-VERIFY/runs/security-regressions-original.json) | **Fail** | New outside-read ID and fabricated escaping-record root/chunk assertions expose failures; original evidence preserved |
| [security-real-capture](sdd/V6-VERIFY/runs/security-real-capture.json) | **Fail** | Stronger fixture captures through real hook, proves allowed ID access, then replaces parent with NTFS junction: ID denied, both hash forms return archived marker |
| [fault-publication-focused](sdd/V6-VERIFY/runs/fault-publication-focused.json) | Go pass | Both pinned known-failure artifacts remain **failed**: stage-one capture and object-before-index; automatic surfaces do not account for incomplete data |
| [diagnostics-focused](sdd/V6-VERIFY/runs/diagnostics-focused.json) | Pass | Seven selected doctor/fsck tests inspect seeded defects, additive repair, locking and evidence-qualified status; in-process scope |
| [install-recovery](sdd/V6-VERIFY/runs/install-recovery.json) | Pass | Three selected e2e tests exercise actual disposable host CLI install/upgrade/uninstall, test-gated rollback and unknown schemas; user settings/plugin fingerprint preserved |
| [fsck-packaged](sdd/V6-VERIFY/runs/fsck-packaged.json) | Fail, fixture error | Initial filename-order selection picked a deliberately unpublished prompt; not evidence of an fsck defect |
| [fsck-packaged-tool-capture](sdd/V6-VERIFY/runs/fsck-packaged-tool-capture.json) | Compile failure | Test used nonexistent `ObservationID.String`; fixed by string conversion; original log retained |
| [fsck-packaged-tool-capture-fixed](sdd/V6-VERIFY/runs/fsck-packaged-tool-capture-fixed.json) | Pass | Selects actual published `observe.tool` sidecar; packaged fsck exits 1, names stage-one defect and preserves objects/sidecars; artifact deliberately says explicit-incomplete, not recovered |
| [docs-integrated](sdd/V6-VERIFY/runs/docs-integrated.json) | Pass | Entire `test/docs` and `tools/devtool` packages once after doc integration corrections; static docs/UAT checks are not human UAT |
| [forensic-bundle](sdd/V6-VERIFY/runs/forensic-bundle.json) | Pass | One windows/amd64 build to retain the failing binary; not a release build matrix |
| [format-new-tests](sdd/V6-VERIFY/runs/format-new-tests.json), [format-new-tests-pinned](sdd/V6-VERIFY/runs/format-new-tests-pinned.json) | Tool invocation failures | `go tool` entry absent, then PowerShell split unquoted modfile argument; neither ran the formatter |
| [format-new-tests-pinned-quoted](sdd/V6-VERIFY/runs/format-new-tests-pinned-quoted.json) | Pass, no output | Repository-pinned formatter on the two new test files, corrected quoted argument |
| [lint-new-tests](sdd/V6-VERIFY/runs/lint-new-tests.json) | Pass, no diagnostics | Repository-pinned golangci-lint on `test/security` and `test/fault` to check the new regression code; 126 seconds, not a whole-tree release suite |

Short independent diagnostic and install scenarios used separate fixture/bundle/store/IPC roots. Authoring and inspection overlapped; no timings are presented as performance evidence. No per-row or per-reviewer whole-tree run was duplicated. Reruns were justified by changed fixtures, compile correction or integrated documentation changes and preserve their earlier failures.

**Deferred long gates:** whole-tree test/race/coverage, replay/rebaseline, supported-platform install matrix, full failure matrix, held-out model evaluations, resident benchmarks and human UAT. Reason: confirmed mandatory privacy/recovery prerequisites fail, so a broader run cannot establish readiness. They remain final obligations after compatible remediation. Linux/macOS runners were not provisioned. Current `test-race` excludes `test/e2e`; child bundle builds use ordinary non-race binaries. No V6 child-path race coverage is claimed.

Manifest validation through the real host CLI and installed launch are represented only by the Windows installation artifacts. License/dependency vulnerability refresh, current registry-name availability, release tag agreement, branch protections and publishing credentials were not checked. No network query or stale name assertion substitutes for those release-time checks.

## 5. Blocking findings, privacy and recovery

| Finding | Severity / reproduction | Owner / required next work |
|---|---|---|
| V6-AUTH-1: discarded file path becomes pathless authority | **Release blocker.** Real out-of-project Read is archived with empty path; expand by ID serves synthetic content. `security-regressions-original` and `security/posture_out_of_project_capture_is_archived.json` | SP20 observer/store + SP13 MCP: distinguish genuinely pathless captures from lost/denied file provenance; define compatible legacy/unknown behavior before implementation |
| V6-AUTH-2: hash addresses bypass current path check | **Release blocker.** Real permitted capture then directory-junction escape; ID refuses while root and chunk hashes each return 34 archived bytes (33-character marker plus newline). `security-real-capture` | SP13/SP20: preserve and resolve provenance for every root/chunk address and enforce the same authority on every retrieval form; retain legitimate pathless behavior |
| V6-RECOVERY-1: publication gaps lack automatic accounting | **Release blocker.** Two `fault-publication-focused` artifacts fail despite Go pass; manually invoked fsck detects the corrected stage-one tool fixture but is not automatic recovery | SP20 daemon/store + SP17 diagnostics: production gap discovery/accounting, durable acknowledgement/recovery and owner-qualified status; the current harness audit is not an existing production daemon service |
| V6-RECOVERY-2: operator backup/restore handoff incomplete | **Release blocker for the promised recovery workflow.** Fixture-gated engine rollback passed, but production `LegacyImportGate` is closed and no supported operator command performs that flow | SP20 migration + SP17/SP18: expose a gated, reviewed operator procedure with writer frontier, verified backup, compatible readers and explicit later-write retention |
| V6-HOST-1: current host denial policy unavailable | **Unverified mandatory trust boundary.** Source path check enforces project filesystem scope, not the host's current deny rules; live forbidden-archive-read UAT not performed | SP19 host capability + SP13/SP20: establish feasible policy handoff or explicitly unsupported affected retrieval scope; do not claim project containment equals host authorization |

These demonstrations re-serve previously captured synthetic bytes; they do not demonstrate reading uncaptured data. Redaction controls passing does not authorize archived disclosure. Documentation now states the failure honestly, but that correction is **not remediation or a waiver**. No production schema/authority redesign was improvised in this verification branch: shared trust contracts and compatible old-record behavior require their owning implementation changes and reviewed fixtures. Failing regressions remain enabled and intentionally red here.

Recording, reinjection, new-result replacement and experimental policies remain distinct. Current source defaults have reinjection enabled, raw-evidence/durable-frontier migration gates refused, admission replacement and experiments off, native veto unsupported/off. Disabled is never recorded as passed. No live combined kill-switch/privacy drill was performed, and no user configuration was changed. This report accepts **no new rollout scope**, including observation-only, while capture/privacy blockers remain unresolved.

Pre/post-new-format-write rollback was driven by the engine test harness with explicit fixture authorization, stopped writers, verified backups/readers and retained IDs. It does not prove historical released-reader compatibility or an operator CLI. Copying a stopped store and obtaining a clean fsck report is not a consistent-backup/frontier/reader proof. UAT-12 and troubleshooting now preserve that distinction. Later writes must be retained or explicitly accounted for before any downgrade; no automatic downgrade is promised.

## 6. Performance, accounting and three evaluation layers

No new acceptance threshold, sample margin or outcome is inferred from the old B-A–B-F/L0/L5 labels. No quiet timing distribution, repeated stochastic baseline, held-out changing-requirement trial or supported-platform p99 was run. Sample count for product quality/cost trials is **zero**; intervals and regression margins are **not estimable**, not zero loss. These protocols must be declared before the later trials.

| Layer | Current evidence |
|---|---|
| Deterministic/mechanism | Focused docs/devtool, diagnostics and packaged synthetic authorization tests; failures retained |
| Closed-loop/human/held-out tasks | Not run; no stock/current/corrected/admission comparison or harness-equivalence claim |
| Recovery/failure | Targeted publication failures, manual fsck evidence and test-driven install/rollback only; full recovery gate incomplete |

Primary completion, constraint/regression and recoverability outcomes therefore remain failed or unknown. Cost and correctness are independent; no token score can clear these failures. Assembled extra context, total context, estimated versus reported usage, cache categories/TTL, aborted trials, latency, storage and retrieval burden have no V6 product-trial samples. Historical synthetic fraction-of-OPT remains a diagnostic, not a task-quality ceiling.

The [request usage record](sdd/V6-VERIFY/request-usage.json) preserves child CLI input/output/cache categories and list-cost estimates where reported. Resumed counters are not summed because overlap is unresolved. Main usage, full retry/compaction attribution, rate-table date, non-token charges, invoice and subscription cash impact are unknown. These are authoring/review calls, not Qompack product calls or a cost-saving evaluation.

## 7. Regression preservation and human UAT

Waves 0–2 and the V3 report/addendum remain unchanged. The 2026-08-26 V3 waiver remains historical: J5 run 32932419445 and the three-platform p99 backfill stay waived-open, not newly certified. V5's carried hosted-fsync, Linux ACK/load and elevated ACL obligations remain scoped to their original evidence. SP17 historical coverage results do not become new V6 coverage.

SP05-D1 still belongs to SP20 drain/ack recovery; SP02-D1–D6 remain one V4 corpus/rebaseline unit; SP06-D2/SP08-D1 remain paired. SP04-D2/D3 and SP06-D1 wontfix rationales, SP04-D7's checkpoint consumer, SP11-C28 sibling frontier reconciliation and SP07 identity/generation fixture rulings are retained, not silently resolved. No frozen fixture, baseline, machine-readable carry or `CARRIED-DEFECTS.tsv` was regenerated. Source inventory is not proof these obligations passed.

| UAT IDs | Result |
|---|---|
| UAT-01, UAT-02, UAT-03, UAT-04 | `unknown` — human interaction not performed |
| UAT-05, UAT-06, UAT-07, UAT-08 | `unknown` — human interaction not performed |
| UAT-09, UAT-10, UAT-11, UAT-12 | `unknown` — human interaction not performed; forbidden-read and recovery prerequisites fail |

The coordinator asked whether the user would be available for disposable-session UAT after prerequisites pass; no participation is inferred from silence. The inventory/docs child inspected text only. All twelve result blocks in `docs/uat.md` remain unexecuted. Installed-host CLI use is real but does not constitute a model task or human UAT.

## 8. Independent review and next handoff

The non-authoring Opus 4.8 high [trust review](sdd/V6-VERIFY/trust-review.md) traced the authority and recovery concerns. Its original source-only hash concern now has executed real-capture evidence. The coordinator rejects a documentation-only resolution of mandatory authorization gates, and corrects its early suggestion that the harness audit was already a production daemon service. The [final independent review](sdd/V6-VERIFY/final-review.md) concurs with the **no-go decision** and finds no unsupported release acceptance. It withholds release sign-off.

Review findings were adjudicated as follows: the returned content length is corrected to **34 bytes**; preservation now carries the exact diff commands, outputs and explicit empty-diff interpretation. The reviewer counted 19 preserved files; the JSON contains **20** (17 dirty tracked files plus three historical reports). The evidence was untracked during review and is committed with this report, rather than having been committed before the reviewer read it. Missing integration terminal artifacts remain disclosed. The dirty-binary/source distinction and incomplete per-ID runtime/mapping assurance remain unchanged. These are report corrections, not product fixes or gate waivers.

Children had exclusive docs/inventory/report scopes, no nesting, at most three concurrently. Main owned integration, test additions, run allocation, shared trust decisions and commits. No permission/configuration/provider/authentication/billing changes were made to obtain the requested route. Coordinator-model identity remains the exception recorded at the top; child routing does not establish Fable as coordinator.

Next owner handoff: SP20/SP13 define and implement compatible provenance/authority behavior, production publication-gap accounting and supported recovery. SP19 supplies real host permission/capability evidence. After those focused tests pass, identify a new immutable bundle, complete SP17 platform/failure/race/packaging gates and SP18 human UAT, then run predeclared held-out quality/cost/performance trials. Reuse this requirement-to-artifact map, preserving failures and old/new source identities. Mandatory failures continue to block enablement/release; publication authorization remains separate.

**Final release gate:** enabled features have incomplete target evidence; representative recovery/privacy/compatibility gates have not passed; human UAT and independent final acceptance remain incomplete. No V6 completion or production-readiness claim is made.
