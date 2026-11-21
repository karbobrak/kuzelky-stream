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

// In progress: duel 6 plays its 4th segment, which goes to the live column without set points;
// earlier parts count as finished because the last part has started.
func TestInProgress(t *testing.T) {
	m, res, part := apply(t, "probiha.json")
	if part != 3 {
		t.Fatalf("live part = %d, want 3", part)
	}
	d := m.Duels[5]
	if d.Lanes[3].Home != nil || d.Live.Home == nil || *d.Live.Home != 108 || *d.Live.Away != 108 {
		t.Fatalf("live segment: lanes %+v live %+v", d.Lanes[3], d.Live)
	}
	dr := res.Duels[5]
	if dr.Finished || dr.LanesDone != 3 || dr.HomeSum != 524 || dr.AwaySum != 523 {
		t.Fatalf("duel 6: %+v", dr)
	}
	for i := 0; i < 4; i++ {
		if !res.Duels[i].Finished {
			t.Fatalf("duel %d of an earlier part not finished", i+1)
		}
	}
	if res.Finished {
		t.Fatal("match in progress reported as finished")
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
