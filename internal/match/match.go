// Package match holds a nine-pin (kuželky) team match: teams, duels, lane results and the scoring.
package match

import (
	"errors"
	"fmt"
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
}

func DefaultRules() Rules {
	return Rules{PlayersPerTeam: 6, Lanes: 4, DuelsPerPart: 2, SetWin: 1, SetDraw: 0.5, DuelPoint: 1,
		DuelTieByPins: true, TotalPinsPoints: 2, MaxPinsPerLane: 270}
}

func (r Rules) Validate() error {
	switch {
	case r.PlayersPerTeam < 1 || r.PlayersPerTeam > 12:
		return errors.New("počet hráčů na tým musí být 1–12")
	case r.Lanes < 1 || r.Lanes > 8:
		return errors.New("počet drah musí být 1–8")
	case r.DuelsPerPart < 1 || r.DuelsPerPart > r.PlayersPerTeam:
		return errors.New("počet soubojů v části musí být 1 až počet hráčů")
	case r.MaxPinsPerLane < 1:
		return errors.New("maximum kuželek na dráhu musí být kladné")
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
type Pair struct {
	Home *int `json:"d"`
	Away *int `json:"h"`
}

type Duel struct {
	HomePlayer string `json:"domaci"`
	AwayPlayer string `json:"hoste"`
	Lanes      []Pair `json:"useky"`
	// Live: pins so far in the lane segment being played (not yet entered in Lanes). Optional.
	Live Pair `json:"prubezne"`
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
}

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
