//go:build !windows

package tray

// 非 Windows 平台（开发模式）不提供系统托盘。

type Handlers struct {
	OnOpen        func()
	OnCheck       func()
	OnPauseToggle func() bool
	OnQuit        func()
}

func Register(Handlers) {}
func Run()              {}
func SetAlerting(bool)  {}
