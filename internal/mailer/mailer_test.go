package mailer

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"storagecheck/internal/config"
)

// 免认证这一挡要压住的不只是配置里的密文：发信口令还可能来自 systemd 凭据或环境变量，
// 所以忽略口令必须发生在 New 里，而不是某一条加密方式分支里。
func TestNoAuthDropsEveryPassword(t *testing.T) {
	cfg := config.SMTPConfig{
		Host: "relay.internal", Port: 25, Security: "none",
		User: "alert@example.com", Recipients: []string{"ops@example.com"},
	}
	cfg.NoAuth = true
	if m := New(cfg, "from-credential-file"); m.pass != "" {
		t.Errorf("免认证时应忽略传入口令，实际保留 %q", m.pass)
	}
	cfg.NoAuth = false
	if m := New(cfg, "from-credential-file"); m.pass != "from-credential-file" {
		t.Errorf("需要认证时不该丢口令，实际 %q", m.pass)
	}
}

// 没有授权码又没勾免认证时，仍然按用户填的原样去连服务器，
// 让服务器给出它自己的回执，本地不替它猜。
func TestEmptyPasswordStillDials(t *testing.T) {
	cfg := config.SMTPConfig{
		Host: "127.0.0.1", Port: 1, Security: "none",
		User: "alert@example.com", Recipients: []string{"ops@example.com"},
	}
	err := New(cfg, "").Send("主题", "<p>正文</p>")
	if err == nil {
		t.Fatal("连不上的服务器应报错")
	}
	if err == ErrNotConfigured {
		t.Errorf("配置完整，不该被本地拦下: %v", err)
	}
}

// selfSignedPEM 造一份能过 AppendCertsFromPEM 的证书，当内网 CA 用。
func selfSignedPEM(t *testing.T) string {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(7),
		Subject:               pkix.Name{CommonName: "internal-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// 三种证书校验状态必须各自落到对的信任锚点上：
// 内网自建 CA 的机器装不进系统信任库时，指定 CA 是唯一不放弃校验的出路。
func TestTLSConfigTrustAnchors(t *testing.T) {
	base := config.SMTPConfig{Host: "mail.internal", Port: 465, Security: "tls", User: "a@b.c", Recipients: []string{"d@e.f"}}

	sys, err := New(base, "").tlsConfig()
	if err != nil {
		t.Fatal(err)
	}
	if sys.RootCAs != nil || sys.InsecureSkipVerify {
		t.Errorf("默认应交给系统信任库，实际 RootCAs=%v insecure=%v", sys.RootCAs != nil, sys.InsecureSkipVerify)
	}
	if sys.ServerName != "mail.internal" {
		t.Errorf("应按服务器名校验证书，实际 %q", sys.ServerName)
	}

	ca := selfSignedPEM(t)
	withCA, err := New(withCAFile(base, ca), "").tlsConfig()
	if err != nil {
		t.Fatal(err)
	}
	if withCA.RootCAs == nil {
		t.Error("指定 CA 文件时应只用这份证书建信任池")
	}

	insecure := base
	insecure.InsecureSkipVerify = true
	got, err := New(insecure, "").tlsConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !got.InsecureSkipVerify {
		t.Error("不校验这一挡必须真的传给 tls.Config")
	}
	if got.RootCAs != nil {
		t.Error("已经不校验了就不必再建信任池")
	}
}

func TestTLSConfigReportsBadCAFile(t *testing.T) {
	base := config.SMTPConfig{Host: "mail.internal", Port: 465, Security: "tls", User: "a@b.c", Recipients: []string{"d@e.f"}}

	missing := filepath.Join(t.TempDir(), "nope.pem")
	if _, err := New(withCAFile(base, missing), "").tlsConfig(); err == nil ||
		!strings.Contains(err.Error(), "读取 CA 文件失败") {
		t.Errorf("CA 文件不存在应报读取失败，实际 %v", err)
	}

	junk := filepath.Join(t.TempDir(), "junk.pem")
	if err := os.WriteFile(junk, []byte("不是证书\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(withCAFile(base, junk), "").tlsConfig(); err == nil ||
		!strings.Contains(err.Error(), "没有可解析的证书") {
		t.Errorf("CA 文件内容不对应报没有可解析的证书，实际 %v", err)
	}

	// 拨号之前就报错，避免把「CA 配错了」演变成一次超时
	err := New(withCAFile(config.SMTPConfig{
		Host: "127.0.0.1", Port: 1, Security: "tls",
		User: "a@b.c", Recipients: []string{"d@e.f"},
	}, missing), "").Send("主题", "<p>正文</p>")
	if err == nil || !strings.Contains(err.Error(), "读取 CA 文件失败") {
		t.Errorf("发信应在连接前就报 CA 文件问题，实际 %v", err)
	}
}

func withCAFile(c config.SMTPConfig, path string) config.SMTPConfig {
	c.CAFile = path
	return c
}
