package appicon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
)

// MakeICO 打包多尺寸 ICO（Vista+ 内嵌 PNG）。
// Windows 托盘走 LoadImage(IMAGE_ICON)，只认 ICO 容器，传裸 PNG 会显示空白图标。
func MakeICO(alert bool, sizes ...int) []byte {
	if len(sizes) == 0 {
		sizes = []int{16, 24, 32, 48}
	}
	images := make([]image.Image, 0, len(sizes))
	for _, s := range sizes {
		images = append(images, DrawIcon(s, alert))
	}
	return EncodeICO(images)
}

// EncodeICO 把若干图像打包成单个 ICO 文件，每个目录项对应一张 PNG。
func EncodeICO(images []image.Image) []byte {
	type entry struct {
		width int
		data  []byte
	}
	entries := make([]entry, 0, len(images))
	for _, img := range images {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			continue
		}
		entries = append(entries, entry{img.Bounds().Dx(), buf.Bytes()})
	}

	out := new(bytes.Buffer)
	_ = binary.Write(out, binary.LittleEndian, struct{ Reserved, Type, Count uint16 }{0, 1, uint16(len(entries))})

	offset := uint32(6 + 16*len(entries))
	for _, e := range entries {
		ent := struct {
			Width, Height, ColorCount, Reserved uint8
			Planes, BitCount                    uint16
			BytesInRes, ImageOffset             uint32
		}{
			Planes:      1,
			BitCount:    32,
			BytesInRes:  uint32(len(e.data)),
			ImageOffset: offset,
		}
		if e.width >= 256 {
			ent.Width, ent.Height = 0, 0 // 目录项用 0 表示 256
		} else {
			ent.Width, ent.Height = uint8(e.width), uint8(e.width)
		}
		_ = binary.Write(out, binary.LittleEndian, &ent)
		offset += uint32(len(e.data))
	}
	for _, e := range entries {
		out.Write(e.data)
	}
	return out.Bytes()
}
