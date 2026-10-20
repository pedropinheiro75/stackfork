package runs

import (
	"fmt"
	"io"
	"os"

	"github.com/pedropinheiro75/stackfork/internal/redact"
)

// Step log cap (design D5): the first headKeep bytes, a marker, the last tailKeep.
// ponytail: constants; make them configuration only if a real project needs more.
const (
	headKeep = 512 << 10
	tailKeep = 512 << 10
)

// stepWriter is the redactor in front of the caller's output and the log.
type stepWriter struct {
	red *redact.Writer
	log *cappedLog
}

func newStepWriter(out io.Writer, logf *os.File, secrets []redact.Secret) *stepWriter {
	w := &stepWriter{log: &cappedLog{f: logf}}
	dst := io.Writer(w.log)
	if out != nil {
		dst = io.MultiWriter(out, w.log)
	}
	w.red = redact.NewWriter(dst, secrets)
	return w
}

func (w *stepWriter) Write(p []byte) (int, error) { return w.red.Write(p) }

// Close flushes the redactor, then the log with its marker and tail.
func (w *stepWriter) Close() error {
	w.red.Close()
	return w.log.Close()
}

// cappedLog keeps the head on disk as it arrives and the tail in memory.
// It never fails a write: a dead log must not stop the command it records.
type cappedLog struct {
	f        *os.File
	head     int
	lastByte byte
	tail     []byte
	dropped  int
}

func (c *cappedLog) Write(p []byte) (int, error) {
	n := len(p)
	if c.f == nil {
		return n, nil
	}
	if c.head < headKeep {
		k := min(len(p), headKeep-c.head)
		c.f.Write(p[:k])
		c.head += k
		c.lastByte = p[k-1]
		p = p[k:]
	}
	if len(p) > 0 {
		c.tail = append(c.tail, p...)
		if over := len(c.tail) - tailKeep; over > 0 {
			c.tail = c.tail[over:]
			c.dropped += over
		}
	}
	return n, nil
}

func (c *cappedLog) Close() error {
	if c.f == nil {
		return nil
	}
	if c.dropped > 0 {
		if c.lastByte != '\n' {
			c.f.WriteString("\n")
		}
		fmt.Fprintf(c.f, "[... %d bytes not kept ...]\n", c.dropped)
	}
	c.f.Write(c.tail)
	err := c.f.Close()
	c.f = nil
	return err
}
