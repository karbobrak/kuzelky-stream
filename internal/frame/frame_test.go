package frame

import (
	"image"
	"image/color"
	"testing"
)

func TestBlendOpaqueWhiteAndTransparent(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	// Left half opaque white, right half transparent.
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	f := New(8, 4)
	f.Black()
	Blend(f, Prepare(img))
	y, u, v := f.Planes()
	if y[0] != 235 || y[7] != 16 {
		t.Fatalf("luma: white=%d (want 235), untouched=%d (want 16)", y[0], y[7])
	}
	if u[0] != 128 || v[0] != 128 {
		t.Fatalf("white chroma = %d,%d, want 128,128", u[0], v[0])
	}
}

func TestBlendHalfAlpha(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.SetRGBA(x, y, color.RGBA{128, 128, 128, 128}) // premultiplied 50 % white
		}
	}
	f := New(2, 2)
	f.Black()
	Blend(f, Prepare(img))
	y, _, _ := f.Planes()
	// 50 % white over black ≈ middle of 16..235.
	if y[0] < 122 || y[0] > 130 {
		t.Fatalf("half-alpha luma = %d, want ≈126", y[0])
	}
}

func TestEmpty(t *testing.T) {
	if !Prepare(image.NewRGBA(image.Rect(0, 0, 4, 4))).Empty() {
		t.Fatal("transparent image should be empty")
	}
}
