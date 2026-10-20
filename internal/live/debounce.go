package live

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Debounce periods (design D4).
const (
	Quiet   = 3 * time.Second
	Ceiling = 60 * time.Second
)

// Debouncer turns the pending changes of each tick into one batch: it fires
// when nothing new has appeared for Quiet, or Ceiling after the first
// unreconciled change when changes keep coming. The clock is the caller's.
type Debouncer struct {
	Quiet, Ceiling time.Duration
	active         bool
	sig            string
	first, last    time.Time
}

// NewDebouncer uses the design's 3 s and 60 s.
func NewDebouncer() *Debouncer { return &Debouncer{Quiet: Quiet, Ceiling: Ceiling} }

// Observe is called once per tick with the poller's pending changes. It
// returns true when the batch is due; the caller then runs it, and the
// debouncer starts over.
func (d *Debouncer) Observe(now time.Time, pending []Change) bool {
	if len(pending) == 0 {
		d.active = false
		return false
	}
	h := sha256.New()
	for _, c := range pending {
		h.Write([]byte(c.Path + "\x00" + c.Digest + "\x00"))
	}
	sig := hex.EncodeToString(h.Sum(nil))
	if !d.active {
		d.active, d.sig, d.first, d.last = true, sig, now, now
	} else if sig != d.sig {
		d.sig, d.last = sig, now
	}
	if now.Sub(d.last) >= d.Quiet || now.Sub(d.first) >= d.Ceiling {
		d.active = false
		return true
	}
	return false
}
