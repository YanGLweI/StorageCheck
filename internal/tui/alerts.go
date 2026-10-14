//go:build linux

package tui

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbletea"
)

type alertsPage struct {
	a *app
	f form
}

func (p *alertsPage) title() string { return "告警设置" }

func (p *alertsPage) keys() []string {
	return []string{"↑↓ 选择", "←→/空格 切换", "输入框直接改数字", "ctrl+s 保存", "esc 返回"}
}

func (p *alertsPage) dirty() bool { return p.f.dirty }

func (p *alertsPage) save() (bool, error) {
	if err := p.f.save(false); err != nil {
		return true, err
	}
	return true, p.a.b.commit()
}

func (p *alertsPage) enter() tea.Cmd {
	p.f = form{a: p.a}
	p.f.fields = []formField{
		{
			label: "检查间隔（秒）",
			help:  "最小 30 秒；采样很轻，但对生产机来说没必要更频繁",
			kind:  kindNumber,
			text:  func() string { return strconv.Itoa(p.a.b.cfg.CheckIntervalSec) },
			save: func(raw string) error {
				v, err := parseInt(raw)
				if err != nil {
					return err
				}
				p.a.b.cfg.CheckIntervalSec = maxInt(30, v)
				return nil
			},
		},
		{
			label: "告警冷却（秒）",
			help:  "同一块盘两次告警的最小间隔，超过后发升级提醒",
			kind:  kindNumber,
			text:  func() string { return strconv.Itoa(p.a.b.cfg.CooldownSec) },
			save: func(raw string) error {
				v, err := parseInt(raw)
				if err != nil {
					return err
				}
				p.a.b.cfg.CooldownSec = maxInt(60, v)
				return nil
			},
		},
		{
			label: "恢复迟滞（百分点）",
			help:  "剩余需高于「阈值 + 迟滞」才算恢复，防止在阈值线上反复抖动",
			kind:  kindNumber,
			text:  func() string { return formatFloat(p.a.b.cfg.RecoveryHysteresisPct) },
			save: func(raw string) error {
				v, err := strconv.ParseFloat(raw, 64)
				if err != nil {
					return fmt.Errorf("只能填数字")
				}
				if v < 0 || v > 50 {
					return fmt.Errorf("范围 0-50")
				}
				p.a.b.cfg.RecoveryHysteresisPct = v
				return nil
			},
		},
		{
			label: "恢复通知",
			help:  "磁盘回到安全水位时是否再发一封邮件",
			kind:  kindChoice,
			options: []string{
				"发送",
				"不发送",
			},
			index: func() int {
				if p.a.b.cfg.NotifyOnRecovery {
					return 0
				}
				return 1
			},
			setIndex: func(i int) { p.a.b.cfg.NotifyOnRecovery = i == 0 },
		},
		{
			label: "新磁盘处理",
			help:  "服务器上新出现的挂载点：自动纳入会带来告警风暴，默认只提示",
			kind:  kindChoice,
			options: []string{
				"只记录事件，手工启用",
				"自动纳入监控",
			},
			index: func() int {
				if p.a.b.cfg.AutoEnableNewDrives() {
					return 1
				}
				return 0
			},
			setIndex: func(i int) {
				v := i == 1
				p.a.b.cfg.AutoEnableNew = &v
			},
		},
		{
			label: "容量下限（GiB）",
			help:  "小于该容量的挂载点忽略，过滤 EFI 分区、内存盘等",
			kind:  kindNumber,
			text:  func() string { return strconv.FormatUint(p.a.b.cfg.MinTotal()>>30, 10) },
			save: func(raw string) error {
				v, err := parseInt(raw)
				if err != nil {
					return err
				}
				if v <= 0 {
					return fmt.Errorf("需大于 0")
				}
				p.a.b.cfg.Linux.MinTotalBytes = uint64(v) << 30
				return nil
			},
		},
	}
	p.f.footer = func() string {
		c := p.a.b.cfg
		return dim.Render(fmt.Sprintf("生效预览：每 %s 检查一次；同一块盘两次告警至少间隔 %s；恢复需高于阈值 %s 个百分点",
			secondsText(c.CheckIntervalSec), secondsText(c.CooldownSec), formatFloat(c.RecoveryHysteresisPct)))
	}
	return p.f.init()
}

func (p *alertsPage) update(msg tea.Msg) tea.Cmd { return p.f.update(msg) }
func (p *alertsPage) view(width int) string      { return p.f.view(width) }

func parseInt(raw string) (int, error) {
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("只能填整数")
	}
	return v, nil
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// secondsText 把秒数说成人话，避免界面上一堆 21600 之类的数字。
func secondsText(sec int) string {
	switch {
	case sec >= 86400 && sec%86400 == 0:
		return fmt.Sprintf("%d 天", sec/86400)
	case sec >= 3600 && sec%3600 == 0:
		return fmt.Sprintf("%d 小时", sec/3600)
	case sec >= 60 && sec%60 == 0:
		return fmt.Sprintf("%d 分钟", sec/60)
	default:
		return fmt.Sprintf("%d 秒", sec)
	}
}
