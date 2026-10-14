//go:build linux

package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbletea"
)

type fieldKind int

const (
	kindText fieldKind = iota
	kindNumber
	kindSecret
	// kindChoice 用左右键切换，布尔项就是「启用/停用」两选项
	kindChoice
)

// formField 是表单里的一行。写回动作放在闭包里，
// 让每个页面只管声明「这行读哪个配置、怎么写哪个配置」。
type formField struct {
	label   string
	help    string
	kind    fieldKind
	input   textinput.Model
	options []string
	// load 返回当前值（文本/数字/口令占位）或选项下标
	text  func() string
	index func() int
	// save 把输入写回配置；返回错误则整次保存中止并在底部显示
	save     func(raw string) error
	setIndex func(i int)
}

type form struct {
	a      *app
	fields []formField
	cursor int
	dirty  bool
	// footer 追加在表单下方（开关说明、风险提示等）
	footer func() string
	// onKey 让页面处理额外按键（如邮件页的 t 发送测试）
	// 返回 handled=false 时表单按默认按键处理
	onKey func(msg tea.KeyMsg) (handled bool, cmd tea.Cmd)
}

func (f *form) newInput(placeholder string, secret bool) textinput.Model {
	if secret {
		ti := newPasswordInput(placeholder)
		ti.CharLimit = 128
		return ti
	}
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.CharLimit = 128
	return ti
}

func (f *form) build() {
	for i := range f.fields {
		fd := &f.fields[i]
		switch fd.kind {
		case kindSecret:
			fd.input = newPasswordInput("留空表示不修改")
		case kindText, kindNumber:
			fd.input = f.newInput("", false)
			if fd.text != nil {
				fd.input.SetValue(fd.text())
			}
		}
		if fd.kind == kindNumber {
			fd.input.Validate = validateNumber
		}
	}
	f.cursor = 0
}

func validateNumber(s string) error {
	if s == "" {
		return nil
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return fmt.Errorf("只能填数字")
	}
	return nil
}

func (f *form) init() tea.Cmd {
	f.build()
	return f.refocus()
}

func (f *form) focusable() bool {
	fd := f.current()
	return fd != nil && (fd.kind == kindText || fd.kind == kindNumber || fd.kind == kindSecret)
}

func (f *form) current() *formField {
	if f.cursor < 0 || f.cursor >= len(f.fields) {
		return nil
	}
	return &f.fields[f.cursor]
}

func (f *form) dirtyState() bool { return f.dirty }

// save 校验并写回所有字段：任一字段非法就整体中止，
// 避免出现「间隔存了、冷却没存」的半套配置。
// keepDirty 为真时保留「未保存」标记：发测试邮件要用屏幕上的值，
// 但落盘仍然只发生在 ctrl+s，不能让一次试发把待保存提示抹掉。
func (f *form) save(keepDirty bool) error {
	for i := range f.fields {
		fd := &f.fields[i]
		if fd.kind == kindChoice || fd.save == nil {
			continue
		}
		raw := strings.TrimSpace(fd.input.Value())
		if fd.kind == kindSecret && raw == "" {
			continue // 口令留空 = 不修改，避免误清授权码
		}
		if err := fd.save(raw); err != nil {
			f.cursor = i
			return fmt.Errorf("%s：%w", fd.label, err)
		}
	}
	if !keepDirty {
		f.dirty = false
	}
	return nil
}

// value 取输入框里的当前文本（未去空白），页脚状态行用它反映屏幕上的值。
func (f *form) value(label string) string {
	for i := range f.fields {
		if f.fields[i].label == label {
			return strings.TrimSpace(f.fields[i].input.Value())
		}
	}
	return ""
}

func (f *form) markDirty() { f.dirty = true }

func (f *form) update(msg tea.Msg) tea.Cmd {
	m, ok := msg.(tea.KeyMsg)
	if !ok {
		return f.forward(msg)
	}
	// 文本行上的可打印字符必须先进输入框，再谈快捷键：主机名里有 t/l/h，
	// 收件人列表里有空格，把它们当快捷键会让用户根本输不完一个地址。
	if f.focusable() && typesIntoInput(m) {
		var cmd tea.Cmd
		f.fields[f.cursor].input, cmd = f.current().input.Update(msg)
		f.dirty = true
		return cmd
	}
	if f.onKey != nil {
		if handled, cmd := f.onKey(m); handled {
			return cmd
		}
	}
	switch m.String() {
	case "up", "k":
		f.move(-1)
		return f.refocus()
	case "down", "j", "tab":
		f.move(1)
		return f.refocus()
	case "left", "h":
		return f.cycle(-1)
	case "right", "l":
		return f.cycle(1)
	case " ":
		return f.cycle(1)
	}
	if f.focusable() {
		var cmd tea.Cmd
		f.fields[f.cursor].input, cmd = f.current().input.Update(msg)
		f.dirty = true
		return cmd
	}
	return nil
}

// typesIntoInput 报告按键是否属于文本编辑：可打印字符、空格、退格与光标移动。
// 上下、tab、回车、esc、ctrl+s 等仍归界面导航与保存。
func typesIntoInput(m tea.KeyMsg) bool {
	switch m.Type {
	case tea.KeyRunes, tea.KeySpace, tea.KeyBackspace, tea.KeyDelete,
		tea.KeyLeft, tea.KeyRight, tea.KeyHome, tea.KeyEnd:
		return true
	}
	// 输入框自带的行内编辑键
	switch m.String() {
	case "ctrl+a", "ctrl+e", "ctrl+f", "ctrl+b", "ctrl+w", "ctrl+u", "ctrl+k", "ctrl+d":
		return true
	}
	return false
}

func (f *form) move(delta int) {
	if len(f.fields) == 0 {
		return
	}
	f.cursor = (f.cursor + delta + len(f.fields)) % len(f.fields)
}

// refocus 把光标停在文本行上时才让输入框接收按键，
// 否则选项行的方向键会被输入框吃掉。
func (f *form) refocus() tea.Cmd {
	for i := range f.fields {
		f.fields[i].input.Blur()
	}
	if f.focusable() {
		return f.fields[f.cursor].input.Focus()
	}
	return nil
}

func (f *form) cycle(delta int) tea.Cmd {
	fd := f.current()
	if fd == nil || fd.kind != kindChoice || fd.setIndex == nil {
		return nil
	}
	n := len(fd.options)
	if n == 0 {
		return nil
	}
	cur := 0
	if fd.index != nil {
		cur = fd.index()
	}
	fd.setIndex((cur + delta + n) % n)
	f.dirty = true
	return nil
}

func (f *form) forward(msg tea.Msg) tea.Cmd {
	if !f.focusable() {
		return nil
	}
	var cmd tea.Cmd
	f.fields[f.cursor].input, cmd = f.current().input.Update(msg)
	return cmd
}

func (f *form) view(width int) string {
	labelWidth := clampWidth(width-24, 18)
	var b strings.Builder
	for i, fd := range f.fields {
		val := f.fieldText(fd, i)
		line := cell(fd.label, labelWidth) + " " + val
		if i == f.cursor {
			b.WriteString(cursor.Render("▶ ") + line + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
		if fd.help != "" {
			b.WriteString("  " + cell("", labelWidth) + " " + dim.Render(fd.help) + "\n")
		}
	}
	if f.footer != nil {
		b.WriteString("\n" + f.footer() + "\n")
	}
	if f.dirty {
		b.WriteString("\n" + warn.Render("有未保存改动，按 ctrl+s 写入并热重载") + "\n")
	}
	return b.String()
}

func (f *form) fieldText(fd formField, i int) string {
	switch fd.kind {
	case kindChoice:
		cur := 0
		if fd.index != nil {
			cur = fd.index()
		}
		text := "-"
		if cur >= 0 && cur < len(fd.options) {
			text = fd.options[cur]
		}
		return field.Render("◀ " + text + " ▶")
	case kindSecret:
		if fd.input.Value() == "" {
			return dim.Render("（留空不修改）")
		}
		return field.Render(strings.Repeat("*", minInt(12, len(fd.input.Value()))))
	default:
		if i == f.cursor && f.focusable() {
			return field.Render(fd.input.View())
		}
		v := fd.input.Value()
		if v == "" {
			return dim.Render("（未设置）")
		}
		return field.Render(v)
	}
}
