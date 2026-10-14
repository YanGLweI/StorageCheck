package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	fastInterval    = 30 * time.Second    // 廉价采样节奏
	deltaGate       = 2.0                 // 剩余百分比变化超过该值时提前评估
	sendRetryGap    = 15 * time.Minute    // 发信失败后的最早重试
	eventsMem       = 500                 // 内存环形缓冲条数
	eventsMaxBytes  = 5 << 20             // 事件日志单文件大小上限，超过即轮转
	eventsKeep      = 3                   // 轮转保留的历史文件数
	stalePruneAfter = 30 * 24 * time.Hour // 未启用且长期不存在的挂载条目在保存时清理
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

	// lastSeen 记录每个配置条目最后一次实际出现的时刻，用于清理长期消失的未启用条目。
	lastSeen map[string]time.Time
	// statePath 是当前状态快照文件；CLI/TUI 通过它读取实时用量，取代原 Web API。
	statePath string
	stateFP   string
	// cfgFP 是配置内容的哈希。守护进程自己写盘（登记新盘）不会触发重载，
	// 只有终端界面改动的哈希与记录值不同才认定外部修改。
	cfgFP string
	// lastSummary 控制「最差磁盘」心跳日志的节流。
	lastSummary time.Time
	// sends 是在途发信计数，停止时需要等它归零，避免半封邮件。
	sends atomic.Int64

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
		col:       col,
		store:     store,
		send:      send,
		host:      host,
		logPath:   filepath.Join(store.DataDir(), model.EventsFileName),
		statePath: filepath.Join(store.DataDir(), model.StateFileName),
		version:   version,
		cfg:       cfg,
		runtimes:  map[string]*driveRuntime{},
		stats:     map[string]model.DriveStat{},
		lastSeen:  map[string]time.Time{},
		trigger:   make(chan struct{}, 1),
	}
	for _, d := range cfg.Drives {
		m.lastSeen[d.ID] = time.Now()
	}
	m.syncFingerprint()
	return m, nil
}

func (m *Monitor) syncFingerprint() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recomputeFingerprintLocked()
}

// recomputeFingerprintLocked 记录「当前磁盘内容」的哈希，调用方需持有 m.mu。
func (m *Monitor) recomputeFingerprintLocked() {
	fp, err := m.store.Fingerprint()
	if err != nil {
		return
	}
	m.cfgFP = fp
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
	m.recomputeFingerprintLocked()
	// 阈值/开关等配置本身就是状态输入：改完立刻重新评估，界面即时反映
	m.force.Store(true)
	m.wake()
	return nil
}

// Reload 从磁盘重新读取配置并立即生效，不写盘。
// 终端界面（独立进程）保存配置后发 SIGHUP，或由监控循环发现文件变化时调用。
func (m *Monitor) Reload() error {
	cfg, err := m.store.Load()
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	m.syncFingerprint()
	m.force.Store(true)
	m.wake()
	m.record(model.Event{Time: time.Now(), Kind: model.KindInfo, Message: "配置已重新加载"})
	return nil
}

// ConfigChangedExternally 报告配置文件是否被其它进程改过（监控循环每个 tick 检查）。
func (m *Monitor) ConfigChangedExternally() bool {
	fp, err := m.store.Fingerprint()
	if err != nil || fp == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return fp != m.cfgFP
}

// WaitSends 等待在途的邮件发送完成，用于停止时不留半封邮件。
func (m *Monitor) WaitSends(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for m.sends.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
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
	// 终端界面是独立进程，改配置只落文件；用内容哈希发现外部修改后立即生效。
	if m.ConfigChangedExternally() {
		if err := m.Reload(); err != nil {
			m.record(model.Event{Time: time.Now(), Kind: model.KindError, Message: "重新加载配置失败: " + err.Error()})
		}
	}
	if err := m.tick(); err != nil {
		m.record(model.Event{Time: time.Now(), Kind: model.KindError, Message: "采集失败: " + err.Error()})
	}
	m.publishState()
	m.logSummary()
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
		m.lastSeen[s.ID] = now
	}
	// 新盘登记：Windows（auto_enable_new 未设置或为真）沿用自动启用；
	// Linux 安装包把它设为 false —— 首次安装（配置里一条都没有）时全量启用，
	// 之后新出现的挂载点只登记为未启用并记一条事件，由管理员在界面里决定。
	changed := false
	firstRun := len(m.cfg.Drives) == 0
	var justDiscovered []string
	for _, s := range stats {
		if _, ok := m.cfg.Drive(s.ID); ok {
			continue
		}
		enable := m.cfg.AutoEnableNewDrives() || firstRun
		m.cfg.Drives = append(m.cfg.Drives, config.DriveConfig{ID: s.ID, Enabled: enable, ThresholdPct: config.DefaultThresholdPct})
		changed = true
		if !enable {
			justDiscovered = append(justDiscovered, s.ID)
		}
	}
	// 长期消失且未启用的条目清理，避免配置里堆满历史挂载点
	pruned := 0
	keptDrives := m.cfg.Drives[:0]
	for _, d := range m.cfg.Drives {
		seen, ok := m.lastSeen[d.ID]
		if !ok {
			seen = now
		}
		if _, present := m.stats[d.ID]; present || d.Enabled || now.Sub(seen) < stalePruneAfter {
			keptDrives = append(keptDrives, d)
			continue
		}
		pruned++
	}
	if pruned > 0 {
		m.cfg.Drives = keptDrives
		changed = true
	}
	if changed {
		if err := m.store.Save(m.cfg); err != nil {
			m.mu.Unlock()
			return fmt.Errorf("保存新盘配置失败: %w", err)
		}
		m.recomputeFingerprintLocked()
	}
	paused := m.paused
	interval := time.Duration(m.cfg.CheckIntervalSec) * time.Second
	cfgSnap := *m.cfg
	cfgSnap.Drives = append([]config.DriveConfig(nil), m.cfg.Drives...)
	m.mu.Unlock()

	// 锁外记事件：自动发现但未被启用的挂载点要让管理员看得见
	for _, id := range justDiscovered {
		m.record(model.Event{Time: now, Kind: model.KindInfo, Drive: id,
			Message: fmt.Sprintf("发现未监控的挂载点 %s，可在终端界面的磁盘配置中启用", id)})
	}

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
			m.sends.Add(1)
			err := m.send.SendRecovery(m.host, s.ID, s.Label, s.Total, s.Free, s.UsedPct, s.FreePct, thr)
			m.sends.Add(-1)
			if err != nil {
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
	m.sends.Add(1)
	defer m.sends.Add(-1)
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

	line, _ := json.Marshal(ev)
	m.appendEventLog(append(line, '\n'))

	for _, h := range hubs {
		select {
		case h <- ev:
		default:
		}
	}
}

// appendEventLog 追加事件，超过上限先轮转，避免无界增长。
func (m *Monitor) appendEventLog(line []byte) {
	if st, err := os.Stat(m.logPath); err == nil && st.Size() > eventsMaxBytes {
		for i := eventsKeep - 1; i >= 1; i-- {
			os.Rename(fmt.Sprintf("%s.%d", m.logPath, i), fmt.Sprintf("%s.%d", m.logPath, i+1))
		}
		os.Rename(m.logPath, m.logPath+".1")
	}
	f, err := os.OpenFile(m.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	f.Write(line)
	f.Close()
}

// publishState 写出实时快照，供 storagecheck status / 终端界面读取（无端口、无 IPC）。
// 内容未变化时不写盘，减少对生产磁盘的额外 I/O。
func (m *Monitor) publishState() {
	view := m.Snapshot()
	// 时间戳每次都变，不参与「是否有变化」的判定
	stable := view
	stable.UpdatedAt = time.Time{}
	data, err := json.MarshalIndent(stable, "", "  ")
	if err != nil {
		return
	}
	sum := sha256.Sum256(data)
	fp := hex.EncodeToString(sum[:8])
	m.mu.Lock()
	unchanged := fp == m.stateFP
	m.stateFP = fp
	m.mu.Unlock()
	if unchanged {
		return
	}
	full, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return
	}
	writeFileAtomic(m.statePath, full)
}

// writeFileAtomic 先写临时文件再 rename，读者不会看到半截 JSON。
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
