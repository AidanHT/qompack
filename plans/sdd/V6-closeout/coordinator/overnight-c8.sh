#!/bin/sh
# overnight-c8.sh <candidate-repo> <candidate-sha> <evidence-dir>
# Candidate 8's local night (D57-D59), strictly sequential, nothing else on the host. As overnight-c6.sh,
# plus D57(d)'s power rule and the reference-host release gate:
#   - every Windows timing step (win-timing, win-e2e-timing, win-x11-alone, quiet c51-win, release-check)
#     starts only on AC (waits up to 180 min), and is INVALID-POWER when Kernel-Power event 105 shows a
#     transition during it; an invalid step is retried once on AC;
#   - release-check --tag v0.3.0 (C3.12/C7.4) runs last, against a LOCAL lightweight tag on the candidate
#     that is deleted afterwards (nothing is pushed; a pushed v* tag would cut a real release).
# Hosted ci.yml and nightly.yml on the same commit supply the native-platform lanes.
set -u
C=$1; H=$2; E=$3
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$E"
log() { echo "$* $(date -u +%FT%TZ)" >> "$E/chain.log"; }
ps1() { powershell -NoProfile -Command "$1" 2>&1 | tr -d '\r'; }
power() {
  ps1 '$b = Get-CimInstance -Namespace root/wmi -ClassName BatteryStatus | Select-Object -First 1
       $p = (Get-CimInstance Win32_Battery | Select-Object -First 1).EstimatedChargeRemaining
       "{0} {1}" -f $(if ($b.PowerOnline) { "AC" } else { "BAT" }), $p'
}
transitions_since() {
  ps1 "Get-WinEvent -FilterHashtable @{LogName='System'; ProviderName='Microsoft-Windows-Kernel-Power'; Id=105; StartTime=[datetime]'$1'} -ErrorAction SilentlyContinue | ForEach-Object { '{0:HH:mm:ss} AC={1}' -f \$_.TimeCreated, \$_.Properties[0].Value }" | tr '\n' ' '
}
acwait() { # $1 step name; waits up to 180 min for AC
  n=0
  while :; do
    set -- "$1" $(power)
    [ "$2" = AC ] && { log "power $1: AC $3%"; return 0; }
    [ "$n" -ge 180 ] && { log "power $1: no AC within 180 min (BAT $3%), running anyway: NOT a reference measurement"; return 1; }
    [ $((n % 15)) -eq 0 ] && log "power $1: waiting for AC (BAT $3%)"
    sleep 60; n=$((n + 1))
  done
}
# timed <name> <command...>: AC-gated, power-verified, retried once when the power source changed.
timed() {
  name=$1; shift
  for try in 1 2; do
    acwait "$name"
    t0=$(date +%FT%T)
    "$@"; rc=$?
    tr=$(transitions_since "$t0")
    if [ -n "$(echo "$tr" | tr -d ' ')" ]; then
      log "step $name try $try exit=$rc INVALID-POWER transitions=[$tr]"
      continue
    fi
    log "step $name try $try exit=$rc VALID power=$(power)"
    return $rc
  done
  return 99
}

log "start candidate=$H"
engine_was_up=0; docker ps > /dev/null 2>&1 && engine_was_up=1; log "engine up at start=$engine_was_up"
docker ps --format "{{.Names}} {{.Status}}" >> "$E/chain.log" 2>&1
docker stop qompack-v6-linux-verification > /dev/null 2>&1
for s in win-timing win-e2e-timing win-x11-alone; do
  timed "$s" sh "$here/phase3.sh" "$C" "$E" "$s" >> "$E/chain.log" 2>&1
done
log "windows timing done"
GOFLAGS=-p=4 sh "$here/phase3.sh" "$C" "$E" win-race bundles >> "$E/chain.log" 2>&1; log "windows race+bundles exit=$?"

docker desktop start > /dev/null 2>&1; docker start qompack-v6-linux-verification > /dev/null && docker update --cpus 8 --memory 8g --memory-swap 8g qompack-v6-linux-verification > /dev/null; log "container start exit=$?"
sh "$here/phase3.sh" "$C" "$E" linux-timing linux-e2e-timing >> "$E/chain.log" 2>&1; log "linux timing exit=$?"
sh "$here/phase3.sh" "$C" "$E" linux-tree linux-e2e linux-child >> "$E/chain.log" 2>&1; log "linux race exit=$?"
docker stop qompack-v6-linux-verification > /dev/null 2>&1; log "container stopped exit=$?"
if [ "$engine_was_up" = 0 ]; then docker desktop stop > /dev/null 2>&1; log "engine stopped exit=$?"; else log "engine left running (owner)"; fi

timed c51-win sh "$here/quiet.sh" "$C" cf31e01 "$E/quiet" c51-win >> "$E/chain.log" 2>&1
log "quiet c51-win done"

# C3.12 / C7.4: the reference-host release gate, against a local tag that never leaves this machine.
if git -C "$C" rev-parse -q --verify refs/tags/v0.3.0 > /dev/null; then
  log "REFUSED release-check: a tag v0.3.0 already exists locally"
else
  git -C "$C" tag v0.3.0 "$H" && log "local tag v0.3.0 -> $H (not pushed)"
  timed release-check sh -c "cd '$C' && go run ./tools/devtool release-check --tag v0.3.0" > "$E/p3-release-check-tag.log" 2>&1
  cp "$C/dist/release-check.json" "$E/release-check.json" 2>/dev/null
  git -C "$C" tag -d v0.3.0 > /dev/null && log "local tag v0.3.0 deleted"
fi
log "done"
