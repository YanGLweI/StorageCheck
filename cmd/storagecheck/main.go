package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"storagecheck/internal/autostart"
	"storagecheck/internal/config"
	"storagecheck/internal/mailer"
	"storagecheck/internal/monitor"
	"storagecheck/internal/tray"
)

var version = "1.0.0-dev"

type runOptions struct {
	dev       bool
	minimized bool
	configDir string
}

type runInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	URL   string `json:"url"`
	PID   int    `json:"pid"`
}

// console 是「本地控制台」这一平台差异点：
// Windows/macOS 开发态是内嵌 Web 服务 + 浏览器窗口，Linux 无 GUI 版则是空实现，
// 于是 internal/server（含 go:embed 的前端）完全不进 Linux 二进制。
type console interface {
	Start() error
	// URL 为空表示本平台没有控制台，此时不写 run.json、也不开浏览器。
	URL() string
	Port() int
	Token() string
	Shutdown(ctx context.Context)
}

type consoleArgs struct {
	mon          *monitor.Monitor
	store        *config.Store
	version      string
	autostartSet func(enable bool) error
	autostartGet func() bool
}

// liveSender 每次发信时按当前配置构建 mailer（改配置即时生效）。
type liveSender struct{ store *config.Store }

func (l *liveSender) mk() (*mailer.Mailer, error) {
	cfg, err := l.store.Load()
	if err != nil {
		return nil, err
	}
	pass, err := smtpPassword(l.store, cfg)
	if err != nil {
		return nil, err
	}
	return mailer.New(cfg.SMTP, pass), nil
}

// smtpPassword 优先取 systemd 凭据或环境变量里的明文授权码，其次才是落盘密文。
// 无 GUI 部署常用挂载凭据/EnvironmentFile 提供密码，避免密钥文件与配置同目录的局限。
func smtpPassword(store *config.Store, cfg *config.Config) (string, error) {
	if p, ok := readCredentialFile(); ok {
		return p, nil
	}
	if p := os.Getenv(EnvSMTPPassword); p != "" {
		return p, nil
	}
	return store.OpenSecret(cfg.SMTP.PassEnc)
}

const EnvSMTPPassword = "STORAGECHECK_SMTP_PASSWORD"

func readCredentialFile() (string, bool) {
	path := credentialPath()
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	s := strings.TrimRight(string(data), "\r\n")
	return s, s != ""
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

// runApp 是各平台共用的启动编排：解析目录 → 加载配置 → 单实例 → 自启同步
// → 采集器/监控/控制台 → 阻塞到退出信号 → 优雅收尾。
func runApp(o runOptions) error {
	dir, err := config.ResolveDir(o.configDir)
	if err != nil {
		return fmt.Errorf("确定配置目录失败: %w", err)
	}
	setupLogging(dir)

	box := newSecretBox(dir)
	store, err := config.NewStore(dir, box)
	if err != nil {
		return err
	}
	cfg, err := store.Load()
	if err != nil {
		log.Printf("加载配置失败，使用默认配置: %v", err)
		cfg = config.Default()
	}

	// 运行数据（快照、事件流、锁）与配置分目录：RPM 布局下数据落在 /var/lib/storagecheck
	if data, explicit := config.ResolveDataDir(dir); data != dir || explicit {
		if err := store.UseDataDir(data); err != nil {
			log.Printf("运行数据目录 %s 不可用，改用配置目录: %v", data, err)
		}
	}

	// 单实例：Windows 上已有实例则唤起它的控制台后退出
	release, acquired, err := acquireLock(store.DataDir())
	if err != nil {
		return err
	}
	if !acquired {
		return alreadyRunning(store, o)
	}
	defer release()

	// 开机自启以系统启动项为事实来源：安装向导勾选、手工改注册表/单位文件都要回流到配置
	am := newAutostartManager(dir)
	exePath, exeErr := os.Executable()
	if exeErr != nil {
		log.Printf("无法确定自身路径，跳过开机自启状态同步: %v", exeErr)
	} else if enabled, changed, aerr := autostart.Reconcile(am, exePath, autostartArgs(), cfg.Autostart); aerr != nil {
		log.Printf("同步开机自启状态失败: %v", aerr)
	} else if changed {
		cfg.Autostart = enabled
		if err := store.Save(cfg); err != nil {
			log.Printf("写入开机自启状态失败: %v", err)
		}
	}

	var mon *monitor.Monitor
	// 采集规则来自运行中的配置（网络盘开关），用取值函数避免重启才生效
	col := newCollector(o.dev, func() *config.Config {
		if mon == nil {
			return cfg
		}
		return mon.Config()
	})
	mon, err = monitor.New(col, store, &liveSender{store}, version)
	if err != nil {
		return err
	}

	c, err := newConsole(consoleArgs{
		mon:     mon,
		store:   store,
		version: version,
		autostartSet: func(enable bool) error {
			if !enable {
				return am.Disable()
			}
			return am.Enable(exePath, autostartArgs())
		},
		// 界面展示真实的启动项状态，而不是可能过期的配置缓存
		autostartGet: func() bool {
			cmd, err := am.Command()
			return err == nil && strings.TrimSpace(cmd) != ""
		},
	})
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		return err
	}

	if url := c.URL(); url != "" {
		if err := writeRunFile(store.DataDir(), runInfo{Port: c.Port(), Token: c.Token(), URL: url, PID: os.Getpid()}); err != nil {
			log.Printf("写入运行信息失败: %v", err)
		}
		if !o.minimized {
			presentOrOpenConsole(url, c.Port())
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// 先记启动再生效：日志行早于首次采样，journal 里顺序就不会反过来
	log.Printf("监控已启动：主机 %s，配置目录 %s，数据目录 %s", mon.Hostname(), store.Dir(), store.DataDir())
	monDone := make(chan struct{})
	go func() {
		defer close(monDone)
		mon.Run(ctx)
	}()

	blockUntilShutdown(ctx, cancel, mon, c, monDone)
	shutdown(mon, c, monDone)
	return nil
}

// blockUntilShutdown 各平台的「常驻方式」：Windows 阻塞在托盘消息循环，
// Linux 阻塞在信号循环（SIGHUP 重载、SIGUSR2 立即检查、SIGUSR1 暂停切换）。
// 非 GUI 平台若直接返回，进程会立刻退出——这正是原 !windows 分支的隐患。
func blockUntilShutdown(ctx context.Context, cancel context.CancelFunc, mon *monitor.Monitor, c console, monDone chan struct{}) {
	defaultBlock(ctx, cancel, mon, c, monDone)
}

func shutdown(mon *monitor.Monitor, c console, monDone chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// 在途邮件必须发完或超时，否则会出现「记录说发过了、邮件没出去」
	mon.WaitSends(15 * time.Second)
	c.Shutdown(ctx)
	cancel()
	select {
	case <-monDone:
		log.Println("监控已停止：采样循环已退出，在途邮件已处理完")
	case <-time.After(5 * time.Second):
		log.Println("监控循环未在 5 秒内退出，放弃等待")
	}
}

// alreadyRunning 处理「已有实例」：桌面平台唤起既有窗口，无 GUI 平台只提示。
func alreadyRunning(store *config.Store, o runOptions) error {
	dir := store.DataDir()
	if msg := alreadyRunningNotice(dir); msg != "" {
		log.Println(msg)
		return nil
	}
	ri, ok := readRunInfo(dir)
	if !ok || ri.URL == "" {
		log.Println("已有实例在运行，但读不到 run.json，无法唤起其窗口")
		return nil
	}
	if o.minimized {
		log.Println("已有实例在运行，--minimized 启动故不唤起窗口")
		return nil
	}
	presentOrOpenConsole(ri.URL, ri.Port)
	return nil
}

// syncTrayIcon 让托盘图标跟随最坏告警状态（仅 Windows 用到真实托盘）。
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

func readRunInfo(dir string) (runInfo, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		return runInfo{}, false
	}
	var ri runInfo
	if json.Unmarshal(data, &ri) != nil {
		return runInfo{}, false
	}
	return ri, true
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
