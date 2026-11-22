// Package config loads and saves the application settings (config.json next to the exe).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"kuzelkystream/internal/fsutil"
	"kuzelkystream/internal/match"
	"kuzelkystream/internal/overlay"
)

type Camera struct {
	// TestSource replaces the camera with a synthetic ffmpeg pattern (for testing without a camera).
	TestSource bool   `json:"testovaciZdroj"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"uzivatel"`
	Password   string `json:"heslo"`
	Path       string `json:"cesta"`
	Transport  string `json:"transport"` // tcp | udp
	// URL, when set, overrides Host/Port/User/Password/Path.
	URL string `json:"url"`

	// Source: "rtsp" (IP camera, the default) | "webkamera" (USB or built-in camera, DirectShow).
	Source string `json:"zdroj"`
	// Webcam: DirectShow device names as Windows lists them; Mic "" = no sound from the webcam.
	Webcam    string `json:"webkamera"`
	Mic       string `json:"mikrofon"`
	WebcamRes string `json:"rozliseniWebkamery"` // "1920x1080"; "" = what the camera offers by default
	WebcamFPS int    `json:"fpsWebkamery"`       // 0 = the camera's default
}

// IsWebcam: the picture comes from a local webcam instead of an RTSP camera.
func (c Camera) IsWebcam() bool { return !c.TestSource && c.Source == "webkamera" }

// Describe says where the picture comes from, without secrets (for logs and UI).
func (c Camera) Describe() string {
	switch {
	case c.TestSource:
		return "testovací obrazec"
	case c.IsWebcam():
		s := "webkamera „" + c.Webcam + "“"
		if c.Mic != "" {
			s += ", mikrofon „" + c.Mic + "“"
		}
		return s
	}
	return c.MaskedCameraURL()
}

type YouTube struct {
	// Mode: "api" = the app creates and manages the broadcast through the YouTube API (recommended);
	// "klic" = stream to a key copied from YouTube Studio by hand.
	Mode string `json:"rezim"`

	// Manual mode.
	Server string `json:"server"`
	Key    string `json:"klic"`

	// API mode. ClientID/ClientSecret come from the user's Google Cloud project ("Desktop app").
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	// Set by the app (sign-in), never by the settings form.
	RefreshToken string `json:"refreshToken"`
	ChannelID    string `json:"kanalId"`
	ChannelTitle string `json:"kanal"`
	StreamID     string `json:"streamId"`
	// The broadcast in progress (fixed address until it is ended), shared by all computers.
	BroadcastID string `json:"prenosId,omitempty"`
	// Broadcast defaults.
	Privacy string `json:"soukromi"` // public | unlisted | private
	Title   string `json:"nazevPrenosu"`
}

// APIReady reports whether API mode has everything it needs to stream.
func (y YouTube) APIReady() bool {
	return y.ClientID != "" && y.ClientSecret != "" && y.RefreshToken != ""
}

type Output struct {
	// Target: youtube | soubor | nic (nic = encode and discard, for performance tests)
	Target       string `json:"cil"`
	FilePath     string `json:"souborCesta"`
	Width        int    `json:"sirka"`
	Height       int    `json:"vyska"`
	FPS          int    `json:"fps"`
	VideoBitrate int    `json:"datovyTokVideaKbps"`
	Encoder      string `json:"koder"` // auto | libx264 | h264_nvenc | h264_qsv | h264_amf
	X264Preset   string `json:"presetX264"`
	HWDecode     bool   `json:"hwDekodovani"`
}

type Audio struct {
	Source  string `json:"zdroj"` // kamera | ticho
	Bitrate int    `json:"datovyTokKbps"`
}

type Overlay struct {
	OnAtStart bool           `json:"grafikaPriStartu"`
	Layout    overlay.Layout `json:"rozlozeni"`
	// Named layouts (e.g. per alley or camera position).
	Presets map[string]overlay.Layout `json:"predvolby,omitempty"`
}

// Picture: what part of the camera image is broadcast and how it becomes 16:9.
// The crop is in percent of the (rotated) camera image, so it survives a change of camera resolution.
type Picture struct {
	CropX     float64 `json:"vyrezX"`
	CropY     float64 `json:"vyrezY"`
	CropW     float64 `json:"vyrezSirka"`
	CropH     float64 `json:"vyrezVyska"`
	Lock169   bool    `json:"zamek169"`     // editor keeps the crop at 16:9
	Fit       string  `json:"prizpusobeni"` // orez | pruhy | roztahnout
	Rotate180 bool    `json:"otoceni180"`
	Mirror    bool    `json:"zrcadlit"`
}

type App struct {
	Port        int  `json:"port"`
	OpenBrowser bool `json:"otevritProhlizec"`
	// How often a match linked to vysledky.kuzelky.cz is read again (seconds).
	ImportSeconds int `json:"intervalNacitaniVysledkuS"`
}

type Config struct {
	Camera  Camera      `json:"kamera"`
	YouTube YouTube     `json:"youtube"`
	Output  Output      `json:"vystup"`
	Audio   Audio       `json:"zvuk"`
	Overlay Overlay     `json:"grafika"`
	App     App         `json:"aplikace"`
	Rules   match.Rules `json:"pravidla"`
	Picture Picture     `json:"obraz"`
	Cloud   Cloud       `json:"cloud"`
}

// Cloud: backup of matches, rosters and graphics settings to Google Drive, and a results spreadsheet.
// Its own Google sign-in (Google does not allow YouTube and Drive in one sign-in); the OAuth client
// (ClientID/ClientSecret) is the one from the YouTube settings.
type Cloud struct {
	Backup bool `json:"zalohaNaDisk"`
	Sheet  bool `json:"tabulkaVysledku"`
	// Managed by the app.
	RefreshToken string `json:"refreshToken"`
	Account      string `json:"ucet"`
	Scopes       string `json:"opravneni"`
	FolderID     string `json:"slozkaId"`
	SheetID      string `json:"tabulkaId"`
	// Settings shared through Google Drive (nastaveni.json): when the shared part last changed
	// (unix ms) and whether this computer has already compared its settings with the Drive copy.
	SettingsChanged int64 `json:"nastaveniZmeneno,omitempty"`
	SettingsSynced  bool  `json:"nastaveniSynchronizovano,omitempty"`
}

// Shared is the part of the settings that travels to other computers through Google Drive:
// everything except this computer's Drive sign-in, its encoder choice (hardware differs) and
// the app's own port/browser options. The built-in OAuth client is added by each exe itself.
func (c Config) Shared() Config {
	s := c
	s.Cloud.RefreshToken, s.Cloud.Account, s.Cloud.Scopes = "", "", ""
	s.Cloud.SettingsChanged, s.Cloud.SettingsSynced = 0, false
	s.Output.Encoder, s.Output.HWDecode = "", false
	s.App = App{}
	s.YouTube.ClientID, s.YouTube.ClientSecret = "", ""
	return s
}

// WithLocal returns shared settings (from another computer) combined with this computer's own
// fields from local.
func (c Config) WithLocal(local Config) Config {
	m := c
	m.Cloud.RefreshToken, m.Cloud.Account, m.Cloud.Scopes = local.Cloud.RefreshToken, local.Cloud.Account, local.Cloud.Scopes
	m.Cloud.SettingsChanged, m.Cloud.SettingsSynced = local.Cloud.SettingsChanged, local.Cloud.SettingsSynced
	m.Output.Encoder, m.Output.HWDecode = local.Output.Encoder, local.Output.HWDecode
	m.App = local.App
	m.YouTube.ClientID, m.YouTube.ClientSecret = local.YouTube.ClientID, local.YouTube.ClientSecret
	if m.Output.Encoder == "" {
		m.Output.Encoder = "auto"
	}
	return m
}

func Default() Config {
	return Config{
		Camera: Camera{
			Host:      "192.168.100.163",
			Port:      554,
			User:      "admin",
			Path:      "h264Preview_01_main",
			Transport: "tcp",
			Source:    "rtsp",
		},
		YouTube: YouTube{
			Mode:    "api",
			Server:  "rtmp://a.rtmp.youtube.com/live2",
			Privacy: "unlisted",
			Title:   "{utkani} ({datum})",
		},
		Output: Output{
			Target:       "youtube",
			FilePath:     "data/zaznamy/test.mkv",
			Width:        1280,
			Height:       720,
			FPS:          25,
			VideoBitrate: 4000,
			Encoder:      "auto",
			X264Preset:   "veryfast",
		},
		Audio:   Audio{Source: "kamera", Bitrate: 128},
		Overlay: Overlay{OnAtStart: true, Layout: overlay.DefaultLayout()},
		Rules:   match.DefaultRules(),
		Picture: Picture{CropW: 100, CropH: 100, Lock169: true, Fit: "orez"},
		Cloud:   Cloud{Backup: true, Sheet: true},
		App:     App{Port: 8080, OpenBrowser: true, ImportSeconds: 60},
	}
}

// ErrCreated is returned by Load when the file did not exist and a default one was written.
var ErrCreated = errors.New("config file created with default values")

// Load reads path. Missing fields keep their default values.
// If the file does not exist, it writes defaults and returns ErrCreated.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := Save(path, cfg); err != nil {
			return cfg, err
		}
		return cfg, ErrCreated
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, cfg.Validate()
}

// Save writes cfg atomically (temp file + rename).
func Save(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return fsutil.Replace(tmp, path)
}

func (c Config) Validate() error {
	o := c.Output
	if o.Width <= 0 || o.Height <= 0 || o.Width%2 != 0 || o.Height%2 != 0 {
		return fmt.Errorf("výstupní rozlišení musí být kladné a sudé (%dx%d)", o.Width, o.Height)
	}
	if o.FPS <= 0 || o.FPS > 60 {
		return fmt.Errorf("fps musí být 1–60 (%d)", o.FPS)
	}
	switch o.Target {
	case "youtube", "soubor", "nic":
	default:
		return fmt.Errorf("neznámý cíl výstupu %q (youtube | soubor | nic)", o.Target)
	}
	switch c.Audio.Source {
	case "kamera", "ticho":
	default:
		return fmt.Errorf("neznámý zdroj zvuku %q (kamera | ticho)", c.Audio.Source)
	}
	if 48000%o.FPS != 0 {
		return fmt.Errorf("fps musí dělit 48000 beze zbytku (např. 24, 25, 30, 50, 60), ne %d", o.FPS)
	}
	if o.VideoBitrate < 500 || o.VideoBitrate > 20000 {
		return fmt.Errorf("datový tok videa musí být 500–20000 kb/s (%d)", o.VideoBitrate)
	}
	switch o.Encoder {
	case "auto", "libx264", "h264_nvenc", "h264_qsv", "h264_amf":
	default:
		return fmt.Errorf("neznámý kodér %q", o.Encoder)
	}
	if c.Audio.Bitrate < 32 || c.Audio.Bitrate > 320 {
		return fmt.Errorf("datový tok zvuku musí být 32–320 kb/s (%d)", c.Audio.Bitrate)
	}
	cam := c.Camera
	switch cam.Source {
	case "", "rtsp", "webkamera":
	default:
		return fmt.Errorf("neznámý zdroj obrazu %q (rtsp | webkamera)", cam.Source)
	}
	if cam.IsWebcam() {
		if strings.TrimSpace(cam.Webcam) == "" {
			return fmt.Errorf("vyberte webkameru")
		}
		if cam.WebcamRes != "" && !reRes.MatchString(cam.WebcamRes) {
			return fmt.Errorf("rozlišení webkamery musí být ve tvaru 1920x1080 (%q)", cam.WebcamRes)
		}
		if cam.WebcamFPS < 0 || cam.WebcamFPS > 120 {
			return fmt.Errorf("snímky za sekundu webkamery musí být 0–120 (%d)", cam.WebcamFPS)
		}
	} else if !cam.TestSource && cam.URL == "" && (cam.Host == "" || cam.Port <= 0 || cam.Port > 65535) {
		return fmt.Errorf("vyplňte adresu a port kamery")
	}
	if cam.Transport != "tcp" && cam.Transport != "udp" {
		return fmt.Errorf("transport kamery musí být tcp nebo udp")
	}
	switch c.YouTube.Mode {
	case "api", "klic":
	default:
		return fmt.Errorf("neznámý režim YouTube %q (api | klic)", c.YouTube.Mode)
	}
	switch c.YouTube.Privacy {
	case "public", "unlisted", "private":
	default:
		return fmt.Errorf("neznámá viditelnost přenosu %q (public | unlisted | private)", c.YouTube.Privacy)
	}
	if strings.TrimSpace(c.YouTube.Title) == "" {
		return fmt.Errorf("vyplňte název přenosu")
	}
	l := c.Overlay.Layout
	for _, p := range []overlay.Place{l.Bar, l.Message} {
		if p.X < 0 || p.X > 100 || p.Y < 0 || p.Y > 100 || p.Scale < 30 || p.Scale > 300 {
			return fmt.Errorf("rozložení grafiky: poloha musí být 0–100 %% a měřítko 30–300 %%")
		}
	}
	if l.Opacity < 0 || l.Opacity > 100 {
		return fmt.Errorf("průhlednost pozadí grafiky musí být 0–100 %%")
	}
	p := c.Picture
	if p.CropX < 0 || p.CropY < 0 || p.CropW < 5 || p.CropH < 5 || p.CropX+p.CropW > 100.001 || p.CropY+p.CropH > 100.001 {
		return fmt.Errorf("výřez obrazu musí ležet uvnitř snímku a mít aspoň 5 %% šířky i výšky")
	}
	switch p.Fit {
	case "orez", "pruhy", "roztahnout":
	default:
		return fmt.Errorf("neznámé přizpůsobení na 16:9 %q (orez | pruhy | roztahnout)", p.Fit)
	}
	if err := c.Rules.Validate(); err != nil {
		return fmt.Errorf("pravidla: %w", err)
	}
	if c.App.ImportSeconds != 0 && (c.App.ImportSeconds < 30 || c.App.ImportSeconds > 900) {
		return fmt.Errorf("interval načítání výsledků musí být 30–900 s (%d)", c.App.ImportSeconds)
	}
	if c.App.Port <= 0 || c.App.Port > 65535 {
		return fmt.Errorf("port aplikace musí být 1–65535 (%d)", c.App.Port)
	}
	return nil
}

// CameraURL builds the RTSP URL; user and password are URL-escaped.
func (c Camera) CameraURL() string {
	if c.URL != "" {
		return c.URL
	}
	u := url.URL{
		Scheme: "rtsp",
		Host:   c.Host + ":" + strconv.Itoa(c.Port),
		Path:   "/" + c.Path,
	}
	if c.User != "" {
		u.User = url.UserPassword(c.User, c.Password)
	}
	return u.String()
}

var reRes = regexp.MustCompile(`^\d{2,5}x\d{2,5}$`)

var credsInURL = regexp.MustCompile(`(?i)(rtsps?|rtmps?|https?)://([^:/@\s]*):[^@\s]*@`)

// Mask removes secrets (camera password, stream key, credentials in any URL) from s.
// Every line that may contain them (ffmpeg output, errors) must pass through Mask before it is logged.
func (c Config) Mask(s string) string {
	s = credsInURL.ReplaceAllString(s, "$1://$2:***@")
	for _, secret := range []string{c.Camera.Password, url.QueryEscape(c.Camera.Password),
		url.PathEscape(c.Camera.Password), c.YouTube.Key, c.YouTube.ClientSecret, c.YouTube.RefreshToken, c.Cloud.RefreshToken} {
		if len(secret) >= 3 {
			s = strings.ReplaceAll(s, secret, "***")
		}
	}
	return s
}

// MaskedCameraURL is CameraURL without the password, for logs and UI.
func (c Camera) MaskedCameraURL() string {
	u, err := url.Parse(c.CameraURL())
	if err != nil {
		return "(neplatná URL)"
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.String()
}
