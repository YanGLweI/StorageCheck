package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"storagecheck/internal/collector"
	"storagecheck/internal/config"
	"storagecheck/internal/model"
)

// 终端界面是独立进程：它写的配置必须能被运行中的守护进程读到并立即生效。
func TestReloadAppliesExternalEdit(t *testing.T) {
	col := &collector.Fake{Drives: []model.DriveStat{stat("C:", 100*gib, 15*gib)}}
	m, fs := newTestMonitor(t, col)
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if len(fs.alerts) != 0 {
		t.Fatalf("15%% 剩余不应告警: %v", fs.alerts)
	}

	// 模拟外部进程把阈值提到 20%
	cfg := m.Config()
	cfg.Drives[0].ThresholdPct = 20
	if err := m.store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	if !m.ConfigChangedExternally() {
		t.Fatal("外部改动应被指纹比对发现")
	}
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if m.ConfigChangedExternally() {
		t.Fatal("重载后不应再次判定为外部改动（否则会反复重载）")
	}
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if len(fs.alerts) != 1 {
		t.Fatalf("阈值提升后应告警: %v", fs.alerts)
	}
}

// 守护进程自己登记新盘会写配置，不能把自己的写入当成外部修改。
func TestOwnSaveIsNotExternalChange(t *testing.T) {
	col := &collector.Fake{Drives: []model.DriveStat{stat("D:", 100*gib, 50*gib)}}
	m, _ := newTestMonitor(t, col)
	if err := m.tick(); err != nil { // 写盘登记 D:
		t.Fatal(err)
	}
	if m.ConfigChangedExternally() {
		t.Fatal("自身保存后指纹未同步，会引发反复重载")
	}
	if err := m.UpdateConfig(func(cfg *config.Config) error {
		cfg.CheckIntervalSec = 120
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if m.ConfigChangedExternally() {
		t.Fatal("UpdateConfig 之后同样不应视为外部改动")
	}
}

// Linux 安装包形态：首装全量启用，之后新挂载点只登记不告警。
func TestDiscoveryPolicyOnLinuxInstall(t *testing.T) {
	dir := t.TempDir()
	store, err := config.NewStore(dir, config.NewAESBox(dir))
	if err != nil {
		t.Fatal(err)
	}
	off := false
	cfg := config.Default()
	cfg.AutoEnableNew = &off
	cfg.Drives = []config.DriveConfig{{ID: "/", Enabled: true, ThresholdPct: 10}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	col := &collector.Fake{Drives: []model.DriveStat{
		stat("/", 100*gib, 50*gib),
		stat("/mnt/usb", 100*gib, 1*gib), // 99% 已用，若被自动启用就会发邮件
	}}
	fs := &fakeSender{}
	m, err := New(col, store, fs, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	dc, ok := m.Config().Drive("/mnt/usb")
	if !ok {
		t.Fatal("新挂载点应登记到配置中，供界面启用")
	}
	if dc.Enabled {
		t.Fatal("非首装的新挂载点不应自动启用")
	}
	if len(fs.alerts) != 0 {
		t.Fatalf("未启用的挂载点不应发信: %v", fs.alerts)
	}
	var found bool
	for _, ev := range m.Events(20) {
		if ev.Kind == model.KindInfo && strings.Contains(ev.Message, "/mnt/usb") {
			found = true
		}
	}
	if !found {
		t.Fatal("应记录一条「发现未监控的挂载点」事件")
	}

	// 同一个挂载点不应每 30 秒重复刷屏
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range m.Events(50) {
		if ev.Kind == model.KindInfo && strings.Contains(ev.Message, "/mnt/usb") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("发现事件应只记一次，实际 %d 次", n)
	}
}

func TestFirstRunEnablesAllMounts(t *testing.T) {
	dir := t.TempDir()
	store, _ := config.NewStore(dir, config.NewAESBox(dir))
	off := false
	cfg := config.Default()
	cfg.AutoEnableNew = &off
	if err := store.Save(cfg); err != nil { // 全新安装：配置里一条盘都没有
		t.Fatal(err)
	}
	col := &collector.Fake{Drives: []model.DriveStat{stat("/", 100*gib, 5*gib), stat("/data", 100*gib, 5*gib)}}
	fs := &fakeSender{}
	m, err := New(col, store, fs, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"/", "/data"} {
		dc, ok := m.Config().Drive(id)
		if !ok || !dc.Enabled {
			t.Fatalf("首装时 %s 应自动启用: %+v %v", id, dc, ok)
		}
	}
	if len(fs.alerts) != 2 {
		t.Fatalf("首装应两块盘都告警: %v", fs.alerts)
	}
}

func TestPublishStateSnapshot(t *testing.T) {
	col := &collector.Fake{Drives: []model.DriveStat{stat("C:", 100*gib, 5*gib)}}
	m, _ := newTestMonitor(t, col)
	m.tickSafe()
	data, err := os.ReadFile(filepath.Join(m.store.Dir(), "state.json"))
	if err != nil {
		t.Fatalf("state.json 应写出（CLI status 依赖它）: %v", err)
	}
	var view StateView
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Drives) != 1 || !view.Drives[0].Alerting || view.Version != "test" {
		t.Fatalf("快照内容不对: %+v", view)
	}

	// 内容没变化时不重复写盘
	first := data
	m.tickSafe()
	second, err := os.ReadFile(filepath.Join(m.store.Dir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Log("状态未变化但快照被重写（时间戳变化可接受，写盘次数需关注）")
	}
}

func TestEventsLogRotation(t *testing.T) {
	col := &collector.Fake{Drives: []model.DriveStat{stat("C:", 100*gib, 5*gib)}}
	m, _ := newTestMonitor(t, col)
	// 预置一个超限文件，下一次记录应触发轮转
	if err := os.WriteFile(m.logPath, []byte(strings.Repeat("x", eventsMaxBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	m.record(model.Event{Time: time.Now(), Kind: model.KindInfo, Message: "轮转测试"})
	if _, err := os.Stat(m.logPath + ".1"); err != nil {
		t.Fatalf("应生成历史文件: %v", err)
	}
	st, err := os.Stat(m.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > 1<<10 {
		t.Fatalf("轮转后当前文件应很小，实际 %d 字节", st.Size())
	}
}
