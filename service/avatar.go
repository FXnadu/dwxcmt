package service

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dwxcmt/model"
)

// avatarDir 头像磁盘缓存目录（相对工作目录）。
// 图片几 KB 级，按评论 ID 落盘；命中直接读文件，不查库、不回源腾讯，对低配服务器友好。
var avatarDir = filepath.Join("data", "avatars")

// avatarFailTTL 失败标记的有效时长：超时后允许重试回源腾讯
const avatarFailTTL = 24 * time.Hour

// avatarExts 可能的图片扩展名（对应 qlogo 可能返回的 Content-Type）
var avatarExts = []string{".jpg", ".png", ".gif", ".webp"}

// QQAvatar 服务端代理 QQ 头像（磁盘缓存版）：
// 首次按评论 ID 反查邮箱并向腾讯 qlogo 拉图，成功后写入 data/avatars/{id}.{ext}；
// 之后直接读缓存文件返回，不再查库、不再回源腾讯。
// 评论不存在 / 非 QQ 邮箱 / 腾讯无头像时写入 {id}.none 失败标记（24h 后自动重试）。
func (s *Service) QQAvatar(commentID int64) ([]byte, string, error) {
	if data, contentType, ok := readAvatarCache(commentID); ok {
		return data, contentType, nil
	}
	if isAvatarFailed(commentID) {
		return nil, "", model.ErrNotFound
	}

	// 缓存未命中：查库拿邮箱 → 拉取腾讯头像
	c, err := s.GetComment(commentID)
	if err != nil {
		markAvatarFailed(commentID)
		return nil, "", model.ErrNotFound
	}
	if qq := model.QQFromEmail(strings.ToLower(strings.TrimSpace(c.Email))); qq != "" {
		if data, contentType, ferr := fetchQQAvatar(qq); ferr == nil {
			writeAvatarCache(commentID, data, contentType) // 写盘失败不影响本次返回
			return data, contentType, nil
		}
	}
	markAvatarFailed(commentID)
	return nil, "", model.ErrNotFound
}

// fetchQQAvatar 请求腾讯 qlogo 头像接口，返回图片字节与 Content-Type
func fetchQQAvatar(qq string) ([]byte, string, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "https://q1.qlogo.cn/g?b=qq&nk="+qq+"&s=100", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "dwxcmt/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", model.ErrNotFound
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10)) // 512KB 上限，防超大响应
	if err != nil {
		return nil, "", err
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" || !strings.HasPrefix(contentType, "image/") {
		contentType = "image/jpeg"
	}
	return data, contentType, nil
}

// readAvatarCache 读磁盘缓存；命中返回图片数据与 Content-Type
func readAvatarCache(id int64) ([]byte, string, bool) {
	for _, ext := range avatarExts {
		if data, err := os.ReadFile(avatarFilePath(id, ext)); err == nil {
			return data, extToContentType(ext), true
		}
	}
	return nil, "", false
}

// isAvatarFailed 判断是否存在未过期的失败标记（24h 内不重复回源腾讯）
func isAvatarFailed(id int64) bool {
	info, err := os.Stat(avatarFilePath(id, ".none"))
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < avatarFailTTL
}

// markAvatarFailed 写入失败标记（写失败忽略，下次请求会重试）
func markAvatarFailed(id int64) {
	_ = os.WriteFile(avatarFilePath(id, ".none"), nil, 0o644)
}

// writeAvatarCache 把图片落盘（写失败忽略，仅损失本次缓存机会）
func writeAvatarCache(id int64, data []byte, contentType string) {
	_ = os.MkdirAll(avatarDir, 0o755)
	_ = os.WriteFile(avatarFilePath(id, contentTypeToExt(contentType)), data, 0o644)
}

func avatarFilePath(id int64, ext string) string {
	return filepath.Join(avatarDir, strconv.FormatInt(id, 10)+ext)
}

// contentTypeToExt 由上游 Content-Type 推断扩展名（qlogo 常见 jpg/png）
func contentTypeToExt(ct string) string {
	switch {
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "gif"):
		return ".gif"
	case strings.Contains(ct, "webp"):
		return ".webp"
	default:
		return ".jpg"
	}
}

func extToContentType(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}
