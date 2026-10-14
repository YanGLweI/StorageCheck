//go:build !linux

package config

// lockPath 非 Linux 平台只有一个进程会写配置（单实例已保证），无需加锁。
func lockPath(string) (func(), error) { return func() {}, nil }
