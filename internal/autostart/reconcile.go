package autostart

import (
	"strings"
)

// Reconcile 让「注册表里的开机自启」与 config.json 对齐。
//
// 开机自启的事实来源是系统启动项本身：安装向导的勾选项、用户手工改注册表、
// 把程序移动到新目录，都不会经过 config.json。不对齐就会出现
// 「安装时勾了开机自启，但程序一保存设置就把启动项删掉」这类问题。
//
// 返回 enabled（当前真实状态）与 changed（配置是否需要更新）。
func Reconcile(m Manager, exePath string, args []string, cfgEnabled bool) (enabled bool, changed bool, err error) {
	cmd, err := m.Command()
	if err != nil {
		return cfgEnabled, false, err
	}
	if strings.TrimSpace(cmd) == "" {
		if !cfgEnabled {
			return false, false, nil
		}
		// 配置要求自启但启动项缺失（被清理工具删掉等）：补写
		return true, false, m.Enable(exePath, args)
	}
	if exePath != "" && !samePath(strippedExe(cmd), exePath) {
		// 安装位置变了：把启动项刷新到当前 exe，否则开机拉起的是已删除的旧文件
		if err := m.Enable(exePath, args); err != nil {
			return true, true, err
		}
	}
	return true, !cfgEnabled, nil
}

// strippedExe 取出启动命令里的可执行文件路径（可能带引号，后跟参数）。
func strippedExe(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if i := strings.IndexByte(cmd[1:], '"'); i >= 0 {
			return cmd[1 : 1+i]
		}
		return strings.TrimPrefix(cmd, `"`)
	}
	if i := strings.IndexByte(cmd, ' '); i >= 0 {
		return cmd[:i]
	}
	return cmd
}

// samePath 判断两个路径是否指向同一文件：Windows 路径大小写不敏感，
// 且分隔符可能是 \ 或 /。这里自己做归一化，不依赖宿主平台的 filepath 语义。
func samePath(a, b string) bool {
	return strings.EqualFold(normalizePath(a), normalizePath(b))
}

func normalizePath(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	parts := strings.Split(p, "/")
	segs := make([]string, 0, len(parts))
	for _, s := range parts {
		if s != "" && s != "." {
			segs = append(segs, s)
		}
	}
	return strings.Join(segs, "/")
}
