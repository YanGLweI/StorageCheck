//go:build linux

package tui

import (
	"syscall"

	"github.com/charmbracelet/bubbles/textinput"

	"storagecheck/internal/collector"
	"storagecheck/internal/config"
	"storagecheck/internal/monitor"
	"storagecheck/internal/state"
)

// backend 是界面各页共享的后端：配置读写、即时采集、与守护进程通信。
type backend struct {
	dir     string // 配置目录
	dataDir string // 运行数据目录（state.json / events.log / .lock）
	version string
	store   *config.Store
	cfg     *config.Config
	col     collector.Collector
	// configError 非空表示磁盘上的配置读不出来，界面正以默认值运行。
	// 终端界面是改配置的唯一入口，这里绝不能因为解析失败就拒绝启动，
	// 否则手工改坏一个字符就得靠编辑器去救。
	configError string
}

func newBackend(dir, dataDir, version string) (*backend, error) {
	// 无 DPAPI，授权码用同目录的 seal.key 做 AES-GCM；文件 0600 属 root。
	store, err := config.NewStore(dir, config.NewAESBoxWithKeyFile(dir, "seal.key"))
	if err != nil {
		return nil, err
	}
	if err := store.UseDataDir(dataDir); err != nil {
		return nil, err
	}
	b := &backend{dir: dir, dataDir: store.DataDir(), version: version, store: store, cfg: config.Default()}
	cfg, err := store.Load()
	if err != nil {
		b.configError = err.Error()
	} else {
		b.cfg = cfg
	}
	// 采集规则跟着内存里的配置走，勾掉「包含网络盘」立刻影响扫描结果。
	b.col = collector.NewDynamic(b.mountRule)
	return b, nil
}

func (b *backend) mountRule() collector.MountRule {
	return collector.MountRule{
		IncludeNetwork:  b.cfg.Linux.IncludeNetwork,
		ExcludePrefixes: b.cfg.Linux.MountExclude,
		MinTotalBytes:   b.cfg.MinTotal(),
	}
}

func (b *backend) drives() ([]collector.MountRow, error) {
	return collector.Diagnose(b.mountRule())
}

func (b *backend) snapshot() (monitor.StateView, bool, error) {
	return state.View(b.dataDir, b.col, b.cfg, b.version)
}

// commit 落盘并通知守护进程热重载：改配置不需要 restart，
// 重启会打断冷却/迟滞状态机，导致同一故障重复告警。
func (b *backend) commit() error {
	// 免认证时不要把一份用不上的密文留在盘上：授权码输入框里「留空 = 不修改」，
	// 这一挡就是界面上唯一的清除入口，取消勾选后需要重新填写。
	if b.cfg.SMTP.NoAuth {
		b.cfg.SMTP.PassEnc = ""
	}
	if err := b.store.Save(b.cfg); err != nil {
		return err
	}
	// 能写成功说明文件已经是合法 JSON，降级提示到此结束。
	b.configError = ""
	b.notify(syscall.SIGHUP)
	return nil
}

// discard 丢弃当前编辑，回到磁盘上的配置。
func (b *backend) discard() {
	if cfg, err := b.store.Load(); err == nil {
		b.cfg = cfg
	}
}

func (b *backend) daemonPID() (int, bool) {
	return state.DaemonPID(b.dataDir)
}

// notify 给守护进程发信号；服务没在跑就返回 false，界面据此提示。
func (b *backend) notify(sig syscall.Signal) bool {
	pid, ok := b.daemonPID()
	if !ok {
		return false
	}
	return syscall.Kill(pid, sig) == nil
}

func (b *backend) running() bool {
	_, ok := b.daemonPID()
	return ok
}

// newPasswordInput 统一口令输入：掩码显示，明文绝不进 argv 也不回显。
func newPasswordInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '*'
	return ti
}
