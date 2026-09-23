Reviewer finding 2 - the host's 10,000-character additionalContext cap, observed on the real host.
One real headless session - the workstream's third of its six (live-s1 and live-s2 were the
implementer's; this is the fix seat's only one), Claude Code 2.1.280 on windows/amd64, 2026-09-23T00:24Z.

Probe plugin (plugin/): a SessionStart hook that prints out.json, whose additionalContext is 11,082
characters: "CAPPROBE-HEAD codeword: amber-falcon." first, then filler lines, then
"CAPPROBE-TAIL codeword: violet-harbor." at offset 11,043 - past the cap and far past the preview.
Shell form is deliberate here (Git Bash is installed); the probe tests the cap, not the launcher.

Command (cwd <scratch>/capprobe/project; ENABLE_CLAUDEAI_MCP_SERVERS=false):
  claude -p "Your session-start context should contain lines that begin with CAPPROBE and name a
    codeword. Quote every CAPPROBE codeword you can actually see in your context, verbatim. If instead
    of the full text you see a file path or a truncated preview, say so and quote the path. Do not use
    any tools. Answer in at most three short lines." --model claude-haiku-4-5-20251001 --max-turns 1
    --output-format stream-json --verbose --include-hook-events --setting-sources project,local
    --permission-mode dontAsk --plugin-dir <scratch>/capprobe/plugin
Exit 0; one turn; $0.020.

Observed (transcript-facts.json, stream.jsonl):
- The hook exited 0 and the host accepted its output (no validation error).
- What Claude received as the SessionStart hook_additional_context was 2,391 characters:
  "<persisted-output>\nOutput too large (10.8KB). Full output saved to: <home>\.claude\projects\
  <scratch-encoded>-capprobe-project\<session>\tool-results\hook-...-1-additionalContext.txt\n\n
  Preview (first 2KB):\nCAPPROBE-HEAD codeword: amber-falcon. ..." - it contains the head codeword
  and NOT the tail codeword.
- The model answered: "I can see the CAPPROBE output is saved to a file (output too large). From the
  preview I can see: CAPPROBE-HEAD codeword: amber-falcon" - it never saw violet-harbor.

This is the documented behaviour, now observed: over 10,000 characters, Claude gets a path and a
2 KB preview, and the rest of a Qompack rehydration payload would not reach the model unless it
chooses to read the file. home-state.txt records the cleanup.

Owner decision (open, C1.12 review finding 2): the rehydration payload is budgeted in tokens
(runtime.rehydrate.maxTokens 12000, Qompack.md §8.6 "target 8-12K", ADR 0011), several times the
host's 10,000-character cap. Either hold the rendered payload under the cap (contradicts §8.6/ADR 0011)
or accept that a large rehydration reaches Claude as a path plus a 2 KB preview. Until decided, the
hook client passes the field through whole and records one Loud line per overrun with sizes only
(internal/hookio HostCapOverruns, internal/cli TestHookOutput_OverTheHostCapIsLoud). It does not
truncate: that would lose the tail the host at least keeps in a file, drop report included.
