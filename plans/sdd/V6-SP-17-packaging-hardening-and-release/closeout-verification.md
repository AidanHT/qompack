# SP-17 close-out verification (partitioned re-run on `87966da`)

Fresh evidence for the close-out, not a second serial `release-check`. Artifact env vars were
unset so records were not rewritten. Shard patterns were confirmed non-empty with
`go test -list`.

`stubskips`, whole-tree cover, `build-all`, and byte-determinism were not repeated: they
passed on `4db5cea` in the serial final gate; `87966da` is docs and evidence only.

## Non-test (all PASS)

fmt-check; `go vet ./...`; `go build ./cmd/qompack`; lint golangci-lint; lint
nomagic/importgraph/testdeps/bindeps/sleepcheck/runpatterns/docmarkers/coveragefloors;
plugin-validate; licenses --check; gen-config-docs / gen-mcp-docs / gen-command-docs --check;
pinned govulncheck (`tools/pinned/go.mod`) — 0 reachable.

## Test partitions

| partition | result |
| --- | --- |
| e2e install / rollback / unknown-schema | PASS |
| e2e `TestV5_` | PASS |
| e2e `TestV4_` | PASS |
| e2e `TestV3_` / `TestV1_` / `TestPhase1` | PASS |
| e2e remainder | PASS |
| integration `TestV5_` | PASS |
| integration remainder (parallel, `QOMPACK_UNDER_COLOAD=1`) | FAIL — `TestIntegration_AppendOnlyHoldsUnderConcurrentDaemonWrites` drained 1388/1600 in 1m |
| that integration remainder **alone** | PASS |
| `internal/store/...` | PASS |
| `test/fault` + `test/security` + `test/platform` | PASS |
| `test/guards` + `test/release` + `test/canary` + `test/replay/...` | PASS |
| remaining `internal`/`cmd`/`tools` (parallel) | FAIL — `TestMatch_RejectsPathologicalPattern` 2.64ms ≮ 1ms |
| that rules test **alone** | PASS |

Both reds are timing-class under the parallel wave. Each passed on the prescribed alone
re-run. No product change.
