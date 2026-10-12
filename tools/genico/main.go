package main

import (
	"image"
	"os"

	"storagecheck/internal/appicon"
)

// genico 从程序化图标生成多尺寸 ICO（Vista+ 内嵌 PNG），供 NSIS 安装包使用。
// 用法: go run ./tools/genico build/icon.ico
func main() {
	out := "build/icon.ico"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}

	var images []image.Image
	for _, s := range []int{16, 24, 32, 48, 64, 128, 256} {
		images = append(images, appicon.DrawIcon(s, false))
	}
	if err := os.WriteFile(out, appicon.EncodeICO(images), 0o644); err != nil {
		panic(err)
	}
	println("wrote", out)
}
