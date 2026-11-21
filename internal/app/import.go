package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"kuzelkystream/internal/cka"
	"kuzelkystream/internal/match"
)

// Live results from vysledky.kuzelky.cz: a match linked to its page there is read every
// ImportSeconds (default 60) while automatic reading is on. The home club has to fill the official
// report during the match, so the stream follows it and nobody has to type the scores twice.

type importState struct {
	mu       sync.Mutex
	last     time.Time // last successful read
	lastErr  string
	status   string // status of the match on the web
	lastPart int    // part being played at the previous read (auto part switching)
	wake     chan struct{}
}

// ImportStatus is shown on the match pages.
type ImportStatus struct {
	Last     string `json:"posledni,omitempty"`
	Error    string `json:"chyba,omitempty"`
	Status   string `json:"stav,omitempty"` // připravuje se | probíhá | dohráno
	Interval int    `json:"interval"`
}

func (a *App) importStatus() ImportStatus {
	interval := a.importInterval()
	a.imp.mu.Lock()
	defer a.imp.mu.Unlock()
	s := ImportStatus{Error: a.imp.lastErr, Status: a.imp.status, Interval: interval}
	if !a.imp.last.IsZero() {
		s.Last = a.imp.last.Format("15:04:05")
	}
	return s
}

func (a *App) importInterval() int {
	if n := a.Config().App.ImportSeconds; n > 0 {
		return n
	}
	return 60
}

func (a *App) importWake() {
	select {
	case a.imp.wake <- struct{}{}:
	default:
	}
}

// RunImport reads the linked match periodically; it runs for the lifetime of the app.
func (a *App) RunImport(ctx context.Context) {
	for {
		_ = a.importOnce(ctx, false)
		select {
		case <-ctx.Done():
			return
		case <-a.imp.wake:
		case <-time.After(time.Duration(a.importInterval()) * time.Second):
		}
	}
}

var errUnchanged = errors.New("beze změny")

var statusText = map[string]string{cka.StatusPreparation: "připravuje se", cka.StatusInProgress: "probíhá", cka.StatusFinished: "dohráno"}

// importOnce reads the linked match (only with automatic reading on, unless forced).
func (a *App) importOnce(ctx context.Context, force bool) error {
	a.mt.mu.Lock()
	var url string
	if a.mt.cur != nil && (a.mt.cur.AutoImport || force) {
		url = a.mt.cur.ResultsURL
	}
	a.mt.mu.Unlock()
	if url == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cm, err := cka.Fetch(ctx, url)
	if err != nil {
		a.importFailed(err)
		return err
	}
	return a.importApply(cm, url)
}

func (a *App) importFailed(err error) {
	a.imp.mu.Lock()
	changed := a.imp.lastErr != err.Error()
	a.imp.lastErr = err.Error()
	a.imp.mu.Unlock()
	if changed {
		a.Log.Printf("výsledky ČKA: %v", err)
	}
}

// importApply merges the published match into the open one (if it is still the linked match).
func (a *App) importApply(cm *cka.Match, url string) error {
	rules := a.Config().Rules
	a.imp.mu.Lock()
	prevPart := a.imp.lastPart
	a.imp.mu.Unlock()
	var livePart int
	var lineupChanged bool
	err := a.change(func(m *match.Match) error {
		if m.ResultsURL != url {
			return errUnchanged // another match was opened meanwhile
		}
		before, _ := json.Marshal(m)
		names := lineup(*m)
		livePart = cm.Apply(m, rules)
		if livePart > 0 && livePart != prevPart && !m.Interrupted {
			m.CurrentPart = livePart // follow the play, but keep a part chosen by hand until it moves on
		}
		after, _ := json.Marshal(m)
		if string(before) == string(after) {
			return errUnchanged
		}
		lineupChanged = lineup(*m) != names
		return nil
	})
	a.imp.mu.Lock()
	a.imp.last, a.imp.lastErr = time.Now(), ""
	newStatus := statusText[cm.Status]
	statusChanged := newStatus != a.imp.status
	a.imp.status = newStatus
	if livePart > 0 {
		a.imp.lastPart = livePart
	}
	a.imp.mu.Unlock()
	if errors.Is(err, errUnchanged) {
		err = nil
	} else if err == nil {
		if lineupChanged {
			a.mt.mu.Lock()
			m := *a.mt.cur
			a.mt.mu.Unlock()
			a.saveRosters(m)
		}
	}
	if err != nil {
		a.importFailed(err)
		return err
	}
	if statusChanged {
		a.Log.Printf("výsledky ČKA: zápas %s", newStatus)
	}
	return nil
}

func lineup(m match.Match) string {
	var b strings.Builder
	b.WriteString(m.Home.Name + "|" + m.Away.Name)
	for _, d := range m.Duels {
		b.WriteString("|" + d.HomePlayer + "|" + d.AwayPlayer)
	}
	return b.String()
}

// SetResultsSource links the open match to its page on vysledky.kuzelky.cz ("" = unlink) and
// switches automatic reading on or off; with it on, the match is read right away.
func (a *App) SetResultsSource(raw string, auto bool) error {
	url := ""
	if strings.TrimSpace(raw) != "" {
		u, err := cka.CheckURL(raw)
		if err != nil {
			return err
		}
		url = u
	}
	if url == "" {
		auto = false
	}
	err := a.change(func(m *match.Match) error {
		m.ResultsURL, m.AutoImport = url, auto
		return nil
	})
	if err != nil {
		return err
	}
	a.imp.mu.Lock()
	a.imp.lastErr, a.imp.status, a.imp.lastPart = "", "", 0
	a.imp.mu.Unlock()
	if auto {
		a.importWake()
	}
	return nil
}

// ImportNow reads the linked match immediately (button "Načíst teď").
func (a *App) ImportNow(ctx context.Context) error { return a.importOnce(ctx, true) }

// NewMatchFromURL creates a new match from its page on vysledky.kuzelky.cz with automatic reading on.
func (a *App) NewMatchFromURL(ctx context.Context, raw string) error {
	url, err := cka.CheckURL(raw)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cm, err := cka.Fetch(ctx, url)
	if err != nil {
		return err
	}
	rules := a.Config().Rules
	m := match.New(rules)
	cm.Apply(&m, rules)
	if m.Home.Name == "" || m.Away.Name == "" {
		return errors.New("zápas na webu nemá vyplněná družstva")
	}
	m.ResultsURL, m.AutoImport = url, true
	if err := a.NewMatch(m); err != nil { // creates it without results …
		return err
	}
	a.imp.mu.Lock()
	a.imp.lastErr, a.imp.status, a.imp.lastPart = "", "", 0
	a.imp.mu.Unlock()
	return a.importApply(cm, url) // … which come in here
}
