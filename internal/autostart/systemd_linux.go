//go:build linux

package autostart

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// systemdManager 用 systemd 单位文件承担 Windows 注册表 Run 键的角色：
// 「开机自启」的事实来源是 unit 的 enable 状态，而不是配置文件里的布尔值。
// 单位文件本身（含 ExecStart）由 RPM 安装，进程只读写启用状态。
type systemdManager struct {
	unit string
	// userMode 保留给非 root 桌面场景：服务形态固定用系统 unit。
	userMode bool
}

// NewSystemd 构造 systemd 自启管理器，dir 仅用于与其它平台签名一致。
func NewSystemd(dir string) Manager {
	return &systemdManager{unit: defaultUnit}
}

const defaultUnit = "storagecheck.service"

var errNoSystemd = errors.New("autostart: 当前环境没有 systemctl（容器或未使用 systemd 的系统）")

func (m *systemdManager) Enable(string, []string) error {
	out, err := m.run("enable", m.unit)
	if err != nil {
		return fmt.Errorf("autostart: systemctl enable 失败: %v %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (m *systemdManager) Disable() error {
	out, err := m.run("disable", m.unit)
	if err != nil {
		return fmt.Errorf("autostart: systemctl disable 失败: %v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// Command 在已启用时返回服务实际执行的文件路径，供 Reconcile 与当前可执行文件比对；
// 未启用/未安装时返回空串（这就是 Reconcile 判定的「没有启动项」）。
func (m *systemdManager) Command() (string, error) {
	out, err := m.run("is-enabled", m.unit)
	state := strings.TrimSpace(out)
	if err != nil && state != "enabled" && state != "static" {
		if _, lookErr := exec.LookPath("systemctl"); lookErr != nil {
			return "", errNoSystemd
		}
		return "", nil // disabled/masked/单位不存在
	}
	if state != "enabled" && state != "static" {
		return "", nil
	}
	// unit 里的 ExecStart 由包维护，运行中的二进制就是它指向的文件
	exe, err := os.Executable()
	if err != nil {
		return m.unit, nil
	}
	return exe, nil
}

func (m *systemdManager) run(args ...string) (string, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "", errNoSystemd
	}
	// systemd 在容器里常以「只读 /run」等方式失败，用 --no-legend 拿纯净输出
	cmd := exec.Command("systemctl", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ReloadDaemon 让 systemd 重新读取单位文件（安装/升级 unit 后调用）。
func ReloadDaemon() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errNoSystemd
	}
	out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl daemon-reload 失败: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// UnitStatus 返回 (是否运行, 是否开机启用, 最近错误)，终端界面的「服务状态」页用。
func UnitStatus() (active bool, enabled bool, err error) {
	m := &systemdManager{unit: defaultUnit}
	if _, lookErr := exec.LookPath("systemctl"); lookErr != nil {
		return false, false, errNoSystemd
	}
	out, _ := m.run("is-active", defaultUnit)
	active = strings.TrimSpace(out) == "active"
	out, _ = m.run("is-enabled", defaultUnit)
	enabled = strings.TrimSpace(out) == "enabled"
	return active, enabled, nil
}

// Reload 请求 systemd 向服务发 SIGHUP（ExecReload），失败时回退为直接发信号。
func ReloadService() error {
	m := &systemdManager{unit: defaultUnit}
	if out, err := m.run("reload", defaultUnit); err != nil {
		return fmt.Errorf("systemctl reload 失败: %v %s", err, strings.TrimSpace(out))
	}
	return nil
}
