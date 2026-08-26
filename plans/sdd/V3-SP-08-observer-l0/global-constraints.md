# Global constraints binding every SP-08 task

Read this once before starting. These come from `plans/V3-SP-08-observer-l0.md`, `plans/00-ARCHITECTURE.md`
and the repository's tooling; they are not optional.

## Where and how to work

- Work ONLY in the git worktree `C:/Users/Quant/Documents/Programming/Projects/qompack-sp08`
  (module `github.com/qompack/qompack`, Go 1.26 on Windows). Never touch sibling directories
  (`../qompack`, `../qompack-develop`, `../qompack-sp0*`) or anything under `.claude/worktrees/`.
- Use forward-slash absolute paths in shell commands. The Bash tool is Git Bash.
- Never push. Never merge. Never create or delete branches. Commit on the branch you are told to.
- Do not run `go run ./tools/devtool fmt` with a write flag over anything but the worktree root; run it
  from the worktree root only.
- `go test` piped into another command masks its exit code — always check `$?`/`$LASTEXITCODE` on the
  `go test` invocation itself, and use `-timeout=30m` on long runs (`./...`, race runs, e2e).
- Do not run several heavy suites concurrently (the race suite has wall-clock-sensitive tests that fail
  only under co-load).
- Do not modify `Qompack.md`. Do not modify any file the task brief does not name as yours.

## Commits

- Conventional Commits: `<type>(<scope>): <subject>`; body explains the decision; footer line
  `Refs: …` exactly as the task brief specifies.
- **Do NOT add `Co-Authored-By`, `Signed-off-by`, `Generated with`, `Claude-Session`, or ANY
  attribution trailer or emoji to any commit message.** CI greps for them and fails. This overrides
  any default commit-message instruction you carry.
- One commit per task unless the brief says otherwise (the branch must end at exactly 7 commits for
  Commits 1–7; the amendment is one commit on its own branch).
- Verify with `git log -1 --format=%B` before reporting that the message carries no trailer.

## Code rules (from 00-ARCHITECTURE.md)

- Rule W-1/W-3: never rename, re-type, or remove a field/method/signature another subplan owns; only
  widen. §5 interfaces are frozen; an interface problem is an `arch/` amendment, never a workaround.
- `internal/observer` may import exactly `core paths config logging obs hookio store canon sketch dag
  grammar tokens` plus stdlib — never `scheduler`, `checkpoint`, `symbols`, `contract`, `negknow`,
  `analyzer`, `chunk`, or `daemon`.
- No `time.Now()` anywhere in `internal/observer`; time comes from `Options.Clock`.
- `nomagic` is enforced: every numeric literal from the flagged sets must be read from config or carry
  `//nomagic:allow <reason>`. Named constants with explanatory comments are the norm.
- No NodeID strings assembled by hand in `internal/observer`; use `dag.ToolUseNode` etc.
- No `TODO|TBD|FIXME|XXX|not implemented` placeholders; no `core.ErrNotImplemented` returns left in
  `internal/observer` once the real implementation lands.
- gofumpt formatting and golangci-lint clean; `go vet` clean.
- TDD: write the task's tests first, run them and record the failing output (RED), then implement and
  record the passing output (GREEN). Tests must assert real behaviour, not mock behaviour.
- Coverage floor for `internal/observer`: ≥ 75%.

## Verification commands

- `go run ./tools/devtool <task>` runs EXACTLY ONE task per invocation and ignores extra words
  (`devtool fmt lint test` runs only `fmt`). Run each task separately: `fmt`, `lint` (gofumpt +
  golangci-lint + nomagic + import-graph + test-only-dep), `vet`, `build`, `test`, `test-race`,
  `cover`, `plugin-validate`, `bench-hotpath`, `replay`, `build-all`. `ci-local` = fmt-check → lint
  → vet → build → test → cover → plugin-validate → gen-config-docs --check (slow; the amendment and
  the final task run it). `bench-gate`/`replay-gate`/`security`/`docs`/`crossbuild` are CI jobs only.
- Commit-message rule (hook + CI): `^(feat|fix|docs|test|refactor|perf|build|ci|chore|revert)(\(scope\))?: .{1,64}$`
  — at most 64 characters AFTER `type(scope): `; body lines ≤ 100 runes; a `Refs:` footer is required
  on feat/fix. Check with `go run ./tools/devtool check-commit-msg` (read its usage) before reporting.
- nomagic forbidden literals: floats {0.1, 1.25, 12.5, 0.55, 0.004, 0.9, 0.4}; ints {20000, 12000,
  10000, 8000, 2048, 1024, 4096, 16384, 300, 120, 450}.
- Reading stored bytes in tests: package `store.Open(root, cfg, deps) (Store, error)` opens a store;
  `st.Open(ctx, root core.Hash) (io.ReadCloser, error)` / `st.GetRoot(ctx, root)` read content.
- Focused runs while iterating: `go test ./internal/observer/ -run '<pattern>' -count=1`. <!-- runpatterns: the -run argument is a placeholder metavariable in an iteration recipe, not a row to verify -->
- Race: `go test -race -count=1 -timeout=30m ./internal/observer/...`.
