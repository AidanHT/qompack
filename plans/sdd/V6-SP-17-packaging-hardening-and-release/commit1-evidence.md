# SP-17 commit 1 — bundle assembly evidence

Evidence for `build(packaging): assemble a versioned plugin bundle` (acceptance row SP17-M7-01,
bundle half). The contract this evidence is about is `packaging/README.md`.

**What this evidence is not.** `claude plugin validate` was run against a bundle **directory where
it sits**. That is the host's own opinion of the manifest bytes, and it is complementary to
`devtool plugin-validate` (which only checks that this repository agrees with itself — Qompack.md
§7.5: "Repository JSON parsing alone does not validate the installed plugin"). It is **not** an
installed-plugin canary: installed manifest resolution, `${CLAUDE_PLUGIN_ROOT}` expansion and
launcher discovery are all unverified by it. The machine-readable record carries the same statement
in its `label` field. `claude plugin install/enable/disable` were not run and are forbidden for this
plan.

## Host

| | |
| --- | --- |
| OS / arch | windows / amd64 (Windows 11) |
| Go toolchain | go1.26.6 |
| Claude CLI | 2.1.263 (Claude Code) |
| Repository commit at assembly time | `acbb0f221a79340493dd01b2e24dedd1deb93352` |
| Worktree | dirty — this commit's own files were not yet committed when the bundle was assembled |

The `dirty` flag is recorded rather than hidden. A bundle assembled from a dirty tree cannot be
reproduced from its commit, so it is not a release artifact; the evidence below is about the
assembler and the host verdict, not about a shippable build. A clean-tree assembly is the release
workflow's job (Task 7).

## 1. Assembly and host validation

```sh
go run ./tools/devtool bundle --host-validate \
  --evidence plans/sdd/V6-SP-17-packaging-hardening-and-release/commit1-host-validation.json
```

Output (exit 0):

```
bundle: qompack-plugin-v0.2.0-602-gacbb0f2-dirty-linux-amd64 (11 file(s), version v0.2.0-602-gacbb0f2-dirty)
bundle: qompack-plugin-v0.2.0-602-gacbb0f2-dirty-linux-arm64 (11 file(s), version v0.2.0-602-gacbb0f2-dirty)
bundle: qompack-plugin-v0.2.0-602-gacbb0f2-dirty-darwin-amd64 (11 file(s), version v0.2.0-602-gacbb0f2-dirty)
bundle: qompack-plugin-v0.2.0-602-gacbb0f2-dirty-darwin-arm64 (11 file(s), version v0.2.0-602-gacbb0f2-dirty)
bundle: qompack-plugin-v0.2.0-602-gacbb0f2-dirty-windows-amd64 (11 file(s), version v0.2.0-602-gacbb0f2-dirty)
bundle: qompack-plugin-v0.2.0-602-gacbb0f2-dirty-windows-arm64 (11 file(s), version v0.2.0-602-gacbb0f2-dirty)
bundle: host validation record written to plans/sdd/V6-SP-17-packaging-hardening-and-release/commit1-host-validation.json
bundle: `claude plugin validate --strict --json` accepted qompack-plugin-v0.2.0-602-gacbb0f2-dirty-windows-amd64 (claude-plugin-validate, verdict from the CLI's --json `success` field, together with its exit status)
```

The command the record shows the CLI being given, with this checkout's absolute location replaced by
`<repo>` (the record is committed; the checkout lives under the user's home directory):

```
claude plugin validate <repo>\dist\bundle\qompack-plugin-v0.2.0-602-gacbb0f2-dirty-windows-amd64 --strict --json
```

Its verdict, verbatim from `commit1-host-validation.json`:

```json
{
  "success": true,
  "strict": true,
  "target": "<repo>\\dist\\bundle\\qompack-plugin-v0.2.0-602-gacbb0f2-dirty-windows-amd64\\.claude-plugin\\plugin.json",
  "manifest": { "file": "…", "type": "plugin", "errors": [], "warnings": [], "notes": [] },
  "contents": []
}
```

The record says which signal decided that. `outcome` is one of `accepted`, `rejected`, `timed-out`,
`could-not-run` or `unverified`, and only `rejected` means the host judged the bundle wrong — a CLI
that never ran or was killed at the deadline is neither a pass nor a rejection. `verdictSource`
names the signal: here the CLI's `--json` `success` field together with its zero exit, which are
required to agree (a non-zero exit is never overridden by the payload, and JSON carrying no
`success` key falls back to the exit status and says so). `stdout` and `stderr` are captured
separately, so a warning or update notice cannot make a good `--json` verdict unparseable. Every
branch of that decision is covered by `TestRunHostValidation`, because none of them is reachable on
a machine whose CLI works.

Two things worth carrying forward: the CLI accepted a non-semver version string
(`v0.2.0-602-gacbb0f2-dirty`) under `--strict`, and it reported `"contents": []` — it validated
`plugin.json` and did not enumerate the seven commands, one MCP server or seven hook events. So this
verdict covers the plugin manifest and nothing else; the command/hook/tool surface is covered by
`devtool plugin-validate` and by `test/canary`, and the installed surface by the platform matrix.

The other five targets are **built, not validated**: they were cross-compiled and assembled on this
host, and no host of theirs has seen them. `packaging/README.md` §6 carries that status per target.

## 2. Determinism

```sh
go run ./tools/devtool bundle --out dist/bundle-det1
go run ./tools/devtool bundle --out dist/bundle-det2
diff -r --exclude=bin dist/bundle-det1 dist/bundle-det2      # exit 0, no output
cmp dist/bundle-det1/<dir>/bin/qompack… dist/bundle-det2/<dir>/bin/qompack…
```

Both assemblies produced identical `BUNDLE.json` and `checksums.txt` for all six targets, and the
binaries themselves compared byte-identical for linux/amd64, windows/amd64 and darwin/arm64 (the
identical `checksums.txt` already covers the other three). Two separate output directories were used
rather than one directory twice, so a file left behind by the first run cannot make the second look
reproducible.

The `checksums.txt` format is verifiable by the standard tool, from inside a bundle directory:

```sh
sha256sum -c checksums.txt
```

```
.claude-plugin/plugin.json: OK
.mcp.json: OK
bin/qompack.exe: OK
commands/checkpoint.md: OK
commands/dropped.md: OK
commands/eval.md: OK
commands/pin.md: OK
commands/recall.md: OK
commands/status.md: OK
commands/why.md: OK
hooks/hooks.json: OK
```

Both temporary output directories were removed afterwards; `/dist/` is git-ignored and nothing from
it is committed.

## 3. Unit evidence

```sh
go test -count=1 ./tools/devtool/ -run 'TestAssembleBundle|TestVersionLdflags|TestGoBuildArgs|TestRunHostValidation|TestHostPathRedactor|TestValidateBundleVersion|TestTaskBundle_RejectsPositionalArgs|TestResolveBundleTargets|TestClaudeCLIVersion|TestBundleVersion_Precedence|TestBundleDirName' -v
```

All pass. Three of them are worth naming:

- `TestAssembleBundle_HostRealBuild` is the only one that compiles for real — one host-target build.
  Everything else substitutes a fixture binary, because six cross-compiles inside a unit test would
  measure the Go toolchain rather than the code under test. Under `-short` it skips with a
  `platform: ` reason, the shape `devtool lint`'s `stubskips` sub-check permits.
- `TestGoBuildArgs` asserts the argv and environment of that one `go build` invocation. `-trimpath`
  and `-buildvcs=false` are what the determinism rule rests on, and until they were lifted out of
  the call into `goBuildArgs`/`goBuildEnv` no test could read them: deleting either left the whole
  suite green while this document and every `BUNDLE.json` went on asserting reproducibility.
- `TestRunHostValidation` drives the verdict logic through a scripted CLI: absent, accepted,
  rejected, a non-zero exit carrying `success:true`, JSON with no `success` key, output that is not
  JSON, a warning on stderr, a CLI that never ran, and a deadline kill. Every one of those decides
  what this committed record claims, and none is reachable by running the happy path here.

## 4. Version finding — a release-owner decision, not taken here

`git tag` and `internal/core/version.go` disagree:

| source | value |
| --- | --- |
| newest release tag in the repository | `v0.2.0` |
| `internal/core.Version` compiled default | `0.1.0` |

Neither was changed by this commit and no tag was created. The bundle is unaffected in practice —
`git describe` succeeds here, so the compiled default is never the version that gets stamped — but
the default IS what an untagged or shallow checkout would stamp, and what `qompack version` reports
from a `go build` with no ldflags. Reconciling them (bump the default to `0.2.0`, or leave it as the
pre-release marker it has been) is a release-owner decision and is recorded for SP-17's later tasks
and for V6-VERIFY.
