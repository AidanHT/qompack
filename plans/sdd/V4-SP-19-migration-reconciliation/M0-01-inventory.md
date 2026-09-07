# SP-19 M0-01 — repository reconciliation inventory

> Session record of SP-19's repository-auditor seat (read-only inventory, 2026-09-07, run against the accepted
> combined baseline `develop` @ 66549ce and the root planning worktree at 7f92af5 carrying the uncommitted
> planning revision). `plans/MIGRATION-EVIDENCE.md` § "M0-01 / M0-G1" is the summary and the coordinator's
> dispositions; this file is the evidence behind it. Requested route Opus 4.8 / medium; the Agent tool exposes
> only the `opus` alias with no effort control, so the effective route is recorded as "opus, effort not exposed".
> Section 6's blockers 1–2 are answered in the ledger section; 3 is fixed in the same commit (TRACEABILITY).

**Role:** SP-19 repository auditor ("After M0-G0: repository auditor"; plan requests Opus 4.8 / medium — the effective route for this run is whatever the harness supplied and is not exposed to me, so no routing claim is made).
**Mode:** READ-ONLY. No file inside any repository was created, edited, staged, committed, stashed or checked out. No `go test`/build/bench was run. No `git worktree` mutation was run.
**Finished:** 2026-09-07 ~14:55 local (host clock, `date` = `Mon, Sep 7, 2026 2:40:01 PM` at mid-run).

## HEADs observed

| Tree | Path | Branch | HEAD |
|---|---|---|---|
| Root (planning, dirty) | `C:\Users\Quant\Documents\Programming\Projects\qompack` | `verify/v3` | `7f92af5353a6bd084aa49043351e4183ecbcb2ad` |
| Develop (code, clean) | `C:\Users\Quant\Documents\Programming\Projects\qompack-develop` | `develop` | `66549ceffdebdf9491e15e42e6b647d4eaeb1250` |
| Remote refs (stale) | — | `origin/develop` | `907f983` (develop is **7 first-parent merges + N commits ahead, unpushed**) |
| Remote refs | — | `origin/verify/v3` | `7f92af5` |
| Remote refs | — | `origin/chore/post-v2-hardening` | `9408dbc` |

## Commands run (summarised)

`git worktree list [--porcelain]`, `git status --short` (root + 12 named worktrees), `git rev-parse HEAD`, `git log --first-parent --merges --oneline/--format`, `git log -1 --format`, `git branch --merged/--no-merged 66549ce`, `git branch -r`, `git stash list`, `git merge-base --is-ancestor <tip> 66549ce` (79 worktree tips + 10 nominated/named commits); `grep -rn -w` sweeps over `internal/ cmd/ test/ tools/ testdata/ docs/ plugin/ plans/ Qompack.md` in **both** trees for the 20 M0-04 tokens; `cat`/`sed -n` reads of the plan and ledger files named in the brief. Nothing else.

---

# 1. Reconciliation snapshot (M0-G1)

## 1(a) `git worktree list` — every entry, run in `qompack-develop`

**76 entries** (32 project worktrees + 44 Claude Code harness worktrees under `.claude/worktrees/`). "Ancestor" = `git merge-base --is-ancestor TIP 66549ce` returned 0.

### Project worktrees (siblings + root)

| # | Path | Branch / detached | HEAD | Scope | Ancestor of 66549ce | Dirty? |
|---|---|---|---|---|---|---|
| 1 | `…\qompack` | `verify/v3` | `7f92af5` | **user's root** — holds the uncommitted v1.4 planning revision | YES | YES (see 1b) |
| 2 | `…\qompack-develop` | `develop` | `66549ce` | **accepted combined baseline (M0-00)** | YES (self) | clean |
| 3 | `…\qompack-sp02` | `feat/sp02-replay-harness-belady-baseline` | `1881f4e` | wave-1 delivery worktree | YES | clean (not re-checked) |
| 4 | `…\qompack-sp03` | `feat/sp03-sketch-library` | `1711cc0` | wave-1 delivery worktree | YES | not checked |
| 5 | `…\qompack-sp05` | `feat/sp05-daemon-ipc-and-hot-path` | `18b8a8b` | wave-1 delivery worktree | YES | not checked |
| 6 | `…\qompack-sp06` | `feat/sp06-content-addressed-store` | `c3440cc` | wave-1 delivery worktree | YES | not checked |
| 7 | `…\qompack-sp07` | `feat/sp07-dependence-dag-and-slicing` | `22b0743` | wave-1 delivery worktree | YES | not checked |
| 8 | `…\qompack-sp08` | `feat/sp08-observer-l0` | `ef6a42c` | wave-2 delivery worktree | YES | not checked |
| 9 | `…\qompack-sp09` | `feat/sp09-negative-knowledge` | `66a0dfd` | wave-2 delivery worktree | YES | not checked |
| 10 | `…\qompack-sp10` | `feat/sp10-checkpointer-l4` | `a99cc15` | **wave-3 delivery merged at M0-00 step 1** | YES | clean |
| 11 | `…\qompack-sp11` | `feat/sp11-rehydrator-l5` | `a74a7f6` | **wave-3 delivery merged at M0-00 step 2** | YES | clean |
| 12 | `…\qompack-sp12` | `feat/sp12-scheduler-l3` | `1e767c3` | **wave-3 delivery merged at M0-00 step 3** | YES | clean |
| 13 | `…\qompack-sp13` | `feat/sp13-mcp-retrieval-layer` | `4ad5048` | **wave-3 delivery merged at M0-00 step 4** | YES | clean |
| 14 | `…\qompack-sp10-a` | detached | `71c46a2` | SP-10 seat scratch (V3 checkpoint commit) | YES | not checked |
| 15 | `…\qompack-sp10-b` | detached | `71c46a2` | SP-10 seat scratch | YES | not checked |
| 16 | `…\qompack-sp10-c` | detached | `71c46a2` | SP-10 seat scratch | YES | not checked |
| 17 | `…\qompack-sp10-d` | detached | `71c46a2` | SP-10 seat scratch | YES | not checked |
| 18 | `…\qompack-sp10-e` | detached | `71c46a2` | SP-10 seat scratch | YES | not checked |
| 19 | `…\qompack-sp10-f` | detached | `71c46a2` | SP-10 seat scratch | YES | not checked |
| 20 | `…\qompack-sp10-g` | detached | `71c46a2` | SP-10 seat scratch | YES | not checked |
| 21 | `…\qompack-sp12-A0` | detached | `71c46a2` | SP-12 seat scratch | YES | not checked |
| 22 | `…\qompack-sp12-A1` | detached | `71c46a2` | SP-12 seat scratch | YES | not checked |
| 23 | `…\qompack-sp12-A2` | detached | `71c46a2` | SP-12 seat scratch | YES | not checked |
| 24 | `…\qompack-sp12-A3` | detached | `71c46a2` | SP-12 seat scratch | YES | not checked |
| 25 | `…\qompack-sp12-B1` | detached | `71c46a2` | SP-12 seat scratch | YES | not checked |
| 26 | `…\qompack-sp12-base` | detached | `ca791d6` | SP-12 seat scratch | **NO** | **DIRTY** — 15 modified (`internal/scheduler/*`, `internal/daemon/scheduler_*`, `test/replay/l3policy/*`, `docs/adr/0012`) |
| 27 | `…\qompack-sp12-C` | detached | `3e234bf` | SP-12 seat scratch | **NO** | **DIRTY** — 3 modified + 12 untracked (`internal/daemon/scheduler_*`, `test/e2e/scheduler_idle_test.go`) |
| 28 | `…\qompack-sp12-C1fix` | detached | `6ff9552` | SP-12 seat scratch | **NO** | **DIRTY** — 4 modified (`internal/daemon/scheduler_runtime*.go`, `scheduler_state.go`, `scheduler_tap_test.go`) |
| 29 | `…\qompack-sp12-D` | detached | `a5033a5` | SP-12 seat scratch | **NO** | **DIRTY** — 2 modified + 5 untracked (`test/replay/*`, `internal/scheduler/bench_test.go`, `docs/adr/0012`) |
| 30 | `…\qompack-sp19` | `feat/sp19-migration-reconciliation` | `66549ce` | **post-M0-G0 SP-19 implementation worktree (already cut)** | YES | clean |
| 31 | `…\qompack-sp19-eval` | `wip/sp19-eval` | `66549ce` | post-M0-G0 SP-19 accounting/baseline seat | YES | clean |
| 32 | `…\qompack-sp19-host` | `wip/sp19-host` | `66549ce` | post-M0-G0 SP-19 host-contract seat | YES | clean |

Rows 26–29 are the only worktree tips in the repository that are **not** ancestors of 66549ce. Their commits are SP-12 development scratch predating `1e767c3`; the SP-12 delivery that was merged is `1e767c3`, which *is* an ancestor. The M0-00 handoff explicitly records "The SP-12 seat worktrees, SP-10 a–g worktrees and the stash list were left intact" (`plans/MIGRATION-EVIDENCE.md:348`).

Rows 30–32 were **not** named in my brief and are a state change since the M0-00 handoff was written: the handoff's "Next" bullet says *"If accepted: cut `feat/sp19-migration-reconciliation` (`../qompack-sp19`) from 66549ce"* (`plans/MIGRATION-EVIDENCE.md:351`). All three exist, are clean, and sit exactly at 66549ce — i.e. the branch was cut but carries no commits yet. `wip/sp19-eval` and `wip/sp19-host` are not named in any plan I read; unclear: whether they are the plan's "baseline owner" and "host tester" seats, since no plan or ledger text names those branch spellings.

### Harness worktrees under `…\qompack\.claude\worktrees\`

All 44 are Claude Code agent/workflow scratch, not project deliveries. All 44 tips are ancestors of 66549ce. Dirt not checked (out of scope).

| Path suffix | Branch | HEAD | Ancestor |
|---|---|---|---|
| `agent-a05041eec08176e55` | `fix/a2-degraded-budget` | `ea56c76` | YES |
| `agent-a0704385b7aaff202` | `worktree-agent-a0704385b7aaff202` | `96ccfcb` | YES |
| `agent-a2e8eb40f51d48502` | `worktree-agent-a2e8eb40f51d48502` | `30a8c14` | YES |
| `agent-a3c3d2c3cd50311b2` | `worktree-agent-a3c3d2c3cd50311b2` | `53d480c` | YES |
| `agent-a43a9b33a4fffca1a` | `docs/v2-report-close` | `21c56f6` | YES |
| `agent-a556baf9dff32f2fa` | `fix/a4-wallclock-gates` | `fc36bc0` | YES |
| `agent-a926f9fbb95374399` | `fix/a3-shared-readers` | `8d1938e` | YES |
| `agent-a9886c1d480b86d5e` | `fix/final-wave` | `e943b23` | YES |
| `agent-a9e586f67557a174a` | `fix/a1-connect-deadline` | `b33dc91` | YES |
| `agent-aaf9f17e8b7c829eb` | `worktree-agent-aaf9f17e8b7c829eb` | `7f92af5` | YES |
| `agent-ac6ea21062acbba43` | `worktree-agent-ac6ea21062acbba43` | `96ccfcb` | YES |
| `agent-aec15a12b89231c33` | `fix/a5-lint-windows` | `0b1247b` | YES |
| `agent-aecda1c03e9d64fb0` | `worktree-agent-aecda1c03e9d64fb0` | `7f92af5` | YES |
| `agent-af1b520d3fc1f5967` | `worktree-agent-af1b520d3fc1f5967` | `7f92af5` | YES — **locked** |
| `agent-afdd3f2e9ce340240` | `worktree-agent-afdd3f2e9ce340240` | `1aa1589` | YES |
| `wf_59704d06-5c3-1 … -12` (12 worktrees) | `worktree-wf_59704d06-5c3-<n>` | `9e3363d` (all) | YES |
| `wf_b785fb2c-98f-1 … -4` (4) | `worktree-wf_b785fb2c-98f-<n>` | `96ccfcb` | YES |
| `wf_b785fb2c-98f-5` | `worktree-wf_b785fb2c-98f-5` | `bc44d2a` | YES |
| `wf_bd5a2ffd-d5c-1,-2,-3,-4,-5,-6,-7,-11` (8) | `worktree-wf_bd5a2ffd-d5c-<n>` | `2a5c31c` | YES |
| `wf_e701cde0-fa8-1 … -4` (4) | `worktree-wf_e701cde0-fa8-<n>` | `2a5c31c` | YES |

### Nominated delivery tips vs the ledger

Every tip nominated in `plans/MIGRATION-EVIDENCE.md:325` and every integration commit named in its step table is an ancestor of 66549ce:

| Commit | Subject (first 70 chars) | Ancestor of 66549ce |
|---|---|---|
| `f22534e` | feat(daemon): wire PreCompact and idle frontier advancement | YES |
| `8f88379` | docs(rehydrate): ADR 0011 on item order, budget shares, whole-rule res… | YES |
| `3280927` | test(scheduler): add Phase 4 replay harness, benchmarks and the L3 ADR | YES |
| `21e481e` | test(mcp): B-F latency gate, stdio e2e suite, and generated docs/mcp-t… | YES |
| `a99cc15` | chore(sp10): list SP-10 as landed in the three registries | YES |
| `a74a7f6` | chore(sp11): merge develop and resolve the wave-3 conflicts | YES |
| `1e767c3` | chore(sp12): merge develop and resolve the wave-3 conflicts | YES |
| `4ad5048` | fix(cli): build SP-13's wave-3 collaborators in the daemon bootstrap | YES |
| `907f983` | docs(adr): 0010, a wall-clock budget is judged where judgeable (pre-merge base) | YES |
| `71c46a2` | chore(verify): V3 — wave 2 verification checkpoint | YES |

Shared stash list: 9 entries, untouched (`stash@{0}`–`{4}` on `feat/sp13-…` @ `4837b9d`; `{5}`,`{6}` detached on `fe50f30`; `{7}` on `fix/post-audit-code-round` @ `778d11a`; `{8}` on `worktree-wf_bd5a2ffd-d5c-4`).

## 1(b) Root worktree dirty files (`git -C <root> status --short`)

**Class A — v1.4 planning revision, tracked, modified (17 files):**

| File | Class |
|---|---|
| `Qompack.md` | design revision (authorized v1.4 exception per `plans/README.md:37`) |
| `plans/00-ARCHITECTURE.md` | v1.4 planning revision |
| `plans/QOMPACK-ERRATA.md` | v1.4 planning revision |
| `plans/README.md` | v1.4 planning revision |
| `plans/TRACEABILITY.md` | v1.4 planning revision |
| `plans/V4-SP-10-checkpointer-l4.md` | v1.4 planning revision |
| `plans/V4-SP-11-rehydrator-l5.md` | v1.4 planning revision |
| `plans/V4-SP-12-scheduler-l3.md` | v1.4 planning revision |
| `plans/V4-SP-13-mcp-retrieval-layer.md` | v1.4 planning revision |
| `plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md` | v1.4 planning revision |
| `plans/V5-SP-14-slash-commands-and-observability.md` | v1.4 planning revision |
| `plans/V5-SP-15-analyzer-selection-and-grammar.md` | v1.4 planning revision |
| `plans/V5-SP-16-phase7-refinements.md` | v1.4 planning revision |
| `plans/V5-VERIFY-commands-selection-grammar-and-refinements.md` | v1.4 planning revision |
| `plans/V6-SP-17-packaging-hardening-and-release.md` | v1.4 planning revision |
| `plans/V6-SP-18-documentation-and-uat.md` | v1.4 planning revision |
| `plans/V6-VERIFY-production-readiness-and-uat.md` | v1.4 planning revision |

**Class B — new planning files, untracked (4):**
`plans/MIGRATION-EVIDENCE.md`, `plans/V4-SP-19-migration-reconciliation.md`, `plans/V4-SP-20-capture-storage-and-state-remediation.md`, `plans/V5-SP-21-deterministic-admission-control.md`.

**Class C — protected artifacts, untracked (6 entries):**
`cover-full.txt`, `cover-full2.txt`, `internal/sketch/testdata/`, `v3-bench.txt`, `v3-benchstat.txt`, `v3-hotpath.json`.
These are exactly the six the ledger declares protected at `plans/MIGRATION-EVIDENCE.md:7` ("Initial status contains only untracked `cover-full.txt`, `cover-full2.txt`, `internal/sketch/testdata/`, `v3-bench.txt`, `v3-benchstat.txt`, and `v3-hotpath.json`. These are protected."). **The root baseline is unchanged from the ledger's recorded baseline**: same HEAD `7f92af5`, same six protected artifacts, plus the 17+4 planning files the revision itself produced.

Size/shape note on Class A (material for M0-01, not a recommendation): the four wave-3 root copies are complete rewrites, far shorter than the delivered plans on `develop`.

| Plan | root (v1.4) lines | develop (as-delivered) lines |
|---|---|---|
| `V4-SP-10-checkpointer-l4.md` | 274 | 1599 |
| `V4-SP-11-rehydrator-l5.md` | 176 | 2119 |
| `V4-SP-12-scheduler-l3.md` | 145 | 3517 |
| `V4-SP-13-mcp-retrieval-layer.md` | 158 | 1480 |
| `V4-VERIFY-…-retrieval.md` | 188 | 1838 |

## 1(c) Local branches: merged into 66549ce vs not

79 local branches. **One** is not merged:

| Branch | Tip | Date | Subject |
|---|---|---|---|
| `fix/post-audit-code-round` | `0b6319f43f80b0bf1d6f3e4a0a01220727d6747b` | 2026-08-25 | `docs(plans): close five gaps a self-audit found in the V3 schedule` |

All 78 others are merged (`git branch --merged 66549ce`). Named (non-`worktree-*`) merged branches and their tips:

`arch/coverage-exempt-composition-roots 1f93df0` · `arch/daemon-options-pointer-receiver e4fe3f7` · `arch/rehydrate-item-kinds 8faff81` · `arch/sp08-observer-seams bc257ee` · `chore/post-v2-hardening 9408dbc` · `develop 66549ce` · `docs/plan-audit 817bdf3` · `docs/v2-report-close 21c56f6` · `feat/sp01-… 8f20b18` · `feat/sp02-… 1881f4e` · `feat/sp03-… 1711cc0` · `feat/sp04-… b597ea8` · `feat/sp05-… 18b8a8b` · `feat/sp06-… c3440cc` · `feat/sp07-… 22b0743` · `feat/sp08-… ef6a42c` · `feat/sp09-… 66a0dfd` · `feat/sp10-… a99cc15` · `feat/sp11-… a74a7f6` · `feat/sp12-… 1e767c3` · `feat/sp13-… 4ad5048` · `feat/sp19-migration-reconciliation 66549ce` · `fix/a1-connect-deadline b33dc91` · `fix/a2-degraded-budget ea56c76` · `fix/a3-shared-readers 8d1938e` · `fix/a4-wallclock-gates fc36bc0` · `fix/a5-lint-windows 0b1247b` · `fix/final-wave e943b23` · `main e7dd4ad` · `verify/v1 e09eae0` · `verify/v2 7eeecb7` · `verify/v3 7f92af5` · `wip/sp19-eval 66549ce` · `wip/sp19-host 66549ce`.

The remaining 44 merged branches are harness `worktree-*` refs listed in 1(a).

Note: `main` is at `e7dd4ad` (`chore(hardening): merge the post-v2 hardening round`) — 13 first-parent merges behind `develop`.

## 1(d) Per-subplan status, merge commit, owner, disposition

Merge commits are `git log --first-parent --merges 66549ce`. "Owner" combines `plans/OWNERS.tsv` (package ownership) and the plan headers.

### SP-01 … SP-13 (landed)

| SP | Status on 66549ce | Merge commit (first-parent) | Packages owned (`plans/OWNERS.tsv`) | Owner per plan header | Disposition |
|---|---|---|---|---|---|
| SP-01 | landed (wave 0 / V1) | `690cedac5c59a743e67f0c5a13982a29d95855e9` `feat(sp01): merge wave-0 foundation, toolchain and contracts` | core, paths, config, logging, obs, tokens, hookio, cli, pluginmanifest, testutil, cmd/qompack (`OWNERS.tsv:23-33`) | root header (develop copy) recommends Opus 5 | **retained** |
| SP-02 | landed (wave 1 / V2) | `9e054de79a673b992b10d90b964a3f810a0442ac` | eval, floor 85 (`OWNERS.tsv:34`) | — | **retained**; SP02-D1–D6 deferred to V4-VERIFY |
| SP-03 | landed | `c441a6776604fdd918498256c9ac190318a11c1e` | sketch, floor 90 (`:35`) | — | **retained** |
| SP-04 | landed | `532ffbe8a9bf0fc22fdbafe6a47ed483f3a9b224` | chunk, canon, symbols (`:36-38`) | — | **retained**; D1/D4/D5/D6/D7 fixed, D2/D3 wontfix |
| SP-05 | landed | `ecea37a40b90be326cd740b82ddbab60ca83fa7b` | ipc, daemon, contract (`:39-41`) | — | **retained**; SP05-D1 deferred:V4-VERIFY |
| SP-06 | landed | `148c8aa20c56c262ab8117b49a97db7b1395942e` | store, redact (`:42-43`) | — | **retained**; SP06-D1 wontfix, SP06-D2 deferred:V4-VERIFY |
| SP-07 | landed | `939097673e8efbc576618d09af76aa94856267ae` | dag, floor 85 (`:44`) | — | **retained**; five rulings in `V2-SP07-handoff.md` (see §3) |
| SP-08 | landed (wave 2 / V3) | `b1a65fae047b9c937d451c171bc2b130a662eeeb` | observer (`:45`) | — | **retained**; SP08-D1 deferred:V4-VERIFY |
| SP-09 | landed | `5170dc0847f9ffa935565e3166a958a1b2781db8` | negknow (`:46`) | — | **retained** |
| SP-10 | landed (wave 3) | `875c8d81aad93836fc9ee2a4c0c88553a605b5ab` `feat(checkpoint): merge SP-10, the L4 checkpointer` | checkpoint, pins (`:47-48`) | root v1.4 header: "Writer B using gpt-5.6-terra at medium" (planning owner); implementation owner future | **retained at M0-00**; integration commit added on the incoming branch: `a99cc15` |
| SP-11 | landed | `729957a331d6c37e01b295a1fdb564278f48d49a` | rehydrate, rules, skills (`:49-51`) | root v1.4: Writer B / gpt-5.6-terra medium | **retained at M0-00**; integration commit `a74a7f6` (merge tree = 729957a) |
| SP-12 | landed | `6853256a89f94e0fa0d855a9cc0a8de52e418a4d` | scheduler (`:52`) | root v1.4: writer C / gpt-5.6-terra medium | **retained at M0-00**; integration commit `1e767c3`; includes the daemon `idleTaskSchedAdvanceFrontier`/`idlePrioSchedAdvanceFrontier` rename (14 occurrences) |
| SP-13 | landed | `66549ceffdebdf9491e15e42e6b647d4eaeb1250` `feat(mcp): merge SP-13, the L6 retrieval layer (wave 3 merged)` | mcp (`:53`) | root v1.4: writer A / gpt-5.6-terra medium | **retained at M0-00**; three integration commits: `a5abd74`, `db02a32` (`fix(mcp): register SP-13 as landed and refuse nil streams in Serve`), `4ad5048` (`fix(cli): build SP-13's wave-3 collaborators in the daemon bootstrap`) |

Other first-parent merges on the same history (not subplan deliveries): `4f98314` V1 checkpoint, `9c9c75a` V2 checkpoint, `71c46a2` V3 checkpoint, `1466b73`/`351278d`/`d5d40dc`/`f4572c2` architecture amendments, `c0c5627`/`33bb206`/`6ca496f` audit rounds, `e7dd4ad` post-v2 hardening.

**No checkbox was reset by me, and none could be**: `grep -c '\[x\]'` returns **0** for every subplan file in both trees (checked `V4-SP-10/11/12/13`, `V3-SP-08`, `V2-SP-02` on develop). The repository does not use checked boxes to record completion; completion is recorded by the merge commit plus `plans/V<K>-report.md` plus `plans/sdd/`. Both trees' wave-3 plans carry only `- [ ]` rows (root: 21/20/14/15; develop: 69/80/99/57).

### SP-14 … SP-21 (future)

| SP | Owner (packages / plan) | Wave | Prerequisite chain as stated in the root v1.4 plan header |
|---|---|---|---|
| SP-14 slash commands + observability | `commands` floor 75 (`OWNERS.tsv:54`) | 4 / V5 | verified V4, SP-19 accounting/capability contract, SP-20 state, SP-10–13 recovery; **SP-15 → SP-16 integrate before this frontend** |
| SP-15 analyzer selection + grammar | `analyzer` 85, `grammar` 75 (`:55-56`); planning owner writer C / gpt-5.6-terra medium | 4 / V5 | SP-01/06/07/08 preserved; SP-19, SP-20/M1–M2, SP-13/M2, SP-10/11 M3, SP-12 supported scheduling |
| SP-16 phase-7 refinements | no new package row; writer C / gpt-5.6-terra medium | 4 / V5 | SP-01/03/06/09 preserved; SP-19, SP-20/M1–M2, SP-13/M2, SP-10/11 M3, SP-12/SP-15 M5 |
| SP-17 packaging + release | no new package row | 5 / V6 | V5 and applicable M0–M6 gates, SP-19 installed capability register, SP-20 migration/backup contract; parallel with SP-18 on disjoint files |
| SP-18 documentation + UAT | no new package row | 5 / V6 | verified V5, SP-17 installed artifact, SP-14 command contract, SP-19 capability/accounting register; integrates **after** SP-17 |
| **SP-19 migration reconciliation** | future repository auditor / host tester / baseline owner / independent reviewer (`V4-SP-19…md:140-157`) | 3 remediation / V4 | **M0-00 → M0-G0 first**; then M0-01–05, `arch/migration-contracts`, commits 1–8 |
| SP-20 capture/storage/state remediation | writer A / gpt-5.6-terra medium | 3 remediation / V4 | M1/M2; opens after SP-19's own prerequisites |
| SP-21 deterministic admission control | Writer B / gpt-5.6-terra medium | 4 / V5 extension after M3 | SP-19/M0 target inventory, SP-20/M1 capture+publication+identity, SP-20/SP-13 M2 authorization/recovery, SP-10/SP-11 M3 lifecycle. **Default state off.** |

`plans/OWNERS.tsv` has **no rows for SP-19, SP-20 or SP-21** — correct, since neither has landed a package; `commands`, `analyzer`, `grammar` are the only pre-registered future packages.

---

# 2. Phase mapping

## 2(a) SP-19 M0-01's stated mapping (`plans/V4-SP-19-migration-reconciliation.md:83`)

| Qompack.md original phase | Subplans / verify group per SP-19 M0-01 |
|---|---|
| Phase 0 measurement | SP-02 / V2 |
| Phase 1 store + observer | SP-04, SP-06 / V2 **plus** SP-08 / V3 |
| Phase 2 negative knowledge | SP-03, SP-09 — with SP-11 and SP-13 as consumers |
| Phase 3 checkpoint / rehydrate | SP-10, SP-11 / V4 |
| Phase 4 scheduler | SP-12 / V4 |
| Phases 5 + 6 selection / grammar | SP-15 / V5, with completed SP-07 |
| Phase 7 refinements | SP-16 / V5 |
| Production | V6 (SP-17 → SP-18) |

## 2(b) Cross-check against root `plans/README.md` and root `plans/TRACEABILITY.md`

| Source | Location | Wording | Agrees with SP-19 M0-01? |
|---|---|---|---|
| `plans/README.md` | `plans/README.md:23` | "Phase0→SP02/V2; Phase1→SP04/06/V2 plus SP08/V3; Phase2→SP03/09 with SP11/13 consumers; Phase3→SP10/11/V4; Phase4→SP12/V4; Phases5/6→SP15/V5 with completed SP07; Phase7→SP16/V5. Phase numbers are not waves." | **YES — verbatim identical** (SP-19 writes "consumption", README writes "consumers"; same claim) |
| `plans/TRACEABILITY.md` §2 | `plans/TRACEABILITY.md:56` (phase 0) | "SP02, Wave1/V2, completed" | YES |
| | `:57` (phase 1) | "SP04/SP06 Wave1/V2, SP08 Wave2/V3, completed" | YES |
| | `:58` (phase 2) | "SP03/SP09 completed; SP11/SP13 active consumers" | YES |
| | `:59` (phase 3) | "SP10/SP11 Wave3/V4, **in progress**" | **NO — status disagreement.** README `plans/README.md:19` calls wave 3 "User-completed SP-10–13"; SP-19 §3 (`V4-SP-19…md:3`) says "The user now reports original Wave 3 SP-10–13 complete"; and on `develop` all four are merged (§1d). TRACEABILITY still says "in progress". |
| | `:60` (phase 4) | "SP12 Wave3/V4, **in progress**" | **NO — same disagreement** |
| | `:61` (phase 5) | "SP07 completed graph, SP15 Wave4/V5" | YES |
| | `:62` (phase 6) | "SP15 Wave4/V5" | YES |
| | `:63` (phase 7) | "SP16 Wave4/V5" | YES |
| | `:64` (production) | "SP17→SP18 Wave5/V6" | YES |
| | `:65` | "Added remediation/extension: SP19/SP20 within V4; SP21 within V5. Logical M packages do not renumber waves." | consistent; SP-19 M0-01 does not restate this |
| `plans/README.md` execution schedule | `plans/README.md:19` | Wave 3 row: "User-completed SP-10–13 → SP-19 M0-00 merge prerequisite → remaining SP-19 → SP-20" | consistent with SP-19 §M0-00 |
| `plans/TRACEABILITY.md` §3 (layers) | `:69` | "L3 is **active** SP12; L4 **active** SP10; L5 **active** SP11; L6 **active** SP13" | **NO — same "active/in-progress" residue**; all four are merged on 66549ce |

**Disagreements found: exactly two claims, at `plans/TRACEABILITY.md:59`, `:60` and `:69`** — all three are the same "Wave 3 in progress / active" status wording, which the README (`:19`), SP-19 (`:3`, `:19`) and the merged history contradict. Everything else in the phase table matches.

---

# 3. Carry reconciliation (M0-05)

## 3(a) `plans/CARRIED-DEFECTS.tsv` — every row (develop tree, `plans/CARRIED-DEFECTS.tsv:32-49`)

| tsv line | id | opened_by | owner | status | evidence test named | Test exists in develop tree? (`func NAME`) | M0-05 disposition per `V4-SP-19…md:103` |
|---|---|---|---|---|---|---|---|
| 32 | SP04-D1 | SP-04 | V2-VERIFY | fixed | `TestCarriedDefect_SP04D1_EscapedTempPathIsStripped` | YES — `internal/canon/carried_defects_test.go:67` | **no M0-05 instruction** (not in the plan's list; status already `fixed`) |
| 33 | SP04-D2 | SP-04 | V2-VERIFY | wontfix | `TestKnownDeletionMediatedLimit` | YES — `internal/canon/golden_test.go:309` | **not reopened silently**; SP-20 adds fidelity/GC requirements *beside* the historical limitation |
| 34 | SP04-D3 | SP-04 | V2-VERIFY | wontfix | `TestCarriedDefect_SP04D3_TimestampAndDurationEdges` | YES — `internal/canon/carried_defects_test.go:90` | **not reopened silently** (same clause as SP04-D2) |
| 35 | SP04-D4 | SP-04 | V2-VERIFY | fixed | `TestNightlyFuzz_LandedSubplansMirrorsCoverGo` | YES — `test/guards/nightlyfuzz_test.go:147` | **no M0-05 instruction** |
| 36 | SP04-D5 | SP-04 | V2-VERIFY | fixed | `-` (no runtime symptom) | n/a | **no M0-05 instruction** |
| 37 | SP04-D6 | SP-04 | V2-VERIFY | fixed | `-` | n/a | **no M0-05 instruction** |
| 38 | SP06-D1 | SP-06 | V2-VERIFY | wontfix | `-` | n/a | **not reopened silently**; SP-20 adds fidelity/GC requirements beside it |
| 39 | SP05-D1 | SP-05 | V2-VERIFY | **deferred:V4-VERIFY** | `-` | n/a | **→ SP-20 durable drain/ack remediation** |
| 40 | SP02-D1 | SP-02 | V2-VERIFY | **deferred:V4-VERIFY** | `-` | n/a | **one V4 baseline/corpus unit with D2–D6** |
| 41 | SP02-D2 | SP-02 | V2-VERIFY | deferred:V4-VERIFY | `-` | n/a | same unit |
| 42 | SP02-D3 | SP-02 | V2-VERIFY | deferred:V4-VERIFY | `-` | n/a | same unit |
| 43 | SP02-D4 | SP-02 | V2-VERIFY | deferred:V4-VERIFY | `-` | n/a | same unit (this is the `belady.pMin` row; code at `internal/eval/belady.go:154`) |
| 44 | SP02-D5 | SP-02 | V2-VERIFY | deferred:V4-VERIFY | `-` | n/a | same unit |
| 45 | SP02-D6 | SP-02 | V2-VERIFY | deferred:V4-VERIFY | `-` | n/a | same unit |
| 46 | SP04-D7 | SP-04 | V2-VERIFY | fixed | `-` | n/a | **not reopened silently** |
| 47 | SP06-D2 | SP-06 | V2-VERIFY | **deferred:V4-VERIFY** | `-` | n/a | **paired performance/novelty decision with SP08-D1** |
| 48 | SP08-D1 | SP-08 | V3-VERIFY | **deferred:V4-VERIFY** | `BenchmarkOnToolUse_TestOutput256KB` | YES — `internal/observer/tooluse_test.go:608` | **paired with SP06-D2** |
| 49 | SP10-D1 | SP-10 | V4-VERIFY | **open** | `BenchmarkFinalize` | YES — `internal/checkpoint/finalize_test.go:340` | **no M0-05 instruction.** Detail doc `plans/V2-SP-10-carried-defects.md:1-40` records 264–284 ms/op vs a 50 ms criterion; the M0-00 handoff cites SP10-D1 as one reason C-1 ledger wiring was deferred (`plans/MIGRATION-EVIDENCE.md:336`) |

Every named evidence test exists. No TSV edit is proposed; `test/guards/carrieddefects_test.go` remains the enforcing guard.

Two additional inherited failures recorded at M0-00 that are **not** TSV rows and have no id: `internal/negknow TestBudget_Open` (`internal/negknow/bench_test.go:829`, 421.9 ms vs 300 ms, inherited from 907f983) and `internal/mcp TestBudgetBF` (`internal/mcp/bench_test.go:77`, 426 ms wall p95 vs 250 ms under race+co-load; ADR 0010 never applied to it). Owner recorded as SP-13 → V4-VERIFY (`plans/MIGRATION-EVIDENCE.md:344`).

## 3(b) SP-11-C28 — `frontierOf` → real `Ref.Frontier`

**What C28 refers to.** C28 is an id from the wave-2+ scope audit, not from `CARRIED-DEFECTS.tsv`. Three records define it, all in the **develop** tree (the root v1.4 rewrites dropped the detail; see below):

- `plans/WAVE2-PLUS-SCOPE-AUDIT.md:315` — classification `obligation-added`: "`frontierOf(s, at)` is newly defined inside the replay driver as the last user-role turn at or before the compaction point, replacing reliance on `Ref.Frontier` from checkpoint goldens … **See design conflict below.**"
- `plans/WAVE2-PLUS-SCOPE-AUDIT.md:331` — the conflict is graded **soft**: "a local modelling choice inside the replay driver, not a redefinition of the shipped `Ref.Frontier`. The plan says the V4 verification run 'replaces `frontierOf` with the real `Ref.Frontier` then' — but no handoff to SP-12 and no row in `plans/V4-VERIFY-*` records that obligation."
- `plans/WAVE2-PLUS-SCOPE-AUDIT.md:848` and `:1103` — the prescribed remedy is one line: "`SP-11-C28` needs only a handoff line: record in `plans/V4-VERIFY-…md` that `frontierOf` is replaced by the real `Ref.Frontier` at the V4 run."

**Does the delivered code resolve it? Partly — the documentation half is discharged; the code substitution is not, and is not supposed to be yet.**

*Documentation half — discharged.* The handoff line the audit demanded now exists on `develop`:
- `plans/V4-SP-11-rehydrator-l5.md:1867` (develop copy) — "**`frontierOf(s, at)` is defined here, deterministically, and does not wait on SP-12.** … The V4 verification run re-executes this file against SP-10's and SP-12's real implementations and replaces `frontierOf` with the real `Ref.Frontier` then. **That substitution is already written down on the other side of the handoff**".
- `plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md:560` and `:570-574` (develop copy) — A3's row states `F` is "SP-11's `frontierOf(s, at)` **replaced at V4 by the real `Ref.Frontier`**", with a "*What changes at V4*" paragraph instructing V4 to derive the frontier from `.qompack/state/precompact.json`.
- Root v1.4 copy: `plans/V4-VERIFY-checkpoint-rehydrate-schedule-retrieval.md:161` — "Reconcile SP11-C28 frontierOf/Ref.Frontier against the active sibling."

*Code half — still open (by design).* `frontierOf` is alive and unmodified in the replay driver on 66549ce:

| Location | What it is |
|---|---|
| `test/replay/policy_rehydrate.go:246` | `func frontierOf(s eval.Session, at core.TurnIndex) core.TurnIndex` — **definition** |
| `test/replay/policy_rehydrate.go:233` | doc comment: "frontierOf is the index of the last user turn at or before at" |
| `test/replay/policy_rehydrate.go:157` | consumer: `Ref: checkpoint.Ref{Seq: checkpointSeqForReplay, Frontier: frontierOf(s, at)}` |
| `test/replay/policy_rehydrate.go:219` | consumer: `f := frontierOf(s, at)` |
| `test/replay/phase3_rehydrate_test.go:223-234` | six assertions pinning `frontierOf`'s semantics, including `core.TurnIndex(-1)` for a session with no user turn |

The real writer-side value it must be swapped for is `checkpoint.Ref.Frontier`, defined at `internal/checkpoint/source.go:140-141` ("Frontier is the turn index this checkpoint advanced the encoding frontier to"), produced only by `Finalize` (`internal/checkpoint/writer.go:317`, `:669`) and **deliberately zero on every reader-returned Ref** — `internal/checkpoint/reader.go:62`, `:85`, `:305` ("Ref.Tokens and Ref.Frontier are writer-only and stay zero"), pinned by `internal/checkpoint/reader_test.go:150` (`require.Equal(t, core.TurnIndex(0), r.Frontier, "Frontier is writer-only")`).

An unrelated same-named helper exists at `internal/daemon/scheduler_frontier_test.go:76` (`frontierOf(fx *rtFixture)` — reads the scheduler runtime frontier under lock). It is **not** the C28 symbol.

**Status for V4:** C28's documentation obligation is met on develop; the substitution itself is scheduled V4-VERIFY work and remains unperformed. Unclear: the root v1.4 `V4-SP-11-rehydrator-l5.md` (176 lines) no longer contains the `:1867` paragraph — `grep -n 'frontierOf' plans/V4-SP-11-rehydrator-l5.md` in the root tree returns nothing. Whether the v1.4 revision intends the develop text to remain authoritative for that obligation, or intends `V4-VERIFY:161` alone to carry it, I cannot determine from the files.

## 3(c) SP-07 NodeID / generation / fixture rulings for V4 to verify

From `plans/V2-SP07-handoff.md` §B ("Deviations forced by SP-01's frozen fixtures" — "The frozen fixtures won.", `:89-92`). Five rulings; each with the code location that embodies it on 66549ce:

| Ruling | Handoff location | The ruling | Code embodying it (file:line) |
|---|---|---|---|
| **V2-SP07-03** — NodeID is **378** bytes, not 376; the long prefixes are contractual | `plans/V2-SP07-handoff.md:96-113` | `5 ("file:") + 360 (head) + 1 ("~") + 12 (Hash.Short) = 378`; "**Read the row as 378.**" | `internal/dag/nodeid.go:73` `var nodeKindPrefixes = [...]string{…}` (the one prefix table; inverse built in `init()` at `:92-95`); the head/cap constants at `internal/dag/nodeid.go:51-54` (`maxKeyBytes = 384`, `keyHeadBytes = 360`); separator `keyHashSep = "~"` at `:35`; hash domain at `:40`. Assertion: `internal/dag/nodeid_test.go:129-131` (`require.Len(t, string(id), 378)`), inside `TestNodeIDLongKeyHashSuffix` (`internal/dag/nodeid_test.go:122`). The long-form rationale is restated at `internal/dag/nodeid.go:18-21`. |
| **V2-SP07-12 / -13** — the generation record is `"type":"generation"`, not a `g` record | `:115-127` | "`gen:1` is literally there. Only the discriminator token differs. **Read `g` as `"type":"generation"`.**" | Producer: `internal/dag/compact.go:121-133` (`materializeLocked` writes the header first; error string "encode generation header"), record accounting at `:187`. Wire doc at `internal/dag/wire.go:25`. Assertions: `internal/dag/compact_test.go:86-88` (`require.Equal(t, "generation", header["type"]…)`, `gen`=1, `v`=1), `:114` (survives restart), `:265` (a cancelled compaction produces no generation). Golden writer note at `internal/dag/golden_test.go:32`, `:186`. |
| **V2-SP07-02** — kinds round-trip via `String()`/`Parse*`, **not** `encoding.TextMarshaler`; `KindInvalid`/`EdgeInvalid` sit last in their iota blocks | `:128-140` | Adding `MarshalText`/`UnmarshalText` breaks both frozen fixtures because kinds are pinned as integers (`"kind":4`) | Standing prohibition: `internal/dag/kinds.go:12-18` ("Deliberately absent, and it must stay that way: MarshalText and UnmarshalText … `TestFrozenFixtureKindNumberingUnchanged` fails loudly if anyone adds the TextMarshaler pair"); duplicated at `internal/dag/doc.go:32-34` and `internal/dag/wire.go:32`. Round-trip surface: `internal/dag/kinds.go:123` `func (k NodeKind) String()`, `:133` `func ParseNodeKind`, `:151` `func ParseEdgeKind`; names table `:26-39`; sentinel handling `:106`, `:136`. Guard assertions: `internal/dag/kinds_test.go:181-202`. |
| **V2-SP07-11** — `thin-vs-full.json` carries **no** `ns_thin`/`ns_full`; the timing claim is asserted in the test, and the measurement must stay batched (20×) | `:141-163` | "**Read the row as: the assertion is in the test, not the golden.**" plus the Windows clock-granularity caveat: "If you touch this measurement, keep the batch." | Golden: `testdata/golden/contracts/dag/thin-vs-full.json` (size/recall/precision only). Assertion: `TestThinVsFullComparison` at `internal/dag/slice_compare_test.go:200`. |
| **V2-SP07-05** — the concurrency test runs **250** iterations, not 2 000 | `:164-177` | "**Read the row as 250**, or move throughput-under-contention to a benchmark" | `internal/dag/graph_test.go:357` — `const writers, readers, iterations = 8, 8, 250`, inside `TestConcurrentMutationAndRead` (`internal/dag/graph_test.go:356`) |

Section C of the same handoff records all three ACTION items **CLOSED** (`ACTION 1` at `:185` closed by `14a9668`; `ACTION 2` at `:210` closed by V2-MERGE-14; `ACTION 3` at `:222` closed as superseded), plus one `INHERIT` (`:239` — "the DAG is not acyclic, and SP-09 must tolerate it") and one `NOTE` (`:259` — wall-clock gates are scaled under instrumentation). No TSV edit proposed.

---

# 4. M0-04 consumer locations

Method: `grep -rn -w <token>` over `internal/ cmd/ test/ tools/ testdata/ docs/ plugin/ .github/` in `qompack-develop` @ 66549ce, and over `plans/*.md` + `Qompack.md` in the **root** tree @ 7f92af5 (the v1.4 planning revision). Marks: **D** = definition, **C** = consumer (source), **F** = fixture / testdata / generated doc, **P** = plan mention. `_test.go` files are consumers unless they hold the only definition.

## 4.1 `YoungDaly` (config + scheduler + eval)

**D** `internal/scheduler/youngdaly.go:15` — `func YoungDaly(deltaSeconds, mtbfSeconds float64) float64` (§6.7 / Appendix A, `I* = √(2·δ·M)`; doc at `:10`).
**D** `internal/config/config.go:82` — `type YoungDalyCfg struct`; field at `internal/config/config.go:75` `YoungDaly YoungDalyCfg \`json:"youngDaly"\``.
**D** `internal/scheduler/types.go:51` — `TriggerYoungDaly TriggerReason = "young_daly"` (doc `:47-49`); **D** `internal/scheduler/types.go:291` `YoungDalySeconds float64` (doc `:290`).

**C (source):** `internal/config/defaults.go:40` · `internal/scheduler/evaluate.go:85`, `:90`, `:91` (`Breakdown["young_daly_disabled"]`), `:93` (`…_delta_unmeasured`), `:95` (`…_no_baseline`), `:97` (`interval = YoungDaly(delta, m)`), `:100`, `:105`, `:131` (`d.Reasons = append(…, TriggerYoungDaly)`), `:237` (`Breakdown["young_daly_seconds"]`), `:257` · `internal/scheduler/youngdaly.go:40`, `:43`, `:48`, `:49` (delta-selection precedence) · `internal/scheduler/types.go:11`, `:18`, `:212`.

**C (tests / conformance):** `internal/scheduler/youngdaly_test.go:10,13,14,16,17,18,19,20,41,53,71,82,89,103,108,112` · `internal/scheduler/evaluate_test.go:90,91,177,190` · `internal/scheduler/schedulertest/behaviour.go:13,74,113,117,125` · `internal/config/load_test.go:81,163,180` · `internal/config/load_kinds_test.go:42,43,102,163` · `internal/config/config_test.go:44` · `internal/daemon/scheduler_state_test.go` (young_daly, 1 hit) · **`test/guards/stubs_test.go:470`** (stub probe row).

**F (config metadata / goldens):** `testdata/golden/config/appendix-c.jsonc:15` — `"youngDaly": { "enabled": true, "measuredDeltaSeconds": null }` · `testdata/golden/config/schema.json:767`, `:798` · `docs/config-reference.md:115` (`scheduler.youngDaly.enabled`, boolean, default `true`), `:116` (`scheduler.youngDaly.measuredDeltaSeconds`, number-or-null, default `null`) · `docs/adr/0012-scheduler-l3.md` (3 hits).
Config golden tests that gate those bytes: **`internal/config/defaults_test.go:18-23`** `TestDefaults_MatchesAppendixCVerbatim` (reads `../../testdata/golden/config/appendix-c.jsonc`) and **`internal/config/schema_test.go:140`** — `schemaNodeAt(t, schema, "scheduler.youngDaly.measuredDeltaSeconds")` (the named leaf), with the golden path constant at `schema_test.go:37`.

**In `internal/eval`:** *no `YoungDaly` hit at all.* The plan's "config + scheduler + eval" grouping is not reflected in the tree; `internal/eval` neither defines nor consumes it.

**P (root plans @ 7f92af5):** `plans/00-ARCHITECTURE.md:38,1657,2736` · `plans/V1-SP-01-…md:941,1394,1405,1589,1977,2153,2208` · `plans/V1-VERIFY-…md:261,1014` · `plans/V2-report.md:174` · `plans/V2-VERIFY-…md:1462` · `plans/V4-SP-12-scheduler-l3.md:11` · `plans/V4-SP-19-migration-reconciliation.md:97` · `plans/WAVE2-PLUS-SCOPE-AUDIT.md:353,359,923`. `young_daly` (snake): `plans/00-ARCHITECTURE.md:1584`, `plans/WAVE2-PLUS-SCOPE-AUDIT.md:364`.
(For contrast, the develop copy of `plans/V4-SP-12-scheduler-l3.md` carries 20 `YoungDaly` + 10 `young_daly` hits at `:17,379,676,679,855,857,1342,1354,1358,1380,1388,1389,1917,1924,3022,3242,3248,3336,3369,3507`; the v1.4 root rewrite reduces it to `:11`.)

## 4.2 `SkiRentalShouldWrite`

**D** `internal/scheduler/skirental.go:20` — `func SkiRentalShouldWrite(expectedReads, r, w float64) bool` (package doc `:3`).
**C (tests):** `internal/scheduler/skirental_test.go:17` (`func TestSkiRentalShouldWrite`), `:18,19,20,22,23,24,25,32,33,34,41,42,43` · `internal/scheduler/cacheregime_test.go:115,116,117,118` · **`test/guards/stubs_test.go:471`** (stub probe row).
**No production caller.** `grep -rn -w SkiRentalShouldWrite --include='*.go'` outside `internal/scheduler` and `test/guards` returns nothing — it is a pure function exercised only by its own tests, the cache-regime tests, and the stub guard.
**F:** `docs/adr/0012-scheduler-l3.md` (2 hits) · `testdata/corpora/toolout/git/git-diff.txt` + `testdata/golden/canon/git/git-diff.txt.canon.txt` (1 each — incidental corpus text, not a contract).
**P (root):** `plans/00-ARCHITECTURE.md:1662,2736` · `plans/V1-SP-01-…md:1394,1409,1978,2153,2208` · `plans/V1-VERIFY-…md:118,262` · `plans/V2-report.md:1495,1597` · `plans/V4-SP-12-scheduler-l3.md:11` · `plans/V4-SP-19-…md:97` · `plans/WAVE2-PLUS-SCOPE-AUDIT.md:353,359,590,594,616,911,923,1065,1099`.

## 4.3 Rewrite cost — `RewriteTokens`, `rewrite_tokens`, `rewrite_span_tokens`, `rewriteCost`

`rewriteCost` and `PSelection`/`PMin`: **zero hits anywhere in either tree.** (The concept exists under other spellings; see 4.4.)

**D** `internal/eval/types.go:166` — `RewriteTokens int \`json:"rewriteTokens"\`` (doc `:165`: "the total rewrite cost this policy incurred (Σ w·(n − p_min))"); `:114` documents `§5.2's cost = w·(n − p_min)`.
**D** `internal/checkpoint/types.go:170-171` — the checkpoint-side `RewriteTokens` / `rewrite_tokens` field.
**D (metric keys)** `internal/eval/score.go:352` `"rewrite_span_tokens"`, `:353` `"rewrite_tokens"` (the frozen metric-name list).

**C (source):** `internal/eval/score.go:77` (rounding rationale), `:86` (`RewriteTokens: int(math.Round(w * float64(rewriteSpan)))`), `:320` (`out.RewriteTokens += s.RewriteTokens`), `:393-394` (derivation doc: "`rewrite_span_tokens` and `forfeited_discount_tokens` are derived from `Score.RewriteTokens` using w and r, which are config keys and never literals (D11, §11.6)"), `:404` (`span = float64(s.RewriteTokens) / w`), `:421` (`"rewrite_span_tokens": span`), `:422` (`"rewrite_tokens": float64(s.RewriteTokens)`) · `internal/scheduler/evaluate.go:184` (`rewrite_tokens` breakdown key).

**C (tests):** `internal/eval/score_test.go:126,135,136,151,152,154,167,168,172,184,185,188,197,198,287,288,392` · `internal/scheduler/evaluate_test.go:4,447` · `internal/checkpoint/truncate_test.go:163,216` · `internal/checkpoint/draft_test.go:80` · `internal/checkpoint/fixture_test.go:107` · `internal/checkpoint/checkpointtest/behaviour.go:414`.

**C (replay harness — explicitly in the audit scope):**
- **`test/replay/main.go:558`** — the report header row: `"policy", "fraction_of_opt", "retrieval", "rewrite_tokens", "pause_p95"`.
- **`test/replay/main.go:563`** — `m["rewrite_tokens"], m["compaction_pause_ms_p95"]`.
- `test/replay/gate_test.go:38,40,46,73,75,85,87,104,110,112` — the replay gate reads `rewrite_tokens`.
- `test/replay/phase4_test.go:36,60,313,356,559,560,563,569,570` — `RewriteTokens`.
- **`test/replay/phases.go` — no hit** for any rewrite-cost token (or for any other M0-04 token; see 4.14).

**F (fixtures / goldens):** `testdata/baseline/phase0.json:25,26,46,47,67,68` (both `rewrite_span_tokens` and `rewrite_tokens`, three sessions) · `testdata/golden/checkpoints/0001-minimal.json:39`, `0002-full.json:234`, `0003-truncated.json:253`, `0004-tier1-over-budget.json:113` · `testdata/golden/contracts/checkpoint/want/0001.json:120`, `…/input/oversized_checkpoint.json:234` · `testdata/golden/scheduler/decision-cold.json:43`, `decision-expiring.json:43`, `decision-warm.json:43` · `docs/adr/0002-replay-methodology.md:146,156,157,219,286` · `docs/adr/0012-scheduler-l3.md:212,370`.

**P (root):** `RewriteTokens` — `plans/00-ARCHITECTURE.md:1733,2029` · `plans/V2-SP-02-…md:350,476,809,814,829,830`. `rewrite_tokens` — `plans/00-ARCHITECTURE.md:1733` · `plans/V1-SP-01-…md:225` · `plans/V2-report.md:88,489` · `plans/V2-SP02-handoff.md:144` · `plans/V2-SP-02-…md:809,823,829,1192,1193,1194,1252,1253,1254,1518` · `plans/V2-VERIFY-…md:341,369` · `plans/V3-VERIFY-…md:283`. `rewrite_span_tokens` — `plans/V2-report.md:488` · `plans/V2-SP-02-carried-defects.md:65` · `plans/V2-SP-02-…md:475,809,823,830,831,1192,1193` · `plans/V3-VERIFY-…md:95,283,513` · `plans/WAVE2-PLUS-SCOPE-AUDIT.md:957`. `plans/CARRIED-DEFECTS.tsv:42` names `rewrite_span_tokens` (SP02-D3 row).

## 4.4 p-selection — `PSelection`, `pMin`, `p_min`, `PMin`

`PSelection` and `PMin`: **zero hits** in either tree (the develop copy of `plans/V4-SP-12-scheduler-l3.md` mentions `PSelectionAvailable()` in its recommended-model banner, but no such symbol exists in Go on 66549ce — `grep -rn 'PSelectionAvailable' --include='*.go'` is empty). Unclear: whether the plan banner names a symbol that was renamed or never shipped.

**D** `internal/eval/belady.go:154` — `func pMin(cands []optItem, kept map[string]bool, blocks []Block) int` (doc `:144-153`, including the SP02-D4 rationale: "Measured over the candidates, p_min answers…").
**C (source):** `internal/eval/belady.go:32` (`pos int // position in the original prefix, for p_min`), `:141` (`P: pMin(cands, kept, blocks)`), `:149`, `:150`, `:171` (`cost = w·(n − p_min)`) · `internal/eval/types.go:114`, `:165`, `:385` · `internal/eval/blocks.go:154` · `internal/eval/policy.go:260` · `internal/eval/breakpoint.go:87`.
**C (tests):** `internal/eval/belady_test.go:24,109,110,124` · `internal/eval/policy_test.go:74` · `internal/eval/score_test.go:126,188` · `test/integration/dagselection_test.go:51,263,266` · `test/replay/phase3_rehydrate_test.go:198` · `test/replay/policy_rehydrate.go:171`.
**F:** `docs/adr/0002-replay-methodology.md` (3 hits).
**P (root):** `p_min` — `plans/00-ARCHITECTURE.md:2029` · `plans/QOMPACK-ERRATA.md:43` · `plans/V2-report.md:75,1588` · `plans/V2-SP02-handoff.md:145,146` · `plans/V2-SP-02-…md:116,174,179,551,554,612,754,809,1192,1193` · `plans/V2-VERIFY-…md:341,356` · `plans/V3-VERIFY-…md:280`. `pMin` — `plans/V2-report.md:962` · `plans/V2-SP-02-carried-defects.md:79,81,128` · `plans/V3-report.md:259` · `plans/V3-VERIFY-…md:96,468` · `plans/WAVE2-PLUS-SCOPE-AUDIT.md:957` · **`plans/CARRIED-DEFECTS.tsv:43`** (SP02-D4: "belady.pMin iterates candidates where 5.2 defines it over all blocks").

## 4.5 `FocusInstructions`

**D** `internal/checkpoint/focus.go:108` — `func FocusInstructions(c Checkpoint, ref Ref, o FocusOptions) string` (doc `:80-107`); `FocusOptions` at `:11`; `Frontier` field at `:18-20`; `maxFocusBytes` cap at `:75`.
**C (source, single production caller):** `internal/checkpoint/precompact.go:193` — `instr := FocusInstructions(cp, ref, FocusOptions{…})`. Also `internal/checkpoint/focus.go:50`, `:110` (`if o.IncrementalSpan && o.Frontier > 0`), `:124` (paragraph-boundary truncation).
**C (tests):** `internal/checkpoint/focus_test.go` (13 hits) · `test/e2e/checkpoint_test.go` (1).
**F:** `testdata/corpora/toolout/grep/grep-many.txt:306` and `testdata/golden/canon/grep/grep-many.txt.canon.txt:306` — both carry the literal stub signature `internal/checkpoint/focus.go:33:func FocusInstructions(c Checkpoint, ref Ref, o FocusOptions) string { return "" }` **frozen inside a corpus fixture**. This is a stale wave-0 stub signature captured as test input; changing the function will not change the fixture, and the fixture cannot be regenerated without regenerating the corpus. Flag for M0-04.
**P (root):** `plans/00-ARCHITECTURE.md:1797,1799` · `plans/V1-SP-01-…md:296` · `plans/V2-SP-05-…md:249` · `plans/V4-SP-10-checkpointer-l4.md:21` · `plans/V4-SP-19-…md:97`.

## 4.6 `CustomInstructions` / `custom_instructions`

**D** `internal/hookio/output.go:25` — `CustomInstructions string \`json:"customInstructions,omitempty"\`` inside `HSO` (doc `:24`: "PreCompact's focus-instruction channel (Qompack.md §8.5)").
**D (constructor)** `internal/hookio/output.go:58-59` — `func PreCompactOutput(instr string) Output`.
**C (source):** `internal/daemon/handlers.go:723` (`if out.HookSpecificOutput != nil && out.HookSpecificOutput.CustomInstructions != ""`), `:724` (`h.SetPrecompactInstr(…)`), doc `:666` · `internal/checkpoint/focus.go:75`, `:84` · `internal/contract/assertions.go:225,228` (`const desc = "custom_instructions accepted"`) · `internal/contract/standard.go:27` (`gated(CPreCompactCustomInstr, SevWarn, "custom_instructions accepted", checkPreCompactCustomInstr)`) · `internal/contract/history.go:25` (`maxPrecompactInstrChars`), `:111` (`PrecompactInstr`) · `internal/contract/contracttest/behaviour.go:81`.
**C (tests):** `internal/hookio/output_test.go:44,50,69` · `internal/ipc/wire_test.go` (2) · `internal/daemon/daemon_test.go` (1) · `internal/observer/observertest/behaviour.go:132` (asserts the observer never emits one) · `internal/contract/assertions_test.go` (2), `internal/contract/standard_test.go` (1) · `internal/checkpoint/focus_test.go` (2) · `test/e2e/checkpoint_test.go:493`, `test/e2e/checkpoint_degraded_test.go:119` ("hookSpecificOutput at all — and therefore no customInstructions").
**F:** `testdata/golden/contracts/contract/want/result_set.json:1` (`custom_instructions`).
**P (root):** `CustomInstructions` — `plans/00-ARCHITECTURE.md:838` · `plans/V2-SP-05-…md:334,1314,1338` · `plans/V3-SP-08-…md:310` · `plans/V4-SP-19-…md:97` · `plans/WAVE2-PLUS-SCOPE-AUDIT.md:241`. `custom_instructions` — `plans/MIGRATION-EVIDENCE.md:68` (E10) · `plans/QOMPACK-ERRATA.md:165` · `plans/V1-SP-01-…md:86,276,1511` · `plans/V2-SP-05-…md:65` · `plans/V4-SP-10-checkpointer-l4.md:45` · `plans/V4-VERIFY-…md:130` · `Qompack.md:311`.

## 4.7 `BloomOnly` / `bloom_only`

**D** `internal/negknow/answer.go:31` — `BloomOnly bool` (doc `:27-30`).
**C (source):** `internal/negknow/ledger.go:807`, `:826` (`return Answer{State: AnswerAbsent, BloomOnly: true}, nil`), rationale `:31`, `:566`, `:772` ("There is no fourth path, and that is §13 invariant 3") · `internal/negknow/doc.go:44`, `:50` · `internal/mcp/handlers.go:136`, `:168` (`if ans.BloomOnly`), `:204` (`if !ans.BloomOnly`) · `internal/sketch/bloom.go:87`, `:224`.
**C (conformance / tests):** `internal/negknow/negknowtest/behaviour.go:14,113,115,118,132,152,153,174,326,370,371,372,379` · `internal/negknow/ledger_test.go:362` (`func TestQuery_BloomOnly`), `:385` (`func TestQuery_UnknownStatusIsBloomOnly`) + 6 more · `internal/negknow/negknow_test.go` (6) · `internal/negknow/bloom_test.go` (1), `bench_test.go` (1) · `internal/mcp/handlers_test.go` (1), `fake_test.go` (1) · `test/e2e/v3_x02_test.go` (2).
**F:** `testdata/golden/contracts/negknow/want/three_way_answer.json` (3 × `bloom_only`) · `docs/adr/0009-negative-knowledge-bloom-as-cache.md:106` (quotes `ledger.go:804-808`), `:2 more`.
**P (root):** `plans/00-ARCHITECTURE.md:1476` · `plans/V1-report.md:109` · `plans/V1-SP-01-…md:1586` · `plans/V1-VERIFY-…md:505` · `plans/V3-SP-09-…md:19,21,176,484,1052,1098,1323,1445,1451,1453,1454,1467,1508,1645,1783` · `plans/V3-VERIFY-…md:699,816,826` · `plans/V4-SP-19-…md:97`.

## 4.8 `AnswerAbsent`

**D** `internal/negknow/answer.go:10` — `AnswerAbsent AnswerState = iota` (doc `:4`, `:9`).
**C (source):** `internal/negknow/ledger.go:790`, `:794`, `:807`, `:826`, `:832` (five return sites), rationale `:284`, `:565`, `:771` · `internal/mcp/handlers.go:174` (`if ans.Record == nil || ans.State == negknow.AnswerAbsent`), `:195` (`case negknow.AnswerAbsent:`), doc `:136` · `internal/negknow/answer.go:21`.
**C (tests):** `internal/negknow/negknowtest/behaviour.go:27,44,131,206,270,326,368` · `internal/negknow/ledger_test.go` (10) · `internal/negknow/bloom_test.go` (2), `bench_test.go` (2) · `internal/mcp/handlers_test.go` (1) · `test/replay/phase2_negknow_test.go` (1) · `test/e2e/v3_x02_test.go` (1), `test/e2e/negknow_test.go` (1).
**F:** `docs/adr/0009-…md:106`, `:119`.
**P (root):** `plans/00-ARCHITECTURE.md:1470` · `plans/V3-report.md:196` · `plans/V3-SP-09-…md:19,483,1049,1052,1091,1092,1098,1106,1166,1352,1445,1449,1451,1453,1454,1456,1467,1508` · `plans/V4-SP-19-…md:97`.

## 4.9 `Ephemeral` (273 `Ephemeral` + 386 `ephemeral` hits; definitions and production consumers below, tests summarised by file)

**Definitions (five independent `Ephemeral` fields + one config key):**

| Location | Declaration |
|---|---|
| `internal/store/tooluse.go:83` | `Ephemeral bool \`json:"ephemeral"\`` on `ToolUseRecord` (doc `:53`) |
| `internal/store/types.go:41` | `Ephemeral` on the stored-content options |
| `internal/dag/node.go:73` | `Ephemeral bool \`json:"ephemeral"\`` on `dag.Node` (wire shape doc `:86`, `internal/dag/wire.go:25`) |
| `internal/analyzer/types.go:23` | `Ephemeral` on `analyzer.Block` ("born ephemeral: the first eviction candidate") |
| `internal/eval/types.go:405` | `Ephemeral bool \`json:"ephemeral,omitempty"\`` |
| `internal/mcp/types.go:26-27` | `Ephemeral` on the tool descriptor; `:77` on the response (`_meta.qompack.ephemeral`) |
| `internal/daemon/mcpop.go:58` | `Ephemeral bool \`json:"ephemeral"\`` on the MCP op wire type |
| `internal/config/config.go:169` | `EphemeralResults bool \`json:"ephemeralResults" doc:"tag every retrieval result ephemeral at birth so it is the first eviction candidate" sec:"§8.7"\`` |

**Production consumers:** `internal/mcp/handlers_common.go:39` (`ephemeralTools` — "the seven of eight whose results are born ephemeral"), `:61` (`metaEphemeral = "ephemeral"`), `:174`, `:183` (`ephemeral := ephemeralTools[name] && h.cfg.Retrieval.EphemeralResults`), `:198`, `:199`, `:231` (`tagEphemeral`), `:249` · `internal/mcp/ephemeral.go:13,31,52,53,64,73,95,121` (`recordEphemeral`, counters `mcp.ephemeral.recorded` / `.failed`) · `internal/mcp/tools.go:202,235,270,310,318,326` · `internal/mcp/server.go:47,51,484` · `internal/mcp/handlers.go:227` · `internal/mcp/doc.go:17` · `internal/observer/tooluse.go:75` (`ephemeral := strings.HasPrefix(e.ToolName, mcpToolPrefix)`), `:100`, `:114`, `:128`, `:242` · `internal/observer/tombstone.go:35` (`tombstoneEphemeral = "ephemeral"`), `:78`, `:169`, `:202` · `internal/observer/supersede.go:88` · `internal/scheduler/dropclass.go:26` (EvictionRank: "ephemeral (3) > superseded (2) > ordinary (1)"), `:53`, `:57` (`func DropClassOf(tool string, ephemeral, superseded bool) DropClass`), `:58` · `internal/store/gcrun.go:320,321,324` (the §8.2 ephemeral exclusion) · `internal/checkpoint/writer.go:772` · `internal/cli/mcpwire.go:83` · `internal/daemon/mcpop.go:204,232`.

**Generated docs:** `tools/devtool/genmcpdocs.go:108,109,112,275-278` (`func ephemeralNote(ephemeral bool) string`) → **F** `docs/mcp-tools.md` (16 hits).

**Tests / conformance (by file, count):** `internal/mcp/mcptest/behaviour.go` 13 · `internal/mcp/ephemeral_test.go` 10 (incl. `TestRecordEliminatedIsNotTaggedEphemeral:325`) · `internal/mcp/tools_test.go` 6 (incl. `TestOnlyRecordEliminatedIsNotEphemeral:261`) · `internal/mcp/handlers_test.go` 4 (incl. `TestRecordEliminatedIsNotEphemeral:502`) · `internal/mcp/fixture_test.go` 2 · `internal/store/gc_test.go` 11 (incl. helper `gcSeedEphemeral:33`) · `internal/store/put_test.go` 2 · `internal/observer/tombstone_test.go` 6 · `internal/observer/tooluse_test.go` 3 (incl. `TestOnToolUse_MCPResultIsEphemeral:150`) · `internal/checkpoint/writer_test.go` 5 · `internal/daemon/mcpop_test.go` 4 · `internal/daemon/scheduler_droppable_test.go` 3 · `internal/scheduler/dropclass_test.go` 3 · `internal/dag/log_test.go` 2 · `test/e2e/v3_x09_test.go` 11 · `test/e2e/mcp_e2e_test.go` 3.

**F (fixtures):** `testdata/golden/mcp/tools-list.json` (6) · `testdata/golden/contracts/dag/graph-basic.jsonl` (12) · `testdata/golden/observer/tombstones.txt` (2) · `internal/dag/testdata/deps-torn.jsonl` (2), `deps-corrupt.jsonl` (2) · `testdata/golden/config/appendix-c.jsonc:40` (`"ephemeralResults": true`) · `testdata/golden/config/schema.json:210,212,226` · `docs/config-reference.md:52` (`retrieval.ephemeralResults`, boolean, default `true`, §8.7).

**P (root):** `plans/00-ARCHITECTURE.md:1155,1201,1335,1537,1902,1914,1967` · `plans/V2-SP-02-…md:427` · `plans/V2-SP-06-…md:654,665,980` · `plans/V2-SP-07-…md:218,342,580,865,866,998` · `plans/V2-VERIFY-…md:569` · `plans/V3-SP-08-…md:331,335,349,371,677,678,853,912,1030,1045,1164,1174,1237,1312,1452,2000,2044` · `plans/V3-SP-09-…md:395,1234` · `plans/V4-SP-19-…md:97`. (The develop copy of `plans/V4-SP-13-mcp-retrieval-layer.md` has 32 hits; the root v1.4 rewrite has none.)

## 4.10 `Truncate`

**D** `internal/checkpoint/truncate.go:98` — `func Truncate(c Checkpoint, budget core.Tokens, t config.TiersCfg, est tokens.Estimator) (Checkpoint, []DropEntry)` (contract doc `:70-97`; "Truncate never mutates c").
**C (source, single production caller):** `internal/checkpoint/finalize.go:71` — `cp, tdrops = Truncate(cp, budget, w.cfg.Checkpoint.Tiers, src.Tokens)` (rationale `:64`). Internal cut helpers: `internal/checkpoint/truncate.go:11,12,51,79,84,88,93,96,117,135,141,192,196,211,220,235,244,260,292,302,317,333`. Documentation references: `internal/checkpoint/checkpoint.go:24` · `internal/checkpoint/doc.go:21` · `internal/checkpoint/draft.go:23` · `internal/checkpoint/obs.go:15` (§5.14 fixes `ExtractDecisions, Truncate, ValidatePointers`) · `internal/checkpoint/schema.go:59,60` · `internal/checkpoint/types.go:30` · `internal/checkpoint/writer.go:110,921,949` · `internal/checkpoint/focus.go:124` (unrelated string truncation) · `internal/negknow/classify.go:77` (unrelated token truncation).
**C (conformance):** `internal/checkpoint/checkpointtest/suite.go:70` (`type TruncateFunc`), `:74`, `:158` (`RunTruncateSuite`), `:168`, `:176`, `:253` (`isStubTruncate`), `:255`, `:262`, `:271`, `:272` (`skipIfStubTruncate`) · `internal/checkpoint/checkpointtest/behaviour.go:29,311` (`runTruncatePurityCase`), `:322`, `:325`, `:353`.
**C (tests):** `internal/checkpoint/truncate_test.go` 27 (incl. `BenchmarkTruncate:833`) · `internal/checkpoint/source_test.go` 2, `schema_test.go` 2, `finalize_test.go` 1, `obs_test.go` 1, `gitindex_test.go` 1, `checkpointtest/suite_test.go` 1 · `test/replay/policy_rehydrate.go:182` ("Both are tier 1, which Truncate is forbidden to touch").
**F:** `testdata/golden/contracts/checkpoint/MANIFEST.json` (1) · `testdata/corpora/toolout/grep/grep-many.txt` + `testdata/golden/canon/grep/grep-many.txt.canon.txt` (1 each, incidental).
**P (root):** `plans/00-ARCHITECTURE.md:1778,1779` · `plans/V1-SP-01-…md:296,1590` · `plans/V2-SP-03-…md:1236` · `plans/V3-SP-09-…md:290,676` · `plans/V4-SP-19-…md:97` · `plans/WAVE2-PLUS-SCOPE-AUDIT.md:573`. (The develop copy of `plans/V4-SP-10-checkpointer-l4.md` has 28 hits; the root rewrite has none.)

## 4.11 `MarkEncoded`

**D (interface)** `internal/store/segment.go:52` — `MarkEncoded(ctx context.Context, ids []core.SegmentID, seq core.CheckpointSeq) error` (doc `:49-51`: "the DPI guard (00-ARCHITECTURE.md §4.6, §8.2): it returns `ErrAlreadyEncoded`…").
**D (implementation)** `internal/store/segments.go:414` — `func (l *segLog) MarkEncoded(…)` (doc `:407`, mutex rationale `:76`, append-only rationale `:16`).
**C (source):** `internal/checkpoint/writer.go:657` (`if err := src.Segments.MarkEncoded(ctx, encodable, d.seq); err != nil`), rationale `:180`, `:574`, `:579`, `:659`, `:822`, `:823`, `:841` · `internal/checkpoint/finalize.go:185` (`if err := src.Segments.MarkEncoded(ctx, ids, seq)`), doc `:168` ("MarkEncoded is deliberately ONE-WAY") · `internal/checkpoint/source.go:29` · `internal/checkpoint/checkpoint.go:19` · `internal/daemon/scheduler_frontier.go:228` ("a segment that MarkEncoded refused is one the log already…").
**C (conformance):** `internal/store/storetest/behaviour.go:16,135` (`runMarkEncodedDPIGuardCase`), `:149,152,162,169` · `internal/store/storetest/suite.go:121` · `internal/checkpoint/checkpointtest/behaviour.go:101`.
**C (test doubles implementing the interface):** `internal/checkpoint/precompact_test.go:254` (`countingSegments`) · `internal/checkpoint/writer_paths_test.go:176` (`flakyMarkSegments`) · `internal/daemon/scheduler_testhelpers_test.go:308` (`fakeSegmentLog`) · `internal/negknow/fakestore_test.go:245` · `internal/rehydrate/fake_test.go:231` · `internal/store/storetest/suite_test.go:113`.
**C (tests, by file):** `internal/store/segments_test.go` 22 · `internal/checkpoint/writer_paths_test.go` 7 · `internal/daemon/scheduler_frontier_test.go` 5 · `internal/checkpoint/precompact_test.go` 3 · `internal/store/degraded_test.go` 2, `appendonly_test.go` 1, `golden_test.go` 1 · `internal/checkpoint/writer_test.go` 2, `finalize_test.go` 1 · `internal/daemon/wire_checkpoint_test.go` 1 · `test/e2e/v3_x07_test.go` 8, `test/e2e/store_test.go` 1.
**F:** `docs/adr/0012-scheduler-l3.md` (1) · `testdata/corpora/toolout/git/git-diff.txt` + canon twin (1 each, incidental).
**P (root):** `plans/00-ARCHITECTURE.md:1286,1288,1761` · `plans/V1-SP-01-…md:1584` · `plans/V1-VERIFY-…md:285,503` · `plans/V2-report.md:319,417,496` · `plans/V2-SP-06-…md:15,77,178,364,748,751,896,1034,1089,1148,1150,1199` · `plans/V2-VERIFY-…md:575,1157` · `plans/V3-SP-08-…md:265` · `plans/V3-VERIFY-…md:715,991,992,1003,1004,1005,1204` · `plans/V4-SP-19-…md:97` · `plans/WAVE2-PLUS-SCOPE-AUDIT.md:955`.

## 4.12 Per-chunk token sums — `EstimateRoot` and chunk token accounting

**D (interface)** `internal/tokens/estimate.go:19` — `EstimateRoot(ctx context.Context, chunks []core.ChunkRef, c Class) core.Tokens` (doc `:11`, `:16` "uses per-chunk cached measurements keyed by chunk hash — the 'exact chunk-level…'", `:28`).
**D (implementation)** `internal/tokens/exact.go:325` — `func (e *exact) EstimateRoot(_ context.Context, chunks []core.ChunkRef, c Class) core.Tokens` (doc `:318` "prices a root from its per-chunk cache without re-scanning a single byte", `:323` "Each chunk is rounded individually, which is what makes EstimateRoot exactly additive over its chunks").
**C (source):** `internal/store/put.go:170` (`res.Root.Tokens = s.deps.Tokens.EstimateRoot(ctx, refs, class)`), `:351` · `internal/observer/tooluse.go:121` (`tok = o.opt.Tokens.EstimateRoot(ctx, res.Root.Chunks, tokens.Classify(display, pathKey, body))`) · `internal/observer/stop.go:212` (`…, tokens.ClassJSON`) · design notes `internal/tokens/doc.go:7,9,13`.
**Chunk-cache seam:** `internal/tokens/chunkcache.go` (`dropped` 1 hit; the memo table `EstimateRoot` reads) with `internal/tokens/chunkcache_test.go` (3 hits).
**C (conformance):** `internal/tokens/tokenstest/estimator_suite.go:35,64,65,66,155,156,157,158` — including the additivity law `require.Equal(t, sumSeparate, together, "EstimateRoot must equal the sum of per-chunk estimates")` at `:158`, and `EstimateRoot` over zero chunks must be zero at `:65-66`.
**C (test doubles):** `internal/tokens/tokenstest/estimator_suite_test.go:21` · `internal/daemon/rehydrate_service_test.go:179` (`rsSpyTokens`) · `internal/observer/stop_test.go:280` (`stopTokens`) · `internal/rehydrate/fake_test.go:456` (`fakeEstimator`).
**C (tests):** `internal/tokens/exact_test.go` 6 · `internal/tokens/estimate_test.go` 4 · `internal/tokens/edge_test.go` 3.
**F:** `testdata/corpora/toolout/git/git-diff.txt` + canon twin (3 each — incidental corpus text).
**P (root):** `plans/00-ARCHITECTURE.md:454,2147,2150` · `plans/V1-SP-01-…md:292,1209,1583` · `plans/V1-VERIFY-…md:206,208,971` · `plans/V2-report.md:309,420` · `plans/V2-SP-04-…md:194` · `plans/V2-SP-06-…md:331,462,525,528,646,894,954,955,956,1119,1249` · `plans/V2-VERIFY-…md:1160,1622` · `plans/V3-report.md:124` · `plans/V3-SP-08-…md:265,412,1039,1584,2679` · `plans/V3-VERIFY-…md:360,1205`. **`EstimateRoot` has no mention in `plans/V4-SP-19-migration-reconciliation.md`**, although §M0-04 names "per-chunk token sums" as in scope — the plan uses the prose phrase, not the symbol.

## 4.13 `dropped` semantics — `DropReport`, the `dropped` MCP tool and slash command

`DropReport` as a Go identifier: **zero hits in either tree.** The nearest names are `buildDropReport` and `TestDroppedReturnsDropReport`; the actual type is `DropEntry`.

**Definitions:**

| Location | Declaration |
|---|---|
| `internal/checkpoint/types.go:155` | `type DropEntry struct` — "one line of the explicit drop report (G4.5, 00-ARCHITECTURE.md §5.14)" (doc `:153`) |
| `internal/checkpoint/types.go:76` | `Dropped []DropEntry \`json:"dropped"\`` on `Checkpoint` |
| `internal/checkpoint/truncate.go:11` | the eight `DropEntry` kind constants ("spelled exactly once") |
| `internal/rehydrate/drops.go:61` | `Dropped []checkpoint.DropEntry \`json:"dropped"\`` on the rehydrator's `Reporter` output |
| `internal/rehydrate/items.go:1027` | `func buildDropReport(entries []checkpoint.DropEntry) built` |
| `internal/mcp/tools.go:36` | `ToolDropped = "dropped"` — the eighth MCP tool name |
| `internal/commands/commands.go:49` | `var commandNames = []string{"status","recall","pin","checkpoint","why","dropped","eval"}` |
| `internal/pluginmanifest/manifest.go:144`, `:147` | the manifest entry `Name: "dropped"`, `Subcommand: "dropped"` |
| `internal/cli/commands.go:47` | `{"dropped", "report what the last compaction dropped (SP-14)"}` |

**Producers (source):** `internal/checkpoint/truncate.go:117,192,211,235,260,292,317,333` (every cut appends exactly one `DropEntry`) · `internal/checkpoint/validate.go:40` (`ValidatePointers` returns `[]DropEntry`), `:54,60,68,77,81` · `internal/checkpoint/finalize.go:70,196,221,228` · `internal/checkpoint/draft.go:113` (`func (d *Draft) AddDrops(e ...DropEntry)`), `:36` · `internal/checkpoint/precompact.go:46` (`ExtraDrops`), `:57` (`Drops`), `:307` (`hasTruncationDrop`) · `internal/checkpoint/schema.go:85` (`c.Dropped = []DropEntry{}` — the empty-not-null normalisation).
**Consumers (source):** `internal/rehydrate/budget.go` (14 hits) · `internal/rehydrate/items.go` (8) · `internal/rehydrate/drops.go` (6) · `internal/rehydrate/build.go` (2), `render.go` (1), `types.go` (1) · `internal/mcp/handlers.go` (7), `internal/mcp/tools.go` (7), `server.go` (1), `schema.go` (1), `doc.go` (1) · `internal/daemon/rehydrate_service.go` (3), `mcpop.go` (1), `registry.go` (1), `scheduler_candidates.go` (3), `scheduler_state.go` (2) · `test/replay/policy_rehydrate.go` (6) · `tools/devtool/planchecks.go:91`, `:507` (both are the word "dropped" in prose about the check's own filtering — **not** a drop-report consumer).
**Tests:** `internal/mcp/handlers_test.go:756` `func TestDroppedReturnsDropReport` (+9 more in file) · `internal/rehydrate/items_test.go:1376` `func TestBuildAll_CoversEveryKindButTheDropReport` (+9) · `internal/checkpoint/truncate_test.go` (9), `schema_test.go` (7) · `internal/rehydrate/verify_test.go` (6), `drops_test.go` (4) · `internal/daemon/mcpop_test.go` (7) · `test/guards/v1_integration_test.go:558` (the frozen key order incl. `"dropped"`).
**F (fixtures + generated):** `plugin/commands/dropped.md` (4) and its golden twin `testdata/golden/plugin/commands/dropped.md` (4) · `testdata/golden/checkpoints/0001-minimal.json`, `0002-full.json`, `0003-truncated.json`, `0004-tier1-over-budget.json` · `testdata/golden/contracts/checkpoint/want/0001.json`, `…/input/oversized_checkpoint.json` · `testdata/golden/rehydrate/{state.json,no-checkpoint.txt,full-8k.txt,full-12k.txt,minimal.txt,degraded.txt}` · `testdata/golden/mcp/tools-list.json`, `initialize.json` · `docs/mcp-tools.md` (2) · `docs/adr/0011-rehydration-budget-and-item-order.md` (4) · `.github/workflows/ci.yml` (1).
**P (root):** `plans/00-ARCHITECTURE.md:142,376,404,1715,1953,1973,2663,2769` · `plans/TRACEABILITY.md:80,81` · `plans/V1-SP-01-…md:74,224,1265,1290,1965,2210` · `plans/V1-VERIFY-…md:234,246,295` · `plans/V2-report.md:43,1235` · `plans/V2-SP-02-carried-defects.md:81` · `plans/V2-SP-02-…md:174,583,665,809,1193` · `plans/V2-SP-04-…md:570,820,1072` · `plans/V2-SP-05-…md:194,889,1627,1750,1755` · `plans/V2-SP-06-…md:546,1270` · `plans/V2-SP-07-…md:79,424,751,765,844,1053` · `plans/V2-VERIFY-…md:445,528,1181,1307` · `plans/V3-SP-08-…md:1121,1559,1686,1856,2694` · `plans/V3-SP-09-…md:137,600,876,920,1300,1421,1773` · `plans/V4-SP-11-rehydrator-l5.md:56` · `plans/V4-SP-13-mcp-retrieval-layer.md:42,112` · `plans/V4-SP-19-…md:97` · `plans/V5-SP-14-…md:52` · `plans/V5-SP-15-…md:39` · `plans/V6-SP-18-…md:49` · `plans/WAVE2-PLUS-SCOPE-AUDIT.md` (19) · `Qompack.md:360`.

## 4.14 The five files SP-19 M0-04 names explicitly

| File | M0-04 tokens found | Notes |
|---|---|---|
| `internal/eval` (package) | `RewriteTokens` (`types.go:165,166`; `score.go:86,320,394,404,422`), `rewrite_tokens` / `rewrite_span_tokens` (`score.go:77,352,353,393,421,422`), `pMin`/`p_min` (`belady.go:32,141,144,149,150,154,171`; `types.go:114,165,385`; `blocks.go:154`; `policy.go:260`; `breakpoint.go:87`), `Ephemeral` (`types.go:405`), `dropped` (`synth.go` 2, `belady.go` 2, `replay.go` 1, `evaltest/behaviour.go` 1), transcript usage import (`importer.go:55-57,274`) | **No `YoungDaly`, no `SkiRentalShouldWrite`, no `EstimateRoot`, no `Truncate`, no `MarkEncoded`.** |
| `test/replay/main.go` | `rewrite_tokens` at **`:558`** and **`:563`** only | Metric-name coupling to `internal/eval/score.go:353` |
| `test/replay/phases.go` | **none** | File exists (`test/replay/phases.go`); it contains no M0-04 token at all. The rewrite/p-selection surface the plan expects lives in `test/replay/main.go`, `gate_test.go`, `phase4_test.go`, `phase3_rehydrate_test.go` and `policy_rehydrate.go`. |
| `tools/devtool/planchecks.go` | `dropped` at `:91` and `:507` — **prose about the checker's own dropped rows**, not the drop report | No other M0-04 token. |
| `test/guards/buildorder_test.go` | **none** | It imports `analyzer`, `config`, `core`, `dag`, `scheduler` but works purely through stub probes (`evalProbe`, `storeProbe`, `negknowProbe`, `checkpointProbe` — `test/guards/buildorder_test.go:22`, `:35`). The M0-04 tokens are not referenced. |
| Config golden tests | `internal/config/defaults_test.go:18` `TestDefaults_MatchesAppendixCVerbatim` (whole-`Defaults()` byte match against `testdata/golden/config/appendix-c.jsonc`, which carries `youngDaly` at `:15` and `ephemeralResults` at `:40`); `internal/config/schema_test.go:140` names `scheduler.youngDaly.measuredDeltaSeconds` explicitly, golden at `testdata/golden/config/schema.json:767,798`; `docs/config-reference.md:52,115,116` and `:27-29` (checkpoint tiers → what `Truncate` cuts) | Changing any M0-04 config semantics moves **both** goldens plus `docs/config-reference.md` (which `devtool gen-config-docs --check` verifies). |

The stub-probe guard `test/guards/stubs_test.go` names two M0-04 symbols directly: `:470` `YoungDaly`, `:471` `SkiRentalShouldWrite`.

---

# 5. Capability register inputs

Rows are the ledger's "Capability decisions" table (`plans/MIGRATION-EVIDENCE.md:80-92`). "Evidence status supportable from the tree alone" uses the SP-19 vocabulary (`V4-SP-19…md:46`).

| Capability | Ledger's stated evidence state | Mechanism in the develop tree (file:line) | Tests exercising it | Installed-host claim anywhere? | Status supportable from the tree alone |
|---|---|---|---|---|---|
| **Observation** | implemented_unverified | Hook arrival: `internal/daemon/handlers.go:103` `resolveEvent`, `:365` `handleObserveTool`, `:371` `handleObserveStop`, `:416` `handleObservePrompt`, `:510` `handleSessionStart`, `:667` `handleCheckpoint`. Observer interface `internal/observer/observer.go:45` `OnToolUse`, `:51` `OnStop`, `:54` `OnSessionStart`, `:56` `OnSessionEnd`; constructor `:329`. Capture: `internal/observer/tooluse.go`, `stop.go`, `supersede.go`, `tombstone.go`; store record `internal/store/tooluse.go:83`. | `internal/observer/*_test.go`; conformance `internal/observer/observertest/behaviour.go`; `test/e2e/observer_e2e_test.go`, `test/e2e/hooks_test.go`, `test/integration/hookflow_test.go`; contract assertions `internal/contract/assertions.go:74` `checkSessionStartFires`, `:287` `checkHookPayloadShape`, `:339` `checkTranscriptReadable`. | **No.** Nothing in the tree claims verification against an installed host. The nearest statements go the other way — `test/e2e/harness.go:7` "this package exists because in-process dispatch cannot prove the things that…"; `internal/mcp/jsonrpc_test.go:26` "…and fail against a real host on the first turn" (a hypothetical, not a result). | **implemented_unverified** — real code, real in-process/e2e tests, zero target evidence. |
| **Injection** | documented; installed behavior unknown | `internal/hookio/output.go:23` `AdditionalContext string \`json:"additionalContext,omitempty"\``, constructor `:51` `SessionStartOutput(ctx string)`. Producer path: `internal/daemon/rehydrate_service.go:74` `NewRehydrateService`, `:100` `OnCompact`, `:190` `OnClear`, `:301` `BindRehydrate`, `:318` `WireRehydrator`. Sentinel proof-of-delivery: `internal/contract/sentinel.go:22` `type Sentinel`, `:31` `MintSentinel`, `:39` `RenderSentinel`; scanned by `internal/daemon/handlers.go:491` `scanSentinelForPrompt`; state machine `internal/contract/history.go:33,48,223`. | `internal/daemon/rehydrate_service_test.go:468`, `:633`, `:657`; `test/e2e/sessionstart_compact_test.go:207`, `:390`; `internal/contract/gated_test.go:48-67`; `internal/contract/sentinel_test.go:36`; assertion `internal/contract/assertions.go:162` `checkAdditionalContextDelivered` ("it only ever READS…"). | **No.** | **implemented_unverified** for the *emission* path; **documented/unknown** for whether the host honours it. The sentinel scan reads a host-written transcript, so it *could* produce target evidence, but no recorded run exists in-tree. |
| **New-result replacement** | documented; target unknown; **off pending SP-21** | **Absent.** `grep -rn 'updatedToolOutput\|UpdatedToolOutput\|permissionDecision\|PermissionDecision'` over `internal cmd test tools testdata plugin` returns **nothing**. `hookio.Output` (`internal/hookio/output.go:11-16`) carries only `Continue`, `SuppressOutput`, `HookSpecificOutput`, `SystemMessage`; `HSO` (`:20-26`) carries only `HookEventName`, `AdditionalContext`, `CustomInstructions`. | none — there is nothing to exercise | **No.** | **unsupported** *in this tree* (no mechanism exists); the ledger's "documented" refers to the external hooks doc (E09, `plans/MIGRATION-EVIDENCE.md:67`), not to code here. |
| **Usage attribution** | documented source contract; target unknown; no zero-filled cost ledger | Only ingest-side: `internal/eval/importer.go:55-57` — `Usage struct { OutputTokens int \`json:"output_tokens"\` } \`json:"usage"\`` — and `:274` `Tokens: core.Tokens(rec.Message.Usage.OutputTokens)`. Calibration hint at `internal/tokens/calibrate.go:24` ("a turn whose usage delta happened to include a cache write"). **No input/cache-read/cache-write field is read anywhere**; no request ledger exists. | `internal/eval/importer*_test.go` (transcript parsing only) | **No.** | **unsupported / unknown** — one field of one category (`output_tokens`) is parsed from recorded transcripts; there is no attribution mechanism, no category sums, no rate schedule. SP-19 commit 5 (`feat(eval): add request usage ledger`) is the named future work. |
| **Token estimation** | existing estimator location; calibration unknown | `internal/tokens/estimate.go:11-24` (`Estimator` interface: `Estimate`, `EstimateString`, `EstimateRoot`, `Calibrate`, `Factor`); `internal/tokens/exact.go:325` `EstimateRoot`; per-chunk memo `internal/tokens/chunkcache.go`; `internal/tokens/calibrate.go`. | `internal/tokens/tokenstest/estimator_suite.go:35,64-66,155-158` (additivity + zero laws), `internal/tokens/{exact,estimate,edge,chunkcache}_test.go` | **No.** `internal/tokens/doc.go:7-13` explicitly calls the formulas "heuristics". | **implemented_unverified** — the estimator exists and is law-tested for internal consistency; nothing calibrates it against a provider count. |
| **Compaction requests** | **unsupported adapter mechanism here**; advisory cadence only | The scheduler computes a *recommendation* only: `internal/scheduler/evaluate.go:166,170,173` set `d.ShouldCompact`; `:192,200,201` cap urgency to `UrgencyAdvisory`; persisted at `internal/daemon/scheduler_state.go:54` `ShouldCompact bool \`json:"should_compact"\``, `:160`, `:182`. **No code path asks the host to compact** — there is no host-facing request channel in `hookio.Output`. | `internal/scheduler/evaluate_test.go`, `internal/daemon/scheduler_state_test.go`, `internal/daemon/scheduler_frontier_test.go`, `test/e2e/scheduler_*`; goldens `testdata/golden/scheduler/decision-{cold,warm,expiring}.json` | **No.** | **unsupported** for "request the host to compact"; **implemented_unverified** for the advisory decision itself. Matches the ledger row exactly. |
| **Compaction blocking** | documented host surface; safe target distinction unknown; **automatic optimization veto off, manual compact never blocked** | **Absent by construction.** The only near-blocking field is `hookio.Output.Continue *bool` (`internal/hookio/output.go:12`), and the conformance suite forbids using it to stop the host: `internal/observer/observertest/behaviour.go:113-114` — `if out.Continue != nil { require.True(t, *out.Continue, "%s must never stop the host from continuing", what) }`. No `deny`/`block`/`permissionDecision` string exists in the tree. | `internal/observer/observertest/behaviour.go:113`; `internal/hookio/output_test.go:56` | **No.** | **unsupported** in this tree, and additionally **actively guarded against** by the conformance suite. |
| **History rewriting / native eviction** | **unsupported**; excluded; ephemeral metadata is a representation hint only | **Absent, and documented as impossible.** `internal/eval/breakpoint.go:19` — "manages its own cache markers and a plugin cannot place or move them; the analysis is retained…"; `internal/eval/types.go:465` — "manages its own cache markers and a plugin cannot move them. Note always carries that sentence". The only eviction concept is Qompack-internal ranking: `internal/scheduler/dropclass.go:26` `EvictionRank` ("ephemeral (3) > superseded (2) > ordinary (1)"), `:57` `DropClassOf`, and the store's own GC (`internal/store/gcrun.go:320-324`). | `internal/scheduler/dropclass_test.go`, `internal/store/gc_test.go` | **No.** | **unsupported** — confirmed by two explicit in-code statements that the plugin cannot move native cache markers. |

## 5(a) `internal/daemon/options.go` — `DeclareProducer` calls

`func DeclareProducers(s *Services)` at **`internal/daemon/options.go:153`** (doc `:145-152`). Called from exactly two production sites — `internal/daemon/daemon.go:247` (inside `New`, **after** every `Bind`; see `internal/cli/daemon.go:185` and `internal/daemon/mcpop.go:65-67`) and `internal/cli/selftest.go:324` (`daemon.DeclareProducers(&daemon.Services{})`, deliberately against a zero `Services`).

| Line | Call | Gate |
|---|---|---|
| `options.go:154` | `contract.DeclareProducer(contract.CSessionStartFires)` | unconditional (SP-05, wave 1) |
| `options.go:155` | `…CSessionStartSourceCompact` | unconditional |
| `options.go:156` | `…CHookPayloadShape` | unconditional |
| `options.go:157` | `…CTranscriptReadable` | unconditional |
| `options.go:158` | `…CPluginRootResolves` | unconditional |
| `options.go:160` | `…CPreCompactTiming` | `if s.PreCompact != nil \|\| s.Checkpoints != nil` (`:159`) |
| `options.go:161` | `…CPreCompactCustomInstr` | same gate |
| `options.go:164` | `…CAdditionalContext` | `if s.Rehydrate != nil` (`:163`) |
| `options.go:167` | `…CMCPRegistered` | `if s.MCPInitialized != nil` (`:166`) |

Primitive: `contract.DeclareProducer(id ID)` at `internal/contract/producers.go:19` (process-global, idempotent; reset helper documented at `:37`). Guarded by `internal/daemon/options_test.go:96-126` (`TestDeclaredProducerSetMatchesArchitecture` — "exactly these four, no more and no fewer") and `test/integration/contractmonitor_test.go:117,157,160`.

## 5(b) `internal/contract/standard.go` — assertion → capability relationships

`StandardAssertions()` at `internal/contract/standard.go:21` returns nine assertions, every one wrapped in `gated(...)`. `internal/contract/standard.go:3-6` defines the frozen string `notYetImplementedObserved = "not-yet-implemented"`; `:8-20` states the whole mechanism: "an assertion whose producer has not been declared reports OK/SevInfo/not-yet-implemented and its real Check … never runs at all."

| standard.go line | Assertion ID (`internal/contract/ids.go`) | Severity | Real check | Capability it observes | Producer gate |
|---|---|---|---|---|---|
| `:23` | `CSessionStartFires` = `session_start.fires` (`ids.go:13`) | SevCritical | `checkSessionStartFires` (`assertions.go:74`) | **Observation** (lifecycle) | always declared |
| `:24` | `CSessionStartSourceCompact` = `session_start.source_compact` (`ids.go:17`) | SevCritical | `checkSessionStartSourceCompact` | **Observation** — PreCompact→SessionStart(compact) sequencing | always |
| `:25` | `CAdditionalContext` = `hook.additional_context_delivered` (`ids.go:20`) | SevCritical | `checkAdditionalContextDelivered` (`assertions.go:162`) | **Injection** | `Services.Rehydrate != nil` |
| `:26` | `CPreCompactTiming` = `precompact.has_time_to_write` (`ids.go:23`) | SevWarn | `checkPreCompactTiming` (`assertions.go:188`) | **Observation** (PreCompact budget) | `PreCompact` or `Checkpoints` bound |
| `:27` | `CPreCompactCustomInstr` = `precompact.custom_instructions_accepted` | SevWarn | `checkPreCompactCustomInstr` (`assertions.go:224`) | **Injection** (PreCompact instruction channel). `assertions.go:225` marks it "Advisory by design (§8.5)" | same gate |
| `:28` | `CHookPayloadShape` = `hook.payload_shape` | SevCritical | `checkHookPayloadShape` (`assertions.go:287`) | **Observation** (payload contract) | always |
| `:29` | `CMCPRegistered` = `mcp.server_registered` | **SevInfo** | `checkMCPServerRegistered` (`assertions.go:318`) | retrieval reachability | `Services.MCPInitialized != nil` |
| `:30` | `CTranscriptReadable` = `transcript.readable` | SevWarn | `checkTranscriptReadable` (`assertions.go:339`) | **Observation** (transcript access) | always |
| `:31` | `CPluginRootResolves` = `plugin.root_resolves` | SevWarn | `checkPluginRootResolves` (`assertions.go:364`) | packaging / launcher | always |

**Material for the capability register.** No assertion covers *new-result replacement*, *usage attribution*, *token estimation*, *compaction requests*, *compaction blocking* or *history rewriting* — the monitor observes only observation, injection and reachability. And the mechanism `standard.go:8-20` describes is exactly the one `V4-SP-19…md:23` warns about: "The currently observed contract monitor in architecture §12.1 treats unavailable producers as informational success". A build with a producer undeclared reports `OK/SevInfo/not-yet-implemented`, which is not the same as a failed host feature.

The V4 gate row that will close this on develop is `plans/V4-VERIFY-…md:299` (V4-SP05-03: "**All nine contract assertions have real producers** … **zero** assertions at `Observed:"not-yet-implemented"` … Five-declared/four-gated was the V2/V3 answer and is now a **regression**"), and the root v1.4 rewrite carries the corresponding row at `plans/V4-VERIFY-…md:141` (4.14 `TestV4_EveryContractAssertionHasARealProducer` → "SP-19: separate capability evidence and missing states; retire fixed 9/0 and setter-success monitor claims; packaged canaries").

---

# 6. Blockers and unclear items

1. **Whether M0-G0 is *accepted*.** The ledger records the reviewer verdict as "ACCEPT M0-G0 subject to validation" (`plans/MIGRATION-EVIDENCE.md:321`) and then says "**Whether M0-G0 is *accepted* on this evidence is the coordinator's call**"; the "Next" section (`:350`) leaves the decision open (accept, or first fix the three V4-VERIFY runpattern rows / the `paths` floor / the negknow budget). *Tried:* read the whole handoff section, searched `plans/` in both trees for a later acceptance record; found none. **However**, `feat/sp19-migration-reconciliation` and `../qompack-sp19` already exist at 66549ce, which is the action the handoff gates on acceptance — so the decision appears to have been taken outside the written record. Unclear: no document states it.

2. **`wip/sp19-eval` and `wip/sp19-host` are unrecorded.** Two branches and two worktrees exist at 66549ce, clean, matching no branch name in `V4-SP-19…md:142` (which names only `feat/sp19-migration-reconciliation`, `../qompack-sp19`, and `arch/migration-contracts`). *Tried:* grepped both trees' `plans/` for `sp19-eval` / `sp19-host` / `wip/`; no hit. Unclear: whether they are the plan's baseline-owner and host-tester seats.

3. **`plans/TRACEABILITY.md` still calls Wave 3 "in progress" / L3–L6 "active"** (`:59`, `:60`, `:69`) while `plans/README.md:19`, `V4-SP-19…md:3` and the merged history all say complete. Recorded in §2 as a disagreement; I did not edit it.

4. **The v1.4 root rewrites drop the delivered plan text.** The four wave-3 root plans and root `V4-VERIFY` are 5–24× shorter than their develop counterparts and no longer contain the delivered test tables, the `frontierOf` handoff paragraph (§3b), or most M0-04 symbol mentions (§4). *Tried:* compared line counts, checkbox counts, headers and per-symbol grep between the two copies. Unclear: whether the v1.4 revision intends the develop copies to remain the authoritative delivery record (in which case the two must be read together, as `V4-SP-11-rehydrator-l5.md:1867` on develop instructs) or to be superseded on the next merge. This is a contract question for the coordinator, not an audit finding.

5. **`test/replay/phases.go` contains none of the M0-04 tokens** although `V4-SP-19…md:97` names it explicitly as an in-scope compatibility-audit file. Same for `test/guards/buildorder_test.go` (probe-only) and `tools/devtool/planchecks.go` (its two `dropped` hits are unrelated prose). *Tried:* word-boundary grep for all 20 tokens in each file. Not a defect — just an inventory fact the future audit needs.

6. **`PSelection` / `PSelectionAvailable()` does not exist.** The develop copy of `plans/V4-SP-12-scheduler-l3.md` says SP-12 "gates SP-15 via `PSelectionAvailable()`", but `grep -rn 'PSelection'` over both trees returns nothing outside that banner. The shipped p-selection surface is `internal/eval/belady.go:154` `pMin` plus `Decision.P`. Unclear: renamed, or never shipped.

7. **`EstimateRoot` is absent from the SP-19 plan text.** M0-04 names "per-chunk token sums" in prose (`V4-SP-19…md:97`) but the symbol appears in no root plan file. Recorded so the future audit does not read the absence as "not in scope".

8. **No installed-host evidence of any kind exists in the tree**, for any of the eight capabilities. I searched for `installed host`, `real Claude Code`, `against Claude Code`, `live host`, `verified against the host`, `in a real session` across `internal/ test/ tools/ docs/ plans/` in both trees. Every hit is either hypothetical ("would fail against a real host"), a fixture-shape note ("the client identity a real host sends", `internal/mcp/server_test.go:37`), or a measurement caveat. `devtool plugin-validate` (`tools/devtool/pluginvalidate.go:38`) regenerates the bundle from `internal/pluginmanifest` and byte-compares — a repository validator, exactly as `V4-SP-19…md:93` insists it be labelled. **B01 stands unresolved.**

9. **Not established because it requires running things (out of scope, and blocked anyway):** CI status for 66549ce (billing-blocked per `plans/V3-report.md:279-292`; nothing has been pushed — `origin/develop` is still `907f983`); any benchmark, race or e2e result beyond what the M0-00 handoff already records; and the four SP-11 deferred timing gates, SP-13's B-F margin and SP-12's baselines, all of which the handoff lists as "Not performed here" (`plans/MIGRATION-EVIDENCE.md:346`).

10. **Dirt in the four non-ancestor SP-12 scratch worktrees** (rows 26–29 in §1a) was recorded but not analysed: whether any of those uncommitted changes represents work not present in `1e767c3` is a question for the SP-12 owner, and reading their diffs was outside this inventory's scope. *Tried:* `git status --short` in each; did not diff.

**All six sections are complete.**
