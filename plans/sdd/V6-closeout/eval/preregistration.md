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

None.
