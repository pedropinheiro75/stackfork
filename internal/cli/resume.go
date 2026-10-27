package cli

import (
	"errors"
	"io"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
)

// cmdResume is `stackfork resume`: the resume the dashboard runs at start,
// once, from a terminal. It only starts what the person already ran.
func cmdResume(stdout io.Writer, subArgs []string, newStateBase, legacyStateBase string) (int, error) {
	if isHelpRequest(subArgs) {
		printFlaglessUsage(stdout, "resume", "resume")
		return 0, nil
	}
	if len(subArgs) > 0 {
		return 2, errors.New("resume takes no arguments")
	}
	srv, err := dashboard.New(newStateBase, legacyStateBase, Version, 0)
	if err != nil {
		return 0, err
	}
	defer srv.Close()
	_, err = srv.Resume(stdout)
	return 0, err
}
