package app

import (
	"encoding/json"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/google"
	"kuzelkystream/internal/match"
)

// fakeGoogle is a tiny in-memory Drive + Sheets.
type fakeGoogle struct {
	mu     sync.Mutex
	files  map[string]*fakeFile // id → file
	sheets map[string]map[string][][]string
	nextID int
}

type fakeFile struct {
	Name, Parent, Mime string
	Data               []byte
	Props              map[string]string
}

func (f *fakeGoogle) id() string { f.nextID++; return "F" + string(rune('A'+f.nextID)) }

func (f *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Path
	switch {
	case p == "/token":
		io.WriteString(w, `{"access_token":"A","expires_in":3600}`)
	case p == "/drive/v3/files" && r.Method == "GET":
		q := r.URL.Query().Get("q")
		var out []map[string]any
		for id, fl := range f.files {
			if (strings.Contains(q, "mimeType='application/vnd.google-apps.folder'") && fl.Mime == folderMimeForTest && strings.Contains(q, "'"+fl.Name+"'")) ||
				(strings.Contains(q, "in parents") && strings.Contains(q, "'"+fl.Parent+"'") && fl.Parent != "") {
				out = append(out, map[string]any{"id": id, "name": fl.Name, "appProperties": fl.Props, "modifiedTime": time.Now().Format(time.RFC3339)})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"files": out})
	case p == "/drive/v3/files" && r.Method == "POST":
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		id := f.id()
		f.files[id] = &fakeFile{Name: m["name"].(string), Mime: m["mimeType"].(string)}
		io.WriteString(w, `{"id":"`+id+`"}`)
	case strings.HasPrefix(p, "/drive/v3/files/") && r.URL.Query().Get("alt") == "media":
		fl := f.files[strings.TrimPrefix(p, "/drive/v3/files/")]
		w.Write(fl.Data)
	case strings.HasPrefix(p, "/drive/v3/files/") && r.Method == "PATCH": // move to folder
		io.WriteString(w, `{}`)
	case strings.HasPrefix(p, "/drive/v3/files/"):
		if _, ok := f.files[strings.TrimPrefix(p, "/drive/v3/files/")]; !ok {
			w.WriteHeader(404)
			return
		}
		io.WriteString(w, `{"id":"x","trashed":false}`)
	case strings.HasPrefix(p, "/upload/drive/v3/files"):
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		part, _ := mr.NextPart()
		var meta struct {
			Name    string            `json:"name"`
			Parents []string          `json:"parents"`
			Props   map[string]string `json:"appProperties"`
		}
		json.NewDecoder(part).Decode(&meta)
		part, _ = mr.NextPart()
		data, _ := io.ReadAll(part)
		id := strings.TrimPrefix(p, "/upload/drive/v3/files/")
		if r.Method == "POST" {
			id = f.id()
			f.files[id] = &fakeFile{Name: meta.Name, Parent: meta.Parents[0]}
		}
		fl := f.files[id]
		fl.Data, fl.Props = data, meta.Props
		io.WriteString(w, `{"id":"`+id+`"}`)
	case p == "/sheets/v4/spreadsheets" && r.Method == "POST":
		f.sheets["S1"] = map[string][][]string{tabMatches: nil, tabPlayers: nil}
		io.WriteString(w, `{"spreadsheetId":"S1"}`)
	case strings.HasPrefix(p, "/sheets/v4/spreadsheets/S1/values:batchUpdate"):
		var b struct{ Data []google.ValueRange }
		json.NewDecoder(r.Body).Decode(&b)
		for _, d := range b.Data {
			f.write(d.Range, d.Values, false)
		}
		io.WriteString(w, `{}`)
	case strings.HasPrefix(p, "/sheets/v4/spreadsheets/S1/values/") && strings.HasSuffix(p, ":append"):
		var b struct{ Values [][]any }
		json.NewDecoder(r.Body).Decode(&b)
		rng := strings.TrimSuffix(strings.TrimPrefix(p, "/sheets/v4/spreadsheets/S1/values/"), ":append")
		f.write(rng, b.Values, true)
		io.WriteString(w, `{}`)
	case strings.HasPrefix(p, "/sheets/v4/spreadsheets/S1/values/"): // GET key column
		tab := strings.Trim(strings.SplitN(strings.TrimPrefix(p, "/sheets/v4/spreadsheets/S1/values/"), "!", 2)[0], "'")
		var col []string
		for _, row := range f.sheets["S1"][tab] {
			col = append(col, row[len(row)-1])
		}
		json.NewEncoder(w).Encode(map[string]any{"values": [][]string{col}})
	case strings.HasPrefix(p, "/sheets/v4/spreadsheets/"):
		if _, ok := f.sheets[strings.TrimPrefix(p, "/sheets/v4/spreadsheets/")]; !ok {
			w.WriteHeader(404)
			return
		}
		io.WriteString(w, `{}`)
	default:
		w.WriteHeader(404)
	}
}

const folderMimeForTest = "application/vnd.google-apps.folder"

// write handles "'Tab'!A5:L5" (update row 5) and "'Tab'!A1" (header / append).
func (f *fakeGoogle) write(rng string, values [][]any, appendRows bool) {
	parts := strings.SplitN(rng, "!", 2)
	tab := strings.Trim(parts[0], "'")
	rows := f.sheets["S1"][tab]
	toStr := func(v []any) []string {
		var s []string
		for _, x := range v {
			b, _ := json.Marshal(x)
			s = append(s, strings.Trim(string(b), `"`))
		}
		return s
	}
	if appendRows {
		for _, v := range values {
			rows = append(rows, toStr(v))
		}
	} else {
		var row int
		for _, c := range strings.SplitN(parts[1], ":", 2)[0] {
			if c >= '0' && c <= '9' {
				row = row*10 + int(c-'0')
			}
		}
		for len(rows) < row {
			rows = append(rows, []string{""})
		}
		rows[row-1] = toStr(values[0])
	}
	f.sheets["S1"][tab] = rows
}

func TestCloudSync(t *testing.T) {
	fg := &fakeGoogle{files: map[string]*fakeFile{}, sheets: map[string]map[string][][]string{}}
	srv := httptest.NewServer(fg)
	defer srv.Close()
	google.SetBaseURLForTest(srv.URL)

	dir := t.TempDir()
	cfg := config.Default()
	cfg.YouTube.ClientID, cfg.YouTube.ClientSecret, cfg.Cloud.RefreshToken = "id", "secret", "R"
	cfg.Cloud.Scopes = google.ScopeDriveFile
	a := New(dir, filepath.Join(dir, "config.json"), cfg, "ffmpeg", log.New(io.Discard, "", 0), NewLogBuf(10))

	m := match.New(cfg.Rules)
	m.Date, m.Home.Name, m.Away.Name = "2026-11-21", "Domácí TJ", "Hosté SK"
	m.Duels[0].HomePlayer, m.Duels[0].AwayPlayer = "Novák", "Dvořák"
	if err := a.NewMatch(m); err != nil {
		t.Fatal(err)
	}
	v := 150
	if err := a.SetCell(Cell{Duel: 0, Lane: 0, Side: "d", Value: &v}); err != nil {
		t.Fatal(err)
	}
	if err := a.SyncNow(t.Context()); err != nil {
		t.Fatal(err)
	}

	names := map[string]bool{}
	for _, fl := range fg.files {
		names[fl.Name] = true
	}
	file := a.MatchView().File
	for _, want := range []string{"KuzelkyStream", "zapas_" + file, "tym_" + match.RosterFile("Domácí TJ"), gfxFile} {
		if !names[want] {
			t.Errorf("Drive is missing %q (has %v)", want, names)
		}
	}
	if got := len(fg.sheets["S1"][tabMatches]); got != 2 { // header + 1 match
		t.Fatalf("Utkání rows = %d, want 2: %v", got, fg.sheets["S1"][tabMatches])
	}
	if got := len(fg.sheets["S1"][tabPlayers]); got != 1+2*cfg.Rules.PlayersPerTeam {
		t.Fatalf("Hráči rows = %d", got)
	}
	// A second change updates rows in place (no duplicates).
	v = 160
	_ = a.SetCell(Cell{Duel: 0, Lane: 0, Side: "h", Value: &v})
	if err := a.SyncNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := len(fg.sheets["S1"][tabMatches]); got != 2 {
		t.Fatalf("after update Utkání rows = %d, want 2", got)
	}

	// A fresh notebook signed in to the same account gets the match back.
	dir2 := t.TempDir()
	cfg2 := a.Config()
	b := New(dir2, filepath.Join(dir2, "config.json"), cfg2, "ffmpeg", log.New(io.Discard, "", 0), NewLogBuf(10))
	if err := b.SyncNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir2, "data", "zapasy", file)); err != nil {
		t.Fatalf("match not restored on the new notebook: %v", err)
	}
	if len(b.SavedMatches()) != 1 || len(b.Rosters()) != 2 {
		t.Fatalf("restored matches %d, rosters %d", len(b.SavedMatches()), len(b.Rosters()))
	}
}

func TestColName(t *testing.T) {
	for i, want := range map[int]string{0: "A", 11: "L", 25: "Z", 26: "AA", 27: "AB"} {
		if got := colName(i); got != want {
			t.Errorf("colName(%d) = %s, want %s", i, got, want)
		}
	}
}
