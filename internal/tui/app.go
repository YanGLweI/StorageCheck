//go:build linux

package tui

import (
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/sys/unix"

	"storagecheck/internal/config"
)

type pageID int

const (
	pageMenu pageID = iota
	pageOverview
	pageDrives
	pageAlerts
	pageSMTP
	pageEvents
	pageService
)

// page 是拟态界面的一个页面；所有页面共享 backend 与同一套按键约定。
type page interface {
	title() string
	enter() tea.Cmd
	update(msg tea.Msg) tea.Cmd
	view(width int) string
	dirty() bool
	// keys 提供底部快捷键条。
	keys() []string
	// save 写回配置；handled=false 表示该页没有可保存的内容（主菜单、总览）。
	save() (handled bool, err error)
}

type (
	noticeMsg string
	errMsg    string
	busyMsg   bool
)

// escClaimer 让有内部层级的页面先消化 esc，避免一按就跳回主菜单。
type escClaimer interface {
	claimEsc() (cmd tea.Cmd, claimed bool)
}

type app struct {
	b       *backend
	pages   map[pageID]page
	current pageID

	width, height int
	ready         bool

	notice   string
	err      string
	busy     bool
	lastTick time.Time

	// 返回主菜单时若有未保存改动，第一次 esc 只提示，第二次才放弃，
	// 免得一次误触把刚改的阈值丢掉。
	warnDiscard bool
}

// Run 打开终端拟态界面。out 用于把 TUI 输出定向到指定流（测试时可丢弃）。
func Run(out io.Writer, dir, dataDir, version string) error {
	if err := checkWritable(dir); err != nil {
		return err
	}
	b, err := newBackend(dir, dataDir, version)
	if err != nil {
		return err
	}
	p := tea.NewProgram(newApp(b), tea.WithOutput(out), tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// checkWritable 提前给出可读错误：非 root 打开界面只能看不能存，
// 与其让用户改完才发现，不如一开始就提示用 sudo。
func checkWritable(dir string) error {
	if err := unix.Access(dir, unix.W_OK); err != nil {
		return fmt.Errorf("配置目录 %s 当前用户不可写，请用 `sudo storagecheck` 打开配置界面", dir)
	}
	return nil
}

func newApp(b *backend) *app {
	a := &app{b: b, pages: map[pageID]page{}, current: pageMenu}
	a.pages[pageMenu] = &menuPage{a: a}
	a.pages[pageOverview] = &overviewPage{a: a}
	a.pages[pageDrives] = &drivesPage{a: a}
	a.pages[pageAlerts] = &alertsPage{a: a}
	a.pages[pageSMTP] = &smtpPage{a: a}
	a.pages[pageEvents] = &eventsPage{a: a}
	a.pages[pageService] = &servicePage{a: a}
	return a
}

func (a *app) page() page { return a.pages[a.current] }

func (a *app) gotoPage(id pageID) tea.Cmd {
	if id != a.current {
		if p, ok := a.pages[a.current]; ok && p.dirty() {
			a.discard()
		}
	}
	a.current = id
	a.warnDiscard = false
	p := a.pages[id]
	return p.enter()
}

func (a *app) discard() {
	a.b.discard()
	a.notice = "已放弃未保存的改动"
}

func (a *app) Init() tea.Cmd {
	return a.pages[pageMenu].enter()
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		a.ready = true
		return a, nil
	case noticeMsg:
		a.notice, a.busy = string(m), false
		return a, nil
	case errMsg:
		a.err, a.busy = string(m), false
		return a, nil
	case busyMsg:
		a.busy = bool(m)
		return a, nil
	case tea.KeyMsg:
		return a, a.handleKey(m)
	}
	if p := a.page(); p != nil {
		return a, p.update(msg)
	}
	return a, nil
}

func (a *app) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	// 任何按键都视为「看过上一条提示」，清屏由下一次渲染完成
	a.err, a.notice = "", ""
	switch key {
	case "ctrl+c":
		return tea.Quit
	case "ctrl+s":
		handled, err := a.page().save()
		if err != nil {
			a.err = err.Error()
			return nil
		}
		if handled {
			a.warnDiscard = false
			return a.reloadAfterSave()
		}
		return nil
	}

	if a.current != pageMenu {
		switch key {
		case "esc":
			// 不绑 h：文本行里的 h 要先给输入框，返回统一用 esc
			// 页面内部还有层级时（磁盘列表 ↔ 编辑表单），先由页面自己退一层
			if p, ok := a.page().(escClaimer); ok {
				if cmd, claimed := p.claimEsc(); claimed {
					a.warnDiscard = false
					return cmd
				}
			}
			if a.page().dirty() && !a.warnDiscard {
				a.warnDiscard = true
				a.notice = "有未保存改动：ctrl+s 保存，再按 esc 放弃"
				return nil
			}
			if a.warnDiscard {
				a.discard()
			}
			a.warnDiscard = false
			return a.gotoPage(pageMenu)
		}
	}

	if p := a.page(); p != nil {
		return p.update(msg)
	}
	return nil
}

// reloadAfterSave 让保存后的界面立刻反映新配置（总览/服务页会重新取快照）。
func (a *app) reloadAfterSave() tea.Cmd {
	a.notice = "已保存并通知服务热重载"
	if p, ok := a.page().(interface{ afterSave() tea.Cmd }); ok {
		return p.afterSave()
	}
	return nil
}

func (a *app) View() string {
	if !a.ready {
		return "正在初始化界面…"
	}
	body := fitLine(a.page().view(a.width), a.width)
	head := fitLine(pageTitle.Render(a.page().title())+"  "+dim.Render(a.b.dir), a.width)
	if a.busy {
		head += "  " + warn.Render("处理中…")
	}
	lines := []string{head, "", body}
	footer := fitLine(a.footer(), a.width)
	if footer != "" {
		lines = append(lines, "", footer)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// fitLine 按显示宽度截断。终端只要有一行超出宽度就会折行，
// Bubble Tea 的行数假设随之失效，整个界面会往上滚并留下残影。
func fitLine(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

func (a *app) footer() string {
	switch {
	case a.err != "":
		return errText.Render("✗ " + a.err)
	case a.notice != "":
		return toast.Render("✓ " + a.notice)
	case a.b.configError != "":
		// 占位代替快捷键条：行数不变，宽终端下也不会把提示挤出屏幕。
		return warn.Render("⚠ 配置读取失败，界面显示的是默认值；改好后 ctrl+s 保存即覆盖修复")
	default:
		return helpKeys(a.width, a.page().keys()...)
	}
}

func (a *app) currentCfg() *config.Config { return a.b.cfg }
