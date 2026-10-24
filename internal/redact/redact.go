// Package redact replaces resolved secret values in log output with a
// [redacted:<name>] marker (task 4.1a, design.md D5), for `stackfork logs`
// and the dashboard's log view alike. Values shorter than 8 characters are
// never matched, to avoid mangling ordinary short log text - a documented
// limit, not a bug.
package redact

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// minLength is design.md D5's "values shorter than 8 characters are not
// matched" limit.
const minLength = 8

// Secret is one resolved value to redact, named for the marker.
type Secret struct {
	Name  string
	Value string
}

// Writer wraps dst, redacting every occurrence of a secret's value (and its
// base64 form) as the bytes flow through, including a value split across
// two Write calls. Callers must call Close when done to flush any buffered
// tail.
type Writer struct {
	dst     io.Writer
	needles [][]byte
	names   []string
	maxLen  int
	buf     []byte
}

// NewWriter builds a Writer for dst. Secrets shorter than 8 characters are
// skipped entirely (neither the raw value nor its base64 form is matched).
func NewWriter(dst io.Writer, secrets []Secret) *Writer {
	w := &Writer{dst: dst}
	for _, s := range secrets {
		if len(s.Value) < minLength {
			continue
		}
		w.addNeedle(s.Value, s.Name)
		w.addNeedle(base64.StdEncoding.EncodeToString([]byte(s.Value)), s.Name)
	}
	return w
}

func (w *Writer) addNeedle(value, name string) {
	b := []byte(value)
	w.needles = append(w.needles, b)
	w.names = append(w.names, name)
	if len(b) > w.maxLen {
		w.maxLen = len(b)
	}
}

// Write redacts p (plus anything held back from a previous call) and
// forwards it to dst. It always reports len(p) written on success, per
// io.Writer's contract, even though the byte count that actually reached
// dst differs once a match is replaced.
func (w *Writer) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	out := w.redact(false)
	if len(out) > 0 {
		if _, err := w.dst.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Close flushes any buffered tail (a final pass: no more data can arrive to
// complete a split match, so whatever's left is safe to emit as-is).
func (w *Writer) Close() error {
	out := w.redact(true)
	if len(out) == 0 {
		return nil
	}
	_, err := w.dst.Write(out)
	return err
}

// redact scans w.buf for every needle, replacing matches, and returns the
// safe-to-emit prefix. Unless final, it withholds the last maxLen-1 bytes
// (a possible partial match straddling the next Write) in w.buf.
func (w *Writer) redact(final bool) []byte {
	if len(w.needles) == 0 {
		out := w.buf
		w.buf = nil
		return out
	}
	var out []byte
	buf := w.buf
	for {
		idx, needleIdx := w.firstMatch(buf)
		if idx < 0 {
			break
		}
		out = append(out, buf[:idx]...)
		out = append(out, []byte(fmt.Sprintf("[redacted:%s]", w.names[needleIdx]))...)
		buf = buf[idx+len(w.needles[needleIdx]):]
	}
	if final {
		out = append(out, buf...)
		w.buf = nil
		return out
	}
	holdback := w.maxLen - 1
	if holdback < 0 || holdback > len(buf) {
		holdback = len(buf)
	}
	if len(buf) > holdback {
		out = append(out, buf[:len(buf)-holdback]...)
	}
	w.buf = append([]byte(nil), buf[len(buf)-holdback:]...)
	return out
}

// firstMatch returns the earliest occurrence, across every needle, in buf.
func (w *Writer) firstMatch(buf []byte) (idx, needleIdx int) {
	idx = -1
	for i, n := range w.needles {
		if j := bytes.Index(buf, n); j >= 0 && (idx < 0 || j < idx) {
			idx, needleIdx = j, i
		}
	}
	return
}

// SecretLikeName reports whether an environment variable name looks like it
// holds a secret (add-env-overrides D5): case-insensitive TOKEN, SECRET or
// PASSWORD, or a name ending in _TOKEN, _SECRET, _PASSWORD, _PASSWD, _KEY or
// _CREDENTIALS. The CLI, the dashboard and the output masking share it.
func SecretLikeName(name string) bool {
	n := strings.ToUpper(name)
	switch n {
	case "TOKEN", "SECRET", "PASSWORD":
		return true
	}
	for _, suf := range []string{"_TOKEN", "_SECRET", "_PASSWORD", "_PASSWD", "_KEY", "_CREDENTIALS"} {
		if strings.HasSuffix(n, suf) {
			return true
		}
	}
	return false
}
