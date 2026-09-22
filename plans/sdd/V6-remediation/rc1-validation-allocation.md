# Candidate 65bc8d7 validation allocation

Declared 2026-09-22 before final candidate outcomes. Source:
`65bc8d7707fed78e1b7344c2d23d5fa948a0a9c1`, clean detached Windows worktree
`qompack-v6-rc1`; immutable Linux source `rc1-linux-20260922.json`.
No tag or published release is implied by the candidate name.

| Run | Reason and coverage | Limit |
|---|---|---|
| `rc1-integrated-whole-tree` | One cumulative `go run ./tools/devtool test`, including package, fault, security, docs, platform and e2e consumers of the integrated corrections | GOMAXPROCS=2, ordinary product binaries, declared whole-tree co-load. Retain unresolved carry guards and every failure/skip; no quiet cost certification |
| `rc1-linux-child-race` | One complete eight-case CI-selected set on current source after the MCP/zombie corrections | Actual child instrumentation and SHA inspected, race reports captured; Docker/WSL, not hosted CI or installed Claude host |
| `rc1-bundle-windows-linux` | Preserve Windows/amd64 and Linux/amd64 bundles/archives with source identity; use supported installed CLI manifest validation on the Windows host target | Validation of manifest is not a real interactive installed-host session; cross-build is not Linux host certification |
| Focused reruns after failures | Only the affected assertion and its dependencies, with original failure preserved | A new source or artifact needs a new identity; no old PASS is copied |

The whole-tree package harnesses assemble their own identified host bundle once
per package. Their outputs are collected under separate subdirectories of
`rc1-whole-tree-artifacts`; inspect the embedded bundle hash before reuse. The
standalone bundles are retained for later installed/compatibility workflows and
are not silently substituted for a harness's recorded executable.

## Performance allocation, pending a quiet window

Do not run these while whole-tree, race, other builds or authoring are active.
Record process load, platform/toolchain and binary identity; unavailable quiet
conditions leave the cost gate incomplete. Keep prior co-loaded and historical
CI failures. The existing committed platform budgets are unchanged.

1. Run `go run ./tools/devtool bench-hotpath --iterations 5000 --hook observe-tool
   --warm-daemon --json <new-artifact>` once on each available target, without
   `--under-coload`. The 5,000 spawned samples match the existing nightly workload;
   record the harness's actual per-row sample populations, missing telemetry,
   failures and full distributions. This is not a universal latency guarantee.
2. Measure the carried store/read/search, observer 256 KiB, checkpoint Finalize,
   and negative-knowledge Open workloads separately using their existing benchmark
   definitions, `-run ^$ -bench <exact-names> -benchmem -count 5`, default one-second
   benchmark calibration. Preserve all five repetitions and compare like workloads
   on the same target. These repetitions characterize gross regressions; they do
   not establish a two-percent effect or task-quality noninferiority.
3. Microbenchmark means are not p99. Historical machine/clock and implementation
   changes remain labeled. The hard hot-path limits and the unresolved carried
   limits remain independent from functional correctness and model task quality.

No held-out model trial margin or sample size is inferred from this performance
allocation. Those require a separate declared task set, product-model identity,
usage completeness and pre-outcome trial design. Product-model/provider and
spending-limit information has been requested while automated work continues.
