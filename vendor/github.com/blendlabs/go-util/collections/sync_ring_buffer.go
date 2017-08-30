package collections

import (
	"sync"
)

// NewSyncRingBuffer returns a new synchronized ring buffer.
func NewSyncRingBuffer() *SyncRingBuffer {
	return &SyncRingBuffer{
		innerBuffer: NewRingBuffer(),
		syncRoot:    &sync.Mutex{},
	}
}

// NewSyncRingBufferWithCapacity returns a new synchronized ring buffer.
func NewSyncRingBufferWithCapacity(capacity int) *SyncRingBuffer {
	return &SyncRingBuffer{
		innerBuffer: NewRingBufferWithCapacity(capacity),
		syncRoot:    &sync.Mutex{},
	}
}

// SyncRingBuffer is a ring buffer wrapper that adds synchronization.
type SyncRingBuffer struct {
	innerBuffer *RingBuffer
	syncRoot    *sync.Mutex
}

// SyncRoot returns the mutex used to synchronize the collection.
func (srb *SyncRingBuffer) SyncRoot() *sync.Mutex {
	return srb.syncRoot
}

// RingBuffer returns the inner ringbuffer.
func (srb *SyncRingBuffer) RingBuffer() *RingBuffer {
	return srb.innerBuffer
}

// Len returns the length of the ring buffer (as it is currently populated).
// Actual memory footprint may be different.
func (srb SyncRingBuffer) Len() (val int) {
	srb.syncRoot.Lock()
	val = srb.innerBuffer.Len()
	srb.syncRoot.Unlock()
	return
}

// Capacity returns the total size of the ring bufffer, including empty elements.
func (srb *SyncRingBuffer) Capacity() (val int) {
	srb.syncRoot.Lock()
	val = srb.innerBuffer.Capacity()
	srb.syncRoot.Unlock()
	return
}

// Enqueue adds an element to the "back" of the RingBuffer.
func (srb *SyncRingBuffer) Enqueue(value interface{}) {
	srb.syncRoot.Lock()
	srb.innerBuffer.Enqueue(value)
	srb.syncRoot.Unlock()
}

// Dequeue removes the first (oldest) element from the RingBuffer.
func (srb *SyncRingBuffer) Dequeue() (val interface{}) {
	srb.syncRoot.Lock()
	val = srb.innerBuffer.Dequeue()
	srb.syncRoot.Unlock()
	return
}

// Peek returns but does not remove the first element.
func (srb *SyncRingBuffer) Peek() (val interface{}) {
	srb.syncRoot.Lock()
	val = srb.innerBuffer.Peek()
	srb.syncRoot.Unlock()
	return
}

// PeekBack returns but does not remove the last element.
func (srb *SyncRingBuffer) PeekBack() (val interface{}) {
	srb.syncRoot.Lock()
	val = srb.innerBuffer.PeekBack()
	srb.syncRoot.Unlock()
	return
}

// TrimExcess resizes the buffer to better fit the contents.
func (srb *SyncRingBuffer) TrimExcess() {
	srb.syncRoot.Lock()
	srb.innerBuffer.trimExcess()
	srb.syncRoot.Unlock()
}

// Contents returns the ring buffer, in order, as a slice.
func (srb *SyncRingBuffer) Contents() (val []interface{}) {
	srb.syncRoot.Lock()
	val = srb.innerBuffer.Contents()
	srb.syncRoot.Unlock()
	return
}

// Clear removes all objects from the RingBuffer.
func (srb *SyncRingBuffer) Clear() {
	srb.syncRoot.Lock()
	srb.innerBuffer.Clear()
	srb.syncRoot.Unlock()
}

// Drain returns the ring buffer, in order, as a slice and empties it.
func (srb *SyncRingBuffer) Drain() (val []interface{}) {
	srb.syncRoot.Lock()
	val = srb.innerBuffer.Drain()
	srb.syncRoot.Unlock()
	return
}

// Each calls the consumer for each element in the buffer.
func (srb *SyncRingBuffer) Each(consumer func(value interface{})) {
	srb.syncRoot.Lock()
	srb.innerBuffer.Each(consumer)
	srb.syncRoot.Unlock()
}

// Consume calls the consumer for each element in the buffer, while also dequeueing that entry.
func (srb *SyncRingBuffer) Consume(consumer func(value interface{})) {
	srb.syncRoot.Lock()
	srb.innerBuffer.Consume(consumer)
	srb.syncRoot.Unlock()
}

// EachUntil calls the consumer for each element in the buffer with a stopping condition in head=>tail order.
func (srb *SyncRingBuffer) EachUntil(consumer func(value interface{}) bool) {
	srb.syncRoot.Lock()
	srb.innerBuffer.EachUntil(consumer)
	srb.syncRoot.Unlock()
}

// ReverseEachUntil calls the consumer for each element in the buffer with a stopping condition in tail=>head order.
func (srb *SyncRingBuffer) ReverseEachUntil(consumer func(value interface{}) bool) {
	srb.syncRoot.Lock()
	srb.innerBuffer.ReverseEachUntil(consumer)
	srb.syncRoot.Unlock()
}
