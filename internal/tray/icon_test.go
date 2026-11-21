package tray

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestIconFormat(t *testing.T) {
	data := makeIcon(colLive)
	var hdr [3]uint16
	if err := binary.Read(bytes.NewReader(data), binary.LittleEndian, &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr != [3]uint16{0, 1, 4} {
		t.Fatalf("ICO header = %v", hdr)
	}
	// Optional: KUZELKY_ICON_OUT=<dir> writes big previews for a visual check.
	if dir := os.Getenv("KUZELKY_ICON_OUT"); dir != "" {
		for name, c := range map[string]color.RGBA{"idle": colIdle, "live": colLive, "warn": colWarn} {
			f, _ := os.Create(filepath.Join(dir, "icon-"+name+".png"))
			_ = png.Encode(f, pin(128, c))
			f.Close()
		}
	}
}
