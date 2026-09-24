package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// moduleRoot finds the repo root from this test file's own path, so the
// parity test can locate scripts/compose.py and testdata/ regardless of
// the working directory `go test` is invoked from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	// internal/cli/parity_test.go -> repo root
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func pythonAvailable(t *testing.T) (python3 string, ok bool) {
	t.Helper()
	path, err := exec.LookPath("python3")
	if err != nil {
		return "", false
	}
	return path, true
}

func runPython(t *testing.T, python3, script string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(python3, append([]string{script}, args...)...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("running python3: %v", err)
		}
	}
	return out.String(), errBuf.String(), code
}

func readJSONFile(t *testing.T, path string) interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("invalid JSON in %s: %v", path, err)
	}
	return v
}

// stripKeys removes the given top-level keys from a decoded JSON object
// (the only state-dir-dependent / tool-specific fields: config_sha256 is a
// hash of each tool's own serialization of the snapshot, and "state" is the
// state directory path, which differs between the two --state-dir values
// used to keep the two runs from colliding).
func stripKeys(v interface{}, keys ...string) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	out := make(map[string]interface{}, len(m))
	for k, val := range m {
		out[k] = val
	}
	for _, k := range keys {
		delete(out, k)
	}
	return out
}

func assertStructurallyEqual(t *testing.T, label string, a, b interface{}) {
	t.Helper()
	if !reflect.DeepEqual(a, b) {
		ab, _ := json.MarshalIndent(a, "", "  ")
		bb, _ := json.MarshalIndent(b, "", "  ")
		t.Fatalf("%s mismatch:\n--- python ---\n%s\n--- go ---\n%s", label, ab, bb)
	}
}

// TestGoldenParitySuccess ports task 3.5: for each testdata/ fixture, run
// Python's prepare and Go's prepare against the SAME checkout (so the
// identity, and therefore the isolated snapshot, is byte-for-byte
// comparable) and structurally compare the snapshot and manifest.
func TestGoldenParitySuccess(t *testing.T) {
	python3, ok := pythonAvailable(t)
	if !ok {
		t.Skip("python3 not available")
	}
	if !hasDockerCLI(t) {
		t.Skip("docker not available")
	}
	root := moduleRoot(t)
	script := filepath.Join(root, "scripts", "compose.py")
	fixtures := []string{"pg17-fixed-name", "pg18-redis", "pg14-missing-initdb"}

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			checkout := initRepo(t)
			composeSrc := filepath.Join(root, "testdata", name, "compose.yaml")
			data, err := os.ReadFile(composeSrc)
			if err != nil {
				t.Fatal(err)
			}
			composePath := filepath.Join(checkout, "compose.yaml")
			if err := os.WriteFile(composePath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if name == "pg14-missing-initdb" {
				// The fixture's bind mount (./db.sql) is deliberately absent
				// to exercise an adapter override (see testdata/README.md);
				// for a plain success-path comparison, just provide it.
				if err := os.WriteFile(filepath.Join(checkout, "db.sql"), []byte(""), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			pyState := filepath.Join(t.TempDir(), "py-state")
			goState := filepath.Join(t.TempDir(), "go-state")

			pyOut, pyErr, pyCode := runPython(t, python3, script, "--root", checkout, "--state-dir", pyState, "prepare", "-f", composePath)
			if pyCode != 0 {
				t.Fatalf("python prepare failed: %s%s", pyOut, pyErr)
			}
			goOut, goErr, goCode := runCLI(t, "--root", checkout, "--state-dir", goState, "prepare", "-f", composePath)
			if goCode != 0 {
				t.Fatalf("go prepare failed: %s", goErr)
			}

			var pyPrepared, goPrepared interface{}
			if err := json.Unmarshal([]byte(pyOut), &pyPrepared); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(goOut), &goPrepared); err != nil {
				t.Fatal(err)
			}
			assertStructurallyEqual(t, "prepare output",
				stripKeys(pyPrepared, "state"), stripKeys(goPrepared, "state"))

			pyProject := pyPrepared.(map[string]interface{})["project"].(string)
			goProject := goPrepared.(map[string]interface{})["project"].(string)
			if pyProject != goProject {
				t.Fatalf("expected identical project names for the same checkout+branch, got %q vs %q", pyProject, goProject)
			}

			pyManifest := readJSONFile(t, filepath.Join(pyState, pyProject, "manifest.json"))
			goManifest := readJSONFile(t, filepath.Join(goState, goProject, "manifest.json"))
			assertStructurallyEqual(t, "manifest",
				stripKeys(pyManifest, "config_sha256"), stripKeys(goManifest, "config_sha256"))

			pySnapshot := readJSONFile(t, filepath.Join(pyState, pyProject, "compose.json"))
			goSnapshot := readJSONFile(t, filepath.Join(goState, goProject, "compose.json"))
			assertStructurallyEqual(t, "compose.json snapshot", pySnapshot, goSnapshot)
		})
	}
}

// TestGoldenParityRefusals ports the "bad" configs from
// scripts/test_compose.py's check(): both tools must refuse the same
// unsafe inputs with the same message.
func TestGoldenParityRefusals(t *testing.T) {
	python3, ok := pythonAvailable(t)
	if !ok {
		t.Skip("python3 not available")
	}
	if !hasDockerCLI(t) {
		t.Skip("docker not available")
	}
	root := moduleRoot(t)
	script := filepath.Join(root, "scripts", "compose.py")

	cases := []struct {
		name    string
		compose string
		setup   func(t *testing.T, checkout string)
	}{
		{"external-volume", "services:\n  app:\n    image: alpine\n    volumes:\n      - data:/data\nvolumes:\n  data:\n    external: true\n", nil},
		{"external-network", "services:\n  app:\n    image: alpine\nnetworks:\n  default:\n    external: true\n", nil},
		{"fixed-subnet", "networks:\n  default:\n    ipam:\n      config:\n        - subnet: 172.20.0.0/16\nservices:\n  app:\n    image: alpine\n", nil},
		{"network-mode-host", "services:\n  app:\n    image: alpine\n    network_mode: host\n", nil},
		{"privileged", "services:\n  app:\n    image: alpine\n    privileged: true\n", nil},
		{"env-file-outside-supplied", "services:\n  app:\n    image: alpine\n    env_file:\n      - other.env\n", func(t *testing.T, checkout string) {
			if err := os.WriteFile(filepath.Join(checkout, "other.env"), []byte("X=1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"writable-bind-outside-checkout", "services:\n  app:\n    image: alpine\n    volumes:\n      - /tmp:/data\n", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkout := initRepo(t)
			composePath := filepath.Join(checkout, "compose.yaml")
			if err := os.WriteFile(composePath, []byte(tc.compose), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.setup != nil {
				tc.setup(t, checkout)
			}

			pyState := filepath.Join(t.TempDir(), "py-state")
			goState := filepath.Join(t.TempDir(), "go-state")

			_, pyErr, pyCode := runPython(t, python3, script, "--root", checkout, "--state-dir", pyState, "prepare", "-f", composePath)
			_, goErr, goCode := runCLI(t, "--root", checkout, "--state-dir", goState, "prepare", "-f", composePath)

			if pyCode == 0 || goCode == 0 {
				t.Fatalf("expected both to refuse; python code=%d go code=%d\npy stderr=%s\ngo stderr=%s", pyCode, goCode, pyErr, goErr)
			}
			pyMsg := strings.TrimSpace(strings.TrimPrefix(pyErr, "isolated-dev: "))
			goMsg := strings.TrimSpace(strings.TrimPrefix(goErr, "isolated-dev: "))
			if pyMsg != goMsg {
				t.Fatalf("refusal message mismatch:\npython: %s\ngo:     %s", pyMsg, goMsg)
			}
		})
	}
}

func hasDockerCLI(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.Command("docker", "compose", "version").Run() == nil
}
