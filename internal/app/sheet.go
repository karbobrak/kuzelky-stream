package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/google"
	"kuzelkystream/internal/match"
)

// The results spreadsheet: one row per match (tab "Utkání") and one row per player (tab "Hráči").
// Rows are found by a key in the last column, so a match updated many times keeps its rows.

func colName(i int) string { // 0 → A, 25 → Z, 26 → AA
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

func matchHeader() []any {
	return []any{"Datum", "Soutěž", "Kolo", "Domácí", "Hosté", "Body domácí", "Body hosté",
		"Kuželky domácí", "Kuželky hosté", "Stav", "Aktualizováno", "Klíč"}
}

func playerHeader(lanes int) []any {
	h := []any{"Datum", "Utkání", "Tým", "Hráč", "Soupeř"}
	for j := 1; j <= lanes; j++ {
		h = append(h, fmt.Sprintf("Dráha %d", j))
	}
	return append(h, "Celkem", "Setové body", "Body", "Klíč")
}

// ensureSheet returns the spreadsheet id, creating the spreadsheet (in the app folder) if needed.
func (a *App) ensureSheet(ctx context.Context, cl *google.Client, folder string, c config.Config) (string, error) {
	ok, err := cl.SpreadsheetExists(ctx, c.Cloud.SheetID)
	if err != nil {
		return "", err
	}
	if ok {
		return c.Cloud.SheetID, nil
	}
	id, err := cl.CreateSpreadsheet(ctx, sheetTitle, tabMatches, tabPlayers)
	if err != nil {
		return "", err
	}
	if err := cl.BatchUpdate(ctx, id, []google.ValueRange{
		{Range: "'" + tabMatches + "'!A1", Values: [][]any{matchHeader()}},
		{Range: "'" + tabPlayers + "'!A1", Values: [][]any{playerHeader(c.Rules.Lanes)}},
	}); err != nil {
		return "", err
	}
	_ = cl.MoveToFolder(ctx, id, folder)
	a.updateConfig(func(cc *config.Config) { cc.Cloud.SheetID = id })
	a.Log.Printf("Google: založena tabulka výsledků %s", google.SpreadsheetURL(id))
	return id, nil
}

// updateSheet writes the match stored in file to the spreadsheet.
func (a *App) updateSheet(ctx context.Context, cl *google.Client, folder, file string) error {
	m, err := a.mt.store.Load(file)
	if err != nil {
		return nil // deleted meanwhile
	}
	c := a.Config()
	id, err := a.ensureSheet(ctx, cl, folder, c)
	if err != nil {
		return err
	}
	rules := c.Rules
	m.Reshape(rules)
	res := match.Compute(m, rules)
	state := "rozehráno"
	if res.Finished {
		state = "dohráno"
	}
	now := time.Now().Format("2006-01-02 15:04")
	title := m.Home.Name + " – " + m.Away.Name

	matchRow := []any{m.Date, m.Competition, m.Round, m.Home.Name, m.Away.Name, res.HomePts, res.AwayPts,
		res.HomePins, res.AwayPins, state, now, file}
	var playerRows [][]any
	var playerKeys []string
	for i, d := range m.Duels {
		dr := res.Duels[i]
		for _, side := range []string{"d", "h"} {
			team, player, opp, sum, sets, pts := m.Home.Name, d.Label("d"), d.Label("h"), dr.HomeSum, dr.HomeSets, dr.HomePts
			if side == "h" {
				team, player, opp, sum, sets, pts = m.Away.Name, d.Label("h"), d.Label("d"), dr.AwaySum, dr.AwaySets, dr.AwayPts
			}
			if d.Absent(side) {
				player += " (nenastoupil)"
			}
			row := []any{m.Date, title, team, player, opp}
			for _, p := range d.Lanes {
				v := p.Home
				if side == "h" {
					v = p.Away
				}
				if v != nil {
					row = append(row, *v)
				} else {
					row = append(row, "")
				}
			}
			var ptsCell any = ""
			if dr.Finished {
				ptsCell = pts
			}
			row = append(row, sum, sets, ptsCell)
			key := fmt.Sprintf("%s#%d#%s", file, i+1, side)
			playerRows = append(playerRows, append(row, key))
			playerKeys = append(playerKeys, key)
		}
	}
	if err := upsertRows(ctx, cl, id, tabMatches, len(matchRow)-1, [][]any{matchRow}, []string{file}); err != nil {
		return err
	}
	return upsertRows(ctx, cl, id, tabPlayers, len(playerRows[0])-1, playerRows, playerKeys)
}

// upsertRows writes rows whose key (in column keyCol) already exists in place and appends the rest.
func upsertRows(ctx context.Context, cl *google.Client, id, tab string, keyCol int, rows [][]any, keys []string) error {
	q := "'" + tab + "'!"
	col, err := cl.GetColumn(ctx, id, q+colName(keyCol)+":"+colName(keyCol))
	if err != nil {
		return err
	}
	at := map[string]int{}
	for i, k := range col {
		if strings.TrimSpace(k) != "" {
			at[k] = i + 1 // 1-based row number
		}
	}
	var updates []google.ValueRange
	var appends [][]any
	for i, r := range rows {
		if row, ok := at[keys[i]]; ok {
			updates = append(updates, google.ValueRange{
				Range:  fmt.Sprintf("%sA%d:%s%d", q, row, colName(len(r)-1), row),
				Values: [][]any{r},
			})
		} else {
			appends = append(appends, r)
		}
	}
	if err := cl.BatchUpdate(ctx, id, updates); err != nil {
		return err
	}
	return cl.Append(ctx, id, q+"A1", appends)
}
