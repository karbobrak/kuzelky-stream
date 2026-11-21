package app

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"kuzelkystream/internal/config"
)

// TestImpactRoundTrip: settings that went to the page and back unchanged must not restart anything.
// KUZELKY_CFG=<path> uses a real config.json (default: built-in defaults with some secrets).
func TestImpactRoundTrip(t *testing.T) {
	c := config.Default()
	c.Camera.Password, c.YouTube.Key, c.YouTube.ClientSecret, c.YouTube.RefreshToken = "p", "k", "s", "r"
	if p := os.Getenv("KUZELKY_CFG"); p != "" {
		var err error
		if c, err = config.Load(p); err != nil {
			t.Fatal(err)
		}
	}
	// What GET /api/nastaveni sends …
	v := c
	v.Camera.Password, v.YouTube.Key, v.YouTube.ClientSecret, v.YouTube.RefreshToken = "", "", "", ""
	data, _ := json.Marshal(v)
	// … and what PUT reads back (same restore rules as web.readSettings).
	var back config.Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	back.Camera.Password, back.YouTube.Key, back.YouTube.ClientSecret = c.Camera.Password, c.YouTube.Key, c.YouTube.ClientSecret
	back.Overlay.Layout, back.Overlay.Presets = c.Overlay.Layout, c.Overlay.Presets
	back.YouTube.RefreshToken, back.YouTube.ChannelID = c.YouTube.RefreshToken, c.YouTube.ChannelID
	back.YouTube.ChannelTitle, back.YouTube.StreamID = c.YouTube.ChannelTitle, c.YouTube.StreamID

	if imp := impactOf(c, back); imp != ImpactNone {
		for _, f := range []string{"Camera", "YouTube", "Output", "Audio", "Picture", "Rules", "App"} {
			a, b := reflect.ValueOf(c).FieldByName(f).Interface(), reflect.ValueOf(back).FieldByName(f).Interface()
			if !reflect.DeepEqual(a, b) {
				t.Errorf("%s differs:\n  saved %+v\n  back  %+v", f, a, b)
			}
		}
		t.Fatalf("impact = %s, want none", imp)
	}
}
