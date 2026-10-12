package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"

	"storagecheck/internal/appicon"
)

// genres 生成 Windows 资源文件（exe 图标 + 版本信息 + manifest），
// 输出 cmd/storagecheck/rsrc_windows_{amd64,arm64}.syso，go build 自动链接。
// 用法: go run ./tools/genres 1.0.0
func main() {
	ver := [4]uint16{1, 0, 0, 0}
	if len(os.Args) > 1 {
		parts := strings.Split(os.Args[1], ".")
		for i := range parts {
			if i > 3 {
				break
			}
			n, _ := strconv.Atoi(parts[i])
			ver[i] = uint16(n)
		}
	}

	img := appicon.DrawIcon(256, false)
	icon, err := winres.NewIconFromResizedImage(img, nil)
	if err != nil {
		panic(err)
	}

	rs := winres.ResourceSet{}
	if err := rs.SetIcon(winres.ID(3), icon); err != nil {
		panic(err)
	}

	vs := fmt.Sprintf("%d.%d.%d.%d", ver[0], ver[1], ver[2], ver[3])
	vi := version.Info{FileVersion: ver, ProductVersion: ver}
	vi.SetFileVersion(vs)
	vi.SetProductVersion(vs)
	_ = vi.Set(0, version.ProductName, "StorageCheck")
	_ = vi.Set(0, version.FileDescription, "StorageCheck 磁盘空间监控告警")
	_ = vi.Set(0, version.CompanyName, "StorageCheck")
	_ = vi.Set(0, version.LegalCopyright, "© 2026 StorageCheck")
	rs.SetVersionInfo(vi)

	rs.SetManifest(winres.AppManifest{
		ExecutionLevel: winres.AsInvoker,
		DPIAwareness:   winres.DPIPerMonitorV2,
	})

	for _, arch := range []struct {
		name string
		a    winres.Arch
	}{{"amd64", winres.ArchAMD64}, {"arm64", winres.ArchARM64}} {
		f, err := os.Create("cmd/storagecheck/rsrc_windows_" + arch.name + ".syso")
		if err != nil {
			panic(err)
		}
		if err := rs.WriteObject(f, arch.a); err != nil {
			f.Close()
			panic(err)
		}
		f.Close()
		fmt.Println("wrote cmd/storagecheck/rsrc_windows_" + arch.name + ".syso")
	}
}
