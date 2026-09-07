A test that can fail for a reason other than the bug it names is worse than no test, because it
trains the team to ignore red.

1. Test files sit beside the code as `<name>.test.ts`. There is no parallel `test/` tree for unit
   tests; `test/` holds load tests, fixtures, and end-to-end suites only.
2. One behaviour per test, named as a sentence: `rejects a reused refresh token`. A name that
   says `works` is not a name.
3. No test reads the wall clock. Time comes from the fixture clock, and a test that calls
   `Date.now()` fails the lint pass.
4. No test sleeps. Waiting is expressed as waiting for a condition with a deadline, so a slow
   machine makes the test slower rather than red.
5. No test depends on another test's state. The suite is run in a random order in CI precisely to
   make that dependency fail immediately.
6. Fixtures are built by explicit factory functions with every relevant field named at the call
   site. A fixture whose important value is a default is a test that does not say what it tests.
7. Assertions name the expected value first and carry a message explaining what the failure would
   mean. An equality assertion with no message costs the next reader ten minutes.
8. Mocks stand in for boundaries the test does not own: the identity provider, the clock, the
   network. Mocking your own module under test is a design smell, not a technique.
9. Every mock asserts its call count. An unasserted mock turns a deleted call site into a passing
   test.
10. Load tests live in `test/load/` and are checked in with their thresholds. A load test with no
    threshold is a script.
11. `refresh-storm.ts` is the pool reproduction and runs at 200 concurrent refreshes. Its pass
    condition is zero acquisition timeouts, not a percentile.
12. End-to-end suites run against the compose environment, unmodified. A suite that needs a
    special compose override is testing a configuration nobody ships.
13. Coverage is a floor, not a goal. The floors are declared per package and lowering one requires
    naming what became untestable and why.
14. A flaky test is quarantined the day it is noticed, with an owner and a date. A quarantine with
    neither becomes permanent within a month.
15. Snapshot tests are permitted only for rendered output a human reads, and every snapshot is
    reviewed line by line when it changes. A snapshot accepted without reading is a placeholder.
