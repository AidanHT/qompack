# Pre-registration — Qompack live evaluation, task set `qompack-live-v1`

V6 close-out C5.4 / C5.5. Written 2026-09-22 by the C5.4 fix seat (an Opus 5.5 workflow subagent),
**before any trial of this task set existed**. The commit that adds this file is its timestamp.

At the time of writing, the only real host sessions this workstream has run are:

- three harness-validation smoke sessions (`runs/smoke1-stock-compact`, `runs/smoke2-plugin-dir-compact`,
  `runs/smoke3-plugin-dir-autocompact`) on a throwaway "release codeword" task that is not in this set;
- nothing else. The pilot sessions (`runs/pilot-*`) run AFTER this file is committed, on
  `testdata/eval/live/pilot.json`, whose one task and fixture are disjoint from this set
  (`TestLiveTaskSet_TheFrozenSetIsValidAndShapedAsPreregistered` pins that).

No task of `qompack-live-v1` has been run by anyone, and the three held-out tasks must not be run
before the confirmatory run.

## 1. Question

Does loading the Qompack plugin change whether real Claude Code sessions still get the work right
after a compaction — the task, the constraints stated before the compaction, and the facts that
were only in the pre-compaction context — and at what token, cost and latency price?

## 2. Frozen materials

| Item | Path | SHA-256 |
|---|---|---|
| Task set | `testdata/eval/live/tasks.json` | `14e9ee33ccfff573c00db0a108824853e08d916c3099842c232b624ac5eafff0` |
| Fixtures + hidden tests (sorted `sha256sum` of every file under `fixtures/` and `hidden/`, hashed) | `testdata/eval/live/{fixtures,hidden}/` | `071d9d1d2b86efbf54115d32ebc11ca8e1eca2fbea1616998c29afdd63a56ec8` |
| Rate table (estimates only) | `testdata/eval/live/rates.json` | `97dfb469e316ca27a05957a380be64f689fb8da664a0c1d566fc462f439b0982` |
| Pilot set (harness validation only) | `testdata/eval/live/pilot.json` | `4da30ddf165ad260b2a386872c25e347eb2ebf7e3b39b02a832e1a301b55b1a6` |

`devtool live-eval` records the task set's SHA-256 in every run's `plan.json`; a run whose hash
differs from the one above is not a run of this pre-registration.

Every task was checked to be gradeable before this file was written:
`TestLiveTaskSet_ReferenceSolutionsPassEveryCheck` (tools/devtool) builds each fixture with a
correct, constraint-respecting reference solution (`testdata/eval/live/reference/`) and the hidden
tests and requires every check to pass, and requires the untouched fixture to fail at least one.

## 3. Design

Two arms, identical except for the plugin:

- **stock** — Claude Code with no Qompack plugin;
- **qompack** — the same, with the plugin loaded from one frozen bundle with `--plugin-dir`
  (the identical plugin bytes a marketplace install copies; the marketplace flow itself is
  C4.1's evidence, not this study's).

Both arms, every trial: one `claude -p --input-format stream-json --output-format stream-json
--verbose --include-hook-events` process; `--setting-sources project,local` (the operator's user
settings, with their own plugins and hooks, load in neither arm); `--permission-mode dontAsk` with
the task set's allow-list; `ENABLE_CLAUDEAI_MCP_SERVERS=false`; a fresh `git init`-ed copy of the
task's fixture in a temporary directory outside any repository; one user message per step, each
sent only after the previous step's result; and the compaction forced by a `/compact` user turn at
the task's declared step. Arm order alternates by task and trial (`planLiveTrials`).

**Model:** `claude-sonnet-5`, pinned by ID. Contingency fixed now: if the host rejects that ID in
the first confirmatory session before any turn completes, the run is restarted with the host alias
`sonnet`, the resolved model the host reports is recorded, and the change is appended to §9 before
the restart. No other model change is permitted.

**Host:** the installed Claude Code CLI on the owner's Windows machine (2.1.280 at the time of
writing), agent-executed per owner decision D3 — never recorded as human UAT.

## 4. Tasks

Ten tasks; each is a Go fixture, steps before the compaction that establish something, the
compaction, and graded steps after it. Graders are programs: file regexes, byte-identity against
the fixture, hidden test suites copied in after the session, `go` commands, answer regexes and
"no tool call after the compaction matched" checks. No model grades anything.

| Task | Category | Variant | Held out |
|---|---|---|---|
| `constraint-verbatim` | constraint retention | base | no |
| `recall-user-fact` | fact recovery | base | no |
| `eliminated-approach` | negative knowledge | base | no |
| `changing-requirement-format` | requirement change | changing-requirement | no |
| `tool-output-recall` | tool-result recovery | base | no |
| `decision-rationale` | decision recovery | base | no |
| `regression-guard` | regression | base | no |
| `constraint-naming` | constraint retention | base | **yes** |
| `changing-requirement-slug` | requirement change | changing-requirement | **yes** |
| `seed-recall` | tool-result recovery | base | **yes** |

Facts a task needs after the compaction are deliberately placed where the host's own post-compaction
file re-attachment cannot reach them — in the user's words, in a program's output, or in a decision
— because the smoke sessions showed the host re-attaches recently read files after `/compact`.

## 5. Hypotheses

- **H1 (primary).** Task success after compaction with Qompack is not worse than stock by more
  than 0.20 (non-inferiority), and may be better.
- **H2.** The proportion of trials with no constraint violation is not lower with Qompack.
- **H3.** Recovery of pre-compaction facts is higher with Qompack.
- **H4 (secondary, descriptive).** Qompack's rehydration adds context and hook work; its token,
  cost and latency overhead per trial is reported, not tested.

## 6. Outcomes

Primary, per trial (`eval.LiveTrial`, graded by `eval.GradeLiveTrial`):

1. **Task success** — every `task` check passed.
2. **Constraint-clean** — no `constraint` check failed (violations are also counted).
3. **Recovery** — every `recovery` check passed (over the tasks that declare recovery checks).

Secondary: total tokens by category (input, output, thinking output, cache read, cache write by
TTL) from the host's own usage JSON, never double-counted across turns (`eval.AccountHostStream`);
a list-price-equivalent **estimate** from the dated rate table, labelled incomplete when a category
is unknown (subscription sessions carry no per-token cash charge); the host's own `total_cost_usd`
beside it, never merged; wall time; host-measured hook latency per hook (transcript `durationMs`);
`.qompack/` size and file count, checkpoints and injected rehydration bytes; Qompack MCP tool calls;
every hook failure the host reported.

## 7. Sample size

10 tasks × 2 trials × 2 arms = **40 sessions**, inside owner decision D3's 40–80 real-session
budget, which the UAT phase shares. That is 20 trials per arm. At success rates near 0.7 in both
arms the Newcombe 95% interval on the difference has a half-width of about 0.27, and about 0.2
near 0.9. **The study can only detect large effects**: it is designed to find breakage (a plugin
that makes real work fail after compaction) and to estimate effect sizes honestly, not to resolve
small differences. A second confirmatory batch of the same 40 is permitted only if §9 records it
before the first batch's outcomes are read.

## 8. Analysis and decision rule

All planned trials are analysed (intention to treat). A trial the harness could not run as
designed is scored as a failure on every outcome and listed by name; nothing is dropped.

**Decision rule** on the primary outcome (implemented as `eval.DecideLive`, 95% Newcombe
hybrid-score interval on qompack − stock, margin 0.20):

- **not-applicable** — any trial's plugin state contradicted its arm (the plugin failed to load on
  a qompack trial, or loaded on a stock trial);
- **superior** — the interval's lower bound is above 0;
- **non-inferior** — the lower bound is above −0.20;
- **inferior** — the upper bound is below −0.20;
- **inconclusive** — otherwise.

H2 and H3 are reported with the same interval and no separate verdict, except that a constraint-clean
difference whose upper bound is below −0.20 is reported as a **regression** whatever H1's verdict.

Reported but not decided: results per task, per variant (base, changing-requirement, held-out) and
the per-task sign of qompack − stock (trials are clustered within tasks; the pooled interval treats
them as independent, which overstates precision — this limitation is stated in the report).

**Re-runs:** a trial whose harness error is infrastructure (the API refused or overloaded before
the first compaction, the host process crashed) may be re-run once. Both records are kept; the
primary analysis uses the first attempt, a sensitivity analysis the re-run.

## 9. Preconditions and amendments

The confirmatory run uses one frozen candidate bundle whose identity (`BUNDLE.json` SHA-256,
commit, dirty flag) is recorded in `plan.json`. At the time of writing the plugin has known host-facing
defects: **C1.12** (Claude Code 2.1.280 rejects the PreCompact hook's `hookSpecificOutput`, so its
instructions never reach the summarizer and the validation error is replayed into the
post-compaction context — observed in `runs/smoke2-plugin-dir-compact`), **C1.1** (live ingest)
and **C1.11** (Windows hooks without Git Bash). The confirmatory run must be on a candidate where
C1.12 and C1.1 are fixed; a run on a candidate with a known open defect is labelled with that defect
and is not the confirmatory run.

Commands (the coordinator runs them):

```
go run ./tools/devtool bundle --target windows/amd64 --version <v> --out dist/live-bundle
QOMPACK_LIVE_EVAL=1 go run ./tools/devtool live-eval --tasks testdata/eval/live/tasks.json \
  --include-held-out --arms stock,qompack --install plugin-dir \
  --bundle dist/live-bundle/qompack-plugin-<v>-windows-amd64 --max-sessions 40
```

Amendments are appended below with date, author and reason, before the first confirmatory trial.
A change to a task after any of its outcomes exists requires a new task-set id.

### Amendments

**A1 — 2026-09-25, by the C5.4 independent-review seat (an Opus 5.5 workflow subagent, branch
`closeout/w2-eval2`), before any confirmatory trial.** No task, fixture, hidden test, rate or
analysis parameter changes; only the recipe that names the fixture tree does.

*Reason.* The §2 row "Fixtures + hidden tests" records `071d9d1d…` as computed with Git Bash on
Windows, where `sha256sum` writes its binary-mode ` *` marker and `sort` ordered the paths by the
`en_US` locale's collation (`fixtures/svc/README.md` sorts after the lowercase names). The same
37 files hash differently under GNU coreutils in text mode or under a byte-order sort, so the
recorded value cannot be reproduced on Linux, by a program, or by anyone who does not know the
recipe's accidents. The bytes are unchanged: the tree still gives `071d9d1d…` under that exact
Windows recipe, and `tasks.json`, `rates.json` and `pilot.json` still match their §2 hashes.

*Amendment.* The fixture tree's identity for every run of this pre-registration is the
locale- and platform-independent manifest hash

`30cf769d243776645506eb43b7e0b336d2fed9a6e2ea29b4fb70a1f2b4054276`

— `find fixtures hidden -type f | LC_ALL=C sort | xargs sha256sum | sha256sum` run in
`testdata/eval/live` with GNU coreutils (one `<sha256>  <path>` line per file, paths in byte
order), implemented as `eval.TreeManifestSHA256`. `devtool live-eval` records it in every run's
`plan.json` as `fixture_tree_sha256` (with `fixture_tree_dirs`) and prints it in `summary.md`; a run
whose value differs is not a run of this pre-registration. `TestLiveTaskSet_FrozenMaterialsMatchThePreregistration`
(internal/eval) reads the §2 hashes and this value out of this document and fails if any frozen
material stops matching, or if the §4 task table stops naming exactly the task set.

The same review brought the harness into line with §8 as written — a harness failure now fails
every outcome (recovery included, so it stays in that denominator), and the H2 regression, the
per-variant results, the per-task signs and the clustering limitation are now reported. Those are
corrections of the code to this document, not changes to it.

**A2 — 2026-09-25, by the C5.4 independent-review seat (an Opus 5.5 workflow subagent, branch
`closeout/w2-eval2`), before any confirmatory trial.** No task, fixture, hidden test, rate or
analysis parameter changes; the §8 not-applicable rule is made precise.

*Reason.* §8 makes the whole verdict not-applicable when "any trial's plugin state contradicted its
arm", and scores a trial the harness could not run as designed as a failure on every outcome. The
code read a qompack trial whose host never started (a failed install, a process that died before
its first line) as "the plugin failed to load", so one such infrastructure failure voided the whole
comparison, while the same failure on a stock trial counted as a stock failure. That asymmetry can
only ever spare the plugin a counted failure.

*Amendment.* A trial's plugin state is the plugin list its host reported at start-up (its `init`
line, recorded as `host_reported_plugins`). Only a trial whose host reported one can contradict its
arm. A trial whose host reported none is a harness failure and is scored under §8's intention-to-treat
rule, as a failure on every outcome and listed by name (`eval.SummarizeLive`,
`TestSummarizeLive_NoPluginStateIsAHarnessFailureNotAMismatch`).

**A3 — 2026-09-25, by the same seat, before any confirmatory trial.** No task, fixture, hidden test,
rate or analysis parameter changes; what a `tool_not_used_after` check counts is made precise.

*Reason.* §4's "no tool call after the compaction matched" checks (`no-rerun` in
`tool-output-recall` and `seed-recall`) exist to catch a fact that was re-derived instead of
recovered: the program run again, its source read, the hash recomputed. The code matched the
pattern against every tool call, the Qompack plugin's own MCP tools included, so a qompack-arm
model that looked the earlier output up in the plugin's archive with a query naming the command
(`recall` for "go run ./cmd/probe") failed the constraint for doing exactly the recovery the plugin
exists to provide. The stock arm has no such tools, so the error could only ever count against the
plugin, in H2.

*Amendment.* A `tool_not_used_after` check ignores calls to the Qompack plugin's own MCP tools
(names beginning `mcp__plugin_qompack_qompack__`): they look up what the plugin archived and run no
program and read no file from disk. Every other tool call still counts, a Bash or PowerShell run of
the same command and a `Read` or `Grep` of the program's source included
(`eval.ToolUsesAfterSteps`, `TestToolUsesAfterSteps_TheArchiveIsRecoveryNotRederivation`).

**A4 — 2026-09-25, by the same seat, before any confirmatory trial.** No task, fixture, hidden test,
rate or analysis parameter changes; a second not-applicable case is made explicit.

*Reason.* `devtool live-eval` stops a run when its guard finds the operator's Claude Code
configuration changed, and still wrote a verdict computed from the trials that had run, with only a
note that the run stopped. §8 analyses every planned trial; trials that never ran cannot be, so a
verdict over the rest is not the pre-registered analysis.

*Amendment.* A run that stopped before every planned trial ran is **not-applicable**, with the reason
naming how many of the planned trials ran; its per-arm intervals are still reported as a description
(`eval.LiveSummary.StopEarly`, `TestRunLiveEval_StoppedRunReachesNoVerdict`). `qompack eval` already
calls such a run not confirmatory.

**A5 — 2026-09-25, by the C5.4 fix seat (an Opus 5.5 workflow subagent, branch `closeout/w2-eval2`),
before any confirmatory trial.** No task, fixture, hidden test, rate or analysis parameter changes;
how §9's known-defect precondition is recorded and read is made precise.

*Reason.* §9 makes the candidate's defect state part of what the confirmatory run is, and nothing
recorded it: a bundle's identity carries its commit and dirty flag, not which defects it fixes, and
`qompack eval` called a run confirmatory without asking, so a 40-trial run on a bundle with C1.12
still open would have been judged. §9 names C1.12 and C1.1 as the defects that must be fixed, then
sets aside "a run on a candidate with a known open defect"; the second clause is read in its plain
sense, as any known open defect, which also covers the first.

*Amendment.* `devtool live-eval` refuses to plan a run with the qompack arm, dry run included,
unless `--known-open-defects` states which known defects the bundle still carries: `none`, or their
checklist IDs. `plan.json` records the statement as the operator's (`known_defects`). A run is the
confirmatory run only if its plan attests that the bundle carries no known open defect — C1.12 and
C1.1 fixed and no other open; a plan with no statement, or one that names an open defect, is
labelled with it and is not the confirmatory run (`eval.LivePreregistration.RequiredFixed`,
`TestEval_LiveNotConfirmatoryWithoutTheSection9DefectAttestation`). The statement is the operator's
and cannot be machine-checked; `qompack eval` and `summary.md` say so beside every run that carries
one. §9's command gains the flag:

```
QOMPACK_LIVE_EVAL=1 go run ./tools/devtool live-eval --tasks testdata/eval/live/tasks.json \
  --include-held-out --arms stock,qompack --install plugin-dir --known-open-defects none \
  --bundle dist/live-bundle/qompack-plugin-<v>-windows-amd64 --max-sessions 40
```
