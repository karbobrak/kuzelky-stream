package app

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strings"
	"sync"
	"time"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/match"
	"kuzelkystream/internal/overlay"
	"kuzelkystream/internal/stream"
)

// snapshotCache avoids opening a new camera connection for every editor refresh.
type snapshotCache struct {
	mu   sync.Mutex
	data []byte
	at   time.Time
	key  config.Camera
}

// CameraSnapshot returns a recent full-resolution JPEG from the camera (cached for 3 s unless fresh).
func (a *App) CameraSnapshot(ctx context.Context, fresh bool) ([]byte, error) {
	cam := a.Config().Camera
	if cam.IsWebcam() && a.Running() {
		// A webcam can be opened by one program only, and the broadcast holds it.
		return nil, errBusyWebcam
	}
	a.snap.mu.Lock()
	defer a.snap.mu.Unlock()
	if !fresh && a.snap.data != nil && a.snap.key == cam && time.Since(a.snap.at) < 3*time.Second {
		return a.snap.data, nil
	}
	data, err := stream.Snapshot(ctx, a.ffmpeg, cam)
	if err != nil {
		return nil, errors.New(a.Config().Mask(err.Error()))
	}
	a.snap.data, a.snap.at, a.snap.key = data, time.Now(), cam
	return data, nil
}

// BlockImage renders one block of the graphics for the layout editor (sample data if no match).
func (a *App) BlockImage(which string) (*image.RGBA, error) {
	cfg := a.Config()
	ren, err := overlay.NewRenderer(cfg.Output.Width, cfg.Output.Height)
	if err != nil {
		return nil, err
	}
	a.mt.mu.Lock()
	var m match.Match
	if a.mt.cur != nil {
		m = *a.mt.cur
	} else {
		m = overlay.SampleMatch(cfg.Rules)
	}
	a.mt.mu.Unlock()
	return ren.Block(overlay.ScoreView{Match: m, Result: match.Compute(m, cfg.Rules), Rules: cfg.Rules,
		Layout: cfg.Overlay.Layout}, which), nil
}

// SetLayout moves/resizes the graphics. Applied to the stream immediately, no restart.
// Called many times while dragging, so it does not log.
func (a *App) SetLayout(l overlay.Layout) error {
	a.mu.Lock()
	c := a.cfg
	a.mu.Unlock()
	old := c
	c.Overlay.Layout = l
	a.stampShared(old, &c)
	if err := c.Validate(); err != nil {
		return err
	}
	if err := config.Save(a.cfgPath, c); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg.Overlay.Layout = l
	a.cfg.Cloud.SettingsChanged = c.Cloud.SettingsChanged
	a.mu.Unlock()
	a.redraw()
	a.sharedChanged(old, c)
	return nil
}

// Preset saves, loads or deletes a named layout.
func (a *App) Preset(action, name string) error {
	name = strings.TrimSpace(name)
	if name == "" && action != "vychozi" {
		return errors.New("zadejte název předvolby")
	}
	a.mu.Lock()
	c := a.cfg
	presets := map[string]overlay.Layout{}
	for k, v := range c.Overlay.Presets {
		presets[k] = v
	}
	a.mu.Unlock()
	switch action {
	case "ulozit":
		presets[name] = c.Overlay.Layout
	case "smazat":
		delete(presets, name)
	case "nacist":
		l, ok := presets[name]
		if !ok {
			return fmt.Errorf("předvolba %q neexistuje", name)
		}
		return a.SetLayout(l)
	case "vychozi":
		return a.SetLayout(overlay.DefaultLayout())
	default:
		return fmt.Errorf("neznámá akce %q", action)
	}
	old := c
	c.Overlay.Presets = presets
	a.stampShared(old, &c)
	if err := config.Save(a.cfgPath, c); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg.Overlay.Presets = presets
	a.cfg.Cloud.SettingsChanged = c.Cloud.SettingsChanged
	a.mu.Unlock()
	a.sharedChanged(old, c)
	a.Log.Printf("předvolba rozložení „%s“: %s", name, action)
	return nil
}
