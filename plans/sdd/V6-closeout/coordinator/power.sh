# power.sh: D57(d)'s power record, sourced by c8-night.sh, prefreeze.sh and overnight-c8.sh.
# A Windows timing run is a reference measurement only when it started on AC and the power source
# did not change while it ran (a battery run is neither a pass nor a fail). These helpers never
# fail and never trip `set -u`; every parse is checked, and anything unrecognised reads UNKNOWN.
#
#   power_read                  one line: "AC <pct>", "BAT <pct>" or "UNKNOWN ?" (the query failed,
#                               printed no recognisable line, or the host has no battery instance;
#                               <pct> is "?" when the charge is not reported)
#   power_events_since <epoch>  the System log's power events at or after <epoch>, oldest first, one
#                               per line: "<epoch> AC=1|0" (Kernel-Power 105, the source changed),
#                               "<epoch> STANDBY" (506, Modern Standby entered, which freezes a run
#                               and its go test kill timer), "<epoch> SLEEP" (42, sleep or hibernate
#                               entered) or "<epoch> RESUME" (107, resumed from sleep). Exit 0 when
#                               the log was read (no line = no event), 1 when it could not be read.
#   power_events_window <first> <last> <events>   the events with <first> <= epoch <= <last>
#   power_verdict <start-reading> <events-ok 0|1> <events in the window> [<end-reading>]
#                               "VALID" (started on AC, no event, and, when given, ended on AC),
#                               "INVALID-POWER <events>" or "NOT-REFERENCE <reason>". An end reading
#                               that is not AC with no event to explain it is NOT-REFERENCE: the
#                               record contradicts itself, so it cannot be a reference.
#   power_wait_ac <budget-var> <budget-min> <deadline-epoch> <log-fn>
#                               polls once a minute until AC; returns 0 on AC, 1 when the minutes in
#                               the named counter reach the budget or the deadline passes. The
#                               counter counts polls, not wall time. A budget of "-" is no budget:
#                               only the deadline ends the wait (a C5.2 chunk, D65(c)), and the
#                               counter still counts its polls.
#   deadline_epoch <HH:MM>      the epoch of the next local HH:MM after now (today's, or tomorrow's
#                               once today's has passed); exit 1 on a malformed time
#   deadline_near <epoch>       exit 0 when <epoch> is at most NIGHT_MAX_AHEAD_H hours away, or
#                               NIGHT_ALLOW_FAR=1. A farther one means a daytime launch, for which
#                               deadline_epoch gave tomorrow's HH:MM: the night would run into the
#                               owner's day.
#   disk_free_ok <log-fn> <dir...>   exit 0 when every <dir>'s drive has at least NIGHT_MIN_FREE_GB
#                               (40) GiB free; otherwise, or when df cannot read a drive, it logs why
#                               through <log-fn> and exits 1
#   govuln_unreachable <file>   exit 0 when <file>'s govulncheck output says the vulnerability
#                               database or the module proxy could not be reached and reports no
#                               vulnerability (GOVULN_FOUND_ERE, GOVULN_UNREACHABLE_ERE below)
#
# Every PowerShell query is bounded (POWER_PS_TIMEOUT_S, 120 s, then a kill 10 s later): a hung WMI
# or event-log query must not hold the night past its deadline. A query that timed out printed
# nothing, so power_read reads "UNKNOWN ?" and power_events_since returns 1 (unreadable, so
# NOT-REFERENCE), as for any other failed query. Measured under load: power_read 0.8 s,
# power_events_since over an hour 2.8 s.
#
# NIGHT_MAX_AHEAD_H (16): candidate 8's night, through release-check, is about 7.25 h (README
# "Candidate 8": the pre-freeze about 1 h, the steps before release-check about 3.25 h,
# release-check 3 h), and the C5.2 night at most about 9 h (the C1.16 rig within its 65 min bound,
# then C5.2's full list in its chunks, about 4.2 h on Windows and 3.8 h on Linux; D62(c) keeps the
# two apart), each plus whatever AC_WAIT_BUDGET_MIN's 3 h of waiting takes. 16 h leaves room for an
# earlier launch the owner allows (from 16:00 for an 08:00 deadline, which also absorbs an AC cut in
# a C5.2 night) and refuses every launch made after the deadline's hour, which deadline_epoch would
# carry into the next day.
NIGHT_MAX_AHEAD_H=${NIGHT_MAX_AHEAD_H:-16}
# RC_EST_S: how long a local release-check is expected to take, shared by c8-night.sh (its launch
# checks) and overnight-c8.sh (release-check's latest start). SP-17's record is 8451 s on a smaller
# tree and w17-release estimates 2.5-3 h on this laptop; rounded up to 3 h. It has never been
# measured with release-check's current step list, so overnight-c8.sh also stops a run still going
# at the deadline plus RC_GRACE_S (its header).
RC_EST_S=${RC_EST_S:-10800}
# NIGHT_MIN_FREE_GB: the night's clones (merged tree, release-check, quiet.sh's base and candidate
# clones with their test binaries) and the whole-tree test binaries in GOCACHE need room. One
# quiet.sh work directory was 879 MB, and the drive had 65 GB free (97 % used) on 2026-10-04.
NIGHT_MIN_FREE_GB=${NIGHT_MIN_FREE_GB:-40}
POWER_PS_TIMEOUT_S=${POWER_PS_TIMEOUT_S:-120}
# govulncheck's own verdict lines (a reported vulnerability fails), and the network failures that
# mean it never checked the tree. prefreeze.sh's gate and overnight-c8.sh's reading of a red
# release-check use the same two patterns.
GOVULN_FOUND_ERE='Vulnerability #|Your code is affected|vulnerabilit(y|ies) found'
GOVULN_UNREACHABLE_ERE='(vuln\.go\.dev|proxy\.golang\.org|sum\.golang\.org).*(dial tcp|no such host|i/o timeout|connection (refused|reset)|TLS handshake timeout|network is unreachable|context deadline exceeded)|(dial tcp|no such host|lookup).*(vuln\.go\.dev|proxy\.golang\.org|sum\.golang\.org)'

power_ps() { timeout -k 10 "$POWER_PS_TIMEOUT_S" powershell -NoProfile -NonInteractive -Command "$1" 2>&1 | tr -d '\r'; }

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
try { \$ev = @(Get-WinEvent -FilterHashtable @{LogName='System'; ProviderName='Microsoft-Windows-Kernel-Power'; Id=105,506,42,107; StartTime=\$from}) }
catch { if (\$_.FullyQualifiedErrorId -like 'NoMatchingEventsFound*') { \$ev = @() } else { 'EVENTS ERROR'; exit 0 } }
\$kind = @{ 506 = 'STANDBY'; 42 = 'SLEEP'; 107 = 'RESUME' }
\$ev | Sort-Object TimeCreated | ForEach-Object { \$t = ([DateTimeOffset]\$_.TimeCreated).ToUnixTimeSeconds(); if (\$_.Id -eq 105) { 'EVENT {0} AC={1}' -f \$t, \$(if (\$_.Properties[0].Value) {1} else {0}) } else { 'EVENT {0} {1}' -f \$t, \$kind[[int]\$_.Id] } }
'EVENTS END'")
  printf '%s\n' "$_pe" | grep -qx 'EVENTS END' || return 1
  printf '%s\n' "$_pe" | grep -qx 'EVENTS ERROR' && return 1
  printf '%s\n' "$_pe" | sed -n 's/^EVENT \([0-9][0-9]*\) \(AC=[01]\|STANDBY\|SLEEP\|RESUME\)$/\1 \2/p'
  return 0
}

power_events_window() {
  printf '%s\n' "${3:-}" | awk -v a="$1" -v b="$2" '$1 ~ /^[0-9]+$/ && $1 + 0 >= a + 0 && $1 + 0 <= b + 0'
}

power_verdict() {
  [ "${2:-0}" = 1 ] || { echo "NOT-REFERENCE the power history could not be read"; return 0; }
  if [ -n "${3:-}" ]; then echo "INVALID-POWER $(printf '%s' "$3" | tr '\n' ' ' | sed 's/ *$//')"; return 0; fi
  case ${1:-} in
    "AC "*) ;;
    *) echo "NOT-REFERENCE started on ${1:-UNKNOWN ?} and the source did not change"; return 0 ;;
  esac
  case ${4-AC } in
    "AC "*) echo "VALID" ;;
    *) echo "NOT-REFERENCE ended on ${4:-UNKNOWN ?} with no power event recorded" ;;
  esac
}

power_wait_ac() {
  _pw_var=$1; _pw_budget=$2; _pw_deadline=$3; _pw_log=$4
  while :; do
    _pw_p=$(power_read)
    case $_pw_p in "AC "*) return 0 ;; esac
    [ "$(date +%s)" -ge "$_pw_deadline" ] && { "$_pw_log" "power: no AC before the deadline ($_pw_p)"; return 1; }
    eval "_pw_used=\${$_pw_var:-0}"
    if [ "$_pw_budget" = - ]; then
      [ $((_pw_used % 15)) -eq 0 ] && "$_pw_log" "power: waiting for AC until $(date -d "@$_pw_deadline" +%FT%T) ($_pw_p; $_pw_used min waited tonight; no budget applies, D65(c))"
    else
      [ "$_pw_used" -ge "$_pw_budget" ] && { "$_pw_log" "power: the night's AC wait budget is spent ($_pw_used of $_pw_budget min; $_pw_p)"; return 1; }
      [ $((_pw_used % 15)) -eq 0 ] && "$_pw_log" "power: waiting for AC ($_pw_p; $_pw_used of $_pw_budget min of the night's wait budget used)"
    fi
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

deadline_near() {
  case ${1:-} in ''|*[!0-9]*) return 1 ;; esac
  [ "${NIGHT_ALLOW_FAR:-}" = 1 ] && return 0
  [ $(( $1 - $(date +%s) )) -le $(( NIGHT_MAX_AHEAD_H * 3600 )) ]
}

disk_free_ok() {
  _df_log=$1; shift; _df_bad=0
  for _df_d in "$@"; do
    [ -n "$_df_d" ] || continue
    if command -v cygpath > /dev/null 2>&1; then _df_d=$(cygpath -u "$_df_d"); fi
    _df_k=$(df -Pk "$_df_d" 2> /dev/null | awk 'NR == 2 && $4 ~ /^[0-9]+$/ { print $4 }')
    case $_df_k in
      ''|*[!0-9]*) "$_df_log" "disk: cannot read the free space of $_df_d"; _df_bad=1 ;;
      *) if [ "$_df_k" -lt $((NIGHT_MIN_FREE_GB * 1048576)) ]; then
           "$_df_log" "disk: $_df_d has $((_df_k / 1048576)) GiB free, under NIGHT_MIN_FREE_GB=$NIGHT_MIN_FREE_GB"; _df_bad=1
         else
           "$_df_log" "disk: $_df_d has $((_df_k / 1048576)) GiB free (NIGHT_MIN_FREE_GB=$NIGHT_MIN_FREE_GB)"
         fi ;;
    esac
  done
  return "$_df_bad"
}

govuln_unreachable() {
  grep -qE "$GOVULN_FOUND_ERE" "$1" 2> /dev/null && return 1
  grep -qiE "$GOVULN_UNREACHABLE_ERE" "$1" 2> /dev/null
}
