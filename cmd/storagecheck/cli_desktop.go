//go:build !linux

package main

import "fmt"

// notifyCheck 桌面版的状态入口是内嵌控制台，信号控制面只有 Linux 服务用到。
func notifyCheck(string) error {
	return fmt.Errorf("桌面版请在控制台界面点击「立即检查」")
}

// alreadyRunningNotice 桌面版返回空串：重复启动要按 run.json 前置既有窗口。
func alreadyRunningNotice(string) string { return "" }
