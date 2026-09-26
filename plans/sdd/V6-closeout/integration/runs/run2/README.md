# Integrated gates, run 2: `closeout/integration` after the wave-2b merges

- `int2-merge-daemon-cli.*` (Windows, `54a4334`): the post-merge check after resolving the one
  `daemon.go` conflict (two additive blocks). `internal/daemon`, `internal/cli` and `internal/ipc`
  all pass.
- `linux/cx-int-int2-linux-race-8d50d6b-*` (Linux, non-root, `-race`, every non-e2e package, co-load
  declared: wave 3's agents were running): every package reported. Three were red:
  - `test/guards` carried-defect guard (six rows): expected until the Phase 2 dispositions.
  - `test/integration` `TestIntegration_HotPathWarmWithRealResidentState`: the known co-load
    timing row, judged in the quiet C5.1 run.
  - `internal/daemon` `TestDrain_AReplayedFlushIsEndedOnItsOwnNotUnderThePassBudget` (new in
    `closeout/w2-sessionend`). It passed 30 of 30 alone (`int2-replayedflush-count30`). Its fixed
    200 ms pass budget could expire under co-load before the pass reached the spooled flush. It
    was fixed in `20af0e6`, which spends the budget by a cancel once SessionEnd runs, and a
    negative control still fails. The whole `internal/daemon` package then passed under
    `-race` with co-load (`int2-daemon-race-after-fix`, 1,473 pass).
- `linux/int2-linux-race.attempt1-badpath.log`: the first launch refused a POSIX-style `--repo` path
  before running anything. Kept as found.
