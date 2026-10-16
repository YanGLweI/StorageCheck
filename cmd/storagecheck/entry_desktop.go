//go:build !linux

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

// dispatch 是桌面入口：标准库 flag + 直接 runApp，不经过 cobra 命令树。
// Linux 服务版需要子命令（run/status/events/check），仍走 cobra，见 entry_linux.go。
func dispatch() int {
	fs := flag.NewFlagSet("storagecheck", flag.ExitOnError)
	fs.BoolVar(&opts.dev, "dev", false, "使用模拟磁盘数据（开发调试用）")
	fs.BoolVar(&opts.minimized, "minimized", false, "启动后不打开控制台窗口")
	fs.StringVar(&opts.configDir, "config-dir", "", "配置目录（留空则用用户配置目录）")
	fs.Parse(os.Args[1:])

	if err := runApp(opts); err != nil {
		fmt.Fprintln(os.Stderr, "执行失败:", err)
		log.Println("启动失败:", err)
		reportStartupFailure(err)
		return 1
	}
	return 0
}
