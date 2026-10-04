# Holds a keep-awake request (ES_CONTINUOUS | ES_SYSTEM_REQUIRED) while a sentinel file exists.
# Changes no power settings: the request ends when this process exits.
# Usage: pwsh -File keepawake.ps1 <sentinel-path>   (create the sentinel first; delete it to release)
# It never creates the sentinel. A caller that refuses early deletes the sentinel, possibly before
# this process has even started (pwsh's start and Add-Type take about 2.4 s), and a script that then
# re-created it would hold the machine awake with nothing left to release it. So a sentinel missing
# at start means: do not hold, and exit.
param([Parameter(Mandatory = $true)][string]$Sentinel)
if (-not (Test-Path $Sentinel)) {
  Write-Output "keep-awake not holding: $Sentinel does not exist (pid $PID)"
  exit 0
}
Add-Type -Namespace Win32 -Name Power -MemberDefinition @'
[DllImport("kernel32.dll")] public static extern uint SetThreadExecutionState(uint esFlags);
'@
$ES_CONTINUOUS = [uint32]"0x80000000"
$ES_SYSTEM_REQUIRED = [uint32]"0x00000001"
if (-not (Test-Path $Sentinel)) {
  Write-Output "keep-awake not holding: $Sentinel was deleted while this process started (pid $PID)"
  exit 0
}
[Win32.Power]::SetThreadExecutionState($ES_CONTINUOUS -bor $ES_SYSTEM_REQUIRED) | Out-Null
Write-Output "keep-awake held while $Sentinel exists (pid $PID)"
while (Test-Path $Sentinel) { Start-Sleep -Seconds 30 }
[Win32.Power]::SetThreadExecutionState($ES_CONTINUOUS) | Out-Null
Write-Output "keep-awake released"
