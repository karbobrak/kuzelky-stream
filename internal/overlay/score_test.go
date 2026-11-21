package overlay

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"kuzelkystream/internal/match"
)

func ip(v int) *int { return &v }

func sampleMatch() (match.Match, match.Rules) {
	r := match.DefaultRules()
	m := match.New(r)
	m.Home = match.Team{Name: "TJ Sokol Kutná Hora", Short: "Kutná Hora"}
	m.Away = match.Team{Name: "SKK Rokycany", Short: "Rokycany"}
	m.Competition, m.Round = "1. KLM", "9"
	names := [][2]string{{"Novák Petr", "Dvořák Jan"}, {"Svoboda Karel", "Černý Tomáš"}, {"Procházka Jiří", "Kučera Martin"},
		{"Veselý Pavel", "Horák Lukáš"}, {"Němec David", "Marek Ondřej"}, {"Pokorný Vladimír Maxmilián", "Pospíšil Josef"}}
	for i, n := range names {
		m.Duels[i].HomePlayer, m.Duels[i].AwayPlayer = n[0], n[1]
	}
	full := func(d int, h, a [4]int) {
		for j := 0; j < 4; j++ {
			m.Duels[d].Lanes[j] = match.Pair{Home: ip(h[j]), Away: ip(a[j])}
		}
	}
	full(0, [4]int{152, 148, 160, 139}, [4]int{140, 155, 141, 150})
	full(1, [4]int{139, 155, 147, 151}, [4]int{141, 144, 150, 133})
	m.Duels[2].Lanes[0] = match.Pair{Home: ip(152), Away: ip(140)}
	m.Duels[2].Lanes[1] = match.Pair{Home: ip(148), Away: ip(158)}
	m.Duels[2].Live = match.Pair{Home: ip(71), Away: ip(66)}
	m.Duels[3].Lanes[0] = match.Pair{Home: ip(139), Away: ip(141)}
	m.Duels[3].Lanes[1] = match.Pair{Home: ip(155), Away: ip(149)}
	m.Duels[3].Live = match.Pair{Home: ip(60), Away: ip(64)}
	m.CurrentPart = 2
	return m, r
}

// TestScoreRender renders every mode; KUZELKY_OVERLAY_OUT=<dir> also writes PNGs for a visual check.
func TestScoreRender(t *testing.T) {
	ren, err := NewRenderer(1280, 720)
	if err != nil {
		t.Fatal(err)
	}
	m, r := sampleMatch()
	for _, mode := range []string{match.ModeMatch, match.ModeMessage, match.ModeResult} {
		m.Mode, m.Message = mode, "Přestávka – pokračujeme ve 14:30"
		img := ren.Score(ScoreView{Match: m, Result: match.Compute(m, r), Rules: r, Layout: DefaultLayout()})
		if dir := os.Getenv("KUZELKY_OVERLAY_OUT"); dir != "" {
			f, _ := os.Create(filepath.Join(dir, "overlay-"+mode+".png"))
			_ = png.Encode(f, img)
			f.Close()
		}
	}
}
