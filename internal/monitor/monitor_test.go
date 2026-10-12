package monitor

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"storagecheck/internal/collector"
	"storagecheck/internal/config"
	"storagecheck/internal/model"
)

type fakeSender struct {
	mu      sync.Mutex
	alerts  []string
	recovs  []string
	failNow bool
}

func (f *fakeSender) fail() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failNow
}

func (f *fakeSender) SendAlert(host, id, label string, total, free uint64, used, freepct, thr float64, escalated bool) error {
	if f.fail() {
		return fmt.Errorf("smtp down")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alerts = append(f.alerts, fmt.Sprintf("%s:%v", id, escalated))
	return nil
}

func (f *fakeSender) SendRecovery(host, id, label string, total, free uint64, used, freepct, thr float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recovs = append(f.recovs, id)
	return nil
}

func newTestMonitor(t *testing.T, col collector.Collector) (*Monitor, *fakeSender) {
	t.Helper()
	dir := t.TempDir()
	store, err := config.NewStore(dir, config.NewAESBox(dir))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.CheckIntervalSec = 30
	cfg.CooldownSec = 60
	cfg.Drives = []config.DriveConfig{{ID: "C:", Enabled: true, ThresholdPct: 10}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	fs := &fakeSender{}
	m, err := New(col, store, fs, "test")
	if err != nil {
		t.Fatal(err)
	}
	return m, fs
}

func stat(id string, total, free uint64) model.DriveStat {
	return collector.MakeStat(id, "", total, free)
}

func TestAlertThenRecoveryWithHysteresis(t *testing.T) {
	col := &collector.Fake{Drives: []model.DriveStat{stat("C:", 100*gib, 20*gib)}} // 20% 剩余，正常
	m, fs := newTestMonitor(t, col)
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if len(fs.alerts) != 0 {
		t.Fatalf("正常状态不应告警: %v", fs.alerts)
	}

	col.Drives = []model.DriveStat{stat("C:", 100*gib, 5*gib)} // 5% < 10% 触发
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if len(fs.alerts) != 1 {
		t.Fatalf("应触发 1 次告警, got %v", fs.alerts)
	}

	// 迟滞带内（11% < 10+2）不恢复、不重复发
	col.Drives = []model.DriveStat{stat("C:", 100*gib, 11*gib)}
	m.mu.Lock()
	rt := m.runtimes["C:"]
	m.mu.Unlock()
	now := time.Now().Add(2 * time.Hour) // 超出冷却
	m.evaluate(rt, now, col.Drives[0], config.DriveConfig{ID: "C:", Enabled: true, ThresholdPct: 10}, m.Config())
	if len(fs.alerts) != 1 || len(fs.recovs) != 0 {
		t.Fatalf("迟滞带内应静默: alerts=%v recovs=%v", fs.alerts, fs.recovs)
	}

	// 13% >= 10+2 恢复
	col.Drives = []model.DriveStat{stat("C:", 100*gib, 13*gib)}
	now = now.Add(2 * time.Minute)
	m.evaluate(rt, now, col.Drives[0], config.DriveConfig{ID: "C:", Enabled: true, ThresholdPct: 10}, m.Config())
	if len(fs.recovs) != 1 {
		t.Fatalf("应发送恢复通知: %v", fs.recovs)
	}

	// 再次跌破 -> 再告警（非升级）
	col.Drives = []model.DriveStat{stat("C:", 100*gib, 9*gib)}
	now = now.Add(2 * time.Minute)
	m.evaluate(rt, now, col.Drives[0], config.DriveConfig{ID: "C:", Enabled: true, ThresholdPct: 10}, m.Config())
	if len(fs.alerts) != 2 || fs.alerts[1] != "C::false" {
		t.Fatalf("恢复后应可再次触发: %v", fs.alerts)
	}
}

func TestCooldownSuppressThenEscalate(t *testing.T) {
	col := &collector.Fake{Drives: []model.DriveStat{stat("C:", 100*gib, 5*gib)}}
	m, fs := newTestMonitor(t, col)
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	rt := m.runtimes["C:"]
	m.mu.Unlock()
	dc := config.DriveConfig{ID: "C:", Enabled: true, ThresholdPct: 10}

	// 冷却期内（cooldown=60s）继续变差：不重复发
	before := len(fs.alerts)
	m.evaluate(rt, time.Now().Add(30*time.Second), stat("C:", 100*gib, 2*gib), dc, m.Config())
	if len(fs.alerts) != before {
		t.Fatal("冷却期内不应重复告警")
	}
	// 超冷却：升级提醒
	m.evaluate(rt, time.Now().Add(2*time.Hour), stat("C:", 100*gib, 2*gib), dc, m.Config())
	if len(fs.alerts) != before+1 || fs.alerts[before] != "C::true" {
		t.Fatalf("超冷却应发送升级提醒: %v", fs.alerts)
	}
}

func TestMailFailureRetriesNotSpam(t *testing.T) {
	col := &collector.Fake{Drives: []model.DriveStat{stat("C:", 100*gib, 5*gib)}}
	m, fs := newTestMonitor(t, col)
	fs.failNow = true
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	rt := m.runtimes["C:"]
	m.mu.Unlock()
	dc := config.DriveConfig{ID: "C:", Enabled: true, ThresholdPct: 10}

	// 失败后 nextTry=+15min，5 分钟后重试被抑制
	m.evaluate(rt, time.Now().Add(5*time.Minute), col.Drives[0], dc, m.Config())
	if len(fs.alerts) != 0 {
		t.Fatal("失败重试窗口内不应发送")
	}
	// 16 分钟后成功补发（lastAlert 仍为零值 => 非升级）
	fs.failNow = false
	m.evaluate(rt, time.Now().Add(16*time.Minute), col.Drives[0], dc, m.Config())
	if len(fs.alerts) != 1 || fs.alerts[0] != "C::false" {
		t.Fatalf("超过重试窗口应补发首告警: %v", fs.alerts)
	}
}

func TestDisabledDriveNotEvaluated(t *testing.T) {
	dir := t.TempDir()
	store, _ := config.NewStore(dir, config.NewAESBox(dir))
	cfg := config.Default()
	cfg.Drives = []config.DriveConfig{{ID: "C:", Enabled: false, ThresholdPct: 10}}
	store.Save(cfg)
	col := &collector.Fake{Drives: []model.DriveStat{stat("C:", 100*gib, 1*gib)}}
	fs := &fakeSender{}
	m, err := New(col, store, fs, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if len(fs.alerts) != 0 {
		t.Fatalf("停用盘不应告警: %v", fs.alerts)
	}
}

func TestNewDriveAutoAdded(t *testing.T) {
	col := &collector.Fake{Drives: []model.DriveStat{stat("D:", 100*gib, 50*gib)}}
	m, _ := newTestMonitor(t, col)
	// tick 会把没有配置的 D: 以默认阈值纳入，但 C: 已不在采集列表 => 无错误
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Config().Drive("D:"); !ok {
		t.Fatal("新盘应自动写入配置")
	}
}

func TestSnapshotAndEvents(t *testing.T) {
	col := &collector.Fake{Drives: []model.DriveStat{stat("C:", 100*gib, 5*gib)}}
	m, _ := newTestMonitor(t, col)
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	if len(snap.Drives) != 1 || !snap.Drives[0].Alerting {
		t.Fatalf("快照应含活动告警: %+v", snap.Drives)
	}
	evs := m.Events(10)
	if len(evs) == 0 || evs[0].Kind != model.KindAlert {
		t.Fatalf("事件流应含告警: %+v", evs)
	}
}

// 采样周期未到、用量也没变化时，「立即检查」仍必须完成状态评估。
func TestTriggerForcesEvaluation(t *testing.T) {
	col := &collector.Fake{Drives: []model.DriveStat{stat("C:", 100*gib, 5*gib)}}
	m, _ := newTestMonitor(t, col)
	if err := m.tick(); err != nil { // 5% < 10% 进入告警
		t.Fatal(err)
	}
	if !m.Snapshot().Drives[0].Alerting {
		t.Fatal("前置条件：应处于告警态")
	}

	m.mu.Lock()
	m.runtimes["C:"].lastEval = time.Now() // 刚评估过，周期未到且用量无变化
	m.mu.Unlock()

	m.Trigger()
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if !m.Snapshot().Drives[0].Alerting {
		t.Fatal("未 Trigger 的常规 tick 不应提前评估")
	}

	// 阈值降到 1%：5% 剩余已高于阈值，强制评估应立即解除告警
	if err := m.UpdateConfig(func(cfg *config.Config) error {
		cfg.Drives[0].ThresholdPct = 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.tick(); err != nil {
		t.Fatal(err)
	}
	if d := m.Snapshot().Drives[0]; d.Alerting {
		t.Fatalf("阈值下调后应立即恢复: %+v", d)
	}
}
