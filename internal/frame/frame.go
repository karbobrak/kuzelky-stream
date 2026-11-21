// Package frame holds raw I420 (yuv420p) video frames and blends RGBA graphics into them.
package frame

import "image"

// Frame is one I420 picture: the Y plane (W×H) followed by U and V (W/2 × H/2 each).
type Frame struct {
	W, H int
	Buf  []byte
}

func Size(w, h int) int { return w*h + 2*(w/2)*(h/2) }

func New(w, h int) *Frame { return &Frame{W: w, H: h, Buf: make([]byte, Size(w, h))} }

func (f *Frame) Planes() (y, u, v []byte) {
	ys := f.W * f.H
	cs := (f.W / 2) * (f.H / 2)
	return f.Buf[:ys], f.Buf[ys : ys+cs], f.Buf[ys+cs : ys+2*cs]
}

// Black fills the frame with video black (limited range).
func (f *Frame) Black() {
	y, u, v := f.Planes()
	fill(y, 16)
	fill(u, 128)
	fill(v, 128)
}

func fill(b []byte, val byte) {
	for i := range b {
		b[i] = val
	}
}

// Prepared is an RGBA image converted once to premultiplied YUV + alpha, ready to be blended
// into many frames cheaply. Colors use BT.709 limited range (HD video).
type Prepared struct {
	w, h   int
	bounds image.Rectangle // area with any non-transparent pixel, aligned to even coordinates
	y, a   []uint8         // full resolution: premultiplied luma, alpha
	u, v   []uint8         // half resolution: premultiplied chroma
	ca     []uint8         // half resolution: alpha averaged over 2×2
}

// Empty reports whether the image is fully transparent (blending would change nothing).
func (p *Prepared) Empty() bool { return p == nil || p.bounds.Empty() }

// Prepare converts img (Go's image.RGBA is alpha-premultiplied) for Blend.
// img must have the frame size and even dimensions.
func Prepare(img *image.RGBA) *Prepared {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	p := &Prepared{
		w: w, h: h,
		y: make([]uint8, w*h), a: make([]uint8, w*h),
		u: make([]uint8, (w/2)*(h/2)), v: make([]uint8, (w/2)*(h/2)), ca: make([]uint8, (w/2)*(h/2)),
	}
	minX, minY, maxX, maxY := w, h, -1, -1
	for py := 0; py < h; py++ {
		row := img.Pix[py*img.Stride:]
		for px := 0; px < w; px++ {
			r, g, b, a := float32(row[4*px]), float32(row[4*px+1]), float32(row[4*px+2]), row[4*px+3]
			if a == 0 {
				continue
			}
			minX, minY, maxX, maxY = min(minX, px), min(minY, py), max(maxX, px), max(maxY, py)
			// For premultiplied input: a·Y(straight) = 16·a/255 + k·rgb_premultiplied.
			af := float32(a) / 255
			p.y[py*w+px] = clamp(16*af + 0.18259*r + 0.61423*g + 0.06201*b)
			p.a[py*w+px] = a
		}
	}
	if maxX < 0 {
		return p // fully transparent
	}
	p.bounds = image.Rect(minX&^1, minY&^1, (maxX+2)&^1, (maxY+2)&^1)
	cw := w / 2
	for cy := p.bounds.Min.Y / 2; cy < p.bounds.Max.Y/2; cy++ {
		for cx := p.bounds.Min.X / 2; cx < p.bounds.Max.X/2; cx++ {
			var su, sv, sa float32
			for dy := 0; dy < 2; dy++ {
				row := img.Pix[(2*cy+dy)*img.Stride:]
				for dx := 0; dx < 2; dx++ {
					i := 4 * (2*cx + dx)
					r, g, b, a := float32(row[i]), float32(row[i+1]), float32(row[i+2]), float32(row[i+3])
					af := a / 255
					su += 128*af - 0.10067*r - 0.33857*g + 0.43922*b
					sv += 128*af + 0.43922*r - 0.39894*g - 0.04027*b
					sa += a
				}
			}
			p.u[cy*cw+cx] = clamp(su / 4)
			p.v[cy*cw+cx] = clamp(sv / 4)
			p.ca[cy*cw+cx] = clamp(sa / 4)
		}
	}
	return p
}

func clamp(f float32) uint8 {
	switch {
	case f <= 0:
		return 0
	case f >= 255:
		return 255
	}
	return uint8(f + 0.5)
}

// Blend draws p over dst (same size), touching only p's non-transparent area.
func Blend(dst *Frame, p *Prepared) {
	if p.Empty() || dst.W != p.w || dst.H != p.h {
		return
	}
	y, u, v := dst.Planes()
	w, b := p.w, p.bounds
	for py := b.Min.Y; py < b.Max.Y; py++ {
		for i := py*w + b.Min.X; i < py*w+b.Max.X; i++ {
			if a := p.a[i]; a != 0 {
				y[i] = over(p.y[i], y[i], uint32(255-a))
			}
		}
	}
	cw := w / 2
	for cy := b.Min.Y / 2; cy < b.Max.Y/2; cy++ {
		for i := cy*cw + b.Min.X/2; i < cy*cw+b.Max.X/2; i++ {
			if a := p.ca[i]; a != 0 {
				inv := uint32(255 - a)
				u[i] = over(p.u[i], u[i], inv)
				v[i] = over(p.v[i], v[i], inv)
			}
		}
	}
}

// over is premultiplied "source over": src + dst·(1-alpha), with inv = 255-alpha.
// Clamped, because rounding can push the sum just past 255.
func over(src, dst uint8, inv uint32) uint8 {
	s := uint32(src) + (uint32(dst)*inv+127)/255
	if s > 255 {
		return 255
	}
	return uint8(s)
}
