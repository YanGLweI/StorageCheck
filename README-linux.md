# StorageCheck Linux 版（RHEL 9 / 10）

无图形界面的磁盘空间监控服务。装完就是一个 systemd 服务，配置通过
`sudo storagecheck` 打开的终端拟态界面（类 `nmtui`）完成。

## 安装

```bash
sudo dnf install ./storagecheck-1.1.2-1.el10.x86_64.rpm
systemctl status storagecheck        # 安装结束即已 active
```

RPM 落地的文件：

| 路径 | 说明 |
| --- | --- |
| `/usr/bin/storagecheck` | 静态二进制（无 glibc 依赖） |
| `/usr/lib/systemd/system/storagecheck.service` | 服务单元 |
| `/etc/storagecheck/config.json` | 配置（`%config(noreplace)`，升级不覆盖） |
| `/etc/storagecheck/seal.key` | 授权码加密密钥（首次保存时生成，0600） |
| `/var/lib/storagecheck/state.json` | 实时状态快照，`status` 命令读它 |
| `/var/lib/storagecheck/events.log` | 事件流（JSONL，超 5 MiB 轮转，保留 3 份） |

## 配置界面

```bash
sudo storagecheck
```

方向键移动、回车进入、空格切换开关、`ctrl+s` 保存（保存即向服务发 `SIGHUP` 热重载，
不需要 restart），`esc` 返回上一层，`q` 或 `ctrl+c` 退出。

光标停在输入框里时，可打印字符、空格与左右键都归输入框（否则主机名里的 `t`、
收件人里的空格会被快捷键吃掉）；此时换行用 `↑↓`，动作键一律带 `ctrl`。

| 页面 | 作用 |
| --- | --- |
| 监控总览 | 各挂载点容量、剩余、阈值与告警状态；`c` 立即检查、`p` 暂停/继续 |
| 磁盘配置 | 勾选要监控的挂载点、设阈值；`a` 重新扫描、`n` 是否包含网络盘 |
| 告警设置 | 检查间隔、告警冷却、恢复迟滞、恢复通知、新盘处理方式 |
| 邮件设置 | 服务商预设、服务器/端口/加密、账号、授权码、收件人；`ctrl+t` 发测试邮件 |
| 事件日志 | 最近的告警、恢复与错误 |
| 服务状态 | systemd 激活与开机自启状态、最近日志、重载配置 |

授权码输入为掩码，落盘前用 AES-256-GCM 加密（密钥 `seal.key`，0600 属 root），
不会出现在命令行参数或 `ps` 里。

`ctrl+t` 发测试邮件用的是**屏幕上当前填的值**，不必先 `ctrl+s`：填完收件人就能直接试发，
试发也只写内存、不落盘，「有未保存改动」的提示会保留到你真正按 `ctrl+s`。

不想把密码放进配置文件时，可用 systemd 凭据通道：

```bash
# /etc/storagecheck/smtp_password 内容设为授权码，chmod 600
# 在 storagecheck.service 的 drop-in 里加：
#   LoadCredential=smtp_password:/etc/storagecheck/smtp_password
```

服务读取顺序为：systemd 凭据 > `STORAGECHECK_SMTP_PASSWORD` > 配置里的密文。

## 只读命令

```bash
storagecheck status            # 表格；--json 输出机器可读
storagecheck events -n 50      # 最近事件
storagecheck check             # 请求服务立即做一次检查
storagecheck drives --all      # 列出候选挂载点及纳入/排除原因
storagecheck test-mail         # 按当前配置发一封测试邮件
```

这些命令不写配置。改配置只有终端界面一条路径。

## 服务的控制信号

| 信号 | 作用 | 等价操作 |
| --- | --- | --- |
| `SIGHUP` | 重新加载 `config.json` | `systemctl reload storagecheck` |
| `SIGUSR2` | 立即检查一次 | `storagecheck check` |
| `SIGUSR1` | 暂停 / 继续监控 | 总览页 `p` |
| `SIGTERM` | 优雅退出（等在途邮件发完，最长 30 秒） | `systemctl stop` |

## 常见问题

**为什么我的 NFS/CIFS 盘没有出现在列表里？**
网络挂载默认排除：服务器失联时 `statfs` 会阻塞，且容量语义与本地盘不一致。
在磁盘配置页按 `n` 打开「包含网络挂载」即可看到，此时每次采样对该类挂载点
设 3 秒超时，超时的只标记为不可用，不会拖慢整个采样循环。

**剩余空间显示得比 `df` 的 Available 少/多？**
本程序与 `df` 的 Avail 列一致：可用空间按 `f_bavail`（非特权用户真正能写的部分）计算，
ext4/xfs 默认预留给 root 的那部分不计入，因此比「总容量 − 已用」要小。这正是运维直觉。

**新插上的数据盘为什么只写了事件、没开始监控？**
RPM 的默认配置是 `auto_enable_new: false`：多盘机器上新挂载点（含临时挂载、容器卷）
自动纳入容易产生告警风暴。日志与事件里会出现「发现未监控的挂载点 …」，
到磁盘配置页勾选并保存即可。想让新盘自动生效，在告警设置页把「新磁盘处理」改成
「自动纳入监控」。

**日志在哪里看？**
`journalctl -u storagecheck -f`。程序只往 stderr 写日志，由 journald 收集；
每 30 秒一次的内部快速采样不打日志，但每 15 分钟会输出一行「状态心跳」，
写明最差磁盘、告警数量与邮件配置是否就绪——没有这行才说明服务没在跑。

**EFI 分区、`/boot`、容器卷为什么被过滤掉了？**
两条规则：容量低于下限（默认 1 GiB）忽略；命中内置前缀黑名单
（`/proc`、`/sys`、`/dev`、`/run`、`/var/lib/docker`、`/var/lib/kubelet`、`/snap`）忽略；
文件系统类型为虚拟/网络类（`tmpfs`、`overlay`、`fuse.*` 等）或块设备名不是真实磁盘
（`loop*`、`ram*`、`zram*`、`sr*`）也忽略。要额外排除某个位置，在告警设置页调容量下限，
或在 `config.json` 的 `linux.mount_exclude` 里加前缀。

**同一个挂载点反复出现/消失怎么办？**
配置以挂载点路径为 ID。挂载点改名会被视为「新盘」，旧条目连续 30 天不再出现且未被
启用时会在保存时清理掉。

**配置文件被手工改坏（比如加了 `#` 注释）怎么办？**
JSON 不支持注释，改坏后 `storagecheck status` 会报
`配置文件 /etc/storagecheck/config.json 无法解析: …`，服务重启时也会在同一行里带上文件路径。
终端界面不受影响：它检测到解析失败后以默认值启动，底部常驻一行降级说明，
你把配置改回合法再 `ctrl+s` 保存，坏文件即被覆盖修复。

**能用非 root 跑吗？**
可以，但只能读到自己有权限的挂载点。加 drop-in：

```bash
sudo systemctl edit storagecheck    # 写入 [Service] 段
#   User=storagecheck
#   Group=storagecheck
sudo systemctl daemon-reload && sudo systemctl restart storagecheck
```
同时把 `/etc/storagecheck`、`/var/lib/storagecheck` 属主改成该用户。

**SELinux？**
RHEL 上服务以常规 systemd 域运行，采集只读挂载点、发信走网络，不涉及策略拦截。
若自定义了非标准配置目录并被拒绝访问，用 `semanage fcontext` + `restorecon` 打标，
而不是关闭 enforcing。

## 构建

```bash
brew install rpm                       # 一次性
./build-linux.sh 1.1.2                 # 二进制 + el9/el10 × x86_64/aarch64
./build-linux.sh 1.1.2 --bins-only     # 只要二进制
./build-linux.sh 1.1.2 --in-container  # 改用 ubi10 容器里的 rpmbuild
./build.sh 1.1.2 all                    # Windows + Linux 一起出
```

构建在 macOS 上完成（`CGO_ENABLED=0` 交叉编译），`build-linux.sh` 会先跑
`GOOS=linux go vet ./...` 作为构建标签守卫。
