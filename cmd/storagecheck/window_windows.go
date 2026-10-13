//go:build windows

package main

import (
	"log"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows 只包装了少量 user32 函数，其余按 internal/secret 的方式延迟取址。
var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procShowWindow          = user32.NewProc("ShowWindow")
	procIsIconic            = user32.NewProc("IsIconic")
	procGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	procGetWindowText       = user32.NewProc("GetWindowTextW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procSwitchToThisWindow  = user32.NewProc("SwitchToThisWindow")
	procFlashWindowEx       = user32.NewProc("FlashWindowEx")
)

const (
	swRestore = 1 // SW_RESTORE

	flashWAll       = 0x00000003 // FLASHW_ALL：标题栏与任务栏按钮
	flashWTimerNofg = 0x00000004 // FLASHW_TIMERNOFG：闪烁到用户切过去为止
)

type flashInfo struct {
	cbSize   uint32
	hwnd     windows.HWND
	flags    uint32
	duration uint32
}

// consoleGuard 合并短时间内重复的新建窗口请求（托盘双击会送来两次左键事件）。
// 它是进程内的：临时进程（双击快捷方式）总是放行，合并只发生在常驻进程里。
var consoleGuard = newOpenGuard()

// consoleMu 串行化「搜索—前置—新建」，同时保护回调共享的搜索状态
// （托盘与启动路径可能在不同 goroutine 里同时搜索窗口）。
var consoleMu sync.Mutex

// 回调要求包级创建，匹配状态也因此放在包级，由 consoleMu 保护。
var enumTarget struct {
	port int
	hwnd windows.HWND
}

var enumConsoleProc = windows.NewCallback(func(hwnd, lparam uintptr) uintptr {
	h := windows.HWND(hwnd)
	if !windows.IsWindowVisible(h) {
		return 1
	}
	if isConsoleTitle(windowText(h), enumTarget.port) {
		enumTarget.hwnd = h
		return 0 // 停止枚举（EnumWindows 返回 FALSE）
	}
	return 1
})

func windowText(hwnd windows.HWND) string {
	n, _, _ := procGetWindowTextLength.Call(uintptr(hwnd))
	if n == 0 || n > 1024 {
		return ""
	}
	buf := make([]uint16, n+1)
	copied, _, _ := procGetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if copied == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:copied])
}

// findConsoleWindow 返回本次会话的控制台窗口，未找到时为 0。按 z-order 自顶向下取第一个命中。
func findConsoleWindow(port int) windows.HWND {
	enumTarget.port, enumTarget.hwnd = port, 0
	// 命中时回调返回 0，EnumWindows 以此结束枚举，此时的 err 不代表失败。
	if err := windows.EnumWindows(enumConsoleProc, nil); err != nil && enumTarget.hwnd == 0 {
		log.Printf("枚举窗口失败: %v", err)
	}
	return enumTarget.hwnd
}

// raiseConsole 还原并前置窗口；系统限制无法夺取前台时改为闪烁任务栏按钮，让用户确有所指。
func raiseConsole(hwnd windows.HWND) bool {
	if iconic, _, _ := procIsIconic.Call(uintptr(hwnd)); iconic != 0 {
		procShowWindow.Call(uintptr(hwnd), uintptr(swRestore))
	}
	procSetForegroundWindow.Call(uintptr(hwnd))
	if windows.GetForegroundWindow() == hwnd {
		return true
	}
	// 本进程没有前台权限时 SetForegroundWindow 会静默失败，SwitchToThisWindow 可越过该限制。
	procSwitchToThisWindow.Call(uintptr(hwnd), 1)
	if windows.GetForegroundWindow() == hwnd {
		return true
	}
	fi := flashInfo{
		cbSize: uint32(unsafe.Sizeof(flashInfo{})),
		hwnd:   hwnd,
		flags:  flashWAll | flashWTimerNofg,
	}
	procFlashWindowEx.Call(uintptr(unsafe.Pointer(&fi)))
	return false
}

// presentOrOpenConsole 已有控制台窗口则前置，否则打开新窗口。
func presentOrOpenConsole(url string, port int) {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	if hwnd := findConsoleWindow(port); hwnd != 0 {
		if raiseConsole(hwnd) {
			log.Printf("已唤起控制台窗口: hwnd=%d port=%d", uintptr(hwnd), port)
			return
		}
		log.Printf("控制台窗口未能取到前台，已闪烁任务栏按钮")
		return
	}
	if !consoleGuard.allowSpawn(time.Now()) {
		log.Printf("控制台窗口正在打开，忽略重复请求")
		return
	}
	log.Printf("未找到控制台窗口，打开新窗口: port=%d", port)
	openBrowser(url)
}
