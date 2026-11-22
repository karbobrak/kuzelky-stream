package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		tag, cur string
		want     bool
	}{
		{"v1.5.0", "1.4.0", true}, {"v1.4.1", "1.4.0", true}, {"v1.10.0", "1.9.9", true},
		{"v1.4.0", "1.4.0", false}, {"v1.3.9", "1.4.0", false}, {"v2.0.0", "vývojová", false}, {"nic", "1.0.0", false},
	} {
		if got := Newer(c.tag, c.cur); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.tag, c.cur, got)
		}
	}
}

func TestInstall(t *testing.T) {
	newExe := append([]byte("MZ"), bytes.Repeat([]byte{1}, 2<<20)...)
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	sumsFile := sum(newExe) + "  " + ExeAsset + "\n" + sum([]byte("návod")) + "  " + GuideAsset + "\n"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + Repo + "/releases/latest":
			json.NewEncoder(w).Encode(Release{Tag: "v9.9.9", Assets: []Asset{
				{Name: ExeAsset, URL: srv.URL + "/exe"}, {Name: SumsAsset, URL: srv.URL + "/sums"},
				{Name: GuideAsset, URL: srv.URL + "/guide"}}})
		case "/exe":
			w.Write(newExe)
		case "/sums":
			w.Write([]byte(sumsFile))
		case "/guide":
			w.Write([]byte("návod"))
		}
	}))
	defer srv.Close()
	APIBase = srv.URL

	dir := t.TempDir()
	exe := filepath.Join(dir, ExeAsset)
	os.WriteFile(exe, []byte("MZ old"), 0o755)
	r, err := Latest(t.Context())
	if err != nil || !Newer(r.Tag, "1.4.0") {
		t.Fatalf("latest %v %v", r, err)
	}
	if err := Install(t.Context(), r, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, newExe) {
		t.Fatal("exe not replaced")
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "MZ old" {
		t.Fatal("old exe not kept for cleanup")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, GuideAsset)); string(b) != "návod" {
		t.Fatal("guide not updated")
	}
	Cleanup(exe)
	if _, err := os.Stat(exe + ".old"); err == nil {
		t.Fatal("old exe not cleaned up")
	}

	// A corrupted download must leave the installed exe alone.
	sumsFile = sum([]byte("jiný")) + "  " + ExeAsset + "\n"
	if err := Install(t.Context(), r, exe); err == nil {
		t.Fatal("bad checksum accepted")
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, newExe) {
		t.Fatal("exe damaged by a failed update")
	}
}
