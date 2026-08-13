# bench-all.ps1 —— dwxComment 严谨压测编排脚本（真实数据采集，供出图与报告）
#
# 阶段划分（与 scripts/stress.ps1 互补，重点解决"旧报告把 HTTP200 的限流响应算作成功吞吐"的口径问题）：
#   Phase A  抗滥用·单 IP 最坏场景（config-stress.yaml，50 rps / 1000 条/日，全部请求同一 XFF IP）
#            -> 验证每秒限流与每日上限"精确生效"，0 业务错误、0 HTTP 错误
#   Phase B  读路径·真实容量（config-bench.yaml，限流放宽至 100 万，等效不限流）
#            -> 空库列表查询真实吞吐 QPS 与延迟分位（hey 5 实例并行 + hey -o csv 延迟采样）
#   Phase C  写路径·真实吞吐（同 bench 配置，不限速）
# 全程以 sample-server.ps1 每 ~1s 采样 WorkingSet / HeapInuse / goroutine。
#
# 用法:
#   powershell -ExecutionPolicy Bypass -File scripts\bench-all.ps1   # 输出到 %TEMP%\lc-bench-<时间戳>
#   可选: -Out <目录> -WriteSeconds 25 -ReadSeconds 30 -LatencySeconds 15 -WriteThruSeconds 10
param(
    [string]$Base = "http://127.0.0.1:8080",
    [string]$Pprof = "http://127.0.0.1:6060",
    [string]$Site = "stress",
    [string]$WriteIp = "203.0.113.50",     # 单 IP 最坏场景专用（TEST-NET-3 保留段，无真实归属）
    [int]$ReadWorkers = 5,
    [int]$ReadConcurrency = 20,
    [int]$ReadSeconds = 30,
    [int]$ReadPages = 20,
    [int]$LatencySeconds = 15,
    [int]$WriteSeconds = 25,
    [int]$WriteThreads = 32,
    [float]$WriteQps = 800,                # 单 IP 攻击者总攻击速率（应远高于 50/s 上限）
    [int]$WriteThruSeconds = 10,           # Phase C 写吞吐时长
    [string]$Out = ""
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$Root = Split-Path -Parent $PSScriptRoot

if (-not $Out) { $Out = Join-Path $env:TEMP ("lc-bench-" + (Get-Date -Format "yyyyMMdd-HHmmss")) }
New-Item -ItemType Directory -Path $Out -Force | Out-Null
$stopA = Join-Path $Out "stop-a"
$stopB = Join-Path $Out "stop-b"

function log($msg) { Write-Host ("[{0}] {1}" -f (Get-Date -Format "HH:mm:ss"), $msg) }
function die($msg) { Write-Host "错误: $msg" -ForegroundColor Red; exit 1 }
function nowEpoch { return [DateTimeOffset]::Now.ToUnixTimeMilliseconds() / 1000.0 }
function MarkPhase($name, $t0, $t1) { "{0},{1},{2}" -f $name, $t0, $t1 | Out-File (Join-Path $Out "phases.csv") -Append -Encoding utf8 }

$phases = Join-Path $Out "phases.csv"
"phase,start_epoch,end_epoch" | Out-File $phases -Encoding utf8

# ---------- 预检 ----------
$hey = (Get-Command hey -ErrorAction SilentlyContinue).Source
if (-not $hey) { die "未找到 hey，请先: go install github.com/rakyll/hey@latest" }
log "hey: $hey"

$conn = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue
if (-not $conn) { die "8080 无监听，请先以 config-stress.yaml 启动服务（压测专用实例）" }
$strictPid = $conn.OwningProcess
log "Phase A 使用现有压测实例 PID=$strictPid（config-stress.yaml: 50 rps / 1000 条/日）"

# ---------- Phase A: 单 IP 最坏场景写压测 ----------
log "Phase A: 单 IP $WriteIp 最坏场景，$WriteThreads 线程 × ${WriteSeconds}s，攻击速率≈${WriteQps}/s"
$tA0 = nowEpoch
Start-Process powershell -WindowStyle Hidden -ArgumentList @('-NoProfile','-ExecutionPolicy','Bypass','-File',(Join-Path $PSScriptRoot 'sample-server.ps1'),'-TargetPid',"$strictPid",'-Pprof',$Pprof,'-Out',(Join-Path $Out 'samples-a.csv'),'-StopFile',$stopA)
& python (Join-Path $PSScriptRoot "stress-write.py") --base $Base --seconds $WriteSeconds --threads $WriteThreads --ip $WriteIp --site $Site --pages $ReadPages --nick sa --qps $WriteQps --out (Join-Path $Out "write-a.json") --csv (Join-Path $Out "write-a-per-sec.csv")
if ($LASTEXITCODE -ne 0) { die "Phase A 失败" }
$tA1 = nowEpoch
Set-Content $stopA "1"
MarkPhase "A_write_strict" $tA0 $tA1
log "Phase A 完成"

# ---------- 切换实例: 停严格实例 -> 构建并启动 bench 实例 ----------
log "停止严格实例 PID=$strictPid"
Stop-Process -Id $strictPid -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 2
for ($i = 0; $i -lt 20; $i++) {
    if (-not (Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue)) { break }
    Start-Sleep -Milliseconds 500
}

log "构建 bench 实例 (go build)"
Push-Location $Root
go build -trimpath -o (Join-Path $Out "dwx-bench.exe") main.go
if ($LASTEXITCODE -ne 0) { Pop-Location; die "go build 失败" }
Pop-Location
log "启动 bench 实例（config-bench.yaml，工作目录=$Out，DB 落于 $Out\bench.db）"
$svcOut = Join-Path $Out "bench-server.out.log"
$svcErr = Join-Path $Out "bench-server.err.log"
$benchProc = Start-Process -WindowStyle Hidden -FilePath (Join-Path $Out "dwx-bench.exe") -ArgumentList @('-config', (Join-Path $Root 'config\config-bench.yaml')) -WorkingDirectory $Out -RedirectStandardOutput $svcOut -RedirectStandardError $svcErr -PassThru
$benchPid = $benchProc.Id
$benchPid | Out-File (Join-Path $Out "bench.pid")
$ok = $false
for ($i = 0; $i -lt 60; $i++) {
    Start-Sleep -Milliseconds 500
    if (& curl.exe -sf -m 2 "$Base/api/v1/health" 2>$null) { $ok = $true; break }
}
if (-not $ok) { Get-Content $svcLog -Tail 20 -ErrorAction SilentlyContinue; die "bench 实例启动失败" }
log "bench 实例就绪 PID=$benchPid"

# ---------- Phase B: 读路径真实容量 ----------
log "Phase B: 读压测 $ReadWorkers 实例 × $ReadConcurrency 并发 × ${ReadSeconds}s，$ReadPages 页面（空库）"
$tB0 = nowEpoch
Start-Process powershell -WindowStyle Hidden -ArgumentList @('-NoProfile','-ExecutionPolicy','Bypass','-File',(Join-Path $PSScriptRoot 'sample-server.ps1'),'-TargetPid',"$benchPid",'-Pprof',$Pprof,'-Out',(Join-Path $Out 'samples-b.csv'),'-StopFile',$stopB)

$urls = 1..$ReadPages | ForEach-Object { "$Base/api/v1/comments?site=$Site&pageId=stress-page-$_&pageSize=20" }
$jobs = @()
for ($i = 1; $i -le $ReadWorkers; $i++) {
    $wOut = Join-Path $Out "read-worker$i.txt"
    $jobs += Start-Job -ArgumentList $hey, $ReadConcurrency, $ReadSeconds, "10.0.0.$i", (, $urls), $wOut -ScriptBlock {
        param($heyExe, $c, $z, $ip, $urlList, $workerOut)
        $a = @("-c", "$c", "-z", "${z}s", "-H", "X-Forwarded-For: $ip") + $urlList
        & $heyExe @a 2>&1 | Out-File $workerOut -Encoding utf8
    }
}
Wait-Job $jobs | Out-Null
Remove-Job $jobs -Force | Out-Null
# 按 worker1..N 顺序合并，保证图表中实例编号与数据一一对应
Get-Content (1..$ReadWorkers | ForEach-Object { Join-Path $Out "read-worker$_.txt" }) | Out-File (Join-Path $Out "read-summary.txt") -Encoding utf8
$tB1 = nowEpoch
MarkPhase "B_read_capacity" $tB0 $tB1
log "读压测完成"

# 延迟采样（hey -o csv，独立 15s，不与其他实例叠加）
# 注意: Out-File 必须带 -Encoding utf8，否则 PS 5.1 默认写成 UTF-16（latency.csv 解析需兼容）
log "延迟采样: hey -o csv ${LatencySeconds}s × $ReadConcurrency 并发"
$tL0 = nowEpoch
$largs = @("-c", "$ReadConcurrency", "-z", "${LatencySeconds}s", "-o", "csv", "-H", "X-Forwarded-For: 10.0.0.9") + $urls
& $hey @largs | Out-File (Join-Path $Out "latency.csv") -Encoding utf8
$tL1 = nowEpoch
MarkPhase "C_latency_sampling" $tL0 $tL1
log "延迟采样完成"

# ---------- Phase C: 写路径真实吞吐（放宽配置，不限速） ----------
log "Phase C: 写吞吐基准 $WriteThruSeconds s（$WriteThreads 线程，不限速）"
$tC0 = nowEpoch
& python (Join-Path $PSScriptRoot "stress-write.py") --base $Base --seconds $WriteThruSeconds --threads $WriteThreads --ip 203.0.113.99 --site $Site --pages $ReadPages --nick st --qps 0 --out (Join-Path $Out "write-b.json") --csv (Join-Path $Out "write-b-per-sec.csv")
if ($LASTEXITCODE -ne 0) { die "Phase C 失败" }
$tC1 = nowEpoch
MarkPhase "D_write_throughput" $tC0 $tC1
Set-Content $stopB "1"
log "Phase C 完成"

# ---------- 汇总 ----------
log "== 压测完成，产物目录: $Out =="
Get-ChildItem $Out | Select-Object Name, Length | Format-Table -AutoSize
