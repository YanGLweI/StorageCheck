package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"storagecheck/internal/collector"
	"storagecheck/internal/config"
	"storagecheck/internal/model"
	"storagecheck/internal/monitor"
)

type nopSender struct{}

func (nopSender) SendAlert(string, string, string, uint64, uint64, float64, float64, float64, bool) error {
	return nil
}
func (nopSender) SendRecovery(string, string, string, uint64, uint64, float64, float64, float64) error {
	return nil
}

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := config.NewStore(dir, config.NewAESBox(dir))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Drives = []config.DriveConfig{{ID: "C:", Enabled: true, ThresholdPct: 10}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	col := &collector.Fake{Drives: []model.DriveStat{collector.MakeStat("C:", "", 100*1024*1024*1024, 50*1024*1024*1024)}}
	mon, err := monitor.New(col, store, nopSender{}, "test")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(mon, store, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(t.Context()) })
	return srv, "http://127.0.0.1:" + strconv.Itoa(srv.Port())
}

// 开机自启的事实来源是系统启动项：GET 必须报活状态，PUT 不能在状态未变时误写。
func TestAutostartUsesLiveState(t *testing.T) {
	srv, base := newTestServer(t)

	live := true // 安装向导勾了自启，但 config.json 里还是 false
	var applied []bool
	srv.SetAutostartStateFunc(func() bool { return live })
	srv.SetAutostartFunc(func(enable bool) error {
		applied = append(applied, enable)
		live = enable
		return nil
	})

	get := func() bool {
		t.Helper()
		res, err := http.Get(base + "/api/config?token=" + srv.Token())
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out struct {
			Autostart bool `json:"autostart"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.Autostart
	}
	if !get() {
		t.Fatal("GET /api/config 应报系统启动项的真实状态")
	}

	// 界面按原样保存（autostart 仍为 true）：不应触发任何注册表写入
	res, err := http.NewRequest(http.MethodPut, base+"/api/config?token="+srv.Token(),
		strings.NewReader(`{"autostart":true,"check_interval_sec":300}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(res)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存失败: %s", resp.Status)
	}
	if len(applied) != 0 {
		t.Fatalf("状态未变时不应写启动项: %v", applied)
	}

	// 用户取消勾选：应真正删除启动项
	res2, err := http.NewRequest(http.MethodPut, base+"/api/config?token="+srv.Token(),
		strings.NewReader(`{"autostart":false}`))
	if err != nil {
		t.Fatal(err)
	}
	resp2, err := http.DefaultClient.Do(res2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if len(applied) != 1 || applied[0] != false {
		t.Fatalf("取消勾选应删除启动项: %v", applied)
	}
	if get() {
		t.Fatal("删除后 GET 应报未启用")
	}
}

// 双击快捷方式的第二实例靠这个接口请常驻进程开窗：必须鉴权、只认 POST，
// 并且真的把回调交给 openConsole —— 回调不执行就等于症状没修。
// 免认证勾选是界面上唯一能把授权码从磁盘上清掉的入口：
// 授权码输入框的「留空」只表示不修改，不勾这一挡就删不掉口令。
func TestNoAuthClearsStoredSecret(t *testing.T) {
	srv, base := newTestServer(t)

	type smtpView struct {
		HasPass bool `json:"has_pass"`
		NoAuth  bool `json:"no_auth"`
	}
	put := func(pass, noAuth string) {
		t.Helper()
		body := `{"smtp":{"preset":"custom","host":"relay.internal","port":25,"security":"none",` +
			`"user":"alert@example.com","recipients":["ops@example.com"],"pass":` + pass + `,"no_auth":` + noAuth + `}}`
		req, err := http.NewRequest(http.MethodPut, base+"/api/config?token="+srv.Token(), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("保存失败: %s", res.Status)
		}
	}
	current := func() smtpView {
		t.Helper()
		res, err := http.Get(base + "/api/config?token=" + srv.Token())
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out struct{ SMTP smtpView }
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.SMTP
	}

	put(`"secret-code"`, "false")
	if got := current(); !got.HasPass || got.NoAuth {
		t.Fatalf("填了授权码应存下密文且仍为需要认证: %+v", got)
	}

	// 输入框留空（不修改）+ 勾上免认证：密文必须消失
	put(`""`, "true")
	if got := current(); got.HasPass {
		t.Errorf("免认证保存后应清空已存授权码: %+v", got)
	} else if !got.NoAuth {
		t.Errorf("no_auth 应落盘: %+v", got)
	}

	// 反悔取消勾选时不该「复活」那份密文，只能重新填
	put(`""`, "false")
	if got := current(); got.HasPass || got.NoAuth {
		t.Errorf("取消免认证后仍应没有授权码: %+v", got)
	}
}

// CA 路径写错了要在保存时就拦住：存进去才发现读不到，故障会一路推到下一次告警邮件。
func TestSMTPCertOptions(t *testing.T) {
	srv, base := newTestServer(t)

	const body = `{"preset":"custom","host":"mail.internal","port":465,"security":"tls",` +
		`"user":"alert@example.com","recipients":["ops@example.com"]%s}`

	put := func(extra string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, base+"/api/config?token="+srv.Token(),
			strings.NewReader(`{"smtp":`+fmt.Sprintf(body, extra)+`}`))
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(out)
	}
	get := func() map[string]any {
		t.Helper()
		res, err := http.Get(base + "/api/config?token=" + srv.Token())
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out struct {
			SMTP map[string]any `json:"smtp"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.SMTP
	}

	if code, msg := put(`,"ca_file":"/nope/smtp_ca.pem"`); code != http.StatusBadRequest ||
		!strings.Contains(msg, "CA 文件") {
		t.Errorf("CA 文件不存在应保存失败并说明原因，实际 %d %s", code, msg)
	}
	if v, ok := get()["ca_file"]; ok && v == "/nope/smtp_ca.pem" {
		t.Error("被拒的配置不该落盘")
	}

	pem := filepath.Join(t.TempDir(), "smtp_ca.pem")
	if err := os.WriteFile(pem, []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, msg := put(`,"ca_file":"` + pem + `"`); code != http.StatusOK {
		t.Fatalf("存在的 CA 文件应能保存: %d %s", code, msg)
	}
	if get()["ca_file"] != pem {
		t.Errorf("ca_file 应落盘，实际 %v", get()["ca_file"])
	}

	if code, msg := put(`,"insecure_skip_verify":true`); code != http.StatusOK {
		t.Fatalf("不校验应能保存: %d %s", code, msg)
	}
	got := get()
	if got["insecure_skip_verify"] != true {
		t.Errorf("insecure_skip_verify 应落盘，实际 %v", got["insecure_skip_verify"])
	}
	if v, ok := got["ca_file"]; ok && v != "" {
		t.Errorf("界面切到不校验时不该再留着 CA 路径，实际 %v", v)
	}
}

func TestOpenConsoleEndpoint(t *testing.T) {
	srv, base := newTestServer(t)

	noToken, err := http.Post(base+"/api/console/open", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	noToken.Body.Close()
	if noToken.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401，实际 %s", noToken.Status)
	}

	called := make(chan struct{}, 1)
	srv.SetOpenConsoleFunc(func() { called <- struct{}{} })

	get, err := http.NewRequest(http.MethodGet, base+"/api/console/open?token="+srv.Token(), nil)
	if err != nil {
		t.Fatal(err)
	}
	getResp, err := http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET 应 405，实际 %s", getResp.Status)
	}
	select {
	case <-called:
		t.Fatal("GET 不该触发开窗回调")
	default:
	}

	post, err := http.NewRequest(http.MethodPost, base+"/api/console/open?token="+srv.Token(), nil)
	if err != nil {
		t.Fatal(err)
	}
	postResp, err := http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusOK {
		t.Fatalf("带 token 的 POST 应 200，实际 %s", postResp.Status)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("openConsole 回调没有被调用")
	}
}
