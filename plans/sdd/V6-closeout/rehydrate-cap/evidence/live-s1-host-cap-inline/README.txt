C1.14 (owner decision D5) on the real host: a rehydration that the pre-D5 build renders over the
host's 10,000-character additionalContext cap now arrives INLINE, and its overflow pointer resolves
through the qompack MCP tools. One real headless session (the only one this workstream used of its
two), Claude Code 2.1.280 on windows/amd64, model claude-haiku-4-5-20251001, 2026-09-23T03:40Z.

Bundle: `go run ./tools/devtool bundle --target windows/amd64 --version 0.1.0-c114` from commit
d61c8c6 (BUNDLE.json: source.commit d61c8c6404f6091022ca7169b13347c7da0967d0, dirty false), loaded
with --plugin-dir. Driver: one `claude -p --input-format stream-json --output-format stream-json
--verbose --include-hook-events` process, one user message at a time (driver-config.json, meta.json):
  --model claude-haiku-4-5-20251001 --max-turns 4 --session-id 374bb22a-… --setting-sources
  project,local --permission-mode dontAsk --allowedTools mcp__plugin_qompack_qompack__expand,
  mcp__plugin_qompack_qompack__dropped; ENABLE_CLAUDEAI_MCP_SERVERS=false.

Seeding (deterministic, no daemon involved): five ~370-character user pins written through the real
pins store (internal/pins Store.Add, run from a throwaway program; `qompack pin` reports "no pin store
is wired into this build"). Nothing else was seeded.

Messages:
  1. A 7,816-byte first prompt (78 lines) whose second line is "The deployment codeword is
     TEAL-OSPREY-42.", asking only for "OK".
  2. /compact  (PreCompact wrote checkpoint 0001; SessionStart:compact answered)
  3. "Qompack just rehydrated your context … (1) inline or saved file path? (2) section 7 names the
     exact call that restores the verbatim original user intent: make exactly that call, passing
     full=true. (3) Quote the deployment codeword exactly as that call's result shows it."
Exit 0; 3 result events (1, 0 and 3 turns); total cost $0.117; 33 s wall. stderr empty.

Observed (transcript-facts.json, injected-additionalContext.txt, stream.jsonl):
- The SessionStart(compact) additionalContext Claude received was 3,048 UTF-16 units, inline: no
  <persisted-output>, no "Output too large", anywhere in the transcript. It carried the five pinned
  invariants whole, section 7, and section 8. The verbatim original prompt (quoted, ~8,000
  characters) did not fit beside them and was left out whole; section 7's first line names it:
  "user_intent tier1 — OVERFLOW: the verbatim original user intent did not fit the rehydration
  payload and is emitted whole or not at all; restore: expand(tool_use_id=prompt_374bb22a-…_0)".
- The model loaded the tool (ToolSearch) and called
  mcp__plugin_qompack_qompack__expand {"tool_use_id": "prompt_374bb22a-…_0", "full": true}; the
  result (8,261 characters, found:true, tool UserPromptSubmit, span [0,7816], not truncated)
  contains TEAL-OSPREY-42, and the model answered "(1) … reached me inline within the
  system-reminder block, not as a file path. … (3) The deployment codeword is TEAL-OSPREY-42."
- LOUD.log holds exactly one line, the rehydrator's own tier-1 overflow notice (spent_chars 2436 of
  a 9,400 ceiling at that point); no "hook output exceeds the host's per-field cap" line. The
  persisted state (rehydrate-state.json) records the same drop entries dropped() answers from.

"Previously would exceed" (replay-summary.json): the session's own post-session project was copied
twice and the same SessionStart(source=compact) event for this session id was replayed through a
startup + compact pair of `qompack session-start` calls:
  - base binary, built from 32e1a37 (the pre-change base): additionalContext 10,882 UTF-16 units,
    over the host cap, and the hook client's Loud line "hook output exceeds the host's per-field
    cap … chars=10882 cap=10000 preview=2000" (replay-base-additionalContext.txt);
  - the D5 binary: 3,048 units, identical to what the host delivered in the session
    (replay-new-additionalContext.txt).
Every daemon these runs started was stopped by PID afterwards (only processes launched from this
workstream's scratch binaries).

Home hygiene (home-state.txt): ~/.claude/settings.json and every ~/.claude/plugins/*.json hash
identical before and after; the run's own plugins/data/qompack-inline/, session-env/<id>/ and
projects/<scratch-encoded>-live-project/ (transcript, copied here as transcript-facts.json first)
were removed by name. Local paths in every file here are redacted to <scratch> and <home>.

Also visible, not C1.14's: a "pointer_git_unavailable" drop line from the checkpointer, because the
scratch project's `git init` left no index file.
