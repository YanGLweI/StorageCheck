# StorageCheck RPM spec
# 由 build-linux.sh 调用，也可手工：
#   rpmbuild --define "_topdir $PWD/dist/rpm/x86_64" --define "_el 10" \
#            --target x86_64-redhat-linux -bb build/storagecheck.spec
#
# 交叉打包（macOS 上打 RHEL 包）必须关掉这些：
#   - debug_package：没有本地工具链去抽调试符号，静态 Go 二进制也不需要
#   - __os_install_post / __strip：RHEL 的 brp-* 脚本在 macOS 上不存在
%global debug_package %{nil}
%global __os_install_post %{nil}
%global __strip /bin/true
%global _binary_payload w9.gzdio
%undefine _missing_build_ids_terminate_build

# el 标签由构建脚本传入；默认跟当前主版本走
%{!?_el: %global _el 10}

# 版本号由构建脚本传入（--define "_version 1.1.0"），缺省值只用于手工打包时兜底
%{!?_version: %global _version 1.1.0}

Name:           storagecheck
Version:        %{_version}
Release:        1.el%{_el}%{?dist}
Summary:        磁盘空间监控与邮件告警服务
License:        Proprietary
URL:            https://github.com/yeunglw/storagecheck
# 不写 BuildArch：二进制是交叉编译出来的架构相关产物，
# 由 --target 决定 x86_64/aarch64，写成 noarch 会装到错的机器上

# 交叉编译产物：spec 不做任何编译，只负责摆放文件
Source0:        storagecheck
Source1:        storagecheck.service
Source2:        config.default.json

# 静态 Go 二进制没有库依赖，依赖全靠下面手写；关掉自动探测，
# 否则 macOS 的 rpmbuild 会去找它根本没有的 elfdeps 脚本报错
AutoReqProv:    no
%global __elfdeps /bin/true
%global __find_provides /bin/true
%global __find_requires /bin/true

# el 版依赖 systemd 的 unit 目录与 scriptlet 宏
Requires(post):    systemd
Requires(preun):   systemd
Requires(postun):  systemd

%global _unitdir /usr/lib/systemd/system
%global _cfgdir /etc/storagecheck
%global _datadir /var/lib/storagecheck

%description
StorageCheck 周期性检查本机各挂载点剩余空间，低于阈值时发送中文 HTML 告警邮件，
恢复时可选发送恢复通知。安装即为 systemd 服务；配置通过 `sudo storagecheck`
打开的终端拟态界面完成，无需图形环境。

%prep
# 无源码压缩包，跳过默认解压（否则会找 storagecheck-1.1.0.tar.gz）

%build
# 二进制由 build-linux.sh 交叉编译好后放进 SOURCES，这里不重复编译

%install
rm -rf %{buildroot}
install -d -m 0755 %{buildroot}/usr/bin
install -d -m 0755 %{buildroot}%{_unitdir}
install -d -m 0700 %{buildroot}%{_cfgdir}
install -d -m 0700 %{buildroot}%{_datadir}

install -m 0755 %{SOURCE0} %{buildroot}/usr/bin/storagecheck
install -m 0644 %{SOURCE1} %{buildroot}%{_unitdir}/storagecheck.service
# noreplace：升级不能覆盖管理员改过的阈值与收件人
install -m 0600 %{SOURCE2} %{buildroot}%{_cfgdir}/config.json

%files
# macOS 的 uid/gid 会跟 RHEL 对不上，每个文件都显式声明属主
%defattr(644,root,root,-)
%attr(0755,root,root) /usr/bin/storagecheck
%attr(0644,root,root) %{_unitdir}/storagecheck.service
%dir %attr(0700,root,root) %{_cfgdir}
%config(noreplace) %attr(0600,root,root) %{_cfgdir}/config.json
%dir %attr(0700,root,root) %{_datadir}

%post
# 安装完即运行：用户要的是 dnf install 之后不用手工启动
if [ -x %{_unitdir}/../systemd/systemd ] || [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || :
  systemctl enable --now storagecheck.service >/dev/null 2>&1 || :
fi

%preun
# 只在真正卸载（$1=0）时停服，升级时保持运行
if [ $1 -eq 0 ] && [ -d /run/systemd/system ]; then
  systemctl disable --now storagecheck.service >/dev/null 2>&1 || :
fi

%posttrans
# 升级后 unit 与二进制都换了，重启一次让新版本生效
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || :
  systemctl try-restart storagecheck.service >/dev/null 2>&1 || :
fi

%changelog
* Wed Oct 14 2026 yeunglw - 1.1.0-1
- 首个 Linux 版：statfs 采集、systemd 服务、终端拟态配置界面
