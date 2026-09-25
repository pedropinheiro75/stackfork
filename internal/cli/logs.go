// logs.go implements `stackfork logs [SERVICE] [--tail N] [--follow]`
// (task 4.1), built the same way `restart` is: the same manifest validation
// every lifecycle action uses, then `docker compose logs` on the snapshot,
// piped through internal/redact (task 4.1a) so a secret value never reaches
// the terminal.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
)

func cmdLogs(stdout, stderr io.Writer, subArgs []string, root, branch, project, instanceDir string) (int, error) {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tailFlag := fs.Int("tail", 200, "")
	followFlag := fs.Bool("follow", false, "")

	// `logs [SERVICE] [--tail N] [--follow]`: Go's flag package stops
	// parsing at the first non-flag argument, so a SERVICE given before
	// its flags (the usage this command documents) would otherwise leave
	// --tail/--follow unparsed. Pull a leading positional out before
	// handing the rest to fs.Parse, and also accept a trailing one
	// (SERVICE after its flags) via fs.Args().
	args := subArgs
	var service string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		service = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 2, fmt.Errorf("%s", err.Error())
	}
	if service == "" {
		if rest := fs.Args(); len(rest) > 0 {
			service = rest[0]
		}
	}

	info, err := lifecycle.LoadAndValidateManifest(instanceDir, root, branch, project, "logs")
	if err != nil {
		return 0, err
	}
	if err := lifecycle.LocalDocker(); err != nil {
		return 0, err
	}

	configPath := filepath.Join(instanceDir, "compose.json")
	dockerArgs := []string{"compose", "-p", project, "--project-directory", root,
		"--env-file", os.DevNull, "-f", configPath, "--profile", "*",
		"logs", "--tail", strconv.Itoa(*tailFlag)}
	if *followFlag {
		dockerArgs = append(dockerArgs, "--follow")
	}
	if service != "" {
		dockerArgs = append(dockerArgs, service)
	}

	cmd := exec.Command("docker", dockerArgs...)
	cmd.Env = compose.DockerEnv()
	redactedStdout := lifecycle.NewRedactedWriter(stdout, info)
	defer redactedStdout.Close()
	cmd.Stdout = redactedStdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if runErr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, runErr
}
