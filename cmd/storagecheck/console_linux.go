//go:build linux

package main

import (
	"context"
)

// headlessConsole：Linux 版没有图形界面也不内嵌 Web 控制台，
// 状态通过 state.json + 终端界面/命令行读取，配置通过 SIGHUP 热生效。
type headlessConsole struct{}

func newConsole(consoleArgs) (console, error) { return headlessConsole{}, nil }

func (headlessConsole) Start() error             { return nil }
func (headlessConsole) URL() string              { return "" }
func (headlessConsole) Port() int                { return 0 }
func (headlessConsole) Token() string            { return "" }
func (headlessConsole) Shutdown(context.Context) {}
