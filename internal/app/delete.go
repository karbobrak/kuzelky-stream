package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/fsutil"
	"kuzelkystream/internal/google"
)

// Deleting a match or a roster removes the local file and records a tombstone in data\smazane.json.
// The cloud worker then moves the Drive copy to the bin, removes the match's rows from the results
// spreadsheet and uploads the tombstones as smazane.json, so other notebooks delete their copy too
// instead of uploading it back. An item changed after its deletion time (e.g. a team that plays
// again) counts as new and its tombstone is dropped.

const tombFile = "smazane.json"

type tombstone struct {
	Time  int64 `json:"cas"`               // when it was deleted, unix ms
	Sheet bool  `json:"tabulka,omitempty"` // rows already removed from the results spreadsheet
}

func (a *App) tombPath() string { return filepath.Join(a.dir, "data", tombFile) }

func (a *App) loadTombs() map[string]tombstone {
	m := map[string]tombstone{}
	if b, err := os.ReadFile(a.tombPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (a *App) saveTombs(m map[string]tombstone) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(filepath.Dir(a.tombPath()), 0o755); err != nil {
		return err
	}
	tmp := a.tombPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return fsutil.Replace(tmp, a.tombPath())
}

// tombstoned reports whether key was deleted (used to avoid reusing a deleted match's file name).
func (a *App) tombstoned(key string) bool {
	a.cl.tombMu.Lock()
	defer a.cl.tombMu.Unlock()
	_, ok := a.loadTombs()[key]
	return ok
}

func (a *App) addTomb(key string) error {
	a.cl.tombMu.Lock()
	defer a.cl.tombMu.Unlock()
	m := a.loadTombs()
	m[key] = tombstone{Time: time.Now().UnixMilli()}
	return a.saveTombs(m)
}

// DeleteMatch deletes a saved match (here, on Google Drive and in the results spreadsheet).
func (a *App) DeleteMatch(file string) error {
	file = filepath.Base(file)
	p := a.mt.store.MatchPath(file)
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("utkání %s neexistuje", file)
	}
	a.mt.mu.Lock()
	open := a.mt.file == file
	a.mt.mu.Unlock()
	if open {
		a.CloseMatch()
	}
	if err := os.Remove(p); err != nil {
		return fmt.Errorf("utkání nelze smazat: %w", err)
	}
	if err := a.addTomb("zapas:" + file); err != nil {
		return err
	}
	a.Log.Printf("utkání: smazáno %s", file)
	a.cloudReconcile()
	return nil
}

// DeleteRoster deletes a saved team (its roster file) here and on Google Drive.
func (a *App) DeleteRoster(file string) error {
	file = filepath.Base(file)
	if !strings.HasSuffix(file, ".json") {
		return errors.New("neplatný tým")
	}
	if err := os.Remove(a.mt.store.RosterPath(file)); err != nil {
		return fmt.Errorf("tým nelze smazat: %w", err)
	}
	if err := a.addTomb("tym:" + file); err != nil {
		return err
	}
	a.Log.Printf("soupiska: smazán tým %s", file)
	a.cloudReconcile()
	return nil
}

// applyTombstones merges the tombstones with the ones on Drive, carries them out (local file,
// Drive file, spreadsheet rows) and returns the Drive listing without the deleted files.
func (a *App) applyTombstones(ctx context.Context, cl *google.Client, c config.Config, folder string,
	files []google.DriveFile) ([]google.DriveFile, error) {
	a.cl.tombMu.Lock()
	defer a.cl.tombMu.Unlock()
	tombs := a.loadTombs()
	remote := map[string]tombstone{}
	var remoteID string
	byName := map[string]google.DriveFile{}
	for _, f := range files {
		byName[f.Name] = f
		if f.Name == tombFile {
			remoteID = f.ID
		}
	}
	if remoteID != "" {
		body, err := cl.Download(ctx, remoteID)
		if err != nil {
			return files, err
		}
		_ = json.Unmarshal(body, &remote)
	}
	for k, t := range remote {
		l, ok := tombs[k]
		switch {
		case !ok || t.Time > l.Time:
			tombs[k] = t
		case t.Time == l.Time:
			l.Sheet = l.Sheet || t.Sheet
			tombs[k] = l
		}
	}
	if len(tombs) == 0 {
		return files, nil
	}

	gone := map[string]bool{} // Drive names deleted in this pass
	removedLocal := 0
	for key, t := range tombs {
		path, name, typ, file := a.localFile(key)
		if typ != "zapas" && typ != "tym" {
			delete(tombs, key)
			continue
		}
		limit := time.UnixMilli(t.Time) // both times come from the clock of the computer that made the change
		st, localErr := os.Stat(path)
		r, onDrive := byName[name]
		if (localErr == nil && st.ModTime().After(limit)) || (onDrive && remoteTime(r).After(limit)) {
			delete(tombs, key) // changed after the deletion: it lives again
			continue
		}
		if localErr == nil {
			if typ == "zapas" {
				a.mt.mu.Lock()
				open := a.mt.file == file
				a.mt.mu.Unlock()
				if open {
					a.CloseMatch()
				}
			}
			if os.Remove(path) == nil {
				removedLocal++
			}
		}
		if onDrive {
			if err := cl.Trash(ctx, r.ID); err != nil {
				return files, err
			}
			gone[name] = true
			a.cl.mu.Lock()
			delete(a.cl.ids, name)
			a.cl.mu.Unlock()
		}
		if typ == "zapas" && !t.Sheet && c.Cloud.Sheet && c.Cloud.SheetID != "" {
			if err := a.deleteSheetRows(ctx, cl, c, file); err != nil {
				_ = a.saveTombs(tombs)
				return files, fmt.Errorf("tabulka výsledků: %w", err)
			}
			t.Sheet = true
			tombs[key] = t
		}
	}
	if err := a.saveTombs(tombs); err != nil {
		return files, err
	}
	if removedLocal > 0 {
		a.Log.Printf("Google Disk: smazáno %d utkání/týmů podle jiného počítače", removedLocal)
	}
	if len(gone) > 0 {
		a.Log.Printf("Google Disk: %d smazaných souborů přesunuto do koše", len(gone))
	}

	// Share the tombstones with the other notebooks.
	if c.Cloud.Backup {
		want, _ := json.Marshal(tombs)
		have, _ := json.Marshal(remote)
		if string(want) != string(have) {
			body, _ := json.MarshalIndent(tombs, "", "  ")
			id, err := cl.Upload(ctx, folder, remoteID, tombFile, body, map[string]string{"typ": "smazane"})
			if err != nil {
				return files, err
			}
			a.cl.mu.Lock()
			if a.cl.ids != nil {
				a.cl.ids[tombFile] = id
			}
			a.cl.mu.Unlock()
		}
	}

	var out []google.DriveFile
	for _, f := range files {
		if !gone[f.Name] {
			out = append(out, f)
		}
	}
	return out, nil
}

// deleteSheetRows removes a match's rows from both tabs of the results spreadsheet.
func (a *App) deleteSheetRows(ctx context.Context, cl *google.Client, c config.Config, file string) error {
	ok, err := cl.SpreadsheetExists(ctx, c.Cloud.SheetID)
	if err != nil || !ok {
		return err
	}
	tabs, err := cl.SheetIDs(ctx, c.Cloud.SheetID)
	if err != nil {
		return err
	}
	for _, t := range []struct {
		tab    string
		keyCol int
		match  func(string) bool
	}{
		{tabMatches, len(matchHeader()) - 1, func(k string) bool { return k == file }},
		{tabPlayers, len(playerHeader(c.Rules.Lanes)) - 1, func(k string) bool { return strings.HasPrefix(k, file+"#") }},
	} {
		sid, ok := tabs[t.tab]
		if !ok {
			continue
		}
		col, err := cl.GetColumn(ctx, c.Cloud.SheetID, "'"+t.tab+"'!"+colName(t.keyCol)+":"+colName(t.keyCol))
		if err != nil {
			return err
		}
		var rows []int
		for i, k := range col {
			if i > 0 && t.match(strings.TrimSpace(k)) { // row 1 is the header
				rows = append(rows, i+1)
			}
		}
		if err := cl.DeleteRows(ctx, c.Cloud.SheetID, sid, rows); err != nil {
			return err
		}
	}
	return nil
}
