//go:build windows

package main

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"

	"storagecheck/internal/autostart"
	"storagecheck/internal/collector"
	"storagecheck/internal/config"
	"storagecheck/internal/secret"
)

func newSecretBox(dir string) config.SecretBox { return secret.NewDPAPIBox() }

func newAutostartManager(dir string) autostart.Manager { return autostart.New() }

func newCollector(dev bool) collector.Collector {
	if dev {
		return devDrives()
	}
	return collector.New()
}

// openBrowser 只负责新建一个 Edge 应用窗口；唤起已有窗口见 presentOrOpenConsole。
func openBrowser(url string) {
	if edge, ok := findEdge(); ok {
		cmd := exec.Command(edge, edgeAppArgs(url, webviewProfileDir())...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Start(); err == nil {
			go cmd.Wait()
			return
		} else {
			log.Printf("Edge 应用窗口启动失败，回退默认浏览器: %v", err)
		}
	}
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func findEdge() (string, bool) {
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		p := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	if p, err := exec.LookPath("msedge.exe"); err == nil {
		return p, true
	}
	return "", false
}

// webviewProfileDir 是控制台窗口的独立浏览器 profile 目录，避免污染用户日常浏览数据。
func webviewProfileDir() string {
	dir, err := config.Dir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "webview")
}

const errorAlreadyExists = windows.Errno(syscall.ERROR_ALREADY_EXISTS)

// acquireLock 命名互斥量保证单实例（当前会话）。
func acquireLock(dir string) (func(), bool, error) {
	name, err := windows.UTF16PtrFromString("StorageCheck.SingleInstance")
	if err != nil {
		return nil, false, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	// 互斥量已存在时 CreateMutex 仍返回有效句柄，并用 ERROR_ALREADY_EXISTS 说明情况，
	// x/sys 把它作为 err 返回。这不是失败：第二个实例应唤起已有窗口后退出。
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			windows.CloseHandle(h)
			return func() {}, false, nil
		}
		return nil, false, err
	}
	if windows.GetLastError() == errorAlreadyExists {
		windows.CloseHandle(h)
		return func() {}, false, nil
	}
	return func() { windows.CloseHandle(h) }, true, nil
}
