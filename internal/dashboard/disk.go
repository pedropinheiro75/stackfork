package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/dockerdisk"
)

// diskTTL: each measurement starts a throwaway container, so the header
// reads a cached value and refreshes it in the background at most this often.
const diskTTL = 5 * time.Minute

// fullDiskTTL: while the banner shows, re-measure often, so it clears soon
// after space is freed (outside the dashboard too).
const fullDiskTTL = 30 * time.Second

func (d *diskState) ttl() time.Duration {
	if d.usage != nil && d.usage.Full() {
		return fullDiskTTL
	}
	return diskTTL
}

// diskState caches dockerdisk.Measure for the header banner.
type diskState struct {
	mu        sync.Mutex
	usage     *dockerdisk.Usage
	at        time.Time
	measuring bool
}

// get returns the last reading (nil before the first one) and starts a
// background refresh when it is older than diskTTL.
func (d *diskState) get() *dockerdisk.Usage {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.measuring && time.Since(d.at) > d.ttl() {
		d.measuring = true
		go d.refresh()
	}
	return d.usage
}

func (d *diskState) refresh() {
	u, err := dockerdisk.Measure(context.Background())
	d.mu.Lock()
	defer d.mu.Unlock()
	d.measuring, d.at = false, time.Now()
	if err == nil {
		d.usage = &u
	}
}

// handleDisk is the header's disk banner: {} until the first reading.
func (s *Server) handleDisk(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{}
	if u := s.disk.get(); u != nil {
		body = map[string]any{"percent": u.Percent, "text": "Docker disk: " + u.String(), "full": u.Full()}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// freeSpace runs the same steps as `stackfork doctor --free-space` (no
// named volume, so no database, is touched) and re-measures.
func (s *Server) freeSpace() actionResponse {
	report, err := dockerdisk.Free(context.Background())
	s.disk.refresh()
	msg := strings.Join(report, "; ")
	if err != nil {
		return actionResponse{Message: strings.TrimPrefix(msg+"; ", "; ") + err.Error()}
	}
	if u := s.disk.get(); u != nil {
		msg += "; now " + u.String()
	}
	return actionResponse{OK: true, Message: msg}
}
