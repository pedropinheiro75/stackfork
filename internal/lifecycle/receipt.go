package lifecycle

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"sync"
)

// receiptScript runs a task's commands (one, or a migrate and then the seed)
// in one shell, each through its own eval, and prints the receipt only once
// the last returned 0. One shell keeps export, cd, set -e and the one-off
// container's files across them; separate evals keep a comment or an open
// heredoc in the first from swallowing the second; an exit inside them, or an
// entrypoint that never runs the command it is given, leaves no receipt. The
// receipt is printed as its two halves joined, so an entrypoint that only
// echoes its arguments does not print it. The fake docker of the cli tests
// recognizes the "__sf_a=" prefix.
const receiptScript = `__sf_a=$1; __sf_b=$2; __sf_1=$3; __sf_2=${4-}; __sf_n=$#; set --; ` +
	`eval "$__sf_1"; __sf_s=$?; [ "$__sf_s" -eq 0 ] || exit "$__sf_s"; ` +
	`if [ "$__sf_n" -gt 3 ]; then eval "$__sf_2"; __sf_s=$?; [ "$__sf_s" -eq 0 ] || exit "$__sf_s"; fi; ` +
	`printf '\n%s%s\n' "$__sf_a" "$__sf_b"`

// taskReceipt watches a task's raw stdout (before redaction, see Execute) for
// a line that is exactly this run's receipt: the evidence that its commands
// ran to their end.
// ponytail: it proves completion, not intent; a recipe written to print the
// receipt on purpose can still fake it, which no exit status could prevent.
type taskReceipt struct {
	a, b, want []byte
	mu         sync.Mutex // stdout and stderr reach one step log
	line       []byte
	long       bool
	seen       bool
}

func newTaskReceipt() *taskReceipt {
	half := func() []byte {
		b := make([]byte, 16)
		rand.Read(b) //nolint:errcheck // crypto/rand.Read never fails
		return []byte(hex.EncodeToString(b))
	}
	a, b := half(), half()
	return &taskReceipt{a: a, b: b, want: append(append([]byte(nil), a...), b...)}
}

// command is the argument vector that runs cmds (one or two) with the receipt.
func (t *taskReceipt) command(cmds ...string) []string {
	return append([]string{"sh", "-c", receiptScript, "sh", string(t.a), string(t.b)}, cmds...)
}

// Seen reports whether the receipt line came by.
func (t *taskReceipt) Seen() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.seen
}

// wrap gives the command's stdout (watched) and stderr (passed through); both
// write under one lock, as one shared writer did before.
func (t *taskReceipt) wrap(stdout, stderr io.Writer) (io.Writer, io.Writer) {
	return receiptWriter{t, stdout, true}, receiptWriter{t, stderr, false}
}

type receiptWriter struct {
	t     *taskReceipt
	w     io.Writer
	watch bool
}

func (r receiptWriter) Write(p []byte) (int, error) {
	r.t.mu.Lock()
	defer r.t.mu.Unlock()
	if r.watch {
		r.t.scan(p)
	}
	return r.w.Write(p)
}

func (t *taskReceipt) scan(p []byte) {
	for _, c := range p {
		if c == '\n' {
			if !t.long && bytes.Equal(bytes.TrimSuffix(t.line, []byte{'\r'}), t.want) {
				t.seen = true
			}
			t.line, t.long = t.line[:0], false
			continue
		}
		if t.long || len(t.line) > len(t.want) {
			t.long = true
			continue
		}
		t.line = append(t.line, c)
	}
}

// noReceiptNote is what a task that exited 0 without its receipt prints.
const noReceiptNote = "note: %s exited 0, but its command did not report that it ran to the end (does the service's entrypoint run the command it is given?); this run is no execution evidence for it\n"
