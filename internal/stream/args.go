package stream

import (
	"fmt"
	"strconv"
	"strings"

	"kuzelkystream/internal/config"
)

func pixFmtFor(enc string) string {
	switch enc {
	case "h264_qsv", "h264_amf":
		return "nv12"
	default:
		return "yuv420p"
	}
}

func encoderArgs(enc, x264Preset string) []string {
	switch enc {
	case "h264_nvenc":
		return []string{"-preset", "p4", "-tune", "ll", "-rc", "cbr", "-profile:v", "high"}
	case "h264_qsv":
		return []string{"-preset", "veryfast", "-look_ahead", "0", "-profile:v", "high"}
	case "h264_amf":
		return []string{"-usage", "lowlatency", "-rc", "cbr", "-quality", "speed", "-profile:v", "high"}
	default:
		return []string{"-preset", x264Preset, "-tune", "zerolatency", "-profile:v", "high", "-sc_threshold", "0"}
	}
}

// PictureFilter is the video filter chain: rotate/mirror → crop → make 16:9 → scale to w×h.
// Crop sizes are rounded to even pixels (needed for yuv420p).
func PictureFilter(p config.Picture, w, h int) string {
	var f []string
	if p.Rotate180 {
		f = append(f, "hflip", "vflip")
	}
	if p.Mirror {
		f = append(f, "hflip")
	}
	if p.CropX != 0 || p.CropY != 0 || p.CropW != 100 || p.CropH != 100 {
		pc := func(v float64) string { return strconv.FormatFloat(v/100, 'f', 5, 64) }
		f = append(f, fmt.Sprintf("crop=w='trunc(iw*%s/2)*2':h='trunc(ih*%s/2)*2':x='trunc(iw*%s/2)*2':y='trunc(ih*%s/2)*2'",
			pc(p.CropW), pc(p.CropH), pc(p.CropX), pc(p.CropY)))
	}
	switch p.Fit {
	case "pruhy": // letterbox / pillarbox with black bars
		f = append(f, fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=bicubic", w, h),
			fmt.Sprintf("pad=%d:%d:'(ow-iw)/2':'(oh-ih)/2':black", w, h))
	case "roztahnout":
		f = append(f, fmt.Sprintf("scale=%d:%d:flags=bicubic", w, h))
	default: // "orez": cut the centre to the output aspect ratio
		f = append(f,
			fmt.Sprintf("crop=w='trunc(min(iw,ih*%d/%d)/2)*2':h='trunc(min(ih,iw*%d/%d)/2)*2'", w, h, h, w),
			fmt.Sprintf("scale=%d:%d:flags=bicubic", w, h))
	}
	return strings.Join(append(f, "setsar=1", "format=yuv420p"), ",")
}

// IngestArgs: ffmpeg #1 reads the camera and writes raw I420 frames of the output size to stdout
// and, when withAudio, PCM to the app's TCP port. It may fail and restart at any time.
func IngestArgs(c config.Config, withAudio bool, audioPort int) []string {
	o := c.Output
	a := []string{"-hide_banner", "-loglevel", "warning", "-nostats"}
	if c.Camera.TestSource {
		a = append(a, "-re", "-f", "lavfi", "-i", "testsrc2=size=2880x1616:rate=25")
		if withAudio {
			a = append(a, "-re", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000")
		}
	} else {
		if o.HWDecode {
			a = append(a, "-hwaccel", "d3d11va")
		}
		a = append(a,
			"-rtsp_transport", c.Camera.Transport,
			"-timeout", "5000000",
			"-fflags", "nobuffer", "-flags", "low_delay",
			"-i", c.Camera.CameraURL())
	}
	a = append(a,
		"-map", "0:v:0",
		"-vf", PictureFilter(c.Picture, o.Width, o.Height),
		"-f", "rawvideo", "pipe:1")
	if withAudio {
		audioIn := "0:a:0"
		if c.Camera.TestSource {
			audioIn = "1:a:0"
		}
		a = append(a, "-map", audioIn,
			// The camera's audio timestamps jitter; resample to a steady stream.
			"-af", "aresample=async=1000",
			"-f", "s16le", "-ar", strconv.Itoa(sampleRate), "-ac", "2",
			fmt.Sprintf("tcp://127.0.0.1:%d", audioPort))
	}
	return a
}

// OutputArgs: ffmpeg #2 takes the finished picture (raw I420 on stdin) and PCM audio (from the app's
// TCP port), encodes and sends it. Timestamps come from the constant frame/sample rate, so picture
// and sound stay in sync. Closing stdin ends it cleanly. Progress goes to stdout.
func OutputArgs(c config.Config, enc string, audioPort int) []string {
	o := c.Output
	itoa := strconv.Itoa
	a := []string{"-hide_banner", "-loglevel", "warning", "-nostats", "-progress", "pipe:1", "-stats_period", "1",
		// Both inputs are fully described, so skip ffmpeg's format probing (it would wait seconds
		// for real-time data before starting).
		"-probesize", "32", "-analyzeduration", "0",
		"-f", "rawvideo", "-pix_fmt", "yuv420p", "-s", fmt.Sprintf("%dx%d", o.Width, o.Height),
		"-framerate", itoa(o.FPS), "-thread_queue_size", "32", "-i", "pipe:0",
		"-probesize", "32", "-analyzeduration", "0",
		"-f", "s16le", "-ar", itoa(sampleRate), "-ch_layout", "stereo", "-thread_queue_size", "256",
		"-i", fmt.Sprintf("tcp://127.0.0.1:%d", audioPort),
		"-map", "0:v", "-map", "1:a",
		"-c:v", enc}
	a = append(a, encoderArgs(enc, o.X264Preset)...)
	gop := itoa(o.FPS * 2) // keyframe every 2 s, as YouTube recommends
	a = append(a,
		"-pix_fmt", pixFmtFor(enc),
		"-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv",
		"-b:v", itoa(o.VideoBitrate)+"k", "-maxrate", itoa(o.VideoBitrate)+"k", "-bufsize", itoa(o.VideoBitrate*2)+"k",
		"-g", gop, "-keyint_min", gop,
		// No -shortest: it buffers up to 10 s (shortest_buf_duration). Stopping closes both inputs.
		"-c:a", "aac", "-b:a", itoa(c.Audio.Bitrate)+"k")
	switch o.Target {
	case "youtube":
		a = append(a, "-f", "flv", "-flvflags", "no_duration_filesize", c.YouTube.Server+"/"+c.YouTube.Key)
	case "soubor":
		a = append(a, "-y", "-f", "matroska", o.FilePath)
	default:
		a = append(a, "-f", "null", "-")
	}
	return a
}
