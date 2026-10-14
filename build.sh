#!/usr/bin/env bash
# 交叉编译 StorageCheck：Windows（exe + NSIS 安装包）、Linux（静态二进制 + RPM）
# 用法：./build.sh <版本> [windows|linux|rpm|all]
#   不带第二个参数时保持历史行为 —— 只打 Windows 包
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:-1.0.0}"
TARGET="${2:-windows}"
mkdir -p dist

build_windows=0
build_linux=0
case "$TARGET" in
  windows) build_windows=1 ;;
  linux)   build_linux=1 ;;
  rpm)     build_linux=1 ;;
  all)     build_windows=1; build_linux=1 ;;
  *) echo "未知目标: $TARGET（可用 windows|linux|rpm|all）" >&2; exit 2 ;;
esac

if [ "$build_windows" = 1 ]; then
# 1. Windows 资源（exe 图标/版本信息/manifest）随版本号重新生成
go run ./tools/genres "$VERSION"

# 2. 安装包图标 (.ico)
go run ./tools/genico build/icon.ico

# 3. 交叉编译 GUI exe（amd64 + arm64）
for ARCH in amd64 arm64; do
  GOOS=windows GOARCH=$ARCH CGO_ENABLED=0 go build \
    -ldflags "-s -w -H windowsgui -X main.version=$VERSION" \
    -o "dist/StorageCheck-${VERSION}-win-${ARCH}.exe" ./cmd/storagecheck
done

# 4. NSIS 安装包（makensis 来自 brew install nsis）
# makensis 在非交互环境下需要显式 UTF-8 locale，否则中文脚本报 Bad text encoding
export LC_ALL=en_US.UTF-8 LANG=en_US.UTF-8
if command -v makensis >/dev/null; then
  for ARCH in amd64 arm64; do
    makensis -DVERSION="$VERSION" -DARCH="$ARCH" build/installer.nsi | tail -1
  done
else
  echo "警告: 未找到 makensis（brew install nsis），跳过安装包" >&2
fi
fi # build_windows

if [ "$build_linux" = 1 ]; then
  # build-linux.sh 自带 GOOS=linux go vet 守卫（标签漏写会在那里报错）
  if [ "$TARGET" = rpm ]; then
    ./build-linux.sh "$VERSION"
  else
    ./build-linux.sh "$VERSION" --bins-only
  fi
fi

ls -la dist/
