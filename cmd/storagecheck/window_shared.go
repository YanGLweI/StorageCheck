// 桌面控制台（Windows 窗口 / macOS 开发态浏览器）专用的窗口识别 helper。
// Linux 版没有控制台，故整组代码不参与 Linux 编译。

//go:build !linux

package main

import (
	"strconv"
	"sync"
	"time"
)

// consoleTitleBase 必须与 internal/server/static/index.html 的 <title> 一致；
// app.js 在加载后把端口追加到标题尾部，程序据此在已打开的窗口里认出「本次会话」。
const consoleTitleBase = "StorageCheck · 磁盘空间监控"

// spawnDedupe 合并短时间内重复的打开请求：双击托盘图标会送来两次左键事件，
// 而新建窗口在页面脚本改写标题之前是搜不到的。
const spawnDedupe = 2 * time.Second

func consoleTitle(port int) string {
	return consoleTitleBase + " · " + strconv.Itoa(port)
}

func isConsoleTitle(title string, port int) bool {
	return title == consoleTitle(port)
}

// edgeAppArgs 是 Edge 应用窗口的参数：无地址栏与标签页，桌面程序观感，独立 profile 不污染日常浏览数据。
func edgeAppArgs(url, profileDir string) []string {
	return []string{
		"--app=" + url,
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}
}

type openGuard struct {
	mu   sync.Mutex
	last time.Time
	win  time.Duration
}

func newOpenGuard() *openGuard { return &openGuard{win: spawnDedupe} }

// allowSpawn 报告此刻是否应该新建窗口；首次调用（last 为零值）总是放行。
func (g *openGuard) allowSpawn(now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Sub(g.last) < g.win {
		return false
	}
	g.last = now
	return true
}
