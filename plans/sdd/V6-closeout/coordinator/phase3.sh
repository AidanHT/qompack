#!/bin/sh
# phase3.sh <candidate-repo> <evidence-dir> <step...>
# Phase 3 gates on the frozen candidate, one recorded run per step (recrun.sh: <id>.json + <id>.log),
# strictly sequential so no gate co-loads another. Steps (run in the order given):
#   win-tree    go run ./tools/devtool test, QOMPACK_UNDER_COLOAD=1 (C3.2, CGO off, whole tree)
#   win-race    go run ./tools/devtool test-race, QOMPACK_UNDER_COLOAD=1 (C3.3)
#   win-timing  ci.yml's timing lane, -p 1, alone, no co-load   (D28: wall-clock rows judged in isolation),
#               then test/integration's three functional hot-path rows alone (D53(a): the hot-path rows
#               run alone; ci.yml's lane names only TestIntegration_HotPathWarmWithRealResidentState)
#   win-e2e-timing  test/e2e alone, no -race, no co-load        (D28: ci.yml's test-e2e job)
#   win-x11-alone  X11 (TestV3_HotPathUnchangedWithLedgerResident) by itself, -v (D53(d): its spawn floor)
#   lint        fmt-check, full devtool lint incl. stubskips, go vet (C3.5)
#   cover       go run ./tools/devtool cover, QOMPACK_UNDER_COLOAD=1 (C3.6: a coverage gate, not a
#               timing gate, so it may run beside the Linux lane)
#   gens        gen-*-docs --check, test/docs, licenses, govulncheck, build-all, plugin-validate,
#               replay --ci (C3.7, C3.8, C3.10)
#   fuzz        every nightly fuzz target for FUZZTIME (default 60s) (C3.9)
#   bundles     two archive builds byte-identical over all six targets, claude plugin validate (C3.11)
# A step's exit status is non-zero when ANY of its commands failed (lint, gens, fuzz and bundles run
# several), not only its last one.
#   linux-tree  non-root -race, every package but test/e2e, --coload (C3.4; D28)
#   linux-e2e   non-root -race test/e2e, --coload (C3.4; D28)
#   linux-timing  ci.yml's timing lane, non-root, no -race, -p 1, no co-load (D28)
#   linux-e2e-timing  test/e2e, non-root, no -race, no co-load (D28)
#   linux-child product-child race lane (QOMPACK_REQUIRE_CHILD_RACE=1, GOFLAGS=-race) (C3.3/C3.4)
#   release     go run ./tools/devtool release-check            (C3.12; needs Phase 2 dispositions)
# D28: as in ci.yml, the -race runs declare co-load (a wall-clock row there is reported, not judged)
# and the *-timing steps judge every wall-clock row alone, on the clock it was written against.
# Run the timing steps with nothing else on the host. The timing -run pattern and packages are
# read from ci.yml's timing job, which TestColoadYieldersAreJudgedInIsolation keeps complete.
# Hold a keep-awake (keepawake.ps1) for the whole run. Never run two copies at once.
set -u
repo=$1; ev=$2; shift 2
here=$(cd "$(dirname "$0")" && pwd)
rec() { sh "$here/recrun.sh" "$repo" "$ev" "$@"; }
gate="$repo/plans/sdd/V6-closeout/linux/linux-nonroot-gate.sh"
wrepo=$(cd "$repo" && pwd -W 2>/dev/null || pwd); wev=$(mkdir -p "$ev" && cd "$ev" && pwd -W 2>/dev/null || pwd)
head=$(git -C "$repo" rev-parse --short HEAD)
V=${BUNDLE_VERSION:-0.3.0}
tline=$(grep -E "^ *- run: go test -p 1 " "$repo/.github/workflows/ci.yml" | head -1)
tpat=$(printf '%s' "$tline" | sed -E "s/.*-run '([^']*)'.*/\1/")
tpkgs=$(printf '%s' "$tline" | sed -E "s/.*-run '[^']*' //")
[ -n "$tpat" ] && [ -n "$tpkgs" ] || { echo "cannot read the timing lane from ci.yml" >&2; exit 2; }
# The pattern must be the real row list: a wrong extraction still passes -n, and `go test -run` with a
# pattern that matches nothing exits 0 ("no tests to run"), so an empty timing step would look green.
case $tpat in *TestBudgetBF*) ;; *) echo "timing pattern from ci.yml lacks TestBudgetBF: $tpat" >&2; exit 2 ;; esac
hpnames="DegradesRatherThanBlocks BAPopulationIsTheDaemonHistogram SpoolTransitionJudgedPerMode"
hppat="^TestIntegration_HotPath($(echo $hpnames | tr ' ' '|'))\$"
targets="darwin-amd64 darwin-arm64 linux-amd64 linux-arm64 windows-amd64 windows-arm64"
rc_all=0
for step in "$@"; do
  case $step in
    win-tree) rec p3-win-tree -- env QOMPACK_UNDER_COLOAD=1 go run ./tools/devtool test ;;
    win-race) rec p3-win-race -- env QOMPACK_UNDER_COLOAD=1 go run ./tools/devtool test-race ;;
    win-timing) r=0
          rec p3-win-timing -- go test -p 1 -count=1 -timeout=30m -run "$tpat" $tpkgs || r=1
          rec p3-win-hotpath -- go test -p 1 -count=1 -timeout=30m -v -run "$hppat" ./test/integration || r=1
          # -run matching nothing exits 0, so each row must have reported a verdict of its own.
          for n in $hpnames; do
            grep -q "^--- PASS: TestIntegration_HotPath$n " "$ev/p3-win-hotpath.log" 2> /dev/null ||
              { echo "p3-win-hotpath: TestIntegration_HotPath$n did not pass (or did not run)"; r=1; }
          done
          [ $r -eq 0 ] ;;
    win-e2e-timing) rec p3-win-e2e-timing -- go test -count=1 -timeout=30m ./test/e2e ;;
    win-x11-alone) rec p3-win-x11-alone -- go test -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e ;;
    lint) r=0
          rec p3-fmt-check -- go run ./tools/devtool fmt-check || r=1
          rec p3-lint -- go run ./tools/devtool lint || r=1
          rec p3-vet -- go vet ./... || r=1
          [ $r -eq 0 ] ;;
    cover) rec p3-cover -- env QOMPACK_UNDER_COLOAD=1 go run ./tools/devtool cover ;;
    gens) r=0
          for g in gen-config-docs gen-mcp-docs gen-command-docs; do rec "p3-$g" -- go run ./tools/devtool $g --check || r=1; done
          rec p3-test-docs -- go test -count=1 ./test/docs/... || r=1
          rec p3-licenses -- go run ./tools/devtool licenses --check || r=1
          rec p3-govulncheck -- go run -modfile=tools/pinned/go.mod golang.org/x/vuln/cmd/govulncheck ./... || r=1
          rec p3-build-all -- go run ./tools/devtool build-all || r=1
          rec p3-plugin-validate -- go run ./tools/devtool plugin-validate || r=1
          rec p3-replay-ci -- go run ./tools/devtool replay --ci || r=1
          [ $r -eq 0 ] ;;
    fuzz) ft=${FUZZTIME:-60s}; r=0; fl=$(mktemp)
          grep -oE "\{ pkg: [^,]+, +fn: [A-Za-z0-9_]+ \}" "$repo/.github/workflows/nightly.yml" | sed -E 's/\{ pkg: ([^,]+), +fn: ([A-Za-z0-9_]+) \}/\1 \2/' > "$fl"
          [ -s "$fl" ] || { echo "no fuzz target found in nightly.yml"; r=1; }
          while read -r pkg fn; do
            rec "p3-fuzz-$fn" -- go test -run '^$' -fuzz "^$fn\$" -fuzztime "$ft" "$pkg" < /dev/null || { echo "fuzz $fn failed"; r=1; }
          done < "$fl"
          rm -f "$fl"; [ $r -eq 0 ] ;;
    bundles) r=0
          for b in A B; do rec "p3-bundle$b" -- go run ./tools/devtool bundle --archive --version "$V" --out "$wev/bundle$b" || r=1; done
          for b in A B; do
            if [ -d "$ev/bundle$b" ]; then (cd "$ev/bundle$b" && find . -type f | sort | xargs -r sha256sum) > "$ev/bundle$b.sha" || r=1
            else echo "bundle$b: no output directory"; : > "$ev/bundle$b.sha"; r=1; fi
          done
          if [ -s "$ev/bundleA.sha" ] && diff "$ev/bundleA.sha" "$ev/bundleB.sha" > "$ev/bundle-diff.txt"; then
            echo "bundles identical: $(wc -l < "$ev/bundleA.sha") files"
          else echo "bundles DIFFER or are empty (see $ev/bundle-diff.txt)"; r=1; fi
          nv=0
          { for t in $targets; do
              d="$ev/bundleA/qompack-plugin-$V-$t"
              if [ ! -f "$d/.claude-plugin/plugin.json" ]; then echo "== $d: no .claude-plugin/plugin.json"; r=1; continue; fi
              echo "== $d"; claude plugin validate "$d"; vr=$?; echo "exit=$vr"; nv=$((nv + 1)); [ $vr -eq 0 ] || r=1
            done; } > "$ev/p3-claude-plugin-validate.txt" 2>&1
          echo "claude plugin validate: $nv of $(echo $targets | wc -w) target(s) checked"
          [ $r -eq 0 ] ;;
    linux-tree) sh "$gate" --prefix cx-p3 --repo "$wrepo" --out "$wev/linux" "$head" p3-linux-tree --coload --timeout 60m -- ALL-NON-E2E > "$ev/p3-linux-tree-host.log" 2>&1 ;;
    linux-e2e) sh "$gate" --prefix cx-p3 --repo "$wrepo" --out "$wev/linux" "$head" p3-linux-e2e --coload --timeout 150m -- ./test/e2e > "$ev/p3-linux-e2e-host.log" 2>&1 ;;
    linux-timing) sh "$gate" --prefix cx-p3 --repo "$wrepo" --out "$wev/linux" "$head" p3-linux-timing --no-race --env GOFLAGS=-p=1 --timeout 30m --run "$tpat" -- $tpkgs > "$ev/p3-linux-timing-host.log" 2>&1 ;;
    linux-e2e-timing) sh "$gate" --prefix cx-p3 --repo "$wrepo" --out "$wev/linux" "$head" p3-linux-e2e-timing --no-race --timeout 60m -- ./test/e2e > "$ev/p3-linux-e2e-timing-host.log" 2>&1 ;;
    linux-child) sh "$gate" --prefix cx-p3 --repo "$wrepo" --out "$wev/linux" "$head" p3-linux-child --no-race --env CGO_ENABLED=1 --env GOFLAGS=-race --env QOMPACK_REQUIRE_CHILD_RACE=1 --timeout 15m \
          --run '^(TestE2E_RequiredProductChildRaceInstrumentation|TestE2EHookRoundTrip|TestE2E_ObserverThroughDaemon|TestE2E_SupersessionVisibleAfterRestart|TestE2E_VerbatimPromptSurvivesRestart|TestE2E_SessionStartCompactRestoresCheckpointItems|TestStdioServerEndToEnd|TestV3_CrashRecoveryReplaysObserverAndLedgerConsistently)$' -- ./test/e2e > "$ev/p3-linux-child-host.log" 2>&1 ;;
    release) rec p3-release-check -- go run ./tools/devtool release-check ;;
    *) echo "unknown step $step" >&2; exit 2 ;;
  esac
  rc=$?; echo "step $step exit=$rc"; [ $rc -ne 0 ] && rc_all=1
done
exit $rc_all
