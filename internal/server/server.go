package server

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"storagecheck/internal/appicon"
	"storagecheck/internal/config"
	"storagecheck/internal/mailer"
	"storagecheck/internal/model"
	"storagecheck/internal/monitor"
)

//go:embed all:static
var staticFS embed.FS

type Server struct {
	mon      *monitor.Monitor
	store    *config.Store
	token    string
	version  string
	listener net.Listener
	srv      *http.Server
	baseURL  string

	onAutostart    func(enable bool) error
	autostartState func() bool
	openConsole    func()
}

// SetAutostartFunc 注册开机自启应用回调（保存配置时调用）。
func (s *Server) SetAutostartFunc(fn func(enable bool) error) { s.onAutostart = fn }

// SetAutostartStateFunc 注册开机自启状态查询回调（读取系统启动项，而非配置缓存）。
func (s *Server) SetAutostartStateFunc(fn func() bool) { s.autostartState = fn }

// SetOpenConsoleFunc 注册「显示控制台」回调：第二个实例（双击快捷方式）不再自己开窗，
// 而是请求常驻进程开 —— 临时进程既没有前台权限，也会在返回后立刻退出，
// 由它拉起的浏览器窗口等于没了主人。
func (s *Server) SetOpenConsoleFunc(fn func()) { s.openConsole = fn }

// Opened 返回一次性访问地址（带 token），浏览器访问后转为 Cookie。
func New(mon *monitor.Monitor, store *config.Store, version string) (*Server, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	tk := make([]byte, 16)
	if _, err := rand.Read(tk); err != nil {
		return nil, err
	}
	s := &Server{mon: mon, store: store, token: hex.EncodeToString(tk), version: version}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/favicon.ico", handleFavicon)
	mux.HandleFunc("/api/state", s.auth(s.handleState))
	mux.HandleFunc("/api/config", s.auth(s.handleConfig))
	mux.HandleFunc("/api/events", s.auth(s.handleEvents))
	mux.HandleFunc("/api/check", s.auth(s.handleCheck))
	mux.HandleFunc("/api/pause", s.auth(s.handlePause))
	mux.HandleFunc("/api/smtp/test", s.auth(s.handleSMTPTest))
	mux.HandleFunc("/api/console/open", s.auth(s.handleOpenConsole))
	mux.HandleFunc("/api/stream", s.handleStream) // SSE 用 query token 鉴权

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.listener = ln
	s.srv = &http.Server{Handler: s.cookieGate(mux)}
	port := ln.Addr().(*net.TCPAddr).Port
	s.baseURL = fmt.Sprintf("http://127.0.0.1:%d/?token=%s", port, s.token)
	return s, nil
}

func (s *Server) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *Server) Token() string { return s.token }

func (s *Server) URL() string { return s.baseURL }

func (s *Server) Start() error {
	go func() {
		if err := s.srv.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			log.Println("web server 异常退出:", err)
		}
	}()
	return nil
}

func (s *Server) Shutdown(ctx context.Context) { s.srv.Shutdown(ctx) }

// cookieGate: 根路径带 token 时种下 HttpOnly Cookie 并去掉 URL 中的 token。
func (s *Server) cookieGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && r.URL.Query().Get("token") == s.token {
			http.SetCookie(w, &http.Cookie{Name: "sc_token", Value: s.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			u := *r.URL
			u.RawQuery = ""
			http.Redirect(w, r, u.String(), http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.URL.Query().Get("token")
		if tok == "" {
			if c, err := r.Cookie("sc_token"); err == nil {
				tok = c.Value
			}
		}
		if !constantTimeEq(tok, s.token) {
			http.Error(w, `{"error":"未授权"}`, http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func constantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	snap := s.mon.Snapshot()
	snap.SMTPReady = s.mon.Config().SMTP.Host != "" && s.mon.Config().SMTP.User != "" && len(s.mon.Config().SMTP.Recipients) > 0
	writeJSON(w, snap)
}

// handleOpenConsole 唤起/新建控制台窗口。窗口搜索会枚举所有顶层窗口、
// 新建窗口还要拉起浏览器，都不该让一个 HTTP 请求陪着等，所以丢给新 goroutine。
func (s *Server) handleOpenConsole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"需要 POST"}`, http.StatusMethodNotAllowed)
		return
	}
	if s.openConsole == nil {
		http.Error(w, `{"error":"本平台没有控制台"}`, http.StatusNotFound)
		return
	}
	go s.openConsole()
	writeJSON(w, map[string]string{"status": "已请求显示控制台"})
}

// PUT /api/config 载荷
type putConfig struct {
	CheckIntervalSec      *int                  `json:"check_interval_sec"`
	CooldownSec           *int                  `json:"cooldown_sec"`
	RecoveryHysteresisPct *float64              `json:"recovery_hysteresis_pct"`
	NotifyOnRecovery      *bool                 `json:"notify_on_recovery"`
	Drives                *[]config.DriveConfig `json:"drives"`
	SMTP                  *putSMTP              `json:"smtp"`
	Autostart             *bool                 `json:"autostart"`
	LaunchMinimized       *bool                 `json:"launch_minimized"`
}

type putSMTP struct {
	Preset     string   `json:"preset"`
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	Security   string   `json:"security"`
	User       string   `json:"user"`
	Pass       string   `json:"pass"`    // 明文；空串表示不修改
	NoAuth     bool     `json:"no_auth"` // 免认证：忽略并删除已存授权码
	FromAlias  string   `json:"from_alias"`
	Recipients []string `json:"recipients"`
	// CAFile 与 InsecureSkipVerify 是证书校验的三态：空 + false 用系统信任库，
	// 有 CAFile 只认那份，InsecureSkipVerify 为真则完全不校验。
	CAFile             string `json:"ca_file"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		view := *s.mon.Config()
		// 开机自启展示系统启动项的真实状态，避免界面与注册表不一致
		if s.autostartState != nil {
			view.Autostart = s.autostartState()
		}
		type smtpView struct {
			config.SMTPConfig
			HasPass bool `json:"has_pass"`
		}
		out := struct {
			*config.Config
			SMTP smtpView `json:"smtp"`
		}{Config: &view}
		sv := smtpView{SMTPConfig: view.SMTP}
		sv.SMTPConfig.PassEnc = ""
		sv.HasPass = view.SMTP.PassEnc != ""
		out.SMTP = sv
		writeJSON(w, out)
		return
	}
	if r.Method != http.MethodPut {
		http.Error(w, `{"error":"method"}`, http.StatusMethodNotAllowed)
		return
	}
	var p putConfig
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, errJSON(err), http.StatusBadRequest)
		return
	}
	err := s.mon.UpdateConfig(func(cfg *config.Config) error {
		if p.CheckIntervalSec != nil {
			if *p.CheckIntervalSec < 30 {
				return errStr("检查间隔不得低于 30 秒")
			}
			cfg.CheckIntervalSec = *p.CheckIntervalSec
		}
		if p.CooldownSec != nil {
			if *p.CooldownSec < 60 {
				return errStr("重复告警间隔不得低于 60 秒")
			}
			cfg.CooldownSec = *p.CooldownSec
		}
		if p.RecoveryHysteresisPct != nil {
			cfg.RecoveryHysteresisPct = *p.RecoveryHysteresisPct
		}
		if p.NotifyOnRecovery != nil {
			cfg.NotifyOnRecovery = *p.NotifyOnRecovery
		}
		if p.Drives != nil {
			cfg.Drives = *p.Drives
		}
		if p.SMTP != nil {
			c := cfg.SMTP
			c.Preset, c.Host, c.Port, c.Security = p.SMTP.Preset, p.SMTP.Host, p.SMTP.Port, p.SMTP.Security
			c.User, c.FromAlias, c.Recipients = p.SMTP.User, p.SMTP.FromAlias, p.SMTP.Recipients
			c.NoAuth = p.SMTP.NoAuth
			c.CAFile = strings.TrimSpace(p.SMTP.CAFile)
			c.InsecureSkipVerify = p.SMTP.InsecureSkipVerify
			if p.SMTP.Pass != "" {
				enc, err := s.store.SealSecret(p.SMTP.Pass)
				if err != nil {
					return err
				}
				c.PassEnc = enc
			}
			// 勾了免认证就不要再留一份用不上的密文，
			// 否则取消勾选时会「复活」一个用户以为早就删掉的口令。
			if c.NoAuth {
				c.PassEnc = ""
			}
			switch c.Security {
			case "tls", "starttls", "none":
			default:
				return errStr("非法的加密方式")
			}
			if c.Port <= 0 || c.Port > 65535 {
				return errStr("非法的端口")
			}
			// 存一个读不到的 CA 路径，等于把故障推到下一次发信才暴露
			if c.CAFile != "" && !c.InsecureSkipVerify {
				if _, err := os.Stat(c.CAFile); err != nil {
					return errStr("CA 文件读不到: " + err.Error())
				}
			}
			for _, rc := range c.Recipients {
				if !strings.Contains(rc, "@") {
					return errStr("收件人地址非法: " + rc)
				}
			}
			cfg.SMTP = c
		}
		if p.Autostart != nil {
			cfg.Autostart = *p.Autostart
		}
		if p.LaunchMinimized != nil {
			cfg.LaunchMinimized = *p.LaunchMinimized
		}
		return nil
	})
	if err != nil {
		http.Error(w, errJSON(err), http.StatusBadRequest)
		return
	}
	if p.Autostart != nil && s.onAutostart != nil && (s.autostartState == nil || *p.Autostart != s.autostartState()) {
		if aerr := s.onAutostart(*p.Autostart); aerr != nil {
			writeJSON(w, map[string]any{"ok": true, "warning": "设置已保存，但开机自启切换失败：" + aerr.Error()})
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.mon.Events(200))
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method"}`, http.StatusMethodNotAllowed)
		return
	}
	s.mon.Trigger()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method"}`, http.StatusMethodNotAllowed)
		return
	}
	var p struct {
		Paused bool `json:"paused"`
	}
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, errJSON(err), http.StatusBadRequest)
		return
	}
	s.mon.SetPaused(p.Paused)
	writeJSON(w, map[string]any{"ok": true, "paused": p.Paused})
}

func (s *Server) handleSMTPTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method"}`, http.StatusMethodNotAllowed)
		return
	}
	cfg := s.mon.Config()
	pass, err := s.store.OpenSecret(cfg.SMTP.PassEnc)
	if err != nil {
		http.Error(w, errJSON(err), http.StatusInternalServerError)
		return
	}
	m := mailer.New(cfg.SMTP, pass)
	if err := m.SendTest(s.mon.Hostname()); err != nil {
		http.Error(w, errJSON(err), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// SSE 事件流。EventSource 可携带同源 Cookie，也支持 query token。
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("token")
	if tok == "" {
		if c, err := r.Cookie("sc_token"); err == nil {
			tok = c.Value
		}
	}
	if !constantTimeEq(tok, s.token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	hub := s.mon.Subscribe()
	defer s.mon.Unsubscribe(hub)
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	enc, _ := json.Marshal(model.Event{Kind: model.KindInfo, Message: "connected"})
	fmt.Fprintf(w, "event: hello\ndata: %s\n\n", enc)
	fl.Flush()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-hub:
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, b)
			fl.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func errJSON(err error) string {
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	return string(b)
}

// handleFavicon 提供程序图标：Edge/Chrome 应用窗口模式用它作为窗口与任务栏图标。
func handleFavicon(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(faviconICO)
}

var faviconICO = appicon.MakeICO(false, 16, 24, 32, 48)

func errStr(msg string) error { return fmt.Errorf("%s", msg) }
