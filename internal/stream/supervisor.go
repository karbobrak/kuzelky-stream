package stream

import (
	"context"
	"log"
	"sync"
	"time"
)

type State string

const (
	Stopped    State = "zastaveno"
	Starting   State = "spoustim"
	Running    State = "bezi"
	Recovering State = "obnovuji"
)

// ProcStats describes one supervised process for the UI.
type ProcStats struct {
	State     State
	Since     time.Time // start of the current run
	Restarts  int
	LastError string
}

// Supervisor runs one ffmpeg role over and over: after a failure it waits (Delay) and starts again.
// Restart() ends the current run on request; the next one starts after a short pause.
type Supervisor struct {
	Name string
	Log  *log.Logger
	// RunOnce runs the process until it ends or ctx is cancelled. It should call SetRunning
	// once the process really works (first frame / first progress report).
	RunOnce func(ctx context.Context, sv *Supervisor) error
	// Delay before the next attempt; failures counts consecutive failed runs (1, 2, …).
	Delay func(failures int) time.Duration

	mu     sync.Mutex
	stats  ProcStats
	cancel context.CancelFunc
	manual bool
}

func (s *Supervisor) Stats() ProcStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *Supervisor) SetRunning() {
	s.mu.Lock()
	s.stats.State = Running
	s.mu.Unlock()
}

// Restart ends the current run; Loop then starts a new one.
func (s *Supervisor) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.manual = true
		s.cancel()
	}
}

// Loop runs until ctx is cancelled.
func (s *Supervisor) Loop(ctx context.Context) {
	failures := 0
	for {
		runCtx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		s.cancel = cancel
		s.manual = false
		s.stats.State, s.stats.Since = Starting, time.Now()
		s.mu.Unlock()

		err := s.RunOnce(runCtx, s)
		cancel()

		s.mu.Lock()
		manual, wasRunning := s.manual, s.stats.State == Running
		s.cancel = nil
		s.mu.Unlock()
		if ctx.Err() != nil {
			s.mu.Lock()
			s.stats.State = Stopped
			s.mu.Unlock()
			return
		}

		var wait time.Duration
		msg := "proces skončil"
		if err != nil {
			msg = err.Error()
		}
		switch {
		case manual:
			// Short pause even here: Reolink rejects an immediate reconnect ("bad cseq").
			failures, wait, msg = 0, time.Second, "ruční restart"
		default:
			if wasRunning {
				failures = 0
			}
			failures++
			wait = s.Delay(failures)
		}
		s.Log.Printf("%s: %s; nový pokus za %s", s.Name, msg, wait)
		s.mu.Lock()
		s.stats.State, s.stats.LastError = Recovering, msg
		s.stats.Restarts++
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.stats.State = Stopped
			s.mu.Unlock()
			return
		case <-time.After(wait):
		}
	}
}
