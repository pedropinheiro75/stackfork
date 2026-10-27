package inventory

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/ports"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// HostInfo is what the dashboard and the API show of a host app beyond the
// service row: the process, its resource use and how it ended.
type HostInfo struct {
	PID       int    `json:"pid,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	ExitedAt  string `json:"exited_at,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Listening bool   `json:"listening"`
	Port      int    `json:"port,omitempty"`
	// Measured is false when ps is not available (Windows) or does not list
	// the process: RSSBytes and CPUPercent are then unknown, not zero.
	Measured   bool    `json:"measured"`
	RSSBytes   int64   `json:"rss_bytes,omitempty"`
	CPUPercent float64 `json:"cpu_percent,omitempty"`
}

// procRow is one line of `ps -A -o pid=,ppid=,rss=,pcpu=`.
type procRow struct {
	pid, ppid int
	rssKB     int64
	cpu       float64
}

// runPS is the one process listing of a poll (a test replaces it).
var runPS = func() (string, error) {
	cmd := exec.Command("ps", "-A", "-o", "pid=,ppid=,rss=,pcpu=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	return string(out), err
}

func parsePS(out string) []procRow {
	var rows []procRow
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		rss, e3 := strconv.ParseInt(f[2], 10, 64)
		cpu, e4 := strconv.ParseFloat(f[3], 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			continue
		}
		rows = append(rows, procRow{pid, ppid, rss, cpu})
	}
	return rows
}

// treeUsage sums RSS and CPU over root and all its descendants. ok is false
// when root is not in rows.
func treeUsage(rows []procRow, root int) (rssBytes int64, cpu float64, ok bool) {
	kids := map[int][]int{}
	byPID := map[int]procRow{}
	for _, r := range rows {
		byPID[r.pid] = r
		kids[r.ppid] = append(kids[r.ppid], r.pid)
	}
	if _, ok = byPID[root]; !ok {
		return 0, 0, false
	}
	seen := map[int]bool{}
	for stack := []int{root}; len(stack) > 0; {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[p] {
			continue
		}
		seen[p] = true
		rssBytes += byPID[p].rssKB * 1024
		cpu += byPID[p].cpu
		stack = append(stack, kids[p]...)
	}
	return rssBytes, cpu, true
}

// psSnapshot lists processes at most once per poll, and only when some
// instance has a host app.
type psSnapshot struct {
	done bool
	rows []procRow
}

func (s *psSnapshot) usage(pid int) (int64, float64, bool) {
	if s == nil {
		return 0, 0, false
	}
	if !s.done {
		s.done = true
		if out, err := runPS(); err == nil {
			s.rows = parsePS(out)
		}
	}
	return treeUsage(s.rows, pid)
}

// hostInspect builds the state, HostInfo and meta line of one host app. It is
// running only when the recorded identity (pid and start time) holds; a
// recycled pid never counts. A host app with a port whose port does not answer
// is "starting" for hostGrace after its start, then "running" with
// Listening false (the pid lives, the server does not answer). A process that
// never listened and exited 0 is "finished": a one-shot task, not a server.
const hostGrace = 30 * time.Second

func hostInspect(instanceDir, name string, proc state.HostProc, snap *psSnapshot, now time.Time) (st string, info *HostInfo, meta string) {
	rec, _ := state.ReadHostRecord(instanceDir, name)
	info = &HostInfo{StartedAt: proc.StartedAt, Port: proc.Port}
	alive := proc.PID != 0 && state.HostIdentity(proc)
	info.Listening = alive && ports.Listening(proc.Port)
	if alive {
		info.PID = proc.PID
		if rec != nil && rec.StartedAt != "" {
			info.StartedAt = rec.StartedAt
		}
		if rec != nil && rec.PID != 0 && rec.SupervisorPID == proc.PID {
			info.PID = rec.PID
		}
		if rss, cpu, ok := snap.usage(proc.PID); ok {
			info.Measured, info.RSSBytes, info.CPUPercent = true, rss, cpu
		}
	} else if rec != nil && rec.SupervisorPID == proc.PID {
		info.StartedAt, info.ExitedAt, info.ExitCode = rec.StartedAt, rec.ExitedAt, rec.ExitCode
	} else if rec != nil { // written by another supervisor: it says nothing about this entry
		return "exited", info, "exited (details unknown)"
	}
	if alive {
		st = "running"
		if t, err := time.Parse(time.RFC3339, info.StartedAt); proc.Port != 0 && !info.Listening && (err != nil || now.Sub(t) < hostGrace) {
			st = "starting" // up, but nothing answers on the port yet
		}
		return st, info, hostMeta(info, now)
	}
	meta = "exited"
	at := ""
	if t, err := time.Parse(time.RFC3339, info.ExitedAt); err == nil {
		at = " at " + t.Local().Format("15:04")
	}
	if info.ExitCode != nil {
		meta = fmt.Sprintf("exited %d", *info.ExitCode) + at
		if *info.ExitCode == 0 && rec != nil && !rec.Listened && !rec.StoppedByPerson {
			return "finished", info, fmt.Sprintf("exit 0%s", at)
		}
	}
	return "exited", info, meta
}

// hostMeta is "pid 4242 · 312 MB · 3% CPU · up 12m".
func hostMeta(h *HostInfo, now time.Time) string {
	parts := []string{fmt.Sprintf("pid %d", h.PID)}
	if h.Measured {
		parts = append(parts, fmt.Sprintf("%d MB", h.RSSBytes>>20), fmt.Sprintf("%.0f%% CPU", h.CPUPercent))
	}
	if t, err := time.Parse(time.RFC3339, h.StartedAt); err == nil {
		parts = append(parts, "up "+uptime(now.Sub(t)))
	}
	return strings.Join(parts, " · ")
}

func uptime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d.Seconds()), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours())/24)
}

// HostApps lists the host apps recorded in m (instanceDir is its directory),
// with the same state and meta line the dashboard shows. It runs ps once.
func HostApps(m *state.Manifest, instanceDir string) []Service {
	return hostServices(m, "", instanceDir, &psSnapshot{})
}

// ComposeRunning is true when every Compose service runs: a host app that is
// down makes the instance partial but does not take the containers away.
func (i Instance) ComposeRunning() bool {
	if i.State == StateRunning {
		return true
	}
	if i.State != StatePartial {
		return false
	}
	for _, s := range i.Services {
		if s.Kind != "host" && s.State != "running" && s.State != ServiceOptional {
			return false
		}
	}
	return true
}
