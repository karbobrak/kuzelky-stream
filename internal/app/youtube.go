package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/google"
)

// ytState is the YouTube API side of the app: the current broadcast (the video viewers watch) and
// what YouTube reports about it. The broadcast id is kept in data\prenos.json, so stopping the stream
// or restarting the app continues the same broadcast at the same address until it is ended.
type ytState struct {
	mu         sync.Mutex
	broadcast  *google.Broadcast
	health     google.StreamHealth
	ingest     google.Stream // address + key of the reusable stream
	lastErr    string
	logins     map[string]*google.Login
	activeFor  int    // consecutive polls with the stream active while the broadcast is not live
	lastIssues string // last logged YouTube remarks
}

type savedBroadcast struct {
	ID    string `json:"id"`
	Title string `json:"nazev"`
}

func (a *App) prenosPath() string { return filepath.Join(a.dir, "data", "prenos.json") }

// loadSavedBroadcast: the broadcast id from the (shared) settings; older versions kept it in
// data\prenos.json.
func (a *App) loadSavedBroadcast() string {
	if id := a.Config().YouTube.BroadcastID; id != "" {
		return id
	}
	var s savedBroadcast
	if b, err := os.ReadFile(a.prenosPath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s.ID
}

func (a *App) saveBroadcast(b *google.Broadcast) { a.saveBroadcastKind(b, "") }

func (a *App) saveBroadcastKind(b *google.Broadcast, kind string) {
	id := ""
	if b != nil {
		id = b.ID
	} else {
		kind = ""
	}
	_ = os.Remove(a.prenosPath()) // migrated into the settings
	y := a.Config().YouTube
	if y.BroadcastID != id || (kind != "" && y.BroadcastKind != kind) || (id == "" && y.BroadcastKind != "") {
		a.updateConfig(func(c *config.Config) {
			c.YouTube.BroadcastID = id
			if kind != "" || id == "" {
				c.YouTube.BroadcastKind = kind
			}
		})
	}
}

// Broadcast kinds.
const (
	KindMatch   = "zapas"
	KindNonstop = "nonstop"
)

// desiredKind: with nonstop broadcasting a match broadcast runs only while a match is open;
// without it every broadcast is a match broadcast (the original behaviour).
func (a *App) desiredKind(cfg config.Config) string {
	if !cfg.YouTube.Nonstop {
		return KindMatch
	}
	a.mt.mu.Lock()
	open := a.mt.cur != nil
	a.mt.mu.Unlock()
	if open {
		return KindMatch
	}
	return KindNonstop
}

func (a *App) broadcastTitle(cfg config.Config, kind string) string {
	if kind == KindNonstop {
		t := cfg.YouTube.NonstopTitle
		if strings.TrimSpace(t) == "" {
			t = cfg.YouTube.ChannelTitle
		}
		if strings.TrimSpace(t) == "" {
			t = "Kuželky – živě"
		}
		return expandTitle(t, time.Now(), a.matchTitleFields())
	}
	return expandTitle(cfg.YouTube.Title, time.Now(), a.matchTitleFields())
}

// endOnYouTube completes a broadcast that is live (it becomes a video); one that never went live
// stays in Studio as upcoming.
func (a *App) endOnYouTube(ctx context.Context, cl *google.Client, id string) error {
	cur, err := cl.GetBroadcast(ctx, id)
	if err != nil || cur == nil || cur.Ended() {
		return err
	}
	if cur.LifeCycle != "live" {
		a.Log.Printf("YouTube: přenos „%s“ nebyl živě (stav %s) – ponechávám ho ve Studiu", cur.Title, cur.LifeCycle)
		return nil
	}
	if err := cl.Transition(ctx, id, "complete"); err != nil {
		return fmt.Errorf("YouTube: přenos nelze ukončit: %w", err)
	}
	a.Log.Printf("YouTube: přenos „%s“ ukončen (zůstane jako video)", cur.Title)
	return nil
}

func ytClient(y config.YouTube) *google.Client {
	return &google.Client{ClientID: y.ClientID, ClientSecret: y.ClientSecret, RefreshToken: y.RefreshToken}
}

func (a *App) setYTError(err error) {
	a.yt.mu.Lock()
	defer a.yt.mu.Unlock()
	if err == nil {
		a.yt.lastErr = ""
		return
	}
	a.yt.lastErr = err.Error()
}

// expandTitle fills {datum}, {cas} and the match placeholders ({utkani}, {domaci}, …) in the title.
// YouTube allows at most 100 characters.
func expandTitle(t string, now time.Time, fields map[string]string) string {
	pairs := []string{
		"{datum}", fmt.Sprintf("%d. %d. %d", now.Day(), int(now.Month()), now.Year()),
		"{cas}", now.Format("15:04"),
	}
	for k, v := range fields {
		pairs = append(pairs, k, v)
	}
	out := strings.Join(strings.Fields(strings.NewReplacer(pairs...).Replace(t)), " ")
	if r := []rune(out); len(r) > 100 {
		out = string(r[:100])
	}
	return out
}

// prepareYouTube makes sure there is a reusable stream and a broadcast bound to it, and remembers
// the ingest address and key for the encoder. Called before the encoder starts (API mode only).
func (a *App) prepareYouTube(ctx context.Context, cfg config.Config) error {
	cl := ytClient(cfg.YouTube)
	st, err := cl.EnsureStream(ctx, cfg.YouTube.StreamID)
	if err != nil {
		return fmt.Errorf("YouTube stream: %w", err)
	}
	if st.ID != cfg.YouTube.StreamID {
		a.Log.Printf("YouTube: založen nový stream %s", st.ID)
		a.updateYouTubeConfig(func(y *config.YouTube) { y.StreamID = st.ID })
	}

	kind := a.desiredKind(cfg)
	var b *google.Broadcast
	if id := a.loadSavedBroadcast(); id != "" {
		if b, err = cl.GetBroadcast(ctx, id); err != nil {
			return fmt.Errorf("YouTube přenos: %w", err)
		}
		if b != nil && b.Ended() {
			b = nil
		}
		savedKind := cfg.YouTube.BroadcastKind
		if savedKind == "" {
			savedKind = KindMatch
		}
		if b != nil && savedKind != kind {
			// Match ↔ alley: end the current broadcast (a video), the stream itself goes on.
			if err := a.endOnYouTube(ctx, cl, b.ID); err != nil {
				return err
			}
			b = nil
		}
	}
	if b == nil {
		nb, err := cl.CreateBroadcast(ctx, a.broadcastTitle(cfg, kind), cfg.YouTube.Privacy)
		if err != nil {
			return fmt.Errorf("YouTube: nelze založit přenos: %w", err)
		}
		b = &nb
		a.Log.Printf("YouTube: založen přenos „%s“ %s (monitor %v, autostart %v)", b.Title, b.WatchURL(), b.MonitorStream, b.AutoStart)
	} else {
		a.Log.Printf("YouTube: pokračuji v přenosu „%s“ %s (stav %s)", b.Title, b.WatchURL(), b.LifeCycle)
	}
	// Binding is idempotent; doing it every time also repairs a broadcast unbound in Studio.
	if err := cl.Bind(ctx, b.ID, st.ID); err != nil {
		return fmt.Errorf("YouTube: nelze propojit přenos se streamem: %w", err)
	}
	a.saveBroadcastKind(b, kind)
	a.yt.mu.Lock()
	a.yt.broadcast, a.yt.ingest, a.yt.lastErr, a.yt.activeFor = b, st, "", 0
	a.yt.mu.Unlock()
	return nil
}

// monitorYouTube polls the broadcast and stream health while streaming in API mode.
func (a *App) monitorYouTube(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cfg := a.Config()
		if cfg.YouTube.Mode != "api" {
			return
		}
		a.pollYouTube(ctx, cfg)
	}
}

func (a *App) pollYouTube(ctx context.Context, cfg config.Config) {
	cl := ytClient(cfg.YouTube)
	a.yt.mu.Lock()
	cur := a.yt.broadcast
	a.yt.mu.Unlock()
	if cur == nil {
		return
	}
	h, err := cl.StreamHealth(ctx, cfg.YouTube.StreamID)
	if err != nil {
		a.setYTError(err)
		return
	}
	b, err := cl.GetBroadcast(ctx, cur.ID)
	if err != nil {
		a.setYTError(err)
		return
	}

	if b == nil || b.Ended() {
		// Ended or deleted outside the app (YouTube Studio). Keep viewers served: start a new one.
		a.Log.Print("YouTube: přenos byl ukončen mimo aplikaci – zakládám nový (bude mít novou adresu)")
		if err := a.prepareYouTube(ctx, cfg); err != nil {
			a.setYTError(err)
		}
		return
	}

	a.yt.mu.Lock()
	a.yt.broadcast, a.yt.health, a.yt.lastErr = b, h, ""
	needLive := b.LifeCycle != "live" && h.Status == "active"
	if needLive {
		a.yt.activeFor++
	} else {
		a.yt.activeFor = 0
	}
	activeFor := a.yt.activeFor
	a.yt.mu.Unlock()

	// Auto-start normally switches the broadcast to live by itself. If YouTube receives video for
	// ~45 s and still has not done it (the "Preparing stream" hang), switch it explicitly.
	// With the monitor stream on, YouTube only allows ready → testing → live.
	if needLive && activeFor >= 3 && (b.LifeCycle == "ready" || b.LifeCycle == "testing") {
		next := "live"
		if b.LifeCycle == "ready" && b.MonitorStream {
			next = "testing"
		}
		a.Log.Printf("YouTube: přenos je ve stavu %s (monitor %v, autostart %v), přestože data chodí – přepínám na %s",
			b.LifeCycle, b.MonitorStream, b.AutoStart, next)
		err := cl.Transition(ctx, b.ID, next)
		if err != nil && next == "live" && b.LifeCycle == "ready" && google.IsInvalidTransition(err) {
			// Some broadcasts insist on the testing step even with the monitor reported off.
			a.Log.Print("YouTube: přímý přechod na živé odmítnut – zkouším přes testovací stav")
			err = cl.Transition(ctx, b.ID, "testing")
		}
		if err != nil {
			a.Log.Printf("YouTube: přepnutí selhalo: %v", err)
			a.setYTError(err)
		}
	}
	// Log YouTube's remarks only when they change (they repeat on every poll).
	key := strings.Join(h.Issues, "\n")
	a.yt.mu.Lock()
	changed := key != a.yt.lastIssues
	a.yt.lastIssues = key
	a.yt.mu.Unlock()
	if changed {
		for _, issue := range h.Issues {
			a.Log.Printf("YouTube hlásí: %s", issue)
		}
	}
}

// StartMatchBroadcast starts broadcasting the open match: starts streaming, or – when the alley
// is already being broadcast nonstop – ends that broadcast and switches to a new one for the match
// (own link and video). The stream to YouTube itself is not interrupted.
func (a *App) StartMatchBroadcast(ctx context.Context) error {
	if !a.Running() {
		return a.Start()
	}
	cfg := a.Config()
	if cfg.YouTube.Mode != "api" || cfg.Output.Target != "youtube" {
		return nil
	}
	if cfg.YouTube.BroadcastKind == KindMatch && a.desiredKind(cfg) == KindMatch {
		return nil // already the match broadcast
	}
	return a.prepareYouTube(ctx, cfg)
}

// EndMatchBroadcast ends the match: its broadcast becomes a video, the score disappears and,
// with nonstop broadcasting, the alley goes on as a new broadcast. Without nonstop the streaming
// stops (as EndBroadcast).
func (a *App) EndMatchBroadcast(ctx context.Context) error {
	cfg := a.Config()
	a.CloseMatch()
	if !cfg.YouTube.Nonstop || cfg.YouTube.Mode != "api" || cfg.Output.Target != "youtube" {
		return a.EndBroadcast(ctx)
	}
	if a.Running() {
		return a.prepareYouTube(ctx, a.Config()) // ends the match broadcast, starts the alley one
	}
	// Not streaming: just end the match broadcast; the next start is the alley.
	if id := a.loadSavedBroadcast(); id != "" && cfg.YouTube.APIReady() {
		if err := a.endOnYouTube(ctx, ytClient(cfg.YouTube), id); err != nil {
			return err
		}
	}
	a.saveBroadcast(nil)
	return nil
}

// AutoStart begins broadcasting when the app starts, if nonstop broadcasting is on and ready.
func (a *App) AutoStart() {
	cfg := a.Config()
	if !cfg.YouTube.Nonstop || cfg.YouTube.Mode != "api" || !cfg.YouTube.APIReady() || cfg.Output.Target != "youtube" || a.Running() {
		return
	}
	a.Log.Print("nonstop vysílání: spouštím automaticky")
	if err := a.Start(); err != nil {
		a.Log.Printf("nonstop vysílání se nespustilo: %v", err)
	}
}

// EndBroadcast stops the stream and ends the broadcast on YouTube (the video becomes a recording).
// The next start creates a new broadcast with a new address.
func (a *App) EndBroadcast(ctx context.Context) error {
	a.Stop()
	cfg := a.Config()
	a.yt.mu.Lock()
	b := a.yt.broadcast
	a.yt.broadcast, a.yt.health = nil, google.StreamHealth{}
	a.yt.mu.Unlock()
	if b == nil {
		if id := a.loadSavedBroadcast(); id != "" {
			b = &google.Broadcast{ID: id}
		}
	}
	a.saveBroadcast(nil)
	if b == nil || cfg.YouTube.Mode != "api" || !cfg.YouTube.APIReady() {
		return nil
	}
	cl := ytClient(cfg.YouTube)
	cur, err := cl.GetBroadcast(ctx, b.ID)
	if err != nil {
		return err
	}
	if cur == nil || cur.Ended() {
		return nil
	}
	if cur.LifeCycle == "live" {
		if err := cl.Transition(ctx, b.ID, "complete"); err != nil {
			return fmt.Errorf("YouTube: přenos nelze ukončit: %w", err)
		}
		a.Log.Printf("YouTube: přenos „%s“ ukončen", cur.Title)
		return nil
	}
	// Never went live: YouTube cannot "complete" it; it stays in Studio as upcoming (harmless).
	a.Log.Printf("YouTube: přenos „%s“ nebyl živě (stav %s) – ponechávám ho ve Studiu", cur.Title, cur.LifeCycle)
	return nil
}

// ---------------------------------------------------------------- sign-in

// Sign-in kinds: Google refuses YouTube and Drive scopes in one request.
const (
	LoginYouTube = "youtube"
	LoginDrive   = "disk"
)

// BeginLogin returns the Google consent URL; redirect is this app's /oauth2/callback URL.
func (a *App) BeginLogin(redirect, kind string) (string, error) {
	cfg := a.Config()
	if cfg.YouTube.ClientID == "" || cfg.YouTube.ClientSecret == "" {
		return "", errors.New("nejdřív vyplňte a uložte Client ID a Client secret (Nastavení → YouTube → Přístup k API)")
	}
	scope := google.ScopeYouTube
	if kind == LoginDrive {
		scope = google.ScopeDriveFile
	}
	l := google.NewLogin(redirect, scope)
	a.yt.mu.Lock()
	if a.yt.logins == nil {
		a.yt.logins = map[string]*google.Login{}
	}
	for k, v := range a.yt.logins {
		if time.Since(v.Created) > 15*time.Minute {
			delete(a.yt.logins, k)
		}
	}
	a.yt.logins[l.State] = l
	a.yt.mu.Unlock()
	return l.AuthURL(cfg.YouTube.ClientID), nil
}

// FinishLogin handles the redirect back from Google and stores the sign-in in config.json.
func (a *App) FinishLogin(ctx context.Context, state, code string) (string, error) {
	a.yt.mu.Lock()
	l := a.yt.logins[state]
	delete(a.yt.logins, state)
	a.yt.mu.Unlock()
	if l == nil {
		return "", errors.New("neplatné nebo prošlé přihlášení – zkuste to znovu z Nastavení")
	}
	cfg := a.Config()
	refresh, scopes, err := l.Exchange(ctx, cfg.YouTube.ClientID, cfg.YouTube.ClientSecret, code)
	if err != nil {
		return "", err
	}
	if !google.HasScope(scopes, l.Scope) {
		return "", errors.New("při přihlášení nebyl povolen požadovaný přístup – zkuste to znovu a nechte oprávnění zaškrtnuté")
	}
	if l.Scope == google.ScopeDriveFile {
		cl := &google.Client{ClientID: cfg.YouTube.ClientID, ClientSecret: cfg.YouTube.ClientSecret, RefreshToken: refresh}
		acct, err := cl.DriveAccount(ctx)
		if err != nil {
			return "", fmt.Errorf("%w (je v Google Cloud zapnuté Google Drive API?)", err)
		}
		a.updateConfig(func(c *config.Config) {
			if c.Cloud.Account != acct {
				c.Cloud.FolderID, c.Cloud.SheetID = "", "" // another account: its own folder and spreadsheet
			}
			c.Cloud.RefreshToken, c.Cloud.Account, c.Cloud.Scopes = refresh, acct, scopes
		})
		a.Log.Printf("Google Disk: přihlášen účet %s", acct)
		a.cloudReconcile()
		return "Google Disk – " + acct, nil
	}

	y := cfg.YouTube
	y.RefreshToken = refresh
	ch, err := ytClient(y).MyChannel(ctx)
	if err != nil {
		return "", err
	}
	a.updateConfig(func(c *config.Config) {
		if c.YouTube.ChannelID != ch.ID {
			c.YouTube.StreamID = "" // a different channel needs its own stream
		}
		c.YouTube.RefreshToken, c.YouTube.ChannelID, c.YouTube.ChannelTitle = refresh, ch.ID, ch.Title
	})
	a.Log.Printf("YouTube: přihlášen kanál „%s“", ch.Title)
	return "YouTube – " + ch.Title, nil
}

// LogoutDrive forgets the Google Drive sign-in (backup and spreadsheet stop).
func (a *App) LogoutDrive() {
	a.updateConfig(func(c *config.Config) {
		c.Cloud.RefreshToken, c.Cloud.Account, c.Cloud.Scopes = "", "", ""
	})
	a.Log.Print("Google Disk: odhlášeno")
}

func (a *App) Logout() {
	a.updateConfig(func(c *config.Config) {
		c.YouTube.RefreshToken, c.YouTube.ChannelID, c.YouTube.ChannelTitle, c.YouTube.StreamID = "", "", "", ""
	})
	a.saveBroadcast(nil)
	a.yt.mu.Lock()
	a.yt.broadcast = nil
	a.yt.mu.Unlock()
	a.Log.Print("YouTube: odhlášeno")
}

// updateYouTubeConfig changes app-managed YouTube fields and saves config.json.
func (a *App) updateYouTubeConfig(f func(*config.YouTube)) {
	a.updateConfig(func(c *config.Config) { f(&c.YouTube) })
}

// updateConfig changes app-managed settings and saves config.json.
func (a *App) updateConfig(f func(*config.Config)) {
	a.mu.Lock()
	old := a.cfg
	f(&a.cfg)
	a.stampShared(old, &a.cfg)
	c := a.cfg
	a.mu.Unlock()
	if err := config.Save(a.cfgPath, c); err != nil {
		a.Log.Printf("nelze uložit %s: %v", a.cfgPath, err)
	}
	a.sharedChanged(old, c)
}

// ---------------------------------------------------------------- status

type YTStatus struct {
	Mode        string   `json:"rezim"`
	SignedIn    bool     `json:"prihlaseno"`
	Channel     string   `json:"kanal,omitempty"`
	ChannelLive string   `json:"kanalLive,omitempty"`
	Title       string   `json:"nazev,omitempty"`
	WatchURL    string   `json:"odkaz,omitempty"`
	LifeCycle   string   `json:"stavPrenosu,omitempty"`
	Privacy     string   `json:"soukromi,omitempty"`
	StreamState string   `json:"stavStreamu,omitempty"`
	Health      string   `json:"zdravi,omitempty"`
	Issues      []string `json:"problemy,omitempty"`
	Error       string   `json:"chyba,omitempty"`
	Kind        string   `json:"druh,omitempty"` // zapas | nonstop
	Nonstop     bool     `json:"nonstop"`
}

func (a *App) ytStatus() YTStatus {
	cfg := a.Config()
	s := YTStatus{Mode: cfg.YouTube.Mode, SignedIn: cfg.YouTube.APIReady(), Channel: cfg.YouTube.ChannelTitle,
		Kind: cfg.YouTube.BroadcastKind, Nonstop: cfg.YouTube.Nonstop}
	if cfg.YouTube.ChannelID != "" {
		s.ChannelLive = "https://www.youtube.com/channel/" + cfg.YouTube.ChannelID + "/live"
	}
	a.yt.mu.Lock()
	defer a.yt.mu.Unlock()
	if b := a.yt.broadcast; b != nil {
		s.Title, s.WatchURL, s.LifeCycle, s.Privacy = b.Title, b.WatchURL(), b.LifeCycle, b.Privacy
	} else if cfg.YouTube.Mode == "api" {
		// Not streaming now, but a broadcast from earlier continues on the next start.
		if id := cfg.YouTube.BroadcastID; id != "" {
			s.WatchURL = (google.Broadcast{ID: id}).WatchURL()
		}
	}
	s.StreamState, s.Health, s.Issues, s.Error = a.yt.health.Status, a.yt.health.Health, a.yt.health.Issues, a.yt.lastErr
	return s
}
