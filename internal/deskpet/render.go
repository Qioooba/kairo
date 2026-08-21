//go:build windows

package deskpet

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
)

// sprite 是一张已解码的精灵图（NRGBA，横排多帧，帧高 = 图高）。
type sprite struct {
	pix    []byte
	stride int
	w, h   int
}

func decodeSprite(data []byte) (*sprite, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	nrgba := toNRGBA(img)
	return &sprite{
		pix:    nrgba.Pix,
		stride: nrgba.Stride,
		w:      nrgba.Rect.Dx(),
		h:      nrgba.Rect.Dy(),
	}, nil
}

func toNRGBA(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok {
		return n
	}
	b := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// canvas 是 straight-alpha 的 BGRA 工作缓冲（自顶向下，逐行紧凑）。
type canvas struct {
	w, h int
	px   []byte
}

func newCanvas(w, h int) *canvas {
	return &canvas{w: w, h: h, px: make([]byte, w*h*4)}
}

func (c *canvas) clear() {
	for i := range c.px {
		c.px[i] = 0
	}
}

func (c *canvas) off(x, y int) int { return (y*c.w + x) * 4 }

func (c *canvas) inBounds(x, y int) bool {
	return x >= 0 && y >= 0 && x < c.w && y < c.h
}

// setOpaque 以不透明方式直接覆盖像素（用于角标 / 不透明文本）。
func (c *canvas) setOpaque(x, y int, b, g, r uint8) {
	if !c.inBounds(x, y) {
		return
	}
	i := c.off(x, y)
	c.px[i+0] = b
	c.px[i+1] = g
	c.px[i+2] = r
	c.px[i+3] = 255
}

// blend 以 straight-alpha 做 "over" 合成。
func (c *canvas) blend(x, y int, b, g, r, a uint8) {
	if !c.inBounds(x, y) {
		return
	}
	if a == 0 {
		return
	}
	if a == 255 {
		c.setOpaque(x, y, b, g, r)
		return
	}
	i := c.off(x, y)
	sa := int(a)
	da := int(c.px[i+3])
	oa := sa + da*(255-sa)/255
	if oa == 0 {
		c.px[i+0], c.px[i+1], c.px[i+2], c.px[i+3] = 0, 0, 0, 0
		return
	}
	c.px[i+0] = uint8((int(b)*sa + int(c.px[i+0])*da*(255-sa)/255) / oa)
	c.px[i+1] = uint8((int(g)*sa + int(c.px[i+1])*da*(255-sa)/255) / oa)
	c.px[i+2] = uint8((int(r)*sa + int(c.px[i+2])*da*(255-sa)/255) / oa)
	c.px[i+3] = uint8(oa)
}

func (c *canvas) fillRect(x, y, w, h int, b, g, r uint8) {
	for j := y; j < y+h; j++ {
		for k := x; k < x+w; k++ {
			c.setOpaque(k, j, b, g, r)
		}
	}
}

// fillRoundRect 简单圆角：四个角像素裁掉，角半径为 4px。
func (c *canvas) fillRoundRect(x, y, w, h int, radius int, b, g, r uint8) {
	if radius < 0 {
		radius = 0
	}
	if radius > w/2 {
		radius = w / 2
	}
	if radius > h/2 {
		radius = h / 2
	}
	for j := y; j < y+h; j++ {
		for k := x; k < x+w; k++ {
			dx := k - x
			dy := j - y
			// 左上 / 右上 / 左下 / 右下
			cx := dx
			if dx >= w-radius {
				cx = w - 1 - dx
			}
			cy := dy
			if dy >= h-radius {
				cy = h - 1 - dy
			}
			if cx < radius && cy < radius {
				rx := radius - 1 - cx
				ry := radius - 1 - cy
				if rx*rx+ry*ry > (radius-1)*(radius-1) {
					continue
				}
			}
			c.setOpaque(k, j, b, g, r)
		}
	}
}

// blitSprite 以最近邻（像素风）把第 frame 帧贴到目标矩形。
func (c *canvas) blitSprite(sp *sprite, frame, dx, dy, dw, dh int) {
	if sp == nil || sp.w == 0 || sp.h == 0 || dw <= 0 || dh <= 0 {
		return
	}
	frameSize := sp.h
	frames := sp.w / sp.h
	if frames < 1 {
		frames = 1
	}
	f := ((frame % frames) + frames) % frames
	srcX0 := f * frameSize

	for j := 0; j < dh; j++ {
		sy := dy + j
		if sy < 0 || sy >= c.h {
			continue
		}
		srcY := j * frameSize / dh
		if srcY >= frameSize {
			srcY = frameSize - 1
		}
		for k := 0; k < dw; k++ {
			sx := dx + k
			if sx < 0 || sx >= c.w {
				continue
			}
			srcX := k * frameSize / dw
			if srcX >= frameSize {
				srcX = frameSize - 1
			}
			pi := ((srcY)*sp.stride) + ((srcX0 + srcX) * 4)
			a := sp.pix[pi+3]
			if a == 0 {
				continue
			}
			c.blend(sx, sy, sp.pix[pi+2], sp.pix[pi+1], sp.pix[pi+0], a)
		}
	}
}

// drawGlyph 以 scale 倍绘制一个 5×7 字形。
func (c *canvas) drawGlyph(ch rune, x, y, scale int, b, g, r uint8) {
	rows, ok := glyphRows[ch]
	if !ok {
		rows = glyphRows[' ']
	}
	for row := 0; row < 7; row++ {
		bits := rows[row]
		for col := 0; col < 5; col++ {
			if bits&(1<<uint(4-col)) == 0 {
				continue
			}
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					c.setOpaque(x+col*scale+sx, y+row*scale+sy, b, g, r)
				}
			}
		}
	}
}

// textW 计算字符串渲染后的像素宽度。
func textW(s string, scale int) int {
	n := 0
	for range s {
		n += 5*scale + scale // 5 列 + 1 列间距
	}
	if n > 0 {
		n -= scale
	}
	return n
}

// drawText 绘制一行像素文本，可选描边（阴影）。
func (c *canvas) drawText(s string, x, y, scale int, b, g, r uint8, shadow bool) {
	runes := []rune(s)
	if shadow {
		off := 1
		if scale > 2 {
			off = scale / 2
		}
		for _, ch := range runes {
			c.drawGlyph(ch, x+off, y+off, scale, 0x2a, 0x1f, 0x12)
			x += 6 * scale
		}
		x = x - 6*scale*len(runes)
	}
	for _, ch := range runes {
		c.drawGlyph(ch, x, y, scale, b, g, r)
		x += 6 * scale
	}
}

// drawAura 在精灵周围画一圈半透明金色光环（高等级特效）。
func (c *canvas) drawAura(cx, cy, r int) {
	if r <= 0 {
		return
	}
	rOut := (r + 6) * (r + 6)
	rIn := (r - 2) * (r - 2)
	for j := cy - r - 7; j <= cy+r+7; j++ {
		for k := cx - r - 7; k <= cx+r+7; k++ {
			dd := (k-cx)*(k-cx) + (j-cy)*(j-cy)
			if dd < rIn || dd > rOut {
				continue
			}
			c.blend(k, j, 0xFF, 0xD7, 0x66, 46)
		}
	}
}

// grayDim 把不透明 BGRA 位图转为灰度并按 factor 压暗（锁定皮肤灰显用）。
func grayDim(bits []byte, factor float64) {
	for i := 0; i+4 <= len(bits); i += 4 {
		b := int(bits[i+0])
		g := int(bits[i+1])
		r := int(bits[i+2])
		lum := int(0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b))
		v := int(float64(lum) * factor)
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		bits[i+0] = uint8(v)
		bits[i+1] = uint8(v)
		bits[i+2] = uint8(v)
	}
}

func (c *canvas) premultiplyInto(dst []byte) {
	n := c.w * c.h * 4
	for i := 0; i < n; i += 4 {
		b := int(c.px[i+0])
		g := int(c.px[i+1])
		r := int(c.px[i+2])
		a := int(c.px[i+3])
		dst[i+0] = uint8(b * a / 255)
		dst[i+1] = uint8(g * a / 255)
		dst[i+2] = uint8(r * a / 255)
		dst[i+3] = uint8(a)
	}
}

// rgbaToBGRA 生成一张不透明 BGRA 位图（供面板 StretchDIBits 使用）。
// src 是 straight-alpha，先合成到 bg 上，再输出不透明 BGRA。
func compositeOnBG(sp *sprite, frame int, bgB, bgG, bgR uint8, dw, dh int) []byte {
	frameSize := sp.h
	frames := sp.w / sp.h
	if frames < 1 {
		frames = 1
	}
	f := ((frame % frames) + frames) % frames
	srcX0 := f * frameSize

	out := make([]byte, dw*dh*4)
	for j := 0; j < dh; j++ {
		srcY := j * frameSize / dh
		if srcY >= frameSize {
			srcY = frameSize - 1
		}
		for k := 0; k < dw; k++ {
			srcX := k * frameSize / dw
			if srcX >= frameSize {
				srcX = frameSize - 1
			}
			pi := srcY*sp.stride + (srcX0+srcX)*4
			a := int(sp.pix[pi+3])
			// 目标像素初始 = 背景色
			ob := int(bgB)
			og := int(bgG)
			or := int(bgR)
			if a > 0 {
				ob = (int(sp.pix[pi+2])*a + ob*(255-a)) / 255
				og = (int(sp.pix[pi+1])*a + og*(255-a)) / 255
				or = (int(sp.pix[pi+0])*a + or*(255-a)) / 255
			}
			oi := (j*dw + k) * 4
			out[oi+0] = uint8(ob) // B
			out[oi+1] = uint8(og) // G
			out[oi+2] = uint8(or) // R
			out[oi+3] = 255
		}
	}
	return out
}

