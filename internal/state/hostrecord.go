package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// HostRecord is host-NAME.json in the instance dir: what the supervised
// command was and how it ended. The manifest's HostProcs entry identifies the
// supervisor; this identifies the command itself (PID plus StartedAt, so a
// recycled pid never matches). Argv is redacted; it never holds environment
// values.
type HostRecord struct {
	Name          string   `json:"name"`
	PID           int      `json:"pid"`            // the supervised command
	SupervisorPID int      `json:"supervisor_pid"` // this stackfork process
	Argv          []string `json:"argv"`
	Cwd           string   `json:"cwd"`
	Port          int      `json:"port"`
	StartedAt     string   `json:"started_at"` // RFC3339 UTC, the command's process start time
	ExitedAt      string   `json:"exited_at,omitempty"`
	ExitCode      *int     `json:"exit_code,omitempty"` // 128+N when a signal N ended it
	// Listened is true once the port first answered: the app is a server and
	// not a one-shot command. StoppedByPerson is set by `host stop` and the
	// dashboard Stop; only a new start (a fresh record) clears it. `up` starts
	// an app again only if it listened and nobody stopped it (see lifecycle).
	Listened        bool `json:"listened,omitempty"`
	StoppedByPerson bool `json:"stopped_by_person,omitempty"`
}

// HostRecordPath is where the record of host app name lives.
func HostRecordPath(instanceDir, name string) string {
	return filepath.Join(instanceDir, "host-"+name+".json")
}

// WriteHostRecord writes the record atomically with mode 0600.
func WriteHostRecord(instanceDir string, r HostRecord) error {
	return WritePrivate(HostRecordPath(instanceDir, r.Name), r)
}

// ReadHostRecord returns nil, nil when there is no record.
func ReadHostRecord(instanceDir, name string) (*HostRecord, error) {
	b, err := os.ReadFile(HostRecordPath(instanceDir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r HostRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
