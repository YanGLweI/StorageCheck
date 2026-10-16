//go:build !linux

package main

import (
	"context"

	"storagecheck/internal/server"
)

// webConsole 是 Windows 与 macOS 开发态的控制台：内嵌 HTTP 服务 + go:embed 前端。
// Linux 无 GUI 版不编译此文件，因此 server 包（含前端资源）不会进入 Linux 二进制。
type webConsole struct {
	srv *server.Server
}

func newConsole(a consoleArgs) (console, error) {
	srv, err := server.New(a.mon, a.store, a.version)
	if err != nil {
		return nil, err
	}
	srv.SetAutostartFunc(a.autostartSet)
	srv.SetAutostartStateFunc(a.autostartGet)
	srv.SetOpenConsoleFunc(a.openConsole)
	return &webConsole{srv: srv}, nil
}

func (c *webConsole) Start() error                 { return c.srv.Start() }
func (c *webConsole) URL() string                  { return c.srv.URL() }
func (c *webConsole) Port() int                    { return c.srv.Port() }
func (c *webConsole) Token() string                { return c.srv.Token() }
func (c *webConsole) Shutdown(ctx context.Context) { c.srv.Shutdown(ctx) }
