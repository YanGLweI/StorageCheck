//go:build linux

package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"storagecheck/internal/model"
	"storagecheck/internal/state"
)

const eventsWindow = 300

type eventsPage struct {
	a      *app
	events []model.Event
	err    string
	top    int // 滚动窗口首行
}

func (p *eventsPage) title() string { return "事件日志" }

func (p *eventsPage) keys() []string {
	return []string{"↑↓ 滚动", "r 刷新", "esc 返回"}
}

func (p *eventsPage) dirty() bool         { return false }
func (p *eventsPage) save() (bool, error) { return false, nil }

func (p *eventsPage) enter() tea.Cmd {
	p.reload()
	return refreshEvery(10 * time.Second)
}

func (p *eventsPage) afterSave() tea.Cmd {
	p.reload()
	return nil
}

func (p *eventsPage) reload() {
	evs, err := state.Events(p.a.b.dataDir, eventsWindow)
	if err != nil {
		p.err = err.Error()
		return
	}
	p.err, p.events, p.top = "", evs, 0
}

func (p *eventsPage) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case refreshMsg:
		p.reload()
		return refreshEvery(10 * time.Second)
	case tea.KeyMsg:
		switch m.String() {
		case "r":
			p.reload()
		case "up", "k":
			p.top = maxInt(0, p.top-1)
		case "down", "j":
			p.top = minInt(maxInt(0, len(p.events)-1), p.top+1)
		}
	}
	return nil
}

func (p *eventsPage) view(width int) string {
	if p.err != "" {
		return errText.Render("读取事件失败：" + p.err)
	}
	if len(p.events) == 0 {
		return dim.Render("暂无事件记录。服务启动、告警、恢复都会写在这里。\n文件：" + state.EventsPath(p.a.b.dataDir))
	}
	// 可见行数受终端高度限制，留出头尾各几行给标题与快捷键条
	rowsLeft := maxInt(2, p.a.height-10)
	msgWidth := clampWidth(width, 80) - 4
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", dim.Render(fmt.Sprintf("最近 %d 条（倒序），文件 %s", len(p.events), state.EventsPath(p.a.b.dataDir))))
	if p.top > 0 {
		fmt.Fprintf(&b, "%s\n", dim.Render(fmt.Sprintf("… 上方还有 %d 条", p.top)))
	}
	shown := 0
	for _, e := range p.events[p.top:] {
		if rowsLeft < 2 {
			break
		}
		style := dim
		switch e.Kind {
		case model.KindAlert:
			style = bad
		case model.KindRecover:
			style = good
		case model.KindError:
			style = warn
		}
		// 消息单独占一行：和元信息挤在同一行只剩三十几格，中文说明会被切掉。
		fmt.Fprintf(&b, "%s %s %s\n    %s\n",
			cell(e.Time.Format("01-02 15:04:05"), 14),
			cell(model.KindLabel(e.Kind), 6),
			cell(orDash(e.Drive), 20),
			style.Render(truncateCells(firstLine(e.Message), msgWidth)))
		rowsLeft -= 2
		shown++
	}
	if rest := len(p.events) - p.top - shown; rest > 0 {
		fmt.Fprintf(&b, "%s\n", dim.Render(fmt.Sprintf("… 下方还有 %d 条", rest)))
	}
	return b.String()
}

// truncateCells 按终端格子数截断，保留 ANSI 样式收尾。
func truncateCells(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

// firstLine 事件消息可能带多行堆栈，列表里只显示首行。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
