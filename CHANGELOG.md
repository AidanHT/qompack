# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `devtool bundle` assembles a deterministic, versioned plugin bundle per release target, with a
  `BUNDLE.json` identity and a `sha256sum`-format `checksums.txt`; see `packaging/README.md`.
- `devtool bundle --archive` packs each bundle reproducibly into a `.zip` for every target (sorted
  entries, the DOS-epoch mtime, normalised Unix modes with `bin/` at 0755, no extra fields) and
  writes one `checksums.txt` over the archives — the files a release uploads.
- `devtool marketplace` generates the release's `marketplace.json` from that `checksums.txt`: six
  per-target `archive` entries, each pinned by sha256 to its release zip.
- `devtool release-check` is the release gate: version agreement, `ci-local`'s own sequence,
  `build-all`, the generated-doc checks, `test/guards`, govulncheck, the licence inventory,
  real-binary determinism, the store rollback rehearsal and `plugin-validate`, stopping at the
  first failure and writing `dist/release-check.json`.
- `devtool release-scope` reports per release target and per acceptance row what a committed
  evidence record establishes — `verified | unverified | excluded` — and never raises a status
  from prose. Its Markdown rendering becomes the draft release's notes.
- `devtool licenses --write | --check` generates and verifies `THIRD_PARTY_NOTICES.md` from the
  modules `go list -deps ./cmd/qompack` actually reaches, intersected with the bindeps allow-list.
- `qompack fsck` checks store, index, capture, checkpoint, pin, negative-knowledge, delivery,
  spool, retention, migration and fidelity integrity, with five explicit repairs behind
  `--repair --yes` and no destructive default.
- `qompack doctor` reports version, host, capability, scope, recording and retrieval gaps and
  disabled controls, and calls no storage ratio or timing target proof of health.
- `docs/release.md`, `docs/install.md` and `docs/security.md`: the release procedure and gate list,
  the install/upgrade/uninstall policy including the retained-data choice, and the trust
  boundaries, bounds, recovery behaviour and known limitations.
- `test/platform`, `test/security`, `test/fault` and `test/release`: the deployment-environment,
  trust-and-privacy, fault-and-recovery and independent-switch matrices, each driving the shipped
  bundle's own binary and each writing a per-case evidence record.
- `.github/workflows/ci.yml` gains a `release-dry-run` job, so every push proves the release path
  builds rather than discovering it on the day of a release.
- Installation, upgrade, uninstall, rollback and unknown-schema rehearsal against the host CLI,
  with committed evidence records under `commit8-install-windows-amd64/`.

### Changed

- `.goreleaser.yaml` publishes and no longer builds: every build entry is skipped, the checksum
  generator is disabled, and the release is a **draft** carrying the archives `devtool bundle
  --archive` produced. One build path (`goBuildArgs`) now produces every shipped byte.
- `.github/workflows/release.yml` runs `release-check` before anything is built or published,
  uploads the host-validation record, and attests build provenance for the archives.
- Hook commands in the generated manifest quote `${CLAUDE_PLUGIN_ROOT}`, so a plugin root
  containing a space no longer word-splits under a shell launcher.
- A configuration violation no longer disables capture wholesale: the hot-path loader applies the
  same per-leaf fallback `config print` does and records the violation in
  `state/config-violations.json`.

### Fixed

- Retrieval authorization resolves a path on disk before answering, so a directory replaced by a
  link pointing outside the project root is refused rather than served from the archive.
- `runtime.hotPath.maxPayloadBytes` is bounded from above at the hook capture cap; a higher value
  is restored to the cap with a warning instead of silently refusing every delivery.
- A quarantined or damaged object answers as the `unavailable` domain outcome on both address
  forms, never as a protocol error and never as a miss.
- The assignment redaction family matches an underscore-prefixed key and a bare `auth` key, and
  the eval exporter's rule set no longer misses a long GitHub token or an operator pattern.
- Append-only index writes are newline-guarded, so a torn tail no longer swallows the next record.
- Checkpoint integrity failures — an orphan artifact, a MANIFEST entry without its artifact, a
  digest mismatch — each Loud once and refuse the affected checkpoint.
- Checkpoint pointer resolvability is decided on disk rather than from the in-memory chunk set.
- An unparseable retention line and a refused startup drain each Loud once instead of reporting
  only to the day log.
- A project whose `.qompack/` cannot be written falls back to the user-level log directory and
  says so, instead of degrading with no durable evidence at all.
