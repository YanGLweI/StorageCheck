//go:build linux

package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// nmtui 的观感：蓝色标题、反白选中行、底部快捷键条。
var (
	pageTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	section   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	selected  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	cursor    = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	dim       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	good      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warn      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	bad       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	field     = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	helpBar   = lipgloss.NewStyle().Background(lipgloss.Color("12")).Foreground(lipgloss.Color("15")).Padding(0, 1)
	toast     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	errText   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	box       = lipgloss.NewStyle().Padding(1, 2).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("12"))
)

// pad 按终端显示宽度补齐，中文占两格时纯 %-*s 会错位。
func pad(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func cell(s string, width int) string {
	if lipgloss.Width(s) > width {
		s = lipgloss.NewStyle().MaxWidth(width).Render(s)
	}
	return pad(s, width)
}

// stateColor 把磁盘状态渲染成对应颜色，让告警在满屏文字里跳出来。
func stateColor(text string, alerting, enabled bool) string {
	switch {
	case alerting:
		return bad.Render(text)
	case !enabled:
		return dim.Render(text)
	default:
		return good.Render(text)
	}
}

// helpKeys 拼底部快捷键条。宽度不够时按重要性从右往左舍弃，
// 绝不能让它超宽——折行会让整个界面错位。
func helpKeys(width int, pairs ...string) string {
	trimmed := make([]string, 0, len(pairs))
	for _, p := range pairs {
		trimmed = append(trimmed, strings.TrimRight(p, " "))
	}
	for len(trimmed) > 1 {
		out := strings.Join(trimmed, "  ")
		if width <= 0 || lipgloss.Width(helpBar.Render(out)) <= width {
			return helpBar.Render(out)
		}
		trimmed = trimmed[:len(trimmed)-1]
	}
	return helpBar.Render(strings.Join(trimmed, "  "))
}
