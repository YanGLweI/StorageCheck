package mailer

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"

	"storagecheck/internal/config"
)

// Mailer 按当前 SMTP 配置发送邮件。
type Mailer struct {
	cfg  config.SMTPConfig
	pass string
}

func New(cfg config.SMTPConfig, password string) *Mailer {
	return &Mailer{cfg: cfg, pass: password}
}

func (m *Mailer) enabled() bool {
	c := m.cfg
	return c.Host != "" && c.Port != 0 && c.User != "" && len(c.Recipients) > 0
}

// ErrNotConfigured 表示 SMTP 尚未配置完整。
var ErrNotConfigured = fmt.Errorf("smtp: 配置不完整（需要服务器、端口、账号与至少一个收件人）")

func (m *Mailer) Send(subject, bodyHTML string) error {
	if !m.enabled() {
		return ErrNotConfigured
	}
	c := m.cfg
	body := buildMIME(c.User, c.FromAlias, subject, m.cfg.Recipients, bodyHTML)
	addr := net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port))
	auth := smtp.PlainAuth("", c.User, m.pass, c.Host)

	switch c.Security {
	case "tls":
		return m.sendImplicitTLS(addr, auth, c.User, c.Recipients, body)
	case "starttls":
		return m.sendSTARTTLS(addr, auth, c.User, c.Recipients, body)
	default: // none —— 仅内网免认证 SMTP 场景
		cl, err := smtp.Dial(addr)
		if err != nil {
			return err
		}
		defer cl.Quit()
		if len(m.pass) > 0 {
			if err := cl.Auth(auth); err != nil {
				return err
			}
		}
		return m.transmit(cl, c.User, c.Recipients, body)
	}
}

func (m *Mailer) sendImplicitTLS(addr string, auth smtp.Auth, from string, to []string, body []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: m.cfg.Host})
	if err != nil {
		return err
	}
	cl, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Quit()
	if err := cl.Auth(auth); err != nil {
		return err
	}
	return m.transmit(cl, from, to, body)
}

func (m *Mailer) sendSTARTTLS(addr string, auth smtp.Auth, from string, to []string, body []byte) error {
	cl, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer cl.Quit()
	if ok, _ := cl.Extension("STARTTLS"); !ok {
		return fmt.Errorf("smtp: 服务器不支持 STARTTLS")
	}
	if err := cl.StartTLS(&tls.Config{ServerName: m.cfg.Host}); err != nil {
		return err
	}
	if len(m.pass) > 0 {
		if err := cl.Auth(auth); err != nil {
			return err
		}
	}
	return m.transmit(cl, from, to, body)
}

func (m *Mailer) transmit(cl *smtp.Client, from string, to []string, body []byte) error {
	if err := cl.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := cl.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	return w.Close()
}

func buildMIME(from, alias, subject string, to []string, bodyHTML string) []byte {
	if alias == "" {
		alias = "StorageCheck"
	}
	q := mime.QEncoding
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s <%s>\r\n", q.Encode("utf-8", alias), from)
	quoted := make([]string, len(to))
	for i, t := range to {
		quoted[i] = "<" + t + ">"
	}
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(quoted, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", q.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%d@storagecheck>\r\n", time.Now().UnixNano())
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(wrapBase64([]byte(bodyHTML)))
	return []byte(b.String())
}

func wrapBase64(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		b.WriteString(enc[i:end])
		b.WriteString("\r\n")
	}
	return b.String()
}
