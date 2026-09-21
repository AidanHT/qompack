# Task 4 report — `docs(sp18): document observable failures and recovery`

Commit: `a3e15ed` on `feat/sp18-documentation-and-uat` (parent `20678d8`). Three files, 759
insertions, no deletions, no trailers.

Routing: requested `claude-opus-4-8`, effort medium (care raised to high on every failure-state and
supported-scope claim). Observed model id for myself: `claude-opus-5[1m]` (reported by the harness
as "Opus 5 (1M context)"). The requested id is not what I am running as; recorded here as the
discrepancy the coordinator asked for.

## Files changed

- `docs/troubleshooting.md` — new, 575 lines.
- `test/docs/troubleshooting_test.go` — new.
- `test/docs/owned_test.go` — one line: `"docs/troubleshooting.md"` appended to `ownedDocs`.

Nothing else was touched. No `git stash`, no push.

## What I implemented, section by section

The page opens with the entry shape (symptom / diagnose / meaning / action), a pointer to
`docs/user-guide.md` for what the commands and tools *are*, and a "these diagnostics write" banner
that names which commands create `.qompack/` and links `docs/architecture.md#2-write-set-and-retention`
instead of duplicating the write set.

**§1 Start with provenance.** Five subsections: `qompack self-test` (read `OK`, `SEVERITY`,
`OBSERVED`; the seven build-self checks and the nine contract assertions named individually);
`qompack status` (the `source:` line and its three values, the contract banner, the sections);
`qompack config print --provenance` (JSONC with an origin comment per leaf, linked to the
reference's origin table; `config schema` beside it); `.qompack/state/config-violations.json` (what
it does and does not carry); and the day log under `.qompack/logs/`. The `not-yet-implemented` trap
is stated here and repeated in §10.

**§2 Unknown capability or telemetry.** The three ways an assertion reports "nothing seen"; what
`degraded-passive` keeps doing and stops doing, quoted from `internal/contract/mode.go`; the
`unavailable` latency rows with their real reason strings; `runtime.telemetry.enabled` hardwired
off.

**§3 Capture gaps.** An action-per-fidelity table (the *meanings* are linked to the user guide, not
restated); no live-disk fallback and no reconstruction from current files; subagent detail entering
only through `SubagentStop`; interrupted output marked unknown with the action that would settle it.

**§4 Denied or unavailable evidence.** All seven outcomes; `denied` is not empty; `unavailable` is
not `absent`; `expired` vs `corrupt`; `absent` is the only one that asserts non-existence; what
`already_tried`'s added `unavailable` requires of a client with a closed three-state enum.

**§5 Retrieval that looks wrong.** `re_read` vs disk; minimal spans, `next_span`, `full: true`; the
recorded partial from `plans/V5-report.md` §22 items 26–27 (a blind-ledger digest rendering a stale
record `[active]`; pin records stamped `mcp`).

**§6 Configuration and schema compatibility.** The four warning classes as a table, explicitly
scoped to `config.Load`; the gated switches refusing `true` and the keyless build gate; then the
subsection that is the real content — `config.LoadForCapture` has no fallback, so the hook path
fails closed where the read path merely warns. Ends with the downgrade/upgrade procedure, linking
the three generated anchors.

**§7 Daemon problems.** `admin delivery-seal` refusing beside a running daemon (verbatim message);
the lock and heartbeat and why not to delete `daemon.lock`; idle exit; and the finding that there is
no operator stop command in this build.

**§8 Safe disable.** Five steps with "what keeps being written" for each:
`runtime.migration.reinjection.sessionStartCompact=false` → `runtime.mode=passive` →
`runtime.mode=off` → `runtime.daemon.enabled=false` → removing the plugin from the host (generic
mechanism only; install/uninstall marked planned (SP-17): docs/install.md as plain text).

**§9 Backup, rollback and recovery.** What exists on disk and in `internal/store`; that none of it
is reachable from any command because `NewMigrator` refuses while the build gate is closed; what
V5-report §24/§23 and MIGRATION-EVIDENCE verify; that there is no operator command and no manual
procedure this page will invent; "do not downgrade data to match old prose", tied to the append-only
guard.

**§10 What not to conclude.** Eight bullets, each pointing back at the section that earns it.

## RED / GREEN evidence

RED — tests committed first, page absent:

```
$ go test -count=1 -run 'TestTroubleshooting|TestOwnedDocsExist' ./test/docs/...
--- FAIL: TestOwnedDocsExist (0.04s)
    owned_test.go:32: docs/troubleshooting.md: ... The system cannot find the file specified.
--- FAIL: TestTroubleshootingNamesEveryEvidenceOutcome (0.08s)
--- FAIL: TestTroubleshootingNamesEverySelfTestCheck (0.04s)
--- FAIL: TestTroubleshootingNamesEveryContractAssertion (0.04s)
--- FAIL: TestTroubleshootingLinksConfigReferenceSections (0.04s)
FAIL	github.com/qompack/qompack/test/docs	0.841s
```

GREEN after the page landed: `ok github.com/qompack/qompack/test/docs 1.432s`.

The tests are the brief's four plus one addition, `TestTroubleshootingNamesEveryContractAssertion`,
which derives the nine host-contract assertion ids from `internal/contract/ids.go`. It exists
because §1's central claim is about those rows by name; without it a renamed assertion would leave
the page citing an id that no longer exists. No test contains a count literal; each inventory is
parsed out of its declaring source with a regexp, and each parser `t.Fatal`s if it matches nothing,
so a changed declaration shape fails loudly instead of vacuously passing.

## Validation before committing (run once each)

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	1.542s

$ go vet ./test/docs/...
(no output, exit 0)

$ go run ./tools/devtool fmt-check
(no output, exit 0)
```

No lint, no whole tree, no race, no coverage, no replay.

## Probes

Binary: `go build -o <scratch>/bin/qompack.exe ./cmd/qompack`. Scratch root, outside the repository:
`…\scratchpad\sp18t4` (per-project subdirectories `p1`…`p6`).

Pre-existing processes recorded first: `Get-Process qompack` returned **none** before any probe, so
every qompack process seen afterwards was mine.

| # | Command | Dir | Observed |
|---|---|---|---|
| 1 | `qompack self-test` | `p1` (empty) | the CHECK/OK/SEVERITY/OBSERVED table, `mode: full`, exit 0; four rows `not-yet-implemented`, plus `first-session`, `no-precompact-pending`, `no-transcript-path`, `unset` — all with `ok`/`info` |
| 2 | `qompack status` | `p1` | `source: daemon (available, 0µs old)`; `host contract: no assertions reported`; every per-hook row `unavailable` with its reason; B-D's permanent-unavailable reason |
| 3 | `qompack config print --provenance` | `p1` | JSONC, `// default config.Defaults()` per leaf |
| 4 | `qompack admin delivery-seal --check` | `p1` (daemon running) | `…a daemon is running in <path> and owns the journals; stop it first: qompack: daemon lock already held`, exit 1 |
| 5 | `qompack doctor` / `fsck` / `bench` | `p1` | `qompack <name>: not implemented in this build`, exit 1, all three |
| 6 | `qompack config print` over a five-fault config | `p2` | empty stderr, no log directory; `.qompack/state/config-violations.json` written with exactly two entries (invalid `runtime.mode`, refused `runtime.phase7.reuse.scopedCandidates`) |
| 7 | `qompack checkpoint </dev/null` | `p3` (empty) | `{}`, exit 0, full layout (17 directories + `.gitignore`), daemon started |
| 8 | `qompack checkpoint </dev/null`, one config file per dir | `p5a`–`p5f` | unknown key / newer `settingsVersion` / invalid value / gated switch `true` / `mode:off` → **nothing created**; retired-meaning key → full layout. All printed `{}`, all exited 0 |
| 9 | `qompack self-test` and `qompack status` | `p5a` (unknown key) | `config.load ok info loaded`; layout created; the hook-path failure is invisible to both |
| 10 | `qompack checkpoint </dev/null` with `runtime.daemon.enabled=false` | `p6` | `.qompack/spool/client-<pid>.ndjson` written, no `run/`, no daemon |
| 11 | `qompack help` | `p1` | `admin delivery-seal` is the only admin entry; no stop command; `bench` still advertised |

Cleanup: `Get-Process qompack | Where-Object Path -like '<scratch>*'` found five processes, all
mine (pids 13876, 63960, 65736, 65980, 67464); each was stopped by pid with `Stop-Process -Force`.
`Get-Process qompack` then returned none. The whole scratch tree, including the built binary, was
deleted (`Test-Path` → False). No process I did not start was touched.

## Claims marked unknown

One, and it is labelled as such on the page: **interrupted output**. Whether an interrupted tool
call delivers a partial payload to the hook at all — and therefore whether it lands as `prefix`,
`partial` or nothing — cannot be established from this repository or from a scratch probe, because
it depends on what the host sends. The page says so and names the action that would settle it
(interrupt a long tool call in an installed host session, then read the record's fidelity through
`expand`).

Two further gaps are stated as recorded limits rather than unknowns, because the source explains
them: the `unavailable` per-hook latency rows, and B-D, which nothing in this repository can
observe.

## Self-review findings (and what I changed)

Reviewing my own draft against the brief and the "cite only what the source says" rule, I found and
fixed six things before committing:

1. The write banner said flatly that every hook entry point creates `.qompack/` — contradicted by my
   own §6 and §8 probes. Qualified to "under a default configuration", with a forward pointer.
2. "the only command that may exit non-zero" was wrong beside `doctor`/`fsck`/`bench`, which exit 1.
   Changed to "on a real finding" (the user guide's own wording) with the exception named.
3. I had asserted from my own reading that `first-session`/`no-transcript-path`/`unset` mean "the
   assertion ran and found nothing to judge". The repository has a first-class table for exactly
   this — `internal/contract/observation.go`'s `noObservationSpellings`, "every Observed string in
   assertions.go that means 'nothing was seen'" — so the page now cites that instead of my
   inference, in both §1 and §10.
4. "the full layout, 19 entries" was a miscount (19 included the config file I had written).
   Replaced with "17 directories and `.gitignore`", and "nothing created" replaced with "nothing
   beyond the config file itself", which is what was observed.
5. Two quoted outputs had a path and a pid elided. Both elisions are now stated in the surrounding
   text rather than left silent.
6. The V5-report §24 citation attributed the evidence file names to §24; they are in §23 (Q19).
   Split the citation, and added §24's own "except uncertainty, which is partial" qualification.

Also corrected: "Four switches" heading the five-step ladder; a claim that `expired` and
`expired_deleted` "match" (softened to what each records); and the four-class table now says
explicitly that it describes `config.Load` and not the hook path.

Nothing on the page quotes an output I did not produce or a source line I did not read. No
performance, cost or saving claim appears. No native-control claim appears. Files that do not exist
are plain text: `planned (SP-17): docs/install.md`, `planned (SP-18 Commit 5): docs/cannot-do.md`,
`planned (SP-18 Commit 6): docs/uat.md`, `docs/security.md`, `docs/release.md` — none of them
linked.

## Concerns

1. **`docs/user-guide.md:468` is now stale.** It still reads `- planned (SP-18 Commit 4):
   docs/troubleshooting.md`, under a sentence that says "none of these files exists". The file now
   exists. I own only my three files, so I did not touch it; Commit 5 or a follow-up should delete
   that line and probably add a link to the page from the guide's "Where to look next". Nothing in
   `test/docs` catches this, because a plain-text mention is not a link.
2. **A real defect is documented, not filed.** §6's finding — that a config file `config print`
   accepts makes every hook admit nothing while still exiting 0, with `self-test`'s `config.load`
   reporting `loaded` — is a silent whole-project capture loss triggered by a typo in a config key.
   The page treats it as a documented behaviour with a diagnosis, which is the most this task may
   do. Whether `LoadForCapture` failing closed on an *unknown key* (as opposed to an invalid value)
   is the intended trade-off is a question for the owner, and I would open it as a V6 row.
3. The page's `internal/contract/observation.go` citation depends on a table that a test pins but
   that no doc test here checks. If the coordinator wants that tightened, deriving
   `noObservationSpellings` in `troubleshooting_test.go` the way the other three inventories are
   derived would be a small addition.

---

## Fix round 1 (review finding, Important)

**Finding.** `docs/troubleshooting.md:199–201` attributed the quoted sentence "An absent measurement
prints the availability word, never a zero." to `internal/commands/statuscollect.go`. It is not
there: it is the doc comment on `latencyText` in `internal/commands/render.go:211–213`. The two
reason strings quoted immediately above it *are* `statuscollect.go`'s (`:195–196` for B-D's
permanent-unavailable reason, `:326–327` for the per-hook "folded into the aggregate" reason).

**Verified before fixing.** `internal/commands/render.go:209–213` carries the sentence verbatim on
`latencyText`; `internal/commands/statuscollect.go:195–196` and `:326–327` carry the two reason
strings. The finding is correct as written.

**Fix.** §2's `unavailable` paragraph now splits the attribution: the two reason strings are
credited to `statuscollect.go`, and the sentence about why the cell carries a word instead of a
borrowed number is credited to `render.go`, on `latencyText`. No other text changed.

**Covering tests, re-run:**

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	1.549s

$ go run ./tools/devtool fmt-check
(no output, exit 0)
```

Nothing else was touched; the Minor findings are left for the final review.
