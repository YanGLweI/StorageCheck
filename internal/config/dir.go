package config

import (
	"os"
	"path/filepath"
)

// ResolveDir 按优先级确定配置目录：
// --config-dir 参数 > STORAGECHECK_CONFIG_DIR 环境变量 > 系统安装目录 > 用户配置目录。
//
// systemd 服务环境里没有 HOME，os.UserConfigDir 会直接失败，因此系统安装目录
// （Linux 为 /etc/storagecheck）必须在它之前参与解析。
func ResolveDir(flagVal string) (string, error) {
	if flagVal != "" {
		return filepath.Abs(flagVal)
	}
	if env := os.Getenv(EnvConfigDir); env != "" {
		return filepath.Abs(env)
	}
	if sys, ok := systemConfigDir(); ok {
		return sys, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "StorageCheck"), nil
}

// Dir 保留给开发态与旧调用点：不做系统目录解析。
func Dir() (string, error) { return ResolveDir("") }

// DataDir 返回只读命令（status/events/界面）该去哪个目录取运行产物，不创建目录。
func DataDir(configDir string) string {
	d, _ := ResolveDataDir(configDir)
	return d
}

// DataDirFor 是只读版本的目录解析：CLI/TUI 用它的地方没有 Store 实例，
// 也不该顺手创建目录。
func DataDirFor(configDir string) string {
	d, _ := ResolveDataDir(configDir)
	return d
}

// EnvDataDir 指向运行数据目录（state.json、events.log、锁文件）。
const EnvDataDir = "STORAGECHECK_DATA_DIR"

// systemDataDir 是 RPM 安装出来的运行数据目录。
const systemDataDir = "/var/lib/storagecheck"

// ResolveDataDir 决定运行数据落在哪里：
//
//	STORAGECHECK_DATA_DIR > /var/lib/storagecheck（已存在时） > 配置目录
//
// 没有环境变量时要求目录已经存在，这样「目录不存在 → 静默回退」的语义清楚；
// 显式指定则由调用方负责创建，创建失败就报错，不悄悄退回配置目录。
func ResolveDataDir(configDir string) (string, bool) {
	if env := os.Getenv(EnvDataDir); env != "" {
		abs, err := filepath.Abs(env)
		if err != nil {
			return configDir, false
		}
		return abs, true
	}
	if fi, err := os.Stat(systemDataDir); err == nil && fi.IsDir() {
		return systemDataDir, false
	}
	return configDir, false
}

const EnvConfigDir = "STORAGECHECK_CONFIG_DIR"
