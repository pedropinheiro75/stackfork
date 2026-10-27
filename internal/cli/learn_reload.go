package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

const reloadUsage = "learn record reload SERVICE|HOSTAPP hot|restart|rebuild [--note TEXT]"

func hasReload(list []knowledge.Reload, service string) bool {
	for _, r := range list {
		if r.Service == service {
			return true
		}
	}
	return false
}

// recordReload is `learn record reload`: it replaces the app's entry, proposed or not.
func recordReload(k *knowledge.Knowledge, rest []string, stack stackView) error {
	fs := newFlagSet(io.Discard, "learn record reload")
	note := fs.String("note", "", "")
	args, err := parseNamed(fs, rest)
	if err != nil {
		return err
	}
	if len(args) != 2 {
		return errors.New("usage: " + reloadUsage)
	}
	name, mode := args[0], args[1]
	if !knowledge.ValidReloadMode(mode) {
		return fmt.Errorf("mode must be hot, restart or rebuild, got %q", mode)
	}
	_, isHost := stack.hosts[name]
	if _, isSvc := stack.targets[name]; !isHost && !isSvc && stack.known {
		return fmt.Errorf("%s is not a service or host app of this project", name)
	}
	r := knowledge.Reload{Service: name, Mode: mode, Note: *note}
	for i := range k.Reload {
		if k.Reload[i].Service == name {
			k.Reload[i] = r
			return knowledge.ValidateReload(k.Reload)
		}
	}
	k.Reload = append(k.Reload, r)
	return knowledge.ValidateReload(k.Reload)
}

// reloadWarning asks for the entry app's reload mode when nobody confirmed it.
func reloadWarning(k *knowledge.Knowledge) []string {
	if k.Entry == nil || k.Entry.None || k.Entry.Proposed || k.Entry.Service == "" {
		return nil
	}
	if r := k.ReloadFor(k.Entry.Service); r != nil && !r.Proposed {
		return nil
	}
	s := k.Entry.Service
	return []string{fmt.Sprintf("how code changes reach %s is not confirmed: edit a visible string, check it changes without a restart, revert the edit, then run `stackfork learn record reload %s hot|restart|rebuild`", s, s)}
}
