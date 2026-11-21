package match

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Store keeps matches in data\zapasy\*.json and team rosters in data\tymy\*.json.
type Store struct {
	Dir string // the app's data folder
}

func (s Store) matchDir() string    { return filepath.Join(s.Dir, "zapasy") }
func (s Store) teamDir() string     { return filepath.Join(s.Dir, "tymy") }
func (s Store) currentFile() string { return filepath.Join(s.Dir, "aktualni-zapas.txt") }

// slug makes a safe file name part: no diacritics, letters/digits/dashes only.
func slug(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// drop combining marks (diacritics)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = out[:40]
	}
	if out == "" {
		out = "bez-nazvu"
	}
	return out
}

// FileName for a new match.
func FileName(m Match) string {
	d := m.Date
	if d == "" {
		d = time.Now().Format("2006-01-02")
	}
	return d + "_" + slug(m.Home.Name) + "_" + slug(m.Away.Name) + ".json"
}

func writeAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Save writes the match to name (a file name from FileName) and marks it as current.
func (s Store) Save(name string, m Match) error {
	if err := writeAtomic(filepath.Join(s.matchDir(), filepath.Base(name)), m); err != nil {
		return err
	}
	return os.WriteFile(s.currentFile(), []byte(filepath.Base(name)), 0o644)
}

func (s Store) Load(name string) (Match, error) {
	var m Match
	data, err := os.ReadFile(filepath.Join(s.matchDir(), filepath.Base(name)))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(data, &m)
}

// Current returns the match that was open last, if any.
func (s Store) Current() (string, Match, error) {
	b, err := os.ReadFile(s.currentFile())
	if err != nil {
		return "", Match{}, err
	}
	name := strings.TrimSpace(string(b))
	m, err := s.Load(name)
	return name, m, err
}

// ClearCurrent forgets the current match (the file stays).
func (s Store) ClearCurrent() { _ = os.Remove(s.currentFile()) }

type Saved struct {
	File     string `json:"soubor"`
	Date     string `json:"datum"`
	Home     string `json:"domaci"`
	Away     string `json:"hoste"`
	Modified string `json:"zmeneno"`
}

// List returns saved matches, newest first.
func (s Store) List() []Saved {
	files, _ := filepath.Glob(filepath.Join(s.matchDir(), "*.json"))
	var out []Saved
	for _, f := range files {
		m, err := s.Load(filepath.Base(f))
		if err != nil {
			continue
		}
		st, _ := os.Stat(f)
		out = append(out, Saved{File: filepath.Base(f), Date: m.Date, Home: m.Home.Name, Away: m.Away.Name,
			Modified: st.ModTime().Format("2006-01-02 15:04")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	return out
}

// Roster is a saved team: name and players, for quick setup of the next match.
type Roster struct {
	Name    string   `json:"nazev"`
	Short   string   `json:"zkratka"`
	Players []string `json:"hraci"`
}

// RosterFile is the file name of a team's roster.
func RosterFile(team string) string { return slug(team) + ".json" }

func (s Store) SaveRoster(r Roster) error {
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("soupiska bez názvu týmu")
	}
	return writeAtomic(filepath.Join(s.teamDir(), RosterFile(r.Name)), r)
}

func (s Store) Rosters() []Roster {
	files, _ := filepath.Glob(filepath.Join(s.teamDir(), "*.json"))
	var out []Roster
	for _, f := range files {
		var r Roster
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
