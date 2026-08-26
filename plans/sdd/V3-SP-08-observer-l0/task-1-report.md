# Task 1 report — Commit 1 (`feat(observer): addressable tombstones, tool tables, task-boundary signals`)

**Status: DONE.** Commit `c0c63c95b60087e11e0f13d078c89adad6409823` on `feat/sp08-observer-l0`,
one commit ahead of `1466b73`, working tree clean.

The two blockers this task originally reported were ruled on by the controller before any code was
written; both rulings are implemented exactly as given, and each is recorded in a comment at the
site it governs so the reasoning does not have to be rediscovered from the plan.

- **Ruling 1** — `reTestFail[2]` is case-SENSITIVE `\bFAILED\b`. Implemented in `signals.go`; the
  regex table carries the rationale, and `TestExtractTestOutcome_CargoPass` is the test that fails
  the moment someone puts `(?i)` back.
- **Ruling 2** — `BenchmarkTombstone` keeps the `< 2 µs/op` clause; the allocation clause is struck.
  `humanBytes` is untouched. Measured numbers are below and in the commit body.

---

## What I implemented

### `internal/observer/tombstone.go`

`Tombstone` gains the four pieces §8.1 item 2 specifies and SP-01's renderer lacked: the `…` after
the 12-hex short hash; `NormalizeToolName(rec.Tool)` in place of the raw host spelling; the
`ArgsPreview` fallback subject truncated to 48 runes; and the ` · ephemeral` / ` · superseded`
segments, in that order, immediately before ` · re-expandable]`. A record with neither a path nor a
preview now elides the subject group **and its leading space**, which is what removes the double
space the old `fmt.Sprintf` form left on a pathless record.

The body is a single `strings.Builder` pre-sized by `tombstoneFixedBytes`, a `const` **expression**
computed from the literal segments rather than a hand-counted number, so it cannot drift from the
format it was derived from. That keeps the renderer's own contribution to one allocation.

`humanBytes`, `bytesPerKB` and `sizeUnits` are byte-for-byte unchanged, GB tier and negative-size
handling included.

Added alongside it: `TombstoneNote()` (the one-line expand affordance), `hostToDisplay` +
`NormalizeToolName`, `compactableTools` + `IsCompactable`, and `supersedableClasses` +
`supersedableClass`. Both predicates run their argument through `NormalizeToolName` first — the
non-blocking reading the controller accepted — so a caller holding a raw `tool_name` off a hook
payload gets the same answer as one holding a `store.ToolUseRecord.Tool`. No row of either test
table changes sign under that.

### `internal/observer/signals.go`

`ExtractSignals`, `ExtractTestOutcome`, `PathsFromInput`, `commandOf` and `responseText`, all pure
functions of a `hookio.Event` — no clock, no state, no I/O. Supporting helpers: `isShellTool`,
`isGitCommit`, `hasCompletedTodo`, and the tolerant decode structs `todoInput`, `pathInput`,
`toolResponse`, `contentBlock`. `TestOutcome` and its three constants live here too, since
`ExtractTestOutcome` returns them.

All five regexes are compiled once in package-level `var`s. `maxScanBytes = 4 << 20` is applied to
the raw `tool_response` **before** any decoding, which is what bounds the decoder's allocation; a
decoded result can therefore never exceed it either, since a JSON string is always at least as long
as the text it encodes. A payload cut at that boundary no longer parses and falls through to the
raw-bytes branch — "the retained prefix used as-is", exactly as the brief specifies.

`Signals.Paths`'s doc comment is corrected per the controller ruling: it now states that the paths
are RAW as the host submitted them, that normalization to `paths.Key` form is the stateful path's
job, and why (purity, so the daemon can reuse it and the fuzzer can drive it).

No new imports beyond stdlib: the package's import set is unchanged from what SP-01 shipped plus
`bytes`, `regexp`, `strings` and `unicode/utf8`. `importgraph` and `testdeps` both PASS.

### `internal/observer/doc.go`

Rewritten: layer L0, the §8.1 eight-item responsibility list, the §5.21 sole-writer rule, the
import discipline including the two allowances SP-08 declines and why, and resolved decisions 1–12
in comment form. It is written as the package's **contract** in normative present tense, with one
sentence up front saying SP-08 lands it across a sequence of commits and that each rule is asserted
by a test in the commit that lands it — so nothing in it claims behaviour that ships today but does
not exist.

### `testdata/golden/observer/tombstones.txt`

13 markers, generated once with `-update` and eyeballed line by line before committing (§8.1 item 2
check below).

### Not touched

`internal/observer/observer.go` — the stub `New` and `stubObserver` are untouched, so commit 2 owns
them. No other file in the package was created.

---

## TDD evidence

### RED

Tests were written in full first. A minimal declaration scaffold (zero-value bodies for the new
symbols, `Tombstone` left exactly as SP-01 shipped it) was added so the package would COMPILE —
without that the run would have been a build failure in which the shipped tests could not pass, and
the task requires them to pass in the RED run.

```
$ go test ./internal/observer/ -run 'Tombstone|HumanBytes|Signals|TestOutcome|PathsFromInput|ResponseText|NormalizeToolName|IsCompactable|SupersedableClass' -count=1
EXIT=1     30 top-level tests FAIL, 13 PASS
```

The three that had to survive did:

```
--- PASS: TestHumanBytes_UsesTheBinaryDivisor
--- PASS: TestHumanBytes_NeverEmitsASpaceBeforeTheUnit
--- PASS: TestTombstone_IsAddressable
```

Representative failures, each for the expected reason:

```
--- FAIL: TestTombstone_RendersTheSection81Form
        expected: "[cleared: sha256:a3f2c9e14b70… · 2.4KB · FileRead src/auth.ts · re-expandable]"
        actual:   "[cleared: sha256:a3f2c9e14b70 · 2.4KB · FileRead src/auth.ts · re-expandable]"
          -> the ellipsis has not landed yet

--- FAIL: TestExtractTestOutcome_GoPass          expected: 0x1  actual: 0x0   (TestPass vs TestUnknown)
--- FAIL: TestExtractSignals_TodoCompleted       Should be true
--- FAIL: TestPathsFromInput_Read                expected: []string{"src/a.ts"}  actual: []string(nil)
--- FAIL: TestSupersedableClass                  expected "filecontent", actual ""
--- FAIL: TestTombstoneGolden                    golden missing (not generated yet)
```

The 13 that passed in RED did so honestly rather than vacuously: they are the rows whose expected
answer IS the zero value (`TestExtractSignals_TodoNoneCompleted`,
`TestExtractTestOutcome_NotATestCommand`, `TestExtractSignals_GitCommitNoop`,
`TestPathsFromInput_EmptyIsNil`, `TestExtractSignals_MalformedJSON`,
`TestExtractSignals_ZeroEventIsZeroSignals`, `FuzzExtractSignals` seed run), plus the two size-tier
`Contains` assertions that the shipped renderer already satisfied, plus the three shipped tests
above.

### GREEN

```
$ go test -count=1 ./internal/observer/...
ok  	github.com/qompack/qompack/internal/observer	1.209s
ok  	github.com/qompack/qompack/internal/observer/observertest	1.158s
EXIT=0
```

Output pristine — no `FAIL`, no unexpected `SKIP`, no logging noise.

---

## Verification commands, each run separately

| Command | Result |
|---|---|
| `go run ./tools/devtool fmt` | clean, no rewrites (git status showed only my own edits) |
| `go run ./tools/devtool lint` | **exit 0**, all 10 subchecks PASS |
| `go run ./tools/devtool vet` | **exit 0**, no output |
| `go test -count=1 ./internal/observer/...` | **exit 0**, both packages ok |
| `go test -count=1 -cover ./internal/observer/` | **93.8%** of statements (floor is 75%) |
| `go run ./tools/devtool check-commit-msg /tmp/msg.txt` | **exit 0** against the real committed message |

`devtool lint` subchecks: `golangci-lint`, `nomagic`, `importgraph` (62 pkgs), `testdeps` (64 pkgs),
`bindeps`, `sleepcheck`, `stubskips`, `runpatterns`, `docmarkers`, `coveragefloors` — all PASS.

`stubskips` reports `internal/observer/observertest: 2 skip(s)`, which is the expected count: both
suite invocations still skip their `/behaviour` block with the exact Rule W-1 message, because
`observer.New` is still SP-01's stub. Nothing in this commit changes that, and commit 2 flips it.

### Benchmark

```
$ go test -count=1 -run '^$' -bench BenchmarkTombstone -benchmem ./internal/observer/
goos: windows  goarch: amd64  cpu: Intel(R) Core(TM) Ultra 7 155H
BenchmarkTombstone-22    2796277    434.1 ns/op    272 B/op    6 allocs/op
```

**434.1 ns/op against the 2 µs/op budget — 4.6x margin.** Allocation breakdown, since the struck
clause is worth accounting for precisely: 1 is `Tombstone`'s own pre-sized `strings.Builder`
(`b.String()` is a no-copy conversion); 2 come from `core.Hash.Short`, which is
`hex.EncodeToString(h[:])[:12]` and so allocates a `[]byte` and then a `string`; 3 come from
`humanBytes`'s `fmt.Sprintf("%.1f%s", …)` — the result string, the boxed `float64`, and the
variadic slice. Five of the six are in functions this commit is forbidden to change.

### Fuzz

```
$ go test -count=1 -run FuzzExtractSignals -fuzz FuzzExtractSignals -fuzztime 20s ./internal/observer/
fuzz: elapsed: 21s, execs: 920402 (22820/sec), new interesting: 375 (total: 387)
PASS  EXIT=0
```

920,402 executions, no crashers, no `testdata/fuzz/` corpus written into the package. Seeded from
five hand-written payloads plus seven `testdata/corpora/toolout/` files (go-test pass/fail,
cargo-test pass, pytest fail, git status, grep symbol, ANSI build), reused rather than extended, as
the fixture list requires.

---

## Golden fixture: eyeballed against §8.1 item 2

`testdata/golden/observer/tombstones.txt`, 13 lines, verified with `cat -A`: every separator is
`M-BM-7` (C2 B7 = U+00B7 MIDDLE DOT with one space each side), every ellipsis is `M-bM-^@M-&`
(E2 80 A6 = U+2026), every line ends LF with no CR.

| # | Branch it covers | Rendered |
|---|---|---|
| 1 | the §8.1 design example, KB tier | `…a3f2c19d0b74… · 2.4KB · FileRead src/auth.ts · re-expandable]` |
| 2 | host name normalized (`Read`→`FileRead`), B tier | `· 973B · FileRead src/index.ts ·` |
| 3 | no path → preview subject | `· 812B · Bash go test ./internal/store/... ·` |
| 4 | path subject, exact KB boundary | `· 1.0KB · Grep internal/observer ·` |
| 5 | preview subject on a search tool | `· 4.0KB · Glob **/*.go ·` |
| 6 | MB tier, URL as subject | `· 3.3MB · WebFetch https://pkg.go.dev/regexp ·` |
| 7 | GB tier | `· 3.0GB · FileWrite dist/bundle.js ·` |
| 8 | superseded alone | `· FileRead src/auth.ts · superseded · re-expandable]` |
| 9 | ephemeral alone, MCP name passes through | `· mcp__qompack__recall src/auth.ts · ephemeral ·` |
| 10 | both, ephemeral first | `· ephemeral · superseded · re-expandable]` |
| 11 | no subject at all, no double space | `· 0B · Bash · re-expandable]` |
| 12 | preview truncated | subject is 47 runes + `…` (counted: `…-timeout=30m ./internal/` ends at 47) |
| 13 | non-compactable tool still renders | `· AgentTool review the diff for correctness ·` |

Size tiers B, KB, MB and GB all appear. Line 11 confirms the double-space fix; line 12 confirms the
48-rune cap.

---

## Files changed

```
internal/observer/doc.go                | 143 ++++++++--     rewritten
internal/observer/signals.go            | 306 +++++++++++++    implemented
internal/observer/signals_test.go       | 356 +++++++++++++    new
internal/observer/tombstone.go          | 219 ++++++++++++     extended
internal/observer/tombstone_test.go     | 401 +++++++++++++    extended
testdata/golden/observer/tombstones.txt |  13 ++              new
6 files changed, 1389 insertions(+), 49 deletions(-)
```

`internal/observer/observer.go` is deliberately absent from that list.

---

## Self-review findings

I re-read the diff against the brief's function list and both test tables.

**Function list — complete, nothing extra.** Every symbol the brief assigns to commit 1 is present:
`Tombstone`, `TombstoneNote`, `NormalizeToolName`, `IsCompactable`, `supersedableClass`,
`ExtractSignals`, `ExtractTestOutcome`, `PathsFromInput`, `commandOf`, `responseText`, plus
`TestOutcome`/`TestUnknown`/`TestPass`/`TestFail`. Nothing from later commits leaked in — no `Mode`,
`SymbolLister`, `Rehydrator`, `FeatureSample`, `SubagentToolRef`, `SubagentCapture`,
`VerbatimPromptID`, `SubagentCaptureID`, `Persister`, and no widening of `Options` or `New`.

**Tests beyond the brief's rows.** I added ten. Each covers a branch the brief's own rows leave
unexercised, and none of them invents behaviour:

- `TestTombstone_NormalizesTheHostToolName` — the brief specifies `NormalizeToolName(rec.Tool)` in
  the renderer, but no table row asserts it end-to-end.
- `TestExtractTestOutcome_PowerShell` and `TestExtractTestOutcome_NotAShellTool` — PowerShell is
  half of the brief's own `∈ {Bash, PowerShell}` gate; the negative case pins the gate itself.
- `TestExtractSignals_GitCommitWithDashC` — the `-C` alternation is in the brief's `reGitCommit`
  and had no row.
- `TestPathsFromInput_NotebookAndOrder` and `TestPathsFromInput_EmptyIsNil` — the emission order
  and the nil return are both brief/ruling requirements with no row of their own.
- `TestExtractSignals_ZeroEventIsZeroSignals`, `TestExtractSignals_ReadReportsItsPath` and
  `TestExtractSignals_PathsAreRaw` — mirror the `observertest` fixtures and the RAW-paths ruling
  in-package, where a failure names itself.
- `TestCommandOf` — `commandOf` is on the brief's function list with no row.

**Test replaced as the brief directs.** `TestTombstone_HandlesAnEmptyRecord` is gone, succeeded by
`TestTombstone_NoSubject`, which keeps its non-panic guarantee (`require.NotPanics` on the zero
record) and adds the no-double-space assertion.

**`TestTombstone_DesignExample`.** Satisfied through the updated `TestTombstone_RendersTheSection81Form`
plus line 1 of the golden (which uses the table's own `a3f2c19d0b74` / 2458 values). I did not add a
second near-identical test, per the non-blocking reading the controller accepted.

**One thing I checked and left alone.** `PathsFromInput`'s `tool` parameter is not dispatched on. I
confirmed this is lint-clean rather than assuming: `unparam` does not check exported functions by
default, and revive's `unused-parameter` rule is not enabled in `.golangci.yml`. The doc comment
explains the design reason (the key union is tool-independent so a new host key is picked up without
a table edit) without reading as a placeholder.

**Commit hygiene.** One commit. Subject is the ruled string exactly, 58 characters after
`feat(observer): ` (cap is 64). Body lines max 94 runes. Footer is exactly
`Refs: SP-08, G3.2, G1.5, §8.1 item 2, §2.2`. A case-insensitive grep of the committed message for
`co-authored-by|signed-off-by|generated with|claude|🤖` returns nothing. No branches created, no
merge, no push.

---

## Concerns

1. **`BenchmarkTombstone` records 6 allocs/op, not the 4 my pre-implementation probe predicted.**
   The extra two are `core.Hash.Short`, which I had not modelled — `hex.EncodeToString(h[:])[:12]`
   allocates a 64-byte slice and then a 64-byte string to hand back 12 characters. It is in
   `internal/core` and outside SP-08's ownership, so I left it. If the ADR in commit 7 wants the
   renderer nearer one allocation, the cheapest honest fix is an `AppendShort(dst []byte) []byte`
   on `core.Hash` plus an `appendHumanBytes` here — together they would take this to 1 alloc/op —
   but both are amendments against frozen code and neither is in scope for this commit. The time
   budget is met with 4.6x margin either way, so nothing is blocked.

2. **`doc.go` documents decisions 1–12, most of which describe code that lands in commits 2–7.** I
   wrote it in normative present tense as the package contract, with an explicit sentence saying the
   package lands across a sequence of commits. It is accurate as a contract but a reader who diffs
   `doc.go` against today's `observer.go` will see the gap. This is what the brief instructs, so I
   have not hedged it further — flagging it only so the commit-7 review knows it was deliberate.

3. **The heredoc route to writing files was unusable in this environment.** Apostrophes in prose
   comments broke `bash <<'EOF'` twice with `unexpected EOF while looking for matching '`. I fell
   back to the Write tool for the two large files. No effect on the result; noting it so the next
   task does not lose time to the same thing.

Nothing here blocks commit 2.
