//go:build !windows

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"storagecheck/internal/autostart"
	"storagecheck/internal/collector"
	"storagecheck/internal/config"
)

func newSecretBox(dir string) config.SecretBox { return config.NewAESBox(dir) }

func newAutostartManager(dir string) autostart.Manager {
	return autostart.NewDevManager(dir)
}

func newCollector(dev bool) collector.Collector {
	if dev {
		return devDrives()
	}
	return collector.New()
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}

// presentOrOpenConsole 非 Windows 开发模式没有窗口前置概念，直接打开。
func presentOrOpenConsole(url string, port int) { openBrowser(url) }

// acquireLock 非 Windows 使用 pid 锁文件（仅开发模式路径）。
func acquireLock(dir string) (func(), bool, error) {
	p := filepath.Join(dir, ".lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.WriteString(strconv.Itoa(os.Getpid()))
			f.Close()
			return func() { os.Remove(p) }, true, nil
		}
		if !os.IsExist(err) {
			return nil, false, err
		}
		data, _ := os.ReadFile(p)
		pid, _ := strconv.Atoi(string(data))
		if pid > 0 && pidAlive(pid) {
			return nil, false, nil
		}
		log.Println("清理过期锁文件:", p)
		os.Remove(p)
		time.Sleep(50 * time.Millisecond)
	}
	return nil, false, os.ErrExist
}

func pidAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
