package lifecycle

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
)

// receiptScript runs a recorded task command and, only when the shell got past
// it with status 0, prints the receipt. eval keeps the command's own shell
// semantics (one shell, so export, cd and set -e behave as in sh -c cmd); an
// exit inside it, or a service entrypoint that never runs the command it is
// given, leaves no receipt. The fake docker of the cli tests recognizes the
// "__sf_c=" prefix.
const receiptScript = `__sf_c=$1; __sf_r=$2; set --; eval "$__sf_c"; __sf_s=$?; [ "$__sf_s" -eq 0 ] || exit "$__sf_s"; printf '%s\n' "$__sf_r"`

// taskReceipt passes a task's output through to w and watches it for this
// run's receipt line: the evidence that the command itself ran to its end.
// ponytail: it proves completion, not intent; a recipe written to print the
// receipt on purpose can still fake it, which no exit status could prevent.
type taskReceipt struct {
	w          io.Writer
	want, tail []byte
	seen       bool
}

func newTaskReceipt(w io.Writer) *taskReceipt {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck // crypto/rand.Read never fails
	return &taskReceipt{w: w, want: []byte("stackfork: task finished " + hex.EncodeToString(b))}
}

// command is the argument vector that runs cmd with the receipt.
func (t *taskReceipt) command(cmd string) []string {
	return []string{"sh", "-c", receiptScript, "sh", cmd, string(t.want)}
}

func (t *taskReceipt) Write(p []byte) (int, error) {
	if !t.seen {
		buf := append(t.tail, p...)
		t.seen = bytes.Contains(buf, t.want)
		if keep := len(t.want) - 1; len(buf) > keep {
			buf = buf[len(buf)-keep:]
		}
		t.tail = append([]byte(nil), buf...)
	}
	return t.w.Write(p)
}

// noReceiptNote is what a task that exited 0 without its receipt prints.
const noReceiptNote = "note: %s exited 0, but its command did not report that it ran to the end (does the service's entrypoint run the command it is given?); this run is no execution evidence for it\n"
