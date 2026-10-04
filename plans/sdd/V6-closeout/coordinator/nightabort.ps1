# nightabort.ps1 -MsysPid <pid> -WinPid <pid> [-Stop]
# Lists, and with -Stop stops, exactly one night's process tree (README.md "Candidate 8", Abort step
# 1): the shell night.log's `start pid <msys> winpid <windows-pid>` line names (chain.log's
# `start candidate=<sha> pid <msys> winpid <windows-pid>` for a night started alone), and nothing
# else.
#
# Why not a Windows parent-pid walk. An MSYS shell starts a program by fork and exec: the fork's
# stub process spawns the program and then exits, so Windows records a parent that no longer exists
# for every program the night's shells start (sh, go, docker, python), and a walk down ParentProcessId
# from the root finds the root alone; stopping it would leave overnight-c8.sh running. MSYS keeps
# its own parent pids (`ps -l`'s PPID), so the tree is walked there first, and each process's
# Windows pid is taken from the same listing. Native Windows children of those (go.exe's test
# binaries, cmd's children) do record a live parent, so they are added by ParentProcessId, a child
# only when it was created after its parent: Windows never clears a dead parent's pid, so a later
# process holding a recycled pid is not taken for a child.
#
# The root must still be the night's shell: its MSYS pid must map to the logged Windows pid, and
# its command line must name c8-night.sh or overnight-c8.sh. Anything else stops nothing (exit 2).
# -Stop stops the MSYS shells top-down first (a stopped shell starts nothing more), then every other
# listed process deepest first, each only while it is still the process listed (same pid, same
# creation time). It changes nothing else; an orphan a shell started between the listing and its
# stop is left to Abort step 1's command-line sweep.
param(
  [Parameter(Mandatory = $true)][int]$MsysPid,
  [Parameter(Mandatory = $true)][int]$WinPid,
  [switch]$Stop,
  [string]$GitUsrBin = 'C:\Program Files\Git\usr\bin'
)
$ErrorActionPreference = 'Stop'
$ps = Join-Path $GitUsrBin 'ps.exe'; $cat = Join-Path $GitUsrBin 'cat.exe'

$w = "$(& $cat "/proc/$MsysPid/winpid" 2> $null)".Trim()
$cl = "$(& $cat "/proc/$MsysPid/cmdline" 2> $null)" -replace "`0", ' '
if ($w -ne "$WinPid" -or $cl -notmatch '(c8-night|overnight-c8)\.sh') {
  Write-Warning "MSYS pid $MsysPid (Windows pid '$w', command '$($cl.Trim())') is not the night's shell with Windows pid ${WinPid}: nothing listed, nothing stopped"
  exit 2
}

# MSYS processes: PID PPID PGID WINPID ...; a leading status letter (I, S, O) is dropped.
$msys = @{}
foreach ($line in (& $ps -l | Select-Object -Skip 1)) {
  $f = @(-split $line)
  if ($f.Count -gt 0 -and $f[0] -notmatch '^\d+$') { $f = @($f | Select-Object -Skip 1) }
  if ($f.Count -ge 4 -and $f[0] -match '^\d+$' -and $f[1] -match '^\d+$' -and $f[3] -match '^\d+$') {
    $msys[[int]$f[0]] = @{ PPid = [int]$f[1]; WinPid = [int]$f[3] }
  }
}
$all = @{}; foreach ($p in Get-CimInstance Win32_Process) { $all[[int]$p.ProcessId] = $p }

$tree = New-Object System.Collections.ArrayList   # @{ Proc; Depth; Msys }
$seen = @{}
$level = @($MsysPid); $depth = 0
while ($level.Count -gt 0) {                          # the MSYS subtree, breadth first
  $next = @()
  foreach ($m in $level) {
    $wp = $msys[$m].WinPid
    if ($all.ContainsKey($wp) -and -not $seen.ContainsKey($wp)) {
      $seen[$wp] = $true; [void]$tree.Add(@{ Proc = $all[$wp]; Depth = $depth; Msys = $m })
    }
    $next += @($msys.Keys | Where-Object { $msys[$_].PPid -eq $m -and $_ -ne $m })
  }
  $level = $next; $depth++
}
$i = 0
while ($i -lt $tree.Count) {                          # native children of everything listed
  $p = $tree[$i].Proc
  foreach ($c in $all.Values) {
    if ($c.ParentProcessId -eq $p.ProcessId -and $c.CreationDate -ge $p.CreationDate -and -not $seen.ContainsKey([int]$c.ProcessId)) {
      $seen[[int]$c.ProcessId] = $true; [void]$tree.Add(@{ Proc = $c; Depth = $tree[$i].Depth + 1; Msys = $null })
    }
  }
  $i++
}

$tree | ForEach-Object { [pscustomobject]@{
    WinPid = $_.Proc.ProcessId; MsysPid = $_.Msys; Depth = $_.Depth; Name = $_.Proc.Name
    Created = $_.Proc.CreationDate; CommandLine = $_.Proc.CommandLine } } |
  Format-Table -AutoSize -Wrap | Out-String -Width 220 | Write-Output
"$($tree.Count) process(es) in the night's tree"
if (-not $Stop) { exit 0 }

$shells = @($tree | Where-Object { $_.Msys -ne $null -and $_.Proc.Name -in @('bash.exe', 'sh.exe') } | Sort-Object { $_.Depth })
$rest = @($tree | Where-Object { -not ($_.Msys -ne $null -and $_.Proc.Name -in @('bash.exe', 'sh.exe')) } | Sort-Object { - $_.Depth })
$stopped = 0
foreach ($t in @($shells) + @($rest)) {
  $now = Get-CimInstance Win32_Process -Filter "ProcessId=$($t.Proc.ProcessId)"
  if ($now -and $now.CreationDate -eq $t.Proc.CreationDate) {
    Stop-Process -Id $t.Proc.ProcessId -Force -ErrorAction SilentlyContinue; $stopped++
  }
}
"stopped $stopped process(es)"
