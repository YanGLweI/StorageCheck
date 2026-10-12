# StorageCheck

Windows 磁盘空间监控与邮件告警工具。剩余空间低于阈值时发送中文 HTML 告警邮件，
带系统托盘与本地 Web 控制台，支持每盘独立阈值、告警冷却、恢复通知与开机自启。

## 功能

- 自动发现本机固定磁盘，每盘独立「剩余百分比」阈值（默认 10%）
- 告警状态机：触发即发邮件；冷却期内不重复骚扰；超冷却发升级提醒；恢复需越过「阈值+迟滞」并可选发送恢复邮件
- 邮件失败自动进入重试窗口，事件写入历史与 `events.log`
- SMTP 预设：QQ / 163 / 腾讯企业邮 / 阿里企业邮 / Gmail / Outlook，亦可自定义（SSL 465、STARTTLS 587、内网免密）
- 授权码使用 Windows DPAPI 加密落盘，界面永不回显
- 托盘：打开控制台 / 暂停监控 / 立即检查 / 退出；任一磁盘告警时图标变红
- 开机自启（HKCU Run 键，无需管理员），自启时以 `--minimized` 静默运行
- 本地控制台仅监听 127.0.0.1，一次性 token 换 HttpOnly Cookie，API 全部鉴权

## 构建（macOS / Linux 交叉编译，无需 Windows）

```bash
brew install nsis      # 一次性
./build.sh 1.0.0       # 产出 dist/ 下的绿色 exe 与安装包
```

`build.sh` 依次完成：

1. `go run ./tools/genres <版本>` — 生成 exe 图标/版本信息/manifest 资源（`*.syso`）
2. `go run ./tools/genico` — 生成安装包 `build/icon.ico`
3. 交叉编译 `CGO_ENABLED=0 -ldflags "-s -w -H windowsgui"` 的 GUI 程序（amd64 + arm64）
4. `makensis build/installer.nsi` — 编译 NSIS 安装包（按用户安装、无需管理员，含卸载与开机自启选项）

产物：`dist/StorageCheck-Setup-1.0.0-win-{amd64,arm64}.exe`。
注意：makensis 需 UTF-8 locale（脚本已在 build.sh 中设置 `LC_ALL=en_US.UTF-8`）。

## 开发

```bash
go test ./...                                  # 状态机全路径单元测试
STORAGECHECK_DEV_FREEPCT=8 \
  go run ./cmd/storagecheck --dev              # macOS 上用模拟磁盘数据联调 UI
```

`--dev` 模式使用假盘数据（C: 剩余百分比由环境变量控制），不开托盘。

## 目录结构

```
cmd/storagecheck      入口：装配、单实例、托盘、浏览器
internal/model        数据结构
internal/collector    磁盘采集（Win32 API / dev Fake）
internal/config       配置读写（原子保存、授权码加密）
internal/monitor      告警状态机（平台无关，Linux 版直接复用）
internal/mailer       SMTP 与 HTML 邮件模板
internal/autostart    开机自启（HKCU Run / dev 标记文件）
internal/server       本地 Web 服务 + go:embed 前端
internal/tray         托盘与图标绘制
tools/genres          Windows 资源(.syso)生成器
tools/genico          安装包图标(.ico)生成器
build/installer.nsi   NSIS 安装脚本
```

## 数据位置

配置与日志位于 `%APPDATA%\StorageCheck`（macOS 开发模式在 `~/Library/Application Support/StorageCheck`）：
`config.json`、`events.log`、`storagecheck.log`、`run.json`。

## 路线图

- Linux（RHEL 9/10）无 GUI 版：statfs 采集器 + systemd 单元 + `storagecheck config` 命令行，复用 internal 库
