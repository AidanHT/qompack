# Task 1 report — Commit 1 `docs(sp18): establish supported architecture and doc contracts`

Routing: **requested** model `claude-opus-4-8`, effort high. **Observed**: this agent runs as
`claude-opus-5[1m]` (the harness reports the model id; the requested override is not reflected in
what this session can observe). Recorded as asked, not claimed as verified.

Worktree `C:\Users\Quant\Documents\Programming\Projects\qompack-sp18`, branch
`feat/sp18-documentation-and-uat`, base `7ace16a`.

**Commit: `9848254` `docs(sp18): establish supported architecture and doc contracts`** — exactly
one, no attribution trailers (verified with `git log -1 --format=%B | grep -iE
'Co-Authored-By|Claude-Session|Generated with|Signed-off'` → no match), working tree clean
afterwards, nothing pushed.

---

## 1. What I implemented

### Part A — the docs-test harness (`test/docs`)

New Go test package, stdlib only, no `internal/` imports, registered as a composition root.

| File | Contents |
|---|---|
| `test/docs/doc.go` | package comment only (what the harness checks and why it is mechanical) |
| `test/docs/harness_test.go` | `repoRoot` (copied from `test/guards/stubs_test.go`'s `go list -m -f {{.Dir}}` approach, not imported), `readLines`, `defenced`/`scrubbed`, `headingText`, `slug`, `anchorsOf`, `markdownFiles`, `firstHeading`, `titleCore` |
| `test/docs/links_test.go` | `TestRelativeLinksResolve` + `knownAnchorDefects` |
| `test/docs/owned_test.go` | `ownedDocs`, `TestOwnedDocsExist`, `TestADRIndexListsEveryADR` |
| `test/docs/inventory_test.go` | `generatedCommandNames`, `generatedToolNames`, `headingNames`, `TestGeneratedInventoriesAreNonEmpty` |

Behaviour against the brief's four numbered items:

1. **`TestRelativeLinksResolve`** — scans `README.md` and every `docs/**/*.md` (generated pages and
   ADRs included). Inline links `[t](target)` and reference definitions `[x]: target` are both
   collected; targets with a URL scheme or a `//` prefix are skipped. Fenced code blocks are blanked
   and inline code spans removed before the scan, so a JSON schema block in `docs/mcp-tools.md`
   contributes nothing. The file half must `os.Stat` to an existing non-directory; a `#anchor` (bare
   or after a path) must match a heading slug in the target file. Slugging is the brief's rule:
   lowercase, backticks dropped, spaces → `-`, all other punctuation except `-`/`_` removed,
   duplicates suffixed `-1`, `-2`. Every failure is `t.Errorf("file:line: target …")` and the scan
   continues.
2. **`TestOwnedDocsExist`** — package-level `var ownedDocs = []string{"README.md",
   "docs/architecture.md", "docs/adr/README.md"}`; each must exist, be non-empty, and open on `# `.
3. **`TestADRIndexListsEveryADR`** — derives the ADR set from `docs/adr/` by the file-name pattern
   `^[0-9]{4}-.*\.md$` (no count anywhere), requires `docs/adr/README.md` to link each one, requires
   the line carrying that link to contain the ADR's own title, and fails on an index link to an ADR
   that does not exist. The title compared is `titleCore(firstHeading)`: the ADR's first `# ` text
   with its leading `ADR 0011`/`12.` numbering and the separator after it stripped, so the index may
   keep the number in its own column. That strip is conservative — it only eats a leading dash or
   colon when followed by a space, so `ADR 0003 — Re-collecting …` keeps its `Re-`.
4. **`inventory_test.go` helpers** — `generatedCommandNames` reads `^## \x60/qompack:NAME\x60$`
   headings from `docs/commands.md`; `generatedToolNames` reads `^## \x60NAME\x60$` from
   `docs/mcp-tools.md`. The backtick-only pattern is what excludes that page's prose heading
   `## Tools at a glance`. Both read raw (de-fenced) lines, because scrubbing inline code would
   delete the heading text itself. Both `t.Fatalf` on zero names.
   `TestGeneratedInventoriesAreNonEmpty` calls both.

### Part B — `README.md` (new, 143 lines)

Opening paragraph derived from `plugin/hooks/hooks.json` (the seven hooks by name),
`plugin/.mcp.json` (`qompack mcp`), and pointers to `docs/commands.md` / `docs/mcp-tools.md` for the
command and tool inventories — no count is stated in prose. Then: pre-release status with both
version numbers as they stand (`v0.2.0` tag, `0.1.0` in `plugin/.claude-plugin/plugin.json`, neither
changed); a supported-environments table transcribed from `.github/workflows/ci.yml` job by job
(runners, Go 1.26.6, the six cross-build targets from `tools/devtool/bindeps.go`'s
`releaseTargets`); the installed-host gap as `implemented_unverified` per `plans/V5-report.md` §24
item B01, with the SP-17 pages named as plain text and never linked; building from source
(`go build ./cmd/qompack`, binary `qompack`/`qompack.exe`, the `plugin/` layout,
`${CLAUDE_PLUGIN_ROOT}/bin/qompack`, `devtool plugin-validate`); configuration pointing at the
generated reference with the five-layer precedence and the invalid-value/unknown-key behaviour; a
switches-that-ship-off section covering the whole `runtime.migration` block, the `runtime.phase7`
block, `runtime.telemetry.enabled`, the two `runtime.selection` gates, the
`settingsVersion` reset-to-defaults semantics for both blocks, and the SP-21 enabled-surface matrix
from `plans/V5-report.md` §24 — stated as unsupported until the corresponding gates pass; and a
links section limited to `docs/architecture.md`, `docs/adr/README.md`, the three generated pages and
`LICENSE`.

### Part C — `docs/architecture.md` (new, 312 lines)

All ten required sections, each claim cited to shipped code, an ADR, `plans/V5-report.md` or
`Qompack.md` v1.5:

1. Process model — the seven hook subcommands with their manifest timeouts, `internal/cli`'s
   exit-0 rule, `internal/mcp`'s stdio-only transport and handlers-run-in-the-daemon contract,
   `internal/daemon`'s reason for existing, `internal/ipc`'s "this is not network access".
2. Write set and retention — every directory `internal/paths/layout.go` names, the `.gitignore`
   the layout writes, the named files, the append-only protection of `checkpoints/`, `pins/` and
   `sketches/tried.bloom`, `plans/V5-report.md` §27's guards, and `internal/redact` as what is
   never written; telemetry hardwired off.
3. Identity — the four closed enumerations quoted from `plans/00-ARCHITECTURE.md` line 62 and
   confirmed value-by-value against `internal/core/evidence.go`; `ObservationID` vs content
   identity; `internal/state`'s authority rules.
4. Publication and durability — the §0.2.2 order, idempotent ack, ADR 0014, `internal/checkpoint`,
   one draft owner; plus the two open regressions (SP20-D1, SP20-D2) and the plainly-stated fact
   that plan §8's gate is not met.
5. State authority and uncertainty — observed vs believed, and the recorded partial stated in as
   many words ("does not survive the digest surface under a blind ledger"), with §29 item 11's two
   concrete symptoms.
6. Retrieval — `unavailable` is not `absent` and is never substituted with current content (ADR
   0013 plus `docs/mcp-tools.md`); `dropped` reports qualified coverage, quoted; `ephemeral` is not
   native eviction, quoted from both the page and `internal/mcp`'s package comment; minimal spans.
7. Checkpoint and rehydration — normative order, and 8–12K stated as a historical Qompack-added
   target for the injected material, explicitly not the total restored native context and with no
   claim that native input shrinks; the shipped defaults that implement it; the injection kill
   switch.
8. Scheduler / observer / negative knowledge — ADR 0012, ADR 0008, ADR 0009 with the bloom as a
   cache and an explicit denial of any "safe false positive" property; ADR 0007 for slices.
9. One table of historical records vs current amendments, consistent with `docs/adr/README.md`,
   with notes on ADR 0013's *proposed* status and ADR 0010.
10. What is not supported — six pointers plus the subscription-vs-price separation, and the full
    page named as plain text "planned (SP-18 Commit 5): `docs/cannot-do.md`".

A closing "Known divergence from the plan documents" section states the code-wins rule and points
at §24's plan-vs-code drift list.

### Part D — `docs/adr/README.md` (new, 47 lines)

All twelve ADRs, derived from the directory: number, linked title, status/date exactly as each ADR's
own header states it (including "no date line" where there is none), and a kind column with the
reason. Nothing in the directory records a supersession, so none is invented. A numbering section
quotes ADR 0030's `<SP number as three digits><sequence>` convention and ADR 0100's "0100 block",
and says explicitly that no reason is recorded for any specific missing number.

### The one row outside the docs

`tools/devtool/importrules.go`: `"test/docs": true,` added to `compositionRoots` with a comment.

---

## 2. RED and GREEN evidence

### RED (harness written, documents absent)

```
$ go test ./test/docs/...
--- FAIL: TestRelativeLinksResolve (0.05s)
    links_test.go:61: read ...\qompack-sp18\README.md: open ...\README.md: The system cannot find the file specified.
--- FAIL: TestOwnedDocsExist (0.05s)
    owned_test.go:30: README.md: GetFileAttributesEx ...\README.md: The system cannot find the file specified.
    owned_test.go:30: docs/architecture.md: GetFileAttributesEx ...\docs\architecture.md: The system cannot find the file specified.
    owned_test.go:30: docs/adr/README.md: GetFileAttributesEx ...\docs\adr\README.md: The system cannot find the file specified.
--- FAIL: TestADRIndexListsEveryADR (0.05s)
    owned_test.go:78: docs/adr/README.md: GetFileAttributesEx ...\docs\adr\README.md: The system cannot find the file specified.
FAIL
FAIL	github.com/qompack/qompack/test/docs	0.813s
```

`TestGeneratedInventoriesAreNonEmpty` passed in the RED run, as it should: the generated pages
already existed.

An intermediate RED caught a real harness bug rather than a document bug — `anchorsOf` was reading
inline-code-scrubbed lines, so `## \x60recall\x60` slugged to the empty string and five valid
anchors in `docs/mcp-tools.md` were reported broken. Split into `defenced` (headings) and `scrubbed`
(links); fixed.

### GREEN

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	0.920s
```

### Negative controls (proof the tests can fail)

Three deliberate breakages, each reverted immediately:

```
$ # appended to docs/architecture.md:
$ #   [bad file](nope.md) and [bad anchor](adr/README.md#no-such-heading) and [good](adr/README.md#numbering)
$ go test -run TestRelativeLinksResolve ./test/docs/...
--- FAIL: TestRelativeLinksResolve (0.05s)
    links_test.go:83: docs/architecture.md:313: nope.md (no such file)
    links_test.go:116: docs/architecture.md:313: adr/README.md#no-such-heading (no heading with that anchor in docs/adr/README.md)
```

(the third link, a real anchor, did not fail — so the check is not simply rejecting anchors)

```
$ # docs/adr/README.md: "The QPKS sketch binary format" -> "The QPKS sketch format"
$ go test -count=1 -run TestADRIndexListsEveryADR ./test/docs/...
--- FAIL: TestADRIndexListsEveryADR (0.08s)
    owned_test.go:116: docs/adr/README.md:30: the row linking 0030-sketch-binary-format.md does not name its title "The QPKS sketch binary format"
```

---

## 3. Validation commands

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	0.920s

$ go vet ./test/docs/...
(no output)

$ go run ./tools/devtool fmt-check
(no output)

$ go run ./tools/devtool lint        # ~25 minutes; all ten sub-checks
...
stubskips: OK
PASS stubskips
== devtool lint: runpatterns ==
runpatterns: 857 -run patterns parsed, 688 of 688 checkable resolved against 67 packages,
  1 skipped (package not in tree yet), 7 skipped (unlanded wave ...), 2 platform-excluded, 18 waived
PASS runpatterns
== devtool lint: docmarkers ==
docmarkers: 153 plan document(s) scanned
PASS docmarkers
== devtool lint: coveragefloors ==
coveragefloors: 63 floor claim(s) across 153 plan document(s) checked against plans/OWNERS.tsv
PASS coveragefloors

[exited with code 0]
```

`lint` exited 0. Its earlier sub-checks (golangci-lint, nomagic, **importgraph**, testdeps, bindeps,
sleepcheck) all passed too — importgraph is the one that mattered here, and it is what the
`"test/docs": true` row in `tools/devtool/importrules.go` is for: without it an undeclared package
under `test/` is an error. The `notice:` and `runpatterns` lines above are pre-existing
platform-gated skips and standing waivers, unrelated to this commit.

Also run once, to check a claim rather than the tree: `go build -o <tmp> ./cmd/qompack` succeeded
(11 MB binary), which is what licenses README's "Building from source" section.

Not run, per the brief: whole-tree `go test ./...`, race, coverage, replay.

---

## 4. Files changed

| File | Status |
|---|---|
| `README.md` | new |
| `docs/architecture.md` | new |
| `docs/adr/README.md` | new |
| `test/docs/doc.go` | new |
| `test/docs/harness_test.go` | new |
| `test/docs/links_test.go` | new |
| `test/docs/owned_test.go` | new |
| `test/docs/inventory_test.go` | new |
| `tools/devtool/importrules.go` | +4 lines (one map row plus its comment) |

Nothing else. No generated page, no ADR body, no `plugin/`, `.github/`, `plans/`, `Qompack.md`,
`internal/` or `cmd/` file was touched. No `git stash`, no push.

---

## 5. Claims marked unknown, and why

- **Installed-host compatibility** — `implemented_unverified`. README says so and names the
  verification action (install the bundle in a real Claude Code host and run a session).
  Source: `plans/V5-report.md` §24 item B01 and `plans/MIGRATION-EVIDENCE.md` M0-G4.
- **Injection behaviour in an installed host** — "documented; installed behavior unknown"
  (`plans/MIGRATION-EVIDENCE.md` capability table). Stated in `docs/architecture.md` §7 and §10.
- **Host retention of anything marked `ephemeral`** — unknown by construction; the generated page
  says "host retention unknown" and the architecture page repeats it rather than resolving it.
- **Name availability** — whether a slash-command or MCP tool name is free in a given host session
  is unknown here; `plans/V5-report.md` §29 item 6 records the competing-hook matrix (T21-HOST-01)
  as needing an installed host.
- **Estimated price** — `unknown`, no rate table applied (`plans/V5-report.md` §25), and
  subscription allowance is kept separate from cash per token.
- **The uncertainty gate** — `partial`, not passed; stated plainly in §5 with its two unruled
  symptoms.
- **My own routing** — the requested `claude-opus-4-8` is not observable from inside this session;
  recorded above as requested-vs-observed rather than asserted.

---

## 6. Self-review findings

Checked against every numbered item of the brief; all present. What I found while re-reading:

1. **A real defect in a file I do not own, and how I handled it.**
   `docs/mcp-tools.md`'s "Tools at a glance" table links three tools to anchors that do not exist:
   `#re-read`, `#already-tried`, `#record-eliminated`. The headings are `` ## `re_read` `` etc., and
   GitHub keeps the underscore (it is a word character), so those three links land nowhere. The
   cause is one line in the generator:

   ```go
   // tools/devtool/genmcpdocs.go:286
   func anchorFor(name string) string { return strings.ReplaceAll(name, "_", "-") }
   ```

   I cannot fix it here: `docs/mcp-tools.md` is generated and CI's `docs` job diffs it byte for
   byte, and `tools/devtool/genmcpdocs.go` is outside this task's owned set. Weakening the slug rule
   to accept `_`→`-` would have hidden the bug, so instead `links_test.go` carries an explicit
   `knownAnchorDefects` map with the three keys, the cause, and the fix. It is enforced in **both**
   directions, the way CI's `EXPECTED_FAILING_ROWS` is: an unlisted broken anchor fails, and a
   listed anchor that starts resolving fails too, so the generator fix cannot land without the
   entries being struck. **This needs an owner decision** — the one-line fix plus a regeneration is
   the right change and belongs to whoever owns `devtool`.
2. **Deviation from the brief's letter on the importrules comment.** The brief says "a one-line
   comment"; I wrote three lines, because every surrounding entry's comment is three to ten lines
   and a one-liner would have been the odd one out. The row itself is exactly one additive row.
3. **`titleCore` is a heuristic.** It strips `ADR <n>` or `<n>.` and one following separator. It is
   deliberately conservative (a dash is only eaten when followed by a space), and it is exercised by
   all twelve current titles including `Re-collecting …`. A future ADR titled, say, `0015 Rev 2 —
   …` would confuse it. Cheap to fix when that happens; not worth pre-solving.
4. **`slug`'s non-ASCII handling is approximate.** It keeps Latin-1 Supplement / Latin Extended
   letters and drops everything else above ASCII (em dashes, curly quotes). That matches GitHub for
   every heading in this repository; it is not a general implementation of the slugger.
5. **`ownedDocs` deliberately excludes the generated pages and the ADRs.** They have their own
   gates (CI's `docs` job; `TestADRIndexListsEveryADR`), and listing them here would have been a
   second, weaker copy of those.
6. **No count anywhere.** `TestADRIndexListsEveryADR` derives its set from disk; the README points
   at the generated pages for the command and tool inventories instead of naming a number; the
   architecture page does the same.
7. **Link discipline.** README links only the six targets the brief allows plus `LICENSE`; the
   user-guide, troubleshooting, cannot-do, upstream-issues and UAT pages are not linked anywhere,
   and the SP-17 pages appear only as plain text.

---

## 7. Concerns

1. The three broken anchors in the generated `docs/mcp-tools.md` (above). Allowlisted and
   documented, not fixed, because both the page and the generator are outside this task's owned
   set. Someone should take the one-line generator fix.
2. `docs/architecture.md` states, in §4, that `plans/V5-report.md` §29 records plan §8's
   no-unresolved-mandatory-regression gate as **not met** (SP20-D1, SP20-D2 carried under the user's
   waiver). That is accurate and cited, but it is the kind of sentence a reader may be surprised to
   find in a public architecture page; it is there because the brief's claims policy requires
   capability statements to be sourced or marked, and hiding it would have made §4 a guarantee.
3. `test/docs` adds a package to the tree that CI's `test` job will now run on three OSes. It is
   pure file reading and one `go list -m`, so it should be fast everywhere, but it has only been
   run on Windows here.

---

# Fix round 1 — two Important review findings

Commit: **`1360b6e` `docs(sp18): narrow README's store claim and complete the ADR table`** (one
commit, no trailers). Only `README.md` and `docs/architecture.md` touched; Minor findings deferred
to the final review as instructed, and the `genmcpdocs` anchor defect left alone because the next
task owns it.

## Finding 1 — README's "append-only store" was overbroad

Accepted without reservation; the reviewer is right and the page contradicted its own link target.
`internal/paths/doc.go` scopes the guard to `checkpoints/`, `pins/` and `sketches/tried.bloom`, and
`plans/V5-report.md` §27 records `CompactDemandLog` and `CompactRetentionRoots` as
rewritten-by-design — so "append-only store" would have told a reader that retention and GC never
rewrite, which is false.

Before (`README.md:3-4`):

> It records what a session produces … into a local, **append-only store** under the project's
> `.qompack/` directory through the host's hooks …

After:

> It records what a session produces … into a local store under the project's `.qompack/` directory
> (checkpoints, pins and the elimination bloom are append-only-protected; retention and GC do
> rewrite other parts, see [docs/architecture.md](docs/architecture.md)) through the host's hooks …

I took the reviewer's suggested wording and added the "retention and GC do rewrite other parts"
half, because dropping the adjective alone would have left the exception unstated — and the
exception is the thing a reader would otherwise get wrong. §2 of `docs/architecture.md`, which the
clause now points at, already carries both halves (the guard, and §27's rewritten-by-design note).

## Finding 2 — §9's table was missing two mandated columns

Accepted. Part C item 9 asks for number, title, status/date-as-stated, and kind; the shipped table
had `ADR` and `Kind` and grouped eight ADRs into one cell. §9 now has one row per ADR — twelve rows
— with the linked title and the status/date string transcribed from `docs/adr/README.md`, which is
where those strings were already quoted from each ADR's own header (including "no date line" where
an ADR has none). Links are `adr/NNNN-….md`, relative to `docs/`, and are checked by
`TestRelativeLinksResolve`. The prose notes on 0013 and 0010 below the table are unchanged, and the
table stays consistent with `docs/adr/README.md` as Part C item 9 requires.

## Covering tests

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	1.249s

$ go run ./tools/devtool fmt-check
(no output; exit 0)
```

`TestRelativeLinksResolve` is the one that carries weight here: §9 added twelve new relative links
and one was added to README, and all thirteen resolve. `TestOwnedDocsExist` and
`TestADRIndexListsEveryADR` still pass. No lint rerun, per the instruction — the change is prose in
two Markdown files and touches no Go code.

## Diffstat

```
 README.md            |  8 +++++---
 docs/architecture.md | 24 +++++++++++++++++-------
 2 files changed, 22 insertions(+), 10 deletions(-)
```

Working tree clean after the commit; nothing pushed.

## Concerns after the fix

None new. The three carried from the original report stand unchanged — the `genmcpdocs.go`
`anchorFor` defect (now owned by the next task), the §4 sentence recording V5-report's §8 gate as
not met, and `test/docs` having been exercised on Windows only.
