//go:build !windows && !linux

package main

import (
	"context"
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
	"storagecheck/internal/model"
	"storagecheck/internal/monitor"
)

// setupLogging 开发态沿用写文件的日志，方便在 IDE 控制台之外回溯。
func setupLogging(dir string) {
	f, err := os.OpenFile(filepath.Join(dir, "storagecheck.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(f)
}

func newSecretBox(dir string) config.SecretBox { return config.NewAESBox(dir) }

func newAutostartManager(dir string) autostart.Manager {
	return autostart.NewDevManager(dir)
}

func credentialPath() string { return "" }

func autostartArgs() []string { return []string{"--minimized"} }

func newCollector(dev bool, cfg func() *config.Config) collector.Collector {
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

// presentOrOpenConsole 开发平台没有窗口前置概念，直接打开。
func presentOrOpenConsole(url string, port int) { openBrowser(url) }

// acquireLock 开发态用 pid 锁文件（Linux 生产形态改用 flock，见 platform_linux.go）。
func acquireLock(dir string) (func(), bool, error) {
	p := filepath.Join(dir, model.LockFileName)
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

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// defaultBlock：开发态没有托盘，等终止信号即可
// （这里也是原 !windows 分支「非 dev 启动立刻退出」隐患的修复点）。
func defaultBlock(ctx context.Context, cancel context.CancelFunc, mon *monitor.Monitor, c console, monDone chan struct{}) {
	<-ctx.Done()
}
