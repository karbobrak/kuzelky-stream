// mkicon writes the Windows resource with the application icon as a COFF object (.syso), which
// the Go linker adds to KuzelkyStream.exe automatically. No external tools (windres, go-winres).
//
//	go run ./cmd/mkicon -o cmd/kuzelkystream/rsrc_windows_amd64.syso -png docs/ikona.png
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"image/png"
	"log"
	"os"

	"kuzelkystream/internal/tray"
)

var sizes = []int{16, 20, 24, 32, 40, 48, 64, 128, 256}

func main() {
	out := flag.String("o", "cmd/kuzelkystream/rsrc_windows_amd64.syso", "výstupní .syso")
	pngOut := flag.String("png", "", "uložit i obrázek 256×256 (nepovinné)")
	flag.Parse()

	var images [][]byte
	for _, sz := range sizes {
		var b bytes.Buffer
		if err := png.Encode(&b, tray.AppImage(sz)); err != nil {
			log.Fatal(err)
		}
		images = append(images, b.Bytes())
	}
	if *pngOut != "" {
		if err := os.WriteFile(*pngOut, images[len(images)-1], 0o644); err != nil {
			log.Fatal(err)
		}
	}
	if err := os.WriteFile(*out, syso(images), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("ikona: %s (%d velikostí)", *out, len(images))
}

const (
	rtIcon      = 3
	rtGroupIcon = 14
	langNeutral = 0
	relAddr32NB = 3 // IMAGE_REL_AMD64_ADDR32NB: RVA of the target
)

// syso builds a COFF object with one .rsrc section holding RT_ICON entries 1..n and the
// RT_GROUP_ICON "1" that Windows Explorer uses as the exe icon.
func syso(images [][]byte) []byte {
	le := binary.LittleEndian
	group := new(bytes.Buffer)
	_ = binary.Write(group, le, [3]uint16{0, 1, uint16(len(images))})
	for i, img := range images {
		dim := byte(sizes[i])
		if sizes[i] >= 256 {
			dim = 0
		}
		group.Write([]byte{dim, dim, 0, 0})
		_ = binary.Write(group, le, [2]uint16{1, 32})
		_ = binary.Write(group, le, uint32(len(img)))
		_ = binary.Write(group, le, uint16(i+1))
	}

	// Layout of the section: root dir, type dirs, name dirs (one per resource), data entries, data.
	type res struct {
		typ, id int
		data    []byte
	}
	var all []res
	for i, img := range images {
		all = append(all, res{rtIcon, i + 1, img})
	}
	all = append(all, res{rtGroupIcon, 1, group.Bytes()})

	dirSize := func(entries int) int { return 16 + 8*entries }
	rootOff := 0
	iconTypeOff := rootOff + dirSize(2)
	groupTypeOff := iconTypeOff + dirSize(len(images))
	langOff := groupTypeOff + dirSize(1) // one language dir per resource
	dataEntryOff := langOff + len(all)*dirSize(1)
	dataOff := dataEntryOff + len(all)*16

	sec := new(bytes.Buffer)
	var relocs []uint32 // offsets of OffsetToData fields that need the section RVA added
	dir := func(entries [][2]uint32) {
		_ = binary.Write(sec, le, [2]uint32{0, 0})                    // characteristics, time stamp
		_ = binary.Write(sec, le, [2]uint16{0, 0})                    // version
		_ = binary.Write(sec, le, [2]uint16{0, uint16(len(entries))}) // named entries, ID entries
		for _, e := range entries {
			_ = binary.Write(sec, le, e)
		}
	}
	const subdir = 0x80000000
	// Root: RT_ICON, RT_GROUP_ICON (IDs ascending).
	dir([][2]uint32{{rtIcon, subdir | uint32(iconTypeOff)}, {rtGroupIcon, subdir | uint32(groupTypeOff)}})
	// RT_ICON: ids 1..n → language dirs.
	var icons [][2]uint32
	for i := range images {
		icons = append(icons, [2]uint32{uint32(i + 1), subdir | uint32(langOff+i*dirSize(1))})
	}
	dir(icons)
	// RT_GROUP_ICON: id 1.
	dir([][2]uint32{{1, subdir | uint32(langOff+len(images)*dirSize(1))}})
	// Language dirs → data entries.
	for i := range all {
		dir([][2]uint32{{langNeutral, uint32(dataEntryOff + i*16)}})
	}
	// Data entries; OffsetToData is an RVA, so it gets a relocation against the section.
	pos := dataOff
	var dataPos []int
	for _, r := range all {
		dataPos = append(dataPos, pos)
		pos += (len(r.data) + 7) &^ 7
	}
	for i, r := range all {
		relocs = append(relocs, uint32(sec.Len()))
		_ = binary.Write(sec, le, [4]uint32{uint32(dataPos[i]), uint32(len(r.data)), 0, 0})
	}
	for _, r := range all {
		sec.Write(r.data)
		for sec.Len()%8 != 0 {
			sec.WriteByte(0)
		}
	}

	// COFF file: header, section header, raw data, relocations, symbol table, string table.
	const fileHdr, secHdr = 20, 40
	rawOff := fileHdr + secHdr
	relocOff := rawOff + sec.Len()
	symOff := relocOff + len(relocs)*10

	f := new(bytes.Buffer)
	_ = binary.Write(f, le, uint16(0x8664)) // AMD64
	_ = binary.Write(f, le, uint16(1))      // sections
	_ = binary.Write(f, le, uint32(0))      // time stamp
	_ = binary.Write(f, le, uint32(symOff))
	_ = binary.Write(f, le, uint32(1)) // symbols
	_ = binary.Write(f, le, uint16(0)) // optional header size
	_ = binary.Write(f, le, uint16(0)) // characteristics

	f.WriteString(".rsrc\x00\x00\x00")
	_ = binary.Write(f, le, [4]uint32{0, 0, uint32(sec.Len()), uint32(rawOff)})
	_ = binary.Write(f, le, [2]uint32{uint32(relocOff), 0})
	_ = binary.Write(f, le, [2]uint16{uint16(len(relocs)), 0})
	_ = binary.Write(f, le, uint32(0x40000040)) // initialized data, readable

	f.Write(sec.Bytes())
	for _, r := range relocs {
		_ = binary.Write(f, le, r)         // where
		_ = binary.Write(f, le, uint32(0)) // symbol 0 (.rsrc)
		_ = binary.Write(f, le, uint16(relAddr32NB))
	}
	// Symbol ".rsrc": value 0, section 1, type 0, storage class STATIC, no aux records.
	f.WriteString(".rsrc\x00\x00\x00")
	_ = binary.Write(f, le, uint32(0))
	_ = binary.Write(f, le, int16(1))
	_ = binary.Write(f, le, uint16(0))
	f.Write([]byte{3, 0})
	_ = binary.Write(f, le, uint32(4)) // empty string table
	return f.Bytes()
}
