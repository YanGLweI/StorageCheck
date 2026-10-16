//go:build linux

package main

// dispatch：服务版走 cobra 子命令树（run/status/events/check/test-mail）。
func dispatch() int { return execute() }
