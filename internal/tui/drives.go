//go:build linux

package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbletea"

	"storagecheck/internal/collector"
	"storagecheck/internal/config"
	"storagecheck/internal/model"
)

type driveRow struct {
	id        string
	label     string
	total     uint64
	free      uint64
	enabled   bool
	threshold float64
	// missing 表示配置里有、但这次扫描没出现的挂载点（数据卷改名、U 盘拔掉等）。
	missing bool
	network bool
}

type drivesPage struct {
	a         *app
	rows      []driveRow
	cursor    int
	editing   bool
	threshold textinput.Model
	dirtyFlag bool
}

func (p *drivesPage) title() string {
	if p.editing {
		return "磁盘配置 · 编辑 " + p.selectedID()
	}
	return "磁盘配置"
}

func (p *drivesPage) selectedID() string {
	if d, ok := p.current(); ok {
		return d.id
	}
	return ""
}

func (p *drivesPage) keys() []string {
	if p.editing {
		return []string{"space 启用/停用", "enter 确认阈值", "esc 返回列表"}
	}
	return []string{"↑↓ 选择", "enter 编辑", "space 启用/停用", "a 重新扫描", "n 网络盘开关", "ctrl+s 保存", "esc 返回"}
}

func (p *drivesPage) dirty() bool { return p.dirtyFlag }

func (p *drivesPage) save() (bool, error) {
	p.commitThresholdInput()
	if err := p.a.b.commit(); err != nil {
		return true, err
	}
	p.dirtyFlag = false
	return true, nil
}

func (p *drivesPage) enter() tea.Cmd {
	p.threshold = textinput.New()
	p.threshold.Placeholder = "剩余百分比阈值，如 10"
	p.threshold.CharLimit = 5
	p.rebuild()
	return nil
}

// rebuild 用当前采集结果与配置重新拼列表：配置是事实来源，
// 采集只提供容量与「这块盘现在还在不在」。
func (p *drivesPage) rebuild() {
	cfg := p.a.b.cfg
	rows := make([]driveRow, 0, len(cfg.Drives))
	seen := make(map[string]bool, len(cfg.Drives))

	scan, err := p.a.b.drives()
	if err != nil {
		p.a.err = "扫描挂载点失败：" + err.Error()
	}
	for _, r := range scan {
		if !r.Wanted {
			continue
		}
		d := driveRow{
			id:        r.Mountpoint,
			label:     collector.MountLabel(r.FSType, r.Device, r.VolLabel),
			total:     r.Total,
			free:      r.Free,
			network:   r.Network,
			threshold: config.DefaultThresholdPct,
		}
		if dc, ok := cfg.Drive(r.Mountpoint); ok {
			d.enabled = dc.Enabled
			d.threshold = dc.ThresholdPct
		}
		seen[r.Mountpoint] = true
		rows = append(rows, d)
	}
	// 配置里残留、本次没扫到的条目也要显示：改名/临时卸载时不能让人以为配置丢了
	for _, dc := range cfg.Drives {
		if seen[dc.ID] {
			continue
		}
		// 卷标列只有 14 格，说明文字放不进；状态列已经写明「未挂载」。
		rows = append(rows, driveRow{id: dc.ID, label: "已卸载", missing: true, enabled: dc.Enabled, threshold: dc.ThresholdPct})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	p.rows = rows
	if p.cursor >= len(p.rows) {
		p.cursor = maxInt(0, len(p.rows)-1)
	}
}

func (p *drivesPage) current() (driveRow, bool) {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return driveRow{}, false
	}
	return p.rows[p.cursor], true
}

// commitThresholdInput 写回阈值：编辑页里直接按 ctrl+s 时输入框内容也要生效。
func (p *drivesPage) commitThresholdInput() {
	d, ok := p.current()
	if !ok {
		return
	}
	raw := strings.TrimSpace(p.threshold.Value())
	if raw == "" {
		return
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 || v > 99 {
		p.a.err = "阈值需为 1-99 之间的数字"
		return
	}
	p.setThreshold(d.id, v)
}

func (p *drivesPage) setThreshold(id string, v float64) {
	p.ensureConfigDrive(id)
	if dc, ok := p.a.b.cfg.Drive(id); ok && dc.ThresholdPct != v {
		dc.ThresholdPct = v
		p.dirtyFlag = true
	}
}

func (p *drivesPage) setEnabled(id string, enabled bool) {
	p.ensureConfigDrive(id)
	if dc, ok := p.a.b.cfg.Drive(id); ok && dc.Enabled != enabled {
		dc.Enabled = enabled
		p.dirtyFlag = true
	}
}

// ensureConfigDrive 让新勾选的挂载点在配置里占一行，默认阈值 10%。
func (p *drivesPage) ensureConfigDrive(id string) {
	cfg := p.a.b.cfg
	if _, ok := cfg.Drive(id); ok {
		return
	}
	cfg.Drives = append(cfg.Drives, config.DriveConfig{ID: id, ThresholdPct: config.DefaultThresholdPct})
	p.dirtyFlag = true
}

func (p *drivesPage) update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(tea.KeyMsg); ok {
		if p.editing {
			return p.updateEditing(m)
		}
		return p.updateList(m)
	}
	if p.editing {
		var cmd tea.Cmd
		p.threshold, cmd = p.threshold.Update(msg)
		return cmd
	}
	return nil
}

func (p *drivesPage) updateList(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		p.cursor = maxInt(0, p.cursor-1)
	case "down", "j":
		p.cursor = minInt(len(p.rows)-1, p.cursor+1)
	case "enter":
		d, ok := p.current()
		if !ok {
			return nil
		}
		p.commitThresholdInput()
		p.editing = true
		p.threshold.SetValue(strconv.FormatFloat(d.threshold, 'f', -1, 64))
		return p.threshold.Focus()
	case " ":
		d, ok := p.current()
		if !ok {
			return nil
		}
		p.setEnabled(d.id, !d.enabled)
		p.rebuild()
		p.a.notice = "已" + toggleWord(!d.enabled) + " " + d.id + "，保存后生效"
	case "a":
		p.rebuild()
		p.a.notice = "已重新扫描挂载点"
	case "n":
		p.a.b.cfg.Linux.IncludeNetwork = !p.a.b.cfg.Linux.IncludeNetwork
		p.dirtyFlag = true
		p.rebuild()
		if p.a.b.cfg.Linux.IncludeNetwork {
			p.a.notice = "已包含网络挂载（NFS/CIFS 等），扫描可能略慢"
		} else {
			p.a.notice = "已排除网络挂载"
		}
	}
	return nil
}

func (p *drivesPage) updateEditing(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		p.commitThresholdInput()
		p.rebuild()
		p.a.notice = "阈值已更新，保存后生效"
		return nil
	case " ":
		if d, ok := p.current(); ok {
			p.setEnabled(d.id, !d.enabled)
			p.rebuild()
		}
		return nil
	}
	var cmd tea.Cmd
	p.threshold, cmd = p.threshold.Update(msg)
	return cmd
}

// claimEsc 从编辑表单退回磁盘列表；列表层交给 app 的全局返回逻辑。
func (p *drivesPage) claimEsc() (tea.Cmd, bool) {
	if !p.editing {
		return nil, false
	}
	p.commitThresholdInput()
	p.editing = false
	p.rebuild()
	return nil, true
}

func toggleWord(on bool) string {
	if on {
		return "启用"
	}
	return "停用"
}

func (p *drivesPage) view(width int) string {
	if p.editing {
		return p.viewEditor()
	}
	return p.viewList(width)
}

func (p *drivesPage) viewList(width int) string {
	if len(p.rows) == 0 {
		return dim.Render("没有发现可监控的磁盘。按 a 重新扫描，或按 n 尝试包含网络挂载。")
	}
	network := "排除"
	if p.a.b.cfg.Linux.IncludeNetwork {
		network = "包含"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", dim.Render(fmt.Sprintf("网络挂载：%s · 共 %d 个候选挂载点 · 空格直接切换启用", network, len(p.rows))))
	// 首列前有 2 格光标位，表格可用宽度要扣掉
	avail := width
	if avail > 0 {
		avail -= 2
	}
	cols, widths := []string{"挂载点", "卷标", "容量", "剩余", "阈值", "状态"}, []int{20, 14, 9, 9, 7, 6}
	if avail > 0 && avail < 72 {
		cols, widths = []string{"挂载点", "容量", "剩余", "阈值", "状态"}, narrowWidths
	}
	full := len(cols) == 6
	// 表头也要留出行首光标的两格占位，否则数据列会整体右移
	b.WriteString("  " + section.Render(row(avail, widths, cols...)) + "\n")
	for i, d := range p.rows {
		status := "正常"
		switch {
		case d.missing:
			status = "未挂载"
		case !d.enabled:
			status = "未监控"
		}
		thr := "-"
		if d.threshold > 0 {
			thr = fmt.Sprintf("≤%.0f%%", d.threshold)
		}
		cells := []string{d.id}
		if full {
			cells = append(cells, d.label)
		}
		cells = append(cells, model.HumanBytes(d.total), model.HumanBytes(d.free), thr,
			stateColor(status, false, d.enabled))
		line := row(avail, widths, cells...)
		if i == p.cursor {
			b.WriteString(cursor.Render("▶ ") + line + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	if p.dirtyFlag {
		b.WriteString("\n" + warn.Render("有未保存改动，按 ctrl+s 写入并热重载") + "\n")
	}
	return b.String()
}

func (p *drivesPage) viewEditor() string {
	d, ok := p.current()
	if !ok {
		return dim.Render("没有选中的磁盘")
	}
	mark := dim.Render("［ ］ 未启用")
	if d.enabled {
		mark = good.Render("［✓］ 已启用")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "挂载点：%s\n", field.Render(d.id))
	fmt.Fprintf(&b, "卷标　：%s\n", orDash(d.label))
	if !d.missing {
		fmt.Fprintf(&b, "容量　：%s　剩余：%s（%s）\n",
			model.HumanBytes(d.total), model.HumanBytes(d.free), freePctText(d.total, d.free))
	}
	fmt.Fprintf(&b, "启用　：%s（按 space 切换）\n", mark)
	fmt.Fprintf(&b, "阈值　：%s\n", field.Render(p.threshold.View()))
	fmt.Fprintf(&b, "　　　　 剩余低于该百分比时发告警邮件，范围 1-99\n")
	if d.network {
		fmt.Fprintf(&b, "\n%s\n", warn.Render("这是网络挂载，失联时 statfs 可能耗时；已按 3 秒超时保护"))
	}
	return b.String()
}

func freePctText(total, free uint64) string {
	if total == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", float64(free)/float64(total)*100)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
