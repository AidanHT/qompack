# Live evaluation 20261007T184908Z-abe10e

Task set `qompack-live-v2` (sha256 `d59dc09d15edb04acb567dd97ac620c41eaf00206093bdc59a5162607619af83`; fixture tree [fixtures hidden hidden-v2] manifest sha256 `a5f57b1e2045378d1b38a629f75f830755e95e3582513ea8e3d3fb0d452ec3d2`), model `claude-sonnet-5` (pre-registered `claude-sonnet-5`), arms stock, qompack, install `plugin-dir`, Claude Code 2.1.280. agent-executed on the real installed host (owner decision D3); not human UAT.

The bundle's known open defects: none — the operator's statement when the run was planned, not machine-checked (preregistration section 9 counts only a run with none as the confirmatory run).

Every cost figure is a list-price-equivalent ESTIMATE from the 2026-09-22 rate table; the sessions ran on a subscription, which has no per-token cash charge.

**Decision (pre-registered rule, primary outcome):** inconclusive — interval [-0.214, 0.214] straddles -0.200

| arm | trials | completed | task success (95% CI) | constraint-clean (95% CI) | violations | recovery (95% CI) | mean host cost USD | hook-problem trials | inconsistent accounts |
|---|---|---|---|---|---|---|---|---|---|
| qompack | 20 | 20 | 18/20 = 0.90 [0.70, 0.97] | 20/20 = 1.00 [0.84, 1.00] | 0 | 6/8 = 0.75 [0.41, 0.93] | 0.2250 | 0 | 0 |
| stock | 20 | 19 | 18/20 = 0.90 [0.70, 0.97] | 19/20 = 0.95 [0.76, 0.99] | 0 | 6/8 = 0.75 [0.41, 0.93] | 0.2101 | 0 | 0 |

Task-success difference (qompack − stock): 0.000 [-0.214, 0.214]

Constraint-clean difference (qompack − stock): 0.050 [-0.116, 0.236]

Recovery difference (qompack − stock): 0.000 [-0.385, 0.385]

## Hook latency, as the host measured it

Transcript durationMs per hook, over every trial of the arm; reported, not decided (preregistration section 6).

| arm | hook | runs | p50 ms | p95 ms | max ms |
|---|---|---|---|---|---|
| qompack | PostToolUse:Bash | 40 | 71 | 246 | 489 |
| qompack | PostToolUse:Edit | 12 | 70 | 95 | 95 |
| qompack | PostToolUse:Glob | 6 | 80 | 114 | 114 |
| qompack | PostToolUse:PowerShell | 2 | 40 | 55 | 55 |
| qompack | PostToolUse:Read | 27 | 101 | 222 | 573 |
| qompack | PostToolUse:Write | 52 | 74 | 299 | 350 |
| qompack | SessionStart:compact | 20 | 113 | 152 | 278 |
| qompack | SessionStart:startup | 20 | 293 | 807 | 911 |
| qompack | Stop | 66 | 78 | 217 | 972 |

## By variant

Reported, not decided (preregistration section 8).

| variant | arm | trials | task success (95% CI) | constraint-clean (95% CI) | recovery (95% CI) |
|---|---|---|---|---|---|
| base | qompack | 16 | 14/16 = 0.88 [0.64, 0.97] | 16/16 = 1.00 [0.81, 1.00] | 6/8 = 0.75 [0.41, 0.93] |
| base | stock | 16 | 14/16 = 0.88 [0.64, 0.97] | 15/16 = 0.94 [0.72, 0.99] | 6/8 = 0.75 [0.41, 0.93] |
| changing-requirement | qompack | 4 | 4/4 = 1.00 [0.51, 1.00] | 4/4 = 1.00 [0.51, 1.00] | n/a |
| changing-requirement | stock | 4 | 4/4 = 1.00 [0.51, 1.00] | 4/4 = 1.00 [0.51, 1.00] | n/a |
| held-out | qompack | 6 | 5/6 = 0.83 [0.44, 0.97] | 6/6 = 1.00 [0.61, 1.00] | 1/2 = 0.50 [0.09, 0.91] |
| held-out | stock | 6 | 6/6 = 1.00 [0.61, 1.00] | 6/6 = 1.00 [0.61, 1.00] | 2/2 = 1.00 [0.34, 1.00] |

## Per-task sign (qompack − stock task success)

Reported, not decided.

- changing-requirement-format: 0
- changing-requirement-slug: 0
- constraint-naming: 0
- constraint-verbatim: 0
- decision-rationale: 0
- eliminated-approach: 0
- recall-user-fact: 0
- regression-guard: 0
- seed-recall: −1
- tool-output-recall: +1

## Failed or incomplete trials

- tool-output-recall/stock/2: completed=false plugin_expected=false plugin_loaded=false harness_error="step 3 of 4: no result within 10m0s"

## Notes

- trials are clustered within tasks; the pooled intervals treat them as independent, which overstates their precision (preregistration section 8)
