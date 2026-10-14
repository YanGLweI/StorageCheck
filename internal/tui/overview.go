//go:build linux

package tui

import (
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"storagecheck/internal/model"
	"storagecheck/internal/monitor"
)

type refreshMsg struct{}

// refreshEvery 让总览页按节奏自更新，省掉一个常驻 goroutine。
func refreshEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return refreshMsg{} })
}

type overviewPage struct {
	a       *app
	snap    monitor.StateView
	offline bool
	err     string
}

func (p *overviewPage) title() string { return "监控总览" }

func (p *overviewPage) keys() []string {
	return []string{"r 刷新", "c 立即检查", "p 暂停/继续", "esc 返回"}
}

func (p *overviewPage) dirty() bool         { return false }
func (p *overviewPage) save() (bool, error) { return false, nil }

func (p *overviewPage) enter() tea.Cmd {
	return tea.Batch(p.reload(), refreshEvery(5*time.Second))
}

func (p *overviewPage) afterSave() tea.Cmd { return p.reload() }

func (p *overviewPage) reload() tea.Cmd {
	snap, offline, err := p.a.b.snapshot()
	p.offline = offline
	if err != nil {
		p.snap, p.err = monitor.StateView{}, err.Error()
	} else {
		p.snap, p.err = snap, ""
	}
	return nil
}

func (p *overviewPage) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case refreshMsg:
		p.reload()
		return refreshEvery(5 * time.Second)
	case tea.KeyMsg:
		switch m.String() {
		case "r":
			return p.reload()
		case "c":
			if !p.a.b.running() {
				p.a.err = "服务未在运行，无法请求立即检查"
				return nil
			}
			p.a.b.notify(syscall.SIGUSR2)
			p.a.notice = "已请求立即检查"
			return p.reload()
		case "p":
			if !p.a.b.running() {
				p.a.err = "服务未在运行，无法切换暂停状态"
				return nil
			}
			p.a.b.notify(syscall.SIGUSR1)
			return p.reload()
		}
	}
	return nil
}

func (p *overviewPage) view(width int) string {
	if p.err != "" {
		return errText.Render("读取状态失败：" + p.err)
	}
	v := p.snap
	var b strings.Builder
	fmt.Fprintf(&b, "主机 %s · 版本 %s · 快照时间 %s\n",
		orDash(v.Hostname), orDash(v.Version), v.UpdatedAt.Format("2006-01-02 15:04:05"))
	if v.Paused {
		b.WriteString(warn.Render("监控已暂停，不会产生新告警") + "\n")
	}
	if p.offline {
		b.WriteString(dim.Render("服务未运行或快照过期，以下为即时采集结果（告警态可能缺失）") + "\n")
	}
	b.WriteString("\n")
	if len(v.Drives) == 0 {
		b.WriteString(dim.Render("还没有被监控的磁盘，请到「磁盘配置」中启用。") + "\n")
		return b.String()
	}
	cols, widths := []string{"挂载点", "卷标", "容量", "剩余", "已用", "阈值", "状态"}, wideWidths
	if width > 0 && width < 74 {
		cols, widths = []string{"挂载点", "容量", "剩余", "阈值", "状态"}, narrowWidths
	}
	full := len(cols) == 7
	b.WriteString(section.Render(row(width, widths, cols...)) + "\n")
	b.WriteString(dim.Render(strings.Repeat("─", clampWidth(width, 82))) + "\n")
	for _, d := range v.Drives {
		status := "正常"
		switch {
		case d.Alerting:
			status = "告警中"
		case !d.Enabled:
			status = "未监控"
		}
		thr := "-"
		if d.ThresholdPct > 0 {
			thr = fmt.Sprintf("≤%.0f%%", d.ThresholdPct)
		}
		cells := []string{d.ID}
		if full {
			cells = append(cells, d.Label)
		}
		cells = append(cells, model.HumanBytes(d.Total), model.HumanBytes(d.Free))
		if full {
			cells = append(cells, fmt.Sprintf("%.1f%%", d.UsedPct))
		}
		cells = append(cells, thr, stateColor(status, d.Alerting, d.Enabled))
		b.WriteString(row(width, widths, cells...) + "\n")
	}
	if n := countAlerts(v); n > 0 {
		b.WriteString("\n" + bad.Render(fmt.Sprintf("%d 块磁盘处于告警状态", n)) + "\n")
	}
	return b.String()
}

// 列宽档位：宽终端给全部列，窄终端砍掉卷标与已用——已用能从剩余/容量推出来，
// 卷标只是装饰，砍掉它们才能保住最有用的阈值和状态列。
var (
	wideWidths   = []int{20, 14, 9, 9, 7, 7, 6}
	narrowWidths = []int{18, 9, 9, 7, 6}
)

// row 按固定列宽拼接并留出一格列间距。补齐宽度与内容无关，表头和数据行才会
// 落在同一格网格上；总宽超出终端时压缩首列：挂载点最长，也最能让位。
func row(width int, widths []int, cols ...string) string {
	w := make([]int, len(widths))
	copy(w, widths)
	used := maxInt(0, len(cols)-1)
	for i, c := range cols {
		if i < len(w) {
			used += w[i]
			continue
		}
		used += lipgloss.Width(c)
	}
	if width > 0 && used > width {
		w[0] = maxInt(10, w[0]-(used-width))
	}
	parts := make([]string, 0, len(cols))
	for i, c := range cols {
		if i < len(w) {
			parts = append(parts, cell(c, w[i]))
			continue
		}
		parts = append(parts, c)
	}
	return strings.TrimRight(strings.Join(parts, " "), " ")
}

func clampWidth(w, max int) int {
	if w <= 0 || w > max {
		return max
	}
	return w
}

func countAlerts(v monitor.StateView) int {
	n := 0
	for _, d := range v.Drives {
		if d.Alerting {
			n++
		}
	}
	return n
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
