# Task 5 report — Commit 5 `docs(sp18): state capability limits and upstream proposals`

Commit: `2bbd441` on `feat/sp18-documentation-and-uat` (parent `8abb7b8`). Four files, 792
insertions, no deletions, no trailers.

No issue was filed or sent.

## Routing

Requested: model `claude-opus-4-8`, effort medium (raised to high on supported-scope and
native-boundary wording). Observed: the session's own system context names the model as
`claude-opus-5[1m]` ("Opus 5 (1M context)"). I cannot verify effective routing from inside the
session, so the requested id and the observed id are recorded as they are, unreconciled.

## Files changed

- `docs/cannot-do.md` — new, 385 lines.
- `docs/upstream-issues.md` — new, 179 lines.
- `test/docs/limits_test.go` — new, 226 lines, in the existing `test/docs` package.
- `test/docs/owned_test.go` — `ownedDocs` gains the two pages. Nothing else in that file.

No other file was touched. No `git stash`, no push, nothing opened or drafted in any tracker.

## `docs/cannot-do.md` — entry by entry, with its source

Every entry carries **Limit / Why / What Qompack does instead / Recorded at**, per the brief.

### 1. Native context control

| Entry | Sources read and cited |
|---|---|
| No native-history cuts, and no marker control | `Qompack.md` v1.5 §12; `plans/MIGRATION-EVIDENCE.md` capability table row *History rewriting / native eviction — unsupported — Excluded* (line 95) and "Capability register inputs" (line 434: "two in-code statements say a plugin cannot move native cache markers"); `docs/architecture.md` §10 |
| No deletion of already-delivered results | `Qompack.md` v1.5 §12; `plans/MIGRATION-EVIDENCE.md` item 7 "Host boundary" (line 49); `docs/config-reference.md` "Gated switches (ship off)" for `runtime.migration.replacement.newResult` |
| No history eviction; `ephemeral` is a Qompack record property | `internal/mcp` package comment ("Retrieval metadata marks Qompack representations ephemeral; it does not control host eviction or establish native context retention"); `docs/mcp-tools.md` intro; `plans/MIGRATION-EVIDENCE.md` capability table |
| No native compaction request/veto | `Qompack.md` v1.5 §12; `internal/config/validate.go:345-350` (the hardwired `blockManualCompact` violation, quoted verbatim); `docs/config-reference.md` gated switches (`automaticVeto`, "the recovery/proactive distinction is unverified") and retired-meaning keys (`scheduler.youngDaly.enabled`, `scheduler.idle.deepCutWhenCold`) |

### 2. Proof

| Entry | Sources |
|---|---|
| No proof of model compliance | `internal/contract/capability.go` `CapInjection` notes ("never complete context or model compliance"); `Qompack.md` v1.5 §12 |
| No exact native loaded bytes | `internal/contract/ids.go` `CAdditionalContext`; `internal/contract/assertions.go` `gated` ("never runs at all"); `docs/troubleshooting.md` §1/§2; cross-linked to proposal 1 |
| No summarizer model substitution | `Qompack.md` v1.5 §7.3 ("`custom_instructions` is PreCompact input, not a summarizer-output setter"); `docs/config-reference.md` retired-meaning key `checkpoint.incrementalSpanInstruction`; `precompact.custom_instructions_accepted` not-yet-implemented |
| The rehydration budget is Qompack-added material | ADR 0011 §3 (hard cap never raised) and §4; `docs/user-guide.md` "Additional context: budget and overflow"; `docs/architecture.md` §7 |
| No PostCompact dependency | `internal/contract/capability.go` ("Reinjection has no PostCompact prerequisite: it cannot wait for an optional event"); cross-linked to proposal 3 |

### 3. Reconstruction

| Entry | Sources |
|---|---|
| Cannot reconstruct uncaptured history from current files | `Qompack.md` v1.5 §12; `plans/00-ARCHITECTURE.md` §0.2.2 as quoted by `docs/architecture.md` §10 ("forbids any handle or pointer that claims it recoverable"); `docs/mcp-tools.md` (`re_read` is "never a live read of disk") |
| A missing or `redacted`/`truncated`/`binary` original stays that way | `internal/core/evidence.go` enumerations as tabulated in `docs/user-guide.md` "Fidelity, coverage and error states"; ADR 0013 ("errors read as absence" is a named defect) |
| No complete capture of unobserved child work | `Qompack.md` v1.5 §12 and §7.3 (seven events in `internal/pluginmanifest/manifest.go`); `docs/mcp-tools.md#dropped`; `docs/troubleshooting.md` §3 |

### 4. Guarantees Qompack does not make

| Entry | Sources |
|---|---|
| No universal improvement, no universal savings | `plans/V5-report.md` §26 (no held-out runs, T21-QUALITY-01 inconclusive by construction) and §24 |
| No performance guarantee on any host | ADR 0010 Context table (same commit, one hosted CI runner class, minutes apart, byte-identical product: B-A p99 3.072 ms isolated vs 18.432 ms whole-tree against a 15 ms limit), "What this does not decide", Addendum 1 (the B-B exemption premise falsified); `plans/V5-report.md` §29 item 2 |
| No cost or price guarantee | `plans/V5-report.md` §25 (categories/TTL/retries `unknown`; estimated price `unknown`); `Qompack.md` Appendix A ("unknown stays unknown"); cross-linked to proposal 6 |
| No name-availability guarantee | `plans/V5-report.md` §24 (host allowlist empty) and §29 item 6 (T21-HOST-01 needs an installed host) |
| No inference of cache state from arbitrary elapsed time | `Qompack.md` v1.5 §12 and Appendix A (break-even as a conditional diagnostic); `plans/QOMPACK-ERRATA.md` v1.3 "What could not be verified, and will not be"; ADR 0009 named as being about Qompack's own Bloom, not the host's prompt cache |

### 5. Trust boundary

| Entry | Sources |
|---|---|
| An archive is never a denied-read bypass | `Qompack.md` v1.5 §12 (risk-register row *Trust/privacy leak* → "no denied-read bypass", and the prose subsection); `docs/troubleshooting.md` §4; `docs/user-guide.md` fidelity/outcome tables |
| Redaction at capture; telemetry hardwired off | `internal/config/validate.go`; `docs/config-reference.md#runtime`; `internal/mcp` package comment (decision D10, no network I/O); `docs/troubleshooting.md` §2 ("Nothing is sent anywhere") |

### 6. Not in this build

| Entry | Sources |
|---|---|
| `/qompack:checkpoint` unrouted | `docs/commands.md#qompackcheckpoint` ("Not available in this build … SP-14 handoff edge H3"); `plans/V5-report.md` §29 item 3 |
| `doctor`/`fsck` unimplemented | `docs/user-guide.md` "Operator commands"; `docs/troubleshooting.md` §9; `plans/V5-report.md` §29 item 7 for the `bench` survivor |
| Installed-host compatibility `implemented_unverified` | `plans/V5-report.md` §24 (B01); `plans/MIGRATION-EVIDENCE.md` "Capability register inputs" ("No test or document in either tree claims installed-host verification for any capability; B01 stands"); the four not-yet-implemented contract ids named here |
| Enabled-surface rows that are "mechanism only" | `plans/V5-report.md` §24 SP-21 enabled-surface matrix (admission gate off, host allowlist empty, three mechanism-only rows, quality row with no observations); `docs/config-reference.md` gated switches and build gates; README "What ships off" |

Closing section "How to read the rest of the docs" states the planned (SP-NN) convention and the
claims rule (read from a named source and cited, or marked `unknown` with what would answer it).

## `docs/upstream-issues.md` — proposal by proposal, with its evidence

Header states: proposals prepared by SP-18, none filed, nothing sent or drafted anywhere, a proposal
becomes an issue only with separate authorization and the URL is then recorded beside it. A Status
table lists all seven as `not filed`, and every section ends with **Status: not filed.**

1. **Observability of delivered `additionalContext`** — evidence: `hook.additional_context_delivered`
   not-yet-implemented (`internal/contract/ids.go`, `assertions.go` `gated`); ADR 0011 (bounds what
   is emitted, cannot observe what was delivered); `internal/contract/capability.go` injection note.
2. **A PreCompact contract for `custom_instructions` acceptance and time to write** — evidence:
   `precompact.has_time_to_write` and `precompact.custom_instructions_accepted` not-yet-implemented,
   with each constant's own comment quoted; `Qompack.md` v1.5 §7.3.
3. **A post-compaction signal** — evidence: `internal/contract/capability.go` "Reinjection has no
   PostCompact prerequisite"; `session_start.source_compact`'s constant comment; §7.3's
   "installed support remains unverified". States that Qompack would keep working without it.
4. **Per-hook latency attribution** — evidence: `internal/commands/statuscollect.go` `hookRows`,
   the `no per-hook instrument: %s is folded into the %s aggregate, which mixes every delivering
   hook and cannot be attributed to one` reason string, plus the B-D reason ("which the created
   process cannot observe"), both as quoted in `docs/troubleshooting.md` §2.
5. **MCP server registration visibility** — evidence: `mcp.server_registered` not-yet-implemented
   (`CMCPRegistered`, "received initialize at least once this session"); `internal/mcp` package
   comment on one server per client with no session_id.
6. **Usage telemetry per request category** — evidence: `plans/V5-report.md` §25 (quoted);
   `plans/MIGRATION-EVIDENCE.md` (usage attribution parses only `output_tokens`,
   `internal/eval/importer.go:55-57`); `Qompack.md` Appendix A "unknown stays unknown".
7. **A supported compaction request/veto or marker API** — evidence: §12's "must function without
   native compaction request/veto"; `plans/MIGRATION-EVIDENCE.md` unsupported/Excluded rows; the
   gated `automaticVeto` and the hardwired `blockManualCompact`. Explicitly states Qompack does not
   depend on it and would keep the switch gated.

No proposal claims it was filed, discussed with, or acknowledged by anyone.

## Tests

`test/docs/limits_test.go`, stdlib only, reusing `repoRoot`, `readLines` and `headingText`:

- `TestCannotDoCoversSection12Limits` — the twelve §12 nouns, verbatim as the brief lists them.
- `TestUpstreamIssuesAreAllUnfiled` — every level-2 section must contain `Status: not filed`, and
  the page must contain no `https://github.com/…/issues/` URL. Documented as a test to be flipped
  deliberately, with the artifact, on the day something is filed.
- `TestCannotDoNamesTheUnimplementedChecks` — derives the not-yet-implemented ids rather than
  hard-coding them, and asserts its own premises first.

### The derivation, and why it walks this path

The brief says to parse `internal/cli/selftest.go` for the checks whose observed value is
`not-yet-implemented`. The literal is not in that file: `selftest.go` calls
`daemon.DeclareProducers(&daemon.Services{})` and then `contract.StandardAssertions()`
(`selfTestContractAssertions`), and `internal/contract/standard.go` holds the frozen string
`notYetImplementedObserved` which `gated` (`internal/contract/assertions.go`) reports for any
assertion whose producer was not declared. So the parser starts at `selftest.go`, asserts both call
shapes are still there (`t.Fatal` with a rewrite instruction if not), then:

1. `internal/contract/standard.go` → every `gated(C…, …)` constant name;
2. `internal/contract/ids.go` → constant name to on-disk id string;
3. `internal/daemon/options.go` → the `DeclareProducer` calls at brace depth 0 inside
   `DeclareProducers`' body, i.e. the ones a **zero** `Services` reaches; the four behind
   `if s.PreCompact != nil || s.Checkpoints != nil`, `if s.Rehydrate != nil` and
   `if s.MCPInitialized != nil` are excluded because a zero Services leaves those fields nil;
4. all minus unconditional → the not-yet-implemented set, which must be non-empty.

On this tree that derives exactly `hook.additional_context_delivered`,
`precompact.has_time_to_write`, `precompact.custom_instructions_accepted` and
`mcp.server_registered` — the four the brief names, arrived at from source rather than assumed.
No counts are hard-coded anywhere.

### RED

```
$ go test -count=1 ./test/docs/...
--- FAIL: TestCannotDoCoversSection12Limits (0.04s)
    limits_test.go:46: read …\docs\cannot-do.md: … The system cannot find the file specified.
--- FAIL: TestUpstreamIssuesAreAllUnfiled (0.05s)
    limits_test.go:69: read …\docs\upstream-issues.md: … The system cannot find the file specified.
--- FAIL: TestCannotDoNamesTheUnimplementedChecks (0.08s)
    limits_test.go:197: read …\docs\cannot-do.md: … The system cannot find the file specified.
--- FAIL: TestOwnedDocsExist (0.04s)
    owned_test.go:34: docs/cannot-do.md: … The system cannot find the file specified.
    owned_test.go:34: docs/upstream-issues.md: … The system cannot find the file specified.
FAIL	github.com/qompack/qompack/test/docs	1.571s
```

### GREEN, plus a mutation check on the derivation

A passing derivation test proves nothing on its own if the derived set could be empty or stale, so
the derived set was perturbed once and restored:

```
$ sed -i 's/mcp\.server_registered/mcp.server_REGISTERED_X/g' docs/cannot-do.md docs/upstream-issues.md
$ go test -count=1 -run TestCannotDoNamesTheUnimplementedChecks ./test/docs/...
--- FAIL: TestCannotDoNamesTheUnimplementedChecks (0.04s)
    limits_test.go:200: neither docs/cannot-do.md nor docs/upstream-issues.md names
      "mcp.server_registered", which self-test reports as not-yet-implemented on this tree
FAIL	github.com/qompack/qompack/test/docs	0.700s
$ (restored)  go test -count=1 -run TestCannotDoNamesTheUnimplementedChecks ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	0.652s
```

The restore was byte-identical (`git status` showed only the intended files afterwards).

## Validation before committing

Run once, from `C:\Users\Quant\Documents\Programming\Projects\qompack-sp18`, after the last content
edit. No lint, no whole-tree run, and the binary was not run for this task.

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	2.245s

$ go vet ./test/docs/...
(no output)

$ go run ./tools/devtool fmt-check
(no output)
```

## Claims marked unknown, and why

The pages mark the following as unknown rather than asserting either way:

- Whether injected `additionalContext` reached the model, and how much of it —
  `hook.additional_context_delivered` is producer-absent; the action that would verify it is
  proposal 1's host signal, or an installed-host canary.
- Whether the host accepts `custom_instructions`, and how much time PreCompact has —
  `precompact.*` producer-absent; verified by proposal 2's contract or an installed-host run.
- Whether the host registered the plugin's MCP server — `mcp.server_registered` producer-absent.
- Per-hook latency — `unavailable` with a reason, never a borrowed number.
- Request price and cache categories — `unknown`, no rate table applied.
- Name availability in a live host — needs T21-HOST-01 on an installed host.
- Installed-host compatibility generally — `implemented_unverified` (B01).

Each is stated with the observation that would settle it, not as a hedge.

## Self-review findings

- Every required entry and every required proposal from the brief is present; checked one at a time
  against the brief's lists (groups 1–6 and proposals 1–7).
- Every §12 noun appears verbatim and unbroken by a line wrap (the test would catch a wrap; it
  passes).
- No duplication of neighbouring pages: the fidelity/coverage tables, the self-test procedure and
  the "what not to conclude" list are linked, not restated. Every cross-reference is a link to a
  file that exists; the strict link test passes, including all anchors
  (`commands.md#qompackcheckpoint`, `config-reference.md#build-gates-no-config-key`, and so on).
- One claim was corrected during self-review: an early draft of the upstream page paraphrased
  `plans/QOMPACK-ERRATA.md` as saying host internals "can be confirmed or refuted", inverting the
  source. It now quotes it correctly ("None appears in published documentation. None can be
  confirmed or refuted from it").
- One wording was softened for the "no external service mentioned as used" rule: ADR 0010's
  `windows-latest` runner class is described as "a single hosted CI runner class". The figures are
  ADR 0010's own.
- Generated pages were linked, never edited. No file outside the four owned ones was touched.

## Concerns

1. **Two pages now under-describe this tree, and I was not permitted to edit them.**
   `docs/architecture.md` §10 says "The full page is planned (SP-18 Commit 5): `docs/cannot-do.md`",
   and `docs/troubleshooting.md`'s "Where to look next" lists "planned (SP-18 Commit 5):
   docs/cannot-do.md" as a file that does not exist. Both are now stale, and by the coordinator's
   own convention they should become links. This is a one-line change in each; it belongs to whoever
   owns those files (Task 1 and Task 4), or to a later commit in this subplan. Flagging rather than
   doing it, since the brief restricts me to four files.
2. **`TestUpstreamIssuesAreAllUnfiled` requires every level-2 heading to carry `Status: not filed`.**
   That is what the brief specifies, and it is the right property, but it means the page cannot grow
   a non-proposal level-2 section (a "Where to look next", say) without the test failing. The
   constraint is documented in the test's own comment so a future author does not weaken the check
   to add a section; the correct move there is a level-3 heading or a change to the section filter.
3. **The derivation test depends on three source files keeping their current shape.** It asserts its
   premises loudly rather than failing silently, but a refactor that, say, declares producers from a
   table rather than from straight-line calls will fail it with a "rewrite this parser" message. That
   is deliberate and stated in the test comment.
4. **`plans/QOMPACK-ERRATA.md` v1.3 records that §2.7's claim about the skill index is "filed in
   §12's upstream-issues list".** That is a plan-side disposition, not an artifact, and it is not one
   of the seven proposals the brief names; I did not add an eighth. If the coordinator wants that
   item carried onto this page, it needs its own brief line — and the word "filed" there means
   "assigned to the list", not "sent anywhere".

---

# Fix report — review round 1 (three Important findings)

Commit `c64f2f5` on `feat/sp18-documentation-and-uat`, on top of `2bbd441`. Three files
changed, all of them mine. The fourth finding (stale "planned" pointers in `docs/architecture.md`
and `docs/troubleshooting.md`) is routed to Commit 7 and was not touched — which also closes
concern 1 of the original report.

No issue was filed or sent.

## Finding 1 — the PostCompact existence claim (`docs/upstream-issues.md`, proposal 3)

Accepted without reservation. "There is no post-compaction event delivered to a plugin" is an
existence claim about the host that neither I nor the cited source can support, and §7.3 lists
PostCompact among the documented surfaces with "installed support remains unverified" — the
opposite shape. It also contradicted my own framing in `docs/cannot-do.md`.

The **Host limitation** is now Qompack-scoped and symmetric: no post-compaction event is used by
this plugin and none is contractual here; §7.3 records PostCompact as documented with installed
support unverified; "this page makes no claim either that such an event is delivered to a plugin or
that it is not"; Qompack infers the boundary from the next SessionStart with `source=compact`
because a capability may not depend on an event whose delivery it cannot verify.

The **Proposal** moved with it, from "deliver a post-compaction event" to "make a post-compaction
signal contractual and observable to a plugin — a documented one-shot event … with a stated
guarantee about when it is delivered", which is a request about the contract rather than about the
event's existence. The evidence and the "would keep working without it" sentence are unchanged.

## Finding 2 — producer-absence is self-test's synthetic run, not the build

Accepted. I checked the wiring the finding names before rewriting, and it is exactly as stated:
`internal/daemon/options.go:338-354` declares five producers unconditionally and the other four
behind `s.PreCompact`/`s.Checkpoints` (344), `s.Rehydrate` (348) and `s.MCPInitialized` (351); a
live daemon binds those at `internal/daemon/wire_checkpoint.go:137-138`,
`internal/daemon/rehydrate_service.go:321` and `internal/daemon/mcpop.go:90-92`.
`internal/cli/selftest.go:324` passes a zero `&daemon.Services{}`, which is why self-test reports
the producer-absent state — and the function's own comment says so. My pages generalised that to
"this build", which is wrong, and it was wrong in a self-serving direction: it made an unwired
synthetic run sound like a shipped incapacity.

Five sites rewritten, each now scoped to self-test's zero-`Services` run *plus* B01, which is what
`plans/V5-report.md` §24 and `plans/MIGRATION-EVIDENCE.md` "Capability register inputs" actually
support:

| Site | Now says |
|---|---|
| `docs/cannot-do.md`, "No exact native loaded bytes" | self-test runs against a zero `daemon.Services`, so the producer is undeclared *there*; a live daemon declares it once the rehydrate seam is bound; no installed-host run has ever been recorded (B01), so no observed delivery figure exists from that path either |
| `docs/cannot-do.md`, "No summarizer model substitution" | `precompact.custom_instructions_accepted` reports `not-yet-implemented` in self-test's zero-`Services` run, and no installed-host run has observed it (B01) |
| `docs/cannot-do.md`, "Installed-host compatibility" | the four contracts "have no recorded observation", naming the guards in `DeclareProducers` and stating that what a live run would observe against an installed host has never been recorded |
| `docs/upstream-issues.md` proposal 1 | same scoping, citing `selfTestContractAssertions`, `DeclareProducers` and B01 |
| `docs/upstream-issues.md` proposals 2 and 5 | "in `qompack self-test`'s zero-`Services` run", with proposal 5 naming `internal/daemon/mcpop.go` as where the producer is declared |

The claim that survives is the one the sources carry: no observation exists anywhere that anyone has
recorded, not that the build is incapable of producing one.

## Finding 3 — the test comment claimed a check the test did not perform

Accepted; the comment promised that a §12 respelling would fail the test, and nothing read
`Qompack.md`. I took the stronger of the two offered fixes.

`TestCannotDoCoversSection12Limits` now runs in two steps. A new helper, `planSection`, extracts a
named subsection of `Qompack.md` (lines after the heading, up to the next heading of any level, via
the harness's own `headingText`) and fails loudly if the heading is missing or the section empty.
Step one requires every entry of `section12Nouns` to appear in §12's own prose; step two requires
each to appear on the page. No count literal was added — the list is still the only enumeration, and
it is now checked against the binding source rather than trusted.

The comment on `section12Nouns` was rewritten to describe what the test actually does.

Mutation check that the new step is live (perturb, observe, restore):

```
$ sed -i 's/"marker control",/"marker controls",/' test/docs/limits_test.go
$ go test -count=1 -run TestCannotDoCoversSection12Limits ./test/docs/...
--- FAIL: TestCannotDoCoversSection12Limits (0.09s)
    limits_test.go:57: Qompack.md §12 "What this plugin cannot do" no longer states "marker
      controls": §12 is the binding source, so this list and docs/cannot-do.md both follow it, not
      the other way around
    limits_test.go:66: docs/cannot-do.md: does not state "marker controls" (Qompack.md v1.5 §12)
FAIL	github.com/qompack/qompack/test/docs	0.894s
$ (restored)  go test -count=1 -run TestCannotDoCoversSection12Limits ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	0.868s
```

## Covering tests, run once after the last edit

```
$ go test -count=1 ./test/docs/...
ok  	github.com/qompack/qompack/test/docs	2.336s

$ go vet ./test/docs/...
(no output)

$ go run ./tools/devtool fmt-check
(no output)
```

`gofmt -l test/docs/limits_test.go` is also clean. All four tests in the file pass, along with
`TestRelativeLinksResolve`, `TestOwnedDocsExist` and `TestADRIndexListsEveryADR`; no link or anchor
changed in this round.

## Files changed in the fix commit

- `docs/cannot-do.md` — three entries rescoped (finding 2).
- `docs/upstream-issues.md` — proposal 3's limitation and proposal restated (finding 1); proposals
  1, 2 and 5 rescoped (finding 2).
- `test/docs/limits_test.go` — `planSection` added, `TestCannotDoCoversSection12Limits` checks §12
  first, `section12Nouns`' comment corrected (finding 3).

Nothing else was touched; `docs/architecture.md` and `docs/troubleshooting.md` are untouched by
design.

## Remaining concerns after this round

Unchanged from the original report: concern 2 (the level-2-sections rule in
`TestUpstreamIssuesAreAllUnfiled` prevents adding a non-proposal h2 without a deliberate test
change), concern 3 (the derivation test asserts its premises against three source files and will
demand a rewrite if they are refactored), and concern 4 (ERRATA's §2.7 skill-index item is not one
of the seven proposals, and "filed" there means "assigned to the list"). Concern 1 is now the
coordinator's Commit 7 item.
