# Task 1 — Commit 1 `docs(sp18): establish supported architecture and doc contracts`

Plan text (verbatim): "Inventory existing docs/ADRs, create the necessary proposed docs-test harness, update README/architecture for actual supported behavior and validate links."

You work in the worktree `C:\Users\Quant\Documents\Programming\Projects\qompack-sp18` on branch `feat/sp18-documentation-and-uat` (HEAD 7ace16a, cut from develop 9c84e31). The repo is a Go module (`github.com/qompack/qompack`, go 1.26). Every command below runs from that directory.

## Files you own (create these; touch nothing else)

- `test/docs/*.go` — new Go test package `docs` (the "docs-test harness"). Test files only (`*_test.go`), plus one `doc.go` with the package comment so `go vet` is happy.
- `README.md` — new, repository root.
- `docs/architecture.md` — new.
- `docs/adr/README.md` — new ADR index.
- `tools/devtool/importrules.go` — ONE additive row only: `"test/docs": true,` in `compositionRoots`, with a one-line comment in the style of the surrounding rows. The architecture (plans/00-ARCHITECTURE.md §3.2, line ~485) says every package under `test/` is a composition root and importrules.go changes only by additive rows; an unregistered `test/` package fails `go run ./tools/devtool importgraph`.

Do NOT edit: `docs/config-reference.md`, `docs/commands.md`, `docs/mcp-tools.md` (generated; CI `docs` job regenerates and diffs them), any `docs/adr/00*.md`, `plugin/`, `.github/`, `plans/`, `Qompack.md`, any `internal/` or `cmd/` file. Never run `git stash` (shared across worktrees). Never push.

## Part A — the harness (write these tests FIRST; they must fail before the docs exist, then pass)

Package `test/docs`, stdlib only (no `internal/` imports). Find the repo root the way `test/guards/stubs_test.go:488 repoRoot(t)` does (copy the approach, do not import test/guards — nothing may import it).

1. `TestRelativeLinksResolve` — for `README.md` and every `docs/**/*.md` (generated pages and ADRs included): every markdown link `[text](target)` and reference-style `[x]: target` whose target has no URL scheme must resolve to an existing file relative to the linking file; if the target carries `#anchor`, the target file must contain a heading whose GitHub-style slug equals the anchor (lowercase, spaces to `-`, punctuation other than `-` and `_` removed, backticks removed; duplicate headings get `-1`, `-2` suffixes). A bare `#anchor` refers to the same file. Ignore links inside fenced code blocks and inline code spans. Report every failure as `file:line: target` and keep going (t.Errorf, not Fatalf).
2. `TestOwnedDocsExist` — a package-level `var ownedDocs = []string{"README.md", "docs/architecture.md", "docs/adr/README.md"}` (later commits append to it). Each path exists, is non-empty, and its first non-blank line is a level-1 heading (`# `).
3. `TestADRIndexListsEveryADR` — `docs/adr/README.md` links every `docs/adr/NNNN-*.md` file found on disk (derive the set from the directory; never hard-code a count), and every ADR it links exists. Also assert the index row for each ADR names the ADR's title (the file's first `# ` heading text).
4. Helpers for later commits, in `inventory_test.go`: `generatedCommandNames(t) []string` returns the names from `docs/commands.md` level-2 headings of the form "## `/qompack:NAME`"; `generatedToolNames(t) []string` returns the names from `docs/mcp-tools.md` level-2 headings of the form "## `NAME`". Both must fail the test if they find zero names. (These pages are generated from `internal/commands.Specs()` and `internal/mcp.ToolDefs` and CI fails when they drift, so they are source-derived.) Add one test `TestGeneratedInventoriesAreNonEmpty` that calls both.

Run `go test ./test/docs/...` before writing any doc (RED: tests 2 and 3 fail because the files are absent), and after (GREEN). Keep the RED and GREEN output in your report.

## Part B — `README.md`

Audience: a Claude Code user deciding whether to try Qompack. Must contain, in this order or similar:

1. One paragraph: what Qompack does — a Go sidecar plugin for Claude Code that records tool output and conversation into a local store through hooks, writes a checkpoint at `PreCompact`, rehydrates a bounded context after compaction, and exposes retrieval/elimination/observability through MCP tools and slash commands. Derive the exact hook list from `plugin/hooks/hooks.json`, the MCP server from `plugin/.mcp.json`, the command list from `docs/commands.md`, the tool list from `docs/mcp-tools.md`. State counts only by pointing at those pages ("the commands listed in docs/commands.md"), never as a hard-coded number in a sentence that a reader would treat as a guarantee.
2. Supported environments: what `.github/workflows/ci.yml` actually builds and tests (read the `test` matrix, `crossbuild`, `timing`, `lint-windows` jobs and report the OS/Go versions they use). Say that installed-host compatibility is `implemented_unverified` (plans/V5-report.md §24, item B01) — the installable bundle, install guide and security guide are SP-17 deliverables that do not exist yet. Write those as plain text "planned (SP-17): docs/install.md" — NOT as markdown links (the link test must stay strict and those files do not exist).
3. Building from source today: `go build ./cmd/qompack` (verify the command works and note the binary name), the `plugin/` layout (`.claude-plugin/plugin.json`, `.mcp.json`, `hooks/hooks.json`, `commands/*.md`) and that hooks expect the binary at `${CLAUDE_PLUGIN_ROOT}/bin/qompack`. Mention `go run ./tools/devtool plugin-validate` as the bundle check.
4. Maturity: pre-release. Last tag is `v0.2.0` (`git tag`), `plugin/.claude-plugin/plugin.json` says `0.1.0`; report both plainly, do not change either (SP-17 owns release/versioning).
5. Opt-in and disabled features: read the `runtime` section of `docs/config-reference.md` and name the switches that ship OFF (the `runtime.migration.*` and `runtime.phase7.*` blocks, `settingsVersion` semantics: a file written for a newer version has its whole block reset to defaults). Cross-check plans/MIGRATION-EVIDENCE.md lines 453–456 ("Disabled capabilities and their switches") and plans/V5-report.md §24 (SP-21 enabled-surface matrix: admission gate off, replacement off, host allowlist empty). Say explicitly that enabling them is unsupported until the corresponding gates pass.
6. Configuration: point to `docs/config-reference.md` (generated; five-layer precedence; invalid value falls back to default and is reported; unknown keys warn).
7. Links: `docs/architecture.md`, `docs/adr/README.md`, `docs/config-reference.md`, `docs/commands.md`, `docs/mcp-tools.md`, `LICENSE`. Do not link user-guide/troubleshooting/cannot-do/upstream-issues/uat (later commits create them; Commit 7 adds their links).

## Part C — `docs/architecture.md`

Audience: a developer or reviewer who needs the actual contracts, not the design aspiration. Every claim must come from shipped code, an ADR, `plans/V5-report.md` or `Qompack.md` v1.5 — cite the file (and section) for each contract. Required sections:

1. **Process model** — hook subcommands (`session-start`, `observe tool|stop|prompt`, `checkpoint`, `flush`) from `plugin/hooks/hooks.json` and `internal/pluginmanifest/manifest.go`; the `mcp` server; the daemon and IPC (read the package comments of `internal/daemon`, `internal/ipc`, `internal/cli` — cite what you read). Distinguish what runs in the hook process from what runs in the daemon.
2. **Write-set and retention** — what Qompack writes and where: read `internal/paths` (package comment / exported path functions) and `internal/state`, and plans/V5-report.md §27 ("Privacy, retention and write-set", lines 882–891). List the directories/files by name as the code names them; say what is never written (secrets redaction: `internal/redact`).
3. **Identity** — sessions, segments, handles, fidelity/coverage/outcome/authority enumerations: quote the closed enumerations from plans/00-ARCHITECTURE.md line 62 (`core.Fidelity`, `core.Coverage`, `core.EvidenceOutcome`, `core.Authority`) after confirming them against `internal/core`.
4. **Publication and durability** — delivery group commit and the A/B seal (`docs/adr/0014-delivery-group-commit-and-ab-seal.md`), checkpoint durability (`internal/checkpoint`, ADR 0011).
5. **State authority and uncertainty** — current authority vs historical reads; the recorded partial: uncertainty "does not survive the digest surface under a blind ledger" (V5-report §24, SP-20 paragraph). Say that plainly.
6. **Retrieval** — the MCP tools (link `docs/mcp-tools.md`), `unavailable` is not `absent` and is never substituted with current content (verify against `docs/mcp-tools.md` and `docs/adr/0013-migration-contracts.md`), `dropped` reports qualified coverage, an `ephemeral` tag does not mean native eviction.
7. **Checkpoint and rehydration** — budget semantics: 8–12K tokens is a historical Qompack-added target for the injected material, not the total restored native context (`docs/adr/0011-rehydration-budget-and-item-order.md`; `checkpoint.budgetTokens` default from `docs/config-reference.md`). No claim that native input shrinks.
8. **Scheduler, observer, negative knowledge** — ADR 0012, ADR 0008, ADR 0009 (Bloom is a cache; no "safe false positive" claim).
9. **Historical vs current decisions** — one table: ADR number, title, status/date as stated in the ADR, and whether it is a historical record or a current amendment. Keep this consistent with `docs/adr/README.md`.
10. **What is not supported** — one short list, each item pointing to where the limit is recorded: no native cuts/markers/history eviction, no model-compliance or native-byte proof, no reconstruction of a missing original, no universal improvement claim, no performance or name-availability guarantee. (Commit 5 writes the full `docs/cannot-do.md`; here only the pointers, as plain text "planned (SP-18 Commit 5): docs/cannot-do.md".)

## Part D — `docs/adr/README.md`

Index every `docs/adr/*.md` (currently 0002, 0003, 0007, 0008, 0009, 0010, 0011, 0012, 0013, 0014, 0030, 0100 — derive from the directory, do not trust this list): number, linked title, status/date as the ADR states it (read each ADR's header), and a one-line "historical record" vs "current amendment" label with the reason (e.g. superseded-by, amended-by, stands). Note that numbering gaps exist (numbers are reserved by subplan); do not invent reasons for them.

## Claims policy (binding; the reviewer checks against it)

- No performance, latency, cost-saving, or name-availability guarantee anywhere.
- Do not reintroduce, even by quoting old design text: native O(delta), free cuts, first-turn savings, exact-native-context, or Bloom "safe false positive" claims.
- 8–12K is a historical Qompack-added target, not total restored native context.
- `dropped` reports qualified coverage; an `ephemeral` tag does not mean native eviction.
- No Codex planning models or internal migration mechanics in user-facing text unless useful for a user decision.
- Subscription usage and estimated API price are separate notions; do not conflate them.
- Every capability statement is either supported by a cited source or explicitly marked unknown with the action that would verify it.
- Files that do not exist yet are named as plain text with "(planned, SP-NN)"; never linked.
- No fixed inventory counts in tests; prose counts only by reference to the generated pages.

## Sources to read (in this order)

1. `Qompack.md` §0 and §§7–12 and Appendix C (it is long — grep the section headings first, then read the sections you cite).
2. `docs/adr/*.md` (all twelve; they are short).
3. `plans/00-ARCHITECTURE.md` §3.2 and the package tree near line 485; treat it as possibly stale versus code — code wins, and say so where they differ.
4. `plans/V5-report.md` §24, §27, §29 (lines 823–891, 971–1013).
5. `plans/MIGRATION-EVIDENCE.md` lines 84–96 and 453–456.
6. `plugin/**`, `internal/pluginmanifest/manifest.go`, `docs/commands.md`, `docs/mcp-tools.md`, `docs/config-reference.md`.
7. `.github/workflows/ci.yml` job matrix.
8. Package comments of `internal/daemon`, `internal/ipc`, `internal/cli`, `internal/paths`, `internal/state`, `internal/core`, `internal/checkpoint`, `internal/rehydrate`, `internal/mcp`, `internal/redact`.

## Validation before committing

```
go test -count=1 ./test/docs/...
go vet ./test/docs/...
go run ./tools/devtool fmt-check
go run ./tools/devtool lint
```
`lint` runs golangci-lint plus the importgraph/testdeps/bindeps sub-checks and may take a few minutes; run it once before committing. Paste the command lines and their tails into your report. Do not run whole-tree `go test ./...`, race, coverage or replay — this is a prose-plus-harness commit.

## Commit

Exactly one commit. Subject line exactly: `docs(sp18): establish supported architecture and doc contracts` (a commit-msg hook enforces conventional commits with a 64-char subject limit and rejects attribution trailers). Body: what the harness checks, what README/architecture/ADR index cover, and the two recorded gaps (installed-host compatibility unverified; SP-17 pages planned). No `Co-Authored-By`, no `Claude-Session`, no other trailer.
