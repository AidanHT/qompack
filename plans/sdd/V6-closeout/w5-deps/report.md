# V6 close-out w5-deps: klauspost/compress and x/mod upgrades

Branch `closeout/w5-deps`. Workflow `wf_8f93ec11-36e`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `58cbb7b`

### Root cause

Both govulncheck findings were module-level only; the vulnerable packages are not in the shipped binary. (1) GO-2026-5841 is in klauspost/compress/s2. The shipped binary links klauspost/compress/zstd (with fse, huff0, internal/*, snapref, xxhash) on all six targets and never links s2, so the module ships but the vulnerable package does not. (2) GO-2026-6180 and GO-2026-6179 are in golang.org/x/mod/sumdb and sumdb/tlog. x/mod is indirect. Only tools/lint/nomagic pulls it, through golang.org/x/tools: the non-test chain is nomagic -> go/analysis/singlechecker -> go/analysis/internal/checker -> go/packages -> internal/gocommand -> x/mod/semver, and the test chain is analysistest -> internal/testenv -> x/mod/modfile. No x/mod, x/tools or x/sync package reaches ./cmd/qompack on any of the six release targets (go list -deps per target, runs/01). What the x/mod upgrade forced: x/mod v0.40.0's go.mod requires golang.org/x/tools v0.49.0 (marked tagx:ignore), and x/tools v0.49.0 requires golang.org/x/sys v0.47.0 and golang.org/x/sync v0.22.0. Minimal version selection therefore raises all three. The x/sys raise is a shipped change (x/sys/windows through internal/paths and go-winio, x/sys/unix through internal/paths), forced rather than chosen.

### Summary

## w5-deps: klauspost/compress v1.18.7 and golang.org/x/mod v0.40.0

Both upgrades are in, and no code change was needed. The pinned govulncheck now reports "No vulnerabilities found" on windows, linux and darwin. Every gate in the brief is green at 2167632. 58cbb7b adds only evidence files, and I re-ran the cheap gates there.

### Resume review
- **5023c12** (klauspost v1.18.7, earlier seat): reviewed and kept as is. The diff is one go.mod line, the klauspost go.sum pair, the NOTICES version, and ten stale go.sum lines that `go mod tidy` drops on the base too (logrus, objx, goldmark, x/net, x/telemetry; `go mod tidy -diff` on the base lists exactly those ten).
- **The uncommitted draft** (x/mod upgrade): I reset go.mod, go.sum and THIRD_PARTY_NOTICES.md to 5023c12 and ran `go get golang.org/x/mod@v0.40.0 && go mod tidy && devtool licenses --write`. That reproduced the draft byte for byte, so I committed it as 2167632 with the rationale in the message.
- **Pre-restart evidence:** none of it is relied on. Everything below was re-run after the restart. My base rebuild at 90e1db3 has the same sha256 on all six binaries as the pre-restart seat's base build.

### Findings (full dependency paths in root_cause)
- **GO-2026-5841** is in `klauspost/compress/s2`. The binary links `klauspost/compress/zstd` on all six targets and never `s2`: the module ships, the vulnerable package does not.
- **GO-2026-6180 / GO-2026-6179** are in `x/mod/sumdb` and `x/mod/sumdb/tlog`. x/mod is indirect and only `tools/lint/nomagic` pulls it, through x/tools. `x/mod/semver` comes in via `go/packages` -> `internal/gocommand`. No x/mod, x/tools or x/sync package reaches `./cmd/qompack` on any target.
- **The x/mod upgrade forces three more upgrades.** x/mod v0.40.0 requires x/tools v0.49.0, which requires x/sys v0.47.0 and x/sync v0.22.0. `go get` reports exactly these four upgrades.
  - All four need go 1.25.0, and klauspost v1.18.7 needs go 1.24. go.mod's go 1.26 / toolchain go1.26.6 is unchanged.
  - LICENSE and PATENTS are byte-identical across every upgraded module. NOTICES changed only in version strings.
  - **Coordinator: x/sys ships**, so its bump from v0.44.0 to v0.47.0 is a shipped change beyond the two named modules. It is forced by the requirement chain, not chosen.

### Is the zstd upgrade safe for stores already on disk?
There are about 398 changed lines in zstd, so I wrote a temporary diagnostic (runs/12, not part of the repo). It encodes 1,516 objects with the store's exact settings under each version (SpeedDefault, concurrency 4, EncodeAll, decoder max 64 MiB). The corpus is random, repetitive, a 64 MiB maximum-size object, and every .go file in the repo.
- Both decoders decode all 3,032 files and every sha256 matches. Old objects read under the new version, and new objects read under an older binary, which is what a rollback needs.
- 1,515 of the 1,516 encodings are byte-identical across versions. The one exception is empty input: 0 bytes under v1.18.0, a 9-byte frame under v1.18.7. The store never encodes empty input: `putObject` only gets chunks, and `splitChecked` gives no chunks for empty data and never a zero-length chunk.
- `MaxEncodedSize(64 MiB)` = 67,110,417 in both, so `EncodedObjectLimit` is unchanged.

### Binary sizes (build-all, base 90e1db3 vs HEAD 2167632)
| target | base | HEAD | change |
| --- | --- | --- | --- |
| darwin-amd64 | 9,791,280 | 9,795,392 | +4,112 |
| darwin-arm64 | 8,939,026 | 8,939,026 | 0 |
| linux-amd64 | 9,625,726 | 9,625,726 | 0 |
| linux-arm64 | 8,781,950 | 8,781,950 | 0 |
| windows-amd64 | 9,717,760 | 9,719,296 | +1,536 |
| windows-arm64 | 8,685,056 | 8,686,080 | +1,024 |

The three zero changes are real: `.text` grew 368 to 704 bytes on every target, inside page-alignment padding. `go version -m` shows klauspost v1.18.7 and x/sys v0.47.0, and no x/mod, x/tools or x/sync.

### Bundle reproducibility
I ran `go run ./tools/devtool bundle --target windows/amd64 --target linux/amd64` three times: into bundle-a, into bundle-b, and into bundle-c with a fresh empty GOCACHE. Each gives 26 files and they are byte-identical (cmp on every file, plus `diff -r` a–b and a–c). The bundled `bin/qompack(.exe)` is byte-identical to build-all's binary for the same target. `git status --porcelain` was empty before and after each run.

### Tests
- **Which packages:** read literally, every package whose deps include klauspost is nearly the whole tree, which the brief forbids. I took the packages that import the upgraded modules directly, including the forced x/sys and x/tools: `internal/store`, `test/security` (klauspost/zstd), `internal/paths` (x/sys), `internal/ipc` (go-winio, which uses x/sys), `tools/lint/nomagic` (x/tools and x/mod).
- **Extra:** `test/docs` and `tools/devtool`, because go.mod and NOTICES are their inputs.
- **Windows:** all pass, one package at a time.
- **Linux:** the committed script ran non-root with -race: store 847 pass / 4 skip, security 30, paths 162 pass / 18 skip, ipc 144, nomagic 2. No race logs, and the source tree was unchanged.
  - The skips are the existing Windows-path and platform cases; they are listed in runs/11.
  - The "writing stat cache: permission denied" line also appears in earlier waves' Linux logs.
- There were no wall-clock failures, so nothing needed re-running alone.

### Other gates
- `go mod tidy -diff` shows no diff, and `go mod verify` passes.
- `licenses --check` says NOTICES is current.
- The pinned govulncheck ran at the base (90e1db3, from a git archive) and at HEAD: the base shows the three findings, HEAD shows none (runs/03 and 05).
- build-all built all six targets.
- fmt-check passes. `go vet` passes on the five packages for windows, linux and darwin.
- The allowed lint set passes: golangci-lint, nomagic (now built on x/tools v0.49), importgraph (72 packages), testdeps, bindeps (6 targets), sleepcheck, runpatterns, docmarkers.

### Criterion changes
None. No test, threshold, golden or lint was touched.

### Evidence
Committed in plans/sdd/V6-closeout/w5-deps/runs/, files 01 to 12. Nothing else under that directory was written, and no report.md, as the brief requires.

### Commits

- 5023c12 fix(deps): upgrade klauspost/compress to v1.18.7 (earlier seat; reviewed, kept unchanged, re-verified at 2167632)
- 2167632 fix(deps): upgrade golang.org/x/mod to v0.40.0 (the earlier seat's uncommitted draft; re-derived from 5023c12 byte for byte, then committed)
- 58cbb7b docs(closeout): record the w5-deps upgrade evidence (plans/sdd/V6-closeout/w5-deps/runs/01..12)

### Tests

- `go test ./tools/lint/nomagic -count=1 -timeout=30m (Windows, HEAD 2167632)` — ok 2.632s
- `go test ./internal/paths -count=1 -timeout=30m (Windows)` — ok 7.962s
- `go test ./internal/ipc -count=1 -timeout=30m (Windows)` — ok 21.523s
- `go test ./test/security -count=1 -timeout=30m (Windows)` — ok 81.804s
- `go test ./internal/store -count=1 -timeout=30m (Windows)` — ok 554.493s
- `go test ./test/docs -count=1 (Windows, at 2167632 and again at 58cbb7b)` — ok 2.729s / ok 3.074s
- `go test ./tools/devtool -count=1 -timeout=30m (Windows)` — ok 192.979s; git status --porcelain empty afterwards
- `sh plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh --prefix cx-w5-deps --repo C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w5-deps --out <scratch>/linux 2167632 deps-affected -- ./internal/store ./test/security ./internal/paths ./internal/ipc ./tools/lint/nomagic` — exit 0, non-root uid 10001, -race, gomaxprocs 4, no coload: store 847 pass/4 skip, security 30, paths 162 pass/18 skip, ipc 144, nomagic 2; 0 race logs; source tree unchanged
- `go mod tidy -diff; go mod verify; go list -m all` — no diff; all modules verified; exit 0
- `go run ./tools/devtool licenses --check` — THIRD_PARTY_NOTICES.md is current
- `go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./... (plus -show verbose, and GOOS=linux / GOOS=darwin with the same pinned binary)` — No vulnerabilities found on all three; the base 90e1db3 shows GO-2026-6180, GO-2026-6179, GO-2026-5841 (module-level, not called)
- `go run ./tools/devtool build-all (base 90e1db3 and HEAD 2167632)` — exit 0 both; sizes in the summary; the base build has the same sha256 as the pre-restart seat's
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all 8 PASS (importgraph 72 packages, bindeps 6 targets)
- `go run ./tools/devtool fmt-check; go vet on store, security, paths, ipc and nomagic (windows, GOOS=linux, GOOS=darwin)` — all exit 0
- `go build ./... (windows, GOOS=linux, GOOS=darwin)` — ok
- `go run ./tools/devtool bundle --target windows/amd64 --target linux/amd64 --out <scratch>/bundle-{a,b}, then c with a fresh GOCACHE` — 26 files each, byte-identical a=b=c; bundled binaries identical to build-all's
- `temporary diagnostic zstdcompat (runs/12): encode 1,516 objects with v1.18.0 and v1.18.7, decode all with both` — 3,032/3,032 decoded under each version with sha256 match; 1,515/1,516 encodings byte-identical; only empty input differs (0 B vs 9 B), which the store never encodes

### Open issues

- The x/sys bump from v0.44.0 to v0.47.0 is a shipped change beyond the two named modules. The x/mod v0.40.0 -> x/tools v0.49.0 requirement chain forces it. Coordinator: note it in the merge record.
- Out of scope, not touched: tools/pinned/go.mod requires golang.org/x/mod v0.37.0 (indirect), below the v0.40.0 fix for GO-2026-6180/6179. It is tooling only (golangci-lint, govulncheck, benchstat, gofumpt), never shipped, and the release govulncheck gate does not scan it. Upgrading it would move the pinned tool builds.
- Informational: klauspost v1.18.7 encodes empty input as a 9-byte frame, where v1.18.0 wrote 0 bytes. The store never encodes empty input (putObject only receives non-empty chunks), and both versions decode both forms.
- linux/arm64, darwin/amd64 and darwin/arm64 were cross-built and govulncheck'd but not test-run. No host for them was in scope.

## Independent review

### review:deps: needs-fixes

- **nit** `internal/store/compress_test.go:29-34 (in TestStoreCompress_RoundTrip's rapid property; unchanged by the diff, but the upgrade made it false)` — The upgrade made a comment false. It says EncodeAll's documented behaviour for empty input is "nothing is returned". klauspost/compress v1.18.7 changed the encoder default to fullZero=true (encoder_options.go:45 in v1.18.7; v1.18.0 has no default there), so store.Encode(nil) now returns a 9-byte empty zstd frame. The implementer's own diagnostic in runs/12 shows this: empty input gives 0 B under v1.18.0 and 9 B under v1.18.7. The assertions are still correct, because bytes.Equal holds and Decode of the empty frame returns empty content, so nothing fails. Only the rationale has gone stale. No doc or code comment in the repo records the behaviour change, which appears only in the evidence files and the implementer's open_issues.
  - Evidence: In the module cache, v1.18.7 zstd/encoder_options.go:45 has `fullZero: true,` in the defaults, and encoder.go:530-548 writes a frame header plus an empty raw last block when fullZero is set. v1.18.0 encoder_options.go only sets fullZero through the option at line 253. runs/12-zstd-crossversion-result.txt says: `differs: e3b0c44298fc1c14-empty.zst 0 vs 9 bytes`. compress_test.go:29-30 says: `EncodeAll's own documented behaviour for empty input is "nothing is returned" (klauspost/compress/zstd), so Decode(Encode(b)) legitimately comes back as a nil []byte`. TestStoreCompress_* passes at HEAD (3 PASS).
  - Fix: Change the comment to say that EncodeAll's empty-input output depends on the version: v1.18.0 returned nothing, and v1.18.7 and later default to WithZeroFrames(true) and return a 9-byte empty frame. Say that either way Decode returns empty content, so the property compares content with bytes.Equal. Optionally, give TestStoreCompress_Encode_EmptyInput a comment saying the store never encodes empty input, because splitChecked never yields a zero-length chunk. Keep this a comment-only change inside the scope of forced changes and do not touch any assertion. Alternatively, have the coordinator record the empty-frame change in the merge record next to the x/sys note.

## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.

