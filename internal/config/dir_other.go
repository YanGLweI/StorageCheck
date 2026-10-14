//go:build !linux

package config

// systemConfigDir 非 Linux 平台没有 /etc 安装形态（Windows 用 %APPDATA%）。
func systemConfigDir() (string, bool) { return "", false }
