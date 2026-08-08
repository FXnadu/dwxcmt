# stress.ps1 —— dwxComment Windows 高并发压测脚本（PowerShell 5.1+）
#
# 目标：在 Windows 下验证 1G 内存环境下长时间高压运行的稳定性
#      （内存不泄漏、不 OOM、接口不雪崩）
#
# 用法：
#   powershell -ExecutionPolicy Bypass -File scripts\stress.ps1
#   可选参数（均有默认值）：
#     -Base          服务地址（默认 http://127.0.0.1:8080）
#     -Pprof         pprof 观测端口（默认 http://127.0.0.1:6060）
#     -Site          压测独立站点名，隔离生产数据（默认 stress）
#     -ReadWorkers   读压测 hey 实例数（默认 5）
#     -ReadConcurrency 每实例并发（默认 20）
#     -ReadSeconds   读压测时长秒（默认 30）
#     -ReadPages     读压测页面数（默认 20）
#     -WriteWorkers  写压测并发 worker 数（默认 8）
#     -WriteSeconds  写压测时长秒（默认 20）
#     -WriteIpPool   写压测伪造 IP 池大小（默认 500，池大小 ×20 即写入预算）
#     -MixedSeconds  读写混合时长秒，0 表示跳过（默认 0）
#
# 与 scripts/stress.sh 的差异（Windows 适配）：
#   - 内存采样用 Get-Process WorkingSet64（替代 /proc/PID/status）
#   - 并发 worker 用 Start-Job（PS5.1 无 ForEach -Parallel）
#   - hey / curl.exe 为外部程序，跨平台可用
#
# 限流背景：全局 5 req/s/IP、每日 20 条评论/IP、127.0.0.1 为可信代理（XFF 生效）。
# 故读压测用 N 个 hey 实例各持不同 XFF IP 叠加吞吐；
# 写压测逐请求随机 XFF IP + pageId + content（hey 无法随机 body，用 curl）。

param(
    [string]$Base = "http://127.0.0.1:8080",
    [string]$Pprof = "http://127.0.0.1:6060",
    [string]$Site = "stress",
    [int]$ReadWorkers = 5,
    [int]$ReadConcurrency = 20,
    [int]$ReadSeconds = 30,
    [int]$ReadPages = 20,
    [int]$WriteWorkers = 8,
    [int]$WriteSeconds = 20,
    [int]$WriteIpPool = 500,
    [int]$MixedSeconds = 0
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$out = Join-Path $env:TEMP ("lc-stress-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
New-Item -ItemType Directory -Path $out -Force | Out-Null
$samplerJob = $null

function log($msg) { Write-Host ("[{0}] {1}" -f (Get-Date -Format "HH:mm:ss"), $msg) }
function die($msg) { Write-Host "错误: $msg" -ForegroundColor Red; exit 1 }

# ---------- 预检 ----------
$hey = Get-Command hey -ErrorAction SilentlyContinue
if (-not $hey) {
    foreach ($c in @("$env:USERPROFILE\go\bin\hey.exe", "$env:GOPATH\bin\hey.exe")) {
        if (Test-Path $c) { $hey = [pscustomobject]@{ Source = $c }; break }
    }
}
if (-not $hey) { die "未安装 hey，请先执行: go install github.com/rakyll/hey@latest" }
$heyPath = $hey.Source

log "预检: $Base/api/v1/health"
& curl.exe -sf --max-time 5 "$Base/api/v1/health" | Out-Null
if ($LASTEXITCODE -ne 0) { die "服务不可达，请先启动 dwx-comment" }
log "预检: pprof $Pprof/debug/pprof/goroutine"
& curl.exe -sf --max-time 5 "$Pprof/debug/pprof/goroutine?debug=1" | Out-Null
if ($LASTEXITCODE -ne 0) { die "pprof 不可达（应为 127.0.0.1:6060）" }

$proc = Get-Process -Name dwx-comment -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $proc) { die "找不到 dwx-comment 进程" }
$lcPid = $proc.Id
log "观测进程 PID=$lcPid"

# ---------- 工具函数 ----------
# pprof 堆接口会先触发一次 GC，取到的是可回收后的基线值，最适合判泄漏；# HeapInuse 单位是字节
# 注意：debug=1 输出为 "# HeapInuse = 4251648"，按空白切分后数值下标为 3（第 0/1/2 项为 "#"/"HeapInuse"/"="）
function Get-HeapInuseBytes {
    $line = & curl.exe -sf --max-time 3 "$Pprof/debug/pprof/heap?debug=1" | Select-String '^# HeapInuse' | Select-Object -First 1
    if (-not $line) { return "" }
    return ($line.ToString() -split '\s+')[3]
}
function Get-WorkingSetBytes {
    $p = Get-Process -Id $lcPid -ErrorAction SilentlyContinue
    if (-not $p) { return "" }
    return $p.WorkingSet64
}

function Start-Sampler {
    $script:samplerJob = Start-Job -ArgumentList $lcPid, $Pprof, (Join-Path $out "samples.csv") -ScriptBlock {
        param($procId, $pprofUrl, $csv)
        "ts,working_set_bytes,heap_inuse_bytes" | Out-File $csv -Encoding utf8
        while (Get-Process -Id $procId -ErrorAction SilentlyContinue) {
            $ws = (Get-Process -Id $procId -ErrorAction SilentlyContinue).WorkingSet64
            $heapLine = & curl.exe -sf --max-time 3 "$pprofUrl/debug/pprof/heap?debug=1" | Select-String '^# HeapInuse' | Select-Object -First 1
            $heap = if ($heapLine) { ($heapLine.ToString() -split '\s+')[3] } else { "" }
            "{0},{1},{2}" -f (Get-Date -Format "HH:mm:ss"), $ws, $heap | Out-File $csv -Append -Encoding utf8
            Start-Sleep -Seconds 2
        }
    }
}
function Stop-Sampler {
    if ($script:samplerJob) { Stop-Job $script:samplerJob -ErrorAction SilentlyContinue | Out-Null; Remove-Job $script:samplerJob -Force -ErrorAction SilentlyContinue | Out-Null; $script:samplerJob = $null }
}
trap { Stop-Sampler; Write-Host "已中断，中间结果见 $out"; exit 1 }

# ---------- 阶段一：读压测（hey，多实例各持不同 XFF IP） ----------
function Invoke-ReadPhase {
    log "阶段一 读压测: $ReadWorkers 实例 × $ReadConcurrency 并发 × ${ReadSeconds}s，$ReadPages 个页面"
    $urls = 1..$ReadPages | ForEach-Object { "$Base/api/v1/comments?site=$Site&pageId=stress-page-$_&pageSize=20" }
    $jobs = @()
    for ($i = 1; $i -le $ReadWorkers; $i++) {
        $jobs += Start-Job -ArgumentList $heyPath, $ReadConcurrency, $ReadSeconds, "10.0.0.$i", (, $urls) -ScriptBlock {
            param($heyExe, $c, $z, $ip, $urlList)
            $args = @("-c", "$c", "-z", "${z}s", "-H", "X-Forwarded-For: $ip") + $urlList
            & $heyExe @args 2>&1
        }
    }
    Wait-Job $jobs | Receive-Job | Out-File (Join-Path $out "read-summary.txt")
    Remove-Job $jobs -Force | Out-Null
    log "阶段一 完成，各实例 QPS 与状态码分布:"
    Select-String -Path (Join-Path $out "read-summary.txt") -Pattern 'Requests/sec:|Status code distribution:|\[[0-9]{3}\]' | ForEach-Object { "  " + $_.Line }
}

# ---------- 阶段二：写压测（并发 curl，逐请求随机 XFF IP/pageId/content） ----------
# 注意：本环境（trae-sandbox）会剥离命令行参数中的双引号，curl -d $body 内联 JSON 会被破坏
# （服务端收到非法 JSON 返回业务码 1001），导致"HTTP 200 但未落库"的假象。
# 因此写请求体必须先写入临时文件，再以 curl -d "@file" 发送；统计按响应体业务码而非 HTTP 状态码
# （限流/业务错误响应均为 HTTP 200，只有 body 里的 code 才能区分真实结果）。
function Invoke-WritePhase {
    log "阶段二 写压测: $WriteWorkers worker × ${WriteSeconds}s，IP 池 $WriteIpPool（每日 20 条/IP 上限）"
    $ips = 1..$WriteIpPool | ForEach-Object { "10.$((Get-Random -Max 256)).$((Get-Random -Max 256)).$((Get-Random -Min 2 -Max 256))" }
    $jobs = @()
    for ($w = 1; $w -le $WriteWorkers; $w++) {
        $jobs += Start-Job -ArgumentList $w, $WriteSeconds, $Base, $Site, $ReadPages, (, $ips) -ScriptBlock {
            param($w, $secs, $base, $site, $pages, $ipPool)
            $sw = [System.Diagnostics.Stopwatch]::StartNew()
            $ok = 0; $rate = 0; $daily = 0; $dup = 0; $bad = 0; $fail = 0
            $tmp = Join-Path $env:TEMP ("lc-body-$w-{0}.json" -f [guid]::NewGuid().ToString("N").Substring(0, 8))
            while ($sw.Elapsed.TotalSeconds -lt $secs) {
                $ip = $ipPool[(Get-Random -Maximum $ipPool.Count)]
                $page = Get-Random -Minimum 1 -Maximum ($pages + 1)
                $content = "stress-$w-$([guid]::NewGuid().ToString('N').Substring(0, 12))"
                # WriteAllText 默认 UTF-8 无 BOM（BOM 会导致 Go json 解码失败）
                [System.IO.File]::WriteAllText($tmp, "{`"pageId`":`"stress-page-$page`",`"site`":`"$site`",`"nick`":`"st$w`",`"content`":`"$content`"}")
                $resp = & curl.exe -s -m 10 -X POST `
                    -H "Content-Type: application/json" -H "X-Forwarded-For: $ip" `
                    -d "@$tmp" "$base/api/v1/comment"
                if ($resp -match '"code":0') { $ok++ }
                elseif ($resp -match '"code":2001') { $rate++ }
                elseif ($resp -match '"code":2002') { $daily++ }
                elseif ($resp -match '"code":2003') { $dup++ }
                elseif ($resp -match '"code":1') { $bad++ }      # 参数校验失败等业务错误
                else { $fail++ }
            }
            Remove-Item $tmp -Force -ErrorAction SilentlyContinue
            "worker$w ok=$ok rate2001=$rate daily2002=$daily dup2003=$dup bad=$bad fail=$fail"
        }
    }
    Wait-Job $jobs | Receive-Job | Out-File (Join-Path $out "write-summary.txt")
    Remove-Job $jobs -Force | Out-Null
    log "阶段二 完成，各 worker 结果:"
    Get-Content (Join-Path $out "write-summary.txt") | ForEach-Object { "  $_" }
    $tot = Get-Content (Join-Path $out "write-summary.txt") | ForEach-Object {
        if ($_ -match 'ok=(\d+) rate2001=(\d+)') { [pscustomobject]@{ Ok = [int]$matches[1]; Rate = [int]$matches[2] } }
    } | Measure-Object -Property Ok, Rate -Sum
    log ("合计 ok={0} rate2001={1}（限流即正常，说明在打真实限流/排队逻辑）" -f $tot.Sum.Ok, $tot.Sum.Rate)
}

# ---------- 阶段三：读写混合（可选） ----------
function Invoke-MixedPhase {
    log "阶段三 混合压测: ${MixedSeconds}s"
    $urls = 1..5 | ForEach-Object { "$Base/api/v1/comments?site=$Site&pageId=stress-page-$_&pageSize=20" }
    $readJob = Start-Job -ArgumentList $heyPath, $MixedSeconds, $urls -ScriptBlock {
        param($heyExe, $z, $urlList)
        $args = @("-c", "10", "-z", "${z}s", "-H", "X-Forwarded-For: 10.0.1.1") + $urlList
        & $heyExe @args 2>&1
    }
    $ips = 1..30 | ForEach-Object { "10.2.$((Get-Random -Max 256)).$((Get-Random -Min 2 -Max 256))" }
    $wJobs = @()
    for ($w = 1; $w -le 3; $w++) {
        $wJobs += Start-Job -ArgumentList $w, $MixedSeconds, $Base, $Site, (, $ips) -ScriptBlock {
            param($w, $secs, $base, $site, $ipPool)
            $sw = [System.Diagnostics.Stopwatch]::StartNew()
            $tmp = Join-Path $env:TEMP ("lc-mix-$w-{0}.json" -f [guid]::NewGuid().ToString("N").Substring(0, 8))
            while ($sw.Elapsed.TotalSeconds -lt $secs) {
                $ip = $ipPool[(Get-Random -Maximum $ipPool.Count)]
                [System.IO.File]::WriteAllText($tmp, "{`"pageId`":`"stress-page-$((Get-Random -Minimum 1 -Maximum 6))`",`"site`":`"$site`",`"nick`":`"mx$w`",`"content`":`"mix-$w-$([guid]::NewGuid().ToString('N').Substring(0, 8))`"}")
                & curl.exe -s -o NUL -m 10 -X POST -H "Content-Type: application/json" -H "X-Forwarded-For: $ip" -d "@$tmp" "$base/api/v1/comment" | Out-Null
            }
            Remove-Item $tmp -Force -ErrorAction SilentlyContinue
        }
    }
    Wait-Job $readJob | Receive-Job | Out-File (Join-Path $out "mixed-read.txt")
    Wait-Job $wJobs | Out-Null
    Remove-Job $readJob, $wJobs -Force | Out-Null
    log "阶段三 完成"
}

# ---------- 主流程 ----------
log "== dwxComment Windows 压测开始，输出目录: $out =="

$beforeHeap = Get-HeapInuseBytes
$beforeRss = Get-WorkingSetBytes
log "压测前基线: WorkingSet=${beforeRss}B HeapInuse=${beforeHeap}B"
& curl.exe -sf --max-time 5 "$Pprof/debug/pprof/heap" -o (Join-Path $out "heap-before.prof")

Start-Sampler
Invoke-ReadPhase
Invoke-WritePhase
if ($MixedSeconds -gt 0) { Invoke-MixedPhase }
Stop-Sampler

$afterHeap = Get-HeapInuseBytes
$afterRss = Get-WorkingSetBytes
& curl.exe -sf --max-time 5 "$Pprof/debug/pprof/heap" -o (Join-Path $out "heap-after.prof")

log "== 压测结束 =="
log "压测后: WorkingSet=${afterRss}B HeapInuse=${afterHeap}B"
$csv = Join-Path $out "samples.csv"
log "内存采样曲线见 $csv（首/末行）:"
Get-Content $csv | Select-Object -First 2 | ForEach-Object { "  $_" }
Get-Content $csv | Select-Object -Last 2 | ForEach-Object { "  $_" }

$maxWs = (Import-Csv $csv | Measure-Object -Property working_set_bytes -Maximum).Maximum
Write-Host ""
Write-Host "======================================================"
Write-Host " 泄漏判定（判据）"
Write-Host "======================================================"
Write-Host ("  1) 采样峰值 WorkingSet: {0:N0} B（1G 机器建议峰值 < 600MB）" -f $maxWs)
Write-Host ("  2) HeapInuse(字节) 基线->压后: ${beforeHeap} -> ${afterHeap} B")
Write-Host "     （GC 后可回收口径。若压后 >> 基线且长时间不回、单调爬升 = 真泄漏）"
Write-Host "  3) 限流判定：写压测 rate2001 统计的是响应体业务码 2001（HTTP 状态仍为 200）；"
Write-Host "     rate2001>0 说明限流/排队逻辑真实生效；读压测 hey 只按 HTTP 状态统计，天然看不到 2001"
Write-Host "  4) 对比堆快照定位泄漏点:"
Write-Host "     go tool pprof -top $out\heap-before.prof"
Write-Host "     go tool pprof -top $out\heap-after.prof"
Write-Host "  5) 本机观察 OOM/异常退出: 事件查看器 -> Windows 日志 -> 系统 (事件 ID 2004/2013)"
Write-Host "======================================================"
