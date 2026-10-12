package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Manager 控制系统开机自启（Windows 为注册表 Run 键，macOS 开发模式为启动项标记文件）。
type Manager interface {
	Enable(exePath string, args []string) error
	Disable() error
	IsEnabled() (bool, error)
}

// DevManager 仅供非 Windows 开发模式使用，把状态写在一个文件里。
type DevManager struct{ dir string }

func NewDevManager(dir string) *DevManager { return &DevManager{dir: dir} }

func (d *DevManager) path() string { return filepath.Join(d.dir, ".autostart") }

func (d *DevManager) Enable(exePath string, args []string) error {
	return os.WriteFile(d.path(), []byte(strings.Join(append([]string{exePath}, args...), " ")), 0o600)
}

func (d *DevManager) Disable() error {
	err := os.Remove(d.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (d *DevManager) IsEnabled() (bool, error) {
	_, err := os.Stat(d.path())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("autostart(dev): %w", err)
	}
	return true, nil
}
