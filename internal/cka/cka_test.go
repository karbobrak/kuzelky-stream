package cka

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"kuzelkystream/internal/match"
)

func load(t *testing.T, name string) *Match {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func apply(t *testing.T, name string) (match.Match, match.Result, int) {
	t.Helper()
	r := match.DefaultRules()
	dst := match.New(r)
	dst.Home.Short = "Domácí zkratka" // kept: the web does not know it
	part := load(t, name).Apply(&dst, r)
	if err := dst.Validate(r); err != nil {
		t.Fatal(err)
	}
	return dst, match.Compute(dst, r), part
}

// The finished match matches the official report: 8:0, 3526:3286, sets 16:8, P/D/Ch totals.
func TestFinishedMatch(t *testing.T) {
	m, res, part := apply(t, "dohrano.json")
	if m.Home.Name != "TJ Sparta Kutná Hora" || m.Away.Name != "TJ Lokomotiva Ústí nad Labem" ||
		m.Competition != "3. KLM B" || m.Round != "9" || m.Date != "2026-11-21" || m.Home.Short != "Domácí zkratka" {
		t.Fatalf("meta: %+v %+v %q %q %q", m.Home, m.Away, m.Competition, m.Round, m.Date)
	}
	if m.Duels[0].HomePlayer != "Jiří Němec" || m.Duels[5].AwayPlayer != "Milan Grejtovský" {
		t.Fatalf("players: %q %q", m.Duels[0].HomePlayer, m.Duels[5].AwayPlayer)
	}
	if !res.Finished || res.HomePts != 8 || res.AwayPts != 0 || res.HomePins != 3526 || res.AwayPins != 3286 ||
		res.HomeSets != 16 || res.AwaySets != 8 {
		t.Fatalf("result: %+v", res)
	}
	if res.Home != (match.Totals{Full: 2264, Clear: 1262, Errors: 12}) || res.Away != (match.Totals{Full: 2173, Clear: 1113, Errors: 31}) {
		t.Fatalf("totals: %+v %+v", res.Home, res.Away)
	}
	if part != 0 {
		t.Fatalf("finished match reports part %d", part)
	}
}

// Substitution: the starting player stays on the position, the substitute from throw 35.
func TestSubstitution(t *testing.T) {
	m, res, _ := apply(t, "stridani.json")
	d := m.Duels[3]
	if d.AwaySub == nil || d.AwaySub.Name != "Jan Večerka" || d.AwaySub.FromThrow != 35 || d.AwayPlayer != "Luděk Rychlovský" {
		t.Fatalf("substitution: %+v %q", d.AwaySub, d.AwayPlayer)
	}
	if res.HomePts != 2.5 || res.AwayPts != 5.5 {
		t.Fatalf("points %v:%v", res.HomePts, res.AwayPts)
	}
}

// In progress (1. KLM): the last part is being played; the web already counts duel 6, whose 4th
// segment has started, so the app does too.
func TestInProgress(t *testing.T) {
	m, res, part := apply(t, "probiha.json")
	if part != 3 {
		t.Fatalf("live part = %d, want 3", part)
	}
	if d := m.Duels[5]; d.Lanes[3].Home == nil || *d.Lanes[3].Home != 108 {
		t.Fatalf("duel 6, 4th segment: %+v", d.Lanes[3])
	}
	if res.Finished {
		t.Log("all duels counted – the match shows as finished like on the web")
	}
}

// A segment of a duel the web has not scored yet is the live (amber) value.
func TestLiveSegment(t *testing.T) {
	r, _ := load(t, "t100_pozdeji.json").Rules(match.DefaultRules())
	dst := match.New(r)
	load(t, "t100_pozdeji.json").Apply(&dst, r)
	d := dst.Duels[3] // Tesař – Nový: 1st segment in progress
	if d.Lanes[0].Home != nil || d.Live.Home == nil || *d.Live.Home != 220 {
		t.Fatalf("live: lanes %+v live %+v", d.Lanes[0], d.Live)
	}
}

// The score always equals the team points the results service shows.
func TestScoreMatchesWeb(t *testing.T) {
	for _, f := range []string{"dohrano.json", "stridani.json", "probiha.json", "t100_16bodu.json", "t100_pozdeji.json"} {
		cm := load(t, f)
		r, _ := cm.Rules(match.DefaultRules())
		dst := match.New(r)
		cm.Apply(&dst, r)
		res := match.Compute(dst, r)
		home, away := *cm.Team(true).TeamPoints, *cm.Team(false).TeamPoints
		if res.HomePts != home || res.AwayPts != away {
			t.Errorf("%s: app %v:%v, web %v:%v", f, res.HomePts, res.AwayPts, home, away)
		}
	}
}

func TestParsePage(t *testing.T) {
	b, _ := os.ReadFile("testdata/stridani.json")
	inner, _ := json.Marshal(`7:["$","$L3c",null,{"match":` + string(b) + `}]` + "\n")
	// The payload is split over several push calls, as on the real page.
	half := len(inner) / 2
	for half < len(inner) && inner[half-1] == '\\' {
		half++
	}
	page := `<html><script>self.__next_f.push([1,"0:[]\n"])</script>` +
		`<script>self.__next_f.push([1,` + string(inner[:half]) + `"])</script>` +
		`<script>self.__next_f.push([1,"` + string(inner[half:]) + `])</script></html>`
	m, err := ParsePage([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	if m.HomeTeam.Name == "" || len(m.Results) != 2 || m.Results[1].Substitutions[0].ThrowNumber != 35 {
		t.Fatalf("parsed %+v", m.HomeTeam)
	}
	if _, err := ParsePage([]byte("<html>nic</html>")); err == nil {
		t.Fatal("page without data accepted")
	}
}

func TestCheckURL(t *testing.T) {
	ok := "https://vysledky.kuzelky.cz/detail-zapasu/3-klm-b-2026-2027-kolo-9-a-b/"
	if u, err := CheckURL(ok); err != nil || strings.HasSuffix(u, "/") {
		t.Fatalf("%q %v", u, err)
	}
	for _, bad := range []string{"https://evil.example/detail-zapasu/x", "https://vysledky.kuzelky.cz/", "nic"} {
		if _, err := CheckURL(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// Krajský přebor: 100 hs over 2 segments. Two duels are over and the away team leads in total
// pins, so it is 0:4 as on the results page (the 2 points for the total are provisional).
func TestT100Rules(t *testing.T) {
	cm := load(t, "t100_16bodu.json")
	r, desc := cm.Rules(match.DefaultRules())
	if r.Lanes != 2 || r.Throws() != 50 || r.PlayersPerTeam != 6 || r.MaxPinsPerLane != 450 {
		t.Fatalf("rules %+v (%s)", r, desc)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	dst := match.New(r)
	part := cm.Apply(&dst, r)
	if err := dst.Validate(r); err != nil {
		t.Fatal(err)
	}
	res := match.Compute(dst, r)
	if res.HomePts != 0 || res.AwayPts != 4 || part != 2 {
		t.Fatalf("score %v:%v part %d", res.HomePts, res.AwayPts, part)
	}
	if !res.Duels[0].Finished || !res.Duels[1].Finished || res.Duels[2].Finished {
		t.Fatalf("finished duels: %v %v %v", res.Duels[0].Finished, res.Duels[1].Finished, res.Duels[2].Finished)
	}
	// The 1. KLM matches keep the 8-point international system and 4 × 30 throws.
	for _, f := range []string{"dohrano.json", "stridani.json", "probiha.json"} {
		r, _ := load(t, f).Rules(match.DefaultRules())
		if r.Lanes != 4 || r.Throws() != 30 || r.DuelByPins {
			t.Fatalf("%s: rules %+v", f, r)
		}
	}
}

// Later in the same match: duel 3 is over (the web gave its point) although the next part has not
// started yet; home leads in total pins. The web shows 3:2.
func TestT100Later(t *testing.T) {
	cm := load(t, "t100_pozdeji.json")
	r, _ := cm.Rules(match.DefaultRules())
	dst := match.New(r)
	cm.Apply(&dst, r)
	res := match.Compute(dst, r)
	if res.HomePts != 3 || res.AwayPts != 2 || !res.Duels[2].Finished || res.Duels[3].Finished {
		t.Fatalf("score %v:%v, duel 3 finished %v, duel 4 finished %v", res.HomePts, res.AwayPts, res.Duels[2].Finished, res.Duels[3].Finished)
	}
}
