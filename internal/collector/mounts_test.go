package collector

import (
	"strings"
	"testing"
)

func TestParseMounts(t *testing.T) {
	in := `# 注释行应忽略

sysfs /sys sysfs rw,nosuid 0 0
/dev/mapper/cl-root / xfs rw,seclabel 0 0
tmpfs /run/user/1000 tmpfs rw 0 0
/dev/sda1 /mnt/My\040Data vfat rw 0 0
短行
`
	got := ParseMounts(strings.NewReader(in))
	if len(got) != 4 {
		t.Fatalf("解析行数 = %d, 期望 4: %+v", len(got), got)
	}
	if got[1].Device != "/dev/mapper/cl-root" || got[1].Mountpoint != "/" || got[1].FSType != "xfs" {
		t.Errorf("LVM 根分区解析错误: %+v", got[1])
	}
	if got[3].Mountpoint != "/mnt/My Data" {
		t.Errorf("八进制转义未还原: %q, 期望 %q", got[3].Mountpoint, "/mnt/My Data")
	}
}

func TestDecideMounts(t *testing.T) {
	entries := []MountEntry{
		{Device: "sysfs", Mountpoint: "/sys", FSType: "sysfs"},
		{Device: "tmpfs", Mountpoint: "/tmp", FSType: "tmpfs"},
		{Device: "/dev/sda3", Mountpoint: "/boot", FSType: "ext4"},
		{Device: "/dev/mapper/vg-lv", Mountpoint: "/", FSType: "xfs"},
		{Device: "snap-core", Mountpoint: "/snap/core/1", FSType: "squashfs"},
		{Device: "docker-overlay", Mountpoint: "/var/lib/docker/overlay2/x/merged", FSType: "overlay"},
		{Device: "server:/export", Mountpoint: "/mnt/nfs", FSType: "nfs4"},
		{Device: "//srv/share", Mountpoint: "/mnt/cifs", FSType: "cifs"},
		{Device: "/dev/sdb1", Mountpoint: "/mnt/usb", FSType: "fuseblk"},
		{Device: "rootfs", Mountpoint: "/dev", FSType: "devtmpfs"},
	}

	decided := DecideMounts(entries, MountRule{})
	kept := map[string]bool{}
	reasons := map[string]string{}
	for _, d := range decided {
		kept[d.Mountpoint] = d.Keep
		reasons[d.Mountpoint] = d.Reason
	}
	for _, p := range []string{"/boot", "/", "/mnt/usb"} {
		if !kept[p] {
			t.Errorf("%s 应当纳入监控，实际原因=%q", p, reasons[p])
		}
	}
	for _, p := range []string{"/sys", "/tmp", "/snap/core/1", "/var/lib/docker/overlay2/x/merged", "/mnt/nfs", "/mnt/cifs", "/dev"} {
		if kept[p] {
			t.Errorf("%s 应当被排除", p)
		}
		if reasons[p] == "" {
			t.Errorf("%s 缺少排除原因，drives 视图无法解释", p)
		}
	}

	withNet := DecideMounts(entries, MountRule{IncludeNetwork: true})
	for _, d := range withNet {
		if d.Mountpoint != "/mnt/nfs" && d.Mountpoint != "/mnt/cifs" {
			continue
		}
		if !d.Keep {
			t.Errorf("开启网络盘采集后 %s 仍被排除: %q", d.Mountpoint, d.Reason)
		}
		if !d.Network {
			t.Errorf("%s 应标记为网络挂载，以便跳过物理设备判定", d.Mountpoint)
		}
	}

	excluded := DecideMounts(entries, MountRule{ExcludePrefixes: []string{"/boot"}})
	for _, d := range excluded {
		if d.Mountpoint == "/boot" && d.Keep {
			t.Error("自定义排除前缀未生效")
		}
	}
}

func TestIsPhysicalBlock(t *testing.T) {
	for _, n := range []string{"sda1", "nvme0n1p3", "vda2", "xvda1", "dm-0", "md127", "mmcblk0p2"} {
		if !IsPhysicalBlock(n) {
			t.Errorf("%s 应判定为真实块设备", n)
		}
	}
	for _, n := range []string{"loop0", "ram1", "zram0", "sr0", "rbd0", "nbd3", "mtdblock1"} {
		if IsPhysicalBlock(n) {
			t.Errorf("%s 不应判定为真实块设备", n)
		}
	}
}

func TestDedupeByDevice(t *testing.T) {
	entries := []MountEntry{
		{Device: "/dev/sdb1", Mountpoint: "/data/mirror", FSType: "xfs"},
		{Device: "/dev/sda3", Mountpoint: "/", FSType: "xfs"},
		{Device: "/dev/sda3", Mountpoint: "/home", FSType: "xfs"},
	}
	devs := map[string]uint64{"/data/mirror": 802, "/": 803, "/home": 803}
	got := DedupeByDevice(entries, func(p string) (uint64, error) { return devs[p], nil })
	if len(got) != 2 {
		t.Fatalf("去重后数量 = %d, 期望 2: %+v", len(got), got)
	}
	if got[0].Mountpoint != "/" || got[1].Mountpoint != "/data/mirror" {
		t.Errorf("同一设备应保留最短挂载点，实际 %+v", got)
	}
}

func TestMountLabel(t *testing.T) {
	if got := MountLabel("xfs", "/dev/mapper/cl-root", "系统卷"); got != "系统卷" {
		t.Errorf("有卷标时应使用卷标，得到 %q", got)
	}
	if got := MountLabel("xfs", "/dev/sda3", ""); got != "sda3 (xfs)" {
		t.Errorf("无卷标时应回退为设备+类型，得到 %q", got)
	}
	if got := MountLabel("", "", ""); got != "" {
		t.Errorf("全空时应返回空串，得到 %q", got)
	}
}
