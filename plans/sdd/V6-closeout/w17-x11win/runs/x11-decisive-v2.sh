#!/bin/sh
# x11-decisive-v2.sh <out-dir> <plan>...       (W17 x11win, fix seat; supersedes x11-decisive.sh)
#
# D53(d)'s decisive experiment, rebuilt after the fix seat found that every strict Windows X11
# failure since D41 ran with the laptop ON BATTERY and inside or straight after the test/e2e package,
# on code that has the background publication pass (5b8f1e1a). The three are confounded in every
# failing run; this script crosses them. Power facts (Kernel-Power event 105): the AC is cut at
# ~90-100 % charge and restored at ~35-40 %, unattended; the DC profile differs from AC in CPU policy
# (EPP 50 vs 25, boost 40 vs 60, heterogeneous policy 4 vs 0), PCIe ASPM (max savings vs off) and NVMe
# power management (latency tolerance 50 vs 15 ms, idle timeout 100 vs 200 ms), and Energy Saver
# turns on below 50 % on DC.
#
# NOT run by the seat that wrote it. The coordinator runs it only after candidate 6's chain.log says
# "quiet exit=" (or "done"), with nothing else measuring. Strictly sequential, normal priority, no
# co-load or non-reference-disk declaration. Every run is power-gated, power-verified afterwards
# (event 105 since its start), snapshotted and counter-logged. A run during which the power source
# changed is INVALID-POWER; a block containing one is retried once.
#
# Arms (checkouts the coordinator prepares; never qompack-cx-cand):
#   STOCK   pristine checkout at 99d0b18 (e.g. ../qompack-cx-w17-x11win)
#   TRACE   99d0b18 + trace.patch          (phase timestamps, pass-end log, kept daemon logs)
#   NOPASS  99d0b18 + trace+nopass.patch   (as TRACE, the background publication pass skipped)
#   OLD     checkout at aa7b799c (w12's last strict X11 pass; its own test and harness)
# TRACE and NOPASS are DIAGNOSTIC builds: never a candidate, never committed, never bundled.
#
# Conditions: iso = IDLE seconds with no test I/O before the run; post = the arm's whole test/e2e
# package immediately before, no gap (the overnight chain's position). Power: ac or bat.
#
# Plans, in priority order:
#   e1  ac-iso, STOCK, 3 x [ctl, x11, int]          the reference condition: does X11 pass at all?
#   e2  ac-post, STOCK, e2e -> x11 -> int -> ctl     position without battery
#   e3  bat-post, STOCK, e2e -> x11 -> int -> ctl    the overnight failure condition (reproduce)
#   e5  bat-iso, STOCK, ctl -> x11 -> int            battery without position
#   e4  under COND (default: the first of bat-post, ac-post, bat-iso that failed), A-B-B-A
#       TRACE/NOPASS on x11 then int                  is the background pass a co-factor?
#   e6  under COND, A-B-B-A OLD/TRACE on x11          anything since w12, if e4 fails both arms
set -u
S=${1:?usage: x11-decisive-v2.sh <out-dir> <plan>...}; shift
plans=${*:-e1 e2 e3 e5}
chain=C:/Users/Quant/Documents/Programming/Projects/qompack-v6/plans/sdd/V6-closeout/phase3/c6/chain.log
grep -q -E '^(quiet exit=|done)' "$chain" || { echo "refusing: candidate 6's chain has not finished quiet.sh" >&2; exit 2; }
unset QOMPACK_UNDER_COLOAD QOMPACK_NONREFERENCE_DISK
IDLE=${IDLE:-300}          # seconds with no test I/O before an iso run
WAIT_MAX=${WAIT_MAX:-150}  # minutes to wait for a block's power state before skipping it
BAT_MIN=${BAT_MIN:-55}     # a battery run starts only above the DC Energy Saver threshold (50 %)
BAT_BLOCK=${BAT_BLOCK:-85} # a battery post block (about 45 min) starts only at or above this charge
AC_MAX=${AC_MAX:-80}       # an AC run starts only below the charge at which the AC has been cut
COND=${COND:-bat-post}
mkdir -p "$S/logs" || exit 2
w() { cygpath -w "$1"; }
ps1() { powershell -NoProfile -Command "$1" 2>&1 | tr -d '\r'; }

power() { # "AC <pct>" or "BAT <pct>"
  ps1 '$b = Get-CimInstance -Namespace root/wmi -ClassName BatteryStatus | Select-Object -First 1
       $p = (Get-CimInstance Win32_Battery | Select-Object -First 1).EstimatedChargeRemaining
       "{0} {1}" -f $(if ($b.PowerOnline) { "AC" } else { "BAT" }), $p'
}
transitions_since() { # Kernel-Power 105 events since $1 (ISO local time)
  ps1 "Get-WinEvent -FilterHashtable @{LogName='System'; ProviderName='Microsoft-Windows-Kernel-Power'; Id=105; StartTime=[datetime]'$1'} -ErrorAction SilentlyContinue | ForEach-Object { \$x=[xml]\$_.ToXml(); '{0:HH:mm:ss} AC={1}' -f \$_.TimeCreated, (\$x.Event.EventData.Data | Where-Object Name -eq 'AcOnline').'#text' }" | tr '\n' ' '
}
wait_power() { # $1 ac|bat, $2 battery minimum for this start; returns 1 on timeout
  want=$1; bmin=$2; waited=0
  while :; do
    set -- $(power); src=$1; pct=$2
    case $want in
      ac)  [ "$src" = AC ] && [ "$pct" -le "$AC_MAX" ] && return 0 ;;
      bat) [ "$src" = BAT ] && [ "$pct" -ge "$bmin" ] && return 0 ;;
    esac
    [ "$waited" -ge "$WAIT_MAX" ] && return 1
    [ $((waited % 10)) -eq 0 ] && echo "waiting for $want>=$bmin (now $src $pct%) $(date +%FT%T)" >> "$S/runs.txt"
    sleep 60; waited=$((waited + 1))
  done
}
snap() { # host state, read-only
  {
    echo "== $1 $(date +%FT%T%z) power=$(power)"
    docker ps --format '{{.Names}} {{.Status}}' 2>&1
    ps1 '$os = Get-CimInstance Win32_OperatingSystem; "freeMB=" + [int]($os.FreePhysicalMemory/1024)
      Get-Process vmmem*,qompack*,go,bench-hotpath* -ErrorAction SilentlyContinue | ForEach-Object { "proc " + $_.Name + " pid=" + $_.Id + " start=" + $_.StartTime + " wsMB=" + [int]($_.WorkingSet64/1MB) }
      $c = Get-PSDrive C; "C: usedGB=" + [int]($c.Used/1GB) + " freeGB=" + [int]($c.Free/1GB)
      powercfg /getactivescheme'
  } >> "$S/snapshots.txt"
}
counters_start() { # bounded: -sc samples at 2 s; stopped when the run ends
  MSYS_NO_PATHCONV=1 typeperf "\PhysicalDisk(_Total)\Avg. Disk sec/Write" "\PhysicalDisk(_Total)\Avg. Disk sec/Transfer" \
    "\PhysicalDisk(_Total)\Current Disk Queue Length" "\Processor Information(_Total)\% Processor Performance" \
    "\Processor Information(_Total)\Processor Frequency" "\Processor(_Total)\% Processor Time" \
    "\Memory\Available MBytes" "\Memory\Standby Cache Normal Priority Bytes" "\Memory\Modified Page List Bytes" \
    -si 2 -sc "$2" -f CSV -o "$(w "$S/$1-counters.csv")" -y > /dev/null 2>&1 &
  tp=$!
}
counters_stop() { kill "$tp" 2>/dev/null; wait "$tp" 2>/dev/null; }
dir_of() { case $1 in stock) echo "${STOCK:?set STOCK}";; trace) echo "${TRACE:?set TRACE}";;
  nopass) echo "${NOPASS:?set NOPASS}";; old) echo "${OLD:?set OLD}";; *) echo "bad arm $1" >&2; exit 2;; esac; }

run() { # $1 id, $2 arm, $3 row. Appends VALID|INVALID-POWER to $S/.verdict
  id=$1; arm=$2; row=$3; dir=$(dir_of "$arm"); before=$(power); t0=$(date +%FT%T)
  case $row in
    ctl) sc=150; set -- go run ./tools/devtool bench-hotpath --iterations 2000 --hook observe-tool --warm-daemon --json "$(w "$S/$id.json")" ;;
    x11) sc=480; set -- go test -count=1 -timeout=30m -v -run '^TestV3_HotPathUnchangedWithLedgerResident$' ./test/e2e ;;
    int) sc=180; set -- go test -p 1 -count=1 -timeout=30m -v -run '^TestIntegration_HotPathWarmWithRealResidentState$' ./test/integration ;;
    e2e) sc=900; set -- go test -count=1 -timeout=30m ./test/e2e ;;
    *) echo "bad row $row" >&2; exit 2 ;;
  esac
  snap "$id"; counters_start "$id" "$sc"
  echo "start $id arm=$arm row=$row power=$before $t0 head=$(git -C "$dir" rev-parse --short HEAD)" >> "$S/runs.txt"
  ( cd "$dir" && QOMPACK_DIAG_KEEP_LOGS="$(w "$S/logs/$id")" "$@" ) > "$S/$id.log" 2>&1; rc=$?
  counters_stop
  after=$(power); tr=$(transitions_since "$t0")
  verdict=VALID; [ -n "$(echo "$tr" | tr -d ' ')" ] && verdict=INVALID-POWER
  echo "$verdict" >> "$S/.verdict"
  echo "end $id exit=$rc power=$after transitions=[$tr] $verdict $(date +%FT%T)" >> "$S/runs.txt"
  { echo "== $id arm=$arm row=$row exit=$rc power $before -> $after transitions=[$tr] $verdict"
    grep -E 'DIAG|spawn floor:|^ +B-(A|B|D) +n=|hook_controlled_observed \(recvTS|delivery ledger:|X11 pair|^--- (PASS|FAIL)|^(ok|FAIL)' "$S/$id.log" | cut -c1-240
    grep -rh -E 'publication accounting continues|DIAG' "$S/logs/$id" 2>/dev/null | cut -c1-240
  } >> "$S/summary.txt"
}

block() { # $1 block id, $2 cond (ac-iso|ac-post|bat-iso|bat-post), $3 arm, rest: rows. Retried once on INVALID-POWER
  bid=$1; cond=$2; arm=$3; shift 3
  for try in 1 2; do
    : > "$S/.verdict"
    pw=${cond%-*}; pos=${cond#*-}; bmin=$BAT_MIN; [ "$pos" = post ] && bmin=$BAT_BLOCK
    [ "$pos" = iso ] && sleep "$IDLE"
    wait_power "$pw" "$bmin" || { echo "skip $bid: no $pw window in ${WAIT_MAX} min" >> "$S/runs.txt"; return; }
    [ "$pos" = post ] && run "$bid-t$try-e2e" "$arm" e2e
    for row in "$@"; do
      [ "$pos" = iso ] && [ "$row" != "$1" ] && sleep "$IDLE"
      run "$bid-t$try-$row" "$arm" "$row"
    done
    grep -q INVALID-POWER "$S/.verdict" || return
    echo "retry $bid: the power source changed during it" >> "$S/runs.txt"
  done
}

echo "plans=$plans COND=$COND STOCK=${STOCK:-} TRACE=${TRACE:-} NOPASS=${NOPASS:-} OLD=${OLD:-} $(date +%FT%T)" >> "$S/runs.txt"
for ph in $plans; do
  n=$(date +%H%M)
  case $ph in
    e1) for r in 1 2 3; do block "e1-$n-r$r" ac-iso stock ctl x11 int; done ;;
    e2) block "e2-$n" ac-post stock x11 int ctl ;;
    e3) block "e3-$n" bat-post stock x11 int ctl ;;
    e5) block "e5-$n" bat-iso stock ctl x11 int ;;
    e4) for row in x11 int; do for a in trace nopass nopass trace; do block "e4-$n-$row-$a-$(date +%H%M%S)" "$COND" "$a" "$row"; done; done ;;
    e6) for a in old trace trace old; do block "e6-$n-$a-$(date +%H%M%S)" "$COND" "$a" x11; done ;;
    *) echo "unknown plan $ph" >&2; exit 2 ;;
  esac
done
echo "done $(date +%FT%T)" >> "$S/runs.txt"
