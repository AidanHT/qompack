# power.sh: D57(d)'s power record, sourced by c8-night.sh, prefreeze.sh and overnight-c8.sh.
# A Windows timing run is a reference measurement only when it started on AC and the power source
# did not change while it ran (a battery run is neither a pass nor a fail). These helpers never
# fail and never trip `set -u`; every parse is checked, and anything unrecognised reads UNKNOWN.
#
#   power_read                  one line: "AC <pct>", "BAT <pct>" or "UNKNOWN ?" (the query failed,
#                               printed no recognisable line, or the host has no battery instance;
#                               <pct> is "?" when the charge is not reported)
#   power_events_since <epoch>  the System log's power events at or after <epoch>, oldest first, one
#                               per line: "<epoch> AC=1|0" (Kernel-Power 105, the source changed) or
#                               "<epoch> STANDBY" (Kernel-Power 506, Modern Standby entered, which
#                               freezes a run and its go test kill timer). Exit 0 when the log was
#                               read (no line = no event), 1 when it could not be read.
#   power_events_window <first> <last> <events>   the events with <first> <= epoch <= <last>
#   power_verdict <start-reading> <events-ok 0|1> <events in the window>
#                               "VALID", "INVALID-POWER <events>" or "NOT-REFERENCE <reason>"
#   power_wait_ac <budget-var> <budget-min> <deadline-epoch> <log-fn>
#                               polls once a minute until AC; returns 0 on AC, 1 when the minutes in
#                               the named counter reach the budget or the deadline passes. The
#                               counter counts polls, not wall time.
#   deadline_epoch <HH:MM>      the epoch of the next local HH:MM after now (today's, or tomorrow's
#                               once today's has passed); exit 1 on a malformed time

power_ps() { powershell -NoProfile -NonInteractive -Command "$1" 2>&1 | tr -d '\r'; }

power_read() {
  _pr=$(power_ps '$ErrorActionPreference = "Stop"
try {
  $b = Get-CimInstance -Namespace root/wmi -ClassName BatteryStatus | Select-Object -First 1
  $c = (Get-CimInstance Win32_Battery | Select-Object -First 1).EstimatedChargeRemaining
  if ($null -eq $b) { "POWER UNKNOWN nobattery" }
  else { "POWER {0} {1}" -f $(if ($b.PowerOnline) { "AC" } else { "BAT" }), $(if ($null -eq $c) { "?" } else { $c }) }
} catch { "POWER UNKNOWN error" }' | grep -E '^POWER (AC|BAT) ([0-9]+|\?)$' | tail -n 1)
  case $_pr in
    "POWER AC "*|"POWER BAT "*) echo "${_pr#POWER }" ;;
    *) echo "UNKNOWN ?" ;;
  esac
}

power_events_since() {
  case ${1:-} in ''|*[!0-9]*) return 1 ;; esac
  _pe=$(power_ps "\$ErrorActionPreference = 'Stop'
\$from = [DateTimeOffset]::FromUnixTimeSeconds($1).LocalDateTime
try { \$ev = @(Get-WinEvent -FilterHashtable @{LogName='System'; ProviderName='Microsoft-Windows-Kernel-Power'; Id=105,506; StartTime=\$from}) }
catch { if (\$_.FullyQualifiedErrorId -like 'NoMatchingEventsFound*') { \$ev = @() } else { 'EVENTS ERROR'; exit 0 } }
\$ev | Sort-Object TimeCreated | ForEach-Object { if (\$_.Id -eq 105) { 'EVENT {0} AC={1}' -f ([DateTimeOffset]\$_.TimeCreated).ToUnixTimeSeconds(), \$(if (\$_.Properties[0].Value) {1} else {0}) } else { 'EVENT {0} STANDBY' -f ([DateTimeOffset]\$_.TimeCreated).ToUnixTimeSeconds() } }
'EVENTS END'")
  printf '%s\n' "$_pe" | grep -qx 'EVENTS END' || return 1
  printf '%s\n' "$_pe" | grep -qx 'EVENTS ERROR' && return 1
  printf '%s\n' "$_pe" | sed -n 's/^EVENT \([0-9][0-9]*\) \(AC=[01]\|STANDBY\)$/\1 \2/p'
  return 0
}

power_events_window() {
  printf '%s\n' "${3:-}" | awk -v a="$1" -v b="$2" '$1 ~ /^[0-9]+$/ && $1 + 0 >= a + 0 && $1 + 0 <= b + 0'
}

power_verdict() {
  [ "${2:-0}" = 1 ] || { echo "NOT-REFERENCE the power history could not be read"; return 0; }
  if [ -n "${3:-}" ]; then echo "INVALID-POWER $(printf '%s' "$3" | tr '\n' ' ' | sed 's/ *$//')"; return 0; fi
  case ${1:-} in
    "AC "*) echo "VALID" ;;
    *) echo "NOT-REFERENCE started on ${1:-UNKNOWN ?} and the source did not change" ;;
  esac
}

power_wait_ac() {
  _pw_var=$1; _pw_budget=$2; _pw_deadline=$3; _pw_log=$4
  while :; do
    _pw_p=$(power_read)
    case $_pw_p in "AC "*) return 0 ;; esac
    [ "$(date +%s)" -ge "$_pw_deadline" ] && { "$_pw_log" "power: no AC before the deadline ($_pw_p)"; return 1; }
    eval "_pw_used=\${$_pw_var:-0}"
    [ "$_pw_used" -ge "$_pw_budget" ] && { "$_pw_log" "power: the night's AC wait budget is spent ($_pw_used of $_pw_budget min; $_pw_p)"; return 1; }
    [ $((_pw_used % 15)) -eq 0 ] && "$_pw_log" "power: waiting for AC ($_pw_p; $_pw_used of $_pw_budget min of the night's wait budget used)"
    sleep 60
    eval "$_pw_var=\$((_pw_used + 1))"
  done
}

deadline_epoch() {
  case ${1:-} in [01][0-9]:[0-5][0-9]|2[0-3]:[0-5][0-9]) ;; *) return 1 ;; esac
  _de_day=$(date +%F) || return 1
  _de=$(date -d "$_de_day $1" +%s) || return 1
  if [ "$_de" -le "$(date +%s)" ]; then
    _de=$(date -d "$(date -d "$_de_day +1 day" +%F) $1" +%s) || return 1
  fi
  echo "$_de"
}
