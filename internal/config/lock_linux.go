//go:build linux

package config

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockPath 用旁路锁文件（而非配置文件本身）串行化写入：
// 保存走 tmp+rename，inode 每次都会换，锁目标文件本身不可靠。
func lockPath(target string) (func(), error) {
	f, err := os.OpenFile(target+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, nil
}
