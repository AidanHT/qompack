// Package testutil is the shared test scaffolding every package in this repository builds its
// tests on: a real, disposable project on disk (Project), a deterministic clock (FakeClock), the
// golden-file comparison helpers (Golden, GoldenJSON), the frozen contract-fixture accessor
// (ContractFixture), and the Windows-hostile path fixture set (WindowsHostileFiles).
//
// testutil is a composition root (00-ARCHITECTURE.md §3.2): it may import anything, and nothing
// outside a _test.go file may import it. That is what lets it depend on internal/cli, internal/store
// and the rest of the tree at once without putting a single edge into the production import DAG,
// and it is why devtool lint's bindeps sub-check can prove os/exec — isolated in spawn.go for the
// §6.2 real-binary hook mode — never reaches cmd/qompack.
//
// Three properties are load-bearing and every helper here preserves them:
//
//   - Nothing writes outside t.TempDir() (§13 invariant 7). NewProject creates its project root
//     AND its user-global home under t.TempDir(), and points QOMPACK_PROJECT_ROOT, HOME and
//     USERPROFILE at them, so no test can reach the developer's real home or the checkout.
//   - No test reads a wall clock (§6.1). FakeClock is the only time source, and time.Sleep is
//     banned tree-wide by devtool lint's sleepcheck.
//   - Golden comparison normalizes CRLF to LF before diffing, so a Windows checkout that has
//     translated line endings on the way out of git never produces a false failure; the file
//     itself is always written with LF.
package testutil
