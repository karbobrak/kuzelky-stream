package overlay

import (
	"image"
	"image/draw"

	"kuzelkystream/internal/match"
)

// Block renders a single movable block ("lista" = score bar, "zprava" = message) cropped to its
// visible area, for the drag-and-drop layout editor. An empty message gets a sample text.
func (r *Renderer) Block(v ScoreView, which string) *image.RGBA {
	img := r.Blank()
	switch which {
	case "zprava":
		if v.Match.Message == "" {
			v.Match.Message = "Přestávka"
		}
		r.message(img, v)
	default:
		r.scoreBar(img, v)
	}
	return cropToContent(img)
}

func cropToContent(img *image.RGBA) *image.RGBA {
	b := img.Rect
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, -1, -1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[(y-b.Min.Y)*img.Stride:]
		for x := b.Min.X; x < b.Max.X; x++ {
			if row[4*(x-b.Min.X)+3] != 0 {
				minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			}
		}
	}
	if maxX < 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	out := image.NewRGBA(image.Rect(0, 0, maxX-minX+1, maxY-minY+1))
	draw.Draw(out, out.Rect, img, image.Pt(minX, minY), draw.Src)
	return out
}

// SampleMatch is shown in the layout editor when no match is open.
func SampleMatch(r match.Rules) match.Match {
	m := match.New(r)
	m.Home, m.Away = match.Team{Name: "Domácí"}, match.Team{Name: "Hosté"}
	for i := range m.Duels {
		m.Duels[i].HomePlayer, m.Duels[i].AwayPlayer = "Domácí hráč", "Hostující hráč"
	}
	v := func(n int) *int { return &n }
	for _, i := range r.PartDuels(1) {
		if len(m.Duels[i].Lanes) > 1 {
			m.Duels[i].Lanes[0] = match.Pair{Home: v(150), Away: v(143)}
			m.Duels[i].Lanes[1] = match.Pair{Home: v(139), Away: v(147)}
		}
		m.Duels[i].Live = match.Pair{Home: v(70), Away: v(68)}
	}
	return m
}
