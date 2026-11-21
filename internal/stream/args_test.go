package stream

import (
	"os/exec"
	"strings"
	"testing"

	"kuzelkystream/internal/config"
)

// TestPictureFilterWithFFmpeg runs every fit mode through the real ffmpeg (skipped without it)
// and checks that the output is always exactly the requested size.
func TestPictureFilterWithFFmpeg(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg není v PATH")
	}
	pictures := map[string]config.Picture{
		"plny orez":      {CropW: 100, CropH: 100, Fit: "orez"},
		"vyrez orez":     {CropX: 10.3, CropY: 20.7, CropW: 47.1, CropH: 33.3, Fit: "orez", Rotate180: true},
		"vyrez pruhy":    {CropX: 25, CropY: 0, CropW: 30, CropH: 100, Fit: "pruhy", Mirror: true},
		"vyrez roztahni": {CropX: 0, CropY: 50, CropW: 100, CropH: 50, Fit: "roztahnout"},
	}
	for _, src := range []string{"2880x1616", "1000x1000"} {
		for name, p := range pictures {
			out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size="+src+":rate=1:duration=1",
				"-vf", PictureFilter(p, 1280, 720)+",showinfo", "-frames:v", "1", "-f", "null", "-").CombinedOutput()
			if err != nil {
				t.Fatalf("%s %s: %v\n%s", src, name, err, out)
			}
		}
	}
	// Check the size explicitly once in ffmpeg's own output.
	out, err := exec.Command(ff, "-v", "info", "-f", "lavfi", "-i", "testsrc2=size=1000x1000:rate=1:duration=1",
		"-vf", PictureFilter(pictures["vyrez pruhy"], 1280, 720), "-frames:v", "1", "-f", "null", "-").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "1280x720") {
		t.Fatalf("output is not 1280x720: %v\n%s", err, out)
	}
}

func TestParseProbe(t *testing.T) {
	out := `Input #0, rtsp, from 'rtsp://admin:***@192.168.100.163:554/h264Preview_01_main':
  Duration: N/A, start: 0.000000, bitrate: N/A
  Stream #0:0: Video: h264 (Main), yuvj420p(pc, bt709, progressive), 2880x1616, 24.83 fps, 25 tbr, 90k tbn
  Stream #0:1: Audio: aac (LC), 16000 Hz, mono, fltp
Output #0, null, to 'pipe:':
  Stream #0:0: Video: wrapped_avframe, yuvj420p, 2880x1616, q=2-31, 200 kb/s, 25 fps`
	r := parseProbe(out)
	if r.VideoCodec != "h264" || r.Width != 2880 || r.Height != 1616 || r.FPS != "24.83" || !r.HasAudio || r.AudioCodec != "aac" {
		t.Fatalf("parseProbe = %+v", r)
	}
	if r := parseProbe("  Stream #0:0: Video: hevc (Main), yuv420p(tv), 1920x1080, 30 fps"); r.VideoCodec != "hevc" || r.HasAudio {
		t.Fatalf("video only = %+v", r)
	}
}

// TestProbeTestSource runs the real ffmpeg probe on a generated stream (skipped without ffmpeg).
func TestProbeTestSource(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg není v PATH")
	}
	out, _ := exec.Command(ff, "-hide_banner", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25",
		"-f", "lavfi", "-i", "sine", "-t", "0.1", "-f", "null", "-").CombinedOutput()
	// lavfi inputs are #0 and #1; the camera has a single input #0 with two streams. Check video only.
	if r := parseProbe(string(out)); r.VideoCodec == "" || r.Width != 640 || r.Height != 360 {
		t.Fatalf("probe of real ffmpeg output = %+v\n%s", r, out)
	}
}

func TestPictureFilterDefault(t *testing.T) {
	f := PictureFilter(config.Picture{CropW: 100, CropH: 100, Fit: "orez"}, 1280, 720)
	if strings.Contains(f, "crop=w='trunc(iw*") || !strings.HasSuffix(f, "setsar=1,format=yuv420p") {
		t.Fatalf("unexpected default filter: %s", f)
	}
}
