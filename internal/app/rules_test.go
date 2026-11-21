package app

import (
	"io"
	"log"
	"path/filepath"
	"testing"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/match"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	a := New(dir, filepath.Join(dir, "config.json"), cfg, "ffmpeg", log.New(io.Discard, "", 0), NewLogBuf(10))
	m := match.New(cfg.Rules)
	m.Home.Name, m.Away.Name = "Domácí", "Hosté"
	for i := range m.Duels {
		m.Duels[i].HomePlayer, m.Duels[i].AwayPlayer = "D"+string(rune('1'+i)), "H"+string(rune('1'+i))
	}
	if err := a.NewMatch(m); err != nil {
		t.Fatal(err)
	}
	return a
}

func ip(v int) *int { return &v }

func TestDetailedScoring(t *testing.T) {
	a := newTestApp(t)
	set := func(lane int, side, field string, v int) {
		t.Helper()
		if err := a.SetCell(Cell{Duel: 0, Lane: lane, Side: side, Field: field, Value: ip(v)}); err != nil {
			t.Fatal(err)
		}
	}
	set(0, "d", "p", 95)
	if a.MatchView().Match.Duels[0].Lanes[0].Home != nil {
		t.Fatal("segment total must wait for the clearing")
	}
	set(0, "d", "d", 40)
	set(0, "d", "ch", 2)
	p := a.MatchView().Match.Duels[0].Lanes[0]
	if p.Home == nil || *p.Home != 135 || *p.HomeDet.Errors != 2 {
		t.Fatalf("segment = %v det %+v", p.Home, p.HomeDet)
	}
	// Undo must give back the state before the last change (details are not shared/mutated).
	if err := a.Undo(); err != nil {
		t.Fatal(err)
	}
	if e := a.MatchView().Match.Duels[0].Lanes[0].HomeDet.Errors; e != nil {
		t.Fatalf("undo kept errors = %d", *e)
	}
	r := a.MatchView().Result
	if r.Home.Full != 95 || r.Home.Clear != 40 {
		t.Fatalf("totals %+v", r.Home)
	}
	if err := a.SetCell(Cell{Duel: 0, Lane: 0, Side: "d", Field: "ch", Value: ip(31)}); err == nil {
		t.Fatal("31 errors in 30 throws accepted")
	}
}

func TestAbsentAndSubstitution(t *testing.T) {
	a := newTestApp(t)
	// Away player of duel 1 did not start: home wins every segment with any score.
	if err := a.SetPlayer(PlayerChange{Duel: 0, Side: "h", Absent: true}); err != nil {
		t.Fatal(err)
	}
	for j := 0; j < 4; j++ {
		if err := a.SetCell(Cell{Duel: 0, Lane: j, Side: "d", Value: ip(100)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.SetCell(Cell{Duel: 0, Lane: 0, Side: "h", Value: ip(5)}); err == nil {
		t.Fatal("result entered for an absent player")
	}
	dr := a.MatchView().Result.Duels[0]
	if !dr.Finished || dr.HomeSets != 4 || dr.HomePts != 1 || dr.AwaySum != 0 {
		t.Fatalf("absent duel: %+v", dr)
	}
	if err := a.SetPlayer(PlayerChange{Duel: 0, Side: "h", Absent: true, Sub: &match.Sub{Name: "X", FromThrow: 1}}); err == nil {
		t.Fatal("an empty place was substituted")
	}

	// Two substitutions per team are allowed, the third is not (SŘ čl. 12).
	for i, want := range []bool{true, true, false} {
		err := a.SetPlayer(PlayerChange{Duel: i + 1, Side: "d", Sub: &match.Sub{Name: "Náhradník", FromThrow: 61}})
		if (err == nil) != want {
			t.Fatalf("substitution %d: err = %v", i+1, err)
		}
	}
	d := a.MatchView().Match.Duels[1]
	if d.Label("d") != "D2 / Náhradník" || d.Current("d") != "Náhradník" {
		t.Fatalf("labels %q %q", d.Label("d"), d.Current("d"))
	}
	if err := a.SetPlayer(PlayerChange{Duel: 3, Side: "h", Sub: &match.Sub{Name: "Y", FromThrow: 121}}); err == nil {
		t.Fatal("substitution from throw 121 of 120 accepted")
	}
}

func TestInterruption(t *testing.T) {
	a := newTestApp(t)
	if err := a.SetMode(match.ModeMessage, "Přestávka"); err != nil {
		t.Fatal(err)
	}
	a.SetOverlay(false)
	if err := a.SetInterrupted(true); err != nil {
		t.Fatal(err)
	}
	m := a.MatchView().Match
	if !m.Interrupted || m.Mode != match.ModeResult || !a.overlayOn.Load() {
		t.Fatalf("interrupted: mode %q overlay %v", m.Mode, a.overlayOn.Load())
	}
	if a.Status().Interrupted != true {
		t.Fatal("status does not show the interruption")
	}
	if err := a.SetInterrupted(false); err != nil {
		t.Fatal(err)
	}
	m = a.MatchView().Match
	if m.Interrupted || m.Mode != match.ModeMessage || m.Message != "Přestávka" || a.overlayOn.Load() {
		t.Fatalf("resumed: mode %q msg %q overlay %v", m.Mode, m.Message, a.overlayOn.Load())
	}
}
