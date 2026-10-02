#!/bin/sh
# prefreeze.sh <repo> <evidence-dir> [step...]
# D53(a)'s pre-freeze merged-tree check on Windows, sequential, at below-normal priority so the
# owner's daytime use wins: test/e2e alone, test/integration's hot-path rows alone and the rest of
# it apart, the fault/security/platform/release (and other test/) packages, then every internal
# package and tools/devtool. Hosted ci.yml on the same tree covers Linux and macOS.
# Steps: gate e2e hotpath integration testpkgs internal (default: all, in that order), and e2efunc
# (test/e2e without its wall-clock X11 row, which the chain judges on AC); gate is build,
# vet on three OSes, fmt, the generated-docs checks and the lint subset.
# Each step writes <step>.log and appends "step <name> exit=<code> <utc>" to summary.log.
set -u
R=$1; E=$2; shift 2
steps=${*:-gate e2e hotpath integration testpkgs internal}
mkdir -p "$E"
hp='^(TestIntegration_HotPath|TestV3_HotPath)'
run() { name=$1; shift
  echo "start $name $(date -u +%FT%TZ)" >> "$E/summary.log"
  (cd "$R" && "$@") > "$E/$name.log" 2>&1
  echo "step $name exit=$? $(date -u +%FT%TZ)" >> "$E/summary.log"; }
echo "head $(git -C "$R" rev-parse HEAD) go=$(go env GOVERSION)" >> "$E/summary.log"
for s in $steps; do
  case $s in
    gate) run gate sh -c 'go build ./... && go vet ./... && GOOS=linux go vet ./... && GOOS=darwin go vet ./... &&
            go run ./tools/devtool fmt-check && go run ./tools/devtool gen-config-docs --check &&
            go run ./tools/devtool gen-mcp-docs --check &&
            go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns,coveragefloors' ;;
    e2e) run e2e go test -p 1 -count=1 -timeout 90m ./test/e2e ;;
    e2efunc) run e2efunc go test -p 1 -count=1 -timeout 90m -skip '^TestV3_HotPath' ./test/e2e ;;
    hotpath) run hotpath go test -p 1 -count=1 -timeout 30m -run "$hp" ./test/integration ;;
    integration) run integration go test -p 2 -count=1 -timeout 60m -skip "$hp" ./test/integration ;;
    testpkgs) run testpkgs go test -p 2 -count=1 -timeout 60m ./test/fault/... ./test/security/... ./test/platform/... \
                ./test/release/... ./test/canary/... ./test/dedup/... ./test/replay/... \
                ./test/guards/... ./test/docs/... ./test/bench/... ;;
    internal) run internal go test -p 2 -count=1 -timeout 60m ./internal/... ./tools/... ./cmd/... ;;
    *) echo "unknown step $s" >> "$E/summary.log" ;;
  esac
done
echo "done $(date -u +%FT%TZ)" >> "$E/summary.log"
