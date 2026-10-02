# Phase 3 candidate 4 (C3.1)

- Commit: `9f6a2fadf086eba8080af589a35dd9554ae6cab4` on verify/v6 ("freeze close-out candidate 4"), merging
  closeout/integration (waves 1-14b, the Phase 4 candidate-3 evidence, merged-tree checks).
- `git describe --always --tags`: `v0.2.0-1631-g9f6a2fad`; tree `fabe163b0ec5023bf73bc5d6a7bae97ca3b251bd`;
  `sha256(git archive --format=tar 9f6a2fad)` = `ea926eeddb35149b2a8da56737564b0c4f2dec6d42b0648f5e8c16fb16f582f4`.
- Product changes from candidate 3: waves 13, 14, 14b (the D45 live-lane defects and their follow-ups, D46, D48).
- Run from `../qompack-cx-cand` (detached at it). Bundles `qompack-bundles/c4/`; windows-amd64 BUNDLE.json sha256
  `aa7da0e17b7597562a6eba47fc48f1db81ff5e9bdc494b9997a323137625558d`.
- Merged-tree checks before the freeze: vet, fmt, gens, lint subset pass; observer, daemon, checkpoint, mcp, cli,
  negknow, rehydrate, store, test/docs pass; test/guards fails only the five carried perf rows.
