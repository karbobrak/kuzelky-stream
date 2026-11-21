// Package tray shows the app's icon in the Windows notification area (next to the clock).
package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Icon colors for the states.
var (
	colIdle = color.RGBA{0x6b, 0x76, 0x88, 0xff} // grey: not streaming
	colLive = color.RGBA{0xd9, 0x30, 0x30, 0xff} // red: on air
	colWarn = color.RGBA{0xe0, 0x9a, 0x20, 0xff} // amber: reconnecting / replacement picture
)

// pin draws a simple white skittle (kuželka) on a colored circle, anti-aliased by supersampling.
func pin(size int, bg color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	const ss = 4 // samples per axis
	s := float64(size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var inCircle, inPin int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					// Normalized coordinates -1..1.
					u := (float64(x)+(float64(sx)+0.5)/ss)/s*2 - 1
					v := (float64(y)+(float64(sy)+0.5)/ss)/s*2 - 1
					if u*u+v*v <= 0.96 {
						inCircle++
						if inSkittle(u, v) {
							inPin++
						}
					}
				}
			}
			n := float64(ss * ss)
			ca, pa := float64(inCircle)/n, float64(inPin)/n
			// White pin over the colored disc; premultiplied alpha.
			r := float64(bg.R)*(ca-pa) + 255*pa
			g := float64(bg.G)*(ca-pa) + 255*pa
			b := float64(bg.B)*(ca-pa) + 255*pa
			img.SetRGBA(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(255 * ca)})
		}
	}
	return img
}

// inSkittle: head circle, thin neck, wide belly – roughly the outline of a nine-pin skittle.
func inSkittle(u, v float64) bool {
	if v < -0.78 || v > 0.72 {
		return false
	}
	if math.Hypot(u, v+0.56) < 0.21 { // head
		return true
	}
	// Half-width along the height: nothing above the head's center, narrow neck, belly widest
	// at v≈0.3, then narrowing to the flat foot.
	var w float64
	switch {
	case v < -0.56:
		return false
	case v < -0.25:
		w = 0.09
	default:
		t := math.Min((v+0.25)/0.97, 1)
		w = 0.09 + 0.3*math.Sin(t*math.Pi*0.85)
	}
	return math.Abs(u) < w
}

// ico packs PNG images into a Windows .ico file (PNG entries are supported since Vista).
func ico(sizes ...int) func(color.RGBA) []byte {
	return func(bg color.RGBA) []byte {
		var pngs [][]byte
		for _, sz := range sizes {
			var b bytes.Buffer
			_ = png.Encode(&b, pin(sz, bg))
			pngs = append(pngs, b.Bytes())
		}
		var out bytes.Buffer
		le := binary.LittleEndian
		_ = binary.Write(&out, le, [3]uint16{0, 1, uint16(len(sizes))})
		offset := 6 + 16*len(sizes)
		for i, sz := range sizes {
			dim := byte(sz)
			if sz >= 256 {
				dim = 0
			}
			out.Write([]byte{dim, dim, 0, 0})
			_ = binary.Write(&out, le, [2]uint16{1, 32})
			_ = binary.Write(&out, le, [2]uint32{uint32(len(pngs[i])), uint32(offset)})
			offset += len(pngs[i])
		}
		for _, p := range pngs {
			out.Write(p)
		}
		return out.Bytes()
	}
}

var makeIcon = ico(16, 24, 32, 48)

// AppImage is the application icon (exe, shortcuts): a white skittle on a red rounded square,
// the "on air" red of the tray icon. Not the YouTube logo – that may not be used as an app icon.
func AppImage(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	const ss = 4
	s := float64(size)
	bg := colLive
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var inBox, inPin int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					u := (float64(x)+(float64(sx)+0.5)/ss)/s*2 - 1
					v := (float64(y)+(float64(sy)+0.5)/ss)/s*2 - 1
					if inRoundedSquare(u, v, 0.94, 0.3) {
						inBox++
						// The skittle a bit larger than in the round tray icon.
						if inSkittle(u/1.15, v/1.15+0.02) {
							inPin++
						}
					}
				}
			}
			n := float64(ss * ss)
			ba, pa := float64(inBox)/n, float64(inPin)/n
			r := float64(bg.R)*(ba-pa) + 255*pa
			g := float64(bg.G)*(ba-pa) + 255*pa
			b := float64(bg.B)*(ba-pa) + 255*pa
			img.SetRGBA(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(255 * ba)})
		}
	}
	return img
}

// inRoundedSquare: square of half-size h with corner radius r (normalized coordinates).
func inRoundedSquare(u, v, h, r float64) bool {
	du, dv := math.Abs(u)-(h-r), math.Abs(v)-(h-r)
	if du <= 0 || dv <= 0 {
		return math.Abs(u) <= h && math.Abs(v) <= h
	}
	return du*du+dv*dv <= r*r
}
