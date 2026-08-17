### `test/bench/hotpath/main.go` — the B-A / B-B / B-D harness

A standalone Go program (not `go test -bench`) because it must measure **real process spawns** (§7).

```
devtool bench-hotpath --iterations 5000 --hook observe-tool --warm-daemon --json out.json [--project <dir>]
```

1. Create a temp project (or use `--project`), `go build` the real binary into it, set `QOMPACK_PROJECT_ROOT` and `QOMPACK_IPC_ADDR` so the socket stays inside the temp dir.
2. Start a real daemon as a child process and wait for `admin.ping`.
3. **Warm it**: send 2 000 `observe.tool` requests over a persistent connection carrying 40 MB of synthesized tool output in total (a deterministic generator seeded at 1, mixing `Read`, `Bash`, `Grep`, `Edit` payloads of 4 KB–256 KB), so the WAL, registry, ring and — once SP-06/SP-03/SP-07 have merged — the CMS, DAG and store are warm. In wave 1 those are stubs, so B-C is omitted from `budgets` and the reason is stated in the `notes` array; the wave-2 verification re-runs this harness against the real implementations.
4. Measure the **process-creation floor** first: spawn `qompack version` 200 times and record its wall-time distribution. `qompack version` prints `qompack <semver>` and exits 0 without reading stdin, resolving a project root, or touching `.qompack/`; it is therefore the same binary, the same loader and the same OS process cost with none of the hook work. *Decision:* SP-05 adds `version` to `cmd/qompack/main.go`'s dispatch switch if SP-01 did not already ship it — a two-line case, and the only honest way to make the floor auditable.
5. Spawn the real `qompack observe tool` binary `N` times, one at a time, with a representative payload on stdin, timing each spawn with `time.Now()` around `cmd.Run()`. That interval is **B-D** (`hook_wall`, includes host process creation).
6. Derive **B-A** per sample, not per percentile: `B-A_i = max(0, B-D_i − floor_p50)`, then compute the percentiles over the adjusted samples. Subtracting p99 from p99 would be statistically meaningless (`p99(X−Y) ≠ p99(X) − p99(Y)`), and B-A is the number CI gates on, so the derivation is stated in the artifact as `"b_a_method": "per-sample subtraction of spawn_floor_ms.p50"` and `spawn_floor_ms` is reported in full. This is the same honesty rule §2.4 applies to B-D.
7. Measure **B-E**: spawn `qompack checkpoint` 50 times with a `PreCompact` payload and record the wall time. The CI gate names B-E ("fails the build if B-A p99 ≥ 15 ms **or B-E p99 ≥ 2 s**"), so the harness must produce the number even though SP-10 owns what happens inside the route; in wave 1 `svc.PreCompact` is nil and the measurement is the route's floor, which is the correct baseline to regress against later.
8. Read the daemon-side histograms via the `status` op for **B-B** and the observed/estimated `hook.controlled` pair.
9. Emit `out.json`:

```json
{"platform":"windows/amd64","n":2000,"b_a_method":"per-sample subtraction of spawn_floor_ms.p50",
 "spawn_floor_ms":{"n":200,"p50":6.1,"p99":11.4},
 "notes":["B-C not measured in wave 1: the processing seams are stubs"],
 "budgets":[{"budget_id":"B-A","n":2000,"p50":2.1,"p95":4.0,"p99":6.8,"p999":9.9,"max":14.2,"limit_ms":15,"pass":true},
            {"budget_id":"B-B","n":2000,"p50":0.10,"p95":0.31,"p99":0.62,"p999":1.1,"max":1.9,"limit_ms":2,"pass":true},
            {"budget_id":"B-D","n":2000,"p50":8.2,"p95":13.9,"p99":18.1,"p999":24.0,"max":31.5,"limit_ms":null,"pass":null},
            {"budget_id":"B-E","n":50,"p50":14.0,"p95":22.5,"p99":31.0,"p999":31.0,"max":31.0,"limit_ms":2000,"pass":true}]}
```

10. Exit non-zero if any `Gated` budget with a non-null `limit_ms` fails — B-A, B-B and B-E. B-D has `limit_ms: null` and can never fail the run. `time.Sleep` is permitted in this directory only (`devtool lint` exempts `test/bench`).

`tools/devtool/task_benchhotpath.go` adds the `bench-hotpath` task (SP-01 declared the name; SP-05 implements it), forwarding flags and printing a human summary.

---

### `.github/workflows/ci.yml` — the bench gate

Replace the placeholder `bench-gate` job body with:

```yaml
  bench-gate:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.26.x' }
      - run: go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json bench-${{ matrix.os }}.json
      - uses: actions/upload-artifact@v4
        with: { name: bench-${{ matrix.os }}, path: bench-${{ matrix.os }}.json }
```

The task exits non-zero when B-A p99 ≥ 15 ms or B-E p99 ≥ 2 s, which fails the job. B-D is uploaded and never gated. `nightly.yml` gets the same job at `--iterations 5000`. Mark `bench-gate` a required check on `develop` and `main` (00-ARCH §8: required "from the end of wave 1 onward").

---

### Benchmarks tied to a named budget

| Benchmark | Budget | Gate |
|---|---|---|
| `test/bench/hotpath` B-A | **B-A p99 < 15 ms** (§8.1, §11.3) | CI hard fail on ubuntu, macos, windows |
| `test/bench/hotpath` B-B | B-B p99 < 2 ms (§2.4) | CI hard fail |
| `test/bench/hotpath` B-E | **B-E p99 < 2 s** (§11.3 L4, §2.4) | CI hard fail |
| `test/bench/hotpath` B-D | reported only (§2.4) | artifact + PR comment |
| `BenchmarkReadState` | hot-path prelude < 100 µs/op | `benchstat` warn at >10%, fail at >25% |
| `BenchmarkIngestAccept` | B-B | as above |
| `BenchmarkServerRoundTrip` | daemon-side B-A portion < 2 ms p99 | as above |
| `BenchmarkEncodeRequest` | < 5 µs/op for a 4 KB payload | as above |

### Commit 7

```
test(sp05): hot-path bench harness, three-platform bench gate, and fixtures

B-A is measured with real process spawns against a warm daemon because that is the
only honest way to measure it; B-D is reported alongside it and never gated, since
process creation is the host's cost and hiding it inside B-A would be the exact
unmeasured-system sin the design document indicts.

Refs: SP-05, §8.1, §11.3, 00-ARCHITECTURE §7, §8
```

- [ ] Write `test/bench/hotpath/report_test.go` first — percentile computation against a hand-checked 512-sample fixture, the per-sample floor subtraction, and a golden of the `out.json` shape including `b_a_method`, `spawn_floor_ms`, `notes` and the B-A/B-B/B-D/B-E rows; confirm it fails (symbols absent).
- [ ] Add `test/bench/hotpath/main.go`, `report.go`, `payload.go`; add `tools/devtool/task_benchhotpath.go`.
- [ ] Run `go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json bench-local.json` on the Windows dev machine and confirm `B-A.pass == true` and `B-E.pass == true`.
- [ ] Update `.github/workflows/ci.yml` (`bench-gate` on ubuntu/macos/windows at `n=2000`) and `.github/workflows/nightly.yml` (`n=5000`); mark `bench-gate` a required check on `develop` and `main`.
- [ ] Add `BenchmarkReadState`, `BenchmarkEncodeRequest`, `BenchmarkIngestAccept`, `BenchmarkServerRoundTrip` and record their numbers into `testdata/bench-baseline.txt` via `benchstat`.
- [ ] Push and confirm CI is green on all three platforms before opening the merge into `develop`.

---

