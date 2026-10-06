# Live re-run on candidate 7 (`d20309c0`, D52/D53/D57)

Workflow `wf_64f2980c-af8`, merged into integration `a357d187`. The lane's returned results and its
independent audit are kept in `plans/sdd/V6-closeout/live/rerun-c7/lane-results.json`
(`bd38a9f6`). Bundle `qompack-bundles/c7/` (windows-amd64 BUNDLE.json sha256 `5212ae4e…e1f395`,
bin/qompack.exe sha256 `ab8046ba…3be7`), Claude Code 2.1.280, Windows 11 Home 25H2 build
10.0.26200.9457, every session on claude-haiku-4-5-20251001. Agent-executed on the owner's real
host per owner decision D3; not human UAT. The wave 19 docs seat rendered this report from the
lane's returned results, its audit and the committed evidence.

## Outcome

**UAT-05, UAT-12 (with C4.6) and C4.5 failed on candidate 7.** Decision D59 orders a fix for each
in wave 19, and each row is re-run on candidate 8. Everything else passed:

| Row | Verdict on candidate 7 | Evidence |
|---|---|---|
| UAT-01 | pass | rerun-c7/UAT-01/ (notes.txt) |
| C4.1 | pass | rerun-c7/UAT-01/ (cli/iso/, cli/01-04, session/stream.jsonl) |
| C4.8 | pass | rerun-c7/C4.8/ (notes.txt; cli/, cli2/, sessionA/B/C/, store/) |
| UAT-12, upgrade leg (steps 1, 6-10) | pass | rerun-c7/UAT-12/notes.txt, data in rerun-c7/C4.8/ |
| C1.7 | pass | rerun-c7/C1.7/notes.txt, data in rerun-c7/C4.8/cli/, cli2/ |
| UAT-03 | pass | rerun-c7/UAT-03/ (notes.txt, steps/, probe/) |
| UAT-04 | pass, with a finding | rerun-c7/UAT-04/ (notes.txt) |
| UAT-05 | **fail** | rerun-c7/UAT-05/ (notes.txt; run1-*, run2r-*, store-run2r/) |
| UAT-06 | pass, with a finding | rerun-c7/UAT-06/ (notes.txt; A/B/C blocks) |
| C4.3 | pass, with findings | rerun-c7/C4.3/notes.txt (judged from UAT-03/04/05/06) |
| C4.5 | **fail** (D53(a)) | rerun-c7/C4.5/notes.txt, uat06/cli/status-json-read1/2 |
| UAT-09 | pass | rerun-c7/UAT-09/ (notes.txt) |
| UAT-12, retrieval leg (steps 2-5, 8) | **fail** (F1) | rerun-c7/UAT-12/notes-retrieval.txt, sessionA/, sessionB/ |
| C4.4 | pass | rerun-c7/C4.4/tool-matrix.md |
| C4.6 | **fail** (F1) | rerun-c7/C4.6/notes.txt, ../UAT-12/cli/40-43 |
| UAT-10 | pass | rerun-c7/UAT-10/ (notes.txt) |
| C4.9 (a), (b) | pass; (c) not repeated | rerun-c7/C4.9/ (notes.txt) |
| C5.6 | numbers only, no budget proposed | rerun-c7/C5.6/summary.md and data.json |

UAT-12 as a row is a fail: its upgrade leg passed and its retrieval leg failed. C1.6 was not re-run
live on candidate 7, because no publication-gap cut was re-run. Under D59 it stays evidenced by its
deterministic rows and by candidate 4's lane.

**D53(i) holds.** Across all 20 sessions there were 474 hook_started and 474 hook_response events,
with 0 non-success outcomes, 0 non-zero exits and 0 timeouts. All 20 stderr.txt files are 0
bytes. 18 '/compact' lines read 'PreCompact [...] completed successfully', and there is no 'Hook
cancelled' string. The audit recounted all of this independently. The two compaction failures in
UAT-05 run2-session came from the account's weekly usage limit, not from a hook, and the C5.6
summary lists them separately. So nothing needs to be added to cannot-do.md or upstream-issues.md.

**Candidate 8's live re-check (D59):**
- UAT-05 run 2
- UAT-12 sessions A and B, with C4.6
- UAT-06, with C4.5
- a status read after a mid-session compaction (D58(d))
- C4.9's settingsVersion leg

That is about 8 sessions. The other rows carry from candidate 7 by the diff, and the C5.6 figures
stay candidate 7's (D58(e)).

## Sessions and interruptions

20 real sessions (sessions.tsv, one meta.json each under rerun-c7):

| Part | Sessions | Rows |
|---|---|---|
| install | 4 of 4 (install-c7 n=1-4) | UAT-01/C4.1, C4.8 with UAT-12's upgrade leg, C1.7 |
| sessions | 8 of 8 (sessions-c7 n=1-8, n=4 void and counted) | UAT-03, 04, 05, 06, C4.3, C4.5 |
| retrieval | 5 of 5 (retrieval-c7 n=1-5) | UAT-09, C4.4, UAT-12 retrieval leg, C4.6 |
| resilience | 3 of 3 (resilience-c7 n=1-3) | UAT-10, C4.9 (a)/(b), C5.6 |

The lane was interrupted twice (D58(g)):

1. **The account's weekly usage limit, at about 04:50 EDT.** It hit during sessions-c7 n=4 (UAT-05
   run 2, started 04:44 EDT). Every turn after the third answered "You've hit your weekly limit",
   and the session exited 1. It is recorded as void, and it is counted. The lane resumed in the
   afternoon from its committed and scratch evidence:
   - no session already run was repeated: install-c7 n=1-4 and sessions-c7 n=1-3 stand as run;
   - UAT-03's steps 4-5, backup and probe were completed from the preserved project;
   - only the void run 2 was replaced, by n=5 in a fresh project (uat05r).
   - The guard's resume check at 14:03:20 EDT is the post-session check for n=4.
2. **A network outage at about 15:00 EDT,** near the end of the sessions part (guard/sessions.txt,
   UAT-05/notes.txt). D58(g) calls it an API outage (ENOTFOUND); the committed evidence names only
   a network outage. The part was finished from evidence with no new session: UAT-05 and C4.3 were
   judged and committed (a1aa1e0b, d0310dc3, 1f1bf5bf), and the part's end was logged (49194010).

The real-home guard stayed unchanged throughout. homeguard clean removed the run-created empty
plugins/data/ directories.

## Part `install`: done, 4 of 4 sessions, head `3e668076`

Commits: f5d074ad, 44987db7, 3d316581, 3e668076.

- **UAT-01: pass.** The frozen c7 bundle was installed through a local marketplace `qompack-live`
  whose one entry is named `qompack-windows-amd64`. It was installed at local scope in the real
  profile, with no QOMPACK_* variables and no config file. The same flow (add, install, list,
  details, uninstall, remove) ran first in an isolated CLAUDE_CONFIG_DIR with no model call
  (cli/iso/).
  - Version: steps 2-8 print 0.3.0, exit 0, and the installed plugin.json, step 2 and BUNDLE.json
    all read 0.3.0.
  - Setup and config: seven exec-form hooks; the host's 8 tools equal docs/mcp-tools.md; all 108
    config leaves default.
  - Diagnostics: self-test exits 0. status reads '9 assertion(s), none failing: 4 holding, 1
    pending, 4 with nothing to judge'. doctor reports '0 gap(s) across 12 sidecar(s)'. fsck and
    fsck --seal-check exit 0.
- **C4.1: pass.** What the session saw under the release entry name (D53(f)):
  - `plugin list` id `qompack-windows-amd64@qompack-live`, installPath
    `plugins/cache/qompack-live/qompack-windows-amd64/0.3.0`;
  - server `plugin:qompack:qompack` connected, with the eight `mcp__plugin_qompack_qompack__*`
    tools;
  - the six `/qompack:` commands, and no qompack:checkpoint;
  - `/qompack:status` ran, and the host resolved `qompack` for it with no PATH change.
  - Recall ranks the host's records of the session's own recall calls after the original captures
    and withholds them as pathless, counted in `denied` (D49). Every hook succeeded with exit 0.
- **C4.8: pass.**
  - The previous build was candidate 5's frozen bundle (0d06ab12), via --plugin-dir. Session B
    upgraded to candidate 7 via --plugin-dir and re-read session A's capture. already_tried
    answered active, and /compact sealed 0003. The block carried A's elimination and decision.
  - Every pre-upgrade object, checkpoints/0001.json and eliminations.jsonl are byte-identical
    afterwards.
  - The pre-upgrade backup restores with reader proof, integrity and seal check, and candidate 5's
    own fsck accepts the recovery.
  - .qompack/ stayed byte-identical across install, uninstall, reinstall and the final uninstall
    (193 and 209 files).
  - The day log has no WARN or LOUD line across 3 daemon starts.
- **UAT-12, upgrade leg: pass.** Step 7: no version-block or retired-meaning warning (no config file
  exists to carry either), and self-test config.capture reads 'applied as written'. Rollback
  verified: unverified, because the recovery was not activated.
- **C1.7: pass.** Pre- and post-new-write backup, verify and restore all exit 0, and fsck
  --seal-check exits 0 on each recovery and on the source. The two refusals and a GC-tombstoned
  root were not exercised here; candidate 4's run covers the refusals.

Findings:
- F1 (doc): install.md §9's namespace sentence is stale.
- F2 (doc, minor): `claude plugin details qompack-windows-amd64` exits 1 'not found'. `details
  qompack` and `details qompack-windows-amd64@qompack-live` work, and they show the entry's
  description.
- F-C48-1 (diagnostics, minor): after a same-session compaction, status reads session_start.fires
  pending 'marker-absent-once' on a healthy store.
- O1: D10 staging pruned the other build's staged copy at the same 0.3.0 version string. Candidate
  5's session removed candidate 7's copy, and the reverse.
- O2: the old build's draft number makes candidate 7's first checkpoint 0003 (chain 0001 -> 0003),
  as on candidate 4. fsck and doctor accept it.

Guard: snap at 04:04:29 EDT; final check at 04:25:33 EDT 'real home fingerprint unchanged', exit 0
(guard/install.txt). Checks inside an open install window differ by design in
installed_plugins.json, known_marketplaces.json and cache/qompack-live; the window-close checks
(04:10:20 for n=1, 04:22:14 for n=4) are the passing ones. The run-created staged copy and the
empty ~/.qompack were removed under install.md §6.

## Part `sessions`: done, 8 of 8 sessions (one void), head `1f1bf5bf`

Commits: 056c030f, 29099cc2, 8231c050, dc3f02d6, a1aa1e0b, d0310dc3, 49194010, 1f1bf5bf.

- **UAT-03: pass.**
  - Two /compact turns wrote 0001 and 0002. The re-read after the DEFAULT idle exit and a restart
    is byte-identical.
  - With 0002 corrupted, every surface names the rollback (D49; F-C4-UAT03-1 fixed): the header
    'checkpoint 0001 (rolled back from 0002)', section 7 and the drop report checkpoint_fallback
    0002, the state degraded_reason, and LOUD.log.
  - Backup uat03-after-idle: create, verify and restore exit 0.
  - The committed checkpoint copies are path-scrubbed, so they do not re-hash to MANIFEST. The
    MATCH was computed on the unscrubbed store (notes.txt, wave 19 note).
- **UAT-04: pass, with a finding.**
  - Run with CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000 at the default percentage: 3 manual and 5
    automatic compactions, 0 thrash lines.
  - The 16,858-character original is never cut. It is named as a tier-1 OVERFLOW with its expand
    call, and the LOUD tier-1 line appears once (D50).
  - fsck exits 0 with the daemon up and after the idle exit, including --seal-check.
  - Facts recovered via expand, and via recall + expand.
- **UAT-05: fail.** Three sessions: run 1, the void n=4 and n=5 in a fresh project.
  - Run 1 meets every field. The TAB correction renders above the superseded original, which stays
    whole (D50). A runtime.rehydrate.* edit reached the running daemon (D49).
  - F-C7-UAT05-1: at minTokens = maxTokens = 150, both compact injections are only the
    contract-probe line, while the state reads tokens 0, degraded true, with 15 and then 14 drops.
- **UAT-06: pass, with a finding.**
  - The fork's first block carries the parent's original, the 60 correction above it, and the
    parent's elimination and decision (F-C4-UAT06-1 fixed).
  - After --resume, 0002 keeps the decision (F-C4-UAT06-2 fixed).
  - F-C7-UAT06-1: the fork's second checkpoint takes Current work from an inherited parent prompt.
  - Doc finding: the expectation said 'Section 2's first unit is the verbatim original', which
    disagrees with D50's order.
- **C4.3: pass, with findings.** Every compact injection is inline and within budget (61 to 3,833
  units), and every checkpoint re-hashes. Current authority is correct wherever a block carries
  one. Carried findings: F-C7-UAT05-1, F-C7-UAT06-1, F-C7-UAT04-1.
- **C4.5: fail (D53(a)).** F-C7-C45-1: two reads of unchanged state 5 s apart list status --json
  data.snapshot.sessions in different orders. The banner, mcp.server_registered and the sentinel
  items pass, and the six commands ran across the lane.

Guard: every per-session check passed except the void n=4, whose post-session check is the 14:03:20
resume check (guard/sessions.txt note). Final check at 15:11:27 EDT: 'real home fingerprint
unchanged', exit 0. The daemon the resumed agent's brief named, pid 81524 in uat03/recovery, had
already ended by its default idle exit, so nothing was stopped.

## Part `retrieval`: done, 5 of 5 sessions, head `de504a88`

Commits: 7bf32b98, f0bb2a81, de504a88.

- **UAT-09: pass.**
  - The stale flip happens seconds after the edit.
  - D49 fixed: a mid-session drop setting reached the running daemon in 1.0 s.
  - R4-1 fixed: after an idle-exit restart the record answers uncertain under drop and stale under
    flag, never absent.
  - Step 5 answers unavailable with degraded true.
- **C4.4: pass.** Every success and error call of the eight tools is correct, including
  already_tried after a restart.
- **UAT-12, retrieval leg: fail (F1).**
  - Step 2 refusals hold on every form, and out-of-project reads are refused.
  - D50 paging is fixed: an explicit span pages like full:true, the last page is not truncated,
    and every response is within 262,144 bytes.
  - Recall: k counts permitted hits, denied hits are counted apart, and self-records rank last.
  - No LOUD.log at all.
  - The step 8 restore passes.
  - F1: section 6 of session B's block shows `{"query":"path:private/deny.txt"}` in a tool
    pointer's argument summary. No content is shown.
- **C4.6: fail (F1).** Planted secrets: 0 hits on every durable Qompack surface (1,011 + 148 files
  and ~/.qompack). Every retrieval form refuses the deny-ruled and out-of-project reads.

Observations:
- O-1: UAT-09 session 1's SessionEnd never reached the daemon ('ending abandoned session'). The
  host reported nothing, and the cause is not established.
- O-3: under drop, section 4 keeps a rejection decision with no staleness mark.
- After a restart, timeline segments overlap and list as 1, 3, 2.
- UAT-12 O-1: section 2 carries user prompts that name the out-of-project path and
  private/deny.txt.

Guard: snap at 15:22:24 EDT; checks after each session and the final one at 15:45:41 EDT all passed
(guard/retrieval.txt; rerun-c7/retrieval-part-staged-cleanup.txt).

## Part `resilience`: done, 3 of 3 sessions, head `a1529fc1`

Commits: 81ca7897, 1a469b30, 8bd9ae57, 7cd74165, 5402eaed, a1529fc1.

- **UAT-10: pass.**
  - Candidate 3's FAILING banner is gone: it reads 'none failing: 7 holding, 1 pending, 1 with
    nothing to judge'.
  - Candidate 3's p95>max is gone on every displayed value.
  - eval is unavailable, exits 1 and names both paths.
  - Age readings are honest.
  - Rollback after the default idle exit verified.
  - The one pending row is F-C48-1.
- **C4.9 (a): pass.** Two verified kills. Each next hook took the lock over 3 s later, with the
  takeover and replay log lines, and the capture taken during the outage was recalled. F-C49-1,
  F-C49-4 and F-C4-C49-3 are gone.
- **C4.9 (b): pass.** settingsVersion 99 was live-reloaded within 1 s, and the reset is recorded
  exactly as the docs state.
- **Unavailable-object wording:** recorded in a host session.
- **C5.6:** `plans/sdd/V6-closeout/live/rerun-c7/C5.6/summary.md` and `data.json` cover all 20
  sessions, with the previous-build session kept apart.
  - Store growth median: 283,041 B (max 5,596,110).
  - Daemon working set median: 41.1 MiB (max 70.9).
  - Host-seen hook delta over 417 measured pairs: median 56 ms, p95 123 ms, max 251 ms. 38 of 455
    pairs arrived before the stream's init event and were not measured: SessionStart at startup,
    resume and fork (19) and UserPromptSubmit (19).

New findings: F-C7-C49-1, F-C7-C49-2 and F-C7-C49-3 (see the dispositions below). Minor
observations:
- after a takeover, status reads 'no assertions reported' until the next SessionStart;
- the text status wraps the metrics path mid-word;
- status --json's raw latency histogram keeps bucket bounds above Max, while the displayed values
  are clamped.

Guard: snap at 15:50:02 EDT; final check at 16:23:00 EDT passed (guard/resilience.txt).

## Findings and their dispositions

**Fix ordered for candidate 8 (D59, wave 19), not merged when this report was written.** Each
row's re-run on candidate 8 is the evidence that the fix holds.
- **Pointer privacy:** C4.6/UAT-12 F1, a host-denied path in a pointer's argument summary. D59(a)
  orders argument summaries through the pointer path gate (wave 19 rehydrate seat). Re-run: UAT-12
  sessions A and B with C4.6.
- **Tiny-budget silence:** F-C7-UAT05-1. D59(b) decides that a degraded compaction that dropped
  material is never silent: when the budget admits no section, a minimal notice names the loss
  and the restore route, and ADR 0011 is amended (wave 19 rehydrate seat). Re-run: UAT-05 run 2.
- **Status order:** F-C7-C45-1. D59(c) orders snapshot.sessions and every map-derived list in
  status and doctor stably (wave 19 statusorder seat, `closeout/w19-statusorder`). Re-run: UAT-06
  with C4.5.
- **Fork current work:** F-C7-UAT06-1. The fork's checkpoint takes Current work from the fork's
  newest prompt (D59; wave 19 forkwork seat, `closeout/w19-forkwork`).
- **Hook config LOUD:** F-C7-C49-2. A newer settingsVersion logged a LOUD line per hook process
  while the docs say warn (D59; wave 19 loudcfg seat, `closeout/w19-loudcfg`). Re-run: C4.9's
  settingsVersion leg.

**Fixed and merged for candidate 8:**
- **session_start.fires pending:** F-C48-1, also UAT-10 O1. After a same-session compaction or
  resume it now reads 'same-session-restart', which counts as holding (D58(d), integrated in
  `8b5adfde`). Re-check: a status read after a mid-session /compact must show 0 pending.

**Documented (wave 19 docs seat unless noted):**
- F1 and F2 (install part): install.md §9 records the namespace and how `claude plugin details`
  resolves its argument; release.md, CHANGELOG, release notes, README and cannot-do follow (D58(f),
  D59).
- O1 (install part): install.md §6 and architecture §1 state the real prune rule. A spawn that
  writes a new staged copy removes every other SHA-256-named copy no daemon runs, so a same-version
  build is pruned too; a spawn whose copy already verifies prunes nothing (`pruneStaged`,
  internal/daemon/spawn_stage.go).
- F-C7-C49-1: troubleshooting §9, cannot-do §4, CHANGELOG, release notes, release.md (accepted
  residual, D59). The takeover daemon's idle exit can leave index/files.json unwritten; fsck exits
  1 naming index.files; `fsck --repair --yes` or the next session's flush writes it; nothing is
  lost.
- F-C7-UAT04-1: troubleshooting §5, cannot-do §4, CHANGELOG, release notes, release.md (D59). With
  a tier-1 original over the cap, evolution entries are not re-admitted into unused room, by ADR
  0011's design (authority order first); dropped() lists them.
- F-C7-C49-3: backup refuses while a newer settingsVersion is in force (D59). The wave 19 loudcfg
  seat documents it in troubleshooting §6 and backup.md (`closeout/w19-loudcfg`).
- UAT-06's doc finding: the expected result and the user guide now state D50's section 2 order.
- The audit's minor findings: the uat.md header names candidate 7, the UAT-03 scrub note, the
  guard notes, and UAT-12's O-1 in its Result block.

**Open, for a coordinator ruling or candidate 8's re-check:**
- UAT-12 O-1: section 2's verbatim user intent names the denied path and the out-of-project
  absolute path. D50 names pointers only, and D59 does not rule on section 2. The audit leaves the
  privacy judgement to the coordinator; the Result block now records the observation.
- F-C7-UAT05-2: the LOUD tier-1 line logged once per daemon across a restart, while the payload
  named nothing. It is tied to F-C7-UAT05-1, has no separate disposition in D59, and UAT-05 run 2
  re-checks it on candidate 8.
- UAT-09 O-1: a SessionEnd that never reached the daemon after a /compact turn. Not a
  host-reported failure; cause not established.

**By design, or recorded with no change for 0.3.0:**
- O2: checkpoint chain 0001 -> 0003 after a previous build's draft; fsck and doctor accept it.
- UAT-09 O-3, the C4.4 timeline overlap, the UAT-04 WAL residue (doctor spool.pending degraded,
  fsck ok, unchanged since candidate 4), C4.9 O1/O2, and the raw histogram bounds in status
  --json. Observations with no documented contract broken.
- C4.8 upgraded at the same 0.3.0 version string via --plugin-dir, so the host's `claude plugin
  update` path was not exercised on candidate 7 (audit nit).
- Not covered: a public v0.2.0 -> 0.3.0 installed upgrade, and what uninstall does with
  plugins/data/<entry>-<marketplace>/ on 2.1.280.

## Independent audit

**Verdict:** needs-fixes

| Item | Status | Evidence (abridged) |
|---|---|---|
| C4.1 install through the real marketplace flow; hooks, MCP server and six commands discovered | evidenced | UAT-01/cli/iso/i01-i08, cli/01-04, session/stream.jsonl init; namespace matches docs/commands.md; install.md §9 stale (F1) |
| C4.3 real compaction round trip, bounded payload, model recovers facts | evidenced | C4.3/notes.txt from UAT-03/04/05/06; UAT-04 8 compact_boundary events, 0 thrash lines; findings F-C7-UAT05-1, F-C7-UAT04-1, F-C7-UAT06-1 carried |
| C4.4 the eight MCP tools with correct results and errors | evidenced | C4.4/tool-matrix.md; R4-1 does not recur |
| C4.5 slash commands in a real session match docs/commands.md | failed | C4.5/notes.txt:41; uat06/cli/status-json-read1 vs read2 list sessions in a different order (D53(a)), re-verified |
| C4.6 privacy | failed | UAT-12/sessionB/injected-2-SessionStart-compact.txt:36 `{"query":"path:private/deny.txt"}`; the secrets half passes, only placeholders committed |
| C4.8 upgrade, uninstall, reinstall; work preserved; backup, restore, fsck certified | evidenced | C4.8/notes.txt; sha256 listings re-compared (193 and 209 lines identical); same-version --plugin-dir upgrade disclosed |
| C4.9 degraded paths; host session never broken | evidenced | C4.9/notes.txt; two takeover lines; 18 'invalid configuration value' LOUD lines; index.files absent after idle exit |
| C1.7 operator backup/verify/restore, pre- and post-new-write | evidenced | C1.7/notes.txt with C4.8/cli and cli2 |
| C1.6 automatic startup accounting; 'automatic recovery' stated honestly | partial | not re-run on candidate 7; wording still honest (backup.md, troubleshooting §9) |
| D53(i) host-reported hook failures and timeouts = 0 | evidenced | 474 hook_started = 474 hook_response, 0 non-success; 20 stderr.txt 0 bytes; 18 PreCompact success lines |
| uat.md Result blocks UAT-01/03/04/05/06/09/10/12 | partial | only Result blocks edited; Executed by honest; gaps: stale header, UAT-03 copies do not re-hash, UAT-12 omits O-1 |
| Budgets per part | evidenced | install 4/4, sessions 8/8 (n=4 void, counted), retrieval 5/5, resilience 3/3: 20 sessions, 20 meta.json |
| Guard: passing check after every session and at each part's end | partial | every part ends unchanged, exit 0; install n=1 and n=4 read exit 1 inside open install windows; sessions n=4 had no check in the file |
| Commit hygiene d20309c0..HEAD | evidenced | 21 commits, all test(live):; no attribution trailers; every quoted SHA reachable |

Audit findings and their dispositions:
- **major:** C4.6/UAT-12 F1, the denied path in a pointer argument summary. Fix ordered for
  candidate 8 (D59(a)), re-run there.
- **major:** F-C7-UAT05-1, injection at a 150/150 budget is only the probe line. Decided under
  D59(b) (never silent; ADR 0011 to be amended); fix ordered for candidate 8, UAT-05 re-run there.
- **minor:** the uat.md header is stale. Fixed (wave 19 docs).
- **minor:** the UAT-03 committed checkpoint copies do not re-hash to MANIFEST. Note added to
  UAT-03/notes.txt.
- **minor:** guard/sessions.txt has no check after n=4. Note added: the 14:03:20 resume check is
  that check.
- **minor:** guard/install.txt reads exit 1 after n=1 and n=4. Note added: by design inside an
  install window, and the window-close checks (04:10:20, 04:22:14) pass.
- **minor:** the UAT-12 Result block omits O-1. Added; the privacy ruling on section 2 stays open
  for the coordinator.
- **minor:** C1.6 was not re-run live on candidate 7. D59 keeps it evidenced by its deterministic
  rows and candidate 4's lane.
- **minor:** UAT-06's expectation versus D50's order, plus F-C7-UAT06-1. The expectation and the
  user guide now state D50's order; F-C7-UAT06-1 has a fix ordered for candidate 8.
- **minor:** install.md doc findings (namespace, details argument, staging prune). Fixed (D58(f)).
- **minor:** F-C7-C45-1. Fix ordered for candidate 8 (D59(c)).
- **minor:** F-C7-C49-1, F-C7-C49-2 and F-C7-C49-3. F-C7-C49-1 is documented as a known limit,
  F-C7-C49-2 has a fix ordered for candidate 8, and F-C7-C49-3 is documented (loudcfg seat).
- **nit:** the C4.8 upgrade at the same version string. No change for candidate 7.
- **nit:** the UAT-01 Result has a 155-character line and its step 5 record omits hooks.json's
  seven events. Rewrapped, and the declaration added.
