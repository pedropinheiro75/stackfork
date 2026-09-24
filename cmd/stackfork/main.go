// Command stackfork is the Docker Compose isolation CLI.
package main

import (
	"os"

	"github.com/pedropinheiro75/stackfork/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
