//go:build !windows

package state

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// DetachAttr makes a child a session (and group) leader, so it outlives the
// caller and can be signalled as one group.
func DetachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// KillGroup signals the process group led by pid. It refuses anything that is
// not a group leader of its own (pgid == pid), pid <= 1, and the calling
// process's own group, so a wrong PID can never reach an unrelated process.
func KillGroup(pid int, sig syscall.Signal) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to signal pid %d", pid)
	}
	pg, err := syscall.Getpgid(pid)
	if err != nil {
		return err
	}
	if pg != pid {
		return fmt.Errorf("pid %d does not lead its own process group; refusing to signal it", pid)
	}
	if pg == syscall.Getpgrp() || pid == os.Getpid() {
		return errors.New("refusing to signal the calling process's own group")
	}
	return syscall.Kill(-pg, sig)
}

func psField(pid int, fields string) (string, error) {
	args := []string{"-ww"}
	for _, f := range strings.Split(fields, ",") {
		args = append(args, "-o", f+"=") // one -o each: "a,b=" would still print a header for a
	}
	cmd := exec.Command("ps", append(args, "-p", fmt.Sprint(pid))...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// KillOrphanGroup SIGKILLs the process group pgid if any member is left. Only
// for the moment after a verified group leader has exited: the kernel does not
// reuse a PID while a group still carries it as its id, so a group that still
// exists is the leader's leftovers and not an unrelated process. Refuses
// pgid <= 1 and the caller's own group.
func KillOrphanGroup(pgid int) error {
	if pgid <= 1 || pgid == syscall.Getpgrp() {
		return fmt.Errorf("refusing to signal process group %d", pgid)
	}
	if err := syscall.Kill(-pgid, 0); err != nil {
		return nil // nobody left
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

// parseProcStat reads /proc/PID/stat: the state letter and the start time in
// jiffies since boot (field 22). comm (field 2) may hold spaces and parens, so
// the fields are counted after the last ')'.
func parseProcStat(stat string) (state string, startTicks string, err error) {
	i := strings.LastIndex(stat, ")")
	if i < 0 {
		return "", "", errors.New("bad stat")
	}
	f := strings.Fields(stat[i+1:])
	if len(f) < 20 { // state is field 3 = f[0]; starttime is field 22 = f[19]
		return "", "", errors.New("bad stat")
	}
	return f[0], f[19], nil
}

// ProcStart identifies when a process started: "jiffies:N" from /proc where it
// exists (exact, no clock jitter), else the RFC3339 UTC time from ps lstart
// (second resolution). Error when pid does not exist or is a zombie.
func ProcStart(pid int) (string, error) {
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		st, ticks, err := parseProcStat(string(b))
		if err != nil || st == "Z" {
			return "", errors.New("no such process")
		}
		return "jiffies:" + ticks, nil
	}
	out, err := psField(pid, "stat,lstart")
	if err != nil || out == "" {
		return "", errors.New("no such process")
	}
	f := strings.Fields(out)
	if len(f) < 6 || strings.HasPrefix(f[0], "Z") {
		return "", errors.New("no such process")
	}
	t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.Join(f[1:], " "), time.Local)
	if err != nil {
		return "", err
	}
	return t.UTC().Format(time.RFC3339), nil
}

func procCmdline(pid int) (string, error) { return psField(pid, "args") }

// processAlive: the PID exists AND it started at startedAt. A recycled PID
// has a different start time.
func processAlive(pid int, startedAt string) bool {
	if pid <= 1 || startedAt == "" {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && err != syscall.EPERM {
		return false
	}
	got, err := ProcStart(pid)
	return err == nil && got == startedAt
}
