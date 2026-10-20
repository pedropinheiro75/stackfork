// env.go implements `stackfork env` (add-env-and-seed, capability
// host-env): the current checkout+branch's instance env block - one line
// per database/cache/mail/HTTP service - in a form a host-run app can
// `eval` or write to a .env file. It reuses the dashboard's own
// inventory.EnvBlock/CopyValue machinery (design.md D3b §4) rather than
// building its own copy: the placeholder-safe block is inventory.Instance's
// own EnvBlock field, and --with-passwords goes through the exact same
// inventory.CopyValue("env_block", ...) path POST /api/copy already uses,
// so a value only ever comes back real when LiteralPassword can verify it's
// a literal Compose value (never --secret, env_file, or an interpolated
// ${VAR}).
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/dashboard"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

func cmdEnv(stdout io.Writer, subArgs []string, root, branch, project, instanceDir, newStateBase, legacyStateBase string) error {
	fs := newFlagSet(stdout, "env")
	format := fs.String("format", "shell", "")
	withPasswords := fs.Bool("with-passwords", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailureErr(fs, err)
	}
	if *format != "shell" && *format != "dotenv" && *format != "powershell" {
		return fmt.Errorf("invalid --format %q; want shell, dotenv or powershell", *format)
	}

	block, err := envBlock(project, instanceDir, newStateBase, legacyStateBase, *withPasswords)
	if err != nil {
		return err
	}

	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch *format {
		case "dotenv":
			fmt.Fprintf(stdout, "%s=%s\n", key, dotenvQuote(value))
		case "powershell":
			fmt.Fprintf(stdout, "$env:%s = %s\n", key, powershellQuote(value))
		default:
			fmt.Fprintf(stdout, "export %s=%s\n", key, shellQuote(value))
		}
	}
	return nil
}

// shellQuote wraps a value in single quotes, the one POSIX-safe form that
// needs no further escaping except for an embedded single quote itself:
// close the quote, emit an escaped literal quote, reopen it. Safe for
// `eval "$(stackfork env)"` regardless of what a value contains ($, `,
// spaces, newlines, ...).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// powershellQuote wraps a value in a PowerShell single-quoted string (every
// single-quote lookalike doubled, see dashboard.PSQuote). $, backtick and
// newlines stay literal.
func powershellQuote(s string) string { return dashboard.PSQuote(s) }

// dotenvQuote double-quotes a value for a plain KEY="value" line, escaping
// the two characters that would otherwise break out of the quotes.
func dotenvQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// envBlock is the instance's env block, shared by `env` and `host`: the
// placeholder-safe block by default, or - withPasswords - real values only
// where LiteralPassword can verify a literal Compose value.
func envBlock(project, instanceDir, newStateBase, legacyStateBase string, withPasswords bool) (string, error) {
	inv, err := inventory.Collect(inventory.Options{NewStateBase: newStateBase, LegacyStateBase: legacyStateBase, Version: Version})
	if err != nil {
		return "", err
	}
	var inst *inventory.Instance
	for i := range inv.Instances {
		if inv.Instances[i].Project == project {
			inst = &inv.Instances[i]
			break
		}
	}
	if inst == nil {
		return "", errors.New("No prepared stack for this checkout/branch. Run prepare first.")
	}
	// Anything short of every declared service actually running ("partial")
	// can mean an unresolved/unassigned port behind one of the block's own
	// lines (a service's ConnectionURL/endpoint URL is only ever built from
	// a live, currently-bound port) - refuse rather than hand a host app a
	// silently incomplete environment.
	if inst.State != "running" {
		return "", fmt.Errorf("instance is %s, not running; run `stackfork up` first", inst.State)
	}

	block := inst.EnvBlock
	if withPasswords {
		svcRaw, err := inventory.ReadComposeServices(instanceDir)
		if err != nil {
			return "", err
		}
		if value, ok := inventory.CopyValue("env_block", "", inst.Services, inst.Files, svcRaw); ok {
			block = value
		}
	}
	if block == "" {
		return "", errors.New("nothing to export: no database, cache, mail or HTTP service found for this instance")
	}
	return block, nil
}
