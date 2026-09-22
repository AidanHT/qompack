# Marketplace-flow rehearsal and pilot sessions (C5.4 fix seat, 2026-09-22)

All agent-executed on the owner's host (Windows 11, Claude Code 2.1.280), per owner decision D3.

## 1. Rehearsal without a session (18:43 local)

Before spending a real session on the marketplace flow, the fix seat ran the four plugin
subcommands the driver uses — no model call — in a scratch project, with a read-only fingerprint of
the operator's configuration (`settings.json`, `plugins/installed_plugins.json`,
`plugins/known_marketplaces.json` by SHA-256; the entry names of `plugins/cache/`,
`plugins/marketplaces/`, `plugins/data/`) taken before and after each step. Bundle
`qompack-plugin-0.3.0-live.pilot-windows-amd64`, assembled from clean commit `251ec8b` with
`go run ./tools/devtool bundle --target windows/amd64 --version 0.3.0-live.pilot --out <scratch>/bundle`.

| Step (cwd = scratch project) | Host output | What changed in the operator's configuration |
|---|---|---|
| `claude plugin marketplace add <mkt> --scope local` | `Successfully added marketplace: qompack-live-eval (declared in local settings)` | project `.claude/settings.local.json` gained `extraKnownMarketplaces`; **`plugins/known_marketplaces.json` changed** (`1b1eda7e…` → `752723ed…`): `--scope local` still registers the marketplace globally |
| `claude plugin install qompack@qompack-live-eval --scope local -y` | `Successfully installed plugin: qompack@qompack-live-eval (scope: local)` | `plugins/installed_plugins.json` changed (`728f7559…` → `c1594326…`, an entry with `scope: local` and this `projectPath`); `plugins/cache/qompack-live-eval/` created |
| `claude plugin uninstall qompack@qompack-live-eval --scope local -y` | `Successfully uninstalled plugin: qompack (scope: local)` | `installed_plugins.json` back to `728f7559…` byte-for-byte |
| `claude plugin marketplace remove qompack-live-eval` | `Successfully removed marketplace: qompack-live-eval` | `known_marketplaces.json` back to `1b1eda7e…` byte-for-byte; **`plugins/cache/qompack-live-eval/qompack/0.3.0-live.pilot/` remained**, marked `.orphaned_at` (`1790117032484`) |

The remaining cache version was byte-identical to the rehearsal bundle (`BUNDLE.json` compared with
`cmp`) — this run's own copy, left for the host's later sweep — and was removed; the fingerprint
then matched the before-state exactly. That finding became commit `09a5222`
(`fix(devtool): remove the orphaned cache a marketplace trial leaves`): the driver removes that
directory only when the trial created it, the host marked every version orphaned, and every version
is this run's bundle; otherwise it is a leftover and the run stops.

## 2. Pilot sessions (`testdata/eval/live/pilot.json`, run after the pre-registration commit `31b1d40`)

Model `claude-haiku-4-5-20251001` (the pilot set's own model), one trial each, `--max-sessions 1`,
same bundle. Command form:

```
QOMPACK_LIVE_EVAL=1 go run ./tools/devtool live-eval --tasks testdata/eval/live/pilot.json \
  --arms <arm> [--install plugin-dir|marketplace --bundle <scratch>/bundle/qompack-plugin-0.3.0-live.pilot-windows-amd64] \
  --out plans/sdd/V6-closeout/eval/runs/<dir> --max-sessions 1 --step-timeout 8m --session-timeout 20m
```

| Directory | Arm / install | Outcome | Plugin evidence | Guard |
|---|---|---|---|---|
| `pilot1-plugin-dir/` | qompack / `--plugin-dir` | completed, task ✓, 0 violations, recovery ✓ | loaded; MCP `connected`; 1 checkpoint; 1 rehydration injected (583 B); daemon stopped by `admin.shutdown` | unchanged |
| `pilot2-marketplace/` | qompack / local-scope marketplace | completed, task ✓, 0 violations, recovery ✓ | loaded (host version `0.3.0-live.pilot`); MCP `connected`; 1 checkpoint; 583 B injected; daemon stopped; orphaned cache removed | unchanged (and an independent before/after fingerprint agreed) |
| `pilot3-stock/` | stock | completed, task ✓, 0 violations, recovery ✓ | — | unchanged |

Both qompack pilots recorded the **C1.12** defect as a hook problem (`PreCompact` `output_rejected`,
from the `/compact` turn's local output) and `SessionEnd … failed: Hook cancelled` from the host's
stderr. No permission denials: the task set's `Bash(go:*)` and `PowerShell(go:*)` rules covered the
model's `go run` in both shells (pilot 1 used PowerShell, pilot 2 Bash).

These are harness-validation runs on a trivial task (the stock arm recovered the code word from the
host's own summary as well as the plugin arms did). They are not evidence about the plugin's value
and are never pooled with `qompack-live-v1` trials.

Session budget: the workstream's cap was six short real sessions — smoke1, smoke2, smoke3 (the
implementer) and pilot1, pilot2, pilot3 (the fix seat). The rehearsal in §1 made no model call.

Observed while running: `plugins/data/qompack-inline` and `plugins/data/qompack-shell-probe-inline`
were present in the operator's configuration when pilot 1 started and gone by pilot 2 — created and
removed by another close-out lane's sessions on the same machine. The guard compares only presence
of `plugins/data/qompack*` and never removes a directory that existed before a trial, but a
concurrent lane creating or removing one mid-trial would stop a run (fail-closed) for a change it
did not make; the operator then re-runs.
