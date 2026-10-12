package mailer

import (
	"strings"
	"testing"

	"storagecheck/internal/config"
)

func TestAlertMailShowsDiskRows(t *testing.T) {
	html := render(mailData{Title: "磁盘空间不足告警", DriveID: `C:`, Label: "系统盘", TotalGB: "512.0", FreeGB: "20.4", UsedPct: "96.0", FreePct: "4.0", Threshold: "10"})
	for _, want := range []string{"磁盘", "剩余空间", "20.4 GB / 512.0 GB", "4.0%（阈值 10%）", "系统盘"} {
		if !strings.Contains(html, want) {
			t.Fatalf("告警邮件缺少 %q", want)
		}
	}
}

func TestTestMailHasNoEmptyDiskRows(t *testing.T) {
	m := New(config.SMTPConfig{Host: "smtp.example.com", Port: 465, User: "me@example.com"}, "pass")
	html := render(mailData{Title: "SMTP 配置成功", Server: m.cfg.Host, Account: m.cfg.User})
	for _, unwanted := range []string{"剩余空间", "阈值", "磁盘", "—"} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("测试邮件不应出现 %q:\n%s", unwanted, html)
		}
	}
	for _, want := range []string{"smtp.example.com", "me@example.com"} {
		if !strings.Contains(html, want) {
			t.Fatalf("测试邮件缺少 %q", want)
		}
	}
}
