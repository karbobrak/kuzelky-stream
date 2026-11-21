// Package match holds a nine-pin (kuželky) team match: teams, duels, lane results and the scoring.
package match

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Rules of the competition. Defaults are 1. KLM; everything is configurable.
type Rules struct {
	PlayersPerTeam  int     `json:"hracuNaTym"`
	Lanes           int     `json:"drah"`
	DuelsPerPart    int     `json:"soubojuNaCast"`
	SetWin          float64 `json:"sbVyhra"`
	SetDraw         float64 `json:"sbRemiza"`
	DuelPoint       float64 `json:"bodSouboj"`
	DuelTieByPins   bool    `json:"remizaSoubojeRozhodujeKuzelky"`
	TotalPinsPoints float64 `json:"bodyZaCelkem"`
	MaxPinsPerLane  int     `json:"maxKuzelekNaDrahu"` // 30 throws × 9 pins = 270
	ThrowsPerLane   int     `json:"hoduNaUsek"`        // 30 (120 hs = 4 × 30); for "substitute from throw N"
	MaxSubs         int     `json:"stridaniNaTym"`     // substitutions allowed per team (SŘ čl. 12: 2, or 1 for smaller teams)
	// Detailed: score full (plné), clearing (dorážka) and errors (chyby) per lane segment, like the
	// official ČKA match report; the segment total is full + clearing.
	Detailed bool `json:"podrobnyZapis"`
	// LanePlan: which physical lane each player plays each lane segment on, per duel slot of a part
	// (index 0 = first duel of every part). Only shown to the scorer; empty = not shown.
	LanePlan []LaneSlot `json:"poradiDrah,omitempty"`
}

// LaneSlot: lane numbers of the home and away player, one per lane segment.
type LaneSlot struct {
	Home []int `json:"domaci"`
	Away []int `json:"hoste"`
}

// Equal compares rules including the lane plan (an empty plan equals a missing one).
func (r Rules) Equal(o Rules) bool {
	if len(r.LanePlan) == 0 {
		r.LanePlan = nil
	}
	if len(o.LanePlan) == 0 {
		o.LanePlan = nil
	}
	return reflect.DeepEqual(r, o)
}

// LaneOf returns the physical lane of duel i (0-based), side "d"/"h", segment j; 0 = unknown.
func (r Rules) LaneOf(i int, side string, j int) int {
	if r.DuelsPerPart < 1 {
		return 0
	}
	slot := i % r.DuelsPerPart
	if slot >= len(r.LanePlan) {
		return 0
	}
	seq := r.LanePlan[slot].Home
	if side == "h" {
		seq = r.LanePlan[slot].Away
	}
	if j < 0 || j >= len(seq) {
		return 0
	}
	return seq[j]
}

func DefaultRules() Rules {
	return Rules{PlayersPerTeam: 6, Lanes: 4, DuelsPerPart: 2, SetWin: 1, SetDraw: 0.5, DuelPoint: 1,
		DuelTieByPins: true, TotalPinsPoints: 2, MaxPinsPerLane: 270, ThrowsPerLane: 30, MaxSubs: 2}
}

// Throws per lane segment (older configs have 0 = the standard 30).
func (r Rules) Throws() int {
	if r.ThrowsPerLane <= 0 {
		return 30
	}
	return r.ThrowsPerLane
}

func (r Rules) Validate() error {
	switch {
	case r.ThrowsPerLane < 0 || r.ThrowsPerLane > 200:
		return errors.New("hodů na úsek musí být 1–200")
	case r.MaxSubs < 0 || r.MaxSubs > 12:
		return errors.New("počet střídání na tým musí být 0–12")
	case r.PlayersPerTeam < 1 || r.PlayersPerTeam > 12:
		return errors.New("počet hráčů na tým musí být 1–12")
	case r.Lanes < 1 || r.Lanes > 8:
		return errors.New("počet drah musí být 1–8")
	case r.DuelsPerPart < 1 || r.DuelsPerPart > r.PlayersPerTeam:
		return errors.New("počet soubojů v části musí být 1 až počet hráčů")
	case r.MaxPinsPerLane < 1:
		return errors.New("maximum kuželek na dráhu musí být kladné")
	}
	for i, sl := range r.LanePlan {
		for _, n := range append(slices.Clone(sl.Home), sl.Away...) {
			if n < 1 || n > 20 {
				return fmt.Errorf("rozpis drah, %d. souboj části: číslo dráhy musí být 1–20 (%d)", i+1, n)
			}
		}
	}
	return nil
}

// Parts is the number of parts (rounds of simultaneous duels).
func (r Rules) Parts() int { return (r.PlayersPerTeam + r.DuelsPerPart - 1) / r.DuelsPerPart }

type Team struct {
	Name  string `json:"nazev"`
	Short string `json:"zkratka"`
}

// Label is the short name if set, otherwise the full name.
func (t Team) Label() string {
	if strings.TrimSpace(t.Short) != "" {
		return t.Short
	}
	return t.Name
}

// Pair is one lane segment of a duel: pins of the home and away player. nil = not played yet.
// With detailed scoring HomeDet/AwayDet hold full/clearing/errors and Home/Away is full + clearing.
type Pair struct {
	Home    *int    `json:"d"`
	Away    *int    `json:"h"`
	HomeDet *Detail `json:"dPodrobne,omitempty"`
	AwayDet *Detail `json:"hPodrobne,omitempty"`
}

// Detail of one player's lane segment as in the ČKA match report. Never modified in place
// (the undo history shares them): replace the whole value.
type Detail struct {
	Full   *int `json:"p"`  // plné
	Clear  *int `json:"d"`  // dorážka
	Errors *int `json:"ch"` // chyby
}

// Sub: a substitute who took over the place from the given throw (1-based, SŘ čl. 12).
type Sub struct {
	Name      string `json:"jmeno"`
	FromThrow int    `json:"odHodu"`
}

type Duel struct {
	HomePlayer string `json:"domaci"`
	AwayPlayer string `json:"hoste"`
	Lanes      []Pair `json:"useky"`
	// Live: pins so far in the lane segment being played (not yet entered in Lanes). Optional.
	Live Pair `json:"prubezne"`
	// Substitutions (nil = none) and places left empty in the line-up (count as 0 pins, SŘ čl. 15).
	HomeSub    *Sub `json:"dStridani,omitempty"`
	AwaySub    *Sub `json:"hStridani,omitempty"`
	HomeAbsent bool `json:"dNenastoupil,omitempty"`
	AwayAbsent bool `json:"hNenastoupil,omitempty"`
}

// Player is the starting player of a side ("d"/"h").
func (d Duel) Player(side string) string {
	if side == "h" {
		return d.AwayPlayer
	}
	return d.HomePlayer
}

// SubOf returns the side's substitution, or nil.
func (d Duel) SubOf(side string) *Sub {
	if side == "h" {
		return d.AwaySub
	}
	return d.HomeSub
}

// Absent reports whether the side's place in the line-up is empty.
func (d Duel) Absent(side string) bool {
	if side == "h" {
		return d.AwayAbsent
	}
	return d.HomeAbsent
}

// Current is who plays the place now (the substitute after a substitution).
func (d Duel) Current(side string) string {
	if s := d.SubOf(side); s != nil && strings.TrimSpace(s.Name) != "" {
		return s.Name
	}
	return d.Player(side)
}

// Label names everyone who played the place: "Novák / Svoboda" after a substitution.
func (d Duel) Label(side string) string {
	if s := d.SubOf(side); s != nil && strings.TrimSpace(s.Name) != "" {
		return d.Player(side) + " / " + s.Name
	}
	return d.Player(side)
}

// Overlay modes chosen on the scoring page.
const (
	ModeMatch   = "utkani"   // normal score bar
	ModeMessage = "zprava"   // score bar + a message (break, …)
	ModeResult  = "vysledek" // big table of all duels
)

type Match struct {
	Date        string `json:"datum"`
	Competition string `json:"soutez"`
	Round       string `json:"kolo"`
	Venue       string `json:"misto"`
	Home        Team   `json:"domaci"`
	Away        Team   `json:"hoste"`
	Duels       []Duel `json:"souboje"`
	CurrentPart int    `json:"aktualniCast"` // 1-based
	Mode        string `json:"stavGrafiky"`
	Message     string `json:"zprava"`
	// Interrupted: the match is stopped (technical failure, SŘ čl. 16); the graphics show the full
	// result table with a notice. PrevMode/PrevMessage restore the graphics when play resumes.
	Interrupted bool   `json:"preruseno,omitempty"`
	PrevMode    string `json:"predchoziStav,omitempty"`
	PrevMessage string `json:"predchoziZprava,omitempty"`
}

// InterruptedText is shown above the result table while the match is interrupted.
const InterruptedText = "Utkání přerušeno"

// New creates an empty match shaped by the rules.
func New(r Rules) Match {
	m := Match{CurrentPart: 1, Mode: ModeMatch}
	m.Reshape(r)
	return m
}

// Reshape makes the number of duels and lane segments match the rules, keeping existing data.
func (m *Match) Reshape(r Rules) {
	for len(m.Duels) < r.PlayersPerTeam {
		m.Duels = append(m.Duels, Duel{})
	}
	m.Duels = m.Duels[:r.PlayersPerTeam]
	for i := range m.Duels {
		d := &m.Duels[i]
		for len(d.Lanes) < r.Lanes {
			d.Lanes = append(d.Lanes, Pair{})
		}
		d.Lanes = d.Lanes[:r.Lanes]
	}
	if m.CurrentPart < 1 {
		m.CurrentPart = 1
	}
	if p := r.Parts(); m.CurrentPart > p {
		m.CurrentPart = p
	}
	if m.Mode == "" {
		m.Mode = ModeMatch
	}
}

// PartDuels returns the indexes of the duels in part p (1-based).
func (r Rules) PartDuels(p int) []int {
	var out []int
	for i := (p - 1) * r.DuelsPerPart; i < p*r.DuelsPerPart && i < r.PlayersPerTeam; i++ {
		out = append(out, i)
	}
	return out
}

// Validate checks the match against the rules (shape and pin counts).
func (m Match) Validate(r Rules) error {
	if len(m.Duels) != r.PlayersPerTeam {
		return fmt.Errorf("utkání má %d soubojů, pravidla %d", len(m.Duels), r.PlayersPerTeam)
	}
	check := func(v *int, who string) error {
		if v != nil && (*v < 0 || *v > r.MaxPinsPerLane) {
			return fmt.Errorf("%s: %d kuželek není možné (0–%d)", who, *v, r.MaxPinsPerLane)
		}
		return nil
	}
	for i, d := range m.Duels {
		if len(d.Lanes) != r.Lanes {
			return fmt.Errorf("souboj %d má %d úseků, pravidla %d", i+1, len(d.Lanes), r.Lanes)
		}
		for j, p := range d.Lanes {
			if err := errors.Join(check(p.Home, fmt.Sprintf("souboj %d, dráha %d, domácí", i+1, j+1)),
				check(p.Away, fmt.Sprintf("souboj %d, dráha %d, hosté", i+1, j+1))); err != nil {
				return err
			}
		}
		if err := errors.Join(check(d.Live.Home, fmt.Sprintf("souboj %d, průběžně domácí", i+1)),
			check(d.Live.Away, fmt.Sprintf("souboj %d, průběžně hosté", i+1))); err != nil {
			return err
		}
		for j, p := range d.Lanes {
			for _, dt := range []struct {
				det *Detail
				who string
			}{{p.HomeDet, "domácí"}, {p.AwayDet, "hosté"}} {
				if dt.det == nil {
					continue
				}
				where := fmt.Sprintf("souboj %d, %d. úsek, %s", i+1, j+1, dt.who)
				if err := errors.Join(check(dt.det.Full, where+", plné"), check(dt.det.Clear, where+", dorážka")); err != nil {
					return err
				}
				if e := dt.det.Errors; e != nil && (*e < 0 || *e > r.Throws()) {
					return fmt.Errorf("%s: %d chyb není možné (0–%d)", where, *e, r.Throws())
				}
			}
		}
		for _, side := range []string{"d", "h"} {
			if s := d.SubOf(side); s != nil {
				if strings.TrimSpace(s.Name) == "" {
					return fmt.Errorf("souboj %d: vyplňte jméno střídajícího hráče", i+1)
				}
				if total := r.Throws() * r.Lanes; s.FromThrow < 1 || s.FromThrow > total {
					return fmt.Errorf("souboj %d: střídání od %d. hodu není možné (1–%d)", i+1, s.FromThrow, total)
				}
			}
		}
	}
	for _, side := range []string{"d", "h"} {
		n := 0
		for _, d := range m.Duels {
			if d.SubOf(side) != nil {
				n++
			}
		}
		if n > r.MaxSubs {
			team := "domácí"
			if side == "h" {
				team = "hosté"
			}
			return fmt.Errorf("%s: %d střídání, pravidla dovolují %d", team, n, r.MaxSubs)
		}
	}
	switch m.Mode {
	case ModeMatch, ModeMessage, ModeResult:
	default:
		return fmt.Errorf("neznámý stav grafiky %q", m.Mode)
	}
	if m.CurrentPart < 1 || m.CurrentPart > r.Parts() {
		return fmt.Errorf("část %d neexistuje (1–%d)", m.CurrentPart, r.Parts())
	}
	return nil
}
