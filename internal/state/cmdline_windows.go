//go:build windows

package state

// cmdlineMatches is always true on Windows.
// ponytail: reading another process's command line needs WMI or the PEB; identity is PID + creation time, which a recycled PID cannot share. Upgrade path: NtQueryInformationProcess.
func cmdlineMatches(pid int, recorded []string) bool { return true }
