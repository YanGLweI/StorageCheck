//go:build linux

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"

	"storagecheck/internal/autostart"
	"storagecheck/internal/collector"
	"storagecheck/internal/config"
	"storagecheck/internal/model"
	"storagecheck/internal/monitor"
)

// setupLogging：服务日志交给 journald（unit 里 StandardOutput=journal），
// 不再往配置目录写 storagecheck.log —— 那会让日志无界增长且没法用 journalctl 看。
func setupLogging(string) {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("storagecheck: ")
}

func newSecretBox(dir string) config.SecretBox {
	return config.NewAESBoxWithKeyFile(dir, "seal.key")
}

func newAutostartManager(dir string) autostart.Manager { return autostart.NewSystemd(dir) }

// autostartArgs：命令行由 unit 文件的 ExecStart 决定，进程只回报启用状态。
func autostartArgs() []string { return []string{"run"} }

// credentialPath 是 systemd LoadCredential 的落点；未配置时返回空串。
func credentialPath() string {
	if d := os.Getenv("CREDENTIALS_DIRECTORY"); d != "" {
		return filepath.Join(d, "smtp_password")
	}
	return "/run/credentials/storagecheck.smtp_password"
}

func newCollector(dev bool, cfg func() *config.Config) collector.Collector {
	if dev {
		return devDrives()
	}
	return collector.NewDynamic(func() collector.MountRule {
		c := cfg()
		if c == nil {
			return collector.MountRule{}
		}
		return collector.MountRule{
			IncludeNetwork:  c.Linux.IncludeNetwork,
			ExcludePrefixes: c.Linux.MountExclude,
			MinTotalBytes:   c.MinTotal(),
		}
	})
}

// presentOrOpenConsole：无 GUI 平台没有窗口可唤起。
func presentOrOpenConsole(string, int) {}

// alreadyRunningNotice：无 Web 控制台，重复启动只能给一句人话，不该提 run.json。
func alreadyRunningNotice(dir string) string {
	return "另一个实例已在运行（占用 " + filepath.Join(dir, model.LockFileName) + " 的 flock），本进程不重复启动监控"
}

// acquireLock 用 flock 保证单实例：内核在进程死亡时自动释放，
// 不需要像 pid 文件那样猜「进程是否还活着」。pid 仍写进文件，供终端界面发信号。
func acquireLock(dir string) (func(), bool, error) {
	path := filepath.Join(dir, model.LockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, false, nil
	}
	f.Truncate(0)
	f.WriteString(strconv.Itoa(os.Getpid()))
	f.Sync()
	// 正常退出时把锁文件带走，让「文件在不在」和「服务在不在」保持一致；
	// 被 kill -9 时来不及执行，残留的 pid 由 DaemonPID 的进程名校验兜底。
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
		os.Remove(path)
	}, true, nil
}

// defaultBlock 是服务常驻主体：等终止信号，其间处理重载/立即检查/暂停切换。
// 终端界面保存配置后向本进程发 SIGHUP，改动无需重启即生效。
func defaultBlock(ctx context.Context, cancel context.CancelFunc, mon *monitor.Monitor, c console, monDone chan struct{}) {
	// SIGHUP 重载、SIGUSR2 立即检查、SIGUSR1 暂停切换：全部走信号，
	// 不需要额外的监听端口或 Unix socket，也就没有鉴权面和残留文件。
	signalCh := make(chan os.Signal, 4)
	signal.Notify(signalCh, syscall.SIGHUP, syscall.SIGUSR1, syscall.SIGUSR2)
	defer signal.Stop(signalCh)

	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-signalCh:
			switch sig {
			case syscall.SIGHUP:
				if err := mon.Reload(); err != nil {
					log.Printf("重新加载配置失败: %v", err)
				} else {
					log.Println("配置已重新加载")
				}
			case syscall.SIGUSR2:
				log.Println("收到立即检查请求")
				mon.Trigger()
			case syscall.SIGUSR1:
				paused := !mon.Paused()
				mon.SetPaused(paused)
				if paused {
					log.Println("监控已暂停（SIGUSR1）")
				} else {
					log.Println("监控已恢复（SIGUSR1）")
				}
			}
		}
	}
}
