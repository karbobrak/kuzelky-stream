package config

import (
	"strings"
	"testing"
)

func TestMask(t *testing.T) {
	c := Default()
	c.Camera.Password = "tajne.heslo@1"
	c.YouTube.Key = "abcd-efgh-ijkl-mnop"

	lines := []string{
		"Error opening input file rtsp://admin:tajne.heslo@1@192.168.100.163:554/h264Preview_01_main.",
		"Error opening input file " + c.Camera.CameraURL(),
		"rtmp://a.rtmp.youtube.com/live2/abcd-efgh-ijkl-mnop: I/O error",
		"Opening rtsp://user:other.password@10.0.0.1/stream",
	}
	for _, l := range lines {
		got := c.Mask(l)
		for _, secret := range []string{"tajne.heslo", "abcd-efgh", "other.password", "%40"} {
			if strings.Contains(got, secret) {
				t.Errorf("Mask(%q) = %q, still contains %q", l, got, secret)
			}
		}
	}
}

func TestCameraURLEscapesPassword(t *testing.T) {
	c := Default().Camera
	c.Password = "p@ss:w/rd"
	u := c.CameraURL()
	if strings.Count(u, "@") != 1 {
		t.Fatalf("password not escaped: %s", u)
	}
	if m := c.MaskedCameraURL(); strings.Contains(m, "p%40ss") {
		t.Fatalf("masked URL leaks password: %s", m)
	}
}
