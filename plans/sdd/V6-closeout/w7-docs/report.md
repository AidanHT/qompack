# w7-docs: user docs match the integrated code

Branch `closeout/w7-docs`. Workflow `wf_00490efb-ff4`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `015e1947`

### Summary

Seat docs, worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w7-docs, branch closeout/w7-docs, based on 898bb8b. I made 6 commits, touched docs only, and pushed nothing.

I checked README.md and docs/{install,user-guide,commands,mcp-tools,config-reference,security,backup,cannot-do,upstream-issues,architecture,release,troubleshooting}.md against the code. Most of the docs already matched it. I built the binary and ran --help for every subcommand. I compared the hook table with plugin/hooks/hooks.json and plugin/.mcp.json, and I diffed every `runtime.*` key the docs name against docs/config-reference.md: none is missing. The MCP tool names and parameters match. These match the code: the D21/D17b budget numbers (internal/cli/hookbudget.go: 13.5 / 3.25 / 8.25 s), D9's 5 s compact bound, D10's staged copy and its uninstall steps, D13's async SessionEnd (500 ms wait, 1.5 s host budget), D18's home refusal, D24's NTFS premise, D30's GC queueing, D2's rollover on by default (internal/daemon/delivery_generation.go:140) with the D6/D16 residuals, and the six-target zip bundles.

Inconsistencies found and fixed:
1. **README "Status: pre-release"** said versioning was still to be decided. D1 chose v0.3.0, but core.Version is still "0.1.0" (internal/core/version.go:6). Tags v0.0.1, v0.1.0 and v0.2.0 exist. I added the D1 decision and said the code keeps 0.1.0 until the release's version commit.
2. **docs/release.md §1 step 1** only said to bump core.Version. plugin.json's version is generated from that constant (internal/pluginmanifest/manifest.go:227,262), so the bump also needs `devtool plugin-validate --write` (tools/devtool, plugin-validate's `--write` flag) or the gate's plugin-validate step fails. I added that to step 1. I did not touch the code-signing open item.
3. **docs/security.md:243 and docs/user-guide.md:500** said "seventeen check classes". fsck now reports 18 rows (internal/cli/fsck.go:506-524): the publication row was added after the text was written. I changed both to "eighteen check rows".
4. **docs/security.md:81** cited "§3.3", but security.md has no §3.3 (§3 is decode and size bounds). I pointed it at architecture §2, the write set.
5. **docs/user-guide.md, operator commands:**
   - The "Which of them write" paragraph left out self-test, which starts a daemon when none answers (internal/cli/selftest.go:314). I added it.
   - The command table was missing `fsck --seal-check` and `admin delivery-seal --accept-torn-slot --yes`, which the CLI's --help shows. I added both.
6. **docs/troubleshooting.md, lines 736 and 768 before the edit** said a spooled request is replayed "at its next idle drain, within 30 s". The idle drain only runs after `scheduler.idle.detectAfterSeconds` of quiet (120 s by default), checked on a tick of at most 30 s (internal/daemon/daemon.go:34,819; idle.go:91). The fast path is the client-spool watcher (spool_watch.go:45-51). I reworded both lines and left the Defender section alone.
7. **docs/architecture.md §1 IPC** did not say when the idle drain runs, and did not mention D31's soft pass budget (idleRunBudget 2 s, drainLineDeadline 5 s). The watcher, idle and lane-requested drains use it (idle.go:190, spool_watch.go:216, delivery_order.go:816); a session end's drains are unbudgeted (delivery_order.go:651). I added one short paragraph and left the EnsureLayout sentence alone.

Checks after the changes: all three generator `--check`s are up to date, `go test ./test/docs/...` passes, the docmarkers and runpatterns lint passes, and all six commit messages pass devtool check-commit-msg and CI's subject regex with no attribution trailers.

To check self-test's rows I ran the scratch binary with HOME, USERPROFILE and CLAUDE_CONFIG_DIR pointed at a scratch folder. It started a daemon anyway, even with --set runtime.daemon.enabled=false (open issue 1). I stopped that process myself: PID 68948, the scratch binary, its project in scratch.

### Commits

- 0f925529 docs(readme): record the chosen v0.3.0 release version
- 189de47a docs(release): regenerate plugin.json with the version bump
- b8ad5576 docs(security): count fsck's eighteen rows and fix a dead reference
- 8a7fce23 docs(user-guide): list self-test as a write and the missing flags
- 17de568a docs(troubleshooting): correct when a spooled start is replayed
- 015e1947 docs(architecture): state the idle window and D31's soft drain budget

### Tests

- `go run ./tools/devtool gen-config-docs --check / gen-command-docs --check / gen-mcp-docs --check` — all three up to date (before and after edits)
- `go test -count=1 ./test/docs/...` — ok (5.9s)
- `go run ./tools/devtool lint --only=docmarkers,runpatterns` — PASS runpatterns, PASS docmarkers (303 plan docs)
- `go run ./tools/devtool check-commit-msg <msg> for each of the 6 commits` — rc=0 for all six
- `qompack.exe --help and <sub> --help (fsck, backup, admin delivery-seal, doctor, status, self-test, config print); qompack bench` — flags compared with the docs; bench prints 'not implemented in this build', exit 1, as documented
- `qompack.exe self-test --set runtime.daemon.enabled=false (scratch HOME/project)` — the 4 later-wave contract rows report not-yet-implemented, as cannot-do.md says; it also started a daemon (open issue 1), which I stopped

### Open issues

- Possible product defect: `qompack self-test` calls daemon.EnsureRunning with no runtime.daemon.enabled check (internal/cli/selftest.go:300-330), so it starts a resident daemon for a project configured with the daemon disabled. That daemon can then drain the spool. release.md §4 says 'runtime.daemon.enabled: false — no resident process and no lock file', and troubleshooting Step 4 describes that setting as never spawning. Observed with --set; not tested with a project config.json.
- /qompack:checkpoint ships in plugin/commands/checkpoint.md ('Write an immutable checkpoint now') but runs the PreCompact hook entry point, which writes no checkpoint. The docs already say so (commands.md 'not yet routed'; SP-14 H3). It is still a user-visible gap before public release.
- docs/release.md's opening 'V6 release status: blocked' paragraph and the README/release §3 scope and evidence statements are the evidence half of C6.1 and were left for the final gates. The 'blocked' paragraph cites retrieval-authorization and publication-recovery failures that may already be fixed on the integration branch.
- D27 (wave 7: a lost-lock or stopped daemon gives back run/spawn.lock) is not merged at 898bb8b. Once it lands, architecture §1 'Starting the daemon' may need a sentence.

## Independent review

### review:docs: sound

- **nit** `docs/troubleshooting.md:736 and :769 (commit 17de568a)` — The new wording says the client-spool watcher replays a spooled request 'a few seconds after the spool is written', with no condition attached. The watcher only runs after a served request kicks it: it keeps looking while kicks arrive, then looks once more one interval after they stop. So a start spooled because no daemon was listening is picked up by the startup drain, not by the watcher. architecture.md:163 words the same fact more carefully ('while requests keep arriving ... about two check intervals').
  - Evidence: internal/daemon/spool_watch.go:23-26 ('Every request the daemon serves kicks it ... looks at the spool directory once per spoolCheckInterval for as long as kicks keep coming, and once more an interval after they stop') and :44-51 (spoolCheckInterval = idleRunBudget = 2 s, so about two intervals).
  - Fix: Optional: change it to 'its client-spool watcher about two check intervals after the spool is written, while the daemon is serving requests', so it matches architecture §1. The rest of the edit is correct as it stands: idle drain after detectAfterSeconds=120 (idle.go:91, defaults.go:54) on a tick of at most 30 s (daemon.go:34).

## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.

