package worker

import "sync"

// NewLatch creates a new latch.
func NewLatch() *Latch {
	return &Latch{
		started:  false,
		stopping: false,
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

// Latch is a helper to coordinate killing goroutines.
type Latch struct {
	syncroot sync.Mutex
	started  bool
	stop     chan struct{}
	stopping bool
	stopped  chan struct{}
}

// IsStarted indicates we can signal to stop.
func (l *Latch) IsStarted() bool {
	l.syncroot.Lock()
	defer l.syncroot.Unlock()
	return l.started
}

// IsStopping returns if the latch is waiting to finish stopping.
func (l *Latch) IsStopping() bool {
	l.syncroot.Lock()
	defer l.syncroot.Unlock()
	return l.stopping
}

// StopSignal returns the abort signal / channel.
func (l *Latch) StopSignal() <-chan struct{} {
	return l.stop
}

// Stopped signals the latch has aborted.
func (l *Latch) Stopped() {
	close(l.stopped)
}

// Started marks the process as started.
func (l *Latch) Started() {
	l.syncroot.Lock()
	defer l.syncroot.Unlock()

	if l.started || l.stopping {
		return
	}

	l.started = true
	l.stopping = false
	l.stop = make(chan struct{})
	l.stopped = make(chan struct{})
}

// Stop signals the tomb to stop.
func (l *Latch) Stop() {
	l.syncroot.Lock()
	defer l.syncroot.Unlock()

	if !l.started || l.stopping {
		return
	}

	l.stopping = true
	close(l.stop)
	<-l.stopped
	l.stopping = false
	l.started = false
}
