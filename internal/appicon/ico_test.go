package appicon

import (
	"bytes"
	"encoding/binary"
	"testing"
)

type icoHeader struct{ Reserved, Type, Count uint16 }

type icoEntry struct {
	W, H, Colors, Reserved  uint8
	Planes, BitCount        uint16
	BytesInRes, ImageOffset uint32
}

func TestMakeICO(t *testing.T) {
	sizes := []int{16, 24, 32, 256}
	ico := MakeICO(true, sizes...)
	if len(ico) < 6+16*len(sizes) {
		t.Fatalf("ICO 过短: %d 字节", len(ico))
	}

	var hdr icoHeader
	if err := binary.Read(bytes.NewReader(ico[:6]), binary.LittleEndian, &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr.Reserved != 0 || hdr.Type != 1 || hdr.Count != uint16(len(sizes)) {
		t.Fatalf("头部错误: %+v", hdr)
	}

	offset := uint32(6 + 16*hdr.Count)
	for i := 0; i < int(hdr.Count); i++ {
		var ent icoEntry
		if err := binary.Read(bytes.NewReader(ico[6+16*i:]), binary.LittleEndian, &ent); err != nil {
			t.Fatal(err)
		}
		want := byte(sizes[i])
		if sizes[i] >= 256 {
			want = 0 // 目录项用 0 表示 256
		}
		if ent.W != want || ent.H != want {
			t.Fatalf("条目 %d 尺寸 %d/%d，期望 %d/%d", i, ent.W, ent.H, want, want)
		}
		if ent.ImageOffset != offset {
			t.Fatalf("条目 %d 偏移 %d，期望 %d", i, ent.ImageOffset, offset)
		}
		blob := ico[ent.ImageOffset:]
		if !bytes.HasPrefix(blob, []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatalf("条目 %d 不是 PNG 数据", i)
		}
		if uint32(len(blob)) < ent.BytesInRes {
			t.Fatalf("条目 %d 数据截断", i)
		}
		offset += ent.BytesInRes
	}
	if int(offset) != len(ico) {
		t.Fatalf("末尾偏移 %d != 文件长度 %d", offset, len(ico))
	}
}

func TestAlertIconDiffersFromNormal(t *testing.T) {
	normal, alert := MakeICO(false), MakeICO(true)
	if len(normal) == 0 || len(alert) == 0 {
		t.Fatal("ICO 编码失败")
	}
	if bytes.Equal(normal, alert) {
		t.Fatal("告警图标与普通图标相同")
	}
}
