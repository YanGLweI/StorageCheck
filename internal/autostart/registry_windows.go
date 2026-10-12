//go:build windows

package autostart

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	valueName  = "StorageCheck"
)

type registryManager struct{}

func New() Manager { return &registryManager{} }
func (m *registryManager) Enable(exePath string, args []string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表 Run 键: %w", err)
	}
	defer k.Close()
	cmd := fmt.Sprintf(`"%s"`, exePath)
	if len(args) > 0 {
		cmd += " " + strings.Join(args, " ")
	}
	return k.SetStringValue(valueName, cmd)
}

func (m *registryManager) Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表 Run 键: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(valueName); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}

// Command 读取 HKCU Run 里登记的启动命令；未配置时返回空串。
func (m *registryManager) Command() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}
