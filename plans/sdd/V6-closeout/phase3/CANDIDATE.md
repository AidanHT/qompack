# Phase 3 candidate (C3.1)

- Commit: `a94a3fb97cdd18633b6b6670446fab2cb98c304c` on verify/v6 ("freeze the close-out candidate"),
  merging closeout/integration `8ba97ed`; outside `plans/` its tree equals `8ba97ed`'s.
- `git describe --always --tags`: `v0.2.0-1417-ga94a3fb9`
- Tree: `dda6a1166c053b856d9a635edc106e25ee0f2295`
- Source snapshot: `sha256(git archive --format=tar a94a3fb)` =
  `c9971ea4c3c652e428a9ea18907657de311b27f99c4191870df54b233900c7cf`
- Run from a clean detached worktree `../qompack-cx-cand` (D34d).
- Daytime load caps (owner's CPU): the Linux container is limited to 8 CPUs (`docker update --cpus 8`)
  and the Windows lane runs with `GOFLAGS=-p=6`; the timing steps and quiet.sh run later, alone, with
  the caps removed.
