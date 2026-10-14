package collector

import (
	"bufio"
	"io"
	"sort"
	"strconv"
	"strings"
)

// MountEntry 是 /proc/self/mounts 里的一行：设备、挂载点、文件系统类型。
type MountEntry struct {
	Device     string
	Mountpoint string
	FSType     string
}

// ParseMounts 解析 mounts 格式。proc 会用八进制转义挂载点里的空格等字符
// （例如空格写成 \040），不还原就会漏掉真实挂载点。
func ParseMounts(r io.Reader) []MountEntry {
	var out []MountEntry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		out = append(out, MountEntry{
			Device:     unescapeProc(fields[0]),
			Mountpoint: unescapeProc(fields[1]),
			FSType:     unescapeProc(fields[2]),
		})
	}
	return out
}

func unescapeProc(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// 伪文件系统：容量与真实存储无关，永远不采。
var virtualFSTypes = map[string]struct{}{
	"proc": {}, "sysfs": {}, "devtmpfs": {}, "tmpfs": {}, "devpts": {},
	"mqueue": {}, "hugetlbfs": {}, "debugfs": {}, "tracefs": {}, "configfs": {},
	"securityfs": {}, "pstore": {}, "bpf": {}, "binfmt_misc": {}, "autofs": {},
	"cgroup": {}, "cgroup2": {}, "cpuset": {}, "efivarfs": {}, "nsfs": {},
	"rpc_pipefs": {}, "selinuxfs": {}, "fusectl": {}, "ramfs": {},
	"squashfs": {}, "overlay": {}, "fuse": {}, "9p": {}, "virtiofs": {},
	"vmhgfs-fuse": {}, "vboxsf": {}, "binfmt": {}, "bpf-maps": {}, "devfs": {},
}

// 网络文件系统：默认不采。挂载断连时 statfs 可能长时间阻塞，
// 只让管理员在界面里显式开启（见 config.LinuxOptions.IncludeNetwork）。
var networkFSTypes = map[string]struct{}{
	"nfs": {}, "nfs4": {}, "cifs": {}, "smb": {}, "smb3": {},
	"glusterfs": {}, "cephfs": {}, "lustre": {}, "afs": {}, "nwfs": {},
	"sshfs": {},
}

// IsVirtualFS 判断文件系统是否与磁盘容量无关。fuse.* 除 fuseblk（ntfs-3g 之类
// 真实块设备上的 FUSE）外都算伪文件系统。
func IsVirtualFS(fstype string) bool {
	if _, ok := virtualFSTypes[fstype]; ok {
		return true
	}
	if fstype == "fuseblk" {
		return false
	}
	return strings.HasPrefix(fstype, "fuse.") || fstype == "fuse" ||
		strings.HasPrefix(fstype, "cgroup") || strings.HasPrefix(fstype, "sunrpc")
}

// IsNetworkFS 判断是否为网络挂载。
func IsNetworkFS(fstype string) bool {
	if _, ok := networkFSTypes[fstype]; ok {
		return true
	}
	return strings.HasPrefix(fstype, "smb") || strings.HasPrefix(fstype, "nfs")
}

// 真实块设备前缀：LVM(dm-)、软 RAID(md)、常见磁盘与云盘。
var physicalDevPrefixes = []string{"sd", "hd", "vd", "xvd", "nvme", "mmcblk", "dm-", "md", "ubi", "hpfs"}

// 明确排除的块设备：回环、内存盘、光驱、nbd/rbd 等远端块设备。
var nonPhysicalDevPrefixes = []string{"loop", "ram", "zram", "sr", "fd", "nbd", "rbd", "mtdblock"}

// IsPhysicalBlock 判断 /sys/dev/block 解析出的设备名是否代表本机真实磁盘。
// 这一步是 snap(loop+ squashfs)、Docker(overlay) 即便 mounts 字段说了谎也被剔除的关键。
func IsPhysicalBlock(name string) bool {
	for _, bad := range nonPhysicalDevPrefixes {
		if strings.HasPrefix(name, bad) {
			return false
		}
	}
	for _, good := range physicalDevPrefixes {
		if strings.HasPrefix(name, good) {
			return true
		}
	}
	return false
}

// 内置挂载点前缀黑名单：这些位置不是用户数据盘。
var defaultExcludePrefixes = []string{
	"/proc", "/sys", "/dev", "/run", "/var/lib/docker", "/var/lib/kubelet", "/snap",
}

// MountRule 是挂载点筛选规则。
type MountRule struct {
	IncludeNetwork  bool
	ExcludePrefixes []string
	MinTotalBytes   uint64
}

func (r MountRule) excludePrefixes() []string {
	return append(defaultExcludePrefixes, r.ExcludePrefixes...)
}

// MountDecision 是一次筛选判定，Reason 供 drives 调试视图说明排除原因。
type MountDecision struct {
	MountEntry
	Network bool
	Keep    bool
	Reason  string
}

// DecideMounts 按文件系统类型与挂载点前缀筛选（不涉及任何系统调用，可单测）。
func DecideMounts(entries []MountEntry, rule MountRule) []MountDecision {
	out := make([]MountDecision, 0, len(entries))
	for _, e := range entries {
		d := MountDecision{MountEntry: e}
		switch {
		case e.Mountpoint == "" || !strings.HasPrefix(e.Mountpoint, "/"):
			d.Reason = "挂载点非法"
		case IsVirtualFS(e.FSType):
			d.Reason = "伪文件系统 " + e.FSType
		case IsNetworkFS(e.FSType):
			d.Network = true
			if !rule.IncludeNetwork {
				d.Reason = "网络文件系统（可在界面中开启采集）"
			}
		case excludedPrefix(e.Mountpoint, rule.excludePrefixes()):
			d.Reason = "位于排除前缀之下"
		}
		d.Keep = d.Reason == ""
		out = append(out, d)
	}
	return out
}

func excludedPrefix(mountpoint string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "" {
			continue
		}
		if mountpoint == p || strings.HasPrefix(mountpoint, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}

// DedupeByDevice 让同一底层文件系统只保留一个条目：bind 挂载与 btrfs 子卷会在
// mounts 里重复出现，取最短挂载点（通常是 /），避免同一空间告警两次。
// devOf 返回挂载点对应的设备号；出错的文件直接丢弃。
func DedupeByDevice(entries []MountEntry, devOf func(path string) (uint64, error)) []MountEntry {
	sorted := append([]MountEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if len(sorted[i].Mountpoint) != len(sorted[j].Mountpoint) {
			return len(sorted[i].Mountpoint) < len(sorted[j].Mountpoint)
		}
		return sorted[i].Mountpoint < sorted[j].Mountpoint
	})
	seen := map[uint64]bool{}
	var out []MountEntry
	for _, e := range sorted {
		dev, err := devOf(e.Mountpoint)
		if err != nil {
			continue
		}
		if seen[dev] {
			continue
		}
		seen[dev] = true
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mountpoint < out[j].Mountpoint })
	return out
}

// DeviceShortName 取块设备展示名：/dev/mapper/cl-root → cl-root，/dev/sda3 → sda3。
func DeviceShortName(device string) string {
	d := strings.TrimSpace(device)
	if d == "" {
		return ""
	}
	return filepathBase(d)
}

func filepathBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// MountLabel 生成展示名：优先用文件系统自身的卷标签（label），
// 否则回退为「设备名 (文件系统)」，保证邮件与界面里永远有可读名称。
func MountLabel(fstype, device, volumeLabel string) string {
	if l := strings.TrimSpace(volumeLabel); l != "" {
		return l
	}
	short := DeviceShortName(device)
	switch {
	case short != "" && fstype != "":
		return short + " (" + fstype + ")"
	case short != "":
		return short
	default:
		return fstype
	}
}
