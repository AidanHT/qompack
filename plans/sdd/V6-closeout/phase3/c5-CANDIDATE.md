# Phase 3 candidate 5 (C3.1)

- Commit: `0d06ab1233efb0fd5be8c99dc453e1aa2ee071af` on verify/v6 ("freeze close-out candidate 5"), merging
  closeout/integration (waves 1-15c, the candidate 4 live re-run evidence, merged-tree checks).
- `git describe --always --tags`: `v0.2.0-1750-g0d06ab12`; tree `3815188b7ba9024ee6b64626c69278254424656d`;
  `sha256(git archive --format=tar 0d06ab12)` = `bbfec6b93d9d3116818dac47b3c15ba5deef87f2c6338cf86e4d622730c5e8f8`.
- Product changes from candidate 4: waves 15, 15a, 15b, 15c (the candidate 4 re-run's defects, D49-D51).
- Run from `../qompack-cx-cand` (detached at it). Bundles `qompack-bundles/c5/` (`bundle -archive -version 0.3.0
  -host-validate`); windows-amd64 BUNDLE.json sha256
  `a1c59ec2d817d1837359033d6f047736470beb8bc1badfae836d9158b211a4ac`, bin/qompack.exe sha256
  `08a513369c0ef65901fcc6ad330e42b62b8c46d77e7444a76f672ad6e3308d25`; `claude plugin validate --strict --json`
  accepted it (`c5/host-validate.txt`).
- Merged-tree checks before the freeze (`integration/runs/w15/merged-w15-pkgs-windows.log`): vet (Windows and
  GOOS=linux), fmt, gen-mcp-docs/gen-config-docs, the lint subset pass; daemon, checkpoint, mcp, cli, negknow,
  rehydrate, store, contract, tools/devtool, test/docs pass; test/guards fails only the five carried perf rows
  (SP06-D2, SP08-D1, SP09-D1, SP10-D1, SP20-D2).
- Owed on this candidate: overnight.sh (D28 isolated timing on both OSes incl. TestBudget_RefreshStaleness, the
  Linux proofs and wave 15's touched packages under -race, quiet.sh C5.1/C5.2), then the live re-run of the rows
  candidate 4 failed plus UAT-10 and C4.9 (a)/(b) (D50).
