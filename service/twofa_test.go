package service

import (
	"bufio"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"dwxcmt/config"
)

// mock2faSMTP 最小 SMTP 服务器：接收并收集邮件 DATA（仅覆盖 net/smtp 客户端所需命令）。
// 不声明 STARTTLS（明文路径），Host 使用 127.0.0.1 以满足 PlainAuth 的本地校验。
type mock2faSMTP struct {
	ln    net.Listener
	mu    sync.Mutex
	mails []string
}

func startMock2faSMTP(t *testing.T) *mock2faSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 mock SMTP 失败: %v", err)
	}
	m := &mock2faSMTP{ln: ln}
	go m.acceptLoop()
	t.Cleanup(func() { ln.Close() })
	return m
}

func (m *mock2faSMTP) port() int {
	_, p, err := net.SplitHostPort(m.ln.Addr().String())
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

func (m *mock2faSMTP) acceptLoop() {
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			return
		}
		go m.handle(conn)
	}
}

func (m *mock2faSMTP) handle(conn net.Conn) {
	defer conn.Close()
	rd := bufio.NewReader(conn)
	wr := bufio.NewWriter(conn)
	reply := func(line string) { wr.WriteString(line + "\r\n"); wr.Flush() }

	reply("220 localhost ESMTP mock")
	var data strings.Builder
	inData := false
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				m.mu.Lock()
				m.mails = append(m.mails, data.String())
				m.mu.Unlock()
				data.Reset()
				reply("250 OK queued")
				continue
			}
			data.WriteString(line + "\n")
			continue
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			reply("250-localhost")
			reply("250-AUTH PLAIN LOGIN")
			reply("250 OK")
		case strings.HasPrefix(cmd, "MAIL FROM"), strings.HasPrefix(cmd, "RCPT TO"):
			reply("250 OK")
		case strings.HasPrefix(cmd, "DATA"):
			reply("354 End data with <CR><LF>.<CR><LF>")
			inData = true
		case strings.HasPrefix(cmd, "AUTH"):
			reply("235 2.7.0 Authentication successful")
		case strings.HasPrefix(cmd, "QUIT"):
			reply("221 Bye")
			return
		default:
			reply("250 OK")
		}
	}
}

// start2faService 构造指向 mock SMTP 的 Service（复用 testService 的临时 DB 装配）
func start2faService(t *testing.T) (*Service, *mock2faSMTP) {
	t.Helper()
	m := startMock2faSMTP(t)
	svc := testService(t)
	svc.Cfg.SMTP = config.SMTPConfig{
		Host:     "127.0.0.1",
		Port:     m.port(),
		Username: "sender@example.com",
		Password: "mockpass",
		UseSSL:   false,
	}
	return svc, m
}

// lastCode 从最近一封验证码邮件中提取 6 位验证码
func lastCode(t *testing.T, m *mock2faSMTP) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.mails) == 0 {
		t.Fatal("mock SMTP 未收到邮件")
	}
	re := regexp.MustCompile(`您的验证码为：(\d{6})`)
	sub := re.FindStringSubmatch(m.mails[len(m.mails)-1])
	if len(sub) != 2 {
		t.Fatalf("邮件中未找到验证码，内容: %s", m.mails[len(m.mails)-1])
	}
	return sub[1]
}

// TestTwoFA_LoginFlow 覆盖两步验证完整生命周期：
// 注册 → 绑定邮箱 → 开启 2FA → 密码登录返回 need2FA（自动发码）→ 输入验证码换正式 token
// → 错误验证码拒绝 → 关闭 2FA 恢复单步登录
func TestTwoFA_LoginFlow(t *testing.T) {
	svc, mock := start2faService(t)

	// 注册首个管理员
	if _, err := svc.Register("admin", "Admin123456"); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	var adminID int64
	if err := svc.DB.QueryRow(`SELECT id FROM admins WHERE username = 'admin'`).Scan(&adminID); err != nil {
		t.Fatalf("查询管理员失败: %v", err)
	}

	// 未绑定邮箱时开启 2FA 应被拒绝
	if err := svc.Enable2FA(adminID); err == nil {
		t.Fatal("未绑定邮箱时 Enable2FA 应返回错误")
	}

	// 绑定邮箱
	if err := svc.BindEmailToAdmin(adminID, "admin@example.com"); err != nil {
		t.Fatalf("绑定邮箱失败: %v", err)
	}
	// 开启 2FA
	if err := svc.Enable2FA(adminID); err != nil {
		t.Fatalf("Enable2FA 失败: %v", err)
	}

	// 密码登录：应返回 need2FA + 预授权凭证 + 脱敏邮箱，并自动发码
	res, err := svc.Login("admin", "Admin123456")
	if err != nil {
		t.Fatalf("Login 失败: %v", err)
	}
	if !res.Need2FA || res.PreAuthToken == "" || res.MaskedEmail != "a***@example.com" {
		t.Fatalf("2FA 登录响应异常: %+v", res)
	}
	code := lastCode(t, mock)

	// 错误验证码应被拒绝
	if _, err := svc.Complete2FALogin(res.PreAuthToken, "000000"); err == nil {
		t.Fatal("错误验证码应返回错误")
	}

	// 正确验证码换取正式 token
	final, err := svc.Complete2FALogin(res.PreAuthToken, code)
	if err != nil {
		t.Fatalf("Complete2FALogin 失败: %v", err)
	}
	if final.Token == "" {
		t.Fatal("正式 token 为空")
	}
	claims, err := svc.JWT.Parse(final.Token)
	if err != nil {
		t.Fatalf("正式 token 解析失败: %v", err)
	}
	if claims.Purpose != "" {
		t.Fatalf("正式 token 不应带 Purpose，got %q", claims.Purpose)
	}
	if claims.Subject != strconv.FormatInt(adminID, 10) {
		t.Fatalf("正式 token subject 错误: %s", claims.Subject)
	}

	// 预授权凭证不可重复使用（验证码一次性，二次提交应失败）
	if _, err := svc.Complete2FALogin(res.PreAuthToken, code); err == nil {
		t.Fatal("重复使用同一验证码应失败")
	}

	// 解绑邮箱应自动关闭 2FA
	if err := svc.UnbindEmail(adminID); err != nil {
		t.Fatalf("UnbindEmail 失败: %v", err)
	}
	res2, err := svc.Login("admin", "Admin123456")
	if err != nil {
		t.Fatalf("关闭 2FA 后 Login 失败: %v", err)
	}
	if res2.Need2FA || res2.Token == "" {
		t.Fatalf("解绑邮箱后应恢复单步登录，got %+v", res2)
	}
}

// TestTwoFA_EnableRejectsNoSMTP SMTP 未配置时不可开启 2FA（验证码无处发送）
func TestTwoFA_EnableRejectsNoSMTP(t *testing.T) {
	svc := testService(t) // SMTP 全空
	if _, err := svc.Register("admin", "Admin123456"); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	var adminID int64
	if err := svc.DB.QueryRow(`SELECT id FROM admins WHERE username = 'admin'`).Scan(&adminID); err != nil {
		t.Fatalf("查询管理员失败: %v", err)
	}
	if err := svc.BindEmailToAdmin(adminID, "admin@example.com"); err != nil {
		t.Fatalf("绑定邮箱失败: %v", err)
	}
	if err := svc.Enable2FA(adminID); err == nil {
		t.Fatal("SMTP 未配置时 Enable2FA 应返回错误")
	}
}

// TestMaskEmail 邮箱脱敏
func TestMaskEmail(t *testing.T) {
	cases := map[string]string{
		"admin@example.com": "a***@example.com",
		"a@b.com":           "a@b.com",
		"":                  "",
	}
	for in, want := range cases {
		if got := MaskEmail(in); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
