//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/spf13/cobra"

	"storagecheck/internal/collector"
	"storagecheck/internal/config"
	"storagecheck/internal/state"
	"storagecheck/internal/tui"
)

// mountRule 把配置里的采集开关翻译成筛选规则，drives 视图与终端界面共用。
func mountRule(cfg *config.Config) collector.MountRule {
	return collector.MountRule{
		IncludeNetwork:  cfg.Linux.IncludeNetwork,
		ExcludePrefixes: cfg.Linux.MountExclude,
		MinTotalBytes:   cfg.MinTotal(),
	}
}

// registerPlatformCommands 补充只在无 GUI 形态下才有意义的命令。
func registerPlatformCommands(root *cobra.Command) {
	root.AddCommand(newDrivesCommand())
}

// defaultAction：直接敲 storagecheck 就是打开终端拟态界面（需要 sudo 才能写配置）。
func defaultAction(cmd *cobra.Command, o runOptions) error {
	cfgDir, dataDir, err := resolveDirs()
	if err != nil {
		return err
	}
	if !isInteractiveTerminal() {
		// cron、管道、CI 里没有终端，初始化 TUI 会留下坏屏幕：退化为打印状态。
		view, offline, err := loadState(cfgDir, dataDir)
		if err != nil {
			return err
		}
		printStatus(cmd.OutOrStdout(), view, offline)
		fmt.Fprintln(cmd.OutOrStdout(), "\n当前不是交互式终端；如需配置界面请在 tty 下运行 `sudo storagecheck`。")
		return nil
	}
	return tui.Run(cmd.OutOrStdout(), cfgDir, dataDir, version)
}

func isInteractiveTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// notifyCheck 给运行中的守护进程发 SIGUSR2；pid 取自单实例锁文件。
func notifyCheck(dir string) error {
	pid, ok := state.DaemonPID(dir)
	if !ok {
		return fmt.Errorf("没有发现正在运行的服务（缺少 %s），可先执行 `systemctl start storagecheck`", state.LockPath(dir))
	}
	if err := syscall.Kill(pid, syscall.SIGUSR2); err != nil {
		return fmt.Errorf("向进程 %d 发送检查请求失败: %w", pid, err)
	}
	return nil
}

// signalDaemon 向守护进程发送任意信号，终端界面保存配置后用 SIGHUP 触发热重载。
func signalDaemon(dir string, sig syscall.Signal) bool {
	pid, ok := state.DaemonPID(dir)
	if !ok {
		return false
	}
	return syscall.Kill(pid, sig) == nil
}

func newDrivesCommand() *cobra.Command {
	var showAll bool
	cmd := &cobra.Command{
		Use:   "drives",
		Short: "列出候选挂载点及纳入/排除的原因（排查「为什么这块盘没被监控」）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgDir, _, err := resolveDirs()
			if err != nil {
				return err
			}
			cfg, err := config.LoadAt(cfgDir)
			if err != nil {
				return err
			}
			rows, err := collector.Diagnose(mountRule(cfg))
			if err != nil {
				return err
			}
			lines := [][]string{{"挂载点", "设备", "文件系统", "容量", "剩余", "结论"}}
			for _, r := range rows {
				if !showAll && !r.Keep {
					continue
				}
				verdict := r.Reason
				if r.Keep {
					verdict = "采集中"
				}
				lines = append(lines, []string{
					r.Mountpoint, r.Device, r.FSType, humanBytes(r.Total), humanBytes(r.Free), verdict,
				})
			}
			printTable(cmd.OutOrStdout(), lines)
			if !showAll {
				fmt.Fprintln(cmd.OutOrStdout(), "\n加 --all 查看被排除的挂载点")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&showAll, "all", false, "同时列出被排除的挂载点")
	return cmd
}
