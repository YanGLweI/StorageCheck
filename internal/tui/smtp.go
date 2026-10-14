//go:build linux

package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbletea"

	"storagecheck/internal/mailer"
)

type smtpPreset struct {
	key, name, host, security string
	port                      int
}

// smtpPresets 与 Web 控制台保持同一组常用服务商，切平台时不用再查端口。
var smtpPresets = []smtpPreset{
	{"qq", "QQ 邮箱", "smtp.qq.com", "tls", 465},
	{"163", "网易 163", "smtp.163.com", "tls", 465},
	{"exmail", "腾讯企业邮", "smtp.exmail.qq.com", "tls", 465},
	{"aliyun", "阿里企业邮", "smtp.mxhichina.com", "tls", 465},
	{"gmail", "Gmail", "smtp.gmail.com", "tls", 465},
	{"outlook", "Office365", "smtp.office365.com", "starttls", 587},
	{"custom", "自定义", "", "tls", 465},
}

var securityOptions = []struct {
	key, name string
	port      int
}{
	{"tls", "TLS（隐式，端口通常 465）", 465},
	{"starttls", "STARTTLS（端口通常 587）", 587},
	{"none", "不加密（内网中继，端口 25）", 25},
}

type smtpPage struct {
	a *app
	f form
}

func (p *smtpPage) title() string { return "邮件设置" }

func (p *smtpPage) keys() []string {
	return []string{"↑↓ 选择", "←→ 切换选项", "ctrl+t 发送测试邮件", "ctrl+s 保存", "esc 返回"}
}

func (p *smtpPage) dirty() bool { return p.f.dirty }

func (p *smtpPage) save() (bool, error) {
	if err := p.f.save(false); err != nil {
		return true, err
	}
	return true, p.a.b.commit()
}

func (p *smtpPage) enter() tea.Cmd {
	p.f = form{a: p.a}
	p.f.fields = []formField{
		{
			label:   "服务商预设",
			help:    "选择后自动填入服务器与端口，仍可手工改",
			kind:    kindChoice,
			options: presetNames(),
			index:   func() int { return presetIndex(p.a.b.cfg.SMTP.Preset) },
			setIndex: func(i int) {
				pr := smtpPresets[i]
				c := &p.a.b.cfg.SMTP
				c.Preset, c.Security = pr.key, pr.security
				if pr.host != "" {
					c.Host = pr.host
				}
				if pr.port > 0 {
					c.Port = pr.port
				}
				p.syncInputs()
			},
		},
		{
			label: "SMTP 服务器",
			kind:  kindText,
			text:  func() string { return p.a.b.cfg.SMTP.Host },
			save: func(raw string) error {
				p.a.b.cfg.SMTP.Host = raw
				return nil
			},
		},
		{
			label: "端口",
			kind:  kindNumber,
			text:  func() string { return itoa(p.a.b.cfg.SMTP.Port) },
			save: func(raw string) error {
				v, err := parseInt(raw)
				if err != nil {
					return err
				}
				if v <= 0 || v > 65535 {
					return fmt.Errorf("端口范围 1-65535")
				}
				p.a.b.cfg.SMTP.Port = v
				return nil
			},
		},
		{
			label:   "加密方式",
			help:    "QQ/163 用 TLS(465)；Office365 用 STARTTLS(587)",
			kind:    kindChoice,
			options: securityNames(),
			index:   func() int { return securityIndex(p.a.b.cfg.SMTP.Security) },
			setIndex: func(i int) {
				p.a.b.cfg.SMTP.Security = securityOptions[i].key
				p.a.b.cfg.SMTP.Port = securityOptions[i].port
				p.syncInputs()
			},
		},
		{
			label: "登录账号",
			help:  "通常是完整邮箱地址",
			kind:  kindText,
			text:  func() string { return p.a.b.cfg.SMTP.User },
			save: func(raw string) error {
				p.a.b.cfg.SMTP.User = raw
				return nil
			},
		},
		{
			label: "授权码",
			help:  "邮箱里生成的 SMTP 授权码，不是登录密码；留空表示不修改",
			kind:  kindSecret,
			save: func(raw string) error {
				enc, err := p.a.b.store.SealSecret(raw)
				if err != nil {
					return fmt.Errorf("加密授权码失败: %w", err)
				}
				p.a.b.cfg.SMTP.PassEnc = enc
				return nil
			},
		},
		{
			label: "发件人别名",
			kind:  kindText,
			text:  func() string { return p.a.b.cfg.SMTP.FromAlias },
			save: func(raw string) error {
				p.a.b.cfg.SMTP.FromAlias = raw
				return nil
			},
		},
		{
			label: "收件人",
			help:  "多个地址用逗号或空格分隔",
			kind:  kindText,
			text:  func() string { return strings.Join(p.a.b.cfg.SMTP.Recipients, ", ") },
			save: func(raw string) error {
				list := splitList(raw)
				if len(list) == 0 {
					return fmt.Errorf("至少填一个收件人")
				}
				p.a.b.cfg.SMTP.Recipients = list
				return nil
			},
		},
	}
	p.f.footer = func() string {
		c := p.a.b.cfg.SMTP
		// 状态行取输入框里的值：刚敲完还没保存时也要对上屏幕上看到的内容，
		// 否则页脚说「收件人 0 个」而发信又用的是这些地址，自相矛盾。
		sealed := c.PassEnc != "" || p.f.value("授权码") != ""
		pass := dim.Render("未设置")
		if sealed {
			pass = good.Render("已设置")
		}
		return fmt.Sprintf("授权码：%s · 收件人：%d 个\n%s",
			pass, len(splitList(p.f.value("收件人"))),
			dim.Render("授权码以 AES-GCM 加密存于配置目录，输入时不回显，也不会出现在命令行参数里"))
	}
	p.f.onKey = func(msg tea.KeyMsg) (bool, tea.Cmd) {
		// ctrl+t 在任何行都能用；单键 t 只在选项行生效，
		// 否则 smtp 主机名里那个 t 永远打不出来。
		switch msg.String() {
		case "ctrl+t", "t":
			return true, p.testMail()
		}
		return false, nil
	}
	cmd := p.f.init()
	p.syncInputs()
	return cmd
}

// syncInputs 让预设/加密方式切换后的新值马上显示，
// 否则要退出重进才能看到填好的服务器地址。
func (p *smtpPage) syncInputs() {
	for i := range p.f.fields {
		fd := &p.f.fields[i]
		if fd.kind == kindChoice || fd.text == nil {
			continue
		}
		fd.input.SetValue(fd.text())
	}
}

func (p *smtpPage) testMail() tea.Cmd {
	host, _ := os.Hostname()
	// 试发要用屏幕上刚填的值：先落到内存（不落盘），
	// 否则填完收件人按 ctrl+t 仍被「请先填写收件人」挡住，非得先 ctrl+s。
	if err := p.f.save(true); err != nil {
		msg := err.Error()
		return func() tea.Msg { return errMsg(msg) }
	}
	return func() tea.Msg {
		cfg := p.a.b.cfg
		if len(cfg.SMTP.Recipients) == 0 {
			return errMsg("请先填写收件人")
		}
		pass, err := p.a.b.store.OpenSecret(cfg.SMTP.PassEnc)
		if err != nil {
			return errMsg("读取授权码失败: " + err.Error())
		}
		if pass == "" {
			return errMsg("请先设置授权码")
		}
		if err := mailer.New(cfg.SMTP, pass).SendTest(host); err != nil {
			return errMsg("测试邮件发送失败: " + err.Error())
		}
		return noticeMsg("测试邮件已发出，请查收（含垃圾箱）")
	}
}

func (p *smtpPage) update(msg tea.Msg) tea.Cmd { return p.f.update(msg) }
func (p *smtpPage) view(width int) string      { return p.f.view(width) }

func presetNames() []string {
	out := make([]string, len(smtpPresets))
	for i, p := range smtpPresets {
		out[i] = p.name
	}
	return out
}

func presetIndex(key string) int {
	for i, p := range smtpPresets {
		if p.key == key {
			return i
		}
	}
	return len(smtpPresets) - 1
}

func securityNames() []string {
	out := make([]string, len(securityOptions))
	for i, s := range securityOptions {
		out[i] = s.name
	}
	return out
}

func securityIndex(key string) int {
	for i, s := range securityOptions {
		if s.key == key {
			return i
		}
	}
	return 0
}

func splitList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func itoa(v int) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprintf("%d", v)
}
