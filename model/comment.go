package model

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

// Comment 评论记录，对应 comments 表
// 公开响应见 ToPublicDTO；email/ip/userAgent 仅管理接口可见
type Comment struct {
	ID         int64  `json:"id"`
	PageID     string `json:"pageId"`
	Site       string `json:"site"`
	Nick       string `json:"nick"`
	Email      string `json:"-"`
	Link       string `json:"link"`
	Content    string `json:"content"`
	ParentID   int64  `json:"parentId"`
	RootID     int64  `json:"rootId"`
	LikeCount  int    `json:"likeCount"`
	IsAudited  int    `json:"-"`
	IsPinned   int    `json:"isPinned"`
	IsAdmin    int    `json:"isAdmin"`
	IP         string `json:"-"`
	UserAgent  string `json:"-"`
	CreateTime int64  `json:"createTime"`
	UpdateTime int64  `json:"updateTime"`
}

// CommentDTO 对外响应结构（含管理端隐私字段）
type CommentDTO struct {
	ID         int64    `json:"id"`
	PageID     string   `json:"pageId"`
	Site       string   `json:"site"`
	Nick       string   `json:"nick"`
	Link       string   `json:"link"`
	Content    string   `json:"content"`
	AvatarUrls []string `json:"avatarUrls,omitempty"`
	ParentID   int64    `json:"parentId"`
	RootID     int64    `json:"rootId"`
	LikeCount  int      `json:"likeCount"`
	IsPinned   int      `json:"isPinned"`
	IsAdmin    int      `json:"isAdmin"`
	CreateTime int64    `json:"createTime"`
	// 以下字段仅管理接口返回
	Email     string `json:"email,omitempty"`
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	IsAudited int    `json:"isAudited"`
	// 非导出：标记是否附带管理端字段（由 ToDTO 设置，不参与序列化）
	private bool
}

// ToDTO 转换为响应结构；includePrivate=true 时附带 email/ip/userAgent/isAudited
func (c *Comment) ToDTO(includePrivate bool) CommentDTO {
	dto := CommentDTO{
		ID:         c.ID,
		PageID:     c.PageID,
		Site:       c.Site,
		Nick:       c.Nick,
		Link:       c.Link,
		Content:    c.Content,
		AvatarUrls: avatarCandidates(c.ID, c.Email),
		ParentID:   c.ParentID,
		RootID:     c.RootID,
		LikeCount:  c.LikeCount,
		IsPinned:   c.IsPinned,
		IsAdmin:    c.IsAdmin,
		CreateTime: c.CreateTime,
		IsAudited:  c.IsAudited,
		private:    includePrivate,
	}
	if includePrivate {
		dto.Email = c.Email
		dto.IP = c.IP
		dto.UserAgent = c.UserAgent
	}
	return dto
}

// MarshalJSON 按访问级别输出字段：公开响应不包含管理字段；
// 管理响应（private=true）附带 email/ip/userAgent，且 isAudited 始终返回（0=待审/-1=垃圾/1=通过）
// isAdmin 两个访问级别均返回：前台需据此展示「站长」身份徽章
func (d CommentDTO) MarshalJSON() ([]byte, error) {
	m := map[string]interface{}{
		"id": d.ID, "pageId": d.PageID, "site": d.Site, "nick": d.Nick,
		"link": d.Link, "content": d.Content,
		"parentId": d.ParentID, "rootId": d.RootID,
		"likeCount": d.LikeCount, "isPinned": d.IsPinned,
		"createTime": d.CreateTime,
	}
	if len(d.AvatarUrls) > 0 {
		m["avatarUrls"] = d.AvatarUrls
	}
	if d.IsAdmin != 0 {
		m["isAdmin"] = d.IsAdmin
	}
	if d.private {
		if d.Email != "" {
			m["email"] = d.Email
		}
		if d.IP != "" {
			m["ip"] = d.IP
		}
		if d.UserAgent != "" {
			m["userAgent"] = d.UserAgent
		}
		m["isAudited"] = d.IsAudited
	}
	return json.Marshal(m)
}

// avatarCandidates 有邮箱时返回有序的真实头像候选地址（前端按顺序加载，全部失败再回退字母头像）。
// 只返回第三方服务的查询地址，不暴露邮箱本身。
// 仅使用 Cravatar 国内 CDN；gravatar.com 国内直连慢/常挂起（曾导致页面加载拖慢数秒），已移除。
// QQ 邮箱（@qq.com 且本地为纯数字 QQ 号）不走腾讯 qlogo 直链，而是返回本服务代理地址
// /api/v1/avatars/{id}（由后端代拉图片并磁盘缓存），避免在公开响应中暴露 QQ 号（= 邮箱前缀）。
// 代理地址放最前：命中缓存时毫秒级返回，不再被第三方慢请求串行拖累。
func avatarCandidates(id int64, email string) []string {
	if email == "" {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(email))
	sum := md5.Sum([]byte(lower))
	hash := hex.EncodeToString(sum[:])
	cands := []string{
		"https://cravatar.cn/avatar/" + hash + "?d=404&s=48",
	}
	if qq := QQFromEmail(lower); qq != "" {
		cands = append([]string{"/api/v1/avatars/" + strconv.FormatInt(id, 10)}, cands...)
	}
	return cands
}

// QQFromEmail 提取 QQ 邮箱中的 QQ 号（@qq.com 且本地部分须为纯数字），非 QQ 邮箱返回空串
func QQFromEmail(lower string) string {
	const suffix = "@qq.com"
	if !strings.HasSuffix(lower, suffix) {
		return ""
	}
	qq := strings.TrimSuffix(lower, suffix)
	for _, r := range qq {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return qq
}

// ToPublic 公开响应（隐藏隐私字段）
func (c *Comment) ToPublic() CommentDTO {
	return c.ToDTO(false)
}

// Admin 管理员，对应 admins 表
type Admin struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	Email        string `json:"-"`
	TwoFAEnabled int    `json:"-"` // 是否开启两步验证（1=开启）
	CanDelete    int    `json:"-"` // 是否有删除评论权限（1=是）
	IsApproved   int    `json:"-"` // 是否已通过站长审批（1=是，未通过不可登录）
	IsDisabled   int    `json:"-"` // 是否被禁用（1=是，禁用后不可登录）
	IsOwner      int    `json:"-"` // 是否为站长（首个注册账号，1=是）
	QQOpenID     string `json:"-"`
	QQName       string `json:"-"`
	GitHubOpenID string `json:"-"`
	GitHubName   string `json:"-"`
	CreateTime   int64  `json:"createTime"`
}

// Settings 系统配置，对应 settings 表
type Settings struct {
	ID         int64  `json:"id"`
	Site       string `json:"site"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	UpdateTime int64  `json:"updateTime"`
}

// Like 点赞去重记录，对应 likes 表
type Like struct {
	ID         int64  `json:"id"`
	CommentID  int64  `json:"commentId"`
	IP         string `json:"-"`
	CreateTime int64  `json:"createTime"`
}
