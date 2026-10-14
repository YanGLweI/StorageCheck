package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"

	"storagecheck/internal/config"
	"storagecheck/internal/mailer"
	"storagecheck/internal/model"
	"storagecheck/internal/monitor"
	"storagecheck/internal/state"
)

// opts 由持久 flag 填充，各子命令共享。
var opts runOptions

func main() {
	os.Exit(execute())
}

func execute() int {
	root := newRootCommand()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "执行失败:", err)
		return 1
	}
	return 0
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:     "storagecheck",
		Short:   "磁盘空间监控与邮件告警",
		Version: version,
		Args:    cobra.NoArgs,
		// 错误由 execute() 统一中文输出，否则同一条失败会打两遍
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return defaultAction(cmd, opts)
		},
	}
	root.SetVersionTemplate("storagecheck " + version + "\n")

	pf := root.PersistentFlags()
	pf.StringVar(&opts.configDir, "config-dir", "", "配置目录（留空则自动选择：$STORAGECHECK_CONFIG_DIR → /etc/storagecheck → 用户配置目录）")
	pf.BoolVar(&opts.dev, "dev", false, "使用模拟磁盘数据（开发调试用）")
	pf.BoolVar(&opts.minimized, "minimized", false, "启动后不打开控制台窗口")

	root.AddCommand(
		newRunCommand(),
		newStatusCommand(),
		newEventsCommand(),
		newCheckCommand(),
		newTestMailCommand(),
		newVersionCommand(),
	)
	registerPlatformCommands(root)
	return root
}

func newRunCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "以前台方式运行监控服务（systemd ExecStart 用）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runApp(opts)
		},
	}
}

func newStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "显示当前监控状态",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgDir, dataDir, err := resolveDirs()
			if err != nil {
				return err
			}
			view, offline, err := loadState(cfgDir, dataDir)
			if err != nil {
				return err
			}
			if asJSON {
				data, err := json.MarshalIndent(view, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}
			printStatus(cmd.OutOrStdout(), view, offline)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "以 JSON 输出（键名保持英文，便于脚本消费）")
	return cmd
}

func newEventsCommand() *cobra.Command {
	var n int
	cmd := &cobra.Command{
		Use:   "events",
		Short: "显示最近的告警与系统事件",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, dataDir, err := resolveDirs()
			if err != nil {
				return err
			}
			evs, err := state.Events(dataDir, n)
			if err != nil {
				return err
			}
			if len(evs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "暂无事件记录")
				return nil
			}
			lines := [][]string{{"时间", "类型", "磁盘", "说明"}}
			for _, e := range evs {
				lines = append(lines, []string{
					e.Time.Format("2006-01-02 15:04:05"), kindText(e.Kind), orDash(e.Drive), firstLine(e.Message),
				})
			}
			printTable(cmd.OutOrStdout(), lines)
			return nil
		},
	}
	cmd.Flags().IntVarP(&n, "tail", "n", 20, "显示最近多少条事件")
	return cmd
}

func newCheckCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "请求正在运行的服务立即执行一次检查",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgDir, dataDir, err := resolveDirs()
			if err != nil {
				return err
			}
			if err := notifyCheck(dataDir); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "已请求立即检查，以下为当前状态：")
			view, offline, err := loadState(cfgDir, dataDir)
			if err != nil {
				return err
			}
			printStatus(cmd.OutOrStdout(), view, offline)
			return nil
		},
	}
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "显示版本信息",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "storagecheck %s\n", version)
		},
	}
}

func newTestMailCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "test-mail",
		Short: "按当前配置发送一封测试邮件，用于验证 SMTP 设置",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgDir, _, err := resolveDirs()
			if err != nil {
				return err
			}
			store, err := config.NewStore(cfgDir, newSecretBox(cfgDir))
			if err != nil {
				return err
			}
			cfg, err := store.Load()
			if err != nil {
				return err
			}
			pass, err := smtpPassword(store, cfg)
			if err != nil {
				return err
			}
			host, _ := os.Hostname()
			if err := mailer.New(cfg.SMTP, pass).SendTest(host); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "测试邮件已发往：%s\n", strings.Join(cfg.SMTP.Recipients, ", "))
			return nil
		},
	}
}

// resolveDirs 给出配置目录与运行数据目录：RPM 布局下两者不同（/etc 与 /var/lib）。
func resolveDirs() (cfgDir, dataDir string, err error) {
	cfgDir, err = config.ResolveDir(opts.configDir)
	if err != nil {
		return "", "", err
	}
	return cfgDir, config.DataDir(cfgDir), nil
}

// loadState 取得展示用状态；采集器由平台 seam 提供。
func loadState(cfgDir, dataDir string) (monitor.StateView, bool, error) {
	cfg, err := config.LoadAt(cfgDir)
	if err != nil {
		return monitor.StateView{}, true, err
	}
	return state.View(dataDir, newCollector(opts.dev, func() *config.Config { return cfg }), cfg, version)
}

func printStatus(out io.Writer, view monitor.StateView, offline bool) {
	fmt.Fprintf(out, "主机：%s  版本：%s  更新于 %s\n", orDash(view.Hostname), orDash(view.Version), view.UpdatedAt.Format("2006-01-02 15:04:05"))
	if view.Paused {
		fmt.Fprintln(out, "状态：已暂停监控（SIGUSR1 恢复）")
	}
	if offline {
		fmt.Fprintln(out, "说明：未读到运行中服务的快照，以下为一次性采集结果")
	}
	if len(view.Drives) == 0 {
		fmt.Fprintln(out, "\n当前没有已配置的磁盘")
		return
	}
	lines := [][]string{{"磁盘", "卷标", "容量", "剩余", "已用", "阈值", "状态"}}
	for _, d := range view.Drives {
		lines = append(lines, []string{
			d.ID, orDash(d.Label), humanBytes(d.Total), humanBytes(d.Free),
			fmt.Sprintf("%.1f%%", d.UsedPct), thresholdText(d.ThresholdPct), driveStateText(d),
		})
	}
	printTable(out, lines)
}

// printTable 按终端格子数对齐输出。tabwriter 按字符数计宽，
// 中文表头会被算成一半宽，整列跟着错位。
func printTable(out io.Writer, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			if n := runewidth.StringWidth(c); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for _, r := range rows {
		var b strings.Builder
		for i, c := range r {
			b.WriteString(c)
			if i == len(r)-1 {
				break
			}
			b.WriteString(strings.Repeat(" ", widths[i]-runewidth.StringWidth(c)+2))
		}
		fmt.Fprintln(out, strings.TrimRight(b.String(), " "))
	}
}

// firstLine 事件说明可能带多行堆栈，列表里只显示首行。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func driveStateText(d monitor.DriveView) string {
	if !d.Enabled {
		return "未监控"
	}
	if d.Alerting {
		return "告警中"
	}
	return "正常"
}

func thresholdText(pct float64) string {
	if pct <= 0 {
		return "-"
	}
	return fmt.Sprintf("剩余<=%.0f%%", pct)
}

func kindText(k model.EventKind) string { return model.KindLabel(k) }

func humanBytes(v uint64) string { return model.HumanBytes(v) }

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
