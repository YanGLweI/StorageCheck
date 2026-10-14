//go:build linux

package collector

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"storagecheck/internal/model"
)

const (
	defaultMinTotalBytes = uint64(1) << 30 // 1 GiB，滤掉 EFI 分区与内存盘
	statfsTimeout        = 3 * time.Second // 失联的网络挂载不能拖住采样循环
)

type linuxCollector struct{ rule MountRule }

func New() Collector { return &linuxCollector{rule: normalizedRule(MountRule{})} }

// NewWithRule 用固定规则构造采集器。
func NewWithRule(rule MountRule) Collector { return &linuxCollector{rule: normalizedRule(rule)} }

// NewDynamic 每次采集时向 provider 取规则，使「是否采集网络盘」这类开关
// 改完配置即刻生效，不必重启服务。provider 返回 nil 时用默认规则。
func NewDynamic(provider func() MountRule) Collector {
	return &dynamicCollector{provider: provider}
}

type dynamicCollector struct{ provider func() MountRule }

func (c *dynamicCollector) List() ([]model.DriveStat, error) {
	var rule MountRule
	if c.provider != nil {
		rule = c.provider()
	}
	return listFrom(normalizedRule(rule))
}

func normalizedRule(r MountRule) MountRule {
	if r.MinTotalBytes == 0 {
		r.MinTotalBytes = defaultMinTotalBytes
	}
	return r
}

// MountRow 是一次采集的完整判定结果，drives 视图据此解释每个挂载点为何采/不采。
type MountRow struct {
	MountEntry
	Wanted   bool   // 通过策略筛选，应当监控
	Keep     bool   // Wanted 且本次 statfs 成功，已进入采集结果
	Reason   string // 未监控的原因（策略排除或采集失败）
	Device   string // 真实块设备名，如 sda3、dm-0
	Total    uint64
	Free     uint64
	VolLabel string
	Network  bool
}

func (r MountRow) Stat() model.DriveStat {
	return MakeStat(r.Mountpoint, MountLabel(r.FSType, r.Device, r.VolLabel), r.Total, r.Free)
}

func (c *linuxCollector) List() ([]model.DriveStat, error) { return listFrom(c.rule) }

// listFrom 把采集行转成 DriveStat；全部应监控的挂载点都失败时才算采集失败，
// 单独一个 U 盘被拔掉不该在事件流里留下错误。
func listFrom(rule MountRule) ([]model.DriveStat, error) {
	rows, err := scanMounts(rule)
	if err != nil {
		return nil, err
	}
	var out []model.DriveStat
	wanted, failed := 0, 0
	for _, r := range rows {
		if !r.Wanted {
			continue
		}
		wanted++
		if !r.Keep {
			failed++
			continue
		}
		out = append(out, r.Stat())
	}
	if wanted == 0 {
		return nil, errors.New("collector: 未发现可监控的本地磁盘挂载点")
	}
	if failed == wanted {
		return nil, fmt.Errorf("collector: %d 个挂载点全部采集失败（statfs 无响应或权限不足）", wanted)
	}
	return out, nil
}

// Diagnose 返回全部挂载点及其筛选结论（供 storagecheck drives 使用）。
func Diagnose(rule MountRule) ([]MountRow, error) { return scanMounts(rule) }

// scanMounts 依次做：类型/前缀筛选 → 真实块设备判定 → 设备号去重 → statfs 与容量下限。
func scanMounts(rule MountRule) ([]MountRow, error) {
	if rule.MinTotalBytes == 0 {
		rule.MinTotalBytes = defaultMinTotalBytes
	}
	entries, err := readMounts()
	if err != nil {
		return nil, err
	}
	decided := DecideMounts(entries, rule)
	labels := volumeLabels()

	rows := make([]MountRow, 0, len(decided))
	for _, d := range decided {
		row := MountRow{MountEntry: d.MountEntry, Wanted: d.Keep, Keep: d.Keep, Reason: d.Reason, Network: d.Network}
		if !row.Wanted {
			rows = append(rows, row)
			continue
		}
		dev, ok := blockDeviceName(d.Mountpoint)
		switch {
		case ok:
			row.Device = dev
			row.VolLabel = labels[dev]
			if !row.Network && !IsPhysicalBlock(dev) {
				row.exclude("非物理存储设备 " + dev)
			}
		case !row.Network:
			row.exclude("无法定位块设备")
		}
		rows = append(rows, row)
	}

	rows = dedupeByDevice(rows)

	var final []MountRow
	for _, row := range rows {
		if !row.Wanted {
			final = append(final, row)
			continue
		}
		total, free, err := statfsTimed(row.Mountpoint, statfsTimeout)
		if err != nil {
			// 应监控但本次没读到：保留 Wanted，让上层区分「不监控」与「监控失败」。
			row.Keep, row.Reason = false, "采集失败: "+err.Error()
			final = append(final, row)
			continue
		}
		if total < rule.MinTotalBytes {
			row.exclude(fmt.Sprintf("容量低于下限 %d GiB", rule.MinTotalBytes/(1<<30)))
			final = append(final, row)
			continue
		}
		row.Total, row.Free = total, free
		final = append(final, row)
	}
	sort.Slice(final, func(i, j int) bool { return final[i].Mountpoint < final[j].Mountpoint })
	return final, nil
}

func (r *MountRow) exclude(reason string) {
	r.Wanted, r.Keep, r.Reason = false, false, reason
}

// dedupeByDevice 在同一底层设备多次出现时保留最短挂载点（/ 优先于 /home）。
func dedupeByDevice(rows []MountRow) []MountRow {
	sorted := append([]MountRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := len(sorted[i].Mountpoint), len(sorted[j].Mountpoint)
		if a != b {
			return a < b
		}
		return sorted[i].Mountpoint < sorted[j].Mountpoint
	})
	seen := map[string]bool{}
	var out []MountRow
	for _, r := range sorted {
		if !r.Wanted {
			out = append(out, r)
			continue
		}
		key := r.Device
		if key == "" {
			key = "mnt:" + r.Mountpoint
		}
		if seen[key] {
			r.exclude("同一设备的重复挂载")
		} else {
			seen[key] = true
		}
		out = append(out, r)
	}
	return out
}

func readMounts() ([]MountEntry, error) {
	for _, p := range []string{"/proc/self/mounts", "/proc/mounts"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		defer f.Close()
		entries := ParseMounts(f)
		if len(entries) == 0 {
			return nil, fmt.Errorf("collector: %s 内容为空", p)
		}
		return entries, nil
	}
	return nil, errors.New("collector: 无法读取 /proc/mounts")
}

// blockDeviceName 反查挂载点所属的真实内核设备名。
// 这一步能识破 mounts 里写成 overlay/tmpfs 的假象，也能识破 snap 的 loop 设备。
func blockDeviceName(path string) (string, bool) {
	maj, min, err := deviceIDs(path)
	if err != nil {
		return "", false
	}
	target, err := filepath.EvalSymlinks(fmt.Sprintf("/sys/dev/block/%d:%d", maj, min))
	if err != nil {
		return "", false
	}
	return filepath.Base(target), true
}

// deviceIDs 取挂载点的 major:minor。只认 statx：glibc 在无原生 stat 的架构上会用
// 旧式 major<<8|minor 回填 st_dev（在 arm64 容器里实测到 253:17 被写成 0xfd11），
// 按内核 new_encode_dev 解位就会算出一个不存在的设备号。
func deviceIDs(path string) (uint32, uint32, error) {
	var sx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_STATX_SYNC_AS_STAT, unix.STATX_ALL, &sx); err != nil {
		return 0, 0, err
	}
	return sx.Dev_major, sx.Dev_minor, nil
}

func statfsTimed(path string, timeout time.Duration) (total, free uint64, err error) {
	type result struct {
		total, free uint64
		err         error
	}
	ch := make(chan result, 1)
	go func() {
		var st unix.Statfs_t
		if err := unix.Statfs(path, &st); err != nil {
			ch <- result{err: err}
			return
		}
		// Frsize 才是容量计算单位（Bsize 是最佳传输尺寸）；Bavail 对应 df 的 Avail，
		// 把 ext4 的 root 预留不算作空闲，与运维在 df 里看到的数字一致。
		fr := uint64(st.Frsize)
		if fr == 0 {
			fr = uint64(st.Bsize)
		}
		ch <- result{total: uint64(st.Blocks) * fr, free: uint64(st.Bavail) * fr}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.total, r.free, r.err
	case <-timer.C:
		return 0, 0, fmt.Errorf("statfs 超时（%s 可能已失联）", path)
	}
}

// volumeLabels 扫 /dev/disk/by-label，返回 真实设备名 → 卷标签。
func volumeLabels() map[string]string {
	out := map[string]string{}
	ents, err := os.ReadDir("/dev/disk/by-label")
	if err != nil {
		return out
	}
	for _, e := range ents {
		target, err := filepath.EvalSymlinks(filepath.Join("/dev/disk/by-label", e.Name()))
		if err != nil {
			continue
		}
		out[filepath.Base(target)] = decodeLabelName(e.Name())
	}
	return out
}

// decodeLabelName 还原 udev 在文件名里做的 \xNN 转义（标签可以含空格）。
func decodeLabelName(s string) string {
	if !strings.Contains(s, `\x`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 4
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
