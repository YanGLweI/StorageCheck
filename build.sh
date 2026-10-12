#!/usr/bin/env bash
# 在 macOS/Linux 上交叉编译 Windows 版 StorageCheck（含安装包，无需 Windows/Wine/Docker）
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:-1.0.0}"
mkdir -p dist

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

ls -la dist/
