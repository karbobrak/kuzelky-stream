package stream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kuzelkystream/internal/config"
	"kuzelkystream/internal/frame"
)

// Options configure an Engine.
type Options struct {
	Cfg     config.Config
	FFmpeg  string
	Encoder string // resolved, never "auto"
	Log     *log.Logger
	// Overlay returns the current graphics (output size, premultiplied RGBA) and a version number
	// that changes whenever the picture changes. Called once per output frame; must be cheap.
	Overlay func() (*image.RGBA, uint64)
	// Slate is the full-screen picture shown while the camera is unavailable (opaque, output size).
	Slate *image.RGBA
}

// Stats is a snapshot for the UI / console.
type Stats struct {
	Camera       ProcStats
	CameraFrames int64     // frames received from the camera so far (for an fps estimate)
	CameraLast   time.Time // when the last camera frame arrived
	ShowingSlate bool      // viewers currently see the replacement picture

	Output      ProcStats
	OutFPS      float64
	BitrateKbps float64
	Speed       float64
	Dropped     int64 // output frames dropped because ffmpeg #2 was not keeping up / not running
}

// Engine keeps the YouTube output running at a constant frame rate no matter what the camera does.
//
//	ffmpeg #1 (ingest)  camera → raw frames + PCM → Engine          restarts freely
//	Engine clock        latest camera frame or slate + graphics      25 fps, never stops
//	ffmpeg #2 (output)  raw frames + PCM → encoder → YouTube         keeps its connection
type Engine struct {
	o      Options
	ingest *Supervisor
	output *Supervisor

	camMu     sync.Mutex
	camFrame  *frame.Frame
	camAt     time.Time
	camFrames int64
	camAudio  int32 // -1 unknown, 0 none, 1 has audio

	ring    *pcmRing
	inLn    *net.TCPListener // camera audio from ffmpeg #1
	outLn   *net.TCPListener // audio for ffmpeg #2
	videoCh chan *frame.Frame
	audioCh chan []byte
	pool    chan *frame.Frame

	cfgMu sync.Mutex
	cfg   config.Config

	previewMu sync.Mutex
	preview   *frame.Frame // copy of an output frame, refreshed once per second

	outMu      sync.Mutex
	outFPS     float64
	outBitrate float64
	outSpeed   float64
	dropped    atomic.Int64
	slateOn    atomic.Bool
}

func New(o Options) (*Engine, error) {
	inLn, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	outLn, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		inLn.Close()
		return nil, err
	}
	fps := o.Cfg.Output.FPS
	e := &Engine{
		o: o, cfg: o.Cfg, ring: newPCMRing(), inLn: inLn, outLn: outLn, camAudio: -1,
		videoCh: make(chan *frame.Frame, fps),
		audioCh: make(chan []byte, 2*fps),
		pool:    make(chan *frame.Frame, fps+8),
	}
	e.ingest = &Supervisor{Name: "kamera", Log: o.Log, RunOnce: e.ingestOnce,
		Delay: func(int) time.Duration { return 3 * time.Second }}
	outDelays := []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second, 10 * time.Second}
	e.output = &Supervisor{Name: "vysílání", Log: o.Log, RunOnce: e.outputOnce,
		Delay: func(n int) time.Duration { return outDelays[min(n, len(outDelays))-1] }}
	return e, nil
}

func (e *Engine) RestartCamera() { e.ingest.Restart() }
func (e *Engine) RestartOutput() { e.output.Restart() }

// Cfg returns the settings the engine currently uses.
func (e *Engine) Cfg() config.Config {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	return e.cfg
}

// SetConfig replaces the settings. Each process picks them up at its next (re)start, so the caller
// restarts whichever process is affected. Output size and fps must not change (they fix the frame
// format between the processes); changing them requires a new Engine.
func (e *Engine) SetConfig(c config.Config) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if c.Camera != e.cfg.Camera || c.Audio.Source != e.cfg.Audio.Source {
		atomic.StoreInt32(&e.camAudio, -1) // probe the camera's audio again
	}
	e.cfg = c
}

// Preview returns the picture viewers saw within the last second, or nil before the first frame.
func (e *Engine) Preview() image.Image {
	e.previewMu.Lock()
	defer e.previewMu.Unlock()
	if e.preview == nil {
		return nil
	}
	f := e.preview
	y, u, v := f.Planes()
	return &image.YCbCr{
		Y: append([]byte(nil), y...), Cb: append([]byte(nil), u...), Cr: append([]byte(nil), v...),
		YStride: f.W, CStride: f.W / 2, SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect: image.Rect(0, 0, f.W, f.H),
	}
}

func (e *Engine) Stats() Stats {
	e.camMu.Lock()
	s := Stats{CameraFrames: e.camFrames, CameraLast: e.camAt}
	e.camMu.Unlock()
	e.outMu.Lock()
	s.OutFPS, s.BitrateKbps, s.Speed = e.outFPS, e.outBitrate, e.outSpeed
	e.outMu.Unlock()
	s.Camera, s.Output = e.ingest.Stats(), e.output.Stats()
	s.ShowingSlate, s.Dropped = e.slateOn.Load(), e.dropped.Load()
	return s
}

// Run blocks until ctx is cancelled and both processes have ended.
func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); e.acceptCameraAudio() }()
	go func() { defer wg.Done(); e.ingest.Loop(ctx) }()
	go func() { defer wg.Done(); e.output.Loop(ctx) }()
	e.clock(ctx)
	e.inLn.Close() // ends acceptCameraAudio
	wg.Wait()
	e.outLn.Close()
}

// ---------------------------------------------------------------- clock / compositor

func (e *Engine) clock(ctx context.Context) {
	c := e.Cfg()
	w, h, fps := c.Output.Width, c.Output.Height, c.Output.FPS
	period := time.Second / time.Duration(fps)
	chunk := sampleRate / fps * bytesPerSample

	slate := frame.New(w, h)
	slate.Black()
	if e.o.Slate != nil {
		frame.Blend(slate, frame.Prepare(e.o.Slate))
	}
	var prep *frame.Prepared
	prepVer := ^uint64(0)
	tick := 0

	next := time.Now()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		next = next.Add(period)
		if d := time.Until(next); d < -time.Second {
			next = time.Now() // fell far behind (PC was busy); do not try to catch up
		} else if d > 0 {
			timer.Reset(d)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			return
		}

		f := e.getFree(w, h)
		e.camMu.Lock()
		fresh := e.camFrame != nil && time.Since(e.camAt) < time.Second
		if fresh {
			copy(f.Buf, e.camFrame.Buf)
		}
		e.camMu.Unlock()
		if !fresh {
			copy(f.Buf, slate.Buf)
		}
		e.slateOn.Store(!fresh)

		if img, ver := e.o.Overlay(); ver != prepVer {
			prep, prepVer = frame.Prepare(img), ver
		}
		frame.Blend(f, prep)

		if tick++; tick%fps == 0 {
			e.previewMu.Lock()
			if e.preview == nil {
				e.preview = frame.New(w, h)
			}
			copy(e.preview.Buf, f.Buf)
			e.previewMu.Unlock()
		}

		select {
		case e.videoCh <- f:
		default:
			e.putFree(f)
			e.dropped.Add(1)
		}
		a := make([]byte, chunk)
		if e.Cfg().Audio.Source == "kamera" && fresh {
			e.ring.Read(a)
		}
		select {
		case e.audioCh <- a:
		default:
		}
	}
}

func (e *Engine) getFree(w, h int) *frame.Frame {
	select {
	case f := <-e.pool:
		return f
	default:
		return frame.New(w, h)
	}
}

func (e *Engine) putFree(f *frame.Frame) {
	select {
	case e.pool <- f:
	default:
	}
}

// ---------------------------------------------------------------- ffmpeg #1: camera

func (e *Engine) acceptCameraAudio() {
	for {
		conn, err := e.inLn.Accept()
		if err != nil {
			return // listener closed
		}
		e.ring.Reset()
		go func() {
			defer conn.Close()
			buf := make([]byte, 16384)
			for {
				n, err := conn.Read(buf)
				if n > 0 {
					e.ring.Write(buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()
	}
}

func (e *Engine) cameraHasAudio(ctx context.Context) (bool, error) {
	c := e.Cfg()
	if c.Audio.Source != "kamera" {
		return false, nil
	}
	if c.Camera.TestSource {
		return true, nil
	}
	switch atomic.LoadInt32(&e.camAudio) {
	case 0:
		return false, nil
	case 1:
		return true, nil
	}
	pr, err := Probe(ctx, e.o.FFmpeg, c.Camera.CameraURL(), c.Camera.Transport)
	if err != nil {
		return false, errors.New(c.Mask(err.Error()))
	}
	e.o.Log.Printf("kamera: %s %dx%d @ %s fps, zvuk: %v %s",
		pr.VideoCodec, pr.Width, pr.Height, pr.FPS, pr.HasAudio, pr.AudioCodec)
	v := int32(0)
	if pr.HasAudio {
		v = 1
	}
	atomic.StoreInt32(&e.camAudio, v)
	return pr.HasAudio, nil
}

func (e *Engine) ingestOnce(ctx context.Context, sv *Supervisor) error {
	c := e.Cfg()
	withAudio, err := e.cameraHasAudio(ctx)
	if err != nil {
		return err
	}
	args := IngestArgs(c, withAudio, e.inLn.Addr().(*net.TCPAddr).Port)
	e.o.Log.Printf("kamera: spouštím ffmpeg %s", c.Mask(strings.Join(args, " ")))

	cmd := exec.CommandContext(ctx, e.o.FFmpeg, args...)
	hideWindow(cmd)
	cmd.WaitDelay = 3 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	lastErr := e.logStderr(cmd, "kamera")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg nelze spustit: %w", err)
	}

	// Watchdog: the camera can hang without closing the connection.
	var lastFrame atomic.Int64
	start := time.Now()
	go watchdog(ctx, func() bool {
		lf := lastFrame.Load()
		if lf == 0 {
			return time.Since(start) > 20*time.Second
		}
		return time.Since(time.Unix(0, lf)) > 5*time.Second
	}, func() { _ = cmd.Process.Kill() })

	w, h := c.Output.Width, c.Output.Height
	buf := frame.New(w, h)
	var readErr error
	for {
		if _, readErr = io.ReadFull(stdout, buf.Buf); readErr != nil {
			break
		}
		now := time.Now()
		e.camMu.Lock()
		old := e.camFrame
		e.camFrame, e.camAt = buf, now
		e.camFrames++
		e.camMu.Unlock()
		if old == nil {
			old = frame.New(w, h)
		}
		buf = old
		if lastFrame.Swap(now.UnixNano()) == 0 {
			sv.SetRunning()
		}
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if lastFrame.Load() != 0 && time.Since(time.Unix(0, lastFrame.Load())) > 5*time.Second {
		return errors.New("kamera přestala posílat obraz")
	}
	return procErr(waitErr, readErr, lastErr())
}

// ---------------------------------------------------------------- ffmpeg #2: output

func (e *Engine) outputOnce(ctx context.Context, sv *Supervisor) error {
	c := e.Cfg()
	// Drop stale frames/audio queued while the output was down.
	for drained := false; !drained; {
		select {
		case f := <-e.videoCh:
			e.putFree(f)
		case <-e.audioCh:
		default:
			drained = true
		}
	}

	args := OutputArgs(c, e.o.Encoder, e.outLn.Addr().(*net.TCPAddr).Port)
	e.o.Log.Printf("vysílání: spouštím ffmpeg %s", c.Mask(strings.Join(args, " ")))

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	cmd := exec.CommandContext(runCtx, e.o.FFmpeg, args...)
	hideWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var connMu sync.Mutex
	var audioConn net.Conn
	// Stopping: close the inputs so ffmpeg finishes the stream/file properly; kill only if it
	// is still running 5 s later.
	cmd.Cancel = func() error {
		connMu.Lock()
		if audioConn != nil {
			audioConn.Close()
		}
		connMu.Unlock()
		return stdin.Close()
	}
	cmd.WaitDelay = 5 * time.Second
	lastErr := e.logStderr(cmd, "vysílání")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg nelze spustit: %w", err)
	}

	var wg sync.WaitGroup
	// Video writer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-runCtx.Done():
				return
			case f := <-e.videoCh:
				_, err := stdin.Write(f.Buf)
				e.putFree(f)
				if err != nil {
					return
				}
			}
		}
	}()
	// Audio: ffmpeg connects to our port, then we feed it.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = e.outLn.SetDeadline(time.Now().Add(20 * time.Second))
		conn, err := e.outLn.Accept()
		if err != nil {
			return
		}
		connMu.Lock()
		audioConn = conn
		connMu.Unlock()
		if runCtx.Err() != nil {
			conn.Close()
			return
		}
		defer conn.Close()
		// Audio queued while ffmpeg was starting would put the sound ahead of the picture.
		for drained := false; !drained; {
			select {
			case <-e.audioCh:
			default:
				drained = true
			}
		}
		for {
			select {
			case <-runCtx.Done():
				return
			case a := <-e.audioCh:
				if _, err := conn.Write(a); err != nil {
					return
				}
			}
		}
	}()

	// Progress.
	var lastProgress atomic.Int64
	start := time.Now()
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		cur := map[string]string{}
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), "=")
			if !ok {
				continue
			}
			cur[k] = strings.TrimSpace(v)
			if k != "progress" {
				continue
			}
			if lastProgress.Swap(time.Now().UnixNano()) == 0 {
				sv.SetRunning()
			}
			fps, _ := strconv.ParseFloat(cur["fps"], 64)
			br, _ := strconv.ParseFloat(strings.TrimSuffix(cur["bitrate"], "kbits/s"), 64)
			sp, _ := strconv.ParseFloat(strings.TrimSuffix(cur["speed"], "x"), 64)
			e.outMu.Lock()
			e.outFPS, e.outBitrate, e.outSpeed = fps, br, sp
			e.outMu.Unlock()
		}
	}()

	stalled := atomic.Bool{}
	go watchdog(runCtx, func() bool {
		lp := lastProgress.Load()
		if lp == 0 {
			return time.Since(start) > 25*time.Second
		}
		return time.Since(time.Unix(0, lp)) > 10*time.Second
	}, func() { stalled.Store(true); cancelRun() })

	waitErr := cmd.Wait()
	cancelRun()
	wg.Wait()
	e.outMu.Lock()
	e.outFPS, e.outBitrate, e.outSpeed = 0, 0, 0
	e.outMu.Unlock()

	if stalled.Load() {
		return errors.New("ffmpeg přestal hlásit postup (výpadek internetu?)")
	}
	if ctx.Err() != nil {
		return nil
	}
	return procErr(waitErr, nil, lastErr())
}

// ---------------------------------------------------------------- helpers

// logStderr sends ffmpeg's stderr (masked) to the log and returns a getter for its last line.
// Must be called before cmd.Start.
func (e *Engine) logStderr(cmd *exec.Cmd, name string) func() string {
	var mu sync.Mutex
	last := ""
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return func() string { return err.Error() }
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			line := e.Cfg().Mask(sc.Text())
			e.o.Log.Printf("%s ffmpeg: %s", name, line)
			mu.Lock()
			last = line
			mu.Unlock()
		}
	}()
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

// watchdog calls kill once when stuck() turns true; it stops with ctx.
func watchdog(ctx context.Context, stuck func() bool, kill func()) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if stuck() {
				kill()
				return
			}
		}
	}
}

func procErr(waitErr, readErr error, lastLine string) error {
	switch {
	case lastLine != "":
		return errors.New(lastLine)
	case waitErr != nil:
		return waitErr
	case readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF):
		return readErr
	}
	return errors.New("ffmpeg skončil")
}
