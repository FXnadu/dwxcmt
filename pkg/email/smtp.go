// Package email 提供基于标准库 net/smtp 的邮件发送与评论通知（T4）。
package email

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"dwxcmt/config"
)

// 单次 SMTP 会话整体超时，防止网络异常时长时间阻塞（通知在后台 goroutine 执行）
const sessionTimeout = 15 * time.Second

// Send 通过 SMTP 发送一封邮件。
// textBody 为纯文本版本，htmlBody 为 HTML 版本；两者同时提供时按 multipart/alternative 发送，
// 仅提供 textBody 时退化为纯文本邮件。use_ssl=true 时使用隐式 TLS（SMTPS，通常 465 端口）。
// 邮件内容按 RFC 822 组装（CRLF 换行），失败返回错误，由调用方记录日志。
func Send(cfg config.SMTPConfig, from, to, subject, textBody, htmlBody string) error {
	if from == "" || to == "" {
		return fmt.Errorf("email: 发件人或收件人为空")
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	var conn net.Conn
	var err error
	if cfg.UseSSL {
		dialer := &net.Dialer{Timeout: sessionTimeout}
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: cfg.Host})
	} else {
		conn, err = net.DialTimeout("tcp", addr, sessionTimeout)
	}
	if err != nil {
		return err
	}
	// 整段会话设统一截止时间，避免服务器不响应时永久阻塞
	if err := conn.SetDeadline(time.Now().Add(sessionTimeout)); err != nil {
		conn.Close()
		return err
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()

	// 明文连接时若服务器支持则升级 TLS（与 net/smtp.SendMail 行为一致）
	if !cfg.UseSSL {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
				return err
			}
		}
	}

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}

	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}

	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(buildMessage(from, to, subject, textBody, htmlBody))); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// buildMessage 组装 RFC 822 格式邮件头与正文（统一 CRLF 换行）。
// 当 htmlBody 为空时发送纯文本邮件；否则发送 multipart/alternative，包含纯文本与 HTML 双版本。
// 头部字段（From/To/Subject）一律经 sanitizeHeader 过滤 CR/LF，防邮件头注入
// （昵称/站点名等用户可控内容可含 \r\n，若原样拼接可注入 Bcc/Cc 等额外头）。
func buildMessage(from, to, subject, textBody, htmlBody string) string {
	var b strings.Builder
	b.WriteString("From: " + sanitizeHeader(from) + "\r\n")
	b.WriteString("To: " + sanitizeHeader(to) + "\r\n")
	b.WriteString("Subject: " + sanitizeHeader(subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")

	if strings.TrimSpace(htmlBody) == "" {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		b.WriteString("\r\n")
		b.WriteString(normalizeCRLF(textBody))
		return b.String()
	}

	boundary := generateBoundary()
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n")
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(normalizeCRLF(textBody))
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(normalizeCRLF(htmlBody))
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "--\r\n")
	return b.String()
}

// normalizeCRLF 将任意换行统一为 CRLF（符合 RFC 822 要求）
func normalizeCRLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// sanitizeHeader 移除头部字段中的 CR/LF 字符（并折叠为单个空格），防邮件头注入。
// 用户可控内容（昵称/站点名/邮箱显示名）可能包含 \r\n，原样写入头部即构成注入向量。
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// generateBoundary 生成 multipart 边界串（随机 16 字节 hex），降低与用户正文冲突的概率
func generateBoundary() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "----dwxCommentBoundary----"
	}
	return "----dwxCommentBoundary_" + hex.EncodeToString(buf) + "----"
}
