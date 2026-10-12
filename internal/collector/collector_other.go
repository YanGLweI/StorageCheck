//go:build !windows

package collector

import (
	"errors"

	"storagecheck/internal/model"
)

// 非 Windows 平台（macOS 开发模式）不提供真实采集器，由 --dev 注入 Fake。
// 后续 Linux 版将在此提供 statfs 实现。
func New() Collector { return &stubCollector{} }

type stubCollector struct{}

var errNoCollector = errors.New("collector: 当前平台暂无采集器，请使用 --dev 模式")

func (c *stubCollector) List() ([]model.DriveStat, error) { return nil, errNoCollector }
