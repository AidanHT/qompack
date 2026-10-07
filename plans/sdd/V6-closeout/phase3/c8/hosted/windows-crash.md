# ci 37562946379 attempt 1, test (windows-latest), on 3ec62ad2
- internal/daemon's -count=2 binary ended at 1185 s (its -timeout is 60m) with 316 tests started and unfinished.
  Only the tail of each test's output is printed by the reconciliation step, and test.json is not uploaded, so the
  crash header (the fatal error line) is lost. The surviving dump is the runtime's level-2 format
  (goroutine headers with gp=/m=, frames with fp=/sp=/pc=). By gotraceback(), level 2 without GOTRACEBACK set
  means a runtime-class throw (memory, an exception or signal, a stack overflow), not a user-class fatal such as
  concurrent map writes or a deadlock, and not a test timeout.
- Attempt 2 (`gh run rerun --failed`, the same commit) is all green: test (windows-latest), test (macos-latest)
  and the rest. ci.yml run 37562946379 concluded success at attempt 2. Nightly 37562945914 is green.
- Local evidence on the same tree: the pre-freeze internal step and the overnight win-race whole tree are green
  on Windows; linux-tree (non-root -race) is green.
- Disposition: one-off, cause undetermined, not reproduced. Not shown to be a blocker or major (D66(c)).
  Post-release CI observability fix: print the head of a crashed binary's output (its fatal error line) as well as
  the tail, and upload test.json on failure.
