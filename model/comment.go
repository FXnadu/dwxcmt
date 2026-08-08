package model

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
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
	ID         int64  `json:"id"`
	PageID     string `json:"pageId"`
	Site       string `json:"site"`
	Nick       string `json:"nick"`
	Link       string `json:"link"`
	Content    string `json:"content"`
	AvatarURL  string `json:"avatarUrl,omitempty"`
	ParentID   int64  `json:"parentId"`
	RootID     int64  `json:"rootId"`
	LikeCount  int    `json:"likeCount"`
	IsPinned   int    `json:"isPinned"`
	IsAdmin    int    `json:"isAdmin"`
	CreateTime int64  `json:"createTime"`
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
		AvatarURL:  gravatarURL(c.Email),
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
	if d.AvatarURL != "" {
		m["avatarUrl"] = d.AvatarURL
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

// gravatarURL 有邮箱时返回 Gravatar 头像地址（不暴露邮箱本身）
func gravatarURL(email string) string {
	if email == "" {
		return ""
	}
	lower := strings.ToLower(strings.TrimSpace(email))
	sum := md5.Sum([]byte(lower))
	return "https://www.gravatar.com/avatar/" + hex.EncodeToString(sum[:]) + "?d=404&s=48"
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
