package collector

import "storagecheck/internal/model"

// Collector 枚举并采集本机固定磁盘的容量信息。
type Collector interface {
	// List 返回所有应被监控的物理盘（Windows 固定盘 / Linux 真实挂载点）。
	List() ([]model.DriveStat, error)
}

// Fake 用于单元测试与 macOS 开发模式。
type Fake struct {
	Drives []model.DriveStat
	Err    error
}

func (f *Fake) List() ([]model.DriveStat, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	out := make([]model.DriveStat, len(f.Drives))
	copy(out, f.Drives)
	return out, nil
}

func MakeStat(id, label string, total, free uint64) model.DriveStat {
	used := total - free
	return model.DriveStat{
		ID:      id,
		Label:   label,
		Total:   total,
		Free:    free,
		UsedPct: pct(used, total),
		FreePct: pct(free, total),
	}
}

func pct(part, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}
