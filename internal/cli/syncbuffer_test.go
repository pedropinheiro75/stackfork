package cli

import (
	"bytes"
	"sync"
)

// syncBuffer is a mutex-protected bytes.Buffer, for tests that read a
// command's output from one goroutine while it's still being written from
// another (cmdDashboard running until cancelled/signalled) - a plain
// bytes.Buffer or strings.Builder read/written across goroutines without
// synchronization is a data race even when, in practice, the writer only
// ever writes one line before the reader starts polling.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
