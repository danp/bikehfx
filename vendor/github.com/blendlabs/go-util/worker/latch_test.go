package worker

import (
	"testing"

	assert "github.com/blendlabs/go-assert"
)

func TestLatch(t *testing.T) {
	assert := assert.New(t)

	l := NewLatch()

	var didStart bool
	var didAbort bool
	var didGetWork bool

	work := make(chan bool)
	workComplete := make(chan bool)
	go func() {
		l.Started()
		didStart = true
		for {
			select {
			case <-work:
				didGetWork = true
				workComplete <- true
			case <-l.StopSignal():
				didAbort = true
				l.Stopped()
				return
			}
		}
	}()

	work <- true
	assert.True(l.IsStarted())
	<-workComplete

	// signal stop
	l.Stop()

	assert.True(didStart)
	assert.True(didAbort)
	assert.True(didGetWork)
	assert.False(l.IsStopping())
	assert.False(l.IsStarted())
}
