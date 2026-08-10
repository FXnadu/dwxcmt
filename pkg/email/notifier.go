package email

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"dwxcmt/config"
	"dwxcmt/model"
)

// SiteSettings 邮件通知所需的站点配置最小集合。
// 由注入方（业务层）从完整站点配置适配而来，避免本包反向依赖 service 层。
type SiteSettings struct {
	SiteName    string
	SiteURL     string
	NoticeEmail string
	NotifyNew   bool
	NotifyReply bool
}

// SettingsLoader 站点通知配置加载器，由业务层实现并注入；
// 返回 ok=false 表示本次不应发送（如 SMTP 未配置或站点配置读取失败）。
type SettingsLoader interface {
	EmailSettings(site string) (SiteSettings, bool)
}

// 发送池参数：既限制并发 SMTP 连接数，也限制排队长度，
// 防止 SMTP 慢/不可达时 goroutine 与内存无限堆积（1G 环境尤为重要）。
const (
	maxSendWorkers = 4  // 并发 SMTP 连接上限
	maxSendQueue   = 64 // 待发送队列上限，满则丢弃本次通知（通知为尽力而为）
)

type sendJob struct {
	to, subject, textBody, htmlBody string
}

// Notifier 实现 service.Notifier 接口，通过 SMTP 发送评论邮件通知。
// 所有发送均为异步，失败仅记录日志，绝不影响评论提交主流程。
// smtp 为静态 SMTP 配置（构造时注入）；loader 动态提供各站点通知配置。
type Notifier struct {
	smtp   config.SMTPConfig
	loader SettingsLoader
	jobs   chan sendJob
	once   sync.Once
}

// NewNotifier 构造邮件通知器。loader 返回 ok=false（如 SMTP 未配置）时通知静默跳过。
func NewNotifier(cfg config.SMTPConfig, loader SettingsLoader) *Notifier {
	return &Notifier{
		smtp:   cfg,
		loader: loader,
		jobs:   make(chan sendJob, maxSendQueue),
	}
}

// startWorkers 惰性启动固定数量的发送 worker（进程生命周期内常驻，最多 maxSendWorkers 个）
func (n *Notifier) startWorkers() {
	for i := 0; i < maxSendWorkers; i++ {
		go func() {
			for job := range n.jobs {
				from := strings.TrimSpace(n.smtp.Username)
				if from == "" {
					from = "no-reply@localhost"
				}
				if err := Send(n.smtp, from, job.to, job.subject, job.textBody, job.htmlBody); err != nil {
					log.Printf("[email] 邮件发送失败 to=%s subject=%q: %v", job.to, job.subject, err)
					continue
				}
				log.Printf("[email] 邮件发送成功 to=%s subject=%q", job.to, job.subject)
			}
		}()
	}
}

// NotifyNewComment 新评论通知管理员。
// 生效条件：SMTP 已配置 && notifyNewComment=true && noticeEmail 已设置。
func (n *Notifier) NotifyNewComment(c *model.Comment) {
	settings, ok := n.loader.EmailSettings(c.Site)
	if !ok {
		return
	}
	if !settings.NotifyNew {
		return
	}
	to := strings.TrimSpace(settings.NoticeEmail)
	if to == "" {
		return
	}
	siteName := displayName(settings.SiteName)
	subject := fmt.Sprintf("【%s】收到新评论：%s", siteName, c.Nick)
	textBody := newCommentBody(siteName, c)
	htmlBody, err := renderNewCommentHTML(siteName, c)
	if err != nil {
		log.Printf("[email] 渲染新评论 HTML 模板失败: %v", err)
		htmlBody = ""
	}
	n.asyncSend(to, subject, textBody, htmlBody)
}

// NotifyReply 评论被回复时通知被回复者。
// 生效条件：SMTP 已配置 && notifyReply=true && 父评论留有邮箱。
func (n *Notifier) NotifyReply(c *model.Comment, parent *model.Comment) {
	settings, ok := n.loader.EmailSettings(c.Site)
	if !ok {
		return
	}
	if !settings.NotifyReply {
		return
	}
	to := strings.TrimSpace(parent.Email)
	if to == "" {
		return
	}
	siteName := displayName(settings.SiteName)
	link := pageLink(settings.SiteURL, c.PageID, c.ID)
	subject := fmt.Sprintf("【%s】有人回复了你的评论", siteName)
	textBody := replyBody(siteName, link, c, parent)
	htmlBody, err := renderReplyHTML(siteName, link, c, parent)
	if err != nil {
		log.Printf("[email] 渲染回复 HTML 模板失败: %v", err)
		htmlBody = ""
	}
	n.asyncSend(to, subject, textBody, htmlBody)
}

// asyncSend 投递发送任务到 worker 池；队列满时丢弃并记日志
// （通知为尽力而为，不影响评论提交主流程，也不允许无界堆积）
func (n *Notifier) asyncSend(to, subject, textBody, htmlBody string) {
	n.once.Do(n.startWorkers)
	select {
	case n.jobs <- sendJob{to: to, subject: subject, textBody: textBody, htmlBody: htmlBody}:
	default:
		log.Printf("[email] 发送队列已满，丢弃通知 to=%s subject=%q", to, subject)
	}
}

// displayName 站点显示名（未配置时用产品名）
func displayName(siteName string) string {
	if strings.TrimSpace(siteName) == "" {
		return "dwxComment"
	}
	return strings.TrimSpace(siteName)
}

// pageLink 拼接评论页完整链接并带评论锚点（#comment-{id}）；
// siteURL 为空时返回空串（邮件不显示链接）；commentID 为 0 时不追加锚点。
func pageLink(siteURL, pageID string, commentID int64) string {
	siteURL = strings.TrimSpace(siteURL)
	if siteURL == "" {
		return ""
	}
	base := strings.TrimRight(siteURL, "/")
	pageID = strings.Trim(strings.TrimSpace(pageID), "/")
	if pageID != "" {
		base += "/" + pageID
	}
	if commentID > 0 {
		base += fmt.Sprintf("#comment-%d", commentID)
	}
	return base
}

// newCommentBody 新评论通知正文
func newCommentBody(siteName string, c *model.Comment) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("您在 %s 收到了新评论：\n\n", siteName))
	b.WriteString(fmt.Sprintf("评论者：%s\n", c.Nick))
	if c.Email != "" {
		b.WriteString(fmt.Sprintf("邮箱：%s\n", c.Email))
	}
	b.WriteString(fmt.Sprintf("页面：%s\n", c.PageID))
	b.WriteString(fmt.Sprintf("时间：%s\n", time.Unix(c.CreateTime, 0).Format("2006-01-02 15:04")))
	b.WriteString("内容：\n")
	b.WriteString(c.Content + "\n")
	return b.String()
}

// replyBody 回复通知正文
func replyBody(siteName, link string, c *model.Comment, parent *model.Comment) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("您在 %s 的评论收到了回复：\n\n", siteName))
	b.WriteString("您的评论：\n" + parent.Content + "\n\n")
	b.WriteString(fmt.Sprintf("回复者：%s\n", c.Nick))
	b.WriteString(fmt.Sprintf("时间：%s\n", time.Unix(c.CreateTime, 0).Format("2006-01-02 15:04")))
	b.WriteString("回复内容：\n")
	b.WriteString(c.Content + "\n")
	if link != "" {
		b.WriteString("\n前往查看完整评论：\n" + link + "\n")
	}
	return b.String()
}
