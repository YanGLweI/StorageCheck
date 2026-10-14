//go:build linux

package config

import "os"

// systemConfigDir 是打包安装的配置目录。只要该目录已存在就优先使用它，
// 这样普通管理员执行 storagecheck status 时读到的也是守护进程那份配置；
// 尚未安装（例如从源码目录跑测试）才回退到用户配置目录。
func systemConfigDir() (string, bool) {
	if _, err := os.Stat("/etc/storagecheck"); err == nil {
		return "/etc/storagecheck", true
	}
	if os.Geteuid() == 0 {
		return "/etc/storagecheck", true
	}
	return "", false
}
