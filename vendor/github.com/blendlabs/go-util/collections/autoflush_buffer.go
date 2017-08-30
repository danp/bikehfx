package collections

import (
	"sync"
	"time"
)

// Any is a loose type alias to interface{}
// We should just make this a builtin.
type Any = interface{}

// NewAutoflushBuffer creates a new autoflush buffer.
func NewAutoflushBuffer(maxLen int, interval time.Duration) *AutoflushBuffer {
	return &AutoflushBuffer{
		maxLen:       maxLen,
		interval:     interval,
		flushOnAbort: true,
		contents:     NewRingBufferWithCapacity(maxLen),
		ticker:       time.Tick(interval),
		abort:        make(chan bool),
		aborted:      make(chan bool),
	}
}

// AutoflushBuffer is a backing store that operates either on a fixed length flush or a fixed interval flush.
// A handler should be provided but without one the buffer will just clear.
// Adds that would cause fixed length flushes do not block on the flush handler.
type AutoflushBuffer struct {
	maxLen   int
	interval time.Duration

	contents     *RingBuffer
	contentsLock sync.Mutex

	ticker <-chan time.Time

	flushOnAbort bool
	handler      func(obj []Any)

	runningLock sync.Mutex
	running     bool
	abort       chan bool
	aborted     chan bool
}

// WithFlushOnAbort sets if we should flush on aborts or not.
// This defaults to true.
func (ab *AutoflushBuffer) WithFlushOnAbort(should bool) *AutoflushBuffer {
	ab.flushOnAbort = should
	return ab
}

// ShouldFlushOnAbort returns if the buffer will do one final flush on abort.
func (ab *AutoflushBuffer) ShouldFlushOnAbort() bool {
	return ab.flushOnAbort
}

// Interval returns the flush interval.
func (ab *AutoflushBuffer) Interval() time.Duration {
	return ab.interval
}

// MaxLen returns the maximum buffer length before a flush is triggered.
func (ab *AutoflushBuffer) MaxLen() int {
	return ab.maxLen
}

// WithFlushHandler sets the buffer flush handler and returns a reference to the buffer.
func (ab *AutoflushBuffer) WithFlushHandler(handler func(objs []Any)) *AutoflushBuffer {
	ab.handler = handler
	return ab
}

// Start starts the buffer flusher.
func (ab *AutoflushBuffer) Start() {
	ab.runningLock.Lock()
	defer ab.runningLock.Unlock()

	if ab.running {
		return
	}
	go ab.runLoop()
}

// Stop stops the buffer flusher.
func (ab *AutoflushBuffer) Stop() {
	ab.runningLock.Lock()
	defer ab.runningLock.Unlock()

	if !ab.running {
		return
	}
	ab.abort <- true
	<-ab.aborted
}

// Add adds a new object to the buffer, blocking if it triggers a flush.
// If the buffer is full, it will call the flush handler on a separate goroutine.
func (ab *AutoflushBuffer) Add(obj Any) {
	ab.contentsLock.Lock()
	defer ab.contentsLock.Unlock()

	ab.contents.Enqueue(obj)
	if ab.contents.Len() >= ab.maxLen {
		ab.flushUnsafeAsync()
	}
}

// AddMany adds many objects to the buffer at once.
func (ab *AutoflushBuffer) AddMany(objs ...Any) {
	ab.contentsLock.Lock()
	defer ab.contentsLock.Unlock()

	for _, obj := range objs {
		ab.contents.Enqueue(obj)
		if ab.contents.Len() >= ab.maxLen {
			ab.flushUnsafeAsync()
		}
	}
}

// Flush clears the buffer, if a handler is provided it is passed the contents of the buffer.
// This call is synchronous, in that it will call the flush handler on the same goroutine.
func (ab *AutoflushBuffer) Flush() {
	ab.contentsLock.Lock()
	defer ab.contentsLock.Unlock()
	ab.flushUnsafe()
}

// FlushAsync clears the buffer, if a handler is provided it is passed the contents of the buffer.
// This call is asynchronous, in that it will call the flush handler on its own goroutine.
func (ab *AutoflushBuffer) FlushAsync() {
	ab.contentsLock.Lock()
	defer ab.contentsLock.Unlock()
	ab.flushUnsafeAsync()
}

// flushUnsafeAsync flushes the buffer without acquiring any locks.
func (ab *AutoflushBuffer) flushUnsafeAsync() {
	if ab.handler != nil {
		if ab.contents.Len() > 0 {
			contents := ab.contents.Drain()
			go ab.handler(contents)
		}
	} else {
		ab.contents.Clear()
	}
}

// flushUnsafeAsync flushes the buffer without acquiring any locks.
func (ab *AutoflushBuffer) flushUnsafe() {
	if ab.handler != nil {
		if ab.contents.Len() > 0 {
			ab.handler(ab.contents.Drain())
		}
	} else {
		ab.contents.Clear()
	}
}

func (ab *AutoflushBuffer) runLoop() {
	ab.runningLock.Lock()
	ab.running = true
	ab.runningLock.Unlock()

	for {
		select {
		case <-ab.ticker:
			ab.FlushAsync()
		case <-ab.abort:
			if ab.flushOnAbort {
				ab.Flush()
			}
			ab.aborted <- true
			return
		}
	}
}
