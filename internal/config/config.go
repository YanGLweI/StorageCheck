package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type DriveConfig struct {
	ID           string  `json:"id"`
	Enabled      bool    `json:"enabled"`
	ThresholdPct float64 `json:"threshold_pct"` // 剩余低于该百分比时告警
}

type SMTPConfig struct {
	Preset     string   `json:"preset"`
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	Security   string   `json:"security"` // tls | starttls | none
	User       string   `json:"user"`
	PassEnc    string   `json:"pass_enc,omitempty"` // secret box 加密后的 hex
	FromAlias  string   `json:"from_alias"`
	Recipients []string `json:"recipients"`
}

type Config struct {
	Version               int           `json:"version"`
	CheckIntervalSec      int           `json:"check_interval_sec"`
	CooldownSec           int           `json:"cooldown_sec"`
	RecoveryHysteresisPct float64       `json:"recovery_hysteresis_pct"`
	NotifyOnRecovery      bool          `json:"notify_on_recovery"`
	Drives                []DriveConfig `json:"drives"`
	SMTP                  SMTPConfig    `json:"smtp"`
	Autostart             bool          `json:"autostart"`
	LaunchMinimized       bool          `json:"launch_minimized"`
}

const DefaultThresholdPct = 10.0

func Default() *Config {
	return &Config{
		Version:               1,
		CheckIntervalSec:      300,
		CooldownSec:           21600,
		RecoveryHysteresisPct: 2,
		NotifyOnRecovery:      true,
		Drives:                []DriveConfig{},
		SMTP: SMTPConfig{
			Preset:    "custom",
			Security:  "tls",
			FromAlias: "StorageCheck 磁盘监控",
		},
		LaunchMinimized: true,
	}
}

func (c *Config) Drive(id string) (*DriveConfig, bool) {
	for i := range c.Drives {
		if c.Drives[i].ID == id {
			return &c.Drives[i], true
		}
	}
	return nil, false
}

// SecretBox 抽象加解密，避免 config 包依赖平台实现。
type SecretBox interface {
	Seal(plain []byte) ([]byte, error)
	Open(sealed []byte) ([]byte, error)
}

// Store 管理配置文件的加载/保存，目录为平台用户配置目录下的 StorageCheck。
type Store struct {
	path string
	dir  string
	box  SecretBox
}

func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "StorageCheck"), nil
}

func NewStore(dir string, box SecretBox) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir, path: filepath.Join(dir, "config.json"), box: box}, nil
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) Load() (*Config, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.CheckIntervalSec < 30 {
		cfg.CheckIntervalSec = 30
	}
	if cfg.CooldownSec < 60 {
		cfg.CooldownSec = 60
	}
	return cfg, nil
}

// Save 原子写入（tmp + rename）。
func (s *Store) Save(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

var ErrShortSecret = errors.New("secret: 密文数据损坏")

// SealSecret 把明文授权码加密为落盘字符串。
func (s *Store) SealSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	sealed, err := s.box.Seal([]byte(plain))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sealed), nil
}

// OpenSecret 还原告盘中的授权码；空返回空串。
func (s *Store) OpenSecret(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	sealed, err := hex.DecodeString(enc)
	if err != nil {
		return "", err
	}
	plain, err := s.box.Open(sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// AESBox 是开发平台（非 Windows）的加密实现：密钥文件保存在配置目录，仅防止明文授权码落盘。
type AESBox struct{ dir string }

func NewAESBox(dir string) *AESBox { return &AESBox{dir: dir} }

func (b *AESBox) key() ([]byte, error) {
	k := filepath.Join(b.dir, ".devkey")
	data, err := os.ReadFile(k)
	if err == nil && len(data) == 32 {
		return data, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	if err := os.WriteFile(k, buf, 0o600); err != nil {
		return nil, err
	}
	return buf, nil
}

func (b *AESBox) Seal(plain []byte) ([]byte, error) {
	key, err := b.key()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func (b *AESBox) Open(sealed []byte) ([]byte, error) {
	key, err := b.key()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, ErrShortSecret
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
}
