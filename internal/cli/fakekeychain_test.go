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
// directory; a file "stdin.rec" there records what `security -i` was fed, "args.rec" the
// argument lists of every call, and creating
// a file "locked" there makes reads fail like a locked keychain.
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
printf '%s\n' "$*" >> "$D/args.rec"
if [ "$1" = "-i" ]; then
  line=$(cat)
  printf '%s\n' "$line" >> "$D/stdin.rec"
  item=$(printf '%s' "$line" | sed -n 's/.* -a \([^ ]*\) -w .*/\1/p')
  val=$(printf '%s' "$line" | sed -n "s/.* -w '\(.*\)'\$/\1/p")
  printf '%s\n' "$val" > "$D/items/$item"
  exit 0
fi
[ -f "$D/locked" ] && { echo "User interaction is not allowed." >&2; exit 36; }
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

// fakeOp points the 1Password backend at a script that answers `op read`
// with value (no newline) for every reference.
func fakeOp(t *testing.T, value string) (calls string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake op tool is a shell script")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(dir, "op")
	calls = filepath.Join(dir, "calls.rec")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho \"$*\" >> \""+calls+"\"\nprintf '%s' '"+value+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := *secretstore.Default
	secretstore.Default.OpPaths = []string{tool}
	t.Cleanup(func() { *secretstore.Default = old })
	return calls
}
