//go:build linux

package collector

import (
	"strings"
	"testing"
)

// 这些断言只跑在真 Linux 上（macOS 交叉编译时由 vet 保证可编译），
// 目的是在 RHEL 上验证筛选规则没有跑偏。
func TestLinuxListInvariants(t *testing.T) {
	c := New().(*linuxCollector)
	stats, err := c.List()
	if err != nil {
		if strings.Contains(err.Error(), "未发现可监控") {
			t.Skip("容器等最小环境里没有本地磁盘")
		}
		t.Fatalf("List: %v", err)
	}
	if len(stats) == 0 {
		t.Fatal("List 返回空结果却没有报错")
	}
	seen := map[string]bool{}
	for _, s := range stats {
		if !strings.HasPrefix(s.ID, "/") {
			t.Errorf("Linux 盘 ID 应为挂载点，得到 %q", s.ID)
		}
		if seen[s.ID] {
			t.Errorf("挂载点重复: %s", s.ID)
		}
		seen[s.ID] = true
		if s.Label == "" {
			t.Errorf("%s 标签为空，邮件与界面会失去可读名称", s.ID)
		}
		if s.Total < s.Free {
			t.Errorf("%s 容量异常: total=%d free=%d", s.ID, s.Total, s.Free)
		}
		if s.FreePct < 0 || s.FreePct > 100 {
			t.Errorf("%s FreePct=%f 越界", s.ID, s.FreePct)
		}
		for _, bad := range []string{"/proc", "/sys", "/dev", "/run"} {
			if s.ID == bad || strings.HasPrefix(s.ID, bad+"/") {
				t.Errorf("%s 位于伪文件系统路径下，不应被采集", s.ID)
			}
		}
	}
}

func TestLinuxDiagnoseExplainsEveryMount(t *testing.T) {
	rows, err := Diagnose(MountRule{})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("Diagnose 没有任何挂载点")
	}
	for _, r := range rows {
		if !r.Keep && r.Reason == "" {
			t.Errorf("%s 被排除但没有原因，drives 视图无法解释", r.Mountpoint)
		}
	}
}

func TestLinuxBlockDeviceNameOnRoot(t *testing.T) {
	name, ok := blockDeviceName("/")
	if !ok {
		t.Skip("当前环境无法反查 / 的块设备（容器内常见）")
	}
	if !IsPhysicalBlock(name) && name != "" {
		t.Errorf("/ 的设备名 %q 不在物理设备白名单里，需要补充规则", name)
	}
}
