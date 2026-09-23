# C5.4 run evidence — provenance

Everything here is **agent-executed on the owner's real installed host** (Windows 11, Claude Code
CLI 2.1.280 logged in on the owner's subscription), per owner decision D3. None of it is human UAT.
Paths inside the files are the original scratchpad paths of the session that ran them and are kept
verbatim as evidence; they will not resolve on another machine and were not rewritten.

## Harness-validation smoke sessions (before the driver existed)

Run 2026-09-22 between 17:26 and 17:37 local time by the first C5.4 implementer instance (workflow
`wf_16dd5d95-b3a`, eval lane) with the scratch driver `smoke_driver.py` (one `claude -p` streaming-input
process, one user message per turn, each sent after the previous turn's `result`). Model
`claude-haiku-4-5-20251001`, `--max-turns 4`, `--setting-sources project,local`,
`--permission-mode dontAsk`, `--allowedTools Read`, `--include-hook-events`.

| Directory | Arm | Compaction | Bundle |
|---|---|---|---|
| `smoke1-stock-compact/` | no plugin | `/compact` turn | — |
| `smoke2-plugin-dir-compact/` | `--plugin-dir` | `/compact` turn | `qompack-plugin-0.3.0-eval.smoke-windows-amd64` |
| `smoke3-plugin-dir-autocompact/` | `--plugin-dir` | automatic, forced by `CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000` + `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=30` | same bundle |

The bundle was assembled from this worktree (base `cf31e01` plus the uncommitted work of the time)
with `go run ./tools/devtool bundle --target windows/amd64 --version 0.3.0-eval.smoke --out <scratch>/bundle`
(`bundle.log` in the scratchpad recorded the one-line output; the bundle was not kept).

What each shows:

- **smoke1** — the host's usage shape: one `result` per user message, `/compact` as a local command
  with zero `usage` and a jump in the running `modelUsage` total, per-content-block assistant lines
  that repeat one message's usage. The recorded fixture of `internal/eval` tests.
- **smoke2** — the full Qompack round trip in one headless session: PreCompact wrote checkpoint 0001
  (`checkpoint-0001.json`), `SessionStart:compact` injected the rehydration block, and the model
  answered after compaction. It is also the evidence of the **C1.12** defect: Claude Code rejected the
  PreCompact hook's `hookSpecificOutput` ("Hook JSON output validation failed — hookSpecificOutput.
  hookEventName: expected one of …") and replayed the rejection into the post-compaction context.
- **smoke3** — automatic compaction can be forced headless (three `trigger: auto` boundaries and one
  refused attempt, `too_few_groups`); four checkpoints were written, one per PreCompact including the
  refused attempt (`checkpoints-MANIFEST.jsonl`). The PreCompact rejection is **not visible** on an
  automatic compaction — no local-command output, nothing in the transcript — so the harness can
  only detect C1.12 on a `/compact` turn. stderr shows `SessionEnd hook [...] failed: Hook cancelled`.

**What the smoke sessions did not do:** they took no before/after record of the operator's
`~/.claude` configuration except `smoke3`, whose `homeguard.py` snapshot (`guard.json`, SHA-256
`5088c069…6c04`, not committed: it lists the operator's installed plugins) was taken before the run;
no check result was preserved. The `--plugin-dir` sessions created `~/.claude/plugins/data/qompack-inline`,
which `homeguard.py` removed afterwards (the directory was absent at the time of this README).
`devtool live-eval` now guards every trial and fails the run closed (see the report).

A second copy of the eval agent, resumed by accident at ~17:31, stopped one of these sessions'
trial daemons (PID 21172, smoke2's) that it had not started; the smoke2 evidence had already been
captured. Its files are preserved in `duplicate-instance-files/` (renamed `.go.txt` so the Go tool
does not compile them); see `../COORDINATOR-DECISION.md`.

## Transcript facts

`transcript-facts.json` files are extracted from the host's transcript JSONL with the operator's
identity (`session_context`), the system prompt (`prompt_snapshot`) and message bodies left out.
Raw transcripts are never committed.

## Test logs

`tests/` holds the focused and full-package test output of the fix seat's runs.
