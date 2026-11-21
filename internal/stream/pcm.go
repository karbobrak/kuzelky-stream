package stream

import "sync"

// Audio format between all processes: signed 16-bit little-endian, 48 kHz, stereo.
const (
	sampleRate     = 48000
	bytesPerSample = 4 // 2 channels × 2 bytes
)

// pcmRing is a small jitter buffer for the camera audio. The camera writes at its own pace, the
// output clock reads fixed chunks. Reading starts only when ~100 ms are buffered (no crackling on
// start), and the buffer is kept short so the audio does not drift behind the picture.
type pcmRing struct {
	mu      sync.Mutex
	buf     []byte
	primed  bool
	prime   int // bytes needed before reading starts
	maxKeep int // bytes kept at most; older audio is dropped
}

func newPCMRing() *pcmRing {
	return &pcmRing{prime: sampleRate * bytesPerSample / 10, maxKeep: sampleRate * bytesPerSample * 3 / 10}
}

func (r *pcmRing) Write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if over := len(r.buf) - r.maxKeep; over > 0 {
		over -= over % bytesPerSample
		r.buf = append(r.buf[:0], r.buf[over:]...)
	}
}

// Read fills out with buffered audio, or silence when there is not enough.
func (r *pcmRing) Read(out []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.primed && len(r.buf) >= r.prime {
		r.primed = true
	}
	if !r.primed || len(r.buf) < len(out) {
		r.primed = false
		clear(out)
		return
	}
	copy(out, r.buf)
	r.buf = append(r.buf[:0], r.buf[len(out):]...)
}

// Reset drops everything (camera reconnected; old audio is stale).
func (r *pcmRing) Reset() {
	r.mu.Lock()
	r.buf, r.primed = r.buf[:0], false
	r.mu.Unlock()
}
