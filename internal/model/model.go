package model

import "time"

// DriveStat 是一块磁盘的实时容量信息。
type DriveStat struct {
	ID      string  `json:"id"`    // 盘符，如 "C:"；Linux 为挂载点，如 "/"
	Label   string  `json:"label"` // 卷标
	Total   uint64  `json:"total"`
	Free    uint64  `json:"free"`
	UsedPct float64 `json:"used_pct"` // 已用百分比 0-100
	FreePct float64 `json:"free_pct"` // 剩余百分比 0-100
}

type EventKind string

const (
	KindAlert   EventKind = "alert"
	KindRecover EventKind = "recovery"
	KindInfo    EventKind = "info"
	KindError   EventKind = "error"
)

// Event 是告警/恢复/错误等时间线条目。
type Event struct {
	Time      time.Time `json:"time"`
	Kind      EventKind `json:"kind"`
	Drive     string    `json:"drive"`
	Message   string    `json:"message"`
	FreePct   float64   `json:"free_pct"`
	Threshold float64   `json:"threshold"`
}
