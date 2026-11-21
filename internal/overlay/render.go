// Package overlay renders the score graphics as a transparent RGBA frame.
package overlay

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Renderer draws frames of a fixed size. Not safe for concurrent use.
type Renderer struct {
	w, h  int
	big   font.Face
	small font.Face
}

func NewRenderer(w, h int) (*Renderer, error) {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	scale := float64(h) / 720
	big, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 34 * scale, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	small, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 22 * scale, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	return &Renderer{w: w, h: h, big: big, small: small}, nil
}

// Blank returns a fully transparent frame (graphics switched off).
func (r *Renderer) Blank() *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, r.w, r.h))
}

// Slate is the opaque full-screen picture shown while the camera is unavailable.
// The score graphics are still drawn over it.
func (r *Renderer) Slate(title, subtitle string) *image.RGBA {
	img := r.Blank()
	draw.Draw(img, img.Rect, image.NewUniform(color.RGBA{12, 22, 48, 255}), image.Point{}, draw.Src)
	s := float64(r.h) / 720
	r.textCentered(img, r.big, int(300*s), color.White, title)
	r.textCentered(img, r.small, int(350*s), color.RGBA{190, 200, 225, 255}, subtitle)
	return img
}

func (r *Renderer) textCentered(img draw.Image, face font.Face, y int, c color.Color, s string) {
	wd := font.MeasureString(face, s).Round()
	r.text(img, face, (r.w-wd)/2, y, c, s)
}

func (r *Renderer) text(img draw.Image, face font.Face, x, y int, c color.Color, s string) {
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}
	d.DrawString(s)
}
