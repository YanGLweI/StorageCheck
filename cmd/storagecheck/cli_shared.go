package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"storagecheck/internal/config"
)

// opts 由平台入口（entry_desktop.go 的 flag、cli.go 的持久 flag）填充。
var opts runOptions

func main() {
	// 参数解析之前也可能失败，而 windowsgui 子系统下 stderr 没有落点：先给日志找个地方，
	// runApp 解析出真正的配置目录后再换过去。
	_ = setupLogging(bootstrapLogDir())
	os.Exit(dispatch())
}

// bootstrapLogDir 用不依赖 flag 的方式先定一个日志目录。定不出来就退到 exe 所在目录，
// 至少让「启动失败」那句话有个落点，而不是让程序凭空消失。
func bootstrapLogDir() string {
	if dir, err := config.ResolveDir(""); err == nil {
		return dir
	}
	exe, err := os.Executable()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Dir(exe)
}

// reportStartupFailure 把启动状况连同进程自己看到的环境一起写盘。
// 它刻意不走 log：windowsgui 下 stderr 没有落点，而 setupLogging 本身往往就是出问题的那一环。
// 写不进一个目录就再写下一个，不接受静默。
func reportStartupFailure(event error) {
	cfgDir, cfgErr := config.ResolveDir(opts.configDir)
	exe, exeErr := os.Executable()
	cwd, cwdErr := os.Getwd()
	msg := fmt.Sprintf("%s %v | 配置目录=%q(解析错误=%v) | exe=%q(err=%v) | 工作目录=%q(err=%v) | APPDATA=%q TMP=%q TEMP=%q | 命令行=%q\n",
		time.Now().Format("2006-01-02 15:04:05"), event,
		cfgDir, cfgErr, exe, exeErr, cwd, cwdErr,
		os.Getenv("APPDATA"), os.Getenv("TMP"), os.Getenv("TEMP"), strings.Join(os.Args, " "))

	seen := map[string]bool{}
	dirs := []string{cfgDir, os.TempDir()}
	if exeErr == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	for _, dir := range dirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		_ = os.MkdirAll(dir, 0o700)
		f, err := os.OpenFile(filepath.Join(dir, "storagecheck-startup.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			continue
		}
		f.WriteString(msg)
		f.Close()
	}
}
