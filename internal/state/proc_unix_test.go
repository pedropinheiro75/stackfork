//go:build !windows

package state

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestKillGroupRefusesUnsafeTargets(t *testing.T) {
	for _, pid := range []int{-1, 0, 1, os.Getpid(), os.Getppid()} {
		if err := KillGroup(pid, syscall.SIGTERM); err == nil {
			t.Errorf("KillGroup(%d) must refuse", pid)
		}
	}
}

func TestHostIdentityNeedsStartTime(t *testing.T) {
	start, err := ProcStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if HostIdentity(HostProc{PID: os.Getpid(), StartedAt: "1970-01-01T00:00:00Z", Cmdline: os.Args}) {
		t.Error("wrong start time must not match")
	}
	if !HostIdentity(HostProc{PID: os.Getpid(), StartedAt: start, Cmdline: os.Args}) {
		t.Error("own pid, start and argv must match")
	}
	if HostIdentity(HostProc{PID: os.Getpid(), StartedAt: start, Cmdline: []string{"other"}}) {
		t.Error("wrong cmdline must not match")
	}
}

func TestKillHostIsKillGroup(t *testing.T) {
	if err := KillHost(HostProc{PID: os.Getpid()}, syscall.SIGTERM); err == nil {
		t.Error("KillHost must refuse the calling process")
	}
	c := exec.Command("sleep", "30")
	c.SysProcAttr = DetachAttr()
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if err := KillHost(HostProc{PID: c.Process.Pid}, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := c.Wait(); err == nil {
		t.Error("child should have been killed")
	}
}
