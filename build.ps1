# build.ps1 - dwxComment 一键打包脚本：每次打包自动递增版本号（patch +1）
#
# 版本规则：
#   - VERSION 文件保存当前版本（形如 v1.0.0），随仓库提交，可追溯
#   - 每次打包：VERSION 的 patch 位 +1，并通过 -ldflags 注入
#     dwxcmt/controller.Version，健康检查 /api/v1/health 可验证
#   - 编译失败不写回 VERSION，保证版本号与产物一致
#
# 用法示例：
#   .\build.ps1                            # 本机平台编译，v1.0.0 -> v1.0.1
#   .\build.ps1 -OS linux -Arch amd64      # 交叉编译 Linux amd64
#   .\build.ps1 -OS linux -Arch arm64 -Front   # 先 npm 构建前端再交叉编译
#   .\build.ps1 -Tag                       # 编译成功后自动打 git tag
#   .\build.ps1 -SkipBump                  # 不递增，用当前 VERSION 重新编译
param(
    [string]$OS = "",          # 目标系统，留空 = 当前系统（windows/linux/darwin）
    [string]$Arch = "",        # 目标架构，留空 = 当前架构（amd64/arm64）
    [switch]$Front,            # 先执行 npm run build 构建前端压缩产物
    [switch]$Tag,              # 编译成功后自动打 git tag（需先手动提交 VERSION）
    [switch]$SkipBump          # 跳过版本自增，用当前 VERSION 编译
)

$ErrorActionPreference = "Stop"
$Root = $PSScriptRoot
Set-Location $Root

# ---------- 1. 读取并计算新版本号 ----------
$versionFile = Join-Path $Root "VERSION"
if (-not (Test-Path $versionFile)) {
    Write-Error "缺少 VERSION 文件，请先创建（内容形如 v1.0.0）"
}
$oldVersion = (Get-Content $versionFile -Raw).Trim()
if ($oldVersion -notmatch '^v?(\d+)\.(\d+)\.(\d+)$') {
    Write-Error "VERSION 格式不正确（应为 vX.Y.Z）：$oldVersion"
}

if ($SkipBump) {
    $newVersion = $oldVersion
} else {
    $patch = [int]$Matches[3] + 1
    $newVersion = "v$($Matches[1]).$($Matches[2]).$patch"
}
Write-Host "版本号: $oldVersion -> $newVersion" -ForegroundColor Cyan

# 保存当前 GOOS/GOARCH 以便 finally 恢复
$savedOS = $env:GOOS
$savedArch = $env:GOARCH
$savedCgo = $env:CGO_ENABLED

try {
    # ---------- 2.（可选）构建前端 ----------
    if ($Front) {
        Write-Host "[1/3] 构建前端 (npm run build)..." -ForegroundColor Yellow
        if (-not (Test-Path (Join-Path $Root "package.json"))) {
            Write-Error "缺少 package.json，无法构建前端"
        }
        npm run build
        if ($LASTEXITCODE -ne 0) { Write-Error "前端构建失败" }
    }

    # ---------- 3. 编译后端 ----------
    $targetOS = $OS;    if (-not $targetOS)  { $targetOS = "windows" }
    $targetArch = $Arch; if (-not $targetArch) { $targetArch = "amd64" }

    $ext = ""
    if ($targetOS -eq "windows") { $ext = ".exe" }
    $output = Join-Path $Root ("dwx-comment" + $ext)

    $step = if ($Front) { "[2/3]" } else { "[1/2]" }
    Write-Host "$step 编译 $targetOS/$targetArch ..." -ForegroundColor Yellow

    $env:GOOS = $targetOS
    $env:GOARCH = $targetArch
    $env:CGO_ENABLED = "0"
    go build -trimpath -ldflags="-s -w -X dwxcmt/controller.Version=$newVersion" -o $output main.go
    if ($LASTEXITCODE -ne 0) { Write-Error "编译失败（VERSION 未变更）" }

    # ---------- 4. 编译成功后再写回 VERSION ----------
    if (-not $SkipBump) {
        Set-Content -Path $versionFile -Value $newVersion -NoNewline
        Write-Host "VERSION 已更新为 $newVersion" -ForegroundColor Green
    }

    $step = if ($Front) { "[3/3]" } else { "[2/2]" }
    Write-Host "$step 完成：$output (版本 $newVersion)" -ForegroundColor Green
    Write-Host "健康检查: curl http://localhost:<port>/api/v1/health 应返回 ""version"": ""$newVersion""" -ForegroundColor Green

    # ---------- 5.（可选）打 git tag ----------
    if ($Tag) {
        git tag $newVersion
        if ($LASTEXITCODE -ne 0) { Write-Error "打 tag 失败" }
        Write-Host "已打 tag: $newVersion（注意：VERSION 变更尚未提交，如需 tag 包含新版本号请先 git commit）" -ForegroundColor Green
    }
}
finally {
    $env:GOOS = $savedOS
    $env:GOARCH = $savedArch
    $env:CGO_ENABLED = $savedCgo
}
