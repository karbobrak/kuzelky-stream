// Package web serves the browser UI and its JSON API on 127.0.0.1 only.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image/jpeg"
	"image/png"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kuzelkystream/internal/app"
	"kuzelkystream/internal/config"
	"kuzelkystream/internal/match"
	"kuzelkystream/internal/overlay"
)

//go:embed static
var static embed.FS

type Server struct {
	app  *app.App
	port int
}

// Listen binds 127.0.0.1 on the wanted port, or the next free one (up to +20).
func Listen(port int) (net.Listener, int, error) {
	var lastErr error
	for p := port; p <= port+20; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			return ln, p, nil
		}
		lastErr = err
	}
	return nil, 0, lastErr
}

func Serve(ln net.Listener, port int, a *app.App) error {
	s := &Server{app: a, port: port}
	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	mux.Handle("GET /", http.FileServerFS(sub))

	mux.HandleFunc("GET /api/stav", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.Status()) })
	mux.HandleFunc("POST /api/start", func(w http.ResponseWriter, r *http.Request) { reply(w, nil, a.Start()) })
	mux.HandleFunc("POST /api/stop", func(w http.ResponseWriter, r *http.Request) { a.Stop(); reply(w, nil, nil) })
	mux.HandleFunc("POST /api/restart-kamery", func(w http.ResponseWriter, r *http.Request) { a.RestartCamera(); reply(w, nil, nil) })
	mux.HandleFunc("POST /api/restart-vysilani", func(w http.ResponseWriter, r *http.Request) { a.RestartOutput(); reply(w, nil, nil) })
	mux.HandleFunc("POST /api/grafika", s.overlay)

	mux.HandleFunc("GET /api/nastaveni", s.getSettings)
	mux.HandleFunc("POST /api/nastaveni/kontrola", s.checkSettings)
	mux.HandleFunc("PUT /api/nastaveni", s.putSettings)
	mux.HandleFunc("POST /api/nastaveni/znovu-nacist", func(w http.ResponseWriter, r *http.Request) {
		imp, err := a.ReloadConfig()
		reply(w, map[string]any{"dopad": imp}, err)
	})
	mux.HandleFunc("POST /api/test/kamera", s.testCamera)
	mux.HandleFunc("POST /api/test/kodery", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.TestEncoders(r.Context()))
	})
	mux.HandleFunc("GET /api/log", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("od"), 10, 64)
		writeJSON(w, a.LogBuf.Since(after, 200))
	})
	mux.HandleFunc("GET /api/nahled.jpg", s.preview)

	// Match and scoring. Every change answers with the whole match view, so the page re-renders from it.
	matchReply := func(w http.ResponseWriter, err error) {
		if err != nil {
			reply(w, nil, err)
			return
		}
		writeJSON(w, a.MatchView())
	}
	mux.HandleFunc("GET /api/zapas", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.MatchView()) })
	mux.HandleFunc("POST /api/zapas/novy", func(w http.ResponseWriter, r *http.Request) {
		var m match.Match
		if err := decode(r, &m); err != nil {
			reply(w, nil, err)
			return
		}
		matchReply(w, a.NewMatch(m))
	})
	mux.HandleFunc("PUT /api/zapas/nastaveni", func(w http.ResponseWriter, r *http.Request) {
		var m match.Match
		if err := decode(r, &m); err != nil {
			reply(w, nil, err)
			return
		}
		matchReply(w, a.UpdateSetup(m))
	})
	mux.HandleFunc("POST /api/zapas/hodnota", func(w http.ResponseWriter, r *http.Request) {
		var c app.Cell
		if err := decode(r, &c); err != nil {
			reply(w, nil, err)
			return
		}
		matchReply(w, a.SetCell(c))
	})
	mux.HandleFunc("POST /api/zapas/cast", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Part int `json:"cast"`
		}
		if err := decode(r, &b); err != nil {
			reply(w, nil, err)
			return
		}
		matchReply(w, a.SetPart(b.Part))
	})
	mux.HandleFunc("POST /api/zapas/grafika", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Mode    string `json:"rezim"`
			Message string `json:"zprava"`
		}
		if err := decode(r, &b); err != nil {
			reply(w, nil, err)
			return
		}
		matchReply(w, a.SetMode(b.Mode, b.Message))
	})
	mux.HandleFunc("POST /api/zapas/zpet", func(w http.ResponseWriter, r *http.Request) { matchReply(w, a.Undo()) })
	mux.HandleFunc("POST /api/zapas/zavrit", func(w http.ResponseWriter, r *http.Request) { a.CloseMatch(); matchReply(w, nil) })
	mux.HandleFunc("GET /api/zapasy", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.SavedMatches()) })
	mux.HandleFunc("POST /api/zapasy/otevrit", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			File string `json:"soubor"`
		}
		if err := decode(r, &b); err != nil {
			reply(w, nil, err)
			return
		}
		matchReply(w, a.OpenMatch(b.File))
	})
	mux.HandleFunc("GET /api/soupisky", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.Rosters()) })
	mux.HandleFunc("GET /api/grafika.png", func(w http.ResponseWriter, r *http.Request) {
		img, err := a.OverlayPreview()
		if err != nil || img == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		_ = enc.Encode(w, img)
	})

	// Google Drive backup and results spreadsheet.
	mux.HandleFunc("GET /api/cloud", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.CloudStatus()) })
	mux.HandleFunc("POST /api/cloud/synchronizovat", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		reply(w, nil, a.SyncNow(ctx))
	})
	mux.HandleFunc("POST /api/cloud/obnovit-grafiku", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		reply(w, nil, a.RestoreGraphics(ctx))
	})

	// Editors (crop, layout).
	mux.HandleFunc("GET /api/kamera/snimek.jpg", func(w http.ResponseWriter, r *http.Request) {
		data, err := a.CameraSnapshot(r.Context(), r.URL.Query().Get("novy") == "1")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /api/grafika/blok.png", func(w http.ResponseWriter, r *http.Request) {
		img, err := a.BlockImage(r.URL.Query().Get("blok"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_ = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(w, img)
	})
	mux.HandleFunc("POST /api/grafika/rozlozeni", func(w http.ResponseWriter, r *http.Request) {
		var l overlay.Layout
		if err := decode(r, &l); err != nil {
			reply(w, nil, err)
			return
		}
		reply(w, nil, a.SetLayout(l))
	})
	mux.HandleFunc("POST /api/grafika/predvolba", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Action string `json:"akce"`
			Name   string `json:"nazev"`
		}
		if err := decode(r, &b); err != nil {
			reply(w, nil, err)
			return
		}
		err := a.Preset(b.Action, b.Name)
		cfg := a.Config()
		reply(w, map[string]any{"rozlozeni": cfg.Overlay.Layout, "predvolby": cfg.Overlay.Presets}, err)
	})

	mux.HandleFunc("POST /api/youtube/prihlaseni", func(w http.ResponseWriter, r *http.Request) {
		u, err := a.BeginLogin(fmt.Sprintf("http://127.0.0.1:%d/oauth2/callback", port), app.LoginYouTube)
		reply(w, map[string]any{"url": u}, err)
	})
	mux.HandleFunc("POST /api/cloud/prihlaseni", func(w http.ResponseWriter, r *http.Request) {
		u, err := a.BeginLogin(fmt.Sprintf("http://127.0.0.1:%d/oauth2/callback", port), app.LoginDrive)
		reply(w, map[string]any{"url": u}, err)
	})
	mux.HandleFunc("POST /api/cloud/odhlasit", func(w http.ResponseWriter, r *http.Request) { a.LogoutDrive(); reply(w, nil, nil) })
	mux.HandleFunc("GET /oauth2/callback", s.oauthCallback)
	mux.HandleFunc("POST /api/youtube/odhlasit", func(w http.ResponseWriter, r *http.Request) { a.Logout(); reply(w, nil, nil) })
	mux.HandleFunc("POST /api/youtube/ukoncit-prenos", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		reply(w, nil, a.EndBroadcast(ctx))
	})

	srv := &http.Server{Handler: s.guard(mux), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

// guard rejects requests that did not come from this app's own page: a web page on the internet
// could otherwise make the browser call 127.0.0.1 (CSRF, DNS rebinding).
func (s *Server) guard(next http.Handler) http.Handler {
	allowed := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", s.port): true,
		fmt.Sprintf("localhost:%d", s.port): true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "nepovolený Host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" && !allowed[strings.TrimPrefix(o, "http://")] {
				http.Error(w, "nepovolený Origin", http.StatusForbidden)
				return
			}
			// A JSON content type cannot be sent by a plain HTML form without a CORS preflight.
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, "očekávám application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) overlay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On bool `json:"zapnuto"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, nil, err)
		return
	}
	s.app.SetOverlay(body.On)
	reply(w, nil, nil)
}

// settingsView is the config for the browser. Secrets are never sent back; the page only learns
// whether they are set. An empty secret in a PUT means "keep the saved one".
type settingsView struct {
	Cfg       config.Config `json:"nastaveni"`
	PassSet   bool          `json:"hesloNastaveno"`
	KeySet    bool          `json:"klicNastaven"`
	SecretSet bool          `json:"secretNastaven"`
	Running   bool          `json:"bezi"`
	FileLabel string        `json:"soubor"`
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	c := s.app.Config()
	v := settingsView{Cfg: c, PassSet: c.Camera.Password != "", KeySet: c.YouTube.Key != "",
		SecretSet: c.YouTube.ClientSecret != "", Running: s.app.Running(), FileLabel: "config.json"}
	v.Cfg.Camera.Password, v.Cfg.YouTube.Key, v.Cfg.YouTube.ClientSecret, v.Cfg.YouTube.RefreshToken = "", "", "", ""
	v.Cfg.Cloud.RefreshToken = ""
	writeJSON(w, v)
}

// readSettings decodes a config from the page and fills in the saved secrets where left empty.
func (s *Server) readSettings(r *http.Request) (config.Config, error) {
	var c config.Config
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("neplatná data: %w", err)
	}
	saved := s.app.Config()
	if c.Camera.Password == "" {
		c.Camera.Password = saved.Camera.Password
	}
	if c.YouTube.Key == "" {
		c.YouTube.Key = saved.YouTube.Key
	}
	if c.YouTube.ClientSecret == "" {
		c.YouTube.ClientSecret = saved.YouTube.ClientSecret
	}
	// The layout has its own editor (saved live while dragging); the form never changes it.
	c.Overlay.Layout, c.Overlay.Presets = saved.Overlay.Layout, saved.Overlay.Presets
	// Managed by the app (sign-in), never taken from the form.
	c.YouTube.RefreshToken, c.YouTube.ChannelID = saved.YouTube.RefreshToken, saved.YouTube.ChannelID
	c.YouTube.ChannelTitle, c.YouTube.StreamID = saved.YouTube.ChannelTitle, saved.YouTube.StreamID
	c.Cloud.Scopes, c.Cloud.FolderID, c.Cloud.SheetID = saved.Cloud.Scopes, saved.Cloud.FolderID, saved.Cloud.SheetID
	c.Cloud.RefreshToken, c.Cloud.Account = saved.Cloud.RefreshToken, saved.Cloud.Account
	return c, nil
}

func (s *Server) checkSettings(w http.ResponseWriter, r *http.Request) {
	c, err := s.readSettings(r)
	if err != nil {
		reply(w, nil, err)
		return
	}
	imp, err := s.app.CheckConfig(c)
	reply(w, map[string]any{"dopad": imp}, err)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	c, err := s.readSettings(r)
	if err != nil {
		reply(w, nil, err)
		return
	}
	imp, err := s.app.ApplyConfig(c)
	reply(w, map[string]any{"dopad": imp}, err)
}

func (s *Server) testCamera(w http.ResponseWriter, r *http.Request) {
	var cam config.Camera
	if err := json.NewDecoder(r.Body).Decode(&cam); err != nil {
		reply(w, nil, err)
		return
	}
	if cam.Password == "" {
		cam.Password = s.app.Config().Camera.Password
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	pr, err := s.app.ProbeCamera(ctx, cam)
	reply(w, map[string]any{
		"kodek": pr.VideoCodec, "sirka": pr.Width, "vyska": pr.Height, "fps": pr.FPS,
		"zvuk": pr.HasAudio, "zvukKodek": pr.AudioCodec,
	}, err)
}

// oauthCallback is where Google sends the browser back after the sign-in.
func (s *Server) oauthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	title, msg := "Přihlášení se nepovedlo", ""
	ok := false
	switch {
	case q.Get("error") != "":
		msg = "Google vrátil: " + q.Get("error")
	default:
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		ch, err := s.app.FinishLogin(ctx, q.Get("state"), q.Get("code"))
		if err != nil {
			msg = err.Error()
		} else {
			ok, title, msg = true, "Přihlášeno k YouTube", "Kanál: "+ch
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	color := "#c53030"
	if ok {
		color = "#1d8a4e"
	}
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<body style="font:16px Segoe UI,sans-serif;display:grid;place-items:center;height:90vh;background:#f3f5f9">
<div style="background:#fff;padding:28px 36px;border-radius:12px;border:1px solid #d9dee7;max-width:520px">
<h2 style="color:%s;margin-top:0">%s</h2><p>%s</p>
<p><a href="/#nastaveni">Zpět do aplikace</a> – toto okno můžete zavřít.</p></div>`,
		html.EscapeString(title), color, html.EscapeString(title), html.EscapeString(msg))
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	img := s.app.Preview()
	if img == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	_ = jpeg.Encode(w, img, &jpeg.Options{Quality: 75})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("neplatná data: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// reply writes {"ok":true,...} or {"ok":false,"chyba":"..."} with status 400.
func reply(w http.ResponseWriter, data map[string]any, err error) {
	if data == nil {
		data = map[string]any{}
	}
	if err != nil {
		var se *json.SyntaxError
		if errors.As(err, &se) {
			err = errors.New("neplatná data")
		}
		data["ok"], data["chyba"] = false, err.Error()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(data)
		return
	}
	data["ok"] = true
	writeJSON(w, data)
}
