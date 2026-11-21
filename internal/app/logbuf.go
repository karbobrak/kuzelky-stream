package app

import (
	"strings"
	"sync"
)

// LogLine is one log line with a sequence number, so the UI can ask only for new lines.
type LogLine struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

// LogBuf keeps the last lines written to it (it is an io.Writer for log.Logger).
type LogBuf struct {
	mu    sync.Mutex
	lines []LogLine
	next  int64
	max   int
}

func NewLogBuf(max int) *LogBuf { return &LogBuf{max: max, next: 1} }

func (b *LogBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, l := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		b.lines = append(b.lines, LogLine{Seq: b.next, Text: l})
		b.next++
	}
	if over := len(b.lines) - b.max; over > 0 {
		b.lines = append(b.lines[:0], b.lines[over:]...)
	}
	return len(p), nil
}

// Since returns lines with Seq > after (at most the last `limit` of them).
func (b *LogBuf) Since(after int64, limit int) []LogLine {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []LogLine
	for _, l := range b.lines {
		if l.Seq > after {
			out = append(out, l)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}
