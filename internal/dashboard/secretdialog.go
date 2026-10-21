package dashboard

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/secretstore"
)

// secretDialogScript asks for one value with a hidden answer. Everything it
// shows arrives in argv, never spliced into the script, so a repository or
// secret name cannot change what the script does. The answer goes to standard
// output, which the server reads: not to the page, not to argv, not to a log.
const secretDialogScript = `on run argv
	set r to display dialog (item 1 of argv) with title (item 2 of argv) default answer "" with hidden answer buttons {"Cancel", "Store"} default button "Store" cancel button "Cancel" with icon note giving up after 300
	if gave up of r then error number -128
	return text returned of r
end run`

// osascriptPath is the system's own tool at its fixed location (never PATH,
// as for the store tools). A var so tests can point it at a recording stub.
var osascriptPath = "/usr/bin/osascript"

var errSecretCancelled = errors.New("cancelled")

// secretDialogText is what the dialog says: the action, the repository, the
// secret and the exact store item, so a person can tell a real request from
// a forged one (D13). Control characters are shown as ?.
func secretDialogText(repo, name, item string) (prompt, title string) {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return '?'
			}
			return r
		}, s)
	}
	prompt = "Type the new value for " + clean(name) + " of " + clean(repo) + ".\n\n" +
		"stackfork stores it in your login keychain as keychain:" + clean(item) + " (service stackfork).\n\n" +
		"You opened this with Set value… on the dashboard. If you did not, press Cancel."
	return prompt, "stackfork: set a secret value"
}

func init() {
	AskSecretValue = askSecretValueNative
}

// askSecretValueNative opens the macOS hidden-answer dialog and returns what
// the person typed. Errors never contain the value.
func askSecretValueNative(repo, name, item string) (string, error) {
	if runtime.GOOS == "windows" {
		return secretstore.Default.HiddenValueDialog(repo, name, item)
	}
	prompt, title := secretDialogText(repo, name, item)
	ctx, cancel := context.WithTimeout(context.Background(), 330*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, osascriptPath, "-e", secretDialogScript, prompt, title)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil { // Cancel and the timeout both exit non-zero
		return "", errSecretCancelled
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}
