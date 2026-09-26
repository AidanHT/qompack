#!/bin/sh
# phase3.sh <candidate-repo> <evidence-dir> <step...>
# Phase 3 gates on the frozen candidate, one recorded run per step (recrun.sh: <id>.json + <id>.log),
# strictly sequential so no gate co-loads another. Steps (run in the order given):
#   win-tree    go run ./tools/devtool test                     (C3.2, CGO off, whole tree)
#   win-race    go run ./tools/devtool test-race                (C3.3)
#   lint        fmt-check, full devtool lint incl. stubskips, go vet (C3.5)
#   cover       go run ./tools/devtool cover                    (C3.6)
#   gens        gen-*-docs --check, test/docs, licenses, govulncheck, build-all, plugin-validate,
#               replay --ci (C3.7, C3.8, C3.10)
#   fuzz        every nightly fuzz target for FUZZTIME (default 60s) (C3.9)
#   bundles     two archive builds byte-identical over all six targets, claude plugin validate (C3.11)
#   linux-tree  non-root -race, every package but test/e2e, no co-load (C3.4)
#   linux-e2e   non-root -race test/e2e (C3.4)
#   linux-child product-child race lane (QOMPACK_REQUIRE_CHILD_RACE=1, GOFLAGS=-race) (C3.3/C3.4)
#   release     go run ./tools/devtool release-check            (C3.12; needs Phase 2 dispositions)
# Hold a keep-awake (keepawake.ps1) for the whole run. Never run two copies at once.
set -u
repo=$1; ev=$2; shift 2
here=$(cd "$(dirname "$0")" && pwd)
rec() { sh "$here/recrun.sh" "$repo" "$ev" "$@"; }
gate="$repo/plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh"
wrepo=$(cd "$repo" && pwd -W 2>/dev/null || pwd); wev=$(mkdir -p "$ev" && cd "$ev" && pwd -W 2>/dev/null || pwd)
head=$(git -C "$repo" rev-parse --short HEAD)
V=${BUNDLE_VERSION:-0.3.0}
rc_all=0
for step in "$@"; do
  case $step in
    win-tree) rec p3-win-tree -- go run ./tools/devtool test ;;
    win-race) rec p3-win-race -- go run ./tools/devtool test-race ;;
    lint) rec p3-fmt-check -- go run ./tools/devtool fmt-check
          rec p3-lint -- go run ./tools/devtool lint
          rec p3-vet -- go vet ./... ;;
    cover) rec p3-cover -- go run ./tools/devtool cover ;;
    gens) for g in gen-config-docs gen-mcp-docs gen-command-docs; do rec "p3-$g" -- go run ./tools/devtool $g --check; done
          rec p3-test-docs -- go test -count=1 ./test/docs/...
          rec p3-licenses -- go run ./tools/devtool licenses --check
          rec p3-govulncheck -- go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./...
          rec p3-build-all -- go run ./tools/devtool build-all
          rec p3-plugin-validate -- go run ./tools/devtool plugin-validate
          rec p3-replay-ci -- go run ./tools/devtool replay --ci ;;
    fuzz) ft=${FUZZTIME:-60s}
          grep -oE "\{ pkg: [^,]+, +fn: [A-Za-z0-9_]+ \}" "$repo/.github/workflows/nightly.yml" | sed -E 's/\{ pkg: ([^,]+), +fn: ([A-Za-z0-9_]+) \}/\1 \2/' |
          while read -r pkg fn; do rec "p3-fuzz-$fn" -- go test -run '^$' -fuzz "^$fn\$" -fuzztime "$ft" "$pkg" || echo "fuzz $fn failed"; done ;;
    bundles) for b in A B; do rec "p3-bundle$b" -- go run ./tools/devtool bundle --archive --version "$V" --out "$wev/bundle$b"; done
          (cd "$ev/bundleA" && find . -type f | sort | xargs sha256sum) > "$ev/bundleA.sha"
          (cd "$ev/bundleB" && find . -type f | sort | xargs sha256sum) > "$ev/bundleB.sha"
          diff "$ev/bundleA.sha" "$ev/bundleB.sha" > "$ev/bundle-diff.txt" && echo "bundles identical: $(wc -l < "$ev/bundleA.sha") files"
          for t in "$ev"/bundleA/*/; do [ -f "$t/.claude-plugin/plugin.json" ] && { echo "== $t"; claude plugin validate "$t"; echo "exit=$?"; }; done > "$ev/p3-claude-plugin-validate.txt" 2>&1 ;;
    linux-tree) sh "$gate" --prefix cx-p3 --repo "$wrepo" --out "$wev/linux" "$head" p3-linux-tree --timeout 60m -- ALL-NON-E2E > "$ev/p3-linux-tree-host.log" 2>&1 ;;
    linux-e2e) sh "$gate" --prefix cx-p3 --repo "$wrepo" --out "$wev/linux" "$head" p3-linux-e2e --timeout 150m -- ./test/e2e > "$ev/p3-linux-e2e-host.log" 2>&1 ;;
    linux-child) sh "$gate" --prefix cx-p3 --repo "$wrepo" --out "$wev/linux" "$head" p3-linux-child --no-race --env CGO_ENABLED=1 --env GOFLAGS=-race --env QOMPACK_REQUIRE_CHILD_RACE=1 --timeout 15m \
          --run '^(TestE2E_RequiredProductChildRaceInstrumentation|TestE2EHookRoundTrip|TestE2E_ObserverThroughDaemon|TestE2E_SupersessionVisibleAfterRestart|TestE2E_VerbatimPromptSurvivesRestart|TestE2E_SessionStartCompactRestoresCheckpointItems|TestStdioServerEndToEnd|TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently)$' -- ./test/e2e > "$ev/p3-linux-child-host.log" 2>&1 ;;
    release) rec p3-release-check -- go run ./tools/devtool release-check ;;
    *) echo "unknown step $step" >&2; exit 2 ;;
  esac
  rc=$?; echo "step $step exit=$rc"; [ $rc -ne 0 ] && rc_all=1
done
exit $rc_all
