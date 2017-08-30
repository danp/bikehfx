package collections

import (
	"fmt"
	"sync"
	"testing"
	"time"

	assert "github.com/blendlabs/go-assert"
)

func TestAutoflushBuffer(t *testing.T) {
	assert := assert.New(t)

	wg := sync.WaitGroup{}
	wg.Add(2)
	buffer := NewAutoflushBuffer(10, time.Hour).WithFlushHandler(func(objects []Any) {
		defer wg.Done()
		assert.Len(objects, 10)
	})

	buffer.Start()
	defer buffer.Stop()

	for x := 0; x < 20; x++ {
		buffer.Add(fmt.Sprintf("foo%d", x))
	}

	wg.Wait()
}

func TestAutoflushBufferTicker(t *testing.T) {
	assert := assert.New(t)
	assert.StartTimeout(500 * time.Millisecond)
	defer assert.EndTimeout()

	wg := sync.WaitGroup{}
	wg.Add(20)
	buffer := NewAutoflushBuffer(100, time.Millisecond).WithFlushHandler(func(objects []Any) {
		for range objects {
			wg.Done()
		}
	})

	buffer.Start()
	defer buffer.Stop()

	for x := 0; x < 20; x++ {
		buffer.Add(fmt.Sprintf("foo%d", x))
	}
	wg.Wait()
}

func BenchmarkAutoflushBuffer(b *testing.B) {
	buffer := NewAutoflushBuffer(128, 500*time.Millisecond).WithFlushHandler(func(objects []Any) {
		if len(objects) > 128 {
			b.Fail()
		}
	})

	buffer.Start()
	defer buffer.Stop()

	for x := 0; x < b.N; x++ {
		for y := 0; y < 1000; y++ {
			buffer.Add(fmt.Sprintf("asdf%d%d", x, y))
		}
	}
}
