//go:build linux

package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbletea"

	"storagecheck/internal/autostart"
	"storagecheck/internal/state"
)

type servicePage struct {
	a       *app
	active  bool
	enabled bool
	status  string
	logs    string
	pid     int
	running bool
}

func (p *servicePage) title() string { return "服务状态" }

func (p *servicePage) keys() []string {
	return []string{"r 刷新", "s 重载配置", "c 立即检查", "space 开机自启开关", "esc 返回"}
}

func (p *servicePage) dirty() bool         { return false }
func (p *servicePage) save() (bool, error) { return false, nil }

func (p *servicePage) enter() tea.Cmd {
	return tea.Batch(p.reload(), refreshEvery(15*time.Second))
}

func (p *servicePage) reload() tea.Cmd {
	active, enabled, err := autostart.UnitStatus()
	p.active, p.enabled = active, enabled
	if err != nil {
		p.status = err.Error()
	} else {
		p.status = ""
	}
	pid, running := p.a.b.daemonPID()
	p.pid, p.running = pid, running
	p.logs = journalTail(12)
	return nil
}

func (p *servicePage) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case refreshMsg:
		return p.reload()
	case tea.KeyMsg:
		switch m.String() {
		case "r":
			return p.reload()
		case "s":
			if err := autostart.ReloadService(); err != nil {
				// 容器或无 systemd 环境下仍能直接给进程发信号
				if p.a.b.notify(syscall.SIGHUP) {
					p.a.notice = "已向进程发送 SIGHUP 重载配置"
					return p.reload()
				}
				p.a.err = err.Error()
				return nil
			}
			p.a.notice = "服务已重新加载配置"
			return p.reload()
		case "c":
			if !p.a.b.notify(syscall.SIGUSR2) {
				p.a.err = "服务未在运行，无法请求立即检查"
				return nil
			}
			p.a.notice = "已请求立即检查"
			return p.reload()
		case " ":
			if err := p.toggleAutostart(); err != nil {
				p.a.err = err.Error()
				return nil
			}
			return p.reload()
		}
	}
	return nil
}

// toggleAutostart 改的是 systemd 的 enable 状态（不是配置文件里的布尔值）：
// 与 Windows 用注册表作为事实来源的做法一致。
func (p *servicePage) toggleAutostart() error {
	m := autostart.NewSystemd(p.a.b.dir)
	exe, err := exec.LookPath("storagecheck")
	if err != nil {
		exe = "/usr/bin/storagecheck"
	}
	if p.enabled {
		p.a.notice = "已关闭开机自启"
		return m.Disable()
	}
	p.a.notice = "已开启开机自启"
	return m.Enable(exe, []string{"run"})
}

func (p *servicePage) view(width int) string {
	run := bad.Render("未运行")
	if p.running {
		run = good.Render(fmt.Sprintf("运行中（pid %d）", p.pid))
	}
	active := bad.Render("未激活")
	if p.active {
		active = good.Render("已激活")
	}
	boot := dim.Render("关闭")
	if p.enabled {
		boot = good.Render("开启")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "systemd 服务：%s\n", active)
	fmt.Fprintf(&b, "锁文件进程：%s\n", run)
	fmt.Fprintf(&b, "开机自启　：%s（space 切换）\n", boot)
	fmt.Fprintf(&b, "配置目录　：%s\n", field.Render(p.a.b.dir))
	fmt.Fprintf(&b, "状态文件　：%s\n", field.Render(state.StatePath(p.a.b.dataDir)))
	if p.status != "" {
		fmt.Fprintf(&b, "\n%s\n", warn.Render(p.status))
	}
	fmt.Fprintf(&b, "\n%s\n", section.Render("最近日志"))
	if p.logs == "" {
		fmt.Fprintf(&b, "%s\n", dim.Render("（读不到 journalctl 输出）"))
	} else {
		fmt.Fprintf(&b, "%s\n", dim.Render(p.logs))
	}
	return b.String()
}

func journalTail(lines int) string {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return ""
	}
	out, _ := exec.Command("journalctl", "-u", "storagecheck", "-n", fmt.Sprintf("%d", lines), "--no-pager", "-o", "cat").Output()
	s := strings.TrimRight(string(out), "\n")
	// journal 的每行前面已带时间戳；容器里常是「无数据」占位句
	if strings.Contains(s, "-- No entries --") {
		return ""
	}
	return s
}
