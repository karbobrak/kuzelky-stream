package overlay

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/opentype"

	"kuzelkystream/internal/match"
)

// Block placement in percent of the picture. X is the block's horizontal center; Y is its bottom
// edge for the score bar and its top edge for the message (so they stay inside the picture).
type Place struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Scale float64 `json:"meritko"` // percent, 100 = default size
}

type Layout struct {
	Bar     Place `json:"lista"`
	Message Place `json:"zprava"`
	Opacity int   `json:"pruhlednostPozadi"` // background opacity in percent
}

func DefaultLayout() Layout {
	return Layout{Bar: Place{X: 50, Y: 97, Scale: 100}, Message: Place{X: 50, Y: 3, Scale: 100}, Opacity: 85}
}

// Colors of the graphics.
var (
	cBg     = color.RGBA{12, 22, 48, 255}
	cBg2    = color.RGBA{24, 38, 72, 255}
	cAccent = color.RGBA{230, 180, 30, 255}
	cText   = color.RGBA{255, 255, 255, 255}
	cMuted  = color.RGBA{175, 188, 212, 255}
	cLive   = color.RGBA{255, 196, 70, 255}
	cWin    = color.RGBA{120, 220, 150, 255}
)

type faces struct {
	scale                    float64
	title, big, mid, small   font.Face
	bigR, midR, smallR, tiny font.Face
}

var fontBold, fontMedium *opentype.Font

func init() {
	fontBold, _ = opentype.Parse(gobold.TTF)
	fontMedium, _ = opentype.Parse(gomedium.TTF)
}

func newFaces(scale float64) faces {
	f := func(fnt *opentype.Font, size float64) font.Face {
		face, _ := opentype.NewFace(fnt, &opentype.FaceOptions{Size: size * scale, DPI: 72, Hinting: font.HintingFull})
		return face
	}
	return faces{scale: scale,
		title: f(fontBold, 40), big: f(fontBold, 26), mid: f(fontBold, 21), small: f(fontBold, 17),
		bigR: f(fontMedium, 26), midR: f(fontMedium, 21), smallR: f(fontMedium, 17), tiny: f(fontMedium, 14)}
}

// ScoreView is everything needed to draw the score graphics.
type ScoreView struct {
	Match  match.Match
	Result match.Result
	Rules  match.Rules
	Layout Layout
}

// Points formats match points the Czech way: 3, 3,5.
func Points(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	return strings.Replace(s, ".", ",", 1)
}

func pins(v int) string {
	s := strconv.Itoa(v)
	if len(s) > 3 {
		s = s[:len(s)-3] + " " + s[len(s)-3:] // Czech thousands separator (the font has no narrow space)
	}
	return s
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA, opacity int) {
	a := uint32(255 * opacity / 100)
	pc := color.RGBA{uint8(uint32(c.R) * a / 255), uint8(uint32(c.G) * a / 255), uint8(uint32(c.B) * a / 255), uint8(a)}
	draw.Draw(img, r, image.NewUniform(pc), image.Point{}, draw.Over)
}

// Score draws the graphics for the view (transparent background).
func (r *Renderer) Score(v ScoreView) *image.RGBA {
	img := r.Blank()
	m := v.Match
	switch m.Mode {
	case match.ModeResult:
		r.resultTable(img, v)
		return img
	case match.ModeMessage:
		if strings.TrimSpace(m.Message) != "" {
			r.message(img, v)
		}
	}
	r.scoreBar(img, v)
	return img
}

func (r *Renderer) facesFor(p Place) faces {
	sc := p.Scale
	if sc <= 0 {
		sc = 100
	}
	return newFaces(float64(r.h) / 720 * sc / 100)
}

func (r *Renderer) message(img *image.RGBA, v ScoreView) {
	fc := r.facesFor(v.Layout.Message)
	s := fc.scale
	text := strings.TrimSpace(v.Match.Message)
	tw := font.MeasureString(fc.big, text).Round()
	w, h := tw+int(60*s), int(52*s)
	cx := int(v.Layout.Message.X / 100 * float64(r.w))
	top := int(v.Layout.Message.Y / 100 * float64(r.h))
	rect := clampRect(image.Rect(cx-w/2, top, cx+w/2, top+h), r.w, r.h)
	fill(img, rect, cBg, v.Layout.Opacity)
	fill(img, image.Rect(rect.Min.X, rect.Max.Y-int(4*s), rect.Max.X, rect.Max.Y), cAccent, 100)
	r.textCenter(img, fc.big, rect.Min.X+rect.Dx()/2, rect.Min.Y+int(35*s), cText, text)
}

func clampRect(rc image.Rectangle, w, h int) image.Rectangle {
	if rc.Min.X < 0 {
		rc = rc.Add(image.Pt(-rc.Min.X, 0))
	}
	if rc.Max.X > w {
		rc = rc.Add(image.Pt(w-rc.Max.X, 0))
	}
	if rc.Min.Y < 0 {
		rc = rc.Add(image.Pt(0, -rc.Min.Y))
	}
	if rc.Max.Y > h {
		rc = rc.Add(image.Pt(0, h-rc.Max.Y))
	}
	return rc
}

// scoreBar: header (teams, points, pins) + one row per duel of the current part.
func (r *Renderer) scoreBar(img *image.RGBA, v ScoreView) {
	fc := r.facesFor(v.Layout.Bar)
	s := fc.scale
	px := func(f float64) int { return int(f * s) }
	m, res, rules := v.Match, v.Result, v.Rules
	duels := rules.PartDuels(m.CurrentPart)

	w := px(1180)
	headH, rowH := px(48), px(36)
	h := headH + rowH*len(duels)
	cx := int(v.Layout.Bar.X / 100 * float64(r.w))
	bottom := int(v.Layout.Bar.Y / 100 * float64(r.h))
	bar := clampRect(image.Rect(cx-w/2, bottom-h, cx+w/2, bottom), r.w, r.h)
	x0, y0 := bar.Min.X, bar.Min.Y
	mid := x0 + w/2

	// Header.
	fill(img, image.Rect(x0, y0, bar.Max.X, y0+headH), cBg, v.Layout.Opacity)
	scoreW := px(150)
	fill(img, image.Rect(mid-scoreW/2, y0, mid+scoreW/2, y0+headH), cAccent, 100)
	r.textCenter(img, fc.big, mid, y0+px(34), cBg, Points(res.HomePts)+" : "+Points(res.AwayPts))
	r.textRight(img, fc.big, mid-scoreW/2-px(16), y0+px(34), cText, m.Home.Label())
	r.text(img, fc.big, mid+scoreW/2+px(16), y0+px(34), cText, m.Away.Label())
	// Corners: total pins and the part.
	r.text(img, fc.tiny, x0+px(14), y0+px(19), cMuted, "kuželky")
	r.text(img, fc.mid, x0+px(14), y0+px(41), cText, pins(res.HomePins)+" : "+pins(res.AwayPins))
	partTxt := fmt.Sprintf("část %d/%d", m.CurrentPart, rules.Parts())
	if res.Finished {
		partTxt = "konec"
	}
	r.textRight(img, fc.tiny, bar.Max.X-px(14), y0+px(19), cMuted, diffText(res.HomePins-res.AwayPins))
	r.textRight(img, fc.mid, bar.Max.X-px(14), y0+px(41), cText, partTxt)

	// Duel rows.
	laneW, sumW, setW, nameW := px(50), px(70), px(96), px(190)
	for i, di := range duels {
		y := y0 + headH + i*rowH
		bg := cBg2
		if i%2 == 1 {
			bg = color.RGBA{18, 30, 60, 255}
		}
		fill(img, image.Rect(x0, y, bar.Max.X, y+rowH), bg, v.Layout.Opacity)
		d, dr := m.Duels[di], res.Duels[di]
		base := y + px(25)

		// Set points in the middle.
		r.textCenter(img, fc.mid, mid, base, cAccent, Points(dr.HomeSets)+" : "+Points(dr.AwaySets))

		// Home side, from the middle outwards: sum, lanes, name.
		hx := mid - setW/2
		r.textRight(img, fc.mid, hx-px(4), base, winColor(dr, true), strconv.Itoa(dr.HomeSum))
		lx := hx - sumW
		vals := laneValues(d, true)
		for j := len(vals) - 1; j >= 0; j-- {
			r.textRight(img, fc.smallR, lx, base, vals[j].c, vals[j].s)
			lx -= laneW
		}
		r.text(img, fc.midR, x0+px(14), base, cText, fit(fc.midR, d.HomePlayer, nameW))

		// Away side.
		ax := mid + setW/2
		r.text(img, fc.mid, ax+px(4), base, winColor(dr, false), strconv.Itoa(dr.AwaySum))
		lx = ax + sumW
		for _, lv := range laneValues(d, false) {
			r.text(img, fc.smallR, lx, base, lv.c, lv.s)
			lx += laneW
		}
		r.textRight(img, fc.midR, bar.Max.X-px(14), base, cText, fit(fc.midR, d.AwayPlayer, nameW))
	}
	// Accent line under the bar.
	fill(img, image.Rect(x0, bar.Max.Y-px(3), bar.Max.X, bar.Max.Y), cAccent, 100)
}

func diffText(d int) string {
	switch {
	case d > 0:
		return fmt.Sprintf("+%d domácí", d)
	case d < 0:
		return fmt.Sprintf("+%d hosté", -d)
	}
	return "nerozhodně"
}

// winColor highlights the winner's sum of a finished duel.
func winColor(dr match.DuelResult, home bool) color.RGBA {
	if dr.Finished && ((home && dr.HomePts > dr.AwayPts) || (!home && dr.AwayPts > dr.HomePts)) {
		return cWin
	}
	return cText
}

type laneVal struct {
	s string
	c color.RGBA
}

// laneValues: entered lane segments (white) and the live value (amber).
func laneValues(d match.Duel, home bool) []laneVal {
	var out []laneVal
	for _, p := range d.Lanes {
		v := p.Away
		if home {
			v = p.Home
		}
		if v != nil {
			out = append(out, laneVal{strconv.Itoa(*v), cMuted})
		}
	}
	live := d.Live.Away
	if home {
		live = d.Live.Home
	}
	if live != nil && len(out) < len(d.Lanes) {
		out = append(out, laneVal{strconv.Itoa(*live), cLive})
	}
	return out
}

// fit shortens s with an ellipsis to fit maxW pixels.
func fit(face font.Face, s string, maxW int) string {
	if font.MeasureString(face, s).Round() <= maxW {
		return s
	}
	rs := []rune(s)
	for len(rs) > 1 {
		rs = rs[:len(rs)-1]
		if font.MeasureString(face, string(rs)+"…").Round() <= maxW {
			return string(rs) + "…"
		}
	}
	return s
}

// resultTable: big centered table of all duels (end of the match or a break).
func (r *Renderer) resultTable(img *image.RGBA, v ScoreView) {
	fc := newFaces(float64(r.h) / 720)
	s := fc.scale
	px := func(f float64) int { return int(f * s) }
	m, res := v.Match, v.Result
	w := px(1000)
	rowH := px(40)
	h := px(110) + rowH*len(m.Duels) + px(64)
	box := image.Rect((r.w-w)/2, (r.h-h)/2, (r.w+w)/2, (r.h+h)/2)
	fill(img, box, cBg, 92)
	x0, y := box.Min.X, box.Min.Y
	mid := x0 + w/2

	title := "Průběžný stav"
	if res.Finished {
		title = "Konečný výsledek"
	}
	r.textCenter(img, fc.mid, mid, y+px(32), cMuted, title)
	r.textRight(img, fc.title, mid-px(90), y+px(84), cText, fit(fc.title, m.Home.Label(), px(330)))
	r.text(img, fc.title, mid+px(90), y+px(84), cText, fit(fc.title, m.Away.Label(), px(330)))
	fill(img, image.Rect(mid-px(80), y+px(48), mid+px(80), y+px(96)), cAccent, 100)
	r.textCenter(img, fc.title, mid, y+px(86), cBg, Points(res.HomePts)+":"+Points(res.AwayPts))
	y += px(110)

	for i, d := range m.Duels {
		dr := res.Duels[i]
		if i%2 == 0 {
			fill(img, image.Rect(x0, y, box.Max.X, y+rowH), cBg2, 100)
		}
		base := y + px(28)
		r.text(img, fc.midR, x0+px(20), base, cText, fit(fc.midR, d.HomePlayer, px(250)))
		r.textRight(img, fc.midR, box.Max.X-px(20), base, cText, fit(fc.midR, d.AwayPlayer, px(250)))
		if !dr.Started {
			r.textCenter(img, fc.mid, mid, base, cMuted, "–")
			y += rowH
			continue
		}
		r.textRight(img, fc.mid, mid-px(110), base, winColor(dr, true), strconv.Itoa(dr.HomeSum))
		r.textCenter(img, fc.smallR, mid-px(55), base, cMuted, Points(dr.HomeSets))
		r.textCenter(img, fc.mid, mid, base, cAccent, Points(dr.HomePts)+":"+Points(dr.AwayPts))
		r.textCenter(img, fc.smallR, mid+px(55), base, cMuted, Points(dr.AwaySets))
		r.text(img, fc.mid, mid+px(110), base, winColor(dr, false), strconv.Itoa(dr.AwaySum))
		y += rowH
	}
	fill(img, image.Rect(x0, y+px(6), box.Max.X, y+px(8)), cAccent, 100)
	base := y + px(46)
	r.text(img, fc.small, x0+px(20), base, cMuted, "Celkem kuželek")
	r.textRight(img, fc.big, mid-px(110), base, cText, pins(res.HomePins))
	r.text(img, fc.big, mid+px(110), base, cText, pins(res.AwayPins))
	r.textRight(img, fc.small, box.Max.X-px(20), base, cMuted, diffText(res.HomePins-res.AwayPins))
}

func (r *Renderer) textRight(img draw.Image, face font.Face, x, y int, c color.Color, s string) {
	r.text(img, face, x-font.MeasureString(face, s).Round(), y, c, s)
}

func (r *Renderer) textCenter(img draw.Image, face font.Face, x, y int, c color.Color, s string) {
	r.text(img, face, x-font.MeasureString(face, s).Round()/2, y, c, s)
}
