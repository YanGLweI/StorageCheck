package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	Preset   string `json:"preset"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"` // tls | starttls | none
	User     string `json:"user"`
	// NoAuth 为真时完全不发送 SMTP 认证，已保存的授权码与 systemd 凭据一并忽略，
	// 对应内网免认证中继；保存这个状态会同时清空 pass_enc。
	NoAuth     bool     `json:"no_auth,omitempty"`
	PassEnc    string   `json:"pass_enc,omitempty"` // secret box 加密后的 hex
	FromAlias  string   `json:"from_alias"`
	Recipients []string `json:"recipients"`
	// CAFile 指向一份 PEM（内网自建 CA 常用），非空时 TLS 只认它、不再用系统信任库。
	CAFile string `json:"ca_file,omitempty"`
	// InsecureSkipVerify 为真时不校验服务器证书：等于放弃 TLS 的中间人防护，
	// 只给拿不到 CA 的可信内网自签名环境用。
	InsecureSkipVerify bool `json:"insecure_skip_verify,omitempty"`
}

// LinuxOptions 是 Linux 采集与运维相关的可选项，Windows/macOS 忽略。
type LinuxOptions struct {
	// IncludeNetwork 为真时把 NFS/CIFS 等网络挂载也纳入采集，默认排除。
	IncludeNetwork bool `json:"include_network,omitempty"`
	// MountExclude 是额外的挂载点前缀黑名单（在内置规则之上叠加）。
	MountExclude []string `json:"mount_exclude,omitempty"`
	// MinTotalBytes 是采集下限，0 表示默认 1 GiB（过滤 EFI 分区、内存盘等小卷）。
	MinTotalBytes uint64 `json:"min_total_bytes,omitempty"`
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
	// AutoEnableNew 为真（缺省即真，Windows 行为）时新发现的盘自动纳入监控；
	// Linux 安装包写成 false：新挂载点只记录事件，等管理员在界面里启用。
	AutoEnableNew *bool        `json:"auto_enable_new,omitempty"`
	Linux         LinuxOptions `json:"linux,omitempty"`
}

// AutoEnableNewDrives 报告新盘是否应当自动启用（未设置时沿用自动启用的历史行为）。
func (c *Config) AutoEnableNewDrives() bool { return c.AutoEnableNew == nil || *c.AutoEnableNew }

// MinTotal 返回挂载点容量下限，未配置时为 1 GiB。
func (c *Config) MinTotal() uint64 {
	if c.Linux.MinTotalBytes > 0 {
		return c.Linux.MinTotalBytes
	}
	return DefaultMinTotalBytes
}

const DefaultMinTotalBytes = uint64(1 << 30)

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
			Port:      465, // 与 Security 的 tls 配套：留空会让「端口：只能填整数」这种报错找不到头
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
	path    string
	dir     string
	dataDir string
	box     SecretBox
}

func NewStore(dir string, box SecretBox) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir, path: filepath.Join(dir, "config.json"), box: box}, nil
}

// LoadAt 只读取某目录下的配置，不创建目录也不需要密钥盒：
// status/events 这类只读命令用它，避免普通用户执行时因无法建目录而失败。
func LoadAt(dir string) (*Config, error) {
	path := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("配置文件 %s 无法解析: %w", path, err)
	}
	cfg.normalize()
	return cfg, nil
}

func (s *Store) Dir() string { return s.dir }

// DataDir 是运行数据（state.json、events.log、锁文件）的落点。
// RPM 布局下配置在 /etc/storagecheck、数据在 /var/lib/storagecheck，
// 没单独指定时与配置同目录（Windows/macOS 行为不变）。
func (s *Store) DataDir() string {
	if s.dataDir != "" {
		return s.dataDir
	}
	return s.dir
}

// UseDataDir 把运行数据挪到另一个目录（调用方需保证该目录可写）。
func (s *Store) UseDataDir(dir string) error {
	if dir == "" || dir == s.dir {
		s.dataDir = ""
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	s.dataDir = dir
	return nil
}

// Path 是配置文件绝对路径，供状态文件定位与热加载指纹计算使用。
func (s *Store) Path() string { return s.path }

// Fingerprint 返回配置内容哈希，用于判断文件是否被外部（终端界面）改动过。
func (s *Store) Fingerprint() (string, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

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
		return nil, fmt.Errorf("配置文件 %s 无法解析: %w", s.path, err)
	}
	cfg.normalize()
	return cfg, nil
}

// normalize 把非法/过小的取值收敛到下限：配置是用户可写的输入，
// 采集间隔和冷却下限保护的是「不被自己刷屏」。
func (c *Config) normalize() {
	if c.CheckIntervalSec < 30 {
		c.CheckIntervalSec = 30
	}
	if c.CooldownSec < 60 {
		c.CooldownSec = 60
	}
	if c.RecoveryHysteresisPct < 0 {
		c.RecoveryHysteresisPct = 0
	}
}

// Save 原子写入（tmp + rename）。Linux 上先取文件锁，
// 避免终端界面与守护进程（登记新盘）并发写互相覆盖。
func (s *Store) Save(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	unlock, err := lockPath(s.path)
	if err != nil {
		return err
	}
	defer unlock()
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

// AESBox 是非 Windows 平台的加密实现：密钥文件与配置同目录（0600），
// 作用是「授权码不以明文落盘」，不防住目录读取者。
type AESBox struct {
	dir     string
	keyName string
}

// NewAESBox 使用开发态密钥文件名，保持既有目录兼容。
func NewAESBox(dir string) *AESBox { return &AESBox{dir: dir, keyName: ".devkey"} }

// NewAESBoxWithKeyFile 供 Linux 生产安装使用（seal.key）。
func NewAESBoxWithKeyFile(dir, keyName string) *AESBox { return &AESBox{dir: dir, keyName: keyName} }

func (b *AESBox) key() ([]byte, error) {
	k := filepath.Join(b.dir, b.keyName)
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
