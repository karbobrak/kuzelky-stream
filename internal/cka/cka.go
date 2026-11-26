// Package cka reads a match from the ČKA results service (vysledky.kuzelky.cz). The home club has
// to fill the official online report during the match, so the public match page carries live
// results. The page is a Next.js app; the match is embedded as JSON in its "flight" payload
// (self.__next_f.push chunks), which is far more robust to read than the rendered table.
//
// The ČKA API itself (kuzelky.cz/api/v1/public) requires authentication and is not used.
package cka

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Host of the results service; only its match pages are accepted.
const Host = "vysledky.kuzelky.cz"

// Match as published by the results service (only the fields the app uses).
type Match struct {
	Slug        string `json:"slug"`
	Date        string `json:"date"` // 2026-11-21
	Time        string `json:"time"` // 09:00
	Round       int    `json:"round"`
	Status      string `json:"status"` // PREPARATION | IN_PROGRESS | FINISHED
	MatchType   string `json:"matchType"`
	Discipline  string `json:"discipline"`
	HomeTeam    Team   `json:"homeTeam"`
	AwayTeam    Team   `json:"awayTeam"`
	Competition struct {
		Name string `json:"name"`
	} `json:"competition"`
	Results []TeamResult `json:"results"`
}

type Team struct {
	Name string `json:"name"`
}

type TeamResult struct {
	IsHome        bool           `json:"isHome"`
	TeamPoints    *float64       `json:"teamPoints"`
	PlayerResults []PlayerResult `json:"playerResults"`
	Substitutions []Substitution `json:"substitutions"`
}

type PlayerResult struct {
	Position    int          `json:"position"` // 1-based place in the line-up
	TeamPoints  *float64     `json:"teamPoints"`
	Player      Person       `json:"player"`
	LaneResults []LaneResult `json:"laneResults"`
}

type Person struct {
	ID        int    `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

// Name is "First Last".
func (p Person) Name() string { return strings.TrimSpace(p.FirstName + " " + p.LastName) }

// LaneResult is one lane segment (laneNumber 1..4 = segment, not the physical lane).
type LaneResult struct {
	LaneNumber int  `json:"laneNumber"`
	Full       *int `json:"full"`
	Spare      *int `json:"spare"`
	Errors     *int `json:"errors"`
	Total      *int `json:"total"`
}

type Substitution struct {
	ThrowNumber int    `json:"throwNumber"`
	PlayerIn    Person `json:"playerIn"`
	PlayerOut   Person `json:"playerOut"`
}

// Status values.
const (
	StatusPreparation = "PREPARATION"
	StatusInProgress  = "IN_PROGRESS"
	StatusFinished    = "FINISHED"
)

// CheckURL accepts only https://vysledky.kuzelky.cz/detail-zapasu/<slug> and returns it normalized.
func CheckURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host != Host || !strings.HasPrefix(u.Path, "/detail-zapasu/") || len(u.Path) <= len("/detail-zapasu/") {
		return "", fmt.Errorf("zadejte odkaz na zápas z %s (https://%s/detail-zapasu/…)", Host, Host)
	}
	return "https://" + Host + strings.TrimRight(u.Path, "/"), nil
}

var client = &http.Client{Timeout: 20 * time.Second}

// UserAgent identifies the app to the results service.
var UserAgent = "KuzelkyStream (+https://github.com/karbobrak/kuzelky-stream)"

// Fetch downloads the match page and reads the match from it.
func Fetch(ctx context.Context, pageURL string) (*Match, error) {
	u, err := CheckURL(pageURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s nedostupné: %w", Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s vrátil %s", Host, resp.Status)
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return ParsePage(page)
}

var reChunk = regexp.MustCompile(`self\.__next_f\.push\(\[1,"((?:[^"\\]|\\.)*)"\]\)`)

// ErrNoMatch: the page does not contain match data (changed site, wrong page).
var ErrNoMatch = errors.New("na stránce nejsou data zápasu – změnil se web výsledkového servisu?")

// ParsePage finds the match object in the page's flight payload.
func ParsePage(page []byte) (*Match, error) {
	var payload strings.Builder
	for _, m := range reChunk.FindAllSubmatch(page, -1) {
		var s string
		if err := json.Unmarshal(append(append([]byte{'"'}, m[1]...), '"'), &s); err != nil {
			continue
		}
		payload.WriteString(s)
	}
	p := payload.String()
	i := strings.Index(p, `{"match":{`)
	if i < 0 {
		return nil, ErrNoMatch
	}
	var wrap struct {
		Match *Match `json:"match"`
	}
	dec := json.NewDecoder(strings.NewReader(p[i:]))
	if err := dec.Decode(&wrap); err != nil || wrap.Match == nil {
		return nil, fmt.Errorf("%w (%v)", ErrNoMatch, err)
	}
	return wrap.Match, nil
}

// ParseJSON reads a match object (tests, saved data).
func ParseJSON(data []byte) (*Match, error) {
	var m Match
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Team returns the home or away result (nil if the line-up is not published yet).
func (m *Match) Team(home bool) *TeamResult {
	for i := range m.Results {
		if m.Results[i].IsHome == home {
			return &m.Results[i]
		}
	}
	return nil
}
