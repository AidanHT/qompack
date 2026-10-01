#!/bin/sh
# prefreeze.sh <repo> <evidence-dir> [step...]
# D53(a)'s pre-freeze merged-tree check on Windows, sequential, at below-normal priority so the
# owner's daytime use wins: test/e2e alone, test/integration's hot-path rows alone and the rest of
# it apart, the fault/security/platform/release (and other test/) packages, then every internal
# package and tools/devtool. Hosted ci.yml on the same tree covers Linux and macOS.
# Steps: e2e hotpath integration testpkgs internal (default: all, in that order).
# Each step writes <step>.log and appends "step <name> exit=<code> <utc>" to summary.log.
set -u
R=$1; E=$2; shift 2
steps=${*:-e2e hotpath integration testpkgs internal}
mkdir -p "$E"
hp='^(TestIntegration_HotPath|TestV3_HotPath)'
run() { name=$1; shift
  echo "start $name $(date -u +%FT%TZ)" >> "$E/summary.log"
  (cd "$R" && "$@") > "$E/$name.log" 2>&1
  echo "step $name exit=$? $(date -u +%FT%TZ)" >> "$E/summary.log"; }
echo "head $(git -C "$R" rev-parse HEAD) go=$(go env GOVERSION)" >> "$E/summary.log"
for s in $steps; do
  case $s in
    e2e) run e2e go test -p 1 -count=1 -timeout 90m ./test/e2e ;;
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
