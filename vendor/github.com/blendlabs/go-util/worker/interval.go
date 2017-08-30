package worker

import "time"

// New returns a new worker that runs an action on an interval.
func New(action func() error, interval time.Duration) *Interval {
	return &Interval{
		interval: interval,
		action:   action,
		latch:    NewLatch(),
	}
}

// Interval is a managed goroutine that does things.
type Interval struct {
	interval time.Duration
	action   func() error
	latch    *Latch
	errors   chan error
}

// Latch returns the worker latch.
func (i *Interval) Latch() *Latch {
	return i.latch
}

// WithAction sets the interval action.
func (i *Interval) WithAction(action func() error) *Interval {
	i.action = action
	return i
}

// Action returns the interval action.
func (i *Interval) Action() func() error {
	return i.action
}

// WithErrors returns the error channel.
func (i *Interval) WithErrors(errors chan error) *Interval {
	i.errors = errors
	return i
}

// Errors returns a channel to read action errors from.
func (i *Interval) Errors() chan error {
	return i.errors
}

// Start starts the worker.
func (i *Interval) Start() {
	i.latch.Started()
	go func() {
		tick := time.Tick(i.interval)
		var err error
		for {
			select {
			case <-tick:
				err = i.action()
				if err != nil && i.errors != nil {
					i.errors <- err
				}
			case <-i.latch.StopSignal():
				i.latch.Stopped()
				return
			}
		}
	}()
}

// Stop stops the worker.
func (i *Interval) Stop() {
	i.latch.Stop()
}
