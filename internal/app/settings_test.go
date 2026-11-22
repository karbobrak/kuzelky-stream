package app

import (
	"io"
	"log"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/google"
	"kuzelkystream/internal/overlay"
)

func TestSharedSettings(t *testing.T) {
	fg := &fakeGoogle{files: map[string]*fakeFile{}, sheets: map[string]map[string][][]string{}}
	srv := httptest.NewServer(fg)
	defer srv.Close()
	google.SetBaseURLForTest(srv.URL)

	signedIn := func() config.Config {
		c := config.Default()
		c.YouTube.ClientID, c.YouTube.ClientSecret, c.Cloud.RefreshToken = "id", "secret", "R"
		c.Cloud.Scopes = google.ScopeDriveFile
		return c
	}
	newApp := func(c config.Config) *App {
		dir := t.TempDir()
		return New(dir, filepath.Join(dir, "config.json"), c, "ffmpeg", log.New(io.Discard, "", 0), NewLogBuf(10))
	}
	sync := func(a *App) {
		t.Helper()
		if err := a.SyncNow(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	// Notebook A: the club's settings.
	a := newApp(signedIn())
	ca := a.Config()
	ca.Camera.Host, ca.Camera.Password = "10.0.0.7", "tajne"
	ca.YouTube.RefreshToken, ca.YouTube.BroadcastID = "YT-TOKEN", "BROADCAST-1"
	ca.Output.Encoder = "h264_nvenc"
	if _, err := a.ApplyConfig(ca); err != nil {
		t.Fatal(err)
	}
	sync(a)

	// Notebook B: fresh install, its own encoder, and a change made before the first sync.
	cb := signedIn()
	cb.Output.Encoder = "h264_qsv"
	cb.Camera.Host = "192.168.1.1" // must not win over the club's settings
	b := newApp(cb)
	if _, err := b.ApplyConfig(cb); err != nil {
		t.Fatal(err)
	}
	sync(b)
	got := b.Config()
	if got.Camera.Host != "10.0.0.7" || got.Camera.Password != "tajne" || got.YouTube.RefreshToken != "YT-TOKEN" ||
		got.YouTube.BroadcastID != "BROADCAST-1" {
		t.Fatalf("B did not get the club settings: %+v %+v", got.Camera, got.YouTube)
	}
	if got.Output.Encoder != "h264_qsv" || got.Cloud.RefreshToken != "R" {
		t.Fatalf("B lost its own fields: encoder %q drive %q", got.Output.Encoder, got.Cloud.RefreshToken)
	}

	// B moves the graphics; A gets it at its next comparison, keeping its encoder.
	time.Sleep(5 * time.Millisecond)
	l := overlay.DefaultLayout()
	l.Bar.Y = 50
	if err := b.SetLayout(l); err != nil {
		t.Fatal(err)
	}
	if err := b.SyncNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	sync(a)
	if a.Config().Overlay.Layout.Bar.Y != 50 || a.Config().Output.Encoder != "h264_nvenc" {
		t.Fatalf("A after B's change: bar %v encoder %q", a.Config().Overlay.Layout.Bar.Y, a.Config().Output.Encoder)
	}
	// A broadcast ended on A disappears on B too.
	a.saveBroadcast(nil)
	if err := a.SyncNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	sync(b)
	if id := b.Config().YouTube.BroadcastID; id != "" {
		t.Fatalf("B still has broadcast %q", id)
	}
}
