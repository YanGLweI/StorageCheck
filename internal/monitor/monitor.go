package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"storagecheck/internal/collector"
	"storagecheck/internal/config"
	"storagecheck/internal/model"
)

// Sender 抽象邮件发送，便于单测注入。
type Sender interface {
	SendAlert(host, driveID, label string, total, free uint64, usedPct, freePct, threshold float64, escalated bool) error
	SendRecovery(host, driveID, label string, total, free uint64, usedPct, freePct, threshold float64) error
}

const (
	fastInterval = 30 * time.Second // 廉价采样节奏
	deltaGate    = 2.0              // 剩余百分比变化超过该值时提前评估
	sendRetryGap = 15 * time.Minute // 发信失败后的最早重试
	eventsMem    = 500              // 内存环形缓冲条数
)

type driveRuntime struct {
	alerting  bool
	lastAlert time.Time
	nextTry   time.Time
	lastEval  time.Time
	lastFree  float64
}

// DriveView 是 API 输出的单盘视图。
type DriveView struct {
	model.DriveStat
	Enabled      bool       `json:"enabled"`
	ThresholdPct float64    `json:"threshold_pct"`
	Alerting     bool       `json:"alerting"`
	LastAlertAt  *time.Time `json:"last_alert_at,omitempty"`
}

// StateView 是 API 输出的全局快照。
type StateView struct {
	Drives    []DriveView `json:"drives"`
	UpdatedAt time.Time   `json:"updated_at"`
	Paused    bool        `json:"paused"`
	Hostname  string      `json:"hostname"`
	Version   string      `json:"version"`
	SMTPReady bool        `json:"smtp_ready"`
}

type Monitor struct {
	col     collector.Collector
	store   *config.Store
	send    Sender
	host    string
	logPath string
	version string

	mu       sync.Mutex
	rtMu     sync.Mutex // 保护 driveRuntime 字段（评估循环写、快照读）
	cfg      *config.Config
	runtimes map[string]*driveRuntime
	stats    map[string]model.DriveStat
	events   []model.Event
	paused   bool

	trigger chan struct{}
	force   atomic.Bool // 置位时下一次 tick 跳过采样周期判断，立即评估
	hubs    []chan model.Event
	hubsMu  sync.Mutex
}

func New(col collector.Collector, store *config.Store, send Sender, version string) (*Monitor, error) {
	host, _ := os.Hostname()
	cfg, err := store.Load()
	if err != nil {
		return nil, err
	}
	m := &Monitor{
		col:      col,
		store:    store,
		send:     send,
		host:     host,
		logPath:  filepath.Join(store.Dir(), "events.log"),
		version:  version,
		cfg:      cfg,
		runtimes: map[string]*driveRuntime{},
		stats:    map[string]model.DriveStat{},
		trigger:  make(chan struct{}, 1),
	}
	return m, nil
}

func (m *Monitor) Hostname() string { return m.host }

// SetPaused 托盘「暂停监控」开关。
func (m *Monitor) SetPaused(v bool) {
	m.mu.Lock()
	m.paused = v
	m.mu.Unlock()
}

func (m *Monitor) Paused() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.paused
}

// Trigger 请求立即执行一次采集+评估（强制评估，不等采样周期到点）。
func (m *Monitor) Trigger() {
	m.force.Store(true)
	m.wake()
}

// wake 唤醒评估循环；循环正忙时忽略（下一次 tick 会带上 force）。
func (m *Monitor) wake() {
	select {
	case m.trigger <- struct{}{}:
	default:
	}
}

// UpdateConfig 修改-校验-保存-生效一体化，无需重启。
func (m *Monitor) UpdateConfig(fn func(*config.Config) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	clone := *m.cfg
	clone.Drives = append([]config.DriveConfig(nil), m.cfg.Drives...)
	clone.SMTP.Recipients = append([]string(nil), m.cfg.SMTP.Recipients...)
	if err := fn(&clone); err != nil {
		return err
	}
	if err := m.store.Save(&clone); err != nil {
		return err
	}
	m.cfg = &clone
	// 阈值/开关等配置本身就是状态输入：改完立刻重新评估，界面即时反映
	m.force.Store(true)
	m.wake()
	return nil
}

func (m *Monitor) Config() *config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// Run 启动监控循环，ctx 取消时退出。
func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(fastInterval)
	defer ticker.Stop()
	m.tickSafe()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tickSafe()
		case <-m.trigger:
			m.tickSafe()
		}
	}
}

func (m *Monitor) tickSafe() {
	defer func() {
		if r := recover(); r != nil {
			m.record(model.Event{Time: time.Now(), Kind: model.KindError, Message: fmt.Sprintf("监控循环异常: %v", r)})
		}
	}()
	if err := m.tick(); err != nil {
		m.record(model.Event{Time: time.Now(), Kind: model.KindError, Message: "采集失败: " + err.Error()})
	}
}

func (m *Monitor) tick() error {
	stats, err := m.col.List()
	if err != nil {
		return err
	}
	now := time.Now()
	m.mu.Lock()
	m.stats = make(map[string]model.DriveStat, len(stats))
	for _, s := range stats {
		m.stats[s.ID] = s
	}
	// 新盘自动纳入监控（默认启用、默认阈值）
	changed := false
	for _, s := range stats {
		if _, ok := m.cfg.Drive(s.ID); !ok {
			m.cfg.Drives = append(m.cfg.Drives, config.DriveConfig{ID: s.ID, Enabled: true, ThresholdPct: config.DefaultThresholdPct})
			changed = true
		}
	}
	if changed {
		if err := m.store.Save(m.cfg); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("保存新盘配置失败: %w", err)
		}
	}
	paused := m.paused
	interval := time.Duration(m.cfg.CheckIntervalSec) * time.Second
	cfgSnap := *m.cfg
	cfgSnap.Drives = append([]config.DriveConfig(nil), m.cfg.Drives...)
	m.mu.Unlock()

	if paused {
		return nil
	}
	// 强制评估：立即检查或配置变更后不等采样周期到点
	forced := m.force.Swap(false)
	for _, s := range stats {
		dc, ok := cfgSnap.Drive(s.ID)
		if !ok || !dc.Enabled {
			continue
		}
		rt := m.runtime(s.ID)
		due := rt.lastEval.IsZero() || now.Sub(rt.lastEval) >= interval
		delta := !rt.lastEval.IsZero() && absF(s.FreePct-rt.lastFree) >= deltaGate
		if due || delta || forced {
			m.evaluate(rt, now, s, *dc, &cfgSnap)
		}
	}
	return nil
}

func (m *Monitor) runtime(id string) *driveRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt, ok := m.runtimes[id]
	if !ok {
		rt = &driveRuntime{}
		m.runtimes[id] = rt
	}
	return rt
}

// evaluate 执行单盘告警状态机：触发 / 冷却 / 升级提醒 / 迟滞恢复。
// 仅评估循环单写者：先把 runtime 拷出本地做状态转移与网络发送，最后写回（rtMu 供 Snapshot 读）。
func (m *Monitor) evaluate(rt *driveRuntime, now time.Time, s model.DriveStat, dc config.DriveConfig, cfg *config.Config) {
	m.rtMu.Lock()
	local := *rt
	m.rtMu.Unlock()
	defer func() {
		m.rtMu.Lock()
		*rt = local
		m.rtMu.Unlock()
	}()

	local.lastEval = now
	local.lastFree = s.FreePct
	thr := dc.ThresholdPct

	if !local.alerting {
		if s.FreePct < thr {
			local.alerting = true
			m.fireAlert(&local, now, s, thr, false)
		}
		return
	}

	// 已处于告警态
	if s.FreePct >= thr+cfg.RecoveryHysteresisPct {
		local.alerting = false
		local.lastAlert = time.Time{}
		m.record(model.Event{Time: now, Kind: model.KindRecover, Drive: s.ID, Message: fmt.Sprintf("%s 空间已恢复（剩余 %.1f%%）", s.ID, s.FreePct), FreePct: s.FreePct, Threshold: thr})
		if cfg.NotifyOnRecovery {
			if err := m.send.SendRecovery(m.host, s.ID, s.Label, s.Total, s.Free, s.UsedPct, s.FreePct, thr); err != nil {
				m.record(model.Event{Time: now, Kind: model.KindError, Drive: s.ID, Message: "恢复邮件发送失败: " + err.Error()})
			}
		}
		return
	}

	if s.FreePct < thr {
		cooldown := time.Duration(cfg.CooldownSec) * time.Second
		needResend := local.lastAlert.IsZero() || now.Sub(local.lastAlert) >= cooldown
		if needResend && (local.lastAlert.IsZero() || now.After(local.nextTry)) {
			m.fireAlert(&local, now, s, thr, !local.lastAlert.IsZero())
		}
	}
}

func (m *Monitor) fireAlert(rt *driveRuntime, now time.Time, s model.DriveStat, thr float64, escalated bool) {
	err := m.send.SendAlert(m.host, s.ID, s.Label, s.Total, s.Free, s.UsedPct, s.FreePct, thr, escalated)
	if err != nil {
		rt.nextTry = now.Add(sendRetryGap)
		m.record(model.Event{Time: now, Kind: model.KindError, Drive: s.ID, Message: fmt.Sprintf("告警邮件发送失败: %v（%d 分钟后重试）", err, int(sendRetryGap.Minutes())), FreePct: s.FreePct, Threshold: thr})
		return
	}
	rt.lastAlert = now
	rt.nextTry = time.Time{}
	msg := fmt.Sprintf("%s 剩余空间 %.1f%%，低于阈值 %.0f%%（剩余 %.1f GB）", s.ID, s.FreePct, thr, float64(s.Free)/gib)
	if escalated {
		msg = "持续告警：" + msg
	}
	m.record(model.Event{Time: now, Kind: model.KindAlert, Drive: s.ID, Message: msg, FreePct: s.FreePct, Threshold: thr})
}

const gib = 1024 * 1024 * 1024

func (m *Monitor) record(ev model.Event) {
	m.mu.Lock()
	m.events = append(m.events, ev)
	if len(m.events) > eventsMem {
		m.events = m.events[len(m.events)-eventsMem:]
	}
	hubs := append([]chan model.Event(nil), m.hubs...)
	m.mu.Unlock()

	if f, err := os.OpenFile(m.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		line, _ := json.Marshal(ev)
		f.Write(append(line, '\n'))
		f.Close()
	}
	for _, h := range hubs {
		select {
		case h <- ev:
		default:
		}
	}
}

// Subscribe 订阅实时事件（SSE 用）。
func (m *Monitor) Subscribe() chan model.Event {
	h := make(chan model.Event, 32)
	m.mu.Lock()
	m.hubs = append(m.hubs, h)
	m.mu.Unlock()
	return h
}

func (m *Monitor) Unsubscribe(h chan model.Event) {
	m.mu.Lock()
	for i, c := range m.hubs {
		if c == h {
			m.hubs = append(m.hubs[:i], m.hubs[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	close(h)
}

func (m *Monitor) Events(limit int) []model.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > len(m.events) {
		limit = len(m.events)
	}
	out := make([]model.Event, limit)
	copy(out, m.events[len(m.events)-limit:])
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Snapshot 合并实时用量、配置与告警状态。
func (m *Monitor) Snapshot() StateView {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.stats))
	for id := range m.stats {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	view := StateView{UpdatedAt: time.Now(), Paused: m.paused, Hostname: m.host, Version: m.version}
	for _, id := range ids {
		s := m.stats[id]
		dv := DriveView{DriveStat: s}
		if dc, ok := m.cfg.Drive(id); ok {
			dv.Enabled = dc.Enabled
			dv.ThresholdPct = dc.ThresholdPct
		}
		m.rtMu.Lock()
		rt, rok := m.runtimes[id]
		var rtc driveRuntime
		if rok {
			rtc = *rt
		}
		m.rtMu.Unlock()
		if rok {
			dv.Alerting = rtc.alerting
			if !rtc.lastAlert.IsZero() {
				la := rtc.lastAlert
				dv.LastAlertAt = &la
			}
		}
		view.Drives = append(view.Drives, dv)
	}
	return view
}

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
