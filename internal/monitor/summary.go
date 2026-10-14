package monitor

import (
	"fmt"
	"log"
	"sort"
	"time"

	"storagecheck/internal/config"
	"storagecheck/internal/model"
)

// summaryInterval 控制「最差磁盘」心跳日志的节奏。
// Linux 形态没有图形控制台，出问题时唯一的可见面就是 journald：
// 只写告警事件的话，一台从没跌破阈值的机器在日志里近乎静默，
// 无法区分「一切正常」和「服务卡住/邮件配错」。
const summaryInterval = 15 * time.Minute

// logSummary 周期性输出一行运行状态摘要。
func (m *Monitor) logSummary() {
	m.mu.Lock()
	now := time.Now()
	if !m.lastSummary.IsZero() && now.Sub(m.lastSummary) < summaryInterval {
		m.mu.Unlock()
		return
	}
	m.lastSummary = now
	stats := make([]model.DriveStat, 0, len(m.stats))
	for _, s := range m.stats {
		stats = append(stats, s)
	}
	paused := m.paused
	cfg := m.cfg
	m.mu.Unlock()

	if len(stats) == 0 {
		log.Printf("状态心跳：没有可采集的磁盘（暂停=%v）", paused)
		return
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].FreePct < stats[j].FreePct })
	worst := stats[0]
	thr := 0.0
	if dc, ok := cfg.Drive(worst.ID); ok {
		thr = dc.ThresholdPct
	}
	log.Printf("状态心跳：%d 块磁盘，最差 %s 剩余 %.1f%%（阈值 %.0f%%，%s），告警 %d 块，邮件配置 %s",
		len(stats), worst.ID, worst.FreePct, thr, m.worstStateText(worst.ID), m.alertCount(), smtpStateText(cfg))
}

func (m *Monitor) worstStateText(id string) string {
	m.rtMu.Lock()
	defer m.rtMu.Unlock()
	rt, ok := m.runtimes[id]
	if !ok {
		return "未监控"
	}
	if rt.alerting {
		return "告警中"
	}
	return "正常"
}

func (m *Monitor) alertCount() int {
	m.rtMu.Lock()
	defer m.rtMu.Unlock()
	n := 0
	for _, rt := range m.runtimes {
		if rt.alerting {
			n++
		}
	}
	return n
}

func smtpStateText(cfg *config.Config) string {
	switch {
	case cfg.SMTP.Host == "":
		return "未配置"
	case len(cfg.SMTP.Recipients) == 0:
		return "缺收件人"
	default:
		return fmt.Sprintf("就绪（%s:%d）", cfg.SMTP.Host, cfg.SMTP.Port)
	}
}
