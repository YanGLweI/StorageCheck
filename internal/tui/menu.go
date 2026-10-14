//go:build linux

package tui

import (
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbletea"
)

type menuItem struct {
	title, desc string
	page        pageID
	quit        bool
}

func (m menuItem) Title() string       { return m.title }
func (m menuItem) Description() string { return m.desc }
func (m menuItem) FilterValue() string { return m.title }

type menuPage struct {
	a    *app
	list list.Model
}

func (p *menuPage) title() string { return "StorageCheck 磁盘监控配置" }

func (p *menuPage) keys() []string {
	return []string{"↑↓ 选择", "enter 进入", "q 退出"}
}

func (p *menuPage) dirty() bool { return false }

func (p *menuPage) save() (bool, error) { return false, nil }

func (p *menuPage) enter() tea.Cmd {
	items := []list.Item{
		menuItem{"监控总览", "各挂载点容量、剩余与告警状态", pageOverview, false},
		menuItem{"磁盘配置", "选择监控哪些磁盘、设置阈值", pageDrives, false},
		menuItem{"告警设置", "检查间隔、冷却时间、恢复通知", pageAlerts, false},
		menuItem{"邮件设置", "SMTP 服务器、授权码与收件人", pageSMTP, false},
		menuItem{"事件日志", "最近的告警、恢复与错误", pageEvents, false},
		menuItem{"服务状态", "systemd 运行与开机自启", pageService, false},
		menuItem{"退出", "关闭配置界面（不影响后台服务）", pageMenu, true},
	}
	// 每项两行（标题 + 说明），行间距置 0 才能让 7 项在 24 行终端里全部可见，
	// 不用翻页 —— nmtui 的使用者通常一眼要看全所有入口。
	d := list.NewDefaultDelegate()
	d.SetSpacing(0)
	l := list.New(items, d, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	// bubbles 的 list 把 esc 也绑成了 Quit；主菜单上按 esc 本该什么都不做，
	// 退出交给下面的 "q" 和全局 ctrl+c，所以要关掉组件自带的退出键。
	l.DisableQuitKeybindings()
	l.SetStatusBarItemName("项", "项")
	p.list = l
	return nil
}

func (p *menuPage) update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.KeyMsg:
		if m.String() == "q" {
			return tea.Quit
		}
		if m.String() == "enter" {
			item, ok := p.list.SelectedItem().(menuItem)
			if !ok {
				return nil
			}
			if item.quit {
				return tea.Quit
			}
			return p.a.gotoPage(item.page)
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	return cmd
}

func (p *menuPage) view(width int) string {
	// 列表宽度取终端一半左右，高度扣掉标题与底部提示
	h := p.a.height - 8
	if h < 5 {
		h = 5
	}
	w := width / 2
	if w < 30 {
		w = minInt(30, width)
	}
	p.list.SetSize(w, h)
	return p.list.View() + "\n\n" + dim.Render("所有改动保存后立即生效，不需要重启服务。")
}
