// Package app ties together the settings, the streaming engine and the graphics; the web UI calls it.
package app

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/overlay"
	"kuzelkystream/internal/stream"
)

type App struct {
	Log     *log.Logger
	LogBuf  *LogBuf
	cfgPath string
	dir     string // app folder; relative paths in the settings are relative to it
	ffmpeg  string
	// KeepAwake is called with true while streaming (stop Windows from sleeping) and false after.
	KeepAwake func(bool)
	// Version of the build (set by build.ps1 through -ldflags).
	Version string

	// OAuth client built into the exe (build.ps1); used when the settings have none.
	builtinID, builtinSecret string

	mu       sync.Mutex
	cfg      config.Config
	encoder  string // resolved encoder of the running engine
	eng      *stream.Engine
	cancel   context.CancelFunc
	done     chan struct{}
	started  time.Time
	starting bool

	yt ytState

	overlayOn atomic.Bool
	// overlayBeforeStop: was the graphics on before the match was interrupted (restored on resume).
	overlayBeforeStop atomic.Bool
	ovMu              sync.Mutex
	ren               *overlay.Renderer
	ovImg             *image.RGBA
	ovVer             uint64
	gfxGen            atomic.Uint64 // bumped on every change that affects the graphics

	mt   matchState
	snap snapshotCache
	cl   cloudState
	imp  importState

	camFPS atomic.Value // float64, measured once per second
}

func New(dir, cfgPath string, cfg config.Config, ffmpeg string, logger *log.Logger, buf *LogBuf) *App {
	a := &App{Log: logger, LogBuf: buf, dir: dir, cfgPath: cfgPath, cfg: cfg, ffmpeg: ffmpeg,
		KeepAwake: func(bool) {}}
	a.overlayOn.Store(cfg.Overlay.OnAtStart)
	a.camFPS.Store(0.0)
	a.cl.wake = make(chan struct{}, 1)
	a.imp.wake = make(chan struct{}, 1)
	a.initMatch()
	return a
}

// UseBuiltinClient sets the Google OAuth client compiled into the exe. Settings without their own
// Client ID get it, so a club only signs in and never visits Google Cloud.
func (a *App) UseBuiltinClient(id, secret string) {
	if id == "" || secret == "" {
		return
	}
	a.mu.Lock()
	a.builtinID, a.builtinSecret = id, secret
	a.applyBuiltin(&a.cfg)
	a.mu.Unlock()
}

// HasBuiltinClient reports whether the exe carries its own OAuth client.
func (a *App) HasBuiltinClient() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.builtinID != ""
}

func (a *App) applyBuiltin(c *config.Config) {
	if c.YouTube.ClientID == "" && a.builtinID != "" {
		c.YouTube.ClientID, c.YouTube.ClientSecret = a.builtinID, a.builtinSecret
	}
}

func (a *App) Config() config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

func (a *App) Running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.eng != nil
}

// ---------------------------------------------------------------- start / stop

func (a *App) Start() error {
	a.mu.Lock()
	if a.eng != nil {
		a.mu.Unlock()
		return nil
	}
	if a.starting {
		a.mu.Unlock()
		return errors.New("vysílání se právě spouští")
	}
	a.starting = true
	cfg := a.cfg
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.starting = false
		a.mu.Unlock()
	}()

	if err := cfg.Validate(); err != nil {
		return err
	}
	useAPI := cfg.Output.Target == "youtube" && cfg.YouTube.Mode == "api"
	switch {
	case useAPI && !cfg.YouTube.APIReady():
		return errors.New("nejdřív se přihlaste k YouTube (Nastavení → YouTube → Přihlásit)")
	case cfg.Output.Target == "youtube" && cfg.YouTube.Mode == "klic" && cfg.YouTube.Key == "":
		return errors.New("chybí klíč streamu YouTube (Nastavení → YouTube)")
	}
	if useAPI {
		// Outside a.mu: the API calls take a few seconds and the status page must stay responsive.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		err := a.prepareYouTube(ctx, cfg)
		cancel()
		if err != nil {
			a.setYTError(err)
			return err
		}
		cfg = a.Config() // prepareYouTube may have saved a new stream id
	}

	enc := cfg.Output.Encoder
	if enc == "auto" {
		enc = stream.DetectEncoder(context.Background(), a.ffmpeg)
	}
	ren, err := overlay.NewRenderer(cfg.Output.Width, cfg.Output.Height)
	if err != nil {
		return err
	}
	a.ovMu.Lock()
	a.ren, a.ovImg = ren, nil
	a.ovMu.Unlock()

	eng, err := stream.New(stream.Options{
		Cfg: a.runtime(cfg), FFmpeg: a.ffmpeg, Encoder: enc, Log: a.Log,
		Overlay: a.overlayFrame,
		Slate:   ren.Slate("Přenos z kamery bude za chvíli obnoven", "Omlouváme se za technické potíže"),
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.mu.Lock()
	a.eng, a.cancel, a.done, a.encoder, a.started = eng, cancel, done, enc, time.Now()
	a.mu.Unlock()
	a.Log.Printf("START vysílání: kodér %s, %dx%d %d fps, cíl %s, kamera %s", enc,
		cfg.Output.Width, cfg.Output.Height, cfg.Output.FPS, cfg.Output.Target, cfg.Camera.Describe())
	a.KeepAwake(true)
	go func() {
		defer close(done)
		eng.Run(ctx)
	}()
	go a.measureCamera(ctx, eng)
	if useAPI {
		go a.monitorYouTube(ctx)
	}
	return nil
}

// Stop ends the stream cleanly and waits until both ffmpeg processes have ended.
func (a *App) Stop() {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.mu.Unlock()
	if cancel == nil {
		return
	}
	a.Log.Print("STOP vysílání")
	cancel()
	<-done
	a.mu.Lock()
	a.eng, a.cancel, a.done = nil, nil, nil
	a.mu.Unlock()
	a.KeepAwake(false)
	a.camFPS.Store(0.0)
}

func (a *App) RestartCamera() {
	if e := a.engine(); e != nil {
		a.Log.Print("ruční restart kamery")
		e.RestartCamera()
	}
}

func (a *App) RestartOutput() {
	if e := a.engine(); e != nil {
		a.Log.Print("ruční restart vysílání")
		e.RestartOutput()
	}
}

func (a *App) engine() *stream.Engine {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.eng
}

func (a *App) measureCamera(ctx context.Context, eng *stream.Engine) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var last int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n := eng.Stats().CameraFrames
		a.camFPS.Store(float64(n - last))
		last = n
	}
}

// ---------------------------------------------------------------- settings

// Impact says what applying new settings does to a running stream.
type Impact string

const (
	ImpactNone   Impact = "zadny"    // applied live
	ImpactCamera Impact = "kamera"   // camera reconnects; viewers briefly see the replacement picture
	ImpactOutput Impact = "vysilani" // YouTube connection restarts (short interruption)
	ImpactAll    Impact = "vse"      // full restart of the stream
)

func impactOf(old, new config.Config) Impact {
	oo, no := old.Output, new.Output
	if oo.Width != no.Width || oo.Height != no.Height || oo.FPS != no.FPS {
		return ImpactAll
	}
	outChanged := old.YouTube != new.YouTube || oo != no || old.Audio.Bitrate != new.Audio.Bitrate
	camChanged := old.Camera != new.Camera || old.Audio.Source != new.Audio.Source || old.Picture != new.Picture
	switch {
	case outChanged && camChanged:
		return ImpactAll
	case outChanged:
		return ImpactOutput
	case camChanged:
		return ImpactCamera
	}
	return ImpactNone
}

// CheckConfig validates new settings and tells what saving them would do, without saving.
func (a *App) CheckConfig(c config.Config) (Impact, error) {
	if err := c.Validate(); err != nil {
		return ImpactNone, err
	}
	if !a.Running() {
		return ImpactNone, nil
	}
	return impactOf(a.Config(), c), nil
}

// ApplyConfig validates, saves config.json and applies the settings to a running stream.
func (a *App) ApplyConfig(c config.Config) (Impact, error) {
	a.mu.Lock()
	a.applyBuiltin(&c)
	a.mu.Unlock()
	if err := c.Validate(); err != nil {
		return ImpactNone, err
	}
	if err := config.Save(a.cfgPath, c); err != nil {
		return ImpactNone, fmt.Errorf("nelze uložit %s: %w", a.cfgPath, err)
	}
	a.mu.Lock()
	old := a.cfg
	a.cfg = c
	eng := a.eng
	a.mu.Unlock()
	a.Log.Print("nastavení uloženo")
	if !old.Rules.Equal(c.Rules) {
		a.mt.mu.Lock()
		if a.mt.cur != nil {
			a.mt.cur.Reshape(c.Rules)
			_ = a.mt.store.Save(a.mt.file, *a.mt.cur)
		}
		a.mt.mu.Unlock()
	}
	a.redraw() // layout or rules may have changed
	if old.Picture != c.Picture || !old.Rules.Equal(c.Rules) {
		a.markDirty("grafika")
	}
	if eng == nil {
		return ImpactNone, nil
	}
	imp := impactOf(old, c)
	switch imp {
	case ImpactAll:
		a.Stop()
		return imp, a.Start()
	case ImpactOutput:
		eng.SetConfig(a.runtime(c))
		eng.RestartOutput()
	case ImpactCamera:
		eng.SetConfig(a.runtime(c))
		eng.RestartCamera()
	default:
		eng.SetConfig(a.runtime(c))
	}
	return imp, nil
}

// ReloadConfig reads config.json again (after a manual edit) and applies it.
func (a *App) ReloadConfig() (Impact, error) {
	c, err := config.Load(a.cfgPath)
	if err != nil && !errors.Is(err, config.ErrCreated) {
		return ImpactNone, err
	}
	return a.ApplyConfig(c)
}

// ---------------------------------------------------------------- tests from the settings page

func (a *App) ProbeCamera(ctx context.Context, cam config.Camera) (stream.ProbeResult, error) {
	if cam.TestSource {
		return stream.ProbeResult{VideoCodec: "testovací obrazec", Width: 2880, Height: 1616, FPS: "25", HasAudio: true}, nil
	}
	c := a.Config()
	if a.Running() && cam.IsWebcam() && c.Camera.IsWebcam() && cam.Webcam == c.Camera.Webcam {
		return stream.ProbeResult{}, errBusyWebcam
	}
	c.Camera = cam
	pr, err := stream.Probe(ctx, a.ffmpeg, cam)
	if err != nil {
		return pr, errors.New(c.Mask(err.Error()))
	}
	return pr, nil
}

// Webcams lists the webcams and microphones of this PC.
func (a *App) Webcams(ctx context.Context) (video, audio []string, err error) {
	return stream.DShowDevices(ctx, a.ffmpeg)
}

// WebcamModes lists the resolutions a webcam offers.
func (a *App) WebcamModes(ctx context.Context, device string) ([]stream.WebcamMode, error) {
	if a.Running() && a.Config().Camera.IsWebcam() && a.Config().Camera.Webcam == device {
		return nil, errBusyWebcam
	}
	return stream.WebcamModes(ctx, a.ffmpeg, device)
}

var errBusyWebcam = errors.New("webkameru právě používá vysílání – zkuste to, až se nevysílá")

// TestEncoders tries every encoder on this PC.
func (a *App) TestEncoders(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	for _, enc := range []string{"h264_nvenc", "h264_qsv", "h264_amf", "libx264"} {
		out[enc] = stream.TestEncoder(ctx, a.ffmpeg, enc) == nil
	}
	return out
}

// ---------------------------------------------------------------- graphics

func (a *App) SetOverlay(on bool) {
	a.overlayOn.Store(on)
	a.redraw()
	a.Log.Printf("grafika %s", map[bool]string{true: "zapnuta", false: "vypnuta"}[on])
}

func (a *App) OverlayOn() bool { return a.overlayOn.Load() }

// ---------------------------------------------------------------- status

type ProcStatus struct {
	State     stream.State `json:"stav"`
	Restarts  int          `json:"restartu"`
	LastError string       `json:"posledniChyba"`
}

type Status struct {
	Running      bool       `json:"bezi"`
	Since        string     `json:"od,omitempty"`
	Encoder      string     `json:"koder,omitempty"`
	Target       string     `json:"cil"`
	OverlayOn    bool       `json:"grafika"`
	ShowingSlate bool       `json:"nahradniObraz"`
	Camera       ProcStatus `json:"kamera"`
	CameraFPS    float64    `json:"kameraFps"`
	Output       ProcStatus `json:"vysilani"`
	OutFPS       float64    `json:"vysilaniFps"`
	BitrateKbps  float64    `json:"datovyTokKbps"`
	Speed        float64    `json:"rychlost"`
	Dropped      int64      `json:"zahozeno"`
	YouTube      YTStatus   `json:"youtube"`
	Version      string     `json:"verze"`
	MatchOpen    bool       `json:"zapasOtevren"`
	Interrupted  bool       `json:"preruseno"`
}

func (a *App) Status() Status {
	a.mu.Lock()
	eng, enc, started, target := a.eng, a.encoder, a.started, a.cfg.Output.Target
	a.mu.Unlock()
	st := Status{Target: target, OverlayOn: a.overlayOn.Load(), YouTube: a.ytStatus(), Version: a.Version}
	a.mt.mu.Lock()
	st.MatchOpen = a.mt.cur != nil
	st.Interrupted = st.MatchOpen && a.mt.cur.Interrupted
	a.mt.mu.Unlock()
	if eng == nil {
		st.Camera.State, st.Output.State = stream.Stopped, stream.Stopped
		return st
	}
	s := eng.Stats()
	st.Running, st.Encoder, st.Since = true, enc, started.Format(time.RFC3339)
	st.ShowingSlate, st.Dropped = s.ShowingSlate, s.Dropped
	st.Camera = ProcStatus{s.Camera.State, s.Camera.Restarts, s.Camera.LastError}
	st.Output = ProcStatus{s.Output.State, s.Output.Restarts, s.Output.LastError}
	st.CameraFPS = a.camFPS.Load().(float64)
	st.OutFPS, st.BitrateKbps, st.Speed = s.OutFPS, s.BitrateKbps, s.Speed
	return st
}

// Preview is the picture viewers see (nil when not streaming).
func (a *App) Preview() image.Image {
	if e := a.engine(); e != nil {
		return e.Preview()
	}
	return nil
}

// runtime resolves relative paths in the settings against the app folder.
func (a *App) runtime(c config.Config) config.Config {
	if c.Output.FilePath != "" && !filepath.IsAbs(c.Output.FilePath) {
		c.Output.FilePath = filepath.Join(a.dir, c.Output.FilePath)
	}
	_ = os.MkdirAll(filepath.Dir(c.Output.FilePath), 0o755)
	if c.YouTube.Mode == "api" {
		// The ingest address and key come from the API (prepareYouTube), not from the settings.
		a.yt.mu.Lock()
		c.YouTube.Server, c.YouTube.Key = a.yt.ingest.Address, a.yt.ingest.Key
		a.yt.mu.Unlock()
	}
	return c
}
