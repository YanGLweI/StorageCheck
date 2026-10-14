#!/usr/bin/env bash
# Linux 版构建：交叉编译静态二进制 + 打 RPM（RHEL 9/10）。
# 用法：
#   ./build-linux.sh 1.1.0                 # 二进制 + el9/el10 × x86_64/aarch64
#   ./build-linux.sh 1.1.0 --bins-only     # 只出二进制
#   ./build-linux.sh 1.1.0 --el=9          # 只打 el9 标签
#   ./build-linux.sh 1.1.0 --in-container  # 用 ubi10 容器里的 rpmbuild 打包
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:?用法: ./build-linux.sh <版本> [选项]}"; shift || true
BINS_ONLY=0
IN_CONTAINER=0
ELS=(9 10)
for arg in "$@"; do
  case "$arg" in
    --bins-only) BINS_ONLY=1 ;;
    --in-container) IN_CONTAINER=1 ;;
    --el=*) ELS=("${arg#--el=}") ;;
    *) echo "未知选项: $arg" >&2; exit 2 ;;
  esac
done

DIST="dist"
STAGE="$DIST/rpm"
mkdir -p "$DIST"

# 标签守卫：漏写 //go:build 的文件会在这一步暴露，而不是等装到机器上才发现
echo "==> go vet (GOOS=linux)"
GOOS=linux go vet ./...

# Go 与 RPM 的架构命名不同，在这里一次翻译完
rpmarch_of() {
  case "$1" in
    amd64) echo x86_64 ;;
    arm64) echo aarch64 ;;
  esac
}

for GOARCH in amd64 arm64; do
  RPMARCH=$(rpmarch_of "$GOARCH")
  echo "==> 编译 linux/$GOARCH"
  BIN="$DIST/storagecheck-$VERSION-linux-$GOARCH"
  CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build \
    -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$BIN" ./cmd/storagecheck
  file "$BIN" | cut -c1-110
  if [ "$BINS_ONLY" = 1 ]; then continue; fi

  # 同一个二进制复制进多套顶层目录，只改 Release 里的 el 标签
  for EL in "${ELS[@]}"; do
    # rpmbuild 需要一套源码树布局：把产物与配置摆进 SOURCES 再喂给 spec
    ROOT="$STAGE/$RPMARCH-el$EL"
    rm -rf "$ROOT"
    mkdir -p "$ROOT"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}
    cp "$BIN" "$ROOT/SOURCES/storagecheck"
    cp build/storagecheck.service build/config.default.json "$ROOT/SOURCES/"
    cp build/storagecheck.spec "$ROOT/SPECS/"

    if [ "$IN_CONTAINER" = 1 ]; then
      # macOS 上的 rpmbuild 不是上游支持组合；容器路径保证宏集与真机一致
      docker run --rm -v "$PWD:/work" -w /work registry.access.redhat.com/ubi10:latest \
        bash -c "dnf -y install rpm-build >/dev/null &&
          rpmbuild --define '_topdir /work/$ROOT' --define '_el $EL' --define '_version $VERSION' \
            --target $RPMARCH-redhat-linux -bb /work/build/storagecheck.spec &&
          cp -f $ROOT/RPMS/*/*.rpm /work/$DIST/"
    else
      LOG="$ROOT/rpmbuild.log"
      if ! rpmbuild --define "_topdir $PWD/$ROOT" --define "_el $EL" --define "_version $VERSION" \
          --target "$RPMARCH-redhat-linux" -bb build/storagecheck.spec > "$LOG" 2>&1; then
        echo "==> rpmbuild 失败（$RPMARCH el$EL），最后 20 行：" >&2
        tail -20 "$LOG" >&2
        exit 1
      fi
      grep -E "^(已写至|RPM 构建警告|错误)" "$LOG" || true
      cp -f "$ROOT"/RPMS/*/*.rpm "$DIST/"
    fi
  done
done

echo "==> 产物"
ls -lh "$DIST"/storagecheck-"$VERSION"-linux-* 2>/dev/null || true
ls -lh "$DIST"/storagecheck-"$VERSION"-*.rpm 2>/dev/null || echo "（未生成 RPM）"
