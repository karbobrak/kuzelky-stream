// Package update keeps the app current from GitHub Releases of the public repository. At start-up
// (never while streaming – the app is not running yet) the newest release is compared with the
// running version; a newer KuzelkyStream.exe is downloaded, checked against the release's
// SHA256SUMS and swapped in. Windows allows renaming a running exe, so no helper program is
// needed: the old one becomes KuzelkyStream.exe.old and is deleted at the next start.
package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Repo publishes the releases.
const Repo = "karbobrak/kuzelky-stream"

// Release assets.
const (
	ExeAsset   = "KuzelkyStream.exe"
	SumsAsset  = "SHA256SUMS"
	GuideAsset = "NAVOD.txt" // optional, updated next to the exe
)

// APIBase can be changed by tests.
var APIBase = "https://api.github.com"

// UserAgent identifies the app to GitHub.
var UserAgent = "KuzelkyStream"

var client = &http.Client{Timeout: 2 * time.Minute}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type Release struct {
	Tag    string  `json:"tag_name"`
	Name   string  `json:"name"`
	Notes  string  `json:"body"`
	Draft  bool    `json:"draft"`
	Pre    bool    `json:"prerelease"`
	Assets []Asset `json:"assets"`
}

// Version is the release version without the "v" prefix.
func (r *Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

func (r *Release) asset(name string) *Asset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

var reVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

func parse(v string) ([3]int, bool) {
	m := reVersion.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

// IsRelease reports whether v is a release version (development builds never update).
func IsRelease(v string) bool { _, ok := parse(v); return ok }

// Newer reports whether release version tag is newer than current.
func Newer(tag, current string) bool {
	a, ok1 := parse(tag)
	b, ok2 := parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	if strings.HasPrefix(url, APIBase) {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: soubor je větší než %d B", url, limit)
	}
	return data, nil
}

// Latest returns the newest published release.
func Latest(ctx context.Context) (*Release, error) {
	data, err := get(ctx, APIBase+"/repos/"+Repo+"/releases/latest", 1<<20)
	if err != nil {
		return nil, err
	}
	var r Release
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Draft || r.Pre || !IsRelease(r.Tag) {
		return nil, errors.New("poslední vydání není platná verze")
	}
	return &r, nil
}

// sums reads a sha256sum-style file: "<hex>  <name>" per line.
func sums(data []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 {
			out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	return out
}

// Install downloads the release's exe (and guide) and puts it in place of exePath.
func Install(ctx context.Context, r *Release, exePath string) error {
	sa, ea := r.asset(SumsAsset), r.asset(ExeAsset)
	if sa == nil || ea == nil {
		return fmt.Errorf("vydání %s nemá soubory %s a %s", r.Tag, ExeAsset, SumsAsset)
	}
	sd, err := get(ctx, sa.URL, 64<<10)
	if err != nil {
		return err
	}
	want := sums(sd)
	exe, err := download(ctx, ea, want[ExeAsset], 200<<20)
	if err != nil {
		return err
	}
	if len(exe) < 1<<20 || !bytes.HasPrefix(exe, []byte("MZ")) {
		return errors.New("stažený soubor není program pro Windows")
	}
	if err := replace(exePath, exe, 0o755); err != nil {
		return err
	}
	if ga := r.asset(GuideAsset); ga != nil && want[GuideAsset] != "" {
		if g, err := download(ctx, ga, want[GuideAsset], 1<<20); err == nil {
			_ = os.WriteFile(filepath.Join(filepath.Dir(exePath), GuideAsset), g, 0o644)
		}
	}
	return nil
}

func download(ctx context.Context, a *Asset, sum string, limit int64) ([]byte, error) {
	if sum == "" {
		return nil, fmt.Errorf("%s chybí v %s", a.Name, SumsAsset)
	}
	data, err := get(ctx, a.URL, limit)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != sum {
		return nil, fmt.Errorf("%s: kontrolní součet nesouhlasí – stažení je poškozené", a.Name)
	}
	return data, nil
}

// replace swaps in the new exe: path → path.old, new → path (rolled back on failure).
func replace(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	old := path + ".old"
	_ = os.Remove(old)
	if err := os.Rename(path, old); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("nelze přejmenovat %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Rename(old, path)
		return err
	}
	return nil
}

// Cleanup deletes the previous exe left by an update (it was still running then).
func Cleanup(exePath string) { _ = os.Remove(exePath + ".old") }
