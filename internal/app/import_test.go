package app

import (
	"os"
	"testing"

	"kuzelkystream/internal/cka"
)

func loadCka(t *testing.T, name string) *cka.Match {
	t.Helper()
	b, err := os.ReadFile("../cka/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	m, err := cka.ParseJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestImportApply(t *testing.T) {
	a := newTestApp(t)
	const url = "https://vysledky.kuzelky.cz/detail-zapasu/2-klm-a-2026-2027-kolo-9-tj-radlice-muzi-skk-bohusovice-muzi"
	if err := a.SetResultsSource(url+"/", false); err != nil {
		t.Fatal(err)
	}
	if got := a.MatchView().Match.ResultsURL; got != url {
		t.Fatalf("stored url %q", got)
	}
	if err := a.SetResultsSource("https://example.com/x", true); err == nil {
		t.Fatal("foreign url accepted")
	}

	live := loadCka(t, "probiha.json")
	if err := a.importApply(live, url); err != nil {
		t.Fatal(err)
	}
	v := a.MatchView()
	if v.Match.CurrentPart != 3 || v.Match.Duels[0].HomePlayer == "D1" || v.Import.Status != "probíhá" {
		t.Fatalf("after import: part %d, player %q, status %q", v.Match.CurrentPart, v.Match.Duels[0].HomePlayer, v.Import.Status)
	}
	found := 0 // the published line-ups are saved as rosters
	for _, r := range a.Rosters() {
		if r.Name == v.Match.Home.Name || r.Name == v.Match.Away.Name {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("rosters of the imported teams: %d", found)
	}
	// A part chosen by hand stays while the play does not move on.
	if err := a.SetPart(1); err != nil {
		t.Fatal(err)
	}
	hist := len(a.mt.history)
	if err := a.importApply(live, url); err != nil {
		t.Fatal(err)
	}
	if a.MatchView().Match.CurrentPart != 1 {
		t.Fatal("import overrode the part chosen by hand")
	}
	if len(a.mt.history) != hist {
		t.Fatal("an unchanged import was saved")
	}
	// A different match opened meanwhile is not touched.
	if err := a.importApply(live, url+"-jiny"); err != nil {
		t.Fatal(err)
	}
	if a.MatchView().Match.ResultsURL != url {
		t.Fatal("import changed a different match")
	}
}
