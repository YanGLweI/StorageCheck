package appicon

import (
	"image"
	"image/color"
	"math"
)

// DrawIcon 程序化绘制应用图标：圆环 + 中心盘片，告警时红环 + 感叹号。
// 全部形状定义在 32x32 基准坐标系内，按像素距离做软边抗锯齿，因此任意尺寸都清晰。
func DrawIcon(size int, alert bool) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size) / 32.0
	center := float64(size) / 2

	blue := color.NRGBA{37, 99, 235, 255}
	red := color.NRGBA{220, 38, 38, 255}
	white := color.NRGBA{255, 255, 255, 255}
	ring := blue
	if alert {
		ring = red
	}

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			nx := (float64(px) - center + 0.5) / s // 基准坐标系，原点居中
			ny := (float64(py) - center + 0.5) / s
			d := math.Hypot(nx, ny)

			col := color.NRGBA{}
			var a float64
			switch {
			case d <= 7.5: // 中心盘片
				col, a = white, cov(7.5-d)
			case d >= 11 && d <= 15: // 外环
				col, a = ring, cov(math.Min(d-10.5, 15.5-d))
			default:
				continue
			}

			if alert { // 感叹号：红色竖条压在白色中心盘上
				if bar := barCover(nx, ny, size <= 24); bar > 0 {
					col, a = red, a*bar
				}
			}
			if a <= 0 {
				continue
			}
			out := col
			out.A = uint8(math.Min(255, a*255))
			img.SetNRGBA(px, py, out)
		}
	}
	return img
}

// barCover 返回感叹号（竖条 + 点）在基准坐标系下的覆盖率。
// 托盘小图标（≤24px）把笔画加粗，否则细节会在 16px 下糊成一团。
func barCover(nx, ny float64, bold bool) float64 {
	w, top, bottom, dot := 1.4, -6.0, -1.5, 3.5
	if bold {
		w, top, bottom, dot = 2.4, -6.5, 0.5, 3.2
	}
	v := within(nx, -w, w) * (within(ny, top, bottom) + within(ny, dot, 6.5))
	return math.Min(v, 1)
}

// within 返回坐标在 [lo, hi] 内的软覆盖率，边缘 1 单位渐入渐出。
func within(v, lo, hi float64) float64 {
	return cov(math.Min(v-lo, hi-v))
}

// cov 把距边缘的像素数（32 基准）映射为平滑 alpha。
func cov(edge float64) float64 {
	if edge <= 0 {
		return 0
	}
	if edge >= 1 {
		return 1
	}
	return edge * edge * (3 - 2*edge)
}
