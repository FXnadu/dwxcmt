package email_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dwxcmt/config"
	"dwxcmt/model"
	"dwxcmt/pkg/cache"
	"dwxcmt/pkg/email"
	"dwxcmt/service"
)

// startService 构造指向 mock SMTP 的完整 Service（临时 SQLite，测试结束自动清理）。
// 与 service 包现有测试模式一致。
func startService(t *testing.T, smtp *config.SMTPConfig) *service.Service {
	t.Helper()
	db, err := model.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试 DB 失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	cfg := config.Default()
	cfg.Auth.JWTSecret = "test-secret-32-chars-minimum!!!"
	cfg.SMTP = *smtp
	return service.New(db, cfg, cache.New(64, time.Minute))
}

// waitMails 轮询等待 mock 收到至少 n 封邮件（通知为异步 worker 发送，需等待）
func waitMails(t *testing.T, m *mockSMTP, n int, timeout time.Duration) []mockMail {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := m.mails(); len(got) >= n {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
	got := m.mails()
	t.Fatalf("等待 %d 封邮件超时，实际收到 %d 封", n, len(got))
	return nil
}

// TestMockSMTP_EmailFlow 覆盖完整邮件链路：
//  1. 验证码邮件（SendEmailCode，同步发送）
//  2. 新评论通知管理员（NotifyNewComment，异步）
//  3. 站长回复通知被回复者（NotifyReply，异步）
func TestMockSMTP_EmailFlow(t *testing.T) {
	m := startMockSMTP(t)
	svc := startService(t, &config.SMTPConfig{
		Host:     "127.0.0.1",
		Port:     m.port(),
		Username: "sender@example.com", // HasSMTP 要求 Username 非空；同时覆盖 AUTH 分支
		Password: "mockpass",
		UseSSL:   false,
	})
	// 与 main.go 相同装配：svc 自身实现 email.SettingsLoader
	svc.Notifier = email.NewNotifier(svc.Cfg.SMTP, svc)

	// 站点通知邮箱（NotifyNew 默认开启，需 noticeEmail 非空才会发）
	if err := svc.SetSetting("default", service.KeyNoticeEmail, "admin@example.com"); err != nil {
		t.Fatalf("设置通知邮箱失败: %v", err)
	}

	// 1) 验证码（同步）
	if err := svc.SendEmailCode("user@example.com"); err != nil {
		t.Fatalf("SendEmailCode 失败: %v", err)
	}

	// 2) 新评论 → 通知管理员（异步）
	rootID, _, err := svc.SubmitComment(&service.SubmitRequest{
		PageID:  "/post/1",
		Site:    "default",
		Nick:    "张三",
		Email:   "zhangsan@example.com",
		Content: "这是一条测试评论",
	}, "1.2.3.4", "go-test-agent")
	if err != nil {
		t.Fatalf("提交评论失败: %v", err)
	}

	// 3) 审核通过后站长回复 → 通知被回复者（异步）
	if err := svc.AuditComment(rootID, 1); err != nil {
		t.Fatalf("审核评论失败: %v", err)
	}
	if _, err := svc.ReplyComment(rootID, "站长回复：欢迎来访"); err != nil {
		t.Fatalf("站长回复失败: %v", err)
	}

	mails := waitMails(t, m, 3, 3*time.Second)

	// 打印全部收件内容，便于人工核验
	for i, ml := range mails {
		t.Logf("===== mock 收到邮件 #%d (from=%s to=%v) =====\n%s", i+1, ml.From, ml.To, ml.Data)
	}

	// 断言三类邮件都到达且收件人正确
	assertMailContains(t, mails, "【dwxComment】验证码", "user@example.com")
	assertMailContains(t, mails, "收到新评论", "admin@example.com")
	assertMailContains(t, mails, "有人回复了你的评论", "zhangsan@example.com")
}

// TestMockSMTP_NoSMTP_Skip SMTP 未配置时的降级行为：
//
//	验证码直接报错；评论通知静默跳过，不产生任何邮件、不影响主流程。
func TestMockSMTP_NoSMTP_Skip(t *testing.T) {
	m := startMockSMTP(t)
	svc := startService(t, &config.SMTPConfig{}) // SMTP 全空
	svc.Notifier = email.NewNotifier(svc.Cfg.SMTP, svc)

	if err := svc.SendEmailCode("user@example.com"); err == nil {
		t.Fatal("SMTP 未配置时 SendEmailCode 应返回错误")
	}

	if _, _, err := svc.SubmitComment(&service.SubmitRequest{
		PageID:  "/post/2",
		Site:    "default",
		Nick:    "李四",
		Content: "无 SMTP 配置下的评论",
	}, "1.2.3.4", "go-test-agent"); err != nil {
		t.Fatalf("提交评论失败: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // 给异步通知留出窗口
	if got := m.mails(); len(got) != 0 {
		t.Fatalf("SMTP 未配置时不应有邮件发送，实际收到 %d 封", len(got))
	}
}

// assertMailContains 断言 mock 收件箱中存在同时满足关键字与收件人的邮件
func assertMailContains(t *testing.T, mails []mockMail, keyword, to string) {
	t.Helper()
	for _, ml := range mails {
		if strings.Contains(ml.Data, keyword) && containsTo(ml.To, to) {
			return
		}
	}
	t.Fatalf("未收到含 %q 且收件人 %q 的邮件，全部收件:\n%+v", keyword, to, mails)
}

func containsTo(tos []string, target string) bool {
	for _, to := range tos {
		if strings.Contains(to, target) {
			return true
		}
	}
	return false
}
