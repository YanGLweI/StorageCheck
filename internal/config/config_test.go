package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 配置坏掉时 journald 与 CLI 只有报错这一行可用，必须点名是哪个文件。
func TestLoadNamesTheBrokenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{ 手抖加了一个井号"), 0o600); err != nil {
		t.Fatalf("写入坏配置失败: %v", err)
	}
	store, err := NewStore(dir, nil)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	for name, load := range map[string]func() (*Config, error){
		"Store.Load": store.Load,
		"LoadAt":     func() (*Config, error) { return LoadAt(dir) },
	} {
		_, err := load()
		if err == nil {
			t.Fatalf("%s 应因坏配置报错", name)
		}
		if !strings.Contains(err.Error(), path) {
			t.Errorf("%s 的错误里没有文件路径: %v", name, err)
		}
		if !strings.Contains(err.Error(), "无法解析") {
			t.Errorf("%s 的错误没说清问题: %v", name, err)
		}
	}
}

// 首装时配置文件不存在，两条读取路径都要给出默认值而不是报错。
func TestMissingConfigFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "storagecheck"), nil)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("配置文件缺失应返回默认值: %v", err)
	}
	if cfg.CheckIntervalSec != Default().CheckIntervalSec {
		t.Errorf("Load 未给出默认检查间隔，实际 %d", cfg.CheckIntervalSec)
	}
	cfg, err = LoadAt(filepath.Join(dir, "empty"))
	if err != nil {
		t.Fatalf("LoadAt 在配置文件缺失时应返回默认值: %v", err)
	}
	if cfg.CheckIntervalSec != Default().CheckIntervalSec {
		t.Errorf("LoadAt 未给出默认检查间隔，实际 %d", cfg.CheckIntervalSec)
	}
}

// 非法取值收敛到下限：防止间隔为 0 时把采样循环和邮件网关一起打死。
func TestNormalizeClampsTooSmallValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"check_interval_sec": 1, "cooldown_sec": 0, "recovery_hysteresis_pct": -5}`), 0o600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	cfg, err := LoadAt(dir)
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	if cfg.CheckIntervalSec < 30 {
		t.Errorf("检查间隔应被抬到下限，实际 %d", cfg.CheckIntervalSec)
	}
	if cfg.CooldownSec < 60 {
		t.Errorf("告警冷却应被抬到下限，实际 %d", cfg.CooldownSec)
	}
	if cfg.RecoveryHysteresisPct != 0 {
		t.Errorf("恢复迟滞不应为负，实际 %v", cfg.RecoveryHysteresisPct)
	}
}
