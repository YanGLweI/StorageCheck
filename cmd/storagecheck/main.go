package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"storagecheck/internal/config"
	"storagecheck/internal/mailer"
	"storagecheck/internal/monitor"
	"storagecheck/internal/server"
	"storagecheck/internal/tray"
)

var version = "1.0.0-dev"

type runInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	URL   string `json:"url"`
	PID   int    `json:"pid"`
}

// liveSender 每次发信时按当前配置构建 mailer（改配置即时生效）。
type liveSender struct{ store *config.Store }

func (l *liveSender) mk() (*mailer.Mailer, error) {
	cfg, err := l.store.Load()
	if err != nil {
		return nil, err
	}
	pass, err := l.store.OpenSecret(cfg.SMTP.PassEnc)
	if err != nil {
		return nil, err
	}
	return mailer.New(cfg.SMTP, pass), nil
}

func (l *liveSender) SendAlert(host, id, label string, total, free uint64, used, freepct, thr float64, escalated bool) error {
	m, err := l.mk()
	if err != nil {
		return err
	}
	return m.SendAlert(host, id, label, total, free, used, freepct, thr, escalated)
}

func (l *liveSender) SendRecovery(host, id, label string, total, free uint64, used, freepct, thr float64) error {
	m, err := l.mk()
	if err != nil {
		return err
	}
	return m.SendRecovery(host, id, label, total, free, used, freepct, thr)
}

func main() {
	dev := flag.Bool("dev", false, "开发模式：使用模拟磁盘数据，不加载系统托盘")
	minimized := flag.Bool("minimized", false, "启动时不自动打开浏览器")
	flag.Parse()

	logPath := filepath.Join(mustDir(), "storagecheck.log")
	if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		log.SetOutput(f)
		defer f.Close()
	}

	dir := mustDir()
	box := newSecretBox(dir)
	store, err := config.NewStore(dir, box)
	if err != nil {
		fatal(err)
	}
	cfg, err := store.Load()
	if err != nil {
		log.Printf("加载配置失败，使用默认配置: %v", err)
		cfg = config.Default()
	}

	// 单实例：已有实例则唤起其控制台并退出
	release, acquired, err := acquireLock(dir)
	if err != nil {
		fatal(err)
	}
	if !acquired {
		if u := readRunURL(dir); u != "" && !*minimized {
			openBrowser(u)
		}
		return
	}
	defer release()

	col := newCollector(*dev)
	mon, err := monitor.New(col, store, &liveSender{store}, version)
	if err != nil {
		fatal(err)
	}

	srv, err := server.New(mon, store, version)
	if err != nil {
		fatal(err)
	}
	am := newAutostartManager(dir)
	srv.SetAutostartFunc(func(enable bool) error {
		exe, _ := os.Executable()
		if enable {
			return am.Enable(exe, []string{"--minimized"})
		}
		return am.Disable()
	})
	if err := srv.Start(); err != nil {
		fatal(err)
	}

	if err := writeRunFile(dir, runInfo{Port: srv.Port(), Token: srv.Token(), URL: srv.URL(), PID: os.Getpid()}); err != nil {
		log.Printf("写入运行信息失败: %v", err)
	}

	// 开机自启：以配置为准同步注册表
	if cfg.Autostart {
		exe, _ := os.Executable()
		if err := am.Enable(exe, []string{"--minimized"}); err != nil {
			log.Printf("同步开机自启失败: %v", err)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go mon.Run(ctx)

	consoleURL := srv.URL()
	if !*minimized {
		openBrowser(consoleURL)
	}

	// 托盘图标跟随最坏告警状态
	go syncTrayIcon(ctx, mon)

	if *dev {
		log.Printf("dev 控制台: %s", consoleURL)
		fmt.Println("dev 控制台:", consoleURL)
		<-ctx.Done()
		return
	}

	tray.Register(tray.Handlers{
		OnOpen:  func() { openBrowser(consoleURL) },
		OnCheck: mon.Trigger,
		OnPauseToggle: func() bool {
			p := !mon.Paused()
			mon.SetPaused(p)
			return p
		},
		OnQuit: cancel,
	})
	tray.Run()
}

func syncTrayIcon(ctx context.Context, mon *monitor.Monitor) {
	hub := mon.Subscribe()
	defer mon.Unsubscribe(hub)
	push := func() {
		snap := mon.Snapshot()
		alert := false
		for _, d := range snap.Drives {
			if d.Alerting {
				alert = true
				break
			}
		}
		tray.SetAlerting(alert)
	}
	push()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hub:
			push()
		case <-t.C:
			push()
		}
	}
}

func mustDir() string {
	dir, err := config.Dir()
	if err != nil {
		fatal(err)
	}
	return dir
}

func readRunURL(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		return ""
	}
	var ri runInfo
	if json.Unmarshal(data, &ri) != nil {
		return ""
	}
	return ri.URL
}

func writeRunFile(dir string, ri runInfo) error {
	data, _ := json.MarshalIndent(ri, "", "  ")
	tmp := filepath.Join(dir, "run.json.tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "run.json"))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "StorageCheck 启动失败:", err)
	log.Println("启动失败:", err)
	os.Exit(1)
}
