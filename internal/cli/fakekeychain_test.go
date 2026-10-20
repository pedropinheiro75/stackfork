package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/secretstore"
)

// fakeKeychain points the secret store at a shell script that plays
// /usr/bin/security over a directory of items (one file per item). Nothing
// touches the real keychain, keyring or 1Password. It returns the item
// directory; a file "stdin.rec" there records what `security -i` was fed.
func fakeKeychain(t *testing.T, items map[string]string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake security tool is a shell script")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(dir, "items"), 0o700)
	for item, value := range items {
		if err := os.WriteFile(filepath.Join(dir, "items", item), []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
D="` + dir + `"
if [ "$1" = "-i" ]; then
  line=$(cat)
  printf '%s\n' "$line" >> "$D/stdin.rec"
  item=$(printf '%s' "$line" | sed -n 's/.* -a \([^ ]*\) -w .*/\1/p')
  val=$(printf '%s' "$line" | sed -n "s/.* -w '\(.*\)'\$/\1/p")
  printf '%s\n' "$val" > "$D/items/$item"
  exit 0
fi
item=; w=0
while [ $# -gt 0 ]; do
  case "$1" in -a) item=$2; shift;; -w) w=1;; esac
  shift
done
[ -f "$D/items/$item" ] || exit 44
[ $w = 1 ] && cat "$D/items/$item"
exit 0
`
	tool := filepath.Join(dir, "security")
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := *secretstore.Default
	secretstore.Default.GOOS, secretstore.Default.Security = "darwin", tool
	t.Cleanup(func() { *secretstore.Default = old })
	return dir
}
