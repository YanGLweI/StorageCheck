# StorageCheck

磁盘空间监控与邮件告警工具，一套核心状态机、两个交付形态：

- **Windows**：系统托盘 + 本地 Web 控制台，按用户安装、无需管理员。
- **Linux（RHEL 9 / 10）**：无图形界面的 systemd 服务，配置全部在终端拟态界面（类 `nmtui`）里完成，RPM 交付。

剩余空间低于阈值时发送中文 HTML 告警邮件；每盘独立阈值、告警冷却、升级提醒、
恢复通知与迟滞、邮件失败重试，两个平台共用同一套逻辑。

## 快速开始

**Windows**：下载 `StorageCheck-Setup-<版本>-win-amd64.exe`（骁龙 X 等 ARM64 机器用 `-win-arm64.exe`）
双击安装，托盘出现图标即已开始监控。安装包未做代码签名，首次运行会有 SmartScreen 提示
（「更多信息 → 仍要运行」）。

**Linux**：

```bash
sudo dnf install ./storagecheck-<版本>-1.el10.x86_64.rpm   # el9/el10 按系统，架构看 uname -m
systemctl status storagecheck                              # 装完即 active，并已 enable 开机自启
sudo storagecheck                                          # 打开终端配置界面
```

Linux 版的界面按键、信号控制、只读命令与常见问题集中在
[README-linux.md](README-linux.md)，那边是权威文档。

## 功能

两个平台共用：

- 自动发现本机磁盘/挂载点，每盘独立「剩余百分比」阈值（默认 10%）
- 告警状态机：触发即发邮件；冷却期内不重复骚扰；超冷却发升级提醒；恢复需越过「阈值+迟滞」，可选发送恢复邮件
- 邮件失败自动进入重试窗口，事件写入历史与 `events.log`
- SMTP 预设：QQ / 163 / 腾讯企业邮 / 阿里企业邮 / Gmail / Outlook，亦可自定义（SSL 465、STARTTLS 587、内网免密）
- 新盘处理方式可选「自动纳入监控」或「只记事件、等人工勾选」

Windows 独有：

- 托盘：左键唤起控制台窗口（窗口不存在则新建），右键菜单——打开控制台 / 暂停监控 / 立即检查 / 退出；任一磁盘告警时图标变红
- 再次启动（双击快捷方式）不会开出第二个界面，而是把已打开的控制台窗口还原并调到前台
- 本地控制台仅监听 `127.0.0.1`，一次性 token 换 HttpOnly Cookie，API 全部鉴权
- 开机自启写 HKCU Run 键，无需管理员；自启时以 `--minimized` 静默运行；以系统启动项为唯一事实来源，安装向导里勾选后程序会自动识别
- 授权码用当前账户的 DPAPI 加密落盘，界面永不回显

Linux 独有：

- `sudo storagecheck` 终端界面六个页面：监控总览 / 磁盘配置 / 告警设置 / 邮件设置 / 事件日志 / 服务状态
- 控制面只用信号：`SIGHUP` 重载配置（保存即热重载，不需要 restart）、`SIGUSR2` 立即检查、`SIGUSR1` 暂停/继续、`SIGTERM` 优雅停止
- 静态二进制（`CGO_ENABLED=0`，无 glibc 依赖），不监听任何端口，Web 控制台不参与 Linux 编译
- `flock` 单实例；进程被 `kill -9` 也由内核自动释放锁
- 只读命令 `status`（`--json` 供脚本接入）/ `events` / `drives` / `check` / `test-mail`
- 授权码以 AES-256-GCM 加密（同目录 `seal.key`，0600 属 root），不出现在命令行参数与 `ps` 里；
  也可走 systemd 凭据通道（读取顺序：systemd 凭据 > `STORAGECHECK_SMTP_PASSWORD` > 配置密文）

## 构建（macOS 上交叉编译，无需 Windows 或 Linux 机器）

```bash
brew install nsis rpm      # 一次性
./build.sh 1.1.2           # 只出 Windows 绿色 exe + 安装包
./build.sh 1.1.2 linux     # Windows + Linux 静态二进制（不打 RPM，快速验证能编过）
./build.sh 1.1.2 all       # Windows + Linux 二进制 + el9/el10 × x86_64/aarch64 的 RPM（发版用）
./build-linux.sh 1.1.2     # 只出 Linux，详见 README-linux.md 的「构建」
```

`build.sh` 依次完成：

1. `go run ./tools/genres <版本>` — 生成 exe 图标/版本信息/manifest 资源（`*.syso`）
2. `go run ./tools/genico` — 生成安装包 `build/icon.ico`
3. 交叉编译 `CGO_ENABLED=0 -ldflags "-s -w -H windowsgui"` 的 GUI 程序（amd64 + arm64）
4. `makensis build/installer.nsi` — NSIS 安装包（按用户安装、无需管理员，含卸载与开机自启选项）
5. 第二个参数为 `linux`/`rpm`/`all` 时调用 `build-linux.sh`：先跑 `GOOS=linux go vet ./...`
   作为构建标签守卫（漏写 `//go:build linux` 会在这一步暴露），再交叉编译；
   `rpm`/`all` 额外用 rpmbuild 打出四个 RPM

产物都在 `dist/`。注意 `makensis` 需 UTF-8 locale（脚本内已设 `LC_ALL=en_US.UTF-8`）。

## 开发

```bash
go test ./...                                    # 状态机、采集、配置、终端界面单测
GOOS=linux go vet ./...                          # Linux 侧编译守卫
STORAGECHECK_DEV_FREEPCT=8 \
  go run ./cmd/storagecheck --dev                # macOS 上用模拟磁盘数据联调 Web 界面
```

`--dev` 使用假盘数据（C: 或挂载点的剩余百分比由环境变量控制），不开托盘。
终端界面只在 Linux 下编译，在 macOS 上想看界面需要进容器或真机。
构建标签三路拆分：`windows` / `linux` / 其余（macOS 开发机），改动入口或采集器时两条平台线各验一遍。

终端界面的交互回归靠 `internal/tui/app_test.go`：它把按键喂给真实的 Bubble Tea 更新链，
覆盖「输入框与快捷键抢按键」这类只有真机才会暴露的问题。

## 目录结构

```
cmd/storagecheck      入口：装配、单实例、托盘、窗口前置与浏览器；Linux 侧为命令树与终端界面
internal/model        数据结构与人类可读的容量/时间格式
internal/collector    磁盘采集（Win32 API / Linux mounts+statfs / dev Fake）
internal/config       配置读写（原子保存、授权码加密、flock、目录解析）
internal/monitor      告警状态机（平台无关，两个交付形态直接复用）
internal/mailer       SMTP 与 HTML 邮件模板（含超时与重试）
internal/autostart    开机自启（HKCU Run / systemd 单元 / dev 标记文件）
internal/state        Linux 侧状态快照与守护进程 pid 发现
internal/tui          Linux 终端拟态界面（仅 linux 编译）
internal/server       本地 Web 服务 + go:embed 前端（Windows 与 dev，Linux 不链接）
internal/tray         托盘与菜单
internal/appicon      程序化图标绘制与 ICO 编码（托盘/exe/安装包共用）
tools/genres          Windows 资源(.syso)生成器
tools/genico          安装包图标(.ico)生成器
build/installer.nsi           NSIS 安装脚本
build/storagecheck.service    systemd 单元
build/storagecheck.spec       RPM 打包描述
build/config.default.json     RPM 里落地的默认配置
```

## 数据位置

- **Windows**：`%APPDATA%\StorageCheck`（macOS 开发模式在 `~/Library/Application Support/StorageCheck`）
  — `config.json`、`events.log`、`storagecheck.log`、`run.json`
- **Linux**：`/etc/storagecheck/`（`config.json` + `seal.key`）与 `/var/lib/storagecheck/`
  （`state.json`、`events.log`、`.lock`），程序日志进 journald（`journalctl -u storagecheck`）
- 配置目录解析顺序：`--config-dir` > `$STORAGECHECK_CONFIG_DIR` > 系统安装目录 > 用户配置目录

## 版本与发版

当前发布 **v1.1.2**（<https://github.com/YanGLweI/StorageCheck/releases/tag/v1.1.2>）：
修正阿里企业邮 SMTP 预设地址。Release 挂 2 个 Windows Setup 安装包与 4 个 RPM，
说明里带实测 SHA-256 与真机验证步骤；RPM 与安装包均未做 GPG / 代码签名，请以上面的哈希核对。

- Windows 版：已完成（v1.0.x 起，托盘 + Web 控制台 + NSIS 安装包）
- Linux（RHEL 9/10）无 GUI 版：已完成（v1.1.x 起，statfs 采集器 + systemd 服务 + 终端配置界面 + RPM）
