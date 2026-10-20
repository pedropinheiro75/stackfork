package secretstore

import (
	"fmt"
	"strings"
)

// readKeychain reads (wantValue) or checks the item through the OS tool (D3).
// Without wantValue no value is requested from the tool.
func (b *Backend) readKeychain(item string, wantValue bool) (string, Status, error) {
	tool, err := b.keychainTool()
	if err != nil {
		return "", Unavailable, fmt.Errorf("credential store: %w", err)
	}
	if b.goos() == "darwin" {
		return b.macRead(tool, item, wantValue)
	}
	return b.linuxRead(tool, item, wantValue)
}

func (b *Backend) writeKeychain(item, value string) error {
	tool, err := b.keychainTool()
	if err != nil {
		return fmt.Errorf("credential store: %w", err)
	}
	if b.goos() == "darwin" {
		return b.macStore(tool, item, value)
	}
	return b.linuxStore(tool, item, value)
}

// macQuote quotes value for a `security -i` command line: single quotes,
// with backslash and single quote escaped.
func macQuote(value string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return "'" + r.Replace(value) + "'"
}

func (b *Backend) macRead(tool, item string, wantValue bool) (string, Status, error) {
	args := []string{"find-generic-password", "-s", Service, "-a", item}
	if wantValue {
		args = append(args, "-w")
	}
	r := runTool(tool, args, "", toolEnv("darwin", false), b.timeout(shortTimeout))
	switch {
	case r.err != nil:
		return "", Error, fmt.Errorf("security: %v", r.err)
	case r.exit == 44:
		return "", Missing, fmt.Errorf("keychain item %s not found", item)
	case r.exit == 36 || strings.Contains(r.stderr, "User interaction is not allowed"):
		return "", Locked, fmt.Errorf("the keychain is locked")
	case r.exit != 0:
		return "", Error, fmt.Errorf("security exited %d: %s", r.exit, firstLine(r.stderr))
	}
	if !wantValue {
		return "", Resolves, nil
	}
	value := strings.TrimSuffix(r.stdout, "\n")
	if value == "" {
		return "", Missing, fmt.Errorf("keychain item %s is empty", item)
	}
	return value, Resolves, nil
}

// macStore writes through `security -i`: the command line, with the quoted
// value, goes to stdin, so the value is never in an argument list. The
// interactive mode can report a failed command without a failing exit
// status, so the write is confirmed with an existence check.
func (b *Backend) macStore(tool, item, value string) error {
	line := "add-generic-password -U -s " + Service + " -a " + item + " -w " + macQuote(value) + "\n"
	r := runTool(tool, []string{"-i"}, line, toolEnv("darwin", false), b.timeout(shortTimeout))
	switch {
	case r.err != nil:
		return fmt.Errorf("security: %v", r.err)
	case r.exit == 36 || strings.Contains(r.stderr, "User interaction is not allowed"):
		return fmt.Errorf("the keychain is locked; unlock it and try again")
	case r.exit != 0:
		return fmt.Errorf("security exited %d: %s", r.exit, firstLine(r.stderr))
	}
	if _, st, err := b.macRead(tool, item, false); st != Resolves {
		return fmt.Errorf("security did not store keychain:%s (%s: %v)", item, st, err)
	}
	return nil
}

func (b *Backend) linuxRead(tool, item string, wantValue bool) (string, Status, error) {
	return "", Unavailable, errNoBackend
}

func (b *Backend) linuxStore(tool, item, value string) error { return errNoBackend }
