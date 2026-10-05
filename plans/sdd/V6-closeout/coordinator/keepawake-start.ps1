# keepawake-start.ps1 <evidence-dir>: takes a night's keep-awake for a launch that has no c8-night.sh
# to hold it (the C5.2 night, and the overnight part run alone after the freeze, README.md Abort
# step 7). It creates <evidence-dir>/keepawake.sentinel, starts keepawake.ps1 hidden with its output
# in <evidence-dir>/keepawake.log, and waits up to 15 one-second polls (ended early when the process
# ends) for keepawake.ps1's "keep-awake held" line, as c8-night.sh does for its own night.
#   exit 0  "keep-awake held (pid <pid>; <log>)": the request is in force; delete the sentinel to
#           release it (keepawake.ps1 exits within 30 s)
#   exit 1  "keep-awake NOT confirmed ...": the sentinel is deleted and the keep-awake process this
#           script started is stopped if still running, so nothing is held; do not launch the night
#   exit 2  bad arguments
# It changes no power setting and stops no process it did not start. -KeepAwake names another script
# in keepawake.ps1's place (the dry harness's K3 case); the pwsh that starts it is this one's own.
param(
  [Parameter(Mandatory = $true)][string]$Evidence,
  [string]$KeepAwake = (Join-Path $PSScriptRoot 'keepawake.ps1')
)
if (-not (Test-Path -LiteralPath $KeepAwake -PathType Leaf)) {
  Write-Output "keepawake-start: no keep-awake script at $KeepAwake"
  exit 2
}
New-Item -ItemType Directory -Force -Path $Evidence | Out-Null
$sentinel = Join-Path $Evidence 'keepawake.sentinel'
$logf = Join-Path $Evidence 'keepawake.log'
$errf = Join-Path $Evidence 'keepawake.err'
if (-not (Test-Path -LiteralPath $sentinel)) { New-Item -ItemType File -Path $sentinel | Out-Null }
$self = (Get-Process -Id $PID).Path   # this pwsh, never a stub or another pwsh found on PATH
$p = Start-Process -FilePath $self -WindowStyle Hidden -PassThru `
  -RedirectStandardOutput $logf -RedirectStandardError $errf `
  -ArgumentList @('-NoProfile', '-File', $KeepAwake, $sentinel)
function Test-Held { (Test-Path -LiteralPath $logf) -and (Select-String -LiteralPath $logf -Pattern '^keep-awake held' -Quiet) }
for ($i = 0; $i -lt 15; $i++) {
  if (Test-Held) { break }
  if ($p.HasExited) { break }
  Start-Sleep -Seconds 1
}
if (Test-Held) {
  Write-Output "keep-awake held (pid $($p.Id); $logf)"
  exit 0
}
Remove-Item -LiteralPath $sentinel -Force -ErrorAction SilentlyContinue
$state = 'had exited'
if (-not $p.HasExited) {
  Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
  $state = 'was stopped'
}
$said = ''
if (Test-Path -LiteralPath $logf) { $said = ((Get-Content -LiteralPath $logf -ErrorAction SilentlyContinue) -join ' ') }
if ($said.Length -gt 200) { $said = $said.Substring(0, 200) }
Write-Output "keep-awake NOT confirmed: keepawake.log says '$said'; the sentinel is deleted and the keep-awake process (pid $($p.Id)) $state, so nothing is held; do not launch the night"
exit 1
