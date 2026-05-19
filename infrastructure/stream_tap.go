package infrastructure

import (
	"io"
	"sync/atomic"
)

// CountingWriter wraps an io.Writer and tallies the number of bytes
// successfully written. Safe for concurrent use; the byte counter is updated
// atomically so the audit-log goroutine can read it after the writer has been
// detached.
type CountingWriter struct {
	W     io.Writer
	count int64
}

// Write forwards p to the underlying writer and atomically adds the number of
// bytes returned by the underlying writer to the count. Only bytes that the
// underlying writer accepted are counted, which matches what was actually
// emitted on the wire.
func (c *CountingWriter) Write(p []byte) (int, error) {
	n, err := c.W.Write(p)
	if n > 0 {
		atomic.AddInt64(&c.count, int64(n))
	}
	return n, err
}

// Count returns the total number of bytes written so far.
func (c *CountingWriter) Count() int64 {
	return atomic.LoadInt64(&c.count)
}
