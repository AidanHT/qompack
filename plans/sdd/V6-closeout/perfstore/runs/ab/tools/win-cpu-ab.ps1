# Interleaved process-CPU A/B: runs one benchmark with a fixed iteration count in each binary,
# alternating order every round, and records the process's own user and kernel CPU time next to the
# wall time. CPU time counts the syscall work done on the process's own threads and not the time
# spent waiting behind other processes, so it separates "does less work" from "waited longer".
param([string]$Label, [int]$Rounds, [string]$Bench, [string]$BenchTime, [string]$VariantList)
$Variants = $VariantList -split ","
$Dir = 'C:\Users\Quant\AppData\Local\Temp\claude\C--Users-Quant-Documents-Programming-Projects-qompack\9c57653d-e5ff-4791-8ab4-ad49457994c1\scratchpad\perfstore'
$WorkDir = 'C:\Users\Quant\Documents\Programming\Projects\qompack-cx-perfstore\internal\store'
$OutFile = Join-Path $Dir "$Label-cpu.tsv"
"round`tvariant`twall_s`tuser_s`tkernel_s`tcpu_total_s`tbench_line" | Out-File -FilePath $OutFile -Encoding utf8
for ($i = 1; $i -le $Rounds; $i++) {
  if ($i % 2 -eq 0) { $order = @($Variants[1], $Variants[0]) } else { $order = @($Variants[0], $Variants[1]) }
  foreach ($v in $order) {
    $exe = Join-Path $Dir "store-$v.exe"
    $log = Join-Path $Dir "$Label-$v-round$i.raw"
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $exe
    $psi.Arguments = "-test.run ^$ -test.bench $Bench -test.benchmem -test.benchtime $BenchTime -test.count 1 -test.timeout 60m"
    $psi.WorkingDirectory = $WorkDir
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $proc = [System.Diagnostics.Process]::Start($psi)
    $stdout = $proc.StandardOutput.ReadToEnd()
    $proc.WaitForExit()
    $sw.Stop()
    $user = $proc.UserProcessorTime.TotalSeconds
    $kern = $proc.PrivilegedProcessorTime.TotalSeconds
    $stdout | Out-File -FilePath $log -Encoding utf8
    $line = ($stdout -split "`n" | Where-Object { $_ -like 'Benchmark*' } | Select-Object -Last 1).Trim()
    "$i`t$v`t$([math]::Round($sw.Elapsed.TotalSeconds,3))`t$([math]::Round($user,3))`t$([math]::Round($kern,3))`t$([math]::Round($user+$kern,3))`t$line" | Out-File -FilePath $OutFile -Append -Encoding utf8
  }
}
"done" | Out-File -FilePath $OutFile -Append -Encoding utf8
