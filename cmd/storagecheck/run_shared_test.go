package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

// 第二实例必须打到常驻进程的 /api/console/open，并带上 run.json 里的 token：
// 少了 token 会被 401，路径不对则谁也不会开窗，两种都表现为「双击没反应」。
func TestRequestConsoleOpenCallsEndpoint(t *testing.T) {
	var method, path, token string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, token = r.Method, r.URL.Path, r.URL.Query().Get("token")
	}))
	defer srv.Close()

	if err := requestConsoleOpen(runInfo{Port: 1, Token: "tk", URL: srv.URL + "/?token=tk"}); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/api/console/open" || token != "tk" {
		t.Fatalf("请求不对: %s %s token=%q", method, path, token)
	}
}

// 常驻进程答不上来时必须报错，让调用方退回自己开窗，而不是静默什么都不做。
func TestRequestConsoleOpenReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	srv.Close()

	if err := requestConsoleOpen(runInfo{Port: port, Token: "tk", URL: srv.URL}); err == nil {
		t.Fatal("服务已关闭，应当返回错误")
	}
}
