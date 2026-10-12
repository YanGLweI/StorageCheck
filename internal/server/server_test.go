package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

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
