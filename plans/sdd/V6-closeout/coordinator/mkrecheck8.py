"""mkrecheck8.py <live-rerun-c7.js> <out.js> <candidate-sha> <bundle-dir>

Builds candidate 8's short live re-check (D59, D60(f)) from candidate 7's live lane: keeps the
COMMON brief, host facts and hard rules; pins the candidate and bundle; moves the branch to
closeout/live8 and the evidence to rerun-c8; and swaps RERUN and PARTS for the rows candidate 8
changed: UAT-05 run 2's loss notice at 150/150, UAT-06 with the fork's current work and C4.5's two
status reads, a status read after a mid-session compaction (D58(c)/(d)), UAT-12 sessions A and B
with C4.6 under a deny rule (punctuated denied names, a project path with a space, D60(c)), and
C4.9's settingsVersion leg (D59, LOUD once per daemon). Nine sessions; the other rows carry from
candidate 7 by the diff. The audit is narrowed to these rows plus D53(i).
"""
import sys

src, out, cand, bundle = sys.argv[1:5]
s = open(src, encoding="utf-8").read()


def swap(old, new, count=1):
    global s
    assert s.count(old) >= 1, old
    s = s.replace(old, new) if count == 0 else s.replace(old, new, count)


RERUN_PARTS = r"""const RERUN = `THIS IS CANDIDATE 8'S SHORT LIVE RE-CHECK (decisions D58, D59 and D60). Candidate 7's lane (d20309c0) passed most rows and failed UAT-05, UAT-12 with C4.6, and C4.5. Waves 18, 19 and 19b fixed those and the other findings. Read these first:
- plans/sdd/V6-closeout/live/report-c7.md: candidate 7's results, findings and audit;
- plans/sdd/V6-closeout/w18-passprogress/report.md, w19-*/report.md and w19b-*/report.md: what changed;
- the ledger's D58, D59 and D60 rows: each disposition.
Re-run ONLY the rows named in your part, on this candidate and bundle, exactly as docs/uat.md and the checklist state them in their CURRENT text (several were revised under D59 and D60).
Write evidence under ${LIVE}/rerun-c8/<row>/ and never overwrite earlier evidence. In docs/uat.md, replace each re-run row's Result block with this candidate's result. Keep the existing history lines and add one line: "Candidate 7 (d20309c0): <its verdict> — <one-line reason>, evidence plans/sdd/V6-closeout/live/rerun-c7/<row>/", unless that line is already there.
Judge every row against its CURRENT expectation. A defect that is still present, or a new one, is a finding with evidence, never excused. A host-reported hook failure or timeout is a finding under D53(i) unless docs/cannot-do.md or docs/upstream-issues.md documents it as host behaviour.
Commit after each row, so an interruption loses little. Log the guard snap state at the start of your part. At its end, remove any run-created ~/.qompack content under install.md §6 (its sha256 equals the bundle's bin, and no qompack.exe is running) and log that (D50).`

const PARTS = [
  { key: 'sessions', phase: 'Sessions', budget: 5, part: `${RERUN}
Scenario ids to return: UAT-05, UAT-06, C4.5, C4.3, F-C48-1. Every session uses --plugin-dir ${BUNDLE}, one disposable project per scenario, and the DEFAULT idle exit unless a step needs the daemon gone (then say so in the Result block).
UAT-05 run 2 (1 session; run 1 passed on candidate 7 and is not repeated): as the current text states it, at the 150/150 budget. A compaction that dropped material must inject the minimal loss notice: section 7 alone, naming the count of dropped items and the dropped() route, plus the original request's restore pointer when it fits (D59(b), ADR 0011 §23). Nothing may be silent. Record the payload, the drop report and LOUD.log. Also step 5's runtime.rehydrate.* change reaching the RUNNING daemon without a restart (record the reload line and the next block's budget).
UAT-06 (3 sessions): compact, then --resume, then --resume --fork-session with a correction. The fork's first block carries the parent's original AND the correction in force; its "Current work" comes from the fork's own newest prompt, never an inherited parent prompt (D59, forkwork). The parent's session-scoped elimination and decision are visible in the fork (checkpoints, already_tried, why). After --resume, the session's second checkpoint still carries its earlier decision.
C4.5 (no extra session): with two or more sessions in the UAT-06 store, read \`qompack status --json\` twice with no state change between and diff them: they must agree exactly, sessions listed in one stable order (D53(a), D59(c)). Also read status and doctor --json once and check that the banner counts no pending row as holding.
F-C48-1 and the drain (1 session, D58(c)/(d)): a session with a few captures, a mid-session /compact, then one more turn. After it, \`qompack status\` must show session_start.fires holding ('same-session-restart' or marker present, never pending), and 0 pending captures once the daemon has drained (record the spool listing and status). This is the owner's stated re-check: compact, then status shows 0 pending.
C4.3: judge from these rows (bounded payloads, correct current authority, Qompack-sourced recovery).` },
  { key: 'retrieval', phase: 'Retrieval', budget: 2, part: `${RERUN}
Scenario ids to return: UAT-12, C4.6. Every session uses --plugin-dir ${BUNDLE}, in fresh disposable projects.
UAT-12 sessions A and B (2 sessions; reuse candidate 7's recipe, rerun-c7/UAT-12/, without the upgrade leg). Put the project in a scratch directory whose name contains a space (for example "${SCRATCH}/retrieval/c8/uat12 space/proj"), and add to the deny rules a file whose name holds punctuation, for example private/deny (1).txt and private/John's notes.txt, beside private/deny.txt. In session A, read, grep and recall around the denied files (a path: selector naming each, a basename selector, a glob) so their argument summaries are captured. After /compact in session B:
  - section 6 shows no host-denied path (plain, punctuated, relative or absolute, behind a selector or glob) and no absolute out-of-project path; such summaries read "summary withheld" and the pointer keeps its id and hash (D50, D60(c));
  - in-project absolute paths ARE shown even though the project path contains a space (D60(c) V4);
  - section 7's drop entries withhold the same paths;
  - every retrieval form (recall, expand, re_read, dropped) refuses or redacts the denied paths; dropped() lists a denied path in a checkpoint drop kind redacted, not removed (D60(c)(iii));
  - paging, recall at the default k and the LOUD.log checks as the current text states them.
C4.6: judge from UAT-12 (every retrieval form and the rehydration block refuse the denied paths).` },
  { key: 'resilience', phase: 'Recovery', budget: 2, part: `${RERUN}
Scenario ids to return: C4.9, D53(i). Every session uses --plugin-dir ${BUNDLE}.
C4.9 (b) only (1-2 sessions): a newer runtime.migration.settingsVersion in the project config. The documented refusal or degradation and no broken session; hooks log the reset at warn in the day log, and LOUD.log carries at most one such line per daemon, never one per hook process (D59, loudcfg). backup create/verify/restore refuse with exit 1 and the documented message while it is in force (docs/backup.md). Record LOUD.log, the day log and the outputs.
D53(i) (no session, last): over EVERY session of this re-check (all of ${LIVE}/rerun-c8/*/ holding a meta.json or hooks.json), list every host-reported hook failure or timeout (a hook_response with a non-zero exit code, an error, blocked or timeout outcome, or a hook the host's stderr names as failed), each with its session and evidence file, and the total, plus host-seen latency per hook event (n, median, p95 and max over measured pairs; unmeasured pairs counted apart). Write ${LIVE}/rerun-c8/D53i/summary.md and data.json. Numbers only; candidate 7's C5.6 figures stand (D58(e)).` },
]
"""

a = s.index("const RERUN = `")
b = s.index("\n]\n", s.index("const PARTS = [")) + 3
s = s[:a] + RERUN_PARTS.strip() + "\n" + s[b:]

swap("name: 'v6-closeout-live-rerun-c7'", "name: 'v6-closeout-live-recheck-c8'")
swap("description: 'Phase 4: agent-run UAT-01..12, C4.1-C4.11, C1.6/C1.7 and C5.6 on the frozen candidate, sequential real sessions, then an evidence audit'",
     "description: 'Candidate 8 live re-check (D59, D60): UAT-05 notice, UAT-06/C4.5, compact then 0 pending, UAT-12/C4.6 under deny rules, C4.9 settingsVersion; then an evidence audit'")
swap("const CAND = 'd20309c03ffc364e4cc48663be73cfbb1f2309b2'", f"const CAND = '{cand}'")
swap("const BUNDLE = 'C:/Users/Quant/Documents/Programming/Projects/qompack-bundles/c7/qompack-plugin-0.3.0-windows-amd64'",
     f"const BUNDLE = '{bundle}'")
swap("closeout/live7", "closeout/live8", 0)
swap("owner decisions D1-D57", "owner decisions D1-D60")
swap("THIS IS A RE-RUN of the rows candidate 4 failed or left open plus D53(f)'s install, upgrade and restore rows (decisions D52, D53); evidence is under ${LIVE}/rerun-c7/. Coverage: for each of C4.1, C4.3, C4.4, C4.5, C4.6, C4.8, C4.9, C1.7 and D53(i)",
     "THIS IS CANDIDATE 8'S SHORT RE-CHECK of the rows candidate 7 failed and the rows waves 18, 19 and 19b changed (decisions D58, D59, D60); evidence is under ${LIVE}/rerun-c8/. Coverage: for each of C4.3, C4.5, C4.6, C4.9, F-C48-1 (status after a mid-session compaction reads session_start.fires holding and 0 pending) and D53(i)")
swap("For every re-run UAT Result block (UAT-01, 03, 04, 05, 06, 09, 10, 12), and the release entry's observed slash-command and MCP-tool namespace against docs/install.md and docs/commands.md in docs/uat.md:",
     "For every re-run UAT Result block (UAT-05, 06, 12) in docs/uat.md:")
swap("C1.6: is \"automatic recovery\" stated honestly? ", "")
for stale in ("live7", "const PREV", "rerun-c7/C5.6"):
    assert stale not in s, stale
open(out, "w", encoding="utf-8", newline="\n").write(s)
print(out)
