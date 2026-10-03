"""mkrecheck8.py <live-rerun-c7.js> <out.js> <candidate-sha> <bundle-dir>

Builds candidate 8's live re-check (D59, D60(f), D61) from candidate 7's live lane: keeps COMMON's
host facts and hard rules, pins the candidate and bundle, moves the branch to closeout/live8 and the
evidence to rerun-c8, and replaces the launch header, meta, RERUN and PARTS. Four parts, strictly in
this order, then the evidence audit:

  sessions    6 = 5 + 1 spare   UAT-05 run 2's loss notice at 150/150 (1), UAT-06 with the fork's
                                current work (3), C4.5's two status reads (no session), F-C48-1:
                                status after a mid-session compaction reads holding and 0 pending
                                (1), whose last turn is a /compact, so UAT-09 O-1 (does SessionEnd
                                reach the daemon after a /compact?) is recorded in it
  retrieval   5 = 3 + 1 spare   UAT-12 sessions A and B with C4.6 under deny rules (2; punctuated
                + 1 deny proof  denied names with ( ) ' , and =, a project path with a space), and
                                session C: 40 or more Bash and MCP tool calls, then /compact, which
                                must inject the rehydration block, never the deferred note, with
                                SessionStart:compact's host-seen latency recorded (1). The deny
                                rules must be proven in effect before each project is relied on;
                                the extra session pays for one settings file the host ignored
  carried     8 = 7 + 1 spare   UAT-02 with C4.2 (1), UAT-07 (1), UAT-08 (2), UAT-11 (1) and C4.7's
                                two kill switches (2): none ran on candidate 7 (their Result blocks
                                report candidate 4; C4.7's last run is candidate 3's), and waves
                                15-20 changed the code they exercise. Sized from candidate 4's
                                sessions.tsv rows for them (sessions-c4, retrieval-c4) and candidate
                                3's two C4.7 sessions
  resilience  4 = 3 + 1 spare   C4.9 (b)'s settingsVersion leg (1-2), C1.6: a daemon ended
                                mid-session by this lane, two publication-gap cuts, restart, the
                                startup accounting line and fsck before and after (1; no live C1.6
                                run since candidate 3, D59), then D53(i) over every session of the
                                re-check (no session, last)

23 real sessions in all. A spare session only replaces a void or invalid one (a usage limit, an API
outage, a settings file the host ignored), and the part says which and why.

Dependencies, checked here before anything is written:
  - the candidate is frozen and resolvable from this repository;
  - ADR 0011 at the candidate has its section 23 (the loss notice and the argument-summary gate,
    D59(b), D60), which wave 19b wrote and wave 19c rewrites. The parts cite "ADR 0011 section 23
    and any later amendment"; generate only once wave 19c's ADR is merged into the candidate, and
    re-read the generated text against the candidate's ADR before launch;
  - the bundle directory holds BUNDLE.json.
The generated script is then checked: no candidate 7 strings left, no Date.now, no TypeScript, and
its body parses as an async function (node). The coordinator creates or verifies branch
closeout/live8 at the candidate in ../qompack-cx-live before launch (the script's header says how),
and every part's first step refuses to run on any other branch or base.
"""
import os
import re
import subprocess
import sys

if len(sys.argv) != 5:
    sys.exit(__doc__)
src, out, cand, bundle = sys.argv[1:5]
here = os.path.dirname(os.path.abspath(__file__))
ADR = "docs/adr/0011-rehydration-budget-and-item-order.md"


def git(*args):
    return subprocess.run(["git", "-C", here] + list(args), capture_output=True, text=True, encoding="utf-8")


if not re.fullmatch(r"[0-9a-f]{40}", cand):
    sys.exit("mkrecheck8: the candidate must be a full 40-character commit SHA, not %r" % cand)
if git("cat-file", "-e", cand + "^{commit}").returncode != 0:
    sys.exit("mkrecheck8: candidate %s is not a commit in this repository" % cand)
adr = git("show", "%s:%s" % (cand, ADR))
if adr.returncode != 0:
    sys.exit("mkrecheck8: %s is missing at the candidate" % ADR)
if not re.search(r"^## 23\. .*\bloss\b", adr.stdout, re.M):
    sys.exit("mkrecheck8: ADR 0011 at the candidate has no section 23 on the loss notice; wave 19b/19c's "
             "ADR is not merged into %s, so the parts' citations would not hold. Merge it, re-freeze, then generate." % cand[:12])
if not os.path.isfile(os.path.join(bundle, "BUNDLE.json")):
    sys.exit("mkrecheck8: %s holds no BUNDLE.json; pass the frozen windows-amd64 bundle directory" % bundle)

s = open(src, encoding="utf-8").read()


def swap(old, new, count=1):
    global s
    assert s.count(old) >= 1, old
    s = s.replace(old, new) if count == 0 else s.replace(old, new, count)


HEADER = """// Candidate 8's live re-check (D59, D60(f), D61), generated by coordinator/mkrecheck8.py from
// candidate 7's lane (live-rerun-c7.js). Candidate __CAND__, bundle __BUNDLE__.
// Before launch, the coordinator (never an agent), with the owner's go and no other lane running:
//   1. git -C ../qompack-cx-live status --porcelain               -> empty
//   2. if branch closeout/live8 does not exist:  git -C ../qompack-cx-live switch -c closeout/live8 __CAND__
//      if it exists:  git rev-parse closeout/live8 == __CAND__, then git -C ../qompack-cx-live switch closeout/live8
//   3. git -C ../qompack-cx-live rev-parse HEAD == __CAND__, and __BUNDLE__/BUNDLE.json exists
// Every part's first step refuses to run on another branch or base. Parts run strictly one after another;
// a failed real-home guard stops the lane. Budgets: sessions 6, retrieval 5, carried 8, resilience 4 = 23
// real sessions, each with one spare (retrieval two: the second pays for a deny-rule settings file the host
// ignored), against D3's ~40-80, which C5.5's 40 pre-registered trials also draw on.
"""
HEADER = HEADER.replace("__CAND__", cand).replace("__BUNDLE__", bundle)
a = s.index("export const meta = {")
s = HEADER + s[a:]

META_OLD_START = s.index("  phases: [")
META_OLD_END = s.index("  ],\n}", META_OLD_START) + len("  ],\n")
s = s[:META_OLD_START] + """  phases: [
    { title: 'Sessions', detail: 'UAT-05 run 2 loss notice, UAT-06 fork, C4.5 status reads, F-C48-1 compact then 0 pending, UAT-09 O-1 SessionEnd after /compact' },
    { title: 'Retrieval', detail: 'UAT-12 A/B and C4.6 under punctuated deny rules, session C: 40+ tool calls then /compact injects the block' },
    { title: 'Carried', detail: 'UAT-02/C4.2, UAT-07, UAT-08, UAT-11 and C4.7 kill switches, none run on candidate 7' },
    { title: 'Recovery', detail: 'C4.9 (b) settingsVersion, C1.6 mid-session daemon end + publication-gap cuts + restart + fsck, D53(i)' },
    { title: 'Audit', detail: 'independent read-only audit of every Result block and coverage row against its evidence' },
  ],
""" + s[META_OLD_END:]

RERUN_PARTS = r"""const RERUN = `THIS IS CANDIDATE 8'S LIVE RE-CHECK (decisions D58-D61). Candidate 7's lane (d20309c0) passed most rows and failed UAT-05, UAT-12 with C4.6, and C4.5; waves 18, 19, 19b, 19c and 20 fixed those and the findings of the pre-freeze audit (D61). Read these first:
- plans/sdd/V6-closeout/live/report-c7.md: candidate 7's results, findings and audit;
- plans/sdd/V6-closeout/w18-*/report.md, w19-*/, w19b-*/, w19c-*/ and w20-*/report.md: what changed;
- the ledger's D58, D59, D60 and D61 rows: each disposition.
FIRST, before any session or guard snap: \`git -C ${WT} symbolic-ref --short HEAD\` must print closeout/live8, \`git -C ${WT} merge-base --is-ancestor ${CAND} HEAD\` must succeed, and \`git -C ${WT} diff --stat ${CAND} HEAD -- . ':!plans/sdd/V6-closeout/live' ':!docs/uat.md'\` must print nothing (since the candidate only this lane's evidence and Result blocks changed). Otherwise run nothing and return status blocked, naming what you saw.
Re-run ONLY the rows named in your part, on this candidate and bundle, exactly as docs/uat.md and the checklist state them in their CURRENT text (several were revised under D59, D60 and D61). Where a row cites ADR 0011, read section 23 and any later amendment at this candidate.
Write evidence under ${LIVE}/rerun-c8/<row>/ and never overwrite earlier evidence. In docs/uat.md, replace each re-run row's Result block with this candidate's result. Keep the existing history lines and add one line for the candidate the block reported before: "Candidate N (<sha>): <its verdict> — <one-line reason>, evidence <its evidence path>", unless that line is already there.
Judge every row against its CURRENT expectation. A defect that is still present, or a new one, is a finding with evidence, never excused. A host-reported hook failure or timeout is a finding under D53(i) unless docs/cannot-do.md or docs/upstream-issues.md documents it as host behaviour.
Budget: the part's budget includes ONE spare session (retrieval: two). Use a spare only to replace a session that was void or invalid (a usage limit, an API outage, a settings file the host ignored), and say in sessions.tsv and in the row's notes which session it replaced and why. If the budget still runs short, run the rows in the order given and record each skip.
Commit after each row, so an interruption loses little. Log the guard snap state at the start of your part. At its end, remove any run-created ~/.qompack content under install.md §6 (its sha256 equals the bundle's bin, and no qompack.exe is running) and log that (D50).`

const DENYPROOF = `Deny rules in effect (D50, D60(c)): with -p the host silently ignores a settings file that fails validation, so before relying on a project's deny rules, prove them in that project's first session: its first turn asks for a Read of private/deny.txt, and the host must refuse it (the result event's permission_denials, or the tool error). If it is not refused, the rules are not in effect: stop that session, fix the settings file, and start again (that replacement is what the part's extra session is for); record both.`

const PARTS = [
  { key: 'sessions', phase: 'Sessions', budget: 6, part: `${RERUN}
Scenario ids to return: UAT-05, UAT-06, C4.5, C4.3, F-C48-1, UAT-09 O-1. Every session uses --plugin-dir ${BUNDLE}, one disposable project per scenario, and the DEFAULT idle exit unless a step needs the daemon gone (then say so in the Result block). Planned: 5 sessions, 1 spare.
UAT-05 run 2 (1 session; run 1 passed on candidate 7 and is not repeated): as the current text states it, at the 150/150 budget. A compaction that dropped material must inject the minimal loss notice: section 7 alone, naming the count of dropped items and the dropped() route, plus the original request's restore pointer when it fits (D59(b), D60 ruling (ii), ADR 0011 section 23). Below the smallest notice nothing is injected, and the overflow drop entry plus one LOUD line keep it from being silent. Nothing may be silent. Record the payload, the drop report and LOUD.log. Also step 5's runtime.rehydrate.* change reaching the RUNNING daemon without a restart (record the reload line and the next block's budget).
UAT-06 (3 sessions): compact, then --resume, then --resume --fork-session with a correction. The fork's first block carries the parent's original AND the correction in force; its "Current work" comes from the fork's own newest prompt, never an inherited parent prompt (D59, forkwork). The parent's session-scoped elimination and decision are visible in the fork (checkpoints, already_tried, why). After --resume, the session's second checkpoint still carries its earlier decision.
C4.5 (no extra session): with two or more sessions in the UAT-06 store, read \`qompack status --json\` twice with no state change between and diff them: they must agree exactly, sessions listed in one stable order (D53(a), D59(c)), and neither read may report a connect miss as the daemon not answering (D60(e)). Also read status and doctor --json once and check that the banner counts no pending row as holding.
F-C48-1 and the drain (1 session, D58(c)/(d)): a session with a few captures, a mid-session /compact, one more turn, then a final /compact as the session's LAST user turn. After it, \`qompack status\` must show session_start.fires holding ('same-session-restart' or marker present, never pending), and 0 pending captures once the daemon has drained (record the spool listing and status). This is the owner's stated re-check: compact, then status shows 0 pending.
UAT-09 O-1 (no extra session; candidate 7's UAT-09 observation, report-c7.md): in candidate 7's lane a session that ended right after a /compact turn never delivered SessionEnd; the daemon ended it later as abandoned. From the F-C48-1 session, whose last turn is a /compact, record whether SessionEnd reached the daemon: wait until the daemon has ended the session or idle-exited, then read the day log (a session-end line, or WARN 'ending abandoned session; no SessionEnd arrived'), sessions.jsonl's end, any client spool, the flush latency in metrics, and the host's stderr ('SessionEnd hook ... failed'). Report what you observed; it is a finding only if the current docs promise otherwise.
C4.3: judge from these rows (bounded payloads, correct current authority, Qompack-sourced recovery).` },
  { key: 'retrieval', phase: 'Retrieval', budget: 5, part: `${RERUN}
Scenario ids to return: UAT-12, C4.6. Every session uses --plugin-dir ${BUNDLE}, in fresh disposable projects. Planned: 3 sessions, 1 spare, 1 for a deny-rule settings file the host ignored.
${DENYPROOF}
Denied names: beside private/deny.txt, deny files whose names hold every punctuation D60(c)(2) lists: private/deny (1).txt, private/John's notes.txt, private/a,b.txt and private/k=v.txt.
UAT-12 sessions A and B (2 sessions; reuse candidate 7's recipe, rerun-c7/UAT-12/, without the upgrade leg). Put the project in a scratch directory whose name contains a space (for example "${SCRATCH}/retrieval/c8/uat12 space/proj"). In session A, read, grep and recall around the denied files (a path: selector naming each, a basename selector, a glob, and free text that mentions them) so their argument summaries are captured. After /compact in session B:
  - section 6 shows no host-denied path (plain, punctuated, relative or absolute, behind a selector or glob, or inside free text) and no absolute out-of-project path; such summaries read "summary withheld" and the pointer keeps its id and hash (D50, D60(c), D61(b));
  - in-project absolute paths and ordinary commands (for example git diff HEAD~1, or cd into the project root and run a command) ARE shown even though the project path contains a space (D60(c), D61(b));
  - section 7's drop entries withhold the same paths, and no drop reason shows an out-of-project or withheld path (D61(b)(3));
  - every retrieval form (recall, expand, re_read, dropped) refuses or redacts the denied paths; dropped() lists a denied path in a checkpoint drop kind redacted, not removed (D60 ruling (iii));
  - paging, recall at the default k and the LOUD.log checks as the current text states them.
Session C (1 session, its own fresh project with the same deny rules and denied names, proven as above): at least 40 tool calls before /compact, Bash and Qompack MCP calls both (for example ls, cat and grep over in-project files, some naming the denied files in their arguments, and recall/expand/re_read), so the block's tool-summary gate judges 40 or more pointers (D60(c)(1)). Then /compact in the same session. The compaction must inject the rehydration block itself, never the deferred note ("Qompack could not deliver this compaction's rehydration", docs/troubleshooting.md): record the payload, how many tool pointers section 6 holds, status --json's session_start_compact_deferred count (0), LOUD.log (no 'compact SessionStart answered without its rehydration' line), and SessionStart:compact's host-seen latency from hooks.json (the arrival delta; say if it is unmeasured). Section 6 again shows no denied path.
C4.6: judge from UAT-12 and session C (every retrieval form and the rehydration block refuse the denied paths).` },
  { key: 'carried', phase: 'Carried', budget: 8, part: `${RERUN}
Scenario ids to return: UAT-02, C4.2, UAT-07, UAT-08, UAT-11, C4.7. None of these ran on candidate 7: docs/uat.md's header says the UAT-02, -07, -08 and -11 Result blocks report candidate 4, and C4.7's last live run is candidate 3's (live/report.md). Waves 15-20 changed the code they exercise (the ledger, paging, hostperm, reload, rehydrate, drain, status), so each is re-run here rather than carried. Every session uses --plugin-dir ${BUNDLE}, one disposable project per scenario. Planned: 7 sessions (candidate 4's runs of these rows, sessions.tsv sessions-c4 and retrieval-c4, and candidate 3's two C4.7 sessions), 1 spare.
UAT-02 with C4.2 (1 session): ordinary work exercising every hook event; every hook event captured and the .qompack/ index counts match what the session did (C4.2), as the current text states it.
UAT-07 (1 session): recall four ways (including globs and host tool names), expand by hash and by id, re_read, a disk edit then re_read, an unknown hash, as the current text states it.
UAT-08 (2 sessions): steps 1-5 before any compaction, /compact, then why; then the session-scope isolation check in a later, different session, as the current text states it.
UAT-11 (1 session): large and unusual results in the first turn (a Read of a file of about 360 KB, as candidate 4 ran it), a commit, /compact, and every pointer in the injected block resolving, as the current text states it.
C4.7 (2 sessions): each kill switch alone, set in the project config before its session (docs/config-reference.md): recording off (runtime.mode "off"): the session works and nothing is captured; reinjection off (runtime.migration.reinjection.sessionStartCompact false): capture continues and SessionStart:compact injects nothing. Each is independent of the other. Record the config, the store listing before and after, status, and the compaction's payload or its absence. Wave 19c rewrote internal/daemon/rehydrate_service.go, where the reinjection switch is read.
C4.7 has no Result block in docs/uat.md: its evidence and verdict go under ${LIVE}/rerun-c8/C4.7/ and in your result.` },
  { key: 'resilience', phase: 'Recovery', budget: 4, part: `${RERUN}
Scenario ids to return: C4.9, C1.6, D53(i). Every session uses --plugin-dir ${BUNDLE}. Planned: 3 sessions, 1 spare.
C4.9 (b) only (1-2 sessions): a newer runtime.migration.settingsVersion in the project config. The documented refusal or degradation and no broken session; hooks log the reset at warn in the day log, and LOUD.log carries at most one such line per daemon, never one per hook process (D59, loudcfg). backup create/verify/restore refuse with exit 1 and the documented message while it is in force (docs/backup.md). Record LOUD.log, the day log and the outputs.
C1.6 (1 session; V6-RECOVERY-1, no live run since candidate 3, D59): confirm automatic startup accounting and fsck detection of publication gaps on the packaged bundle, and state honestly whether automatic RECOVERY exists. Candidate 3's recipe (${LIVE}/recovery/C1.6/notes.txt), with the daemon ended mid-session:
  - one real session in a disposable project with a few Read and Bash turns;
  - mid-session, end ONLY this lane's daemon (check its <project>/.qompack/run/daemon.lock pid, process image qompack.exe and command line first) and confirm it is gone;
  - with the daemon down: fsck --json (before; D59 documents that after a daemon killed mid-session fsck can exit 1 for index/files.json until --repair or the next flush, so name exactly which checks fail), a fingerprint of objects/ and the capture sidecars, then candidate 3's two cuts: remove the last index/roots.jsonl line naming an object that exists on disk, and return one published observe.tool sidecar with durable bytes to stage one (published false, root zero, tool_use_id removed);
  - restart: the session's next turn (a hook starts the daemon), or a bare \`qompack session-start\` hook entry through the bundle CLI if no hook does; record which;
  - the startup accounting: status --json counters daemon.publication.unpublished_captures and unindexed_object_candidates, the loud tail and LOUD.log's 'unpublished captures or unindexed objects found at startup ... incomplete=false' line, and no 'publication accounting incomplete' line;
  - fsck --json after (exit 1 naming both gaps, read_only), with the daemon up and after it exits, and the re-fingerprint: nothing reconstructed.
D53(i) (no session, last): over EVERY session of this re-check (all of ${LIVE}/rerun-c8/*/ holding a meta.json or hooks.json, from every part), list every host-reported hook failure or timeout (a hook_response with a non-zero exit code, an error, blocked or timeout outcome, or a hook the host's stderr names as failed), each with its session and evidence file, and the total, plus host-seen latency per hook event (n, median, p95 and max over measured pairs; unmeasured pairs counted apart, never as 0 ms). Write ${LIVE}/rerun-c8/D53i/summary.md and data.json. Numbers only; candidate 7's C5.6 figures stand (D58(e)).` },
]
"""

a = s.index("const RERUN = `")
b = s.index("\n]\n", s.index("const PARTS = [")) + 3
s = s[:a] + RERUN_PARTS.strip() + "\n" + s[b:]

swap("name: 'v6-closeout-live-rerun-c7'", "name: 'v6-closeout-live-recheck-c8'")
swap("description: 'Phase 4: agent-run UAT-01..12, C4.1-C4.11, C1.6/C1.7 and C5.6 on the frozen candidate, sequential real sessions, then an evidence audit'",
     "description: 'Candidate 8 live re-check (D59-D61): UAT-05/06/12 and C4.5/C4.6, F-C48-1 and SessionEnd after /compact, the carried UAT-02/07/08/11 and C4.2/C4.7, C4.9 (b) and C1.6; then an evidence audit'")
swap("const CAND = 'd20309c03ffc364e4cc48663be73cfbb1f2309b2'", "const CAND = '%s'" % cand)
swap("const BUNDLE = 'C:/Users/Quant/Documents/Programming/Projects/qompack-bundles/c7/qompack-plugin-0.3.0-windows-amd64'",
     "const BUNDLE = '%s'" % bundle.replace("\\", "/"))
swap("if (CAND.startsWith('__') || BUNDLE.startsWith('__')) throw new Error('live-uat.js: replace __CANDIDATE__ and __BUNDLE__ before launch')",
     "if (!/^[0-9a-f]{40}$/.test(CAND) || !BUNDLE) throw new Error('live re-check c8: CAND must be the frozen 40-character SHA and BUNDLE the frozen bundle')")
swap("closeout/live7", "closeout/live8", 0)
swap("owner decisions D1-D57", "owner decisions D1-D61")
swap("THIS IS A RE-RUN of the rows candidate 4 failed or left open plus D53(f)'s install, upgrade and restore rows (decisions D52, D53); evidence is under ${LIVE}/rerun-c7/. Coverage: for each of C4.1, C4.3, C4.4, C4.5, C4.6, C4.8, C4.9, C1.7 and D53(i)",
     "THIS IS CANDIDATE 8'S LIVE RE-CHECK of the rows candidate 7 failed, the rows waves 18-20 changed and the rows candidate 7 never ran (decisions D58-D61); evidence is under ${LIVE}/rerun-c8/. Coverage: for each of C4.2, C4.3, C4.5, C4.6, C4.7, C4.9, C1.6, F-C48-1 (status after a mid-session compaction reads session_start.fires holding and 0 pending), UAT-09 O-1 (whether SessionEnd reached the daemon after a final /compact) and D53(i)")
swap("For every re-run UAT Result block (UAT-01, 03, 04, 05, 06, 09, 10, 12), and the release entry's observed slash-command and MCP-tool namespace against docs/install.md and docs/commands.md in docs/uat.md:",
     "For every re-run UAT Result block (UAT-02, 05, 06, 07, 08, 11, 12) in docs/uat.md:")
swap("Budgets: count claude processes (meta.json files, sessions.tsv) per part against its budget.",
     "Budgets: count claude processes (meta.json files, sessions.tsv) per part against its budget; a spare session may only replace a void or invalid one, with the reason recorded. Branch: the lane's commits sit on closeout/live8 on top of the candidate and change only evidence and Result blocks.")

for stale in ("live7", "const PREV", "rerun-c7/C5.6", "live-uat.js", "__CANDIDATE__", "closeout/w7-livelane",
              "C5.6 resource summary", "36 real", "(install 8, sessions 12"):
    assert stale not in s, stale
for banned, why in (("Date.now", "a workflow script must not read the clock"),
                    ("Math.random", "a workflow script must be deterministic")):
    assert banned not in s, why
assert not re.search(r"^\s*(interface|type)\s+\w+\s*[={]|:\s*(string|number|boolean)\s*[,)=;]|\bas\s+(string|number|any)\b", s, re.M), \
    "TypeScript syntax in the generated script"
body = s.replace("export const meta", "const meta", 1)
chk = subprocess.run(["node", "-e",
                      "const src=require('fs').readFileSync(0,'utf8');"
                      "new (Object.getPrototypeOf(async function(){}).constructor)('agent','parallel',src)"],
                     input=body, capture_output=True, text=True, encoding="utf-8")
if chk.returncode != 0:
    sys.exit("mkrecheck8: the generated script does not parse:\n" + chk.stderr)
open(out, "w", encoding="utf-8", newline="\n").write(s)
print(out)
