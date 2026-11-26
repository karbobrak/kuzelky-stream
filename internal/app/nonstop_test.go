package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/google"
	"kuzelkystream/internal/match"
)

// fakeYouTube: one reusable stream and broadcasts that go live as soon as they are bound (as
// with auto-start while video is flowing).
type fakeYouTube struct {
	mu         sync.Mutex
	broadcasts map[string]*fakeBroadcast
	order      []string
}

type fakeBroadcast struct {
	Title, State, Stream string
}

func (f *fakeYouTube) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	p, q := strings.TrimPrefix(r.URL.Path, "/youtube/v3/"), r.URL.Query()
	switch {
	case r.URL.Path == "/token":
		io.WriteString(w, `{"access_token":"A","expires_in":3600}`)
	case p == "liveStreams":
		io.WriteString(w, `{"items":[{"id":"S1","cdn":{"ingestionInfo":{"streamName":"key","ingestionAddress":"rtmp://x"}},"status":{"streamStatus":"active"}}]}`)
	case p == "liveBroadcasts" && r.Method == "POST":
		var body struct {
			Snippet struct{ Title string } `json:"snippet"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		id := fmt.Sprintf("B%d", len(f.order)+1)
		f.broadcasts[id] = &fakeBroadcast{Title: body.Snippet.Title, State: "ready"}
		f.order = append(f.order, id)
		fmt.Fprintf(w, `{"id":%q,"snippet":{"title":%q},"status":{"lifeCycleStatus":"ready"}}`, id, body.Snippet.Title)
	case p == "liveBroadcasts":
		b := f.broadcasts[q.Get("id")]
		if b == nil {
			io.WriteString(w, `{"items":[]}`)
			return
		}
		fmt.Fprintf(w, `{"items":[{"id":%q,"snippet":{"title":%q},"status":{"lifeCycleStatus":%q}}]}`, q.Get("id"), b.Title, b.State)
	case p == "liveBroadcasts/bind":
		b := f.broadcasts[q.Get("id")]
		b.Stream, b.State = q.Get("streamId"), "live"
		io.WriteString(w, `{}`)
	case p == "liveBroadcasts/transition":
		f.broadcasts[q.Get("id")].State = q.Get("broadcastStatus")
		io.WriteString(w, `{}`)
	default:
		w.WriteHeader(404)
	}
}

func (f *fakeYouTube) state(id string) (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.broadcasts[id]
	return b.Title, b.State
}

// Alley (nonstop) → match → alley: every switch ends the previous broadcast (a video) and binds a
// new one to the same stream; the match broadcast is titled after the match.
func TestNonstopSwitching(t *testing.T) {
	fy := &fakeYouTube{broadcasts: map[string]*fakeBroadcast{}}
	srv := httptest.NewServer(fy)
	defer srv.Close()
	google.SetBaseURLForTest(srv.URL)

	dir := t.TempDir()
	cfg := config.Default()
	cfg.YouTube.ClientID, cfg.YouTube.ClientSecret, cfg.YouTube.RefreshToken = "id", "secret", "YT"
	cfg.YouTube.StreamID = "S1"
	a := New(dir, filepath.Join(dir, "config.json"), cfg, "ffmpeg", log.New(io.Discard, "", 0), NewLogBuf(10))
	ctx := t.Context()

	// No match open: the alley.
	if err := a.prepareYouTube(ctx, a.Config()); err != nil {
		t.Fatal(err)
	}
	if c := a.Config().YouTube; c.BroadcastID != "B1" || c.BroadcastKind != KindNonstop {
		t.Fatalf("alley broadcast: %q %q", c.BroadcastID, c.BroadcastKind)
	}
	if title, _ := fy.state("B1"); title != "TJ Sparta Kutná Hora" {
		t.Fatalf("alley title %q", title)
	}

	// A match: the alley broadcast ends, the match gets its own.
	m := match.New(cfg.Rules)
	m.Home.Name, m.Away.Name = "TJ Sparta Kutná Hora", "TJ Lhotka"
	if err := a.NewMatch(m); err != nil {
		t.Fatal(err)
	}
	if err := a.prepareYouTube(ctx, a.Config()); err != nil {
		t.Fatal(err)
	}
	if _, st := fy.state("B1"); st != "complete" {
		t.Fatalf("alley broadcast state %q after the match started", st)
	}
	c := a.Config().YouTube
	title, st := fy.state(c.BroadcastID)
	if c.BroadcastID != "B2" || c.BroadcastKind != KindMatch || st != "live" || !strings.HasPrefix(title, "TJ Sparta Kutná Hora – TJ Lhotka (") {
		t.Fatalf("match broadcast %q %q %q %q", c.BroadcastID, c.BroadcastKind, title, st)
	}
	// Starting the match again changes nothing.
	a.mt.mu.Lock()
	open := a.mt.cur != nil
	a.mt.mu.Unlock()
	if !open {
		t.Fatal("match not open")
	}

	// End of the match (not streaming in this test): the match broadcast ends, the score is gone.
	if err := a.EndMatchBroadcast(ctx); err != nil {
		t.Fatal(err)
	}
	if _, st := fy.state("B2"); st != "complete" {
		t.Fatalf("match broadcast state %q after the end", st)
	}
	if a.MatchView().Match != nil {
		t.Fatal("match still open after its end")
	}
	// The next start is the alley again.
	if err := a.prepareYouTube(ctx, a.Config()); err != nil {
		t.Fatal(err)
	}
	if c := a.Config().YouTube; c.BroadcastID != "B3" || c.BroadcastKind != KindNonstop {
		t.Fatalf("after the match: %q %q", c.BroadcastID, c.BroadcastKind)
	}
}
