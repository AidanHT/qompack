# Live evaluation `qompack-live-v7-harness-test`

Test-only (V7 W6): the pilot task in the four-arm Stage A shape, for the harness tests. It is never run against a real host, and its outcomes would say nothing about the plugin.

Written by `devtool live-eval --describe` from the files and settings a run with these flags reads. It started nothing.

## Run

- Task set: `../../testdata/eval/live/tasks-v7-harness-test.json` (sha256 `f952d96c63e9f4177f4bc3ac8f63412e70169faa1c25e7ae094c2160909a0d28`), 1 task(s) declared, 1 selected; held-out tasks left out.
- Fixture tree [fixtures hidden], manifest sha256 `30cf769d243776645506eb43b7e0b336d2fed9a6e2ea29b4fb70a1f2b4054276`.
- Model: `claude-opus-5-5` (pre-registered `claude-opus-5-5`).
- Host: Claude Code 2.1.280 at `claude-fake`, pinned: before every trial the host's version is compared with this one and a difference stops the run; every session and every `claude` command runs with `DISABLE_AUTOUPDATER=1`.
- Plugin install: `plugin-dir`.
- Daemon idle exit: 1800 s; step timeout 10m0s; session timeout 45m0s; max sessions no cap.
- Rate table for the list-price estimate: 2026-09-22.
- Known open defects in the bundles: none (the operator's statement, not machine-checked).
- Pre-registered confirmatory design: not met (its task set qompack-live-v7-harness-test has no pre-registration; it excluded the held-out tasks).

## Pacing

- Five-hour gate 0.90: no trial starts while the newest `rate_limit_event` puts the five-hour window at or above it, or rejected; the run waits for its reset (plus 1m0s) when that falls inside the night window, and pauses otherwise.
- Seven-day ceiling 0.60: while any seven-day window is at or above it, or rejected, the run pauses until it resets (the owner keeps 40 %).
- Night window: 22:00-07:00; outside it no trial starts and the run pauses.
- Paid overage: any event with `isUsingOverage` or `overageInUse` true stops the run.
- A trial a limit cuts short (a rejected event, or a usage-limit result) is set aside as `trial.limited-<n>.json`, excluded from the analysis and run again in its slot; a pause is a clean exit, and `devtool live-eval --resume <run-dir>` continues the plan.

## Arms

| arm | trials per task | plugin | bundle | mod enabled | mod steering | CLAUDE.md compact section | store before the session |
|---|---|---|---|---|---|---|---|
| stock | 1 | no | none | no | no | no | no |
| classic | 2 | yes | `--bundle` | no | no | no | yes |
| generic | 1 | yes | `--bundle` | no | no | yes | yes |
| steer | 2 | yes | `--bundle-mods` | yes | yes | no | yes |

Each arm's session environment, set by the harness:

- stock: `ENABLE_CLAUDEAI_MCP_SERVERS=false` `DISABLE_AUTOUPDATER=1` `QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=1800`
- classic: `ENABLE_CLAUDEAI_MCP_SERVERS=false` `DISABLE_AUTOUPDATER=1` `QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=1800` `QOMPACK_RUNTIME__MOD__ENABLED=false` `QOMPACK_RUNTIME__MOD__STEERING=false`
- generic: `ENABLE_CLAUDEAI_MCP_SERVERS=false` `DISABLE_AUTOUPDATER=1` `QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=1800` `QOMPACK_RUNTIME__MOD__ENABLED=false` `QOMPACK_RUNTIME__MOD__STEERING=false`
- steer: `ENABLE_CLAUDEAI_MCP_SERVERS=false` `DISABLE_AUTOUPDATER=1` `QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS=1800` `QOMPACK_RUNTIME__MOD__ENABLED=true` `QOMPACK_RUNTIME__MOD__STEERING=true`

`--bundle` (classic, generic): Qompack 9.9.9-test at commit `deadbeef` (dirty false), mod sources absent, BUNDLE.json sha256 `<bundle sha256>`, at `<bundle dir>`.

`--bundle-mods` (steer): Qompack 9.9.9-test at commit `deadbeef` (dirty false), mod sources present, BUNDLE.json sha256 `<bundle-mods sha256>`, at `<bundle-mods dir>`.

The generic arm's project root `CLAUDE.md` gains this section (created when the fixture has none, appended after a blank line otherwise), text G from the hashed task set:

````markdown
# Compact instructions

When compacting, reproduce verbatim every identifier, file path, number, error message and every rule or constraint the user stated; list the approaches that were tried and failed, with the reason; keep the user's latest request word for word.
````

## Tasks

### `pilot-codeword`: Harness validation: a program's output recalled after /compact

Category `harness-validation`, variant `base`, domain `harness-validation`, held out no. Fixture `fixtures/pilot`; hidden fixture `hidden/pilot`, copied over the project after the session and before any check. At most 6 turns per message; allowed tools: `Read`, `Edit`, `Write`, `Glob`, `Grep`, `Bash(go:*)`, `Bash(git status:*)`, `Bash(git diff:*)`, `PowerShell(go:*)`, `mcp__plugin_qompack_qompack`.

Steps, each sent by every arm:

1. `run`: the prompt

   ```text
   Run go run ./cmd/code and tell me the code word it prints, in one line.
   ```
2. `compact`: a compaction, sent as `/compact Keep the code word the program printed.`
3. `write`: the prompt

   ```text
   Without running any program again, write the code word the program printed into CODE.txt at the project root, then tell me the code word in one line.
   ```

Checks, graded by a program after the session:

- `answer-before` (recovery): the answer to step `run` matches `2c1a7a62c3` — the code word was read before compaction.
- `code-file` (recovery): the file `CODE.txt` matches `^\s*2c1a7a62c3\s*$` — CODE.txt holds the code word.
- `answer-after` (recovery): the answer to step `write` matches `2c1a7a62c3` — the code word is recalled after compaction.
- `no-rerun` (constraint): no tool call after step `compact` matches `(?i)cmd/code|go run` (matched against the tool's name and input JSON; Qompack's own lookups excepted) — the code word was not re-derived.
- `gomod` (constraint): the file `go.mod` is byte-identical to the fixture's — go.mod is unchanged.
- `hidden` (task): `go test ./...` exits 0 in the project root — the hidden test reads CODE.txt.

## Trial order

6 trial(s) in balanced blocks of rounds, shuffled within each block from order seed 11:

1. pilot-codeword / steer / 1
2. pilot-codeword / stock / 1
3. pilot-codeword / generic / 1
4. pilot-codeword / classic / 1
5. pilot-codeword / classic / 2
6. pilot-codeword / steer / 2
