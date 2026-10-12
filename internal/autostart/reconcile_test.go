package autostart

import (
	"fmt"
	"testing"
)

type fakeManager struct {
	cmd     string
	enables int
}

func (f *fakeManager) Enable(exePath string, args []string) error {
	f.enables++
	f.cmd = fmt.Sprintf(`"%s" %v`, exePath, args)
	return nil
}

func (f *fakeManager) Disable() error { f.cmd = ""; return nil }

func (f *fakeManager) Command() (string, error) { return f.cmd, nil }

const exe = `C:\Users\me\AppData\Local\Programs\StorageCheck\StorageCheck.exe`

func TestReconcileAdoptsInstallerState(t *testing.T) {
	// 安装向导勾了开机自启（注册表已写），但 config.json 还是默认的 false
	m := &fakeManager{cmd: `"` + exe + `" --minimized`}
	enabled, changed, err := Reconcile(m, exe, []string{"--minimized"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled || !changed {
		t.Fatalf("应把注册表的启用状态回流进配置: enabled=%v changed=%v", enabled, changed)
	}
	if m.enables != 0 {
		t.Fatalf("状态一致时不应重复写注册表: %d 次", m.enables)
	}
}

func TestReconcileRefreshesStalePath(t *testing.T) {
	m := &fakeManager{cmd: `"D:\Old\Location\StorageCheck.exe" --minimized`}
	enabled, _, err := Reconcile(m, exe, []string{"--minimized"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if m.enables != 1 {
		t.Fatalf("安装位置变化后应重写启动项: %d 次", m.enables)
	}
	if !enabled {
		t.Fatal("重写后仍应为启用")
	}
}

func TestReconcileRecreatesMissingEntry(t *testing.T) {
	m := &fakeManager{} // 启动项被清理工具删掉，但配置认为该自启
	enabled, changed, err := Reconcile(m, exe, []string{"--minimized"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if m.enables != 1 || !enabled || changed {
		t.Fatalf("应补写启动项且配置无需变更: enables=%d enabled=%v changed=%v", m.enables, enabled, changed)
	}
}

func TestReconcileKeepsDisabledQuiet(t *testing.T) {
	m := &fakeManager{}
	enabled, changed, err := Reconcile(m, exe, []string{"--minimized"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if enabled || changed || m.enables != 0 {
		t.Fatalf("双方都未启用时应无副作用: %+v %v %v", enabled, changed, m.enables)
	}
}

func TestReconcilePathComparison(t *testing.T) {
	// 大小写与分隔符差异不算「位置变了」
	m := &fakeManager{cmd: `"c:/users/me/AppData/Local/Programs/StorageCheck/storagecheck.exe" --minimized`}
	if _, _, err := Reconcile(m, exe, []string{"--minimized"}, true); err != nil {
		t.Fatal(err)
	}
	if m.enables != 0 {
		t.Fatal("等价路径不应触发重写")
	}
}
