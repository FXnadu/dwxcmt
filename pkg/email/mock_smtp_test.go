package email_test

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// mockMail 一封被 mock SMTP 服务器接收的邮件
type mockMail struct {
	From string   // MAIL FROM 原始值
	To   []string // RCPT TO 列表
	Data string   // DATA 完整内容（含 RFC822 头与正文）
}

// mockSMTP 最小 SMTP 服务器：接收连接并收集邮件内容，不真正投递。
// 仅实现 net/smtp 客户端所需命令：EHLO/HELO、MAIL、RCPT、DATA、QUIT。
// 明文连接且不声明 STARTTLS/AUTH，正好覆盖 email.Send 的默认路径。
type mockSMTP struct {
	ln    net.Listener
	mu    sync.Mutex
	inbox []mockMail
}

// startMockSMTP 启动 mock SMTP，监听 127.0.0.1 随机端口；测试结束自动关闭。
func startMockSMTP(t *testing.T) *mockSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 mock SMTP 失败: %v", err)
	}
	m := &mockSMTP{ln: ln}
	go m.acceptLoop()
	t.Cleanup(func() { ln.Close() })
	return m
}

// port 返回监听端口，供 SMTPConfig 使用
func (m *mockSMTP) port() int {
	_, p, err := net.SplitHostPort(m.ln.Addr().String())
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

// mails 返回当前收到的全部邮件快照（并发安全）
func (m *mockSMTP) mails() []mockMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mockMail(nil), m.inbox...)
}

func (m *mockSMTP) acceptLoop() {
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			return // listener 已关闭，测试结束
		}
		go m.handle(conn)
	}
}

func (m *mockSMTP) handle(conn net.Conn) {
	defer conn.Close()
	rd := bufio.NewReader(conn)
	wr := bufio.NewWriter(conn)
	reply := func(line string) {
		wr.WriteString(line + "\r\n")
		wr.Flush()
	}

	reply("220 localhost ESMTP mock")
	var mail mockMail
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
				m.inbox = append(m.inbox, mail)
				m.mu.Unlock()
				mail = mockMail{}
				reply("250 OK queued")
				continue
			}
			mail.Data += line + "\n"
			continue
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			reply("250-localhost")
			reply("250-AUTH PLAIN LOGIN")
			reply("250 OK")
		case strings.HasPrefix(cmd, "HELO"):
			reply("250 localhost")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			mail.From = strings.TrimSpace(line[10:])
			reply("250 OK")
		case strings.HasPrefix(cmd, "RCPT TO"):
			mail.To = append(mail.To, strings.TrimSpace(line[8:]))
			reply("250 OK")
		case strings.HasPrefix(cmd, "DATA"):
			reply("354 End data with <CR><LF>.<CR><LF>")
			inData = true
		case strings.HasPrefix(cmd, "AUTH"):
			// 接受任意 AUTH（PLAIN/LOGIN），不做真实校验
			reply("235 2.7.0 Authentication successful")
		case strings.HasPrefix(cmd, "RSET"):
			mail = mockMail{}
			reply("250 OK")
		case strings.HasPrefix(cmd, "QUIT"):
			reply("221 Bye")
			return
		default:
			reply("250 OK")
		}
	}
}

// dumpMails 打印全部收件内容（便于人工核验邮件头与正文）
func dumpMails(mails []mockMail) {
	for i, ml := range mails {
		fmt.Printf("===== mock 收到邮件 #%d (from=%s to=%v) =====\n%s\n", i+1, ml.From, ml.To, ml.Data)
	}
}
