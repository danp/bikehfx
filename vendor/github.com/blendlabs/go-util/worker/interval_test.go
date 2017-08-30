package worker

import (
	"sync"
	"testing"
	"time"

	assert "github.com/blendlabs/go-assert"
)

func TestWorker(t *testing.T) {
	assert := assert.New(t)

	var didWork bool
	wg := sync.WaitGroup{}
	wg.Add(1)
	w := New(func() error {
		didWork = true
		wg.Done()
		return nil
	}, time.Millisecond)

	w.Start()
	assert.True(w.Latch().IsStarted())
	wg.Wait()
	w.Stop()

	assert.True(didWork)
}
