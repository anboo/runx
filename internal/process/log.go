package process

import (
	"sync"
)

type RingBuffer struct {
	buf   []LogEntry
	size  int
	start int
	len   int
	mu    sync.RWMutex
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buf:  make([]LogEntry, size),
		size: size,
	}
}

func (r *RingBuffer) Append(entry LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := (r.start + r.len) % r.size
	r.buf[idx] = entry
	if r.len < r.size {
		r.len++
	} else {
		r.start = (r.start + 1) % r.size
	}
}

func (r *RingBuffer) Lines(n int) []LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n <= 0 || n > r.len {
		n = r.len
	}
	start := r.len - n
	if start < 0 {
		start = 0
	}
	res := make([]LogEntry, n)
	for i := 0; i < n; i++ {
		idx := (r.start + start + i) % r.size
		res[i] = r.buf[idx]
	}
	return res
}

func (r *RingBuffer) All() []LogEntry {
	return r.Lines(r.len)
}

func (r *RingBuffer) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.len
}
