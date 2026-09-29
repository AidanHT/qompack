# Phase 3 candidate 3 (C3.1)

- Commit: `d5598eb4445954120ee795560c2ea46640772f43` on verify/v6 ("freeze close-out candidate 3"),
  merging closeout/integration `f490b16c` (waves 1-12).
- `git describe --always --tags`: `v0.2.0-1478-gd5598eb4`
- Tree: `3032e6ce44dbc5b13a5522f01dd651f86a81367c`
- Source snapshot: `sha256(git archive --format=tar d5598eb4)` =
  `f19c27a71b12fdcac6131aad04fdf5ff6dd0ef748d070def600c6ca3dd58d313`
- Product diff from candidate 1 (`a94a3fb`): D41 (platform-derived B-A default), D43 (config WARN), doc
  comments; everything else is tests, harness and docs (waves 9-12).
- Run from the clean detached worktree `../qompack-cx-cand` (moved from `a94a3fb`, so the go test cache
  serves unchanged packages).
- Bundles (live lane): `qompack-bundles/c3/` via `devtool bundle --archive --version 0.3.0`;
  windows-amd64 BUNDLE.json sha256 `32600778ae6463cd47736fc6b0a8614ad782e5bbd0ef0440f4e2c3937ccf4505`.
