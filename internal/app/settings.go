package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/google"
)

// Settings shared through Google Drive. Every computer signed in to the club's Drive gets the
// same camera, YouTube channel and broadcast, graphics, crop and rules (config.Shared); only the
// Drive sign-in, the encoder and the app's port stay per computer. A new notebook therefore
// needs just the Drive sign-in.
//
// Conflicts are settled by time: Cloud.SettingsChanged is stamped whenever the shared part
// changes and the newer copy wins. A computer that has never compared with Drive yet
// (SettingsSynced false, e.g. a fresh install with default settings) always takes the Drive copy,
// so its defaults cannot overwrite the club's settings.

// stampShared sets the change time when the shared part differs (call with a.mu held or on a copy).
func (a *App) stampShared(old config.Config, c *config.Config) {
	if !reflect.DeepEqual(old.Shared(), c.Shared()) {
		c.Cloud.SettingsChanged = time.Now().UnixMilli()
	}
}

// sharedChanged queues the settings for upload when their shared part changed.
func (a *App) sharedChanged(old, c config.Config) {
	if !reflect.DeepEqual(old.Shared(), c.Shared()) {
		a.markDirty("nastaveni")
	}
}

// syncSettings compares the settings with the Drive copy during a full comparison (start-up,
// sign-in, "Synchronizovat teď"): downloads a newer copy, or queues this one for upload.
func (a *App) syncSettings(ctx context.Context, cl *google.Client, remote []google.DriveFile, dirty map[string]bool) error {
	c := a.Config()
	var r *google.DriveFile
	for i := range remote {
		if remote[i].Name == settingsFile {
			r = &remote[i]
		}
	}
	switch {
	case r != nil && (!c.Cloud.SettingsSynced || remoteTime(*r).UnixMilli() > c.Cloud.SettingsChanged):
		return a.downloadSettings(ctx, cl, *r)
	case r == nil || remoteTime(*r).UnixMilli() < c.Cloud.SettingsChanged:
		if c.Cloud.SettingsChanged == 0 {
			a.updateConfig(func(cc *config.Config) { cc.Cloud.SettingsChanged = time.Now().UnixMilli() })
		}
		dirty["nastaveni"] = true
	}
	if !c.Cloud.SettingsSynced {
		a.updateConfig(func(cc *config.Config) { cc.Cloud.SettingsSynced = true })
	}
	return nil
}

// downloadSettings applies the Drive copy of the shared settings to this computer.
func (a *App) downloadSettings(ctx context.Context, cl *google.Client, f google.DriveFile) error {
	body, err := cl.Download(ctx, f.ID)
	if err != nil {
		return err
	}
	shared := config.Default() // fields missing in an older copy keep their defaults
	if err := json.Unmarshal(body, &shared); err != nil {
		return fmt.Errorf("nastavení na Disku je poškozené: %w", err)
	}
	local := a.Config()
	nc := shared.WithLocal(local)
	nc.Cloud.SettingsChanged = remoteTime(f).UnixMilli()
	nc.Cloud.SettingsSynced = true
	if reflect.DeepEqual(nc, local) {
		return nil
	}
	if _, err := a.applyConfig(nc, true); err != nil {
		return fmt.Errorf("nastavení z Disku nelze použít: %w", err)
	}
	if !reflect.DeepEqual(nc.Shared(), local.Shared()) {
		a.Log.Print("Google Disk: načteno nastavení oddílu (kamera, YouTube, grafika, pravidla)")
	}
	return nil
}
