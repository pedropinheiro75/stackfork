package secretstore

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The test binary doubles as the fake store tool (design: tests never touch
// the real keychain, Secret Service or 1Password). A copy of the binary
// with a ".cfg" file next to it acts as the tool: it records argv, stdin and
// environment to ".rec" (one JSON object per call) and answers as the config
// says. The config is a file because store tools run with a minimal
// environment.

type helperCfg struct {
	Exit    int    `json:"exit"`
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
	SleepMS int    `json:"sleep_ms"`
}

type helperRec struct {
	Argv  []string `json:"argv"`
	Stdin string   `json:"stdin"`
	Env   []string `json:"env"`
}

func TestMain(m *testing.M) {
	if data, err := os.ReadFile(os.Args[0] + ".cfg"); err == nil {
		os.Exit(runHelper(data))
	}
	os.Exit(m.Run())
}

func runHelper(cfgData []byte) int {
	var cfg helperCfg
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		return 99
	}
	stdin, _ := io.ReadAll(os.Stdin)
	rec, _ := json.Marshal(helperRec{Argv: os.Args[1:], Stdin: string(stdin), Env: os.Environ()})
	if f, err := os.OpenFile(os.Args[0]+".rec", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		f.Write(append(rec, '\n'))
		f.Close()
	}
	time.Sleep(time.Duration(cfg.SleepMS) * time.Millisecond)
	os.Stdout.WriteString(cfg.Stdout)
	os.Stderr.WriteString(cfg.Stderr)
	return cfg.Exit
}

// newHelper installs a fake tool in its own private directory and returns
// its path.
func newHelper(t *testing.T, cfg helperCfg) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return newHelperIn(t, dir, "tool", cfg)
}

func newHelperIn(t *testing.T, dir, name string, cfg helperCfg) string {
	t.Helper()
	exe, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, exe, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, 0o755)
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(p+".cfg", data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// calls returns what the helper at p recorded.
func calls(t *testing.T, p string) []helperRec {
	t.Helper()
	data, err := os.ReadFile(p + ".rec")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []helperRec
	dec := json.NewDecoder(bytesReader(data))
	for dec.More() {
		var r helperRec
		if err := dec.Decode(&r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
