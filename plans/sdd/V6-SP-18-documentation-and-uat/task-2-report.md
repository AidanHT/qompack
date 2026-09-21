# Task 2 report — SP-18 Part 0 + Commit 2

Routing requested: model `claude-opus-4-8`, effort high.
Routing observed: this session reports itself as **Opus 5 (1M context), model id `claude-opus-5[1m]`**.
The requested `claude-opus-4-8` seat is NOT what ran; recording the mismatch as instructed. Effort
level is not observable from inside the session.

Worktree: `C:\Users\Quant\Documents\Programming\Projects\qompack-sp18`, branch
`feat/sp18-documentation-and-uat`, base `1360b6e`.

---

## Commit 1 (Part 0) — `fix(devtool): slug MCP tool anchors the way GitHub does`

SHA: `ddb1a41`

### What it does

- `tools/devtool/genmcpdocs.go`: `anchorFor` was `strings.ReplaceAll(name, "_", "-")`. It now
  reproduces GitHub's heading slug — lowercase, a space becomes `-`, `_` and `-` survive (an
  underscore is a word character to that slugger), every other character is dropped. The doc
  comment records the defect it fixes.
- `tools/devtool/genmcpdocs_test.go`: new `TestMCPDocsGlanceLinksResolve`. It derives the tool set
  from `mcp.ToolDefs(mcp.ToolDeps{})` (no count literal), re-derives the slug rule from the `## `
  headings the renderer actually emits, and asserts every glance-table link resolves to one of
  them. Re-deriving the rule from the headings rather than calling `anchorFor` on both sides is
  deliberate: a test that asked the generator to agree with itself would have passed while the page
  shipped `#re-read` for a heading that answers to `#re_read`.
- `docs/mcp-tools.md`: regenerated (3 link cells: `#re_read`, `#already_tried`,
  `#record_eliminated`).
- `test/docs/links_test.go`: `knownAnchorDefects` and BOTH of its enforcement blocks removed
  (the switch in the per-target loop and the "listed but nobody links it" sweep, plus the now-unused
  `hit` map). The link harness is strict again.

### RED / GREEN evidence

The test was written first and run against the OLD `anchorFor` (temporarily restored in place; the
fixed file was kept aside and copied back immediately after — no `git stash` was used):

```
--- FAIL: TestMCPDocsGlanceLinksResolve (0.00s)
    genmcpdocs_test.go:210:
        Error:      Should be true
        Messages:   the glance-table link for "re_read" points at #re-read, which no heading in
                    the page offers; anchorFor must produce GitHub's slug of the heading text
FAIL    github.com/qompack/qompack/tools/devtool        1.288s
```

With the fix restored:

```
ok      github.com/qompack/qompack/tools/devtool        1.376s
```

### Validation (brief §Part 0 item 4), run once before committing

```
$ go test -count=1 ./tools/devtool/ ./test/docs/...
ok      github.com/qompack/qompack/tools/devtool        3.670s
ok      github.com/qompack/qompack/test/docs            1.139s

$ go run ./tools/devtool gen-mcp-docs --check
gen-mcp-docs: docs/mcp-tools.md is up to date

$ go run ./tools/devtool fmt-check
exit=0
```

### Regenerated page diff

```
$ git diff --stat docs/mcp-tools.md
 docs/mcp-tools.md | 6 +++---
 1 file changed, 3 insertions(+), 3 deletions(-)
```

The three changed lines are the three glance-table cells; nothing else in the page moved.

### Files changed

- `tools/devtool/genmcpdocs.go`
- `tools/devtool/genmcpdocs_test.go`
- `docs/mcp-tools.md` (regenerated only)
- `test/docs/links_test.go`

---

## Commit 2 — `feat(devtool): document versioned configuration metadata`

SHA: `4acc129`

### Part A — exported accessors (`internal/config/migration.go`)

- `type RetiredMeaningKey struct { Key, Note string }` and `RetiredMeaningKeys() []RetiredMeaningKey`,
  built from the unexported `retiredMeaningKeys` in table order. The unexported table stays the
  single source; no row is duplicated.
- `type VersionedSection struct { Path string; Version int }` and `VersionedSections()`.
  `applyVersionedSections` (migration.go:183) hard-coded the two rows in an anonymous struct slice,
  so — as the brief directs — ONE unexported table `versionedSections` was introduced and the
  function now ranges over it. Behaviour is identical: same two blocks, same order, same versions.
- Two doc comments were re-pointed so they name `versionedSections` rather than "here". No other
  change in `internal/config`.

### Part B — generator sections (`tools/devtool/genconfigdocs.go`)

`renderConfigDoc` now calls `writeConfigMetadata(&b)` after the per-section tables. Four sections,
in the brief's order, every row rendered from `internal/config` (the generator contains no config
key of its own):

1. `## Provenance origins` — one row per `Origin`, ascending from `OriginDefault`. The enum bounds
   itself: the loop runs while `o.String() != "unknown"`, which is exactly the `default:` arm of
   `Origin.String()` (config.go:205-229), not a guessed count. Meanings come from an
   `originMeanings` map keyed by the `config.Origin` CONSTANTS (not by name), one short phrase per
   the constants' doc comments, falling back to the `String()` name for an origin with no entry.
   The pre-existing "Run `qompack config print --provenance`" sentence in the page header is
   untouched; the new section opens with its own sentence.
2. `## Versioned blocks` — from `config.VersionedSections()`: Block, `settingsVersion` this build
   understands, Behaviour. One sentence above says the two blocks are versioned independently.
3. `## Gated switches (ship off)` — from `config.MigrationGates()`: Key (code span, not a markdown
   link), Default (read out of the schema leaf by `gateDefaultCell`, never asserted), Owner, Gate,
   Status (`pending` when `!Passed`, else `passed`). One sentence above describes the load
   behaviour. Then `### Build gates (no config key)` from `config.MigrationBuildGates()`: Key,
   Owner, Gate, Status, with its own sentence that these are unreachable from any config layer.
4. `## Retired-meaning keys` — from `config.RetiredMeaningKeys()`: Key, What it no longer means.
   One sentence above says the value is still applied and that a non-default layer produces a
   deprecation warning naming the file and line.

Every rendered cell goes through `escapePipes`. The existing header text and the existing tables'
columns are unchanged (see the additive-diff confirmation below).

### Part C — tests

`tools/devtool/genconfigdocs_test.go` (new, written before the generator changes):

1. `TestGenConfigDocs_LeavesMatchDefaultsOneToOne` — parses the first cell of every key row under a
   code-span `## \`section\`` heading into a set, walks `config.Defaults().JSONSchema()` leaves into
   a set, asserts equality in both directions with the missing/extra keys named. Restricting to
   code-span headings is what keeps the metadata sections' keys — a build gate has no config leaf
   at all — out of the inventory. No count literal.
2. `TestGenConfigDocs_GatedSwitchesRenderFromSource` — five subtests: gated switches (present,
   Default cell `false`, schema default `false`, Owner/Gate/Status against the accessor), build
   gates (present, and NOT a schema leaf), retired-meaning keys (present, Note matches, key is
   still a documented leaf), versioned blocks (present with its Version), provenance origins
   (ascending from `Origin(0)`, every meaning non-empty, and the table stops exactly where the enum
   does — `Origin(len(rows)).String() == "unknown"`). Each table is also checked for rows the
   accessor does not have, via `require.Len(rows, len(accessor()))` — a length against a live
   source, not a literal.
3. `TestGenConfigDocs_CommittedPageIsCurrent` — same shape as
   `TestGenCommandDocs_CommittedPageIsCurrent`, CRLF-normalised on the read side.
4. `TestGenConfigDocs_CheckDetectsMissingAndStale` — `root` pointed at a `t.TempDir()`:
   `--check` errors with "is missing"; after writing the rendered page it returns nil; after
   appending one byte it errors with "is stale". Both errors are asserted to name `configDocPath`.

`internal/config/migration_test.go` (two rows appended):

- `TestRetiredMeaningKeys_MirrorTheDeprecationTable`
- `TestVersionedSections_MatchTheConstantsAndTheSchema`

### RED / GREEN evidence

Part A tests written first, against a package with no accessors:

```
internal\config\migration_test.go:338:27: undefined: config.RetiredMeaningKeys
internal\config\migration_test.go:372:27: undefined: config.VersionedSections
FAIL    github.com/qompack/qompack/internal/config [build failed]
```

After Part A:

```
--- PASS: TestRetiredMeaningKeys_MirrorTheDeprecationTable (0.44s)
--- PASS: TestVersionedSections_MatchTheConstantsAndTheSchema (0.49s)
ok      github.com/qompack/qompack/internal/config      2.193s
```

Part C tests written next, against the un-extended generator — each metadata subtest failed on the
absent section, and the two pre-existing-behaviour rows (one-to-one inventory, --check) passed, as
they should:

```
--- FAIL: TestGenConfigDocs_GatedSwitchesRenderFromSource/gated_switches
    Messages: the page must have a "## Gated switches (ship off)" section
--- FAIL: .../build_gates        the page must have a "### Build gates (no config key)" section
--- FAIL: .../retired-meaning_keys   the page must have a "## Retired-meaning keys" section
--- FAIL: .../versioned_blocks   the page must have a "## Versioned blocks" section
--- FAIL: .../provenance_origins the page must have a "## Provenance origins" section
```

After Part B, only the stale-page row remained red (the page had not been regenerated yet):

```
--- FAIL: TestGenConfigDocs_CommittedPageIsCurrent (0.01s)
    Messages: docs/config-reference.md is stale; run `go run ./tools/devtool gen-config-docs`
```

and after Part D's regeneration the whole package is green (see validation below).

### Part D — regeneration

```
$ go run ./tools/devtool gen-config-docs
gen-config-docs: wrote docs/config-reference.md

$ go run ./tools/devtool gen-config-docs --check
gen-config-docs: docs/config-reference.md is up to date

$ git diff --stat docs/config-reference.md
 docs/config-reference.md | 64 ++++++++++++++++++++++++++++++++++++++++++++++++
 1 file changed, 64 insertions(+)
```

**The diff is purely additive**: 64 insertions, 0 deletions. Confirmed directly —
`git diff --unified=0 docs/config-reference.md | grep '^-' | grep -v '^---'` returns nothing, i.e.
not one pre-existing line was removed or altered, and the 64 added lines are all at the end of the
file after the last per-section table.

### Validation (brief §Validation before committing), run once before committing

```
$ go test -count=1 ./tools/devtool/ ./internal/config/...
ok      github.com/qompack/qompack/tools/devtool        3.269s
ok      github.com/qompack/qompack/internal/config      2.083s

$ go run ./tools/devtool gen-config-docs --check
gen-config-docs: docs/config-reference.md is up to date

$ go run ./tools/devtool gen-mcp-docs --check
gen-mcp-docs: docs/mcp-tools.md is up to date

$ go run ./tools/devtool gen-command-docs --check
gen-command-docs: docs/commands.md is up to date

$ go run ./tools/devtool fmt-check
(no output, exit 0)

$ go run ./tools/devtool lint
== devtool lint: golangci-lint ==   PASS
== devtool lint: nomagic ==         PASS
== devtool lint: importgraph ==     importgraph: OK (67 package(s) checked)   PASS
== devtool lint: testdeps ==        testdeps: OK (69 package(s) checked)      PASS
== devtool lint: bindeps ==         bindeps: OK (6 target(s) checked)         PASS
== devtool lint: sleepcheck ==      sleepcheck: OK                            PASS
== devtool lint: stubskips ==       stubskips: OK                             PASS  (exit 0)
(runpatterns, docmarkers, coveragefloors queued behind stubskips)
```

Note on `devtool lint`: its `stubskips` sub-check runs the WHOLE `internal/ cmd/ test/` test
suite under `-json`, which is the run the brief otherwise excludes, and it is co-loaded with the
SP-17 session on this machine. An earlier full `lint` run aborted at `stubskips` with
`exit status 0xffffffff` / exit 127; per the controller that window (11:22-11:36) is when the
SP-17 session killed devtool/go/*.test processes, so that abort is a KILL, not a red. A fresh
`lint --only=stubskips` was started at 11:36:08 and was still running at 12:44 (its `go test`
driver alive at 1404s CPU). Per the controller's ruling the commit did not wait on it: this change
adds no `t.Skip` and no package, so it cannot move that sub-check. Its exit code and output tail
are appended below when it lands.

```
```

Out of scope per the brief and not run: the whole tree, race, coverage, replay.
Run additionally, because Part 0 touched its allowlist and the new page adds headings it scans:
`go test -count=1 ./test/docs/...` → `ok  github.com/qompack/qompack/test/docs  1.702s`.

### Files changed

- `tools/devtool/genconfigdocs.go` (additive: `writeConfigMetadata` and its four writers,
  `originMeanings`, `gateStatus`, `gateDefaultCell`; one call added at the end of `renderConfigDoc`)
- `tools/devtool/genconfigdocs_test.go` (new)
- `internal/config/migration.go` (accessors + the one shared `versionedSections` table)
- `internal/config/migration_test.go` (two rows and three helpers appended)
- `docs/config-reference.md` (regenerated only)

---

## Self-review against the brief

| Brief item | Status |
|---|---|
| Part 0.1 anchorFor produces GitHub's slug; test derives names from `mcp.ToolDefs`, no count | done |
| Part 0.2 regenerate, `--check` passes | done |
| Part 0.3 `knownAnchorDefects` + both enforcement blocks removed; `./test/docs/...` green | done |
| Part 0.4 validation list | done, output above |
| Part 0 file scope (4 files) | respected |
| Part A.1 `RetiredMeaningKey` + `RetiredMeaningKeys()`, copy, table order, single source | done |
| Part A.2 `VersionedSection` + `VersionedSections()`, ONE shared unexported table | done |
| Part A tests: every Key a schema leaf; every Path an object; Version == constant | done |
| Part B.1 Provenance origins, ascending, enum's own bound, meanings from doc comments, header sentence kept | done |
| Part B.2 Versioned blocks, one sentence above, from `VersionedSections()` | done |
| Part B.3 Gated switches (Key/Default/Owner/Gate/Status) + `Build gates (no config key)` | done |
| Part B.4 Retired-meaning keys | done |
| `escapePipes` on every rendered cell; header text and existing columns unchanged | done |
| Part C.1-C.4 four tests, no count literal | done |
| Part D regenerate + additive diff confirmation | done |
| Claims policy: no performance/cost/savings, no rate schedule, no leaf count | done |
| Two commits, exact subjects, no trailers | done |
| Never `git stash`, never push, nothing else edited | respected |

### Findings from the self-review

1. **One deliberate deviation, Part A tests.** The brief asks that `RetiredMeaningKeys()` be
   compared "element-wise" with the unexported `retiredMeaningKeys`. Every test file in
   `internal/config` is `package config_test`, and the brief forbids touching any other file in
   that package, so the unexported table is not reachable from where the test must live. Rather
   than assert a weaker property, the test compares the accessor against the only OTHER reader of
   that table — the deprecation diagnostic itself. It loads a config document that sets every leaf
   in the schema to that leaf's own default (so every candidate key is touched from a non-default
   layer without changing one effective value) and asserts the resulting `Deprecated` warnings and
   the accessor's rows are the same set, both directions, message text included. A row the accessor
   dropped shows up as a warning nobody claimed; a row it invented as a claim nobody warned about.
   That is stronger than an element-wise comparison against the table, not weaker. It also asserts
   the returned slice is a copy. If the coordinator prefers the literal element-wise form, it needs
   a new `export_test.go` in `internal/config`, which the file-scope rule did not authorise.
2. `require.Len(rows, len(config.MigrationGates()))` and friends in the devtool test are lengths
   against a live accessor, not count literals — the brief's ban is on hardcoded counts, and there
   is none.
3. The `Default` column of the gated-switch table is READ from the schema (`gateDefaultCell`), not
   written as `false` by the generator; the test is what requires the value to be `false`. A gate
   whose leaf stopped defaulting off would therefore show the true value on the page AND fail the
   test, rather than have the page quietly lie.
4. `platformDefaultCells` in the generator still contains three literal config keys. It is
   pre-existing, documented, and out of this task's scope; the "no literal keys" rule was applied
   to everything this commit adds.
5. `TestGenConfigDocs_GatedSwitchesRenderFromSource` asserts the origins table stops exactly where
   the `Origin` enum does. If a sixth origin is ever added without an `originMeanings` entry, the
   page renders its `String()` name as the meaning (the brief's stated fallback) and the test still
   passes — that is intended, and it is why the map is keyed by the constant rather than the name.

## Concerns

- **Routing mismatch** (above): `claude-opus-4-8` was requested; the session identifies as
  `claude-opus-5[1m]`.
- The `### Build gates (no config key)` sub-table is rendered as an `h3` under
  `## Gated switches (ship off)` because the brief calls it "a second small table" within that
  section. If the reviewer wants it as its own `##`, it is a one-line change in `writeGateSection`
  plus a regeneration and one string in the test.
- Part 0 changes anchors that other prose may have linked by their old spelling. `test/docs`'s link
  harness is strict again and passes on the whole of `README.md` + `docs/**`, so nothing in the
  scanned set referenced `#re-read` and friends by hand; anything outside `docs/` (e.g. `plans/`)
  is not scanned by that harness and was not checked.
- `internal/config/migration.go`'s two doc comments were re-pointed at `versionedSections` so they
  do not describe a function that no longer holds the rows. That is a comment-only edit inside the
  file the brief allows; no behaviour changed.

---

## Appendix — `stubskips` result (appended on exit)

The re-run started 11:36:08 finished clean.

```
$ go run ./tools/devtool lint --only=stubskips
== devtool lint: stubskips ==
stubskips: github.com/qompack/qompack/internal/analyzer/analyzertest: 1 skip(s)
  ... (25 packages reporting skip counts, then 19 "platform-gated, not run on this host" notices)
stubskips: OK
PASS stubskips
EXIT=0
```

Nothing in either commit appears in that output: no `t.Skip` was added and no package was created,
which is why the controller ruled it not a gate for this change. `internal/config`'s one skip is
the pre-existing `TestLoadForCapture_RefusesUnsafeConfigFileLeaves/symlink_leaf` platform gate.

With this, every `devtool lint` sub-check that ran is PASS: golangci-lint, nomagic, importgraph,
testdeps, bindeps, sleepcheck, stubskips. The three that queue behind stubskips in a full run
(runpatterns, docmarkers, coveragefloors) read plan Markdown and `plans/OWNERS.tsv` only; neither
commit touches `plans/`.
