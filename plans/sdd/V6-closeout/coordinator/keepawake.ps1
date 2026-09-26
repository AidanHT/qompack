# Holds a keep-awake request (ES_CONTINUOUS | ES_SYSTEM_REQUIRED) while a sentinel file exists.
# Changes no power settings: the request ends when this process exits.
# Usage: pwsh -File keepawake.ps1 <sentinel-path>
param([Parameter(Mandatory = $true)][string]$Sentinel)
Add-Type -Namespace Win32 -Name Power -MemberDefinition @'
[DllImport("kernel32.dll")] public static extern uint SetThreadExecutionState(uint esFlags);
'@
$ES_CONTINUOUS = [uint32]"0x80000000"
$ES_SYSTEM_REQUIRED = [uint32]"0x00000001"
if (-not (Test-Path $Sentinel)) { New-Item -ItemType File -Path $Sentinel | Out-Null }
[Win32.Power]::SetThreadExecutionState($ES_CONTINUOUS -bor $ES_SYSTEM_REQUIRED) | Out-Null
Write-Output "keep-awake held while $Sentinel exists (pid $PID)"
while (Test-Path $Sentinel) { Start-Sleep -Seconds 30 }
[Win32.Power]::SetThreadExecutionState($ES_CONTINUOUS) | Out-Null
Write-Output "keep-awake released"
