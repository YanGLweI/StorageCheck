// Package state 读取守护进程落在配置目录里的运行产物（state.json / events.log），
// 并在服务未运行时退回一次性采集。CLI 与终端界面共用，保证两者口径一致。
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"storagecheck/internal/collector"
	"storagecheck/internal/config"
	"storagecheck/internal/model"
	"storagecheck/internal/monitor"
)

// Freshness 是快照被视为「还跟着服务走」的时限：超过它说明守护进程已停或卡住，
// 界面改用即时采集，避免显示几小时前的数字。
const Freshness = 5 * time.Minute

func StatePath(dir string) string { return filepath.Join(dir, model.StateFileName) }

func EventsPath(dir string) string { return filepath.Join(dir, model.EventsFileName) }

// LockPath 是单实例锁文件，内容就是守护进程的 pid。
func LockPath(dir string) string { return filepath.Join(dir, model.LockFileName) }

// DaemonPID 读出守护进程 pid，并确认这个进程真的是我们自己：
// 服务停止后锁文件里的旧 pid 会留着，直接发信号可能打到复用该 pid 的无关进程。
func DaemonPID(dataDir string) (int, bool) {
	data, err := os.ReadFile(LockPath(dataDir))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil || strings.TrimSpace(string(comm)) != "storagecheck" {
		return 0, false
	}
	return pid, true
}

// Read 读取运行中的快照；第二个返回值表示快照是否够新。
// 文件缺失或损坏不算错误，只报告「不新鲜」，由调用方决定是否退回采集。
func Read(dir string) (monitor.StateView, bool) {
	var view monitor.StateView
	data, err := os.ReadFile(StatePath(dir))
	if err != nil {
		return view, false
	}
	if json.Unmarshal(data, &view) != nil || view.UpdatedAt.IsZero() {
		return view, false
	}
	return view, time.Since(view.UpdatedAt) < Freshness
}

// View 给出用于展示的状态：优先用新鲜快照，否则即时采集，并把快照里的告警态带过来。
// 第二个返回值为 true 表示结果来自离线采集（不含告警态，界面需要说明）。
func View(dir string, col collector.Collector, cfg *config.Config, version string) (monitor.StateView, bool, error) {
	if view, fresh := Read(dir); fresh {
		return view, false, nil
	}
	stats, err := col.List()
	if err != nil {
		return monitor.StateView{}, true, fmt.Errorf("采集磁盘信息失败: %w", err)
	}
	host, _ := os.Hostname()
	view := monitor.StateView{
		UpdatedAt: time.Now(),
		Hostname:  host,
		Version:   version,
		SMTPReady: smtpReady(cfg),
	}
	snap, hadSnap := Read(dir)
	// 告警态是守护进程的状态机算出来的，即时采集拿不到，只在能对上时沿用旧值。
	alerting := make(map[string]monitor.DriveView, len(snap.Drives))
	for _, d := range snap.Drives {
		alerting[d.ID] = d
	}
	for _, s := range stats {
		dv := monitor.DriveView{DriveStat: s}
		if dc, ok := cfg.Drive(s.ID); ok {
			dv.Enabled = dc.Enabled
			dv.ThresholdPct = dc.ThresholdPct
		}
		if old, ok := alerting[s.ID]; ok && hadSnap {
			dv.Alerting = old.Alerting
			dv.LastAlertAt = old.LastAlertAt
		}
		view.Drives = append(view.Drives, dv)
	}
	return view, true, nil
}

func smtpReady(cfg *config.Config) bool {
	return cfg.SMTP.Host != "" && cfg.SMTP.User != "" && len(cfg.SMTP.Recipients) > 0
}

// Events 读取事件流末尾 limit 条，按时间倒序返回。文件不存在返回空切片。
// 轮转出的 .1/.2 历史不在此处拼接：日常排障看当前文件就够，
// 需要考古时直接 cat 那些文件更清楚。
func Events(dir string, limit int) ([]model.Event, error) {
	data, err := os.ReadFile(EventsPath(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	lines := strings.Split(string(data), "\n")
	out := make([]model.Event, 0, limit)
	for i := len(lines) - 1; i >= 0 && len(out) < limit; i-- {
		var e model.Event
		if json.Unmarshal([]byte(lines[i]), &e) == nil && !e.Time.IsZero() {
			out = append(out, e)
		}
	}
	return out, nil
}
