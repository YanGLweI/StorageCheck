package main

import (
	"os"
	"strconv"

	"storagecheck/internal/collector"
	"storagecheck/internal/model"
)

// devDrives 为 --dev 模式提供模拟磁盘数据；STORAGECHECK_DEV_FREEPCT 控制 C: 剩余百分比。
func devDrives() collector.Collector {
	free := 20.0
	if v, err := strconv.ParseFloat(os.Getenv("STORAGECHECK_DEV_FREEPCT"), 64); err == nil {
		free = v
	}
	const gib = uint64(1 << 30)
	return &collector.Fake{Drives: []model.DriveStat{
		collector.MakeStat("C:", "系统盘", 512*gib, uint64(float64(512*gib)*free/100)),
		collector.MakeStat("D:", "数据盘", 1024*gib, 563*gib),
		collector.MakeStat("E:", "软件盘", 256*gib, 205*gib),
	}}
}
