package mailer

import (
	"fmt"
	"html/template"
	"strings"
	"time"
)

type mailData struct {
	Title     string
	Accent    string // 告警红 / 恢复绿
	Hostname  string
	Time      string
	DriveID   string
	Label     string
	TotalGB   string
	FreeGB    string
	UsedPct   string
	FreePct   string
	Threshold string
	Server    string
	Account   string
	Note      string
}

var tpl = template.Must(template.New("mail").Parse(`<!DOCTYPE html>
<html><head><meta charset="UTF-8"></head>
<body style="margin:0;padding:24px;background:#f3f4f6;font-family:'Segoe UI',system-ui,sans-serif;color:#1f2937;">
  <div style="max-width:560px;margin:0 auto;background:#fff;border-radius:12px;overflow:hidden;box-shadow:0 1px 3px rgba(0,0,0,.08);">
    <div style="background:{{.Accent}};color:#fff;padding:18px 24px;font-size:17px;font-weight:600;">{{.Title}}</div>
    <div style="padding:24px;">
      <table style="width:100%;border-collapse:collapse;font-size:14px;">
        {{if .DriveID}}
        <tr><td style="padding:6px 0;color:#6b7280;">磁盘</td><td style="text-align:right;font-weight:600;">{{.DriveID}}{{if .Label}}（{{.Label}}）{{end}}</td></tr>
        <tr><td style="padding:6px 0;color:#6b7280;">剩余空间</td><td style="text-align:right;font-weight:600;">{{.FreeGB}} GB / {{.TotalGB}} GB</td></tr>
        <tr><td style="padding:6px 0;color:#6b7280;">剩余百分比</td><td style="text-align:right;font-weight:600;">{{.FreePct}}%（阈值 {{.Threshold}}%）</td></tr>
        <tr><td style="padding:6px 0;color:#6b7280;">已用</td><td style="text-align:right;">{{.UsedPct}}%</td></tr>
        {{end}}
        {{if .Server}}<tr><td style="padding:6px 0;color:#6b7280;">邮件服务器</td><td style="text-align:right;">{{.Server}}</td></tr>{{end}}
        {{if .Account}}<tr><td style="padding:6px 0;color:#6b7280;">发件账号</td><td style="text-align:right;">{{.Account}}</td></tr>{{end}}
        <tr><td style="padding:6px 0;color:#6b7280;">计算机</td><td style="text-align:right;">{{.Hostname}}</td></tr>
        <tr><td style="padding:6px 0;color:#6b7280;">时间</td><td style="text-align:right;">{{.Time}}</td></tr>
      </table>
      {{if .Note}}<p style="margin:16px 0 0;padding:12px;background:#fef3c7;border-radius:8px;font-size:13px;color:#92400e;">{{.Note}}</p>{{end}}
    </div>
    <div style="padding:14px 24px;background:#f9fafb;color:#9ca3af;font-size:12px;">由 StorageCheck 自动发送</div>
  </div>
</body></html>`))

const gb = 1024 * 1024 * 1024

func render(d mailData) string {
	var b strings.Builder
	_ = tpl.Execute(&b, d)
	return b.String()
}

func fmtGB(v uint64) string {
	return fmt.Sprintf("%.1f", float64(v)/gb)
}

// SendAlert 发送磁盘空间不足告警。
func (m *Mailer) SendAlert(host, driveID, label string, total, free uint64, usedPct, freePct, threshold float64, escalated bool) error {
	title := "磁盘空间不足告警"
	note := "请及时清理该磁盘上的文件，或转移不常用数据。"
	if escalated {
		title = "磁盘空间持续不足提醒"
		note = "该磁盘仍处于告警状态且已超过重复提醒间隔，本邮件为再次提醒。"
	}
	return m.Send(fmt.Sprintf("[StorageCheck] %s %s 剩余 %.1f%%", host, driveID, freePct), render(mailData{
		Title:     title,
		Accent:    "#dc2626",
		Hostname:  host,
		Time:      time.Now().Format("2006-01-02 15:04:05"),
		DriveID:   driveID,
		Label:     label,
		TotalGB:   fmtGB(total),
		FreeGB:    fmtGB(free),
		UsedPct:   fmt.Sprintf("%.1f", usedPct),
		FreePct:   fmt.Sprintf("%.1f", freePct),
		Threshold: fmt.Sprintf("%.0f", threshold),
		Note:      note,
	}))
}

// SendRecovery 发送空间恢复通知。
func (m *Mailer) SendRecovery(host, driveID, label string, total, free uint64, usedPct, freePct, threshold float64) error {
	return m.Send(fmt.Sprintf("[StorageCheck] %s %s 空间已恢复", host, driveID), render(mailData{
		Title:     "磁盘空间已恢复",
		Accent:    "#16a34a",
		Hostname:  host,
		Time:      time.Now().Format("2006-01-02 15:04:05"),
		DriveID:   driveID,
		Label:     label,
		TotalGB:   fmtGB(total),
		FreeGB:    fmtGB(free),
		UsedPct:   fmt.Sprintf("%.1f", usedPct),
		FreePct:   fmt.Sprintf("%.1f", freePct),
		Threshold: fmt.Sprintf("%.0f", threshold),
		Note:      "该磁盘剩余空间已回到阈值以上，告警状态解除。",
	}))
}

// SendTest 发送配置验证邮件。
func (m *Mailer) SendTest(host string) error {
	return m.Send("[StorageCheck] SMTP 配置测试", render(mailData{
		Title:    "SMTP 配置成功",
		Accent:   "#2563eb",
		Hostname: host,
		Time:     time.Now().Format("2006-01-02 15:04:05"),
		Server:   fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port),
		Account:  m.cfg.User,
		Note:     "这是一封测试邮件，说明 SMTP 服务器、账号与收件人配置均可正常使用。",
	}))
}
