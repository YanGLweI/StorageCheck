//go:build !linux

package main

import (
	"os"
	"regexp"
	"testing"
	"time"
)

// 页面标题与 Go 侧常量必须一致，否则窗口永远匹配不到。
func TestConsoleTitleMatchesPage(t *testing.T) {
	data, err := os.ReadFile("../../internal/server/static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`<title>([^<]+)</title>`).FindSubmatch(data)
	if m == nil {
		t.Fatal("index.html 没有 <title>")
	}
	if got, want := string(m[1]), consoleTitleBase; got != want {
		t.Errorf("页面标题 %q 与窗口匹配用的 %q 不一致", got, want)
	}
}

func TestIsConsoleTitle(t *testing.T) {
	title := consoleTitle(53124)
	if !isConsoleTitle(title, 53124) {
		t.Errorf("应匹配本次会话: %q", title)
	}
	// 上一次被终止的会话遗留的同名窗口不能算命中
	if isConsoleTitle(consoleTitle(41000), 53124) {
		t.Error("不同端口的遗留窗口不应匹配")
	}
	if isConsoleTitle(consoleTitleBase, 53124) {
		t.Error("尚未改写标题的首屏窗口不应匹配")
	}
	if isConsoleTitle("StorageCheck · 磁盘空间监控 · 53124 · 多余", 53124) {
		t.Error("带后缀的标题不应匹配")
	}
}

func TestEdgeAppArgs(t *testing.T) {
	args := edgeAppArgs("http://127.0.0.1:1/?token=ab", "/tmp/profile")
	want := []string{
		"--app=http://127.0.0.1:1/?token=ab",
		"--user-data-dir=/tmp/profile",
		"--no-first-run",
		"--no-default-browser-check",
	}
	if len(args) != len(want) {
		t.Fatalf("参数数量 %d != %d", len(args), len(want))
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, 期望 %q", i, args[i], want[i])
		}
	}
}

func TestOpenGuard(t *testing.T) {
	g := newOpenGuard()
	now := time.Now()
	if !g.allowSpawn(now) {
		t.Fatal("首次请求应放行")
	}
	if g.allowSpawn(now.Add(spawnDedupe / 2)) {
		t.Error("双击产生的第二次请求应被合并")
	}
	if g.allowSpawn(now.Add(time.Millisecond)) {
		t.Error("窗口仍在打开过程中，不应再开一个")
	}
	if !g.allowSpawn(now.Add(spawnDedupe + time.Second)) {
		t.Error("超过合并窗口后应放行（用户关掉了窗口再点击）")
	}
}
