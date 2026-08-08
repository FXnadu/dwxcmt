# dwxComment API 接口文档 v3

> 版本：v3.0 ｜ Base URL：`https://api.example.com`（生产由 Nginx 反代）
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
  "parentId": 0,
  "rootId": 0,
  "likeCount": 3,
  "isPinned": 1,
  "isAdmin": 0,
  "createTime": 1700000000
}
```
> `isAdmin`：站长回复标识（1 = 站长/管理员身份，前台展示「站长」徽章）。
> 隐私字段 `email` / `ip` / `userAgent` 仅出现在管理接口响应中。

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
| `sort` | string | ❌ | `asc` | `asc` / `desc` / `hot`（热度按点赞数） |

只返回 `is_audited=1` 的评论，置顶评论优先。

**响应示例**：
```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "roots": [
      { "id": 1, "pageId": "/post/hello.html", "nick": "张三", "content": "写得太好了！", "parentId": 0, "rootId": 0, "likeCount": 3, "isPinned": 1, "createTime": 1700000000 }
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
{ "code": 0, "msg": "success", "data": { "status": "ok", "version": "1.0.0" } }
```

---

## 3. 管理接口（需 Bearer Token）

> 除登录外，所有管理接口需携带请求头：`Authorization: Bearer <token>`（Header 传递，不用 Cookie，免疫 CSRF）

### 3.1 管理员登录

- **POST** `/api/v1/admin/login`

**请求体**：
```json
{ "username": "admin", "password": "yourpassword" }
```

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "token": "eyJhbGciOiJIUzI1NiIs...", "expiresIn": 86400 } }
```
> 限制：单 IP 每分钟 ≤ 5 次；不做账号锁定（仅依赖 IP 限流）。

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

物理删除（含子回复级联），不做软删除（决策 #4）。

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

### 3.6 获取待审核评论列表

- **GET** `/api/v1/admin/comments/pending?page=1&pageSize=10&site=default`

返回 `is_audited=0`，按创建时间倒序。响应含 `email` / `ip` 等隐私字段。

### 3.7 获取全部评论列表（v2 新增）

- **GET** `/api/v1/admin/comments?page=1&pageSize=10&status=0&keyword=xxx`

支持按审核状态筛选、关键词搜索。

| status | 含义 |
|--------|------|
| 0 | 待审 |
| 1 | 已通过 |
| -1 | 垃圾 |
| 不传 | 全部 |

### 3.8 置顶 / 取消置顶（v2 新增）

- **PUT** `/api/v1/admin/comment/:id/pin` → 设置 `is_pinned=1`
- **PUT** `/api/v1/admin/comment/:id/unpin` → 设置 `is_pinned=0`

同一文章最多置顶 3 条（可配置 `comment.max_pinned_per_page`）。

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "id": 123, "isPinned": 1 } }
```

### 3.9 站长回复评论（v2.4 新增）

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

### 3.10 站点配置

- **PUT** `/api/v1/admin/settings` → 修改 `settings` 表配置项
- **GET** `/api/v1/admin/settings` → 获取所有配置项

**PUT 请求体**（全部选填，只更新传入字段）：
```json
{
  "siteName": "我的博客",
  "noticeEmail": "admin@example.com",
  "notifyNewComment": true,
  "notifyReply": true,
  "adminBadge": "博主",
  "adminAvatar": "https://example.com/me.png",
  "adminNick": "站长"
}
```

> `auditMode` 固定 `"all"`，不可修改。
>
> `adminBadge`：站长徽章文案（默认「站长」，≤10 字），前台站长回复/评论的身份标签。
> `adminAvatar`：站长头像 URL（默认空 = 使用字母头像），前台站长评论优先展示该头像。
> `adminNick`：站长昵称（默认「站长」），站长回复评论显示的作者名。

### 3.11 公开站点配置（v2.4 新增）

- **GET** `/api/v1/site-config?site=default`

公开接口，供前台评论组件读取站长身份展示配置与评论字数上限（无需鉴权）：
```json
{ "code": 0, "msg": "success", "data": { "adminBadge": "博主", "adminAvatar": "https://example.com/me.png", "contentMaxLength": 500 } }
```

> 仅返回前台展示所需字段，不含任何隐私配置。
>
> `contentMaxLength`：评论内容字数上限（对应服务端 `comment.content_max_length`），前台据此渲染输入框 `maxlength` 与字数统计。

### 3.12 站点列表（v2.3 新增）

- **GET** `/api/v1/admin/sites`

返回系统中出现过的全部站点（来自 `comments` 与 `settings` 表去重，始终包含 `default`），管理端用于动态渲染站点切换下拉框。仅做站点枚举，不做站点增删改。

**响应示例**：
```json
{ "code": 0, "msg": "success", "data": { "sites": ["blog1", "default"] } }
```

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

---

## 5. CORS 与限流（生产由 Nginx 兜底）

| 项 | 策略 |
|----|------|
| CORS | 中间件只放行配置的 `cors.allowed_origins`；未配置仅同源；`OPTIONS` 预检返回 204 |
| 全局限流 | Nginx `limit_req`：10r/s + burst 10 |
| 接口限流 | Go 层：单 IP 每秒 ≤ 5 次 |
| 内容限流 | Go 层：单 IP 每日 ≤ 20 条评论 |
| 登录限流 | Go 层：单 IP 每分钟 ≤ 5 次（不做账号锁定） |

> ⚠️ 站点标识统一用请求参数 `site`，**不再用 `X-Site-Id` 头**（决策 #11，防伪造）。

---

## 6. 变更记录

| 版本 | 日期 | 变更 |
|------|------|------|
| v2.0 | 2026-08-05 | 基于《技术设计说明书 v2》拆分；加 /api/v1 前缀、完整错误码表、登出/QQ/置顶/全量列表/批量计数/健康检查接口；移除 X-Site-Id |
| v2.1 | 2026-08-05 | 二次评审修正：删除3006/4002错误码、分页页码从1开始(公开50/管理100)、字段统一camelCase、重复点赞幂等、登录不锁定、临时token10分钟、评论列表两层结构 |
| v2.3 | 2026-08-06 | 新增 GET /api/v1/admin/sites 站点列表接口（§3.12），管理端站点下拉框动态加载 |
| v2.4 | 2026-08-07 | 新增 POST /api/v1/admin/comment/:id/reply 站长回复接口（§3.9）；评论对象新增 isAdmin 字段（站长徽章标识）；新增 GET /api/v1/site-config 公开站点配置接口（§3.11），站点配置新增 adminBadge/adminAvatar/adminNick 字段（站长徽章文案、头像、昵称可自定义） |
| v3.0 | 2026-08-07 | 产品决策：移除图片上传与图床功能；删除接口 POST /api/v1/upload/image；删除错误码 4001/5002/6001~6004；删除请求/响应中的 imageUrl 与 settings 中的 imageHostProvider/imageHostConfig；章节编号由 7 调整为 6 |
