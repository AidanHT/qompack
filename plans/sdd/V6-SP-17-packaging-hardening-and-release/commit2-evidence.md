# Commit 2 evidence — the supported-deployment-environment matrix

What `test/platform` established on this host, what it could not establish here, and what it
returned as a defect. SP17-M7-01's OS/version matrix and SP17-M7-02's path/restriction acceptance
row are both recorded here.

Every claim below is backed by a JSON record in `commit2-platform-windows-amd64/`, written by the
test that made it, and **the table in §2 is transcribed from those records rather than written
beside them**. `INDEX.json` in that directory names the records one run produced: a record file is
not self-dating, so the collecting run prunes the directory first and states afterwards exactly
what it wrote. **No row says passed without a record**, and a row nobody ran says `skipped` or
"not executed by this task" rather than nothing at all.

## 1. What was executed, and against what

| | |
| --- | --- |
| host | Windows 11 (10.0.26200), windows/amd64 |
| Go | go1.26.6 |
| Claude CLI | 2.1.263 (read-only `claude --version`; no session was started, nothing was installed) |
| bundle | `qompack-plugin-v0.2.0-603-gbb7066f-windows-amd64`, assembled per test binary by `go run ./tools/devtool bundle --target windows/amd64 --out <temp>` |
| bundle version string | `v0.2.0-603-gbb7066f-dirty` |
| `bin/qompack.exe` sha256 | `31bfeaf5c56f10aee803088d52baccc60738016824f376829e5beca20f50a5e1` |
| Git Bash | `C:\Program Files\Git\usr\bin\bash.exe`, found at the Git for Windows install location under `%ProgramFiles%` |
| PowerShell | `C:\Program Files\PowerShell\7\pwsh.exe` |

The `-dirty` suffix and the `gbb7066f` commit are correct and load-bearing: the bundle was assembled
from the worktree while this commit's own files were still uncommitted, so `git describe` reported
the parent commit plus `-dirty`. `bb7066f` is this branch's tip before the commit and is permanently
reachable. Nothing in this evidence set is a release artifact, and `BUNDLE.json`'s `source.dirty`
flag is what says so (packaging/README.md §2).

The Git Bash path is recorded, and is resolved from the **install location** rather than from
`PATH`, because on a Windows machine with WSL installed `PATH`'s first `bash.exe` is
`C:\Windows\System32\bash.exe` — the WSL launcher, which cannot execute a Windows `.exe` named by a
Windows path. Which one `exec.LookPath` returns depends on nothing more principled than the shell
`go test` happened to be launched from, so a record that merely said "Git Bash" could have been
describing WSL.

Every case drives the **assembled bundle's** `bin/qompack.exe`, never a `go build` of
`./cmd/qompack`. That is the whole reason this package exists rather than more cases in `test/e2e`:
the bundle is what ships, and the launcher, the manifest and the install directory are only
observable through it. Child processes are started with every ambient `QOMPACK_*` and `CLAUDE_*`
variable stripped, so a developer's own installed plugin or exported configuration cannot silently
change what the matrix measures.

## 2. Case matrix — windows/amd64, executed here

Transcribed from `commit2-platform-windows-amd64/`; the `reason` column is each record's own.

| # | record | reason as recorded | outcome |
| --- | --- | --- | --- |
| 1 | `path-root-plain` | an ordinary ASCII project root (the control) | **verified** |
| 2 | `path-root-spaces` | a project root containing spaces | **verified** |
| 3 | `path-root-unicode` | a project root containing non-ASCII characters | **verified** |
| 4 | `path-root-long` | a project root whose absolute path exceeds MAX_PATH | **verified** |
| 5 | `path-root-mixed-case` | mixed-case project root, resolved through the payload cwd, with the product exercised at both spellings | **verified** |
| 6 | `path-plugin-root-spaces-unicode` | plugin root containing a space and non-ASCII characters | **verified** |
| 7 | `shell-gitbash-exe-resolution` | Git Bash resolves the manifest's extensionless bin/qompack to bin/qompack.exe | **verified** |
| 8 | `shell-gitbash-quoted-spaced` | the QUOTED placeholder form works from an install directory containing a space | **verified** |
| 9 | `shell-gitbash-unquoted-spaced` | the manifest's UNQUOTED `${CLAUDE_PLUGIN_ROOT}` breaks under Git Bash when the plugin install directory contains a space | **failed** — finding F-1 |
| 10 | `shell-powershell-spaced` | the PowerShell launcher form works from an install directory containing a space and non-ASCII characters | **verified** |
| 11 | `shell-launcher-forms` | the Windows hook launcher matrix ran (8 invocations) | **verified** |
| 12 | `restrict-readonly-project-root` | all six hooks exit 0 with consumable output on a read-only project root | **verified** |
| 13 | `restrict-readonly-qompack-dir` | a read-only `.qompack` produces NO durable degradation evidence a later process can read | **failed** — finding F-2 |
| 14 | `restrict-readonly-bundle-dir` | all six hooks run from a read-only plugin install directory | **verified** |
| 15 | `restrict-unwritable-home` | all six hooks exit 0 with an unwritable HOME | **verified** |
| 16 | `optimizations-disabled-fresh-project` | every gated optimization is reported disabled by the shipped bundle | **verified** |
| 17 | `optimizations-unknown-settings-version` | a configuration from a newer plugin has its whole `runtime.migration` block reset to defaults | **verified** |

**15 verified, 2 failed, 0 skipped.** Row 11's aggregate is derived from the number of launcher
invocations that actually happened, not from the test reaching the end: on a host with neither
shell both subtests skip, the parent resumes, and the aggregate then records `skipped` too. A skip
is never counted as verified (Qompack.md §7.5).

Rows 1–4 and 6 each assert the same five things: all six hook subcommands exit 0 with a parseable
`hookio.Output`, `.qompack/` exists beneath the root through `paths.Long`, a daemon answers the
address the root resolves to, one `admin.ping` round trip is answered by that daemon rather than
spooled, and — **within the project root and HOME trees, the only two walked** — nothing was
written outside `<root>/.qompack/` and `<home>/.qompack/`, with a non-vacuity check that something
WAS written inside them. That boundary matters: this is not a whole-filesystem claim. The
whole-filesystem half of §13 invariant 7 belongs to `test/guards/writeset_test.go`, which runs the
hooks in process and so shares a working directory with a stray relative-path write.

Row 5 reconciles two independent facts rather than asserting one. The volume's case behaviour is
probed at runtime; the product's project-key normalization is observed by asking
`ipc.ProjectHash12` whether it folds the two spellings together. Where they agree the product is
exercised at BOTH spellings — one daemon for one directory on a case-insensitive volume, or two
projects with two transports on a case-sensitive one. Where they **disagree** the row records
`failed` against `internal/ipc` and the test stays green: `normalizeRoot` keys on GOOS and never
asks the volume, which is a product characteristic, and a product characteristic must not be
reported as a broken build.

Rows 12–15 assert the exit contract (§13 invariant 6) and, before asserting anything, prove the
deny actually took effect and did not also block reading. Row 14 additionally re-verifies §3.3: the
install directory is byte-identical after every hook.

Two observations recorded but not asserted, because nothing in the specification fixes them:

- a daemon in a fresh temporary project answers `admin.ping` with `mode=degraded-passive` until the
  contract monitor has established otherwise (row 5, which runs `session-start` twice, reports
  `mode=full`). That is §7.1's "unknown host environments report unsupported/degraded capability"
  behaving as designed, not a defect.
- `self-test --json` exited 0 in the spaced/Unicode plugin root with `plugin.root_resolves`
  reporting `ok=true, observed="resolved"`.

## 3. Target matrix — SP17-M7-01

| target | status | basis |
| --- | --- | --- |
| **windows/amd64** | **executed here** — 15 verified, 2 failed (F-1, F-2), 0 skipped | the 17 records in `commit2-platform-windows-amd64/`, listed in its `INDEX.json` |
| linux/amd64 | **CI-pending**: the tests run in the `test` job's OS matrix (`ubuntu-latest`) once the branch is pushed; not executed by this task | `.github/workflows/ci.yml` `test` job, `matrix.os` |
| darwin/arm64 | **CI-pending**: the tests run in the `test` job's OS matrix (`macos-latest`) once the branch is pushed; not executed by this task | as above |
| linux/arm64 | **built, unverified: no runner** | packaging/README.md §6 |
| darwin/amd64 | **built, unverified: no runner** | packaging/README.md §6 |
| windows/arm64 | **built, unverified: no runner** | packaging/README.md §6 |

"CI-pending" is a statement about a run that has not happened. It is not a pass, and the two
CI-pending rows must be filled in from a real job before anything cites them. What HAS been
established for them here is narrower and worth stating exactly: `go vet` succeeds for
`GOOS=linux GOARCH=amd64` and `GOOS=darwin GOARCH=arm64`, so the package compiles for both; no
behaviour on either was observed.

The three unverified rows are unverified for the reason packaging/README.md §6 already gives: the
cross-compile succeeds and the bundle assembles, which says nothing about the binary having been
executed, a host having loaded the manifest, or the launcher having been discovered.

## 4. Findings returned (not fixed here)

### F-1 — the manifest's unquoted `${CLAUDE_PLUGIN_ROOT}` breaks under Git Bash on a spaced install directory

**Owner: `internal/pluginmanifest` (SP-17 Task 6), for `hooks.json` ONLY.** Record:
`commit2-platform-windows-amd64/shell-gitbash-unquoted-spaced.json`.

`hooks/hooks.json` names `${CLAUDE_PLUGIN_ROOT}/bin/qompack <subcommand>` with no quotes for all
seven events, and that string is **shell form**: Claude Code runs it through Git Bash when one is
installed. Measured, with a control at every row:

| form | install directory | exit |
| --- | --- | --- |
| unquoted, placeholder via the environment | no space | 0 |
| unquoted, placeholder via the environment | **with a space** | **127** — `bash: …\plug: No such file or directory` |
| unquoted, placeholder substituted textually | **with a space** | **127** — `bash: C:UsersQuant…plug: command not found` |
| quoted, placeholder via the environment | with a space | 0 |
| quoted, placeholder substituted textually | with a space | 0 |

Two distinct failures, one remedy. Unquoted word splitting breaks the path at the space; and under
textual substitution the Windows backslashes are additionally consumed as escapes (`C:UsersQuant…`).
Quoting the placeholder fixes both, and both quoted rows are asserted green, so the fix is measured
rather than assumed. `C:\Program Files\…` is an ordinary install location, so this is reachable in
normal use, not a corner case.

> **Scope: `hooks.json` only. `.mcp.json` must NOT be quoted.**
> `.mcp.json` names the same placeholder — `{"command": "${CLAUDE_PLUGIN_ROOT}/bin/qompack",
> "args": ["mcp"]}` — but the presence of `args` makes it **exec form**: the host spawns the
> executable directly, with no shell (survey-host-docs.md, "Hook command execution"). There is no
> word splitting to defend against, and adding quotes would make them literal characters in the
> path, so the MCP server would fail to spawn with ENOENT on *every* install directory, spaced or
> not. This test neither reads nor exercises `.mcp.json`; extending the remedy to it would be an
> unmeasured extrapolation from a shell-form case to an exec-form one.

PowerShell is unaffected (row 10), and Git Bash resolves the extensionless `bin/qompack` to
`bin/qompack.exe` on its own (row 7) — packaging/README.md §7's open question 1 is answered YES for
both shells on this host, so no per-OS manifest, extensionless copy or launcher shim is needed for
the extension.

### F-2 — a read-only `.qompack` leaves no durable degradation evidence

**Owner: `internal/cli` + `internal/ipc`** (route through SP-17 Task 6; Task 5 if it needs a
`doctor`-visible surface). Record: `commit2-platform-windows-amd64/restrict-readonly-qompack-dir.json`.

With `.qompack` present and then made non-writable, all six hooks exit 0 with consumable output —
the contract holds. But none of the six places a later process could look changed:
`spool/client-*.ndjson`, `logs/LOUD.log`, `logs/*`, `metrics/*`, `records/*`,
`state/config-violations.json`, and hook stderr was empty. The verdict distinguishes the three
possible answers: a change in one of the four **drop-naming** observables (the spool line, the two
Loud sinks, stderr) is `verified`; a change only elsewhere is `failed`, because a write that is not
about the lost event cannot stand in for the one that is missing; no change at all is `failed` too.
This host produced the third.

The mechanism is visible in the code and is a closed loop: §12.3's drop path Louds through
`internal/cli`'s `hookLogger`, whose sink is `<root>/.qompack/logs` — inside the directory that
cannot be written, so `logging.New` fails and the logger falls back to `Nop`, which records the
Loud only in the process-wide ring; and it counts `l0_dropped` in an `obs.Registry` that dies with
the hook process. §13 invariant 10 says "degradation is loud". Under this particular restriction it
is loud only to a process that has already exited.

This is recorded, not fixed: the fix is a decision about where a store-unavailable degradation
should surface (stderr? `~/.qompack`? `doctor`?), and that is an owning-package decision, not a
test's.

### F-3 (minor, adjacent) — `test/e2e`'s `obsDenyWrites` denies more than writes

**Owner: `test/e2e`.** No record: this is a defect in a sibling test helper, found while building
this package's own.

`test/e2e/observer_e2e_test.go`'s `obsDenyWrites` uses icacls' simple `W` right. `W` is
`FILE_GENERIC_WRITE`, which includes `READ_CONTROL` and `SYNCHRONIZE`. Denying those has two
effects a "read-only directory" should not have, both measured here before `test/platform` stopped
using that mask:

- `os.ReadDir` on a directory beneath the deny fails, because Go opens a directory handle with
  `SYNCHRONIZE`. In this package that turned a before/after directory comparison into a list of
  false differences — the case reported it had found degradation evidence when it had found its own
  blindness.
- `CreateProcess` on a binary beneath the deny fails with "Access is denied", for the same reason,
  which made the read-only-install-directory case unable to run the product at all.

`test/platform` uses the specific rights instead (`WD,AD,WEA,WA,DE,DC`, see
`test/platform/platform.go`'s `windowsDenyMask`). Whether `test/e2e` is actually harmed by the
broader mask was not investigated — it may not read or execute beneath its denied directory — so
this is reported rather than asserted.

## 5. What stayed unverified

- **Everything on linux and darwin.** Compiles for both; no behaviour observed. The two CI-pending
  rows above are the plan. In particular the case-SENSITIVE branch of row 5 — which creates a
  second project at the lower-case spelling and drives the product there — has never run anywhere;
  `ubuntu-latest` is where it first will.
- **Any volume where the filesystem and the product's case rule disagree.** Row 5 detects that and
  records it against `internal/ipc`, but no such volume exists on this host, so the branch that
  writes that record is itself unexercised.
- **An installed plugin.** Every case here sets `CLAUDE_PLUGIN_ROOT` itself and runs the binary
  directly or through a shell. No `claude plugin install/enable/disable` was run, no Claude session
  was started, and `~/.claude` was neither read nor written. So this matrix establishes what the
  shells and the filesystem do with the bundle; it does not establish that a host resolves the
  manifest the same way (Qompack.md §7.5). Task 8 owns that rehearsal.
- **The daemon under a managed restriction.** The four restriction cases suppress the daemon
  through `QOMPACK_RUNTIME__DAEMON__ENABLED=false` so they stay hermetic, and each record says so.
  They cover the hook adapter's exit contract and its degradation path, not the daemon behind it.
- **`${CLAUDE_PLUGIN_ROOT}`'s actual Windows spelling.** The host's own expansion (slashes or
  backslashes, and whether it quotes) is undocumented, so F-1 measures BOTH the environment-variable
  and textual-substitution expansions. Which one a host really performs is still unknown; the
  finding holds either way.
- **`.mcp.json`'s exec-form spawn.** Nothing in this package reads or runs it. The scope note in §4
  is a statement about what was NOT measured, and Task 8's install rehearsal is where the MCP
  server's own launch should be observed.
