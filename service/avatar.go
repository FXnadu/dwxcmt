package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"dwxcmt/model"
)

// avatarDir 头像磁盘缓存目录（相对工作目录）。
// 缓存文件与失败标记均以邮箱 md5 命名：同一邮箱的多条评论共享一份缓存，
// 回源量从「评论数」降到「邮箱数」，删除单条评论也不会产生孤儿文件。
var avatarDir = filepath.Join("data", "avatars")

// avatarCacheTTL 成功头像缓存默认有效期；可通过 comment.avatar_cache_ttl（秒）覆盖。
// 惰性过期：读时检查文件修改时间，超过 TTL 视为 miss 并回源覆盖同一文件；
// 物理文件由 StartAvatarCleaner 启动时 + 定时清理回收（见 cleanAvatarCache）。
const avatarCacheTTL = 7 * 24 * time.Hour

// avatarFailTTL 失败标记的有效时长：超时后允许重试回源（覆盖「无 → 新注册头像」场景）
const avatarFailTTL = 24 * time.Hour

// avatarExts 可能的图片扩展名（对应上游可能返回的 Content-Type）
var avatarExts = []string{".jpg", ".png", ".gif", ".webp"}

// 缓存目录内文件命名校验（清理时区分合法文件与孤儿/残留）
var (
	// avatarFileRE 图片缓存：{邮箱md5}.{ext}
	avatarFileRE = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png|gif|webp)$`)
	// avatarMarkerRE 失败标记：{邮箱md5}.none / .fail
	avatarMarkerRE = regexp.MustCompile(`^[0-9a-f]{32}\.(none|fail)$`)
	// avatarTmpRE 原子写临时文件：{邮箱md5}-{随机数}.tmp
	avatarTmpRE = regexp.MustCompile(`^[0-9a-f]{32}-[0-9]+\.tmp$`)
)

// errAvatarNotFound 上游返回 404：确定性无头像结果，区别于网络错误/5xx 等暂时性故障。
// Avatar 据此决定不 stale 兜底（头像被删除应立即回退字母头像）。
var errAvatarNotFound = errors.New("avatar not found: upstream 404")

// Avatar 服务端头像代理（磁盘缓存版）：
// 按评论 ID 反查邮箱 → 按邮箱类型分流回源（QQ → qlogo，其他 → Cravatar）→ 落盘缓存。
// 缓存键为邮箱 md5，命中直接读文件返回；成功缓存惰性 TTL 过期后回源覆盖。
// 评论不存在 / 无邮箱时返回 ErrNotFound 且不落盘（防 ID 遍历放大磁盘文件）。
// 上游 404（确定性无头像）写 {md5}.none：直接返回 404，不 stale 兜底；
// 网络错误/上游故障写 {md5}.fail：降级返回磁盘上已过期的缓存（stale-if-error），仅无历史缓存时 404。
// 两种标记均 24h 自动过期重试；回源成功后统一清理。
func (s *Service) Avatar(commentID int64) ([]byte, string, error) {
	// 评论ID→邮箱 映射走内存缓存（LRU，60s）：磁盘缓存命中的高频路径不再每次查库。
	// 邮箱在评论生命周期内不可变，60s 过期仅使新访问者的首次请求多一次主键点查。
	email, ok := s.avatarEmailCache(commentID)
	if !ok {
		c, err := s.GetComment(commentID)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				return nil, "", model.ErrNotFound
			}
			// 数据库等真实故障：记日志并透传（controller 返回 500），
			// 避免静默降级为 404、误判「该用户无头像」且无从排查
			log.Printf("[avatar] 读取评论 %d 失败: %v", commentID, err)
			return nil, "", err
		}
		email = strings.ToLower(strings.TrimSpace(c.Email))
		if email == "" {
			return nil, "", model.ErrNotFound
		}
		s.Cache.Set(avatarEmailKey(commentID), email)
	}
	hash := emailHash(email)
	ttl := s.avatarCacheTTL()

	// 新鲜缓存命中
	if data, contentType, ok := readAvatarCache(hash, ttl); ok {
		return data, contentType, nil
	}
	// 确定性无头像标记（.none）：直接 404，不 stale 兜底
	if isAvatarNotFound(hash) {
		return nil, "", model.ErrNotFound
	}
	// 网络故障标记（.fail）：stale-if-error 兜底返回过期缓存
	if isAvatarFetchFailed(hash) {
		if data, contentType, ok := readAvatarFile(hash); ok {
			return data, contentType, nil
		}
		return nil, "", model.ErrNotFound
	}

	// 缓存未命中：按邮箱类型回源
	var (
		data        []byte
		contentType string
		err         error
	)
	if qq := model.QQFromEmail(email); qq != "" {
		// qlogo 官方接口仅支持 b/nk/s 参数，不支持 d=404：
		// 不存在的 QQ 号返回 200 + 灰色默认轮廓图，故 QQ 邮箱无「无头像 → 404 → 字母头像」
		// 语义（拿到默认图而非 404），属上游行为，前端仅对非 200 回退字母头像
		data, contentType, err = s.fetch("https://q1.qlogo.cn/g?b=qq&nk=" + qq + "&s=100")
	} else {
		data, contentType, err = s.fetch("https://cravatar.cn/avatar/" + hash + "?d=404&s=48")
	}
	if err != nil {
		if errors.Is(err, errAvatarNotFound) {
			// 上游确认无头像：写 .none 标记（24h 内不重复回源），直接 404，不 stale 兜底
			markAvatarNotFound(hash)
			return nil, "", model.ErrNotFound
		}
		// 网络错误/上游故障：写 .fail 标记（24h 内不重复回源），stale 兜底。
		// 无历史缓存的新邮箱会持续 404 至标记过期（最长 24h），前端回退字母头像；
		// 这是防回源风暴的有意取舍（见 TestAvatar_UpstreamError_NoCache）。
		markAvatarFetchFailed(hash)
		if data, contentType, ok := readAvatarFile(hash); ok {
			return data, contentType, nil
		}
		return nil, "", model.ErrNotFound
	}
	// 回源成功：写缓存并清理失败标记，避免残留干扰后续语义
	writeAvatarCache(hash, data, contentType)
	clearAvatarFailures(hash)
	return data, contentType, nil
}

// fetch 头像回源入口；测试可注入 s.avatarFetch 替换，避免依赖外网
func (s *Service) fetch(url string) ([]byte, string, error) {
	if s.avatarFetch != nil {
		return s.avatarFetch(url)
	}
	return fetchRemote(url)
}

// emailHash 邮箱 md5（缓存文件与失败标记的键）
func emailHash(email string) string {
	sum := md5.Sum([]byte(email))
	return hex.EncodeToString(sum[:])
}

// avatarEmailKey 评论ID→邮箱 内存映射的缓存键
func avatarEmailKey(id int64) string {
	return "avatar:email:" + strconv.FormatInt(id, 10)
}

// avatarEmailCache 读 评论ID→邮箱 内存映射；未命中或类型异常返回 false。
// 邮箱仅用于计算缓存键与 QQ 分流，60s 过期后由 Avatar 重新查库补录。
func (s *Service) avatarEmailCache(id int64) (string, bool) {
	v, ok := s.Cache.Get(avatarEmailKey(id))
	if !ok {
		return "", false
	}
	email, ok := v.(string)
	return email, ok
}

// avatarCacheTTL 成功头像缓存有效期；配置为 0 或负时使用默认 7 天
func (s *Service) avatarCacheTTL() time.Duration {
	if s.Cfg.Comment.AvatarCacheTTL <= 0 {
		return avatarCacheTTL
	}
	return time.Duration(s.Cfg.Comment.AvatarCacheTTL) * time.Second
}

// fetchRemote 拉取远端头像图片，返回图片字节与 Content-Type（8s 超时、512KB 上限）。
// 404 返回 errAvatarNotFound（确定性无头像）；其他非 200（5xx 等）与网络错误返回普通 error（暂时性故障）。
func fetchRemote(url string) ([]byte, string, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "dwxcmt/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", errAvatarNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("avatar upstream status %d", resp.StatusCode)
	}
	// 512KB 上限，读 limit+1 字节以检测超限：截断/空 body 视为上游故障（走 .fail 语义），
	// 避免把坏图当成功缓存对外服务 TTL 之久
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > 512<<10 {
		return nil, "", fmt.Errorf("avatar upstream body exceeds 512KB")
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("avatar upstream empty body")
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" || !strings.HasPrefix(contentType, "image/") {
		contentType = "image/jpeg"
	}
	return data, contentType, nil
}

// readAvatarCache 读磁盘缓存；命中返回图片数据与 Content-Type。
// 惰性 TTL：先 stat 检查文件修改时间，超过 ttl 视为 miss（由调用方回源覆盖同一文件），
// 新鲜才读取文件内容，避免把已过期文件整块读入内存再丢弃。
func readAvatarCache(hash string, ttl time.Duration) ([]byte, string, bool) {
	for _, ext := range avatarExts {
		path := avatarFilePath(hash, ext)
		info, err := os.Stat(path)
		if err != nil {
			continue // 该扩展名文件不存在
		}
		if time.Since(info.ModTime()) > ttl {
			continue // 超过 TTL 视为 miss，回源覆盖
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue // 文件被并发删除/替换，跳过该扩展名
		}
		return data, extToContentType(ext), true
	}
	return nil, "", false
}

// readAvatarFile 读取任意存在的缓存文件（不检查 TTL），供上游故障时的 stale-if-error 降级兜底。
func readAvatarFile(hash string) ([]byte, string, bool) {
	for _, ext := range avatarExts {
		if data, err := os.ReadFile(avatarFilePath(hash, ext)); err == nil {
			return data, extToContentType(ext), true
		}
	}
	return nil, "", false
}

// isAvatarNotFound 判断是否存在未过期的「无头像」标记（.none，24h 内不重复回源）。
// 该标记表示上游确定性 404：命中时直接 404，不做 stale 兜底（头像被删除应立即反映）。
func isAvatarNotFound(hash string) bool {
	info, err := os.Stat(avatarFilePath(hash, ".none"))
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < avatarFailTTL
}

// markAvatarNotFound 写入「无头像」标记（写失败忽略，下次请求会重试）。
// 先确保目录存在：否则标记静默写失败，「24h 内不重复回源」保证在新部署（目录未创建）时失效。
func markAvatarNotFound(hash string) {
	if err := os.MkdirAll(avatarDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(avatarFilePath(hash, ".none"), nil, 0o644)
}

// isAvatarFetchFailed 判断是否存在未过期的「网络故障」标记（.fail，24h 内不重复回源）。
// 该标记表示暂时性故障（网络错误/5xx）：命中时 stale-if-error 兜底返回过期缓存。
func isAvatarFetchFailed(hash string) bool {
	info, err := os.Stat(avatarFilePath(hash, ".fail"))
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < avatarFailTTL
}

// markAvatarFetchFailed 写入「网络故障」标记（写失败忽略，下次请求会重试）。
// 与 markAvatarNotFound 一致：先确保目录存在，避免新部署（目录未创建）时标记静默写失败。
func markAvatarFetchFailed(hash string) {
	if err := os.MkdirAll(avatarDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(avatarFilePath(hash, ".fail"), nil, 0o644)
}

// clearAvatarFailures 回源成功后清理 .none/.fail 标记，避免残留干扰后续语义
func clearAvatarFailures(hash string) {
	_ = os.Remove(avatarFilePath(hash, ".none"))
	_ = os.Remove(avatarFilePath(hash, ".fail"))
}

// avatarHashLocks 同 hash 回源写入的分片锁（按 md5 首字符分 16 片）。
// 解决并发回源同一邮箱时 cleanupAvatarCache 相互删除对方缓存文件的竞态（见 writeAvatarCache）。
var avatarHashLocks [16]sync.Mutex

// writeAvatarCache 把图片落盘（写失败忽略，仅损失本次缓存机会）。
// 先写唯一临时文件再 os.Rename 原子替换：同目录 rename 原子，读方永远看到完整旧/新文件；
// 临时文件用 os.CreateTemp 唯一化，避免并发回源同一 hash 时多个写者共享同一 tmp 相互截断。
// 同 hash 写入按首字符分片锁串行：cleanupAvatarCache 会删其他扩展名文件，若并发不加锁，
// 两个写者（如 jpg/png）会互相删掉对方刚落盘的文件，导致缓存被清空、被迫反复回源。
// 成功后清理同 hash 的旧扩展名文件；失败标记由调用方 clearAvatarFailures 清理。
func writeAvatarCache(hash string, data []byte, contentType string) {
	if err := os.MkdirAll(avatarDir, 0o755); err != nil {
		return
	}
	lock := &avatarHashLocks[hash[0]%16]
	lock.Lock()
	defer lock.Unlock()
	ext := contentTypeToExt(contentType)
	path := avatarFilePath(hash, ext)
	tmp, err := os.CreateTemp(avatarDir, hash+"-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // 清理：rename 失败或异常路径下不留垃圾
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		return
	}
	cleanupAvatarCache(hash, ext)
}

// cleanupAvatarCache 清理同 hash 的旧扩展名缓存文件（失败忽略）。
// 避免上游 Content-Type 变化（如 jpg → png）遗留的旧扩展名文件越积越多。
func cleanupAvatarCache(hash, keepExt string) {
	for _, ext := range avatarExts {
		if ext != keepExt {
			os.Remove(avatarFilePath(hash, ext))
		}
	}
}

func avatarFilePath(hash, ext string) string {
	return filepath.Join(avatarDir, hash+ext)
}

// contentTypeToExt 由上游 Content-Type 推断扩展名（qlogo/cravatar 常见 jpg/png）
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

// cleanAvatarCache 回收头像缓存目录中的失效文件（启动时 + 按 avatar_clean_interval_hours 定时执行）：
//   - 图片缓存：修改时间超过缓存 TTL（+1h 冗余）后删除（readAvatarCache 已将其视为 miss；
//     注意 stale-if-error 降级兜底仅对 TTL+1h 内的过期缓存生效——更早的缓存被回收后，
//     上游故障期间该邮箱将回退字母头像，属磁盘空间与降级可用性的取舍）
//   - .none/.fail 标记：超过 24h（+1h 冗余）后删除（isAvatarNotFound/isAvatarFetchFailed 已忽略）
//   - .tmp 残留：超过 1h 后删除（防误删进行中的原子写，进程被强杀时残留的 tmp 由此回收）
//   - 非法命名文件（历史 {评论ID}.ext 孤儿、损坏文件等）：直接删除，当前代码永远不会读取
//
// 目录不存在视为空目录。删除与写文件并发无危害：写入用原子 rename，删了也只是下次回源。
func (s *Service) cleanAvatarCache() {
	entries, err := os.ReadDir(avatarDir)
	if err != nil {
		return
	}
	imgTTL := s.avatarCacheTTL() + time.Hour
	markTTL := avatarFailTTL + time.Hour
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(avatarDir, name)
		switch {
		case avatarTmpRE.MatchString(name):
			removeAvatarIfOld(path, time.Hour, now)
		case avatarFileRE.MatchString(name):
			removeAvatarIfOld(path, imgTTL, now)
		case avatarMarkerRE.MatchString(name):
			removeAvatarIfOld(path, markTTL, now)
		default:
			// 非法命名：当前代码永远不会读取的历史孤儿（如旧 {评论ID}.ext），直接删除
			os.Remove(path)
		}
	}
}

// removeAvatarIfOld 在删除前重新 stat 复核 mtime，文件超龄才删。
// 避免清理器与并发 writeAvatarCache 的 rename 竞态：若旧快照判定后文件被替换成新鲜文件则不删
// （仅损失一次回收机会，由下轮清理补上）。
func removeAvatarIfOld(path string, maxAge time.Duration, now time.Time) {
	info, err := os.Stat(path)
	if err != nil {
		return // 已被并发 rename/删除，无需处理
	}
	if now.Sub(info.ModTime()) > maxAge {
		os.Remove(path)
	}
}

// StartAvatarCleaner 启动头像缓存清理：立即执行一次，随后按 comment.avatar_clean_interval_hours 定时执行。
// 间隔为 0 或负数时仅执行启动清理（不启动定时任务）；ctx 取消时停止定时任务（配合优雅退出）。
func (s *Service) StartAvatarCleaner(ctx context.Context) {
	s.cleanAvatarCache()
	hours := s.Cfg.Comment.AvatarCleanIntervalHours
	if hours <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Duration(hours) * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.cleanAvatarCache()
			}
		}
	}()
}
