package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/google"
	"kuzelkystream/internal/match"
	"kuzelkystream/internal/overlay"
)

// Cloud backup: every change marks an item dirty; a worker uploads dirty items to the app folder on
// Google Drive ~10 s later (and retries while offline). A reconcile at start-up and after sign-in
// uploads what Drive lacks and downloads matches/rosters the computer lacks (e.g. a new notebook).
//
// Drive files: zapas_<file>.json, tym_<file>.json, nastaveni-grafiky.json, each with appProperties
// typ (zapas|tym|grafika), soubor (local file name) and zmeneno (local change time, unix ms).

const (
	folderName   = "KuzelkyStream"
	gfxFile      = "nastaveni-grafiky.json"
	syncDelay    = 10 * time.Second
	retryDelay   = time.Minute
	sheetTitle   = "Kuželky – výsledky"
	tabMatches   = "Utkání"
	tabPlayers   = "Hráči"
	downloadSkew = 2 * time.Second
)

type cloudState struct {
	mu        sync.Mutex
	dirty     map[string]bool // "zapas:<file>", "tym:<file>", "grafika"
	reconcile bool
	ids       map[string]string // Drive name → file id
	lastSync  time.Time
	lastErr   string
	wake      chan struct{}
}

// gfxSettings is what travels as "nastaveni-grafiky.json".
type gfxSettings struct {
	Layout  overlay.Layout            `json:"rozlozeni"`
	Presets map[string]overlay.Layout `json:"predvolby"`
	Picture config.Picture            `json:"obraz"`
	Rules   match.Rules               `json:"pravidla"`
}

func (a *App) cloudReady(c config.Config) bool {
	return (c.Cloud.Backup || c.Cloud.Sheet) && driveSignedIn(c)
}

// driveSignedIn: the app has a Google Drive sign-in (separate from the YouTube one).
func driveSignedIn(c config.Config) bool {
	return c.YouTube.ClientID != "" && c.YouTube.ClientSecret != "" && c.Cloud.RefreshToken != "" &&
		google.HasScope(c.Cloud.Scopes, google.ScopeDriveFile)
}

func driveClient(c config.Config) *google.Client {
	return &google.Client{ClientID: c.YouTube.ClientID, ClientSecret: c.YouTube.ClientSecret, RefreshToken: c.Cloud.RefreshToken}
}

func (a *App) markDirty(keys ...string) {
	a.cl.mu.Lock()
	if a.cl.dirty == nil {
		a.cl.dirty = map[string]bool{}
	}
	for _, k := range keys {
		a.cl.dirty[k] = true
	}
	a.cl.mu.Unlock()
	a.cloudWake()
}

func (a *App) cloudWake() {
	select {
	case a.cl.wake <- struct{}{}:
	default:
	}
}

// cloudReconcile asks the worker for a full comparison with Drive.
func (a *App) cloudReconcile() {
	a.cl.mu.Lock()
	a.cl.reconcile = true
	a.cl.mu.Unlock()
	a.cloudWake()
}

// RunCloud is the sync worker; it runs for the lifetime of the app.
func (a *App) RunCloud(ctx context.Context) {
	a.cloudReconcile()
	t := time.NewTicker(retryDelay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.cl.wake:
			// Collect more changes before uploading (typing a lane result changes the match often).
			select {
			case <-ctx.Done():
				return
			case <-time.After(syncDelay):
			}
		case <-t.C:
		}
		a.cloudSync(ctx)
	}
}

func (a *App) cloudSync(ctx context.Context) {
	c := a.Config()
	if !a.cloudReady(c) {
		return
	}
	a.cl.mu.Lock()
	dirty, reconcile := a.cl.dirty, a.cl.reconcile
	a.cl.dirty, a.cl.reconcile = map[string]bool{}, false
	a.cl.mu.Unlock()
	if len(dirty) == 0 && !reconcile {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	err := a.cloudSyncItems(ctx, c, dirty, reconcile)
	a.cl.mu.Lock()
	if err != nil {
		// Keep everything for the next attempt.
		for k := range dirty {
			a.cl.dirty[k] = true
		}
		a.cl.reconcile = a.cl.reconcile || reconcile
		if a.cl.lastErr != err.Error() {
			a.Log.Printf("záloha na Google Disk: %v", err)
		}
		a.cl.lastErr = err.Error()
	} else {
		a.cl.lastErr, a.cl.lastSync = "", time.Now()
	}
	a.cl.mu.Unlock()
}

func (a *App) cloudSyncItems(ctx context.Context, c config.Config, dirty map[string]bool, reconcile bool) error {
	cl := driveClient(c)
	folder, err := cl.EnsureFolder(ctx, c.Cloud.FolderID, folderName)
	if err != nil {
		if errors.Is(err, google.ErrAuth) {
			return err
		}
		return fmt.Errorf("složka na Disku: %w (je v Google Cloud zapnuté Google Drive API?)", err)
	}
	if folder != c.Cloud.FolderID {
		a.updateConfig(func(cc *config.Config) { cc.Cloud.FolderID = folder })
	}
	a.cl.mu.Lock()
	needList := reconcile || a.cl.ids == nil
	a.cl.mu.Unlock()
	if needList {
		files, err := cl.ListFolder(ctx, folder)
		if err != nil {
			return err
		}
		ids := map[string]string{}
		for _, f := range files {
			ids[f.Name] = f.ID
		}
		a.cl.mu.Lock()
		a.cl.ids = ids
		a.cl.mu.Unlock()
		if reconcile {
			if err := a.reconcileFiles(ctx, cl, files, dirty); err != nil {
				return err
			}
		}
	}

	if c.Cloud.Backup {
		for k := range dirty {
			if err := a.uploadItem(ctx, cl, folder, k); err != nil {
				return err
			}
		}
	}
	if c.Cloud.Sheet {
		for k := range dirty {
			if file, ok := strings.CutPrefix(k, "zapas:"); ok {
				if err := a.updateSheet(ctx, cl, folder, file); err != nil {
					return fmt.Errorf("tabulka výsledků: %w (je v Google Cloud zapnuté Google Sheets API?)", err)
				}
			}
		}
	}
	return nil
}

// localFile returns the local path and drive name of a dirty key.
func (a *App) localFile(key string) (path, driveName, typ, file string) {
	data := filepath.Join(a.dir, "data")
	switch {
	case key == "grafika":
		return "", gfxFile, "grafika", gfxFile
	case strings.HasPrefix(key, "zapas:"):
		f := strings.TrimPrefix(key, "zapas:")
		return filepath.Join(data, "zapasy", f), "zapas_" + f, "zapas", f
	default:
		f := strings.TrimPrefix(key, "tym:")
		return filepath.Join(data, "tymy", f), "tym_" + f, "tym", f
	}
}

func (a *App) uploadItem(ctx context.Context, cl *google.Client, folder, key string) error {
	path, name, typ, file := a.localFile(key)
	var data []byte
	var mod time.Time
	if typ == "grafika" {
		c := a.Config()
		data, _ = json.MarshalIndent(gfxSettings{Layout: c.Overlay.Layout, Presets: c.Overlay.Presets,
			Picture: c.Picture, Rules: c.Rules}, "", "  ")
		mod = time.Now()
	} else {
		var err error
		if data, err = os.ReadFile(path); err != nil {
			return nil // deleted locally meanwhile
		}
		st, _ := os.Stat(path)
		mod = st.ModTime()
	}
	a.cl.mu.Lock()
	id := a.cl.ids[name]
	a.cl.mu.Unlock()
	props := map[string]string{"typ": typ, "soubor": file, "zmeneno": strconv.FormatInt(mod.UnixMilli(), 10)}
	newID, err := cl.Upload(ctx, folder, id, name, data, props)
	if err != nil {
		return err
	}
	a.cl.mu.Lock()
	a.cl.ids[name] = newID
	a.cl.mu.Unlock()
	return nil
}

// reconcileFiles compares Drive with the computer: newer local files are queued for upload,
// matches and rosters missing or older locally are downloaded.
func (a *App) reconcileFiles(ctx context.Context, cl *google.Client, remote []google.DriveFile, dirty map[string]bool) error {
	remoteBy := map[string]google.DriveFile{}
	for _, f := range remote {
		remoteBy[f.Name] = f
	}
	data := filepath.Join(a.dir, "data")
	// Local → Drive.
	for _, sub := range []string{"zapasy", "tymy"} {
		files, _ := filepath.Glob(filepath.Join(data, sub, "*.json"))
		for _, p := range files {
			base := filepath.Base(p)
			key, name := "zapas:"+base, "zapas_"+base
			if sub == "tymy" {
				key, name = "tym:"+base, "tym_"+base
			}
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			r, ok := remoteBy[name]
			if !ok || st.ModTime().After(remoteTime(r).Add(downloadSkew)) {
				dirty[key] = true
			}
		}
	}
	if _, ok := remoteBy[gfxFile]; !ok {
		dirty["grafika"] = true
	}
	// Drive → local (matches and rosters only; graphics settings are restored on request).
	got := 0
	for _, r := range remote {
		var dir string
		switch r.Props["typ"] {
		case "zapas":
			dir = "zapasy"
		case "tym":
			dir = "tymy"
		default:
			continue
		}
		file := filepath.Base(r.Props["soubor"])
		if file == "." || file == "" || !strings.HasSuffix(file, ".json") {
			continue
		}
		p := filepath.Join(data, dir, file)
		if st, err := os.Stat(p); err == nil && !remoteTime(r).After(st.ModTime().Add(downloadSkew)) {
			continue
		}
		body, err := cl.Download(ctx, r.ID)
		if err != nil {
			return err
		}
		if !json.Valid(body) {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, body, 0o644); err != nil {
			return err
		}
		_ = os.Chtimes(p, time.Now(), remoteTime(r)) // same change time on both sides
		got++
	}
	if got > 0 {
		a.Log.Printf("Google Disk: staženo %d souborů (utkání, soupisky)", got)
		a.reloadCurrentMatch()
	}
	return nil
}

// remoteTime is when the file was last changed on the computer that uploaded it.
func remoteTime(f google.DriveFile) time.Time {
	if ms, err := strconv.ParseInt(f.Props["zmeneno"], 10, 64); err == nil {
		return time.UnixMilli(ms)
	}
	return f.Modified
}

// RestoreGraphics downloads the graphics settings (layout, presets, crop, rules) from Drive.
func (a *App) RestoreGraphics(ctx context.Context) error {
	c := a.Config()
	if !a.cloudReady(c) {
		return errors.New("záloha na Disk není připravená – přihlaste Google Disk (Nastavení → Záloha a výsledky)")
	}
	cl := driveClient(c)
	folder, err := cl.EnsureFolder(ctx, c.Cloud.FolderID, folderName)
	if err != nil {
		return err
	}
	files, err := cl.ListFolder(ctx, folder)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.Name != gfxFile {
			continue
		}
		body, err := cl.Download(ctx, f.ID)
		if err != nil {
			return err
		}
		var g gfxSettings
		if err := json.Unmarshal(body, &g); err != nil {
			return fmt.Errorf("nastavení grafiky na Disku je poškozené: %w", err)
		}
		nc := a.Config()
		nc.Overlay.Layout, nc.Overlay.Presets, nc.Picture, nc.Rules = g.Layout, g.Presets, g.Picture, g.Rules
		_, err = a.ApplyConfig(nc)
		if err == nil {
			a.Log.Print("Google Disk: obnoveno nastavení grafiky, výřezu a pravidel")
		}
		return err
	}
	return errors.New("na Disku zatím žádné nastavení grafiky není")
}

// FlushCloud uploads pending changes now (when the app is closing).
func (a *App) FlushCloud(ctx context.Context) { a.cloudSync(ctx) }

// SyncNow runs a full comparison right away (button in the settings).
func (a *App) SyncNow(ctx context.Context) error {
	c := a.Config()
	if !a.cloudReady(c) {
		return errors.New("záloha na Disk není připravená – přihlaste Google Disk (Nastavení → Záloha a výsledky)")
	}
	a.cl.mu.Lock()
	a.cl.reconcile = true
	a.cl.mu.Unlock()
	a.cloudSync(ctx)
	a.cl.mu.Lock()
	defer a.cl.mu.Unlock()
	if a.cl.lastErr != "" {
		return errors.New(a.cl.lastErr)
	}
	return nil
}

type CloudStatus struct {
	Backup         bool   `json:"zaloha"`
	Sheet          bool   `json:"tabulka"`
	Ready          bool   `json:"pripraveno"`
	NeedLogin      bool   `json:"chybiPrihlaseni"`
	Account        string `json:"ucet,omitempty"`
	LastSync       string `json:"posledni,omitempty"`
	Pending        int    `json:"cekajici"`
	Error          string `json:"chyba,omitempty"`
	FolderURL      string `json:"slozka,omitempty"`
	SpreadsheetURL string `json:"tabulkaOdkaz,omitempty"`
}

func (a *App) CloudStatus() CloudStatus {
	c := a.Config()
	s := CloudStatus{Backup: c.Cloud.Backup, Sheet: c.Cloud.Sheet, Ready: a.cloudReady(c),
		NeedLogin: !driveSignedIn(c), Account: c.Cloud.Account}
	if c.Cloud.FolderID != "" {
		s.FolderURL = "https://drive.google.com/drive/folders/" + c.Cloud.FolderID
	}
	if c.Cloud.SheetID != "" {
		s.SpreadsheetURL = google.SpreadsheetURL(c.Cloud.SheetID)
	}
	a.cl.mu.Lock()
	defer a.cl.mu.Unlock()
	if !a.cl.lastSync.IsZero() {
		s.LastSync = a.cl.lastSync.Format("15:04:05")
	}
	s.Pending, s.Error = len(a.cl.dirty), a.cl.lastErr
	return s
}
