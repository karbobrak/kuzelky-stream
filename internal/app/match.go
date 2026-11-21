package app

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kuzelkystream/internal/match"
	"kuzelkystream/internal/overlay"
)

// matchState is the match being scored. Every change is saved to disk at once (a crash loses
// nothing) and pushed onto an undo history.
type matchState struct {
	mu      sync.Mutex
	store   match.Store
	cur     *match.Match
	file    string
	history []match.Match
}

const maxUndo = 300

func (a *App) initMatch() {
	a.mt.store = match.Store{Dir: filepath.Join(a.dir, "data")}
	if name, m, err := a.mt.store.Current(); err == nil {
		m.Reshape(a.Config().Rules)
		a.mt.cur, a.mt.file = &m, name
		a.Log.Printf("utkání: načteno %s", name)
	}
}

// MatchView is the match with its computed result, for the UI.
type MatchView struct {
	Match   *match.Match  `json:"zapas"`
	Result  *match.Result `json:"vysledek,omitempty"`
	File    string        `json:"soubor,omitempty"`
	Rules   match.Rules   `json:"pravidla"`
	Parts   int           `json:"casti"`
	CanUndo bool          `json:"lzeZpet"`
}

func (a *App) MatchView() MatchView {
	rules := a.Config().Rules
	a.mt.mu.Lock()
	defer a.mt.mu.Unlock()
	v := MatchView{Rules: rules, Parts: rules.Parts(), File: a.mt.file, CanUndo: len(a.mt.history) > 0}
	if a.mt.cur != nil {
		m := *a.mt.cur
		r := match.Compute(m, rules)
		v.Match, v.Result = &m, &r
	}
	return v
}

// change applies f to a copy of the current match, validates, saves, records undo and redraws.
func (a *App) change(f func(m *match.Match) error) error {
	rules := a.Config().Rules
	a.mt.mu.Lock()
	defer a.mt.mu.Unlock()
	if a.mt.cur == nil {
		return errors.New("není otevřené žádné utkání – založte ho na záložce Utkání")
	}
	next := cloneMatch(*a.mt.cur)
	if err := f(&next); err != nil {
		return err
	}
	next.Reshape(rules)
	if err := next.Validate(rules); err != nil {
		return err
	}
	if err := a.mt.store.Save(a.mt.file, next); err != nil {
		return fmt.Errorf("utkání nelze uložit: %w", err)
	}
	a.mt.history = append(a.mt.history, *a.mt.cur)
	if len(a.mt.history) > maxUndo {
		a.mt.history = a.mt.history[len(a.mt.history)-maxUndo:]
	}
	a.mt.cur = &next
	a.redraw()
	a.markDirty("zapas:" + a.mt.file)
	return nil
}

func cloneMatch(m match.Match) match.Match {
	c := m
	c.Duels = make([]match.Duel, len(m.Duels))
	for i, d := range m.Duels {
		c.Duels[i] = d
		c.Duels[i].Lanes = append([]match.Pair(nil), d.Lanes...)
	}
	return c
}

// NewMatch starts a new match from the setup page (teams, players, meta) and makes it current.
func (a *App) NewMatch(m match.Match) error {
	rules := a.Config().Rules
	if strings.TrimSpace(m.Home.Name) == "" || strings.TrimSpace(m.Away.Name) == "" {
		return errors.New("vyplňte názvy obou týmů")
	}
	if m.Date == "" {
		m.Date = time.Now().Format("2006-01-02")
	}
	m.CurrentPart, m.Mode, m.Message = 1, match.ModeMatch, ""
	for i := range m.Duels { // a new match starts without results
		m.Duels[i].Lanes, m.Duels[i].Live = nil, match.Pair{}
	}
	m.Reshape(rules)
	if err := m.Validate(rules); err != nil {
		return err
	}
	name := a.uniqueFile(match.FileName(m))
	if err := a.mt.store.Save(name, m); err != nil {
		return err
	}
	a.saveRosters(m)
	a.mt.mu.Lock()
	a.mt.cur, a.mt.file, a.mt.history = &m, name, nil
	a.redraw()
	a.mt.mu.Unlock()
	a.markDirty("zapas:" + name)
	a.Log.Printf("utkání: nové %s – %s (%s)", m.Home.Name, m.Away.Name, name)
	return nil
}

func (a *App) uniqueFile(name string) string {
	base := strings.TrimSuffix(name, ".json")
	for i := 1; ; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s_%d.json", base, i)
		}
		if _, err := os.Stat(filepath.Join(a.mt.store.Dir, "zapasy", cand)); err != nil && !a.tombstoned("zapas:"+cand) {
			return cand
		}
	}
}

func (a *App) saveRosters(m match.Match) {
	home := match.Roster{Name: m.Home.Name, Short: m.Home.Short}
	away := match.Roster{Name: m.Away.Name, Short: m.Away.Short}
	for _, d := range m.Duels {
		home.Players = append(home.Players, d.HomePlayer)
		away.Players = append(away.Players, d.AwayPlayer)
	}
	for _, r := range []match.Roster{home, away} {
		if err := a.mt.store.SaveRoster(r); err != nil {
			a.Log.Printf("soupiska %s: %v", r.Name, err)
			continue
		}
		a.markDirty("tym:" + match.RosterFile(r.Name))
	}
}

// UpdateSetup changes teams, players and meta of the current match, keeping its results.
func (a *App) UpdateSetup(s match.Match) error {
	err := a.change(func(m *match.Match) error {
		if strings.TrimSpace(s.Home.Name) == "" || strings.TrimSpace(s.Away.Name) == "" {
			return errors.New("vyplňte názvy obou týmů")
		}
		m.Date, m.Competition, m.Round, m.Venue, m.Home, m.Away = s.Date, s.Competition, s.Round, s.Venue, s.Home, s.Away
		for i := range m.Duels {
			if i < len(s.Duels) {
				m.Duels[i].HomePlayer, m.Duels[i].AwayPlayer = s.Duels[i].HomePlayer, s.Duels[i].AwayPlayer
			}
		}
		return nil
	})
	if err == nil {
		a.mt.mu.Lock()
		m := *a.mt.cur
		a.mt.mu.Unlock()
		a.saveRosters(m)
	}
	return err
}

// Cell addresses one value on the scoring page.
type Cell struct {
	Duel  int    `json:"souboj"` // 0-based
	Lane  int    `json:"usek"`   // 0-based; -1 = live value of the lane being played
	Side  string `json:"strana"` // "d" home, "h" away
	Value *int   `json:"hodnota"`
	// Field for detailed scoring: "p" full, "d" clearing, "ch" errors; "" = the segment total.
	Field string `json:"pole,omitempty"`
}

func (a *App) SetCell(c Cell) error {
	return a.change(func(m *match.Match) error {
		if c.Duel < 0 || c.Duel >= len(m.Duels) {
			return fmt.Errorf("souboj %d neexistuje", c.Duel+1)
		}
		d := &m.Duels[c.Duel]
		var p *match.Pair
		switch {
		case c.Lane == -1:
			p = &d.Live
		case c.Lane >= 0 && c.Lane < len(d.Lanes):
			p = &d.Lanes[c.Lane]
		default:
			return fmt.Errorf("dráha %d neexistuje", c.Lane+1)
		}
		if c.Side != "d" && c.Side != "h" {
			return errors.New("strana musí být d nebo h")
		}
		if d.Absent(c.Side) {
			return errors.New("hráč nenastoupil – jeho výsledky se nezapisují")
		}
		if c.Field != "" {
			if c.Lane < 0 {
				return errors.New("průběžný stav se zapisuje jen jako součet")
			}
			// Replace the detail (never modify it in place: the undo history shares it).
			det := p.HomeDet
			if c.Side == "h" {
				det = p.AwayDet
			}
			nd := match.Detail{}
			if det != nil {
				nd = *det
			}
			switch c.Field {
			case "p":
				nd.Full = c.Value
			case "d":
				nd.Clear = c.Value
			case "ch":
				nd.Errors = c.Value
			default:
				return fmt.Errorf("neznámé pole %q", c.Field)
			}
			var sum *int // the segment counts once both full and clearing are entered
			if nd.Full != nil && nd.Clear != nil {
				v := *nd.Full + *nd.Clear
				sum = &v
			}
			if c.Side == "d" {
				p.HomeDet, p.Home = &nd, sum
			} else {
				p.AwayDet, p.Away = &nd, sum
			}
			c.Value = sum
		} else if c.Side == "d" {
			p.Home = c.Value
		} else {
			p.Away = c.Value
		}
		// Entering a finished lane value clears that player's live value (the lane is over).
		if c.Lane >= 0 && c.Value != nil {
			if c.Side == "d" {
				d.Live.Home = nil
			} else {
				d.Live.Away = nil
			}
		}
		return nil
	})
}

// PlayerChange is a substitution or an empty place on the scoring page.
type PlayerChange struct {
	Duel   int        `json:"souboj"`
	Side   string     `json:"strana"`
	Sub    *match.Sub `json:"stridani"`    // nil = no substitution
	Absent bool       `json:"nenastoupil"` // the place in the line-up is empty
}

// SetPlayer records a substitution (SŘ čl. 12) or an empty place (čl. 15) for one side of a duel.
func (a *App) SetPlayer(pc PlayerChange) error {
	return a.change(func(m *match.Match) error {
		if pc.Duel < 0 || pc.Duel >= len(m.Duels) {
			return fmt.Errorf("souboj %d neexistuje", pc.Duel+1)
		}
		d := &m.Duels[pc.Duel]
		var sub *match.Sub
		if pc.Sub != nil {
			s := *pc.Sub
			s.Name = strings.TrimSpace(s.Name)
			sub = &s
		}
		if pc.Absent && sub != nil {
			return errors.New("prázdné místo v sestavě nelze vystřídat (výklad STP 01/2009)")
		}
		switch pc.Side {
		case "d":
			d.HomeSub, d.HomeAbsent = sub, pc.Absent
			if pc.Absent {
				d.Live.Home = nil
			}
		case "h":
			d.AwaySub, d.AwayAbsent = sub, pc.Absent
			if pc.Absent {
				d.Live.Away = nil
			}
		default:
			return errors.New("strana musí být d nebo h")
		}
		return nil
	})
}

// SetInterrupted stops the match (full result table with "Utkání přerušeno", graphics forced on)
// or resumes it (the graphics return to what they showed before).
func (a *App) SetInterrupted(on bool) error {
	a.mt.mu.Lock()
	was := a.mt.cur != nil && a.mt.cur.Interrupted
	a.mt.mu.Unlock()
	if on == was {
		return nil
	}
	err := a.change(func(m *match.Match) error {
		if on {
			m.PrevMode, m.PrevMessage = m.Mode, m.Message
			m.Mode, m.Message, m.Interrupted = match.ModeResult, "", true
		} else {
			m.Mode, m.Message, m.Interrupted = m.PrevMode, m.PrevMessage, false
			if m.Mode == "" {
				m.Mode = match.ModeMatch
			}
			m.PrevMode, m.PrevMessage = "", ""
		}
		return nil
	})
	if err != nil {
		return err
	}
	if on {
		a.overlayBeforeStop.Store(a.overlayOn.Load())
		a.SetOverlay(true)
		a.Log.Print("utkání: přerušeno")
	} else {
		a.SetOverlay(a.overlayBeforeStop.Load())
		a.Log.Print("utkání: pokračuje")
	}
	return nil
}

func (a *App) SetPart(p int) error {
	return a.change(func(m *match.Match) error { m.CurrentPart = p; return nil })
}

func (a *App) SetMode(mode, message string) error {
	return a.change(func(m *match.Match) error {
		if m.Interrupted { // the scorer changed the graphics by hand: this ends the interruption view
			m.Interrupted, m.PrevMode, m.PrevMessage = false, "", ""
		}
		m.Mode, m.Message = mode, message
		return nil
	})
}

func (a *App) Undo() error {
	a.mt.mu.Lock()
	defer a.mt.mu.Unlock()
	if len(a.mt.history) == 0 {
		return errors.New("není co vrátit")
	}
	prev := a.mt.history[len(a.mt.history)-1]
	if err := a.mt.store.Save(a.mt.file, prev); err != nil {
		return err
	}
	a.mt.history = a.mt.history[:len(a.mt.history)-1]
	a.mt.cur = &prev
	a.redraw()
	a.markDirty("zapas:" + a.mt.file)
	return nil
}

func (a *App) OpenMatch(file string) error {
	m, err := a.mt.store.Load(file)
	if err != nil {
		return fmt.Errorf("utkání nelze načíst: %w", err)
	}
	m.Reshape(a.Config().Rules)
	if err := a.mt.store.Save(file, m); err != nil {
		return err
	}
	a.mt.mu.Lock()
	a.mt.cur, a.mt.file, a.mt.history = &m, filepath.Base(file), nil
	a.redraw()
	a.mt.mu.Unlock()
	a.Log.Printf("utkání: otevřeno %s", file)
	return nil
}

// CloseMatch stops showing the match (the file stays saved).
func (a *App) CloseMatch() {
	a.mt.mu.Lock()
	a.mt.cur, a.mt.file, a.mt.history = nil, "", nil
	a.mt.store.ClearCurrent()
	a.redraw()
	a.mt.mu.Unlock()
}

// reloadCurrentMatch re-reads the open match from disk (after a newer copy came from Google Drive).
func (a *App) reloadCurrentMatch() {
	a.mt.mu.Lock()
	defer a.mt.mu.Unlock()
	if a.mt.file == "" {
		if name, m, err := a.mt.store.Current(); err == nil { // a match may have arrived for a fresh install
			m.Reshape(a.Config().Rules)
			a.mt.cur, a.mt.file = &m, name
			a.redraw()
		}
		return
	}
	m, err := a.mt.store.Load(a.mt.file)
	if err != nil {
		return
	}
	m.Reshape(a.Config().Rules)
	a.mt.cur, a.mt.history = &m, nil
	a.redraw()
}

func (a *App) SavedMatches() []match.Saved { return a.mt.store.List() }
func (a *App) Rosters() []match.Roster     { return a.mt.store.Rosters() }

// ---------------------------------------------------------------- graphics

// redraw marks the graphics as changed; the next output frame renders them again.
func (a *App) redraw() { a.gfxGen.Add(1) }

// overlayFrame is called by the engine for every output frame; it renders only after a change.
func (a *App) overlayFrame() (*image.RGBA, uint64) {
	gen := a.gfxGen.Load()
	a.ovMu.Lock()
	defer a.ovMu.Unlock()
	if a.ovImg != nil && a.ovVer == gen {
		return a.ovImg, a.ovVer
	}
	a.ovImg, a.ovVer = a.renderOverlay(a.ren), gen
	return a.ovImg, a.ovVer
}

func (a *App) renderOverlay(ren *overlay.Renderer) *image.RGBA {
	if ren == nil {
		return nil
	}
	if !a.overlayOn.Load() {
		return ren.Blank()
	}
	cfg := a.Config()
	a.mt.mu.Lock()
	var m *match.Match
	if a.mt.cur != nil {
		c := *a.mt.cur
		m = &c
	}
	a.mt.mu.Unlock()
	if m == nil {
		return ren.Blank()
	}
	return ren.Score(overlay.ScoreView{Match: *m, Result: match.Compute(*m, cfg.Rules), Rules: cfg.Rules, Layout: cfg.Overlay.Layout})
}

// OverlayPreview renders the graphics alone (for the page, also when not streaming).
func (a *App) OverlayPreview() (*image.RGBA, error) {
	cfg := a.Config()
	ren, err := overlay.NewRenderer(cfg.Output.Width, cfg.Output.Height)
	if err != nil {
		return nil, err
	}
	return a.renderOverlay(ren), nil
}

// matchTitleFields fill the broadcast title placeholders.
func (a *App) matchTitleFields() map[string]string {
	a.mt.mu.Lock()
	defer a.mt.mu.Unlock()
	f := map[string]string{"{utkani}": "Kuželky – přímý přenos", "{domaci}": "", "{hoste}": "", "{soutez}": "", "{kolo}": ""}
	if m := a.mt.cur; m != nil {
		u := m.Home.Name + " – " + m.Away.Name
		if m.Competition != "" {
			prefix := m.Competition
			if m.Round != "" {
				prefix += ", " + m.Round + ". kolo"
			}
			u = prefix + ": " + u
		}
		f["{utkani}"], f["{domaci}"], f["{hoste}"], f["{soutez}"], f["{kolo}"] = u, m.Home.Name, m.Away.Name, m.Competition, m.Round
	}
	return f
}
