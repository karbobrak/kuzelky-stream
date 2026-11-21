package stream

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"kuzelkystream/internal/config"
)

// FindTool looks for ffmpeg in <exe dir>\ffmpeg\, then next to the exe, then on PATH.
func FindTool(name string) (string, error) {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, p := range []string{
			filepath.Join(dir, "ffmpeg", name+".exe"),
			filepath.Join(dir, name+".exe"),
		} {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s nenalezen (patří do složky ffmpeg vedle aplikace)", name)
	}
	return p, nil
}

// encoderCandidates in order of preference.
var encoderCandidates = []string{"h264_nvenc", "h264_qsv", "h264_amf", "libx264"}

// DetectEncoder returns the first H.264 encoder that can actually encode on this machine.
// A listed encoder is not enough: NVENC is in the build even without an NVIDIA GPU.
func DetectEncoder(ctx context.Context, ffmpeg string) string {
	for _, enc := range encoderCandidates {
		if enc == "libx264" {
			return enc
		}
		if TestEncoder(ctx, ffmpeg, enc) == nil {
			return enc
		}
	}
	return "libx264"
}

func TestEncoder(ctx context.Context, ffmpeg, enc string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=1280x720:r=25:d=0.5",
		"-pix_fmt", pixFmtFor(enc), "-c:v", enc, "-f", "null", "-")
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", enc, err, out)
	}
	return nil
}

// Snapshot grabs one full-resolution frame from the camera as JPEG (for the crop editor).
func Snapshot(ctx context.Context, ffmpeg string, cam config.Camera) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	in := CameraInput(cam, false)
	if cam.TestSource {
		in = []string{"-f", "lavfi", "-i", "testsrc2=size=2880x1616:rate=1"}
	}
	args := append([]string{"-hide_banner", "-loglevel", "error"}, in...)
	args = append(args, "-frames:v", "1", "-q:v", "3", "-f", "image2pipe", "-c:v", "mjpeg", "-")
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = string(ee.Stderr)
		}
		return nil, fmt.Errorf("snímek z kamery: %v %s", err, msg)
	}
	return out, nil
}

// ProbeResult describes the camera stream.
type ProbeResult struct {
	VideoCodec string
	Width      int
	Height     int
	FPS        string
	HasAudio   bool
	AudioCodec string
}

// Probe asks ffmpeg what the camera sends. (ffmpeg, not ffprobe: one tool less to ship.)
func Probe(ctx context.Context, ffmpeg string, cam config.Camera) (ProbeResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := append([]string{"-hide_banner", "-loglevel", "info"}, CameraInput(cam, true)...)
	cmd := exec.CommandContext(ctx, ffmpeg, append(args, "-t", "0.1", "-f", "null", "-")...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	r := parseProbe(string(out))
	if r.VideoCodec == "" {
		last := strings.TrimSpace(string(out))
		if i := strings.LastIndex(last, "\n"); i >= 0 {
			last = last[i+1:]
		}
		if err == nil {
			return r, fmt.Errorf("kamera neposílá video")
		}
		if cam.IsWebcam() {
			return r, fmt.Errorf("webkameru nelze otevřít (není připojená, nebo ji používá jiný program?): %s", last)
		}
		return r, fmt.Errorf("kamera neodpovídá: %v %s", err, last)
	}
	return r, nil
}

var (
	reVideo = regexp.MustCompile(`Stream #0:\d+.*?: Video: (\w+).*?, (\d{2,5})x(\d{2,5})(?:.*?, ([\d.]+) fps)?`)
	reAudio = regexp.MustCompile(`Stream #0:\d+.*?: Audio: (\w+)`)
)

// parseProbe reads the input description ffmpeg prints, e.g.
// "Stream #0:0: Video: h264 (Main), yuvj420p(pc, progressive), 2880x1616, 25 fps, 25 tbr, 90k tbn".
func parseProbe(out string) ProbeResult {
	var r ProbeResult
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Output #") {
			break // the rest describes the output, not the camera
		}
		if m := reVideo.FindStringSubmatch(line); m != nil && r.VideoCodec == "" {
			r.VideoCodec = m[1]
			r.Width, _ = strconv.Atoi(m[2])
			r.Height, _ = strconv.Atoi(m[3])
			r.FPS = m[4]
		}
		if m := reAudio.FindStringSubmatch(line); m != nil && !r.HasAudio {
			r.HasAudio, r.AudioCodec = true, m[1]
		}
	}
	return r
}

// DShowDevices lists the webcams and microphones Windows offers (DirectShow names).
func DShowDevices(ctx context.Context, ffmpeg string) (video, audio []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	hideWindow(cmd)
	out, _ := cmd.CombinedOutput() // always "fails" on the dummy input
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	video, audio = parseDShowDevices(string(out))
	return video, audio, nil
}

var reDShowDev = regexp.MustCompile(`\] "(.+)" \((video|audio|none)\)`)

func parseDShowDevices(out string) (video, audio []string) {
	for _, line := range strings.Split(out, "\n") {
		m := reDShowDev.FindStringSubmatch(strings.TrimRight(line, "\r"))
		switch {
		case m == nil:
		case m[2] == "video":
			video = append(video, m[1])
		case m[2] == "audio":
			audio = append(audio, m[1])
		}
	}
	return video, audio
}

// WebcamModes lists a webcam's resolutions with the highest frame rate each offers, largest first.
func WebcamModes(ctx context.Context, ffmpeg, device string) ([]WebcamMode, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-list_options", "true", "-f", "dshow", "-i", "video="+device)
	hideWindow(cmd)
	out, _ := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	modes := parseWebcamModes(string(out))
	if len(modes) == 0 {
		return nil, fmt.Errorf("webkamera „%s“ nevrátila žádné rozlišení (není připojená, nebo ji používá jiný program?)", device)
	}
	return modes, nil
}

type WebcamMode struct {
	Size string `json:"rozliseni"`
	FPS  int    `json:"fps"`
}

var reDShowMode = regexp.MustCompile(`max s=(\d+)x(\d+) fps=([\d.]+)`)

func parseWebcamModes(out string) []WebcamMode {
	best := map[string]int{}
	area := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		m := reDShowMode.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		w, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		f, _ := strconv.ParseFloat(m[3], 64)
		size := m[1] + "x" + m[2]
		if int(f+0.5) > best[size] {
			best[size] = int(f + 0.5)
		}
		area[size] = w * h
	}
	var out2 []WebcamMode
	for s, f := range best {
		out2 = append(out2, WebcamMode{Size: s, FPS: f})
	}
	sort.Slice(out2, func(i, j int) bool { return area[out2[i].Size] > area[out2[j].Size] })
	return out2
}
