# ============================================================
# dwxComment 演示数据生成脚本
# 通过 HTTP API 生成一套完整的演示数据：
#   评论 / 回复 / 站长回复 / 置顶 / 点赞 / 待审 / 垃圾评论 / 多页面 / 多站点 / 站点配置
# 前提：服务已运行在 http://localhost:8080，且数据库为全新状态
# 管理员账号：admin / admin123（首个注册自动成为站长）
# 注意：全局限流 5 req/s，脚本内已插入足够间隔；请勿中途手动打断
# ============================================================
$ErrorActionPreference = 'Stop'
$base = 'http://localhost:8080'
$token = $null

function Invoke-Api {
    param(
        [Parameter(Mandatory)][string]$Method,
        [Parameter(Mandatory)][string]$Path,
        $Body = $null,
        [bool]$UseAuth = $false
    )
    $params = @{
        Uri         = $base + $Path
        Method      = $Method
        ContentType = 'application/json; charset=utf-8'
    }
    if ($null -ne $Body) {
        # 关键：PS 5.1 发送字符串 body 时默认非 UTF-8 编码，中文会被替换成 '?'
        # 必须显式转成 UTF-8 字节数组，确保中文正常入库
        $json = $Body | ConvertTo-Json -Compress -Depth 6
        $params.Body = [System.Text.Encoding]::UTF8.GetBytes($json)
    }
    if ($UseAuth) { $params.Headers = @{ Authorization = 'Bearer ' + $token } }
    return Invoke-RestMethod @params
}

# 带业务码校验的调用：code!=0 时抛出错误，避免“静默失败”
function Invoke-Checked {
    param(
        [Parameter(Mandatory)][string]$Method,
        [Parameter(Mandatory)][string]$Path,
        $Body = $null,
        [bool]$UseAuth = $false,
        [string]$Desc = ''
    )
    $resp = Invoke-Api -Method $Method -Path $Path -Body $Body -UseAuth $UseAuth
    if ($resp.code -ne 0) { throw ('[FAIL] ' + $Desc + ' -> code=' + $resp.code + ' msg=' + $resp.msg) }
    return $resp
}

$demoPage = '/post/demo.html'
$goPage   = '/post/golang-tips.html'
$techPage = '/post/hello.html'

# ---------- 1. 注册站长账号 ----------
Write-Host '== [1/9] 注册站长账号 admin / admin123 =='
try {
    $r = Invoke-Api -Method Post -Path '/api/v1/admin/register' -Body @{ username = 'admin'; password = 'admin123' }
    Write-Host "    $($r.msg)"
} catch {
    Write-Host '    账号已存在，跳过注册'
}

# ---------- 2. 登录管理员 ----------
Write-Host '== [2/9] 登录管理员 =='
$login = Invoke-Api -Method Post -Path '/api/v1/admin/login' -Body @{ username = 'admin'; password = 'admin123' }
if (-not $login.data.token) { throw ('登录失败: ' + $login.msg) }
$token = $login.data.token
Write-Host '    登录成功'

# ---------- 3. 提交根评论（阶段一，按时间先后模拟对话） ----------
Write-Host '== [3/9] 提交根评论 =='
$roots = @(
    @{ key = 'c1';  pageId = $demoPage; site = 'default';    nick = '山间清风';   email = 'qingfeng@example.com';    link = '';                    content = '文章写得很棒，条理清晰，尤其是架构图部分，收藏了！' },
    @{ key = 'c2';  pageId = $demoPage; site = 'default';    nick = '前端小张';   email = 'xiaozhang@example.com';   link = 'https://zhang.dev';   content = '请问代码块用的什么高亮方案？主题和页面风格很搭。' },
    @{ key = 'c3';  pageId = $demoPage; site = 'default';    nick = 'Monica';     email = 'monica@example.com';      link = '';                    content = '排版很舒服，暗色模式也好看，已点赞 👍' },
    @{ key = 'c4';  pageId = $demoPage; site = 'default';    nick = '夜航船';     email = 'yehangchuan@example.com'; link = '';                    content = '这个评论系统是开源的么？想了解一下技术栈和部署方式。' },
    @{ key = 'c5';  pageId = $demoPage; site = 'default';    nick = '老王';       email = 'laowang@example.com';     link = '';                    content = '补充一个坑：Windows 下部署记得放行防火墙 8080 端口，不然外网访问超时。' },
    @{ key = 'c6';  pageId = $demoPage; site = 'default';    nick = '路过的猫';   email = 'cat@example.com';         link = '';                    content = 'Markdown 图片可以直接贴链接吗？想发个截图上来。' },
    @{ key = 'c7';  pageId = $demoPage; site = 'default';    nick = '星辰';       email = 'star@example.com';        link = '';                    content = '期待支持更多主题配色，浅色深色都好看。' },
    @{ key = 'p1';  pageId = $demoPage; site = 'default';    nick = '测试员';     email = 'tester@example.com';      link = '';                    content = '这条评论应该出现在管理后台的待审列表里。' },
    @{ key = 'p2';  pageId = $demoPage; site = 'default';    nick = '路人甲';     email = 'passer@example.com';      link = '';                    content = '请问新评论一般多久审核通过？' },
    @{ key = 's1';  pageId = $demoPage; site = 'default';    nick = '广告哥';     email = 'ad@example.com';          link = '';                    content = '加微信 xxx888，兼职日结，稳赚不赔，详情私聊！' },
    @{ key = 'g1';  pageId = $goPage;   site = 'default';    nick = 'Gopher小白'; email = 'gopher@example.com';      link = '';                    content = 'goroutine 泄漏这个坑我也踩过，图解很直观，学到了。' },
    @{ key = 'g2';  pageId = $goPage;   site = 'default';    nick = 'Alex';       email = 'alex@example.com';        link = '';                    content = '建议补充 context 超时控制的实战示例，配合 select 更完整。' },
    @{ key = 't1';  pageId = $techPage; site = 'tech-blog';  nick = '博客园访客'; email = 'guest@example.com';       link = '';                    content = '测试一下多站点隔离：这条评论属于 tech-blog 站点。' },
    @{ key = 't2';  pageId = $techPage; site = 'tech-blog';  nick = '老猫';       email = 'maomao@example.com';      link = '';                    content = '多站点功能很实用，一个服务可以管多个博客。' }
)

$idMap = @{}
foreach ($it in $roots) {
    $resp = Invoke-Checked -Method Post -Path '/api/v1/comment' -Desc ('提交评论 ' + $it.key) -Body @{
        pageId   = $it.pageId
        site     = $it.site
        nick     = $it.nick
        email    = $it.email
        link     = $it.link
        content  = $it.content
        parentId = 0
    }
    $idMap[$it.key] = [int64]$resp.data.id
    Write-Host ("    [{0}] {1} -> id={2}" -f $it.key, $it.nick, $resp.data.id)
    Start-Sleep -Milliseconds 1100   # 错开 create_time + 控制请求频率
}

# ---------- 4. 审核通过根评论（前台展示） ----------
Write-Host '== [4/9] 审核通过根评论 =='
$auditRootKeys = @('c1','c2','c3','c4','c5','c6','c7','g1','g2','t1','t2')
foreach ($k in $auditRootKeys) {
    Invoke-Checked -Method Put -Path ("/api/v1/admin/comment/{0}/audit" -f $idMap[$k]) -UseAuth $true -Desc ('审核 ' + $k) -Body @{ status = 1 }
    Write-Host ("    [{0}] 审核通过" -f $k)
    Start-Sleep -Milliseconds 400
}

# ---------- 5. 提交回复（阶段二，父评论已审核） ----------
Write-Host '== [5/9] 提交回复 =='
$replies = @(
    @{ key = 'r2a'; nick = '小明';      email = 'xiaoming@example.com'; link = ''; content = '同问！高亮是 highlight.js 吗？'; parent = 'c2' },
    @{ key = 'r2b'; nick = '山间清风';  email = 'qingfeng@example.com'; link = ''; content = '实测是 highlight.js，支持 100+ 语言，文章里有说明。'; parent = 'c2' },
    @{ key = 'r4a'; nick = '程序员老K'; email = 'oldk@example.com';     link = ''; content = '看部署文档好像是 Go + SQLite 单文件部署，非常轻量。'; parent = 'c4' }
)
foreach ($it in $replies) {
    $resp = Invoke-Checked -Method Post -Path '/api/v1/comment' -Desc ('提交回复 ' + $it.key) -Body @{
        pageId   = $demoPage
        site     = 'default'
        nick     = $it.nick
        email    = $it.email
        link     = $it.link
        content  = $it.content
        parentId = $idMap[$it.parent]
    }
    $idMap[$it.key] = [int64]$resp.data.id
    Write-Host ("    [{0}] {1} -> id={2}" -f $it.key, $it.nick, $resp.data.id)
    Start-Sleep -Milliseconds 1100
}

# ---------- 6. 审核回复 + 站长回复 ----------
Write-Host '== [6/9] 审核回复 + 站长回复 =='
foreach ($k in @('r2a','r2b','r4a')) {
    Invoke-Checked -Method Put -Path ("/api/v1/admin/comment/{0}/audit" -f $idMap[$k]) -UseAuth $true -Desc ('审核 ' + $k) -Body @{ status = 1 }
    Write-Host ("    [{0}] 审核通过" -f $k)
    Start-Sleep -Milliseconds 400
}
$adminReplies = @(
    @{ parent = 'c1'; content = '谢谢支持！有建议随时反馈，欢迎常来。' },
    @{ parent = 'c2'; content = '用的是 highlight.js，主题定制过，和站点风格保持一致。' },
    @{ parent = 'c4'; content = '目前是个人开源项目，Go + SQLite 单二进制部署，部署文档写得很详细。' },
    @{ parent = 'g1'; content = '感谢反馈，文末已补充 context 超时控制的示例。' },
    @{ parent = 't1'; content = '感谢测试，多站点数据完全隔离，互不影响。' }
)
foreach ($rp in $adminReplies) {
    $resp = Invoke-Checked -Method Post -Path ("/api/v1/admin/comment/{0}/reply" -f $idMap[$rp.parent]) -UseAuth $true -Desc ('站长回复 ' + $rp.parent) -Body @{ content = $rp.content }
    Write-Host ("    站长回复 [{0}] -> id={1}" -f $rp.parent, $resp.data.id)
    Start-Sleep -Milliseconds 400
}

# ---------- 7. 置顶 + 点赞 ----------
Write-Host '== [7/9] 置顶评论 + 点赞 =='
$pinResp = Invoke-Checked -Method Put -Path ("/api/v1/admin/comment/{0}/pin" -f $idMap['c1']) -UseAuth $true -Desc '置顶 c1'
Write-Host ("    c1 已置顶 isPinned={0}" -f $pinResp.data.isPinned)
Start-Sleep -Milliseconds 400
foreach ($lk in @('c1','c3','c5','c6')) {
    $likeResp = Invoke-Checked -Method Post -Path ("/api/v1/comment/{0}/like" -f $idMap[$lk]) -Desc ('点赞 ' + $lk)
    Write-Host ("    [{0}] 点赞 -> likeCount={1}" -f $lk, $likeResp.data.likeCount)
    Start-Sleep -Milliseconds 400
}

# ---------- 8. 站点配置 ----------
Write-Host '== [8/9] 更新站点配置 =='
$prompt = [uri]::EscapeDataString('flat minimalist avatar icon of a friendly site administrator developer mascot, geometric abstract shapes, soft violet gradient background, modern vector illustration, square')
$avatar = 'https://trae-api-cn.mchost.guru/api/ide/v1/text_to_image?prompt=' + $prompt + '&image_size=square'

$resp = Invoke-Checked -Method Put -Path '/api/v1/admin/settings?site=default' -UseAuth $true -Desc '更新 default 配置' -Body @{
    siteName     = 'dwxComment 演示站'
    adminBadge   = '站长'
    adminNick    = '站长'
    adminAvatar  = $avatar
    noticeEmail  = ''
}
Write-Host ("    default 站点配置已更新: {0}" -f $resp.data.siteName)
Start-Sleep -Milliseconds 400
$resp2 = Invoke-Checked -Method Put -Path '/api/v1/admin/settings?site=tech-blog' -UseAuth $true -Desc '更新 tech-blog 配置' -Body @{ siteName = '技术博客' }
Write-Host ("    tech-blog 站点配置已更新: {0}" -f $resp2.data.siteName)
Start-Sleep -Milliseconds 400

# ---------- 9. 结果汇总 ----------
Write-Host '== [9/9] 数据汇总 =='
$list = Invoke-Api -Method Get -Path ('/api/v1/comments?pageId={0}&site=default&pageSize=50' -f $demoPage)
Write-Host ("    演示页 {0} : 根评论 {1} 条" -f $demoPage, $list.data.total)
Start-Sleep -Milliseconds 400
$go = Invoke-Api -Method Get -Path ('/api/v1/comments?pageId={0}&site=default&pageSize=50' -f $goPage)
Write-Host ("    页面 {0} : 根评论 {1} 条" -f $goPage, $go.data.total)
Start-Sleep -Milliseconds 400
$pending = Invoke-Api -Method Get -Path '/api/v1/admin/comments/pending?pageSize=50' -UseAuth $true
Write-Host ("    待审(含垃圾)评论: {0} 条" -f $pending.data.total)
Start-Sleep -Milliseconds 400
$sites = Invoke-Api -Method Get -Path '/api/v1/admin/sites' -UseAuth $true
Write-Host ("    站点列表: {0}" -f ($sites.data.sites -join ', '))

Write-Host ''
Write-Host '完成！预览入口：'
Write-Host '  前台评论组件: http://localhost:8080/comment/comment.html'
Write-Host '  管理后台:     http://localhost:8080/admin/admin.html  (admin / admin123)'
