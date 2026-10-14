package model

import "fmt"

// HumanBytes 用二进制单位渲染容量，界面与 CLI 共用，避免两处四舍五入不一致。
func HumanBytes(v uint64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d B", v)
	}
	suffixes := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	f := float64(v) / unit
	i := 0
	for f >= unit && i < len(suffixes)-1 {
		f /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", f, suffixes[i])
}

// KindLabel 返回事件的中文标签；Kind 本身作为机器可读键名保持不变。
func KindLabel(k EventKind) string {
	switch k {
	case KindAlert:
		return "告警"
	case KindRecover:
		return "恢复"
	case KindError:
		return "错误"
	default:
		return "信息"
	}
}
