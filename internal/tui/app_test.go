//go:build linux

package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbletea"

	"storagecheck/internal/config"
	"storagecheck/internal/model"
	"storagecheck/internal/monitor"
	"storagecheck/internal/state"
)

func newTestApp(t *testing.T) *app {
	t.Helper()
	// 用短目录名：配置目录会出现在每页标题行里，长名字会掩盖真实的宽度问题。
	dir, err := os.MkdirTemp("/tmp", "sc")
	if err != nil {
		t.Fatalf("创建临时配置目录失败: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	seedFixture(t, dir)
	b, err := newBackend(dir, dir, "test")
	if err != nil {
		t.Fatalf("构造后端失败: %v", err)
	}
	a := newApp(b)
	a.Init()
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return a
}

// seedFixture 写入确定性的快照与事件流，让每一页都有内容可渲染，
// 不依赖测试机上真实存在的挂载点。
func seedFixture(t *testing.T, dir string) {
	t.Helper()
	v := monitor.StateView{
		UpdatedAt: time.Now(), Hostname: "test-host", Version: "test", SMTPReady: true,
		Drives: []monitor.DriveView{
			{DriveStat: model.DriveStat{ID: "/", Label: "系统卷 (xfs)", Total: 64 << 30, Free: 52 << 30, UsedPct: 18.7, FreePct: 81.3}, Enabled: true, ThresholdPct: 10},
			{DriveStat: model.DriveStat{ID: "/data", Label: "nas_backup_2024 (xfs)", Total: 209715200000, Free: 11509760000, UsedPct: 94.5, FreePct: 5.5}, Enabled: true, ThresholdPct: 15, Alerting: true},
			{DriveStat: model.DriveStat{ID: "/var/log", Label: "日志卷 (ext4)", Total: 10 << 30, Free: 2 << 30, UsedPct: 77.0, FreePct: 23.0}, Enabled: false, ThresholdPct: 20},
		},
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化快照失败: %v", err)
	}
	if err := os.WriteFile(state.StatePath(dir), data, 0o600); err != nil {
		t.Fatalf("写入快照失败: %v", err)
	}
	var events strings.Builder
	for i := range 20 {
		fmt.Fprintf(&events, `{"time":"2026-10-13T18:05:%02dZ","kind":"alert","drive":"/data","message":"数据卷 (xfs) 剩余 4.8%%，低于阈值 15%%","free_pct":4.8,"threshold":15}`+"\n", i)
	}
	if err := os.WriteFile(state.EventsPath(dir), []byte(events.String()), 0o600); err != nil {
		t.Fatalf("写入事件流失败: %v", err)
	}
}

// press 投一条消息并检查返回的 Cmd。Bubble Tea 在真实运行时是用 goroutine 执行
// Cmd 的（tea.Tick 会挂着等时间到），所以「命令没立刻返回」不是问题；
// 这里只关心它是否马上产出了 QuitMsg——那是界面意外退出的唯一信号。
func press(t *testing.T, a *app, msg tea.Msg) {
	t.Helper()
	_, cmd := a.Update(msg)
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case m := <-done:
		if _, isQuit := m.(tea.QuitMsg); isQuit {
			t.Fatalf("按键 %v 让程序退出了（当前页 %d）", msg, a.current)
		}
	case <-time.After(50 * time.Millisecond):
		// 定时器类命令，正常
	}
}

var keyNames = map[string]tea.KeyMsg{
	"enter": {Type: tea.KeyEnter}, "esc": {Type: tea.KeyEscape}, "up": {Type: tea.KeyUp},
	"down": {Type: tea.KeyDown}, "left": {Type: tea.KeyLeft}, "right": {Type: tea.KeyRight},
	"backspace": {Type: tea.KeyBackspace},
	"ctrl+u":    {Type: tea.KeyCtrlU},
	// 真实终端里空格是带 Runes 的 KeySpace，输入框从 Runes 取字符
	"space": {Type: tea.KeySpace, Runes: []rune{' '}},
}

func key(t *testing.T, spec string) tea.KeyMsg {
	t.Helper()
	if km, ok := keyNames[spec]; ok {
		return km
	}
	if spec == "ctrl+c" {
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	if spec == "ctrl+s" {
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	}
	if spec == "ctrl+t" {
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	}
	if len([]rune(spec)) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(spec)}
	}
	t.Fatalf("测试里不认得的按键 %q", spec)
	return tea.KeyMsg{}
}

func TestEscapeReturnsToMenuInsteadOfQuitting(t *testing.T) {
	a := newTestApp(t)
	press(t, a, key(t, "enter")) // 主菜单第一项 → 监控总览
	if a.current != pageOverview {
		t.Fatalf("回车应进入监控总览，当前页 %d", a.current)
	}
	press(t, a, key(t, "esc"))
	if a.current != pageMenu {
		t.Fatalf("esc 后应回到主菜单，当前页 %d", a.current)
	}
}

// TestKeyTourKeepsAppAlive 复演 pty 里跑过的整串按键：
// 任何一步让程序退出，都说明某个页面的按键处理漏了兜底。
func TestKeyTourKeepsAppAlive(t *testing.T) {
	a := newTestApp(t)
	tour := []string{
		"enter", "esc", "esc", // 监控总览 进出
		"down", "enter", "enter", "esc", "right", "3", "enter", "esc", "esc", "esc", // 磁盘配置：列表→编辑→阈值→返回
		"down", "down", "enter", "down", "down", "esc", "esc", // 告警设置
		"down", "down", "down", "enter", "down", "down", "down", "esc", "esc", // 邮件设置
		"down", "down", "down", "down", "enter", "esc", "esc", // 事件日志
		"down", "down", "down", "down", "down", "enter", "esc", "esc", // 服务状态
		"up", "up", "up", "up", "up", "up", "enter", // 回到主菜单第一项
	}
	for i, spec := range tour {
		press(t, a, key(t, spec))
		if a.current == pageMenu && i == len(tour)-1 {
			continue
		}
		if body := a.View(); body == "" {
			t.Fatalf("第 %d 步按键 %s 后界面渲染为空", i, spec)
		}
	}
}

func TestEveryPageRendersWithinRows(t *testing.T) {
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}, {64, 24}} {
		a := newTestApp(t)
		a.width, a.height = size.w, size.h
		for id := pageMenu; id <= pageService; id++ {
			a.current = id
			a.pages[id].enter()
			body := a.View()
			if body == "" {
				t.Fatalf("页面 %d 渲染为空", id)
			}
			if lines := countLines(body); lines > size.h {
				t.Errorf("页面 %d 在 %d×%d 终端渲染 %d 行，会滚动破版", id, size.w, size.h, lines)
			}
			for _, line := range splitLines(body) {
				if w := displayWidth(line); w > size.w {
					t.Errorf("页面 %d 在 %d×%d 终端有 %d 宽的行：%q", id, size.w, size.h, w, line)
				}
			}
			// 窄终端要砍掉装饰列，不能把最后的阈值/状态挤出屏幕
			if id == pageOverview || id == pageDrives {
				for _, head := range []string{"阈值", "状态"} {
					if !strings.Contains(body, head) {
						t.Errorf("页面 %d 在 %d×%d 终端丢了「%s」列", id, size.w, size.h, head)
					}
				}
			}
		}
	}
}

// 配置被手工改坏时界面必须还能打开：它是修好配置的唯一入口，
// 报错退出等于把管理员锁在门外。
func TestBrokenConfigStillOpensInterface(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "sc")
	if err != nil {
		t.Fatalf("创建临时配置目录失败: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{ 坏掉的 JSON"), 0o600); err != nil {
		t.Fatalf("写入坏配置失败: %v", err)
	}
	b, err := newBackend(dir, dir, "test")
	if err != nil {
		t.Fatalf("坏配置不应阻止界面启动: %v", err)
	}
	if b.configError == "" {
		t.Error("应记录配置读取失败，界面才知道要提示")
	}
	if b.cfg.CheckIntervalSec != config.Default().CheckIntervalSec {
		t.Errorf("应退回默认检查间隔，实际 %d", b.cfg.CheckIntervalSec)
	}
	a := newApp(b)
	a.Init()
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if view := ansiRe.ReplaceAllString(a.View(), ""); !strings.Contains(view, "默认值") {
		t.Errorf("界面应常驻显示降级说明，实际：%q", view)
	}
	if err := b.commit(); err != nil {
		t.Fatalf("保存修复后的配置失败: %v", err)
	}
	if b.configError != "" {
		t.Error("保存成功后应清除降级提示")
	}
}

func countLines(s string) int { return len(splitLines(s)) }

// fieldByLabel 按标签取字段，避免测试跟着字段顺序一起碎。
func fieldByLabel(p *smtpPage, label string) *formField {
	for i := range p.f.fields {
		if p.f.fields[i].label == label {
			return &p.f.fields[i]
		}
	}
	return nil
}

// typeInto 按一个字段名把文本逐字符敲进去（空格按真实终端的方式发 KeySpace）。
func typeInto(t *testing.T, a *app, p *smtpPage, label, text string) {
	t.Helper()
	fd := fieldByLabel(p, label)
	if fd == nil {
		t.Fatalf("邮件设置里没有字段 %q", label)
	}
	for p.f.cursor < len(p.f.fields) && &p.f.fields[p.f.cursor] != fd {
		press(t, a, key(t, "down"))
	}
	for _, c := range text {
		if c == ' ' {
			press(t, a, key(t, "space"))
			continue
		}
		press(t, a, key(t, string(c)))
	}
}

// 主机名里有 t/l/h、收件人里有空格：这些字符必须先落进输入框，
// 不能被「发送测试邮件」「切换选项」「返回」这类快捷键吃掉。
func TestTypingAddressesKeepsEveryCharacter(t *testing.T) {
	a := newTestApp(t)
	a.gotoPage(pageSMTP)
	p := a.pages[pageSMTP].(*smtpPage)

	typeInto(t, a, p, "SMTP 服务器", "smtp.qiye.aliyun.com")
	if got := fieldByLabel(p, "SMTP 服务器").input.Value(); got != "smtp.qiye.aliyun.com" {
		t.Errorf("SMTP 服务器被快捷键吃了字符，实际 %q", got)
	}
	typeInto(t, a, p, "登录账号", "alert@liyuanmail.com")
	if got := fieldByLabel(p, "登录账号").input.Value(); got != "alert@liyuanmail.com" {
		t.Errorf("登录账号被快捷键吃了字符，实际 %q", got)
	}
	typeInto(t, a, p, "收件人", "ops@example.com dba@example.com")
	if got := fieldByLabel(p, "收件人").input.Value(); got != "ops@example.com dba@example.com" {
		t.Errorf("收件人里的空格丢了，实际 %q", got)
	}

	// 文本行里按 t 只是打字，发信要用不冲突的 ctrl+t（此刻光标停在收件人行）
	before := fieldByLabel(p, "收件人").input.Value()
	a.Update(key(t, "t"))
	if got := fieldByLabel(p, "收件人").input.Value(); got != before+"t" {
		t.Errorf("文本行里的 t 应打进输入框，实际 %q", got)
	}
	_, cmd := a.Update(key(t, "ctrl+t"))
	if cmd == nil {
		t.Fatal("ctrl+t 应触发发送测试邮件")
	}
	// 没按过 ctrl+s：收件人得从输入框直接落到内存，试发不必先保存。
	if got := p.a.b.cfg.SMTP.Recipients; len(got) != 2 {
		t.Errorf("试发应把屏幕上的收件人落到内存，实际 %v", got)
	}
	if !p.f.dirtyState() {
		t.Error("试发不该抹掉未保存标记，ctrl+s 仍须落盘")
	}
	// 授权码可选：没填时不能再用「请先设置授权码」挡在本地，
	// 而应真去连服务器。指向本机一个没人监听的端口，握手立刻失败且不会发出邮件。
	p.a.b.cfg.SMTP.Host, p.a.b.cfg.SMTP.Port = "127.0.0.1", 1
	msg, ok := cmd().(errMsg)
	if !ok || !strings.Contains(string(msg), "测试邮件发送失败") {
		t.Errorf("未填授权码时应当直接尝试发信并报连接错误，实际 %#v", cmd())
	}
	// 选项行上单键 t 仍然可用，不必记两套绑定（按类型找行，别数字段个数）
	for i := 0; i < len(p.f.fields) && p.f.current().kind != kindChoice; i++ {
		a.Update(key(t, "up"))
	}
	if p.f.current().kind != kindChoice {
		t.Fatal("邮件设置页里没有选项行")
	}
	if _, cmd := a.Update(key(t, "t")); cmd == nil {
		t.Error("选项行上的 t 应仍然触发发送测试邮件")
	}
}

// 授权码输入框的「留空 = 不修改」让界面无法清空口令，
// 免认证这一挡就是唯一的显式清除入口，而且必须真的落到磁盘上。
func TestNoAuthSwitchClearsSavedSecret(t *testing.T) {
	a := newTestApp(t)
	a.gotoPage(pageSMTP)
	p := a.pages[pageSMTP].(*smtpPage)

	typeInto(t, a, p, "收件人", "ops@example.com")
	typeInto(t, a, p, "授权码", "abcd1234")
	press(t, a, key(t, "ctrl+s"))
	if a.err != "" {
		t.Fatalf("第一次 ctrl+s 报错: %s", a.err)
	}
	if p.a.b.cfg.SMTP.PassEnc == "" {
		t.Fatal("填了授权码并按 ctrl+s，应已加密写入配置")
	}
	if got := fieldByLabel(p, "授权码").input.Value(); got != "" {
		t.Errorf("保存后授权码输入框该清空，屏幕上还留着 %q", got)
	}

	fd := fieldByLabel(p, "SMTP 认证")
	if fd == nil {
		t.Fatal("邮件设置页里没有 SMTP 认证这一行")
	}
	for i := 0; i < len(p.f.fields) && p.f.current() != fd; i++ {
		press(t, a, key(t, "up"))
	}
	if p.f.current() != fd {
		t.Fatal("没能把光标移到 SMTP 认证行")
	}
	press(t, a, key(t, "right"))
	if !p.a.b.cfg.SMTP.NoAuth {
		t.Fatal("右移应切到免认证")
	}
	// 输入框已在保存时清空，磁盘上那份密文只能靠免认证这一挡删掉
	press(t, a, key(t, "ctrl+s"))
	if a.err != "" {
		t.Fatalf("切到免认证后 ctrl+s 报错: %s", a.err)
	}
	if p.a.b.cfg.SMTP.PassEnc != "" {
		t.Errorf("免认证保存后内存里的密文应清空，实际 %q", p.a.b.cfg.SMTP.PassEnc)
	}

	saved, err := p.a.b.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.SMTP.PassEnc != "" {
		t.Errorf("磁盘上的密文应随之删除，实际 %q", saved.SMTP.PassEnc)
	}
	if !saved.SMTP.NoAuth {
		t.Error("no_auth 应落盘")
	}
}

// 证书校验三态全靠「不校验标志 + CA 路径」两个字段推出来，
// 循环切换时不能同时出现、也不能切走了还留着上一种的路径。
func TestCertVerifyChoiceCyclesCleanly(t *testing.T) {
	a := newTestApp(t)
	a.gotoPage(pageSMTP)
	p := a.pages[pageSMTP].(*smtpPage)

	fd := fieldByLabel(p, "证书校验")
	if fd == nil {
		t.Fatal("邮件设置页里没有证书校验这一行")
	}
	for i := 0; i < len(p.f.fields) && p.f.current() != fd; i++ {
		press(t, a, key(t, "down"))
	}
	if p.f.current() != fd {
		t.Fatal("没能把光标移到证书校验行")
	}

	c := func() config.SMTPConfig { return p.a.b.cfg.SMTP }
	if got := c(); got.InsecureSkipVerify || got.CAFile != "" {
		t.Fatalf("默认应是系统信任库，实际 %+v", got)
	}

	press(t, a, key(t, "right")) // → 指定 CA 文件
	if got := c(); got.InsecureSkipVerify {
		t.Error("指定 CA 文件时不该同时不校验")
	} else if got.CAFile == "" {
		t.Error("选到指定 CA 文件时应先填上约定路径，省得手敲一整串")
	}
	if !strings.Contains(p.f.view(110), "CA 文件") {
		t.Error("CA 文件路径应显示在页面上")
	}

	// 路径写错要在 ctrl+s 就被拦下，别把故障留到告警邮件发不出去那天
	press(t, a, key(t, "down")) // → CA 文件
	press(t, a, key(t, "ctrl+u"))
	typeInto(t, a, p, "CA 文件", "/nope/smtp_ca.pem")
	press(t, a, key(t, "ctrl+s"))
	if !strings.Contains(a.err, "CA 文件") {
		t.Errorf("CA 路径不可读应保存失败并说明原因，实际 %q", a.err)
	}
	if got := c(); got.CAFile == "/nope/smtp_ca.pem" {
		t.Error("被拒的路径不该落盘")
	}

	press(t, a, key(t, "up")) // 回到证书校验行
	press(t, a, key(t, "right")) // → 不校验
	if got := c(); !got.InsecureSkipVerify {
		t.Fatal("右移应切到不校验")
	} else if got.CAFile != "" {
		t.Errorf("切到不校验后不该再留着 CA 路径，实际 %q", got.CAFile)
	}
	if !strings.Contains(p.f.view(110), "证书：不校验") {
		t.Error("页脚应显式标出不校验")
	}

	press(t, a, key(t, "right")) // → 回到系统信任库
	if got := c(); got.InsecureSkipVerify || got.CAFile != "" {
		t.Errorf("绕一圈应回到默认，实际 %+v", got)
	}
}

// 选项行切换只该刷它自己改写的那几行：用户刚敲完、还没 ctrl+s 的收件人
// 不该被一次「按了个左右键」顺手抹回磁盘上的旧值。
func TestTogglingChoicesKeepsUnsavedTyping(t *testing.T) {
	a := newTestApp(t)
	a.gotoPage(pageSMTP)
	p := a.pages[pageSMTP].(*smtpPage)

	typeInto(t, a, p, "收件人", "ops@example.com")
	before := fieldByLabel(p, "收件人").input.Value()

	for _, label := range []string{"加密方式", "服务商预设"} {
		fd := fieldByLabel(p, label)
		if fd == nil {
			t.Fatalf("邮件设置页里没有 %s 行", label)
		}
		for i := 0; i < len(p.f.fields) && p.f.current() != fd; i++ {
			press(t, a, key(t, "up"))
		}
		if p.f.current() != fd {
			t.Fatalf("没能把光标移到 %s 行", label)
		}
		press(t, a, key(t, "right"))
		if got := fieldByLabel(p, "收件人").input.Value(); got != before {
			t.Errorf("切「%s」把没保存的收件人刷掉了：应为 %q，实际 %q", label, before, got)
		}
	}
	if got := fieldByLabel(p, "端口").input.Value(); got == "" {
		t.Error("预设切换后端口的新值应立刻显示，不必退出重进")
	}
}

func splitLines(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func displayWidth(s string) int {
	// 与界面渲染同一套宽度规则：ANSI 不算，东亚全角算两格
	w := 0
	for _, r := range ansiRe.ReplaceAllString(s, "") {
		if isWide(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]")

func isWide(r rune) bool {
	for _, rng := range [][2]rune{
		{0x1100, 0x115F}, {0x2E80, 0x303E}, {0x3041, 0x33FF},
		{0x3400, 0x4DBF}, {0x4E00, 0x9FFF}, {0xA000, 0xA4CF},
		{0xAC00, 0xD7A3}, {0xF900, 0xFAFF}, {0xFE30, 0xFE6F},
		{0xFF00, 0xFF60}, {0xFFE0, 0xFFE6}, {0x20000, 0x2FFFD},
	} {
		if r >= rng[0] && r <= rng[1] {
			return true
		}
	}
	return false
}
