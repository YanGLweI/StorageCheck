//go:build windows

package tray

import (
	"fyne.io/systray"

	"storagecheck/internal/appicon"
)

// Handlers 是托盘菜单回调集合。
type Handlers struct {
	OnOpen        func()
	OnCheck       func()
	OnPauseToggle func() bool // 返回切换后的暂停状态
	OnQuit        func()
}

var (
	h        Handlers
	statusCh = make(chan bool, 1)
)

func Register(hh Handlers) { h = hh }

// Run 启动托盘事件循环（阻塞）。
func Run() { systray.Run(onReady, nil) }

// SetAlerting 更新托盘图标：任一磁盘处于告警时显示红叹号。
func SetAlerting(alert bool) {
	select {
	case statusCh <- alert:
	default:
	}
}

func onReady() {
	iconNormal := appicon.MakeICO(false)
	iconAlert := appicon.MakeICO(true)

	systray.SetIcon(iconNormal)
	systray.SetTitle("StorageCheck 磁盘监控")
	systray.SetTooltip("StorageCheck 磁盘监控")

	mOpen := systray.AddMenuItem("打开控制台", "打开 StorageCheck 管理窗口")
	systray.AddSeparator()
	pauseItem := systray.AddMenuItem("暂停监控", "暂停期间不再发送邮件告警")
	mCheck := systray.AddMenuItem("立即检查", "马上采集磁盘并评估告警条件")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "退出 StorageCheck")

	go func() {
		lastAlert := false
		for {
			select {
			case <-mOpen.ClickedCh:
				if h.OnOpen != nil {
					h.OnOpen()
				}
			case <-mCheck.ClickedCh:
				if h.OnCheck != nil {
					h.OnCheck()
				}
			case <-pauseItem.ClickedCh:
				paused := false
				if h.OnPauseToggle != nil {
					paused = h.OnPauseToggle()
				}
				if paused {
					pauseItem.SetTitle("恢复监控")
				} else {
					pauseItem.SetTitle("暂停监控")
				}
			case <-mQuit.ClickedCh:
				if h.OnQuit != nil {
					h.OnQuit()
				}
				systray.Quit()
			case alert := <-statusCh:
				if alert != lastAlert {
					lastAlert = alert
					if alert {
						systray.SetIcon(iconAlert)
					} else {
						systray.SetIcon(iconNormal)
					}
				}
			}
		}
	}()
}
