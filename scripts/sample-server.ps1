# sample-server.ps1 —— dwxComment 压测内存/协程采样器（独立进程，跨终端会话存活）
#
# 用法（由 bench-all.ps1 以 Start-Process 启动）:
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\sample-server.ps1 `
#       -TargetPid <服务PID> -Pprof http://127.0.0.1:6060 -Out <csv路径> -StopFile <停止标记文件>
#
# 行为: 每 ~0.9s 采样一次，写一行 CSV:
#   ts_epoch, ts, working_set_bytes, heap_inuse_bytes, goroutines
# 当 StopFile 出现或目标进程消失时退出。
# 说明:
#   - WorkingSet 来自进程内存计数器（Windows）
#   - HeapInuse 来自 pprof heap?debug=1&gc=0（gc=0 不强制 GC，反映真实堆占用）
#   - goroutines 来自 pprof goroutine?debug=1 首行 total
param(
    [int]$TargetPid,        # 注意: 不能叫 $Pid（PowerShell 只读自动变量，会直接启动失败）
    [string]$Pprof = "http://127.0.0.1:6060",
    [string]$Out,
    [string]$StopFile
)

$ErrorActionPreference = "SilentlyContinue"
"ts_epoch,ts,working_set_bytes,heap_inuse_bytes,goroutines" | Out-File $Out -Encoding utf8

while (-not (Test-Path $StopFile)) {
    $epoch = [DateTimeOffset]::Now.ToUnixTimeMilliseconds() / 1000.0
    $p = Get-Process -Id $TargetPid -ErrorAction SilentlyContinue
    if (-not $p) { break }
    $ws = $p.WorkingSet64

    $heap = ""
    $hl = & curl.exe -sf -m 3 "$Pprof/debug/pprof/heap?debug=1&gc=0" 2>$null | Select-String '^# HeapInuse' | Select-Object -First 1
    if ($hl) { $heap = ($hl.ToString() -split '\s+')[3] }

    $g = ""
    $gl = & curl.exe -sf -m 3 "$Pprof/debug/pprof/goroutine?debug=1" 2>$null | Select-Object -First 1
    if ($gl -and $gl.ToString() -match 'total (\d+)') { $g = $Matches[1] }

    "{0},{1},{2},{3},{4}" -f $epoch, (Get-Date -Format "HH:mm:ss"), $ws, $heap, $g | Out-File $Out -Append -Encoding utf8
    Start-Sleep -Milliseconds 900
}
