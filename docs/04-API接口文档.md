# dwxComment API 接口文档 v3.1

> 版本：v3.1 ｜ Base URL：`https://api.example.com`（生产由 Nginx 反代）
> 数据格式：JSON ｜ 字符编码：UTF-8 ｜ **API 版本前缀：`/api/v1/`**
> 基于《技术设计说明书 v2》拆分

---

## 1. 通用约定

### 1.1 统一响应格式
```json
{
  "code": 0,
  "msg": "success",
  "data": {}
}
```
| 字段 | 类型 | 说明 |
|------|------|------|
| `code` | int | 0 成功，非 0 失败 |
| `msg` | string | 提示信息 |
| `data` | object/array | 业务数据 |

### 1.2 错误码表（v2 新增完整定义）

| 错误码 | 常量 | 说明 |
|--------|------|------|
| `0` | OK | 成功 |
| `1001` | ErrInvalidParam | 参数校验失败 |
| `1002` | ErrContentInvalid | 评论内容长度不符合要求（1~500 字符） |
| `1003` | ErrNickInvalid | 昵称长度不符合要求（1~20 字符） |
| `1004` | ErrEmailInvalid | 邮箱格式无效 |
| `1005` | ErrURLInvalid | URL 格式无效 |
| `1006` | ErrPageIDRequired | pageId 必填 |
| `2001` | ErrIPRateLimit | IP 频率限制（每秒 5 次） |
| `2002` | ErrDailyLimit | 单 IP 日评论数超限（20 条） |
| `2003` | ErrDuplicateContent | 短时间内重复内容 |
| `3001` | ErrCommentNotFound | 评论不存在 |
| `3002` | ErrLoginFailed | 用户名或密码错误 |
| `3003` | ErrTokenExpired | JWT 已过期 |
| `3004` | ErrTokenInvalid | JWT 无效 |
| `3005` | ErrPermissionDenied | 权限不足 |
| `5001` | ErrInternal | 服务器内部错误 |
| `7001` | ErrOAuthAlreadyBound | 该第三方账号已绑定其他管理员 |
| `7002` | ErrOAuthNotBound | 未绑定该第三方账号 |
| `7003` | ErrSMTPNotConfigured | SMTP 未配置，无法发送验证码邮件 |
| `7004` | ErrEmailCodeInvalid | 验证码错误或已过期 |
| `7005` | ErrEmailCooldown | 发送过于频繁，请稍后再试 |
| `7006` | ErrEmailAlreadyBound | 该邮箱已绑定其他管理员 |
| `7007` | ErrEmailNotBound | 该邮箱未绑定管理员账号 |
| `7008` | ErrEmailDailyLimit | 该邮箱今日发送验证码次数已达上限 |
| `7009` | ErrUsernameTaken | 用户名已存在 |
| `7010` | ErrNotApproved | 账号待站长审批，暂无法登录 |
| `7011` | ErrAccountDisabled | 账号已被禁用，暂无法登录 |

### 1.3 分页约定
- 请求参数：`page`（从 1 开始，默认 1）、`pageSize`（默认 10，公开接口最大 50，管理接口最大 100）
- 响应体：`{ "list": [...], "total": 42, "page": 1, "pageSize": 10, "totalPages": 5 }`

### 1.4 评论对象结构（公开，两层返回 + 前端拼树）
```json
{
  "id": 1,
  "pageId": "/post/hello.html",
  "site": "default",
  "nick": "张三",
  "link": "https://example.com",
  "content": "写得太好了！",
  "avatarUrls": ["https://cravatar.cn/avatar/xxx?d=404&s=48", "https://www.gravatar.com/avatar/xxx?d=404&s=48"],
  "parentId": 0,
  "rootId": 0,
  "likeCount": 3,
  "isPinned": 1,
  "isAdmin": 0,
  "createTime": 1700000000
}
```
> `avatarUrls`：有序的真实头像候选地址数组（**v3.1 替代早期单一 `avatar_url`**）。有邮箱时返回；QQ 邮箱在首位追加本服务代理地址 `/api/v1/avatars/{id}`。前端按序加载，全部失败回退字母头像。
> `isAdmin`：站长回复标识（1 = 站长/管理员身份，前台展示「站长」徽章；仅非 0 时输出该字段）。
> `isPinned`：是否置顶（1=是，置顶区按置顶时间倒序展示）。
> 隐私字段 `email` / `ip` / `userAgent` 仅出现在管理接口响应中；非站长管理员查看时邮箱/IP 脱敏。

---

## 2. 公开接口（无需鉴权）

### 2.1 获取评论列表

- **GET** `/api/v1/comments`

**Query 参数**：

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `pageId` | string | ✅ | - | 文章标识 |
| `site` | string | ❌ | `default` | 站点标识（请求参数，非请求头） |
| `page` | int | ❌ | 1 | 页码（从 1 开始） |
| `pageSize` | int | ❌ | 10 | 每页条数（最大 50） |
| `sort` | string | ❌ | `asc` | `asc`（默认，最新在前）/ `desc`（最旧在前）/ `hot`（按点赞数降序） |

只返回 `is_audited=1` 的评论；置顶区固定按**置顶时间倒序**（后置顶的排最上），与 `sort` 无关；排序末尾追加 `id` 兜底保证并列时间顺序确定。

**响应示例**：
```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "roots": [
      { "id": 1, "pageId": "/post/hello.html", "nick": "张三", "content": "写得太好了！", "avatarUrls": ["https://cravatar.cn/avatar/xxx?d=404&s=48"], "parentId": 0, "rootId": 0, "likeCount": 3, "isPinned": 1, "createTime": 1700000000 }
    ],
    "children": [
      { "id": 2, "nick": "李四", "content": "@张三 同意！", "parentId": 1, "rootId": 1, "isPinned": 0, "createTime": 1700000100 }
    ],
    "total": 1,
    "page": 1,
    "pageSize": 10,
    "totalPages": 1
  }
}
```
> 后端返回两层结构：`roots` 为根评论（含置顶），`children` 为子回复。前端按 `parentId` / `rootId` 拼装为树形，回复层级不限深度，前端默认展开 3 层（决策 #12）。

### 2.2 获取评论总数

- **GET** `/api/v1/comments/count?pageId=xxx&site=default`

仅含已审核评论。

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "count": 42 } }
```

### 2.3 提交评论/回复

- **POST** `/api/v1/comment`

**请求体**：
```json
{
  "pageId": "/post/xxx.html",
  "site": "default",
  "nick": "昵称",
  "email": "mail@example.com",
  "link": "https://example.com",
  "content": "内容（可包含 Markdown 图片语法 ![](url)）",
  "parentId": 0,
  "rootId": 0
}
```

**字段校验规则**：

| 字段 | 规则 |
|------|------|
| `pageId` | 必填，≤ 255 字符 |
| `nick` | 必填，1-20 字符，去首尾空格 |
| `email` | 选填，格式校验 |
| `link` | 选填，URL 格式校验，≤ 200 字符 |
| `content` | 必填，1-500 字符（去空白后），支持 Markdown 图片语法 |
| `parentId` | 选填，默认 0；非 0 时校验父评论存在 |
| `rootId` | 选填，默认 0；跟随父评论的 rootId |

写入时 `is_audited=0`（待审），**不乐观更新到前端列表**。

**响应示例**：
```json
{ "code": 0, "msg": "评论已提交，审核通过后显示", "data": { "id": 123, "audited": false } }
```

### 2.4 评论点赞

- **POST** `/api/v1/comment/:id/like`

通过 IP + `likes` 表 24h 去重，点赞数原子递增。**不提供取消赞**（决策 #8）。

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "likeCount": 4 } }
```
> 重复点赞幂等返回 `code=0`，点赞数不变（不报错）。

### 2.5 健康检查（v2 新增）

- **GET** `/api/v1/health`

用于 systemd 监控、负载均衡存活性检测。

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "status": "ok", "version": "1.0.1" } }
```

### 2.6 QQ 头像代理（v2.5 新增）

- **GET** `/api/v1/avatars/{id}`

QQ 邮箱评论者的头像代理接口：公开响应中不暴露 QQ 号（= 邮箱前缀），由服务端代拉腾讯 qlogo 图片并本地磁盘缓存（24h）。

- 命中缓存：毫秒级返回；失败返回 404（前端回退到下一候选头像）
- 响应头：`Content-Type`、`Cache-Control: public, max-age=86400`、`X-Content-Type-Options: nosniff`

---

## 3. 管理接口（需 Bearer Token）

> 除登录外，所有管理接口需携带请求头：`Authorization: Bearer <token>`（Header 传递，不用 Cookie，免疫 CSRF）

### 3.1 管理员注册 / 登录

- **POST** `/api/v1/admin/register`

**请求体**：
```json
{ "username": "admin", "password": "yourpassword" }
```
> 首个注册账号自动成为**站长**（可直接登录）；后续注册账号进入待审批（`is_approved=0`），需站长在「账号管理」审批通过后方可登录。注册接口不再关闭。

- **POST** `/api/v1/admin/login`

**请求体**：
```json
{ "username": "admin", "password": "yourpassword" }
```

**响应示例（未开启 2FA）**：
```json
{ "code": 0, "msg": "success", "data": { "token": "eyJhbGciOiJIUzI1NiIs...", "expiresIn": 86400 } }
```

**响应示例（已开启 2FA）**：返回预授权凭证，验证码已自动发送到绑定邮箱
```json
{ "code": 0, "msg": "success", "data": { "need2FA": true, "preAuthToken": "eyJhbGciOiJIUzI1NiIs...", "maskedEmail": "ad***@example.com" } }
```

- **POST** `/api/v1/admin/login/2fa`：校验 `{ "preAuthToken": "...", "code": "123456" }`，通过后签发正式 JWT（返回 `{ token, expiresIn }`）

> 限制：单 IP 每分钟 ≤ 5 次；不做账号锁定（仅依赖 IP 限流）。
> 密码错误/待审批（7010）/被禁用（7011）均登录失败。

### 3.2 管理员登出（v2 新增）

- **POST** `/api/v1/admin/logout`

将当前 token 加入内存黑名单（24h 过期自动清理）。

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": {} }
```

### 3.3 QQ 绑定回调（v2 新增）

- **GET** `/api/v1/admin/qq/callback?code=xxx&state=xxx` → 个人中心发起 QQ 绑定，`state` 为绑定凭证 JWT，绑定成功后回调页 postMessage 通知弹窗父窗口

> 需在 `config.yaml` 配置：`qq_oauth.app_id` / `app_key` / `redirect_uri`

### 3.4 删除评论

- **DELETE** `/api/v1/admin/comment/:id`

物理删除（含子回复级联），不做软删除（决策 #4）。**仅站长或站长授予删除权限的管理员可执行**（无权限返回 3005）。

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "deleted": 5 } }
```

### 3.5 审核评论

- **PUT** `/api/v1/admin/comment/:id/audit`

**请求体**：
```json
{ "status": 1 }
```
| status | 含义 |
|--------|------|
| 1 | 通过（前台展示） |
| -1 | 标记垃圾（前台不展示） |

审核通过时清除对应文章的所有缓存分页。

### 3.6 去除评论链接（v3.2 新增）

- **PUT** `/api/v1/admin/comment/:id/link`

保留评论本身，仅清空其网站链接（`link` 置空）。用于链接不适合展示（如未备案、敏感站点）但评论内容需要保留的场景；清除后前台该评论昵称不再渲染为外链。

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "id": 123 } }
```

> 链接清空后不可恢复，前端操作前有二次确认；清除会同步失效对应文章缓存，前台即时生效。

### 3.7 获取待审核评论列表

- **GET** `/api/v1/admin/comments/pending?page=1&pageSize=10&site=default`

返回 `is_audited=0`，按创建时间倒序。响应含 `email` / `ip` 等隐私字段；非站长管理员查看时邮箱/IP 脱敏。

### 3.8 获取全部评论列表（v2 新增，v3.1 支持排序）

- **GET** `/api/v1/admin/comments?page=1&pageSize=10&status=0&keyword=xxx&site=default&sort=newest`

支持按审核状态筛选、关键词搜索、站点过滤与排序。

| 参数 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `status` | int | ❌ | 不传=全部 | 0 待审 / 1 已通过 / -1 垃圾；非法值拒绝（1001） |
| `keyword` | string | ❌ | - | 模糊匹配昵称 / 内容 / 邮箱 |
| `site` | string | ❌ | 不传或 `all` = 全部站点 | 站点过滤 |
| `sort` | string | ❌ | `newest` | `newest`（最新在前，默认）/ `oldest`（最早在前）/ `hot`（点赞最多）；非法值拒绝（1001） |

> 返回含隐私字段；非站长管理员查看时邮箱/IP 脱敏。
> 管理端「后台回复」筛选使用 **GET `/api/v1/admin/comments/replied`**（v3.1 新增），筛选 `is_admin=1` 的站长回复，参数与排序同上。

### 3.9 置顶 / 取消置顶（v2 新增）

- **PUT** `/api/v1/admin/comment/:id/pin` → 设置 `is_pinned=1`
- **PUT** `/api/v1/admin/comment/:id/unpin` → 设置 `is_pinned=0`

同一文章最多置顶 3 条（可配置 `comment.max_pinned_per_page`）。

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "id": 123, "isPinned": 1 } }
```

### 3.10 站长回复评论（v2.4 新增）

- **POST** `/api/v1/admin/comment/:id/reply`

以站长身份回复任意已存在评论，回复以**站点配置的站长昵称（`adminNick`，默认「站长」）**为作者、标记 `is_admin=1`（前台显示站长徽章）、**直接已审核即时可见**，并自动挂载到目标评论下（`parent_id`/`root_id` 推导）。

**请求体**：
```json
{ "content": "感谢反馈，已修复！" }
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `content` | string | ✅ | 1~500 字，与公开评论同一长度上限 |

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "id": 205 } }
```

> 同时异步触发被回复者邮件通知（未配置 SMTP 时静默）；回复后清除对应文章缓存分页。

### 3.11 站点配置

- **PUT** `/api/v1/admin/settings` → 修改 `settings` 表配置项
- **GET** `/api/v1/admin/settings` → 获取所有配置项

**PUT 请求体**（全部选填，只更新传入字段）：
```json
{
  "siteName": "我的博客",
  "siteUrl": "https://blog.example.com",
  "noticeEmail": "admin@example.com",
  "notifyNewComment": true,
  "notifyReply": true,
  "adminBadge": "博主",
  "adminAvatar": "https://example.com/me.png",
  "adminNick": "站长",
  "pagerType": "more"
}
```

> `auditMode` 固定 `"all"`，不可修改。
>
> `siteUrl`：站点地址（v3.1 新增），用于邮件通知中的评论链接跳转锚点。
> `pagerType`：评论区翻页方式（v3.1 新增），`more`（加载更多，默认）/ `pages`（页码分页）。
> `adminBadge`：站长徽章文案（默认「站长」，≤10 字），前台站长回复/评论的身份标签。
> `adminAvatar`：站长头像 URL（默认空 = 使用字母头像），前台站长评论优先展示该头像。
> `adminNick`：站长昵称（默认「站长」），站长回复评论显示的作者名。

### 3.12 公开站点配置（v2.4 新增，v3.1 补充字段）

- **GET** `/api/v1/site-config?site=default`

公开接口，供前台评论组件读取站长身份展示配置、评论字数上限与翻页方式（无需鉴权）：
```json
{ "code": 0, "msg": "success", "data": { "adminBadge": "博主", "adminAvatar": "https://example.com/me.png", "adminNick": "站长", "contentMaxLength": 500, "pagerType": "more" } }
```

> 仅返回前台展示所需字段，不含任何隐私配置。
>
> `adminNick`：站长昵称（前台站长评论显示的作者名）。
> `contentMaxLength`：评论内容字数上限（对应服务端 `comment.content_max_length`），前台据此渲染输入框 `maxlength` 与字数统计。
> `pagerType`：评论区翻页方式（`more` / `pages`），前台据此渲染「加载更多」或页码分页器。

### 3.12 站点列表（v2.3 新增）

- **GET** `/api/v1/admin/sites`

返回系统中出现过的全部站点（来自 `comments` 与 `settings` 表去重，始终包含 `default`），管理端用于动态渲染站点切换下拉框。仅做站点枚举，不做站点增删改。

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "sites": ["blog1", "default"] } }
```

### 3.13 账号管理（站长审批 / 权限，v3.1 新增，仅站长可操作）

| 接口 | 方法 | 说明 |
|------|------|------|
| `GET /api/v1/admin/accounts` | GET | 全部管理员账号列表（含审批/权限/禁用状态） |
| `POST /api/v1/admin/accounts/{id}/approve` | POST | 审批通过新注册账号 |
| `PUT /api/v1/admin/accounts/{id}/delete-permission` | PUT | 授予/收回删除权限，请求体 `{ "canDelete": true }` |
| `PUT /api/v1/admin/accounts/{id}/disabled` | PUT | 禁用/启用账号，请求体 `{ "disabled": true }` |
| `DELETE /api/v1/admin/accounts/{id}` | DELETE | 删除指定管理员账号 |

> 非站长调用返回 3005（权限不足）。

### 3.14 批量审核 / 批量删除（v3.1 新增）

- **PUT** `/api/v1/admin/comments/batch-audit`

**请求体**：`{ "ids": [1, 2, 3], "status": 1 }`（status：1 通过 / -1 垃圾）。自动去重并跳过不存在的 ID，返回 `{ "affected": n }`。

- **POST** `/api/v1/admin/comments/batch-delete`

**请求体**：`{ "ids": [1, 2, 3] }`。物理删除（根评论级联删除回复），返回 `{ "deleted": n }`。权限要求同单条删除（3005）。

### 3.16 管理员个人中心（v3.1 新增）

- **GET** `/api/v1/admin/profile`

返回当前管理员资料（用户名、绑定邮箱脱敏、绑定状态、站长标识、权限、2FA 状态等）。

### 3.17 OAuth 绑定（QQ / GitHub，v3.1 新增）

| 接口 | 方法 | 说明 |
|------|------|------|
| `POST /api/v1/admin/oauth/{provider}/start` | POST | 发起绑定，`provider` = `qq` / `github`，返回 `{ "authUrl": "..." }` 前端跳转 |
| `DELETE /api/v1/admin/oauth/{provider}` | DELETE | 解除绑定 |
| `GET /api/v1/admin/qq/callback?code=xxx&state=xxx` | GET | QQ 回调（`state` 为绑定凭证 JWT，成功后 postMessage 通知弹窗） |
| `GET /api/v1/admin/github/callback?code=xxx&state=xxx` | GET | GitHub 回调 |

> OAuth 绑定需在 `config.yaml` 配置 `qq_oauth.*` / `github_oauth.*`；未配置时返回 1001 并提示。

### 3.18 两步验证（2FA，v3.1 新增）

| 接口 | 方法 | 说明 |
|------|------|------|
| `POST /api/v1/admin/2fa/enable` | POST | 开启邮箱验证码两步验证（前置：已绑定邮箱且 SMTP 可用） |
| `POST /api/v1/admin/2fa/disable` | POST | 关闭两步验证 |

> 开启后，密码登录需先通过 `/api/v1/admin/login` 获取预授权凭证，再调用 `/api/v1/admin/login/2fa` 完成登录。

### 3.19 邮箱验证码登录 / 绑定（v3.1 新增）

| 接口 | 方法 | 说明 |
|------|------|------|
| `POST /api/v1/admin/email/send-code` | POST | 发送验证码（登录/绑定通用），请求体 `{ "email": "xxx" }`；10 分钟有效，带发送冷却与每日上限 |
| `POST /api/v1/admin/email/login` | POST | 邮箱验证码登录，请求体 `{ "email": "xxx", "code": "123456" }`，返回 `{ token, expiresIn }` |
| `POST /api/v1/admin/email/bind-send-code` | POST | 个人中心绑定邮箱时发码（需 JWT，预检邮箱唯一性） |
| `POST /api/v1/admin/email/bind` | POST | 校验验证码后绑定邮箱（需 JWT） |
| `DELETE /api/v1/admin/email` | DELETE | 解除邮箱绑定（需 JWT） |

> 邮箱未配置 SMTP 返回 7003；验证码错误/过期返回 7004；绑定邮箱冲突返回 7006。

---

## 4. 数据迁移接口（管理员）

### 4.1 数据导出

- **GET** `/api/v1/admin/export?site=xxx&start_date=xxx&end_date=xxx`

支持按站点、时间范围过滤导出，返回 JSON（文件流）：
```
Content-Disposition: attachment; filename="dwx-comment-export-20260805.json"
```

### 4.2 数据导入

- **POST** `/api/v1/admin/migrate`

`multipart/form-data` 上传 JSON 文件，需附带 `source` 字段声明来源（`waline` / `twikoo` / `disqus`），后端按映射表转换。

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "imported": 120, "skipped": 3 } }
```

### 4.3 一键备份（v3.1 新增）

- **GET** `/api/v1/admin/backup`

服务端执行 `PRAGMA wal_checkpoint(TRUNCATE)` 后直接下载 `comment.db` 文件快照（文件流下载）。

```
Content-Disposition: attachment; filename="dwx-comment-backup-20260810.db"
```

---

## 5. CORS 与限流（生产由 Nginx 兜底）

| 项 | 策略 |
|----|------|
| CORS | 中间件只放行配置的 `cors.allowed_origins`；未配置仅同源；`OPTIONS` 预检返回 204 |
| 全局限流 | Nginx `limit_req`：10r/s + burst 10 |
| 接口限流 | Go 层：单 IP 每秒 ≤ 5 次 |
| 内容限流 | Go 层：单 IP 每日 ≤ 20 条评论 |
| 登录限流 | Go 层：单 IP 每分钟 ≤ 5 次（不做账号锁定） |
| 验证码限流 | Go 层：发送冷却 + 每邮箱每日发送上限（防邮件轰炸，见 7005/7008） |

> ⚠️ 站点标识统一用请求参数 `site`，**不再用 `X-Site-Id` 头**（决策 #11，防伪造）。

---

## 6. 变更记录

| 版本 | 日期 | 变更 |
|------|------|------|
| v2.0 | 2026-08-05 | 基于《技术设计说明书 v2》拆分；加 /api/v1 前缀、完整错误码表、登出/QQ/置顶/全量列表/批量计数/健康检查接口；移除 X-Site-Id |
| v2.1 | 2026-08-05 | 二次评审修正：删除3006/4002错误码、分页页码从1开始(公开50/管理100)、字段统一camelCase、重复点赞幂等、登录不锁定、临时token10分钟、评论列表两层结构 |
| v2.3 | 2026-08-06 | 新增 GET /api/v1/admin/sites 站点列表接口（§3.12），管理端站点下拉框动态加载 |
| v2.4 | 2026-08-07 | 新增 POST /api/v1/admin/comment/:id/reply 站长回复接口（§3.10）；评论对象新增 isAdmin 字段（站长徽章标识）；新增 GET /api/v1/site-config 公开站点配置接口（§3.12），站点配置新增 adminBadge/adminAvatar/adminNick 字段（站长徽章文案、头像、昵称可自定义） |
| v3.0 | 2026-08-07 | 产品决策：移除图片上传与图床功能；删除接口 POST /api/v1/upload/image；删除错误码 4001/5002/6001~6004；删除请求/响应中的 imageUrl 与 settings 中的 imageHostProvider/imageHostConfig；章节编号由 7 调整为 6 |
| v3.1 | 2026-08-10 | 同步至 v1.0.1 实现：评论对象 `avatarUrls`（候选数组，QQ 走代理）替代早期 avatar_url；公开列表 `sort` 语义明确（asc 最新在前默认 / desc / hot），置顶区按置顶时间倒序；管理端列表支持 `sort=newest/oldest/hot` 与已通过/垃圾 tab 关键词搜索；新增接口：注册（多管理员+站长审批）、2FA 登录、批量审核/删除、后台回复列表 `/admin/comments/replied`、账号管理、个人中心、OAuth 绑定（QQ/GitHub）、邮箱验证码登录/绑定、头像代理 `/avatars/{id}`、一键备份 `/admin/backup`；站点配置补 `siteUrl`/`pagerType`；错误码补 7001~7011 |
| v3.2 | 2026-08-12 | 新增 PUT /api/v1/admin/comment/:id/link 去除评论网站链接接口（§3.6）：保留评论本身仅清空 link，前台昵称不再渲染为外链，同步失效对应文章缓存 |
