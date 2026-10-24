package lifecycle

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// envInstance writes a minimal prepared instance: a snapshot with services web
// and db, a manifest with a host app and a vault secret, and an env file.
func envInstance(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	envFile := filepath.Join(dir, "project.env")
	os.WriteFile(envFile, []byte("# comment\nFROM_FILE=file-value\nexport QUOTED=\"two words\"\nDB_PASSWORD=file-secret\nDATABASE_URL=postgres://u:hunter2@db:5432/x\nENABLE_GUEST_ACCESS=false\n"), 0o600)
	snap := map[string]any{"services": map[string]any{
		"web": map[string]any{
			"image":    "nginx",
			"env_file": []any{map[string]any{"path": envFile, "required": true}},
			"environment": map[string]any{
				"ENABLE_GUEST_ACCESS": "true",
				"NPM_TOKEN":           "${NPM_TOKEN}",
				"PLAIN":               "x",
				"AUTH_LINE":           "Bearer ${NPM_TOKEN}",
				"API_KEY":             "literal-key",
			},
		},
		"db": map[string]any{"image": "postgres"},
	}}
	b, _ := json.Marshal(snap)
	os.WriteFile(filepath.Join(dir, "compose.json"), b, 0o600)
	m := &state.Manifest{Project: "p", Root: dir, Secrets: map[string]string{"npm_token": "env:NPM"},
		HostProcs: map[string]state.HostProc{"app": {PID: 1}}, HostPorts: map[string]int{"worker": 31000}}
	if err := state.WritePrivate(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSetEnvOverridesValidatesService(t *testing.T) {
	dir := envInstance(t)
	err := SetEnvOverrides(dir, "nope", map[string]string{"A": "1"}, nil, false)
	if err == nil || !strings.Contains(err.Error(), "app, db, web, worker") {
		t.Fatalf("want the valid names listed, got %v", err)
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 {
		t.Error("nothing may be written")
	}
	for _, svc := range []string{"web", "app", "worker"} {
		if err := SetEnvOverrides(dir, svc, map[string]string{"FLAG": "1"}, nil, false); err != nil {
			t.Errorf("%s: %v", svc, err)
		}
	}
}

func TestSetEnvOverridesRefusesSecretLikeNames(t *testing.T) {
	dir := envInstance(t)
	for _, name := range []string{"API_TOKEN", "db_password", "NPM_TOKEN", "npm_token"} {
		err := SetEnvOverrides(dir, "web", map[string]string{name: "abc"}, nil, false)
		if err == nil || !strings.Contains(err.Error(), "stackfork secret") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 {
		t.Error("nothing may be written")
	}
	if err := SetEnvOverrides(dir, "web", map[string]string{"API_TOKEN": "abc"}, nil, true); err != nil {
		t.Errorf("--allow-secret: %v", err)
	}
	// a set that mixes a fine name and a secret-like one writes neither
	dir = envInstance(t)
	if err := SetEnvOverrides(dir, "web", map[string]string{"OK": "1", "X_SECRET": "v"}, nil, false); err == nil {
		t.Error("expected a refusal")
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 {
		t.Error("a refused set must write nothing")
	}
}

func TestSetEnvOverridesBulkAndUnset(t *testing.T) {
	dir := envInstance(t)
	if err := SetEnvOverrides(dir, "web", map[string]string{"A": "1", "B": "2"}, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := SetEnvOverrides(dir, "web", map[string]string{"C": "3"}, []string{"A"}, false); err != nil {
		t.Fatal(err)
	}
	o, _ := state.ReadEnvOverrides(dir)
	if strings.Join(o.Pairs("web"), ",") != "B=2,C=3" {
		t.Errorf("pairs %v", o.Pairs("web"))
	}
	if err := SetEnvOverrides(dir, "web", nil, []string{"NOPE"}, false); err == nil || !strings.Contains(err.Error(), "no override NOPE") {
		t.Errorf("unset of a missing name: %v", err)
	}
}

func TestClearEnvOverrides(t *testing.T) {
	dir := envInstance(t)
	SetEnvOverrides(dir, "web", map[string]string{"A": "1"}, nil, false)
	SetEnvOverrides(dir, "app", map[string]string{"B": "2"}, nil, false)
	touched, n, err := ClearEnvOverrides(dir, "app")
	if err != nil || n != 1 || strings.Join(touched, ",") != "app" {
		t.Fatalf("%v %d %v", touched, n, err)
	}
	touched, n, err = ClearEnvOverrides(dir, "")
	if err != nil || n != 1 || strings.Join(touched, ",") != "web" {
		t.Fatalf("%v %d %v", touched, n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, state.EnvOverridesFile)); !os.IsNotExist(err) {
		t.Errorf("file should be gone: %v", err)
	}
}

func byName(vars []EnvVar, name string) *EnvVar {
	for i := range vars {
		if vars[i].Name == name {
			return &vars[i]
		}
	}
	return nil
}

func TestEnvViewListsNamesWithOriginAndMasking(t *testing.T) {
	dir := envInstance(t)
	if err := SetEnvOverrides(dir, "web", map[string]string{"ENABLE_GUEST_ACCESS": "false", "NEW_FLAG": "1"}, nil, false); err != nil {
		t.Fatal(err)
	}
	SetEnvOverrides(dir, "web", map[string]string{"STRIPE_KEY": "sk-live-0123456789"}, nil, true)
	o, _ := state.ReadEnvOverrides(dir) // a service a later prepare dropped: only a file edit can leave it
	o.Set("gone", "X", "1")
	o.Write(dir)
	svcs, err := EnvView(dir)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string]string{}
	var web *EnvService
	for i, s := range svcs {
		byKind[s.Name] = s.Kind
		if s.Name == "web" {
			web = &svcs[i]
		}
	}
	if byKind["web"] != "container" || byKind["db"] != "container" || byKind["app"] != "host" || byKind["worker"] != "host" || byKind["gone"] != "unknown" {
		t.Errorf("kinds %v", byKind)
	}
	if web == nil {
		t.Fatal("no web")
	}
	check := func(name, origin string, masked bool, value string) {
		t.Helper()
		v := byName(web.Vars, name)
		if v == nil {
			t.Errorf("%s missing", name)
			return
		}
		if v.Origin != origin || v.Masked != masked {
			t.Errorf("%s: origin=%s masked=%v", name, v.Origin, v.Masked)
		}
		if masked && v.Value != nil {
			t.Errorf("%s: masked but has a value", name)
		}
		if !masked && (v.Value == nil || *v.Value != value) {
			t.Errorf("%s: value %v, want %q", name, v.Value, value)
		}
	}
	check("ENABLE_GUEST_ACCESS", "override", false, "false")
	check("NEW_FLAG", "override", false, "1")
	check("PLAIN", "project", false, "x")
	check("FROM_FILE", "env_file", false, "file-value")
	check("QUOTED", "env_file", false, "two words")
	check("DB_PASSWORD", "env_file", true, "")
	check("API_KEY", "project", true, "")
	check("NPM_TOKEN", "vault", true, "")
	check("STRIPE_KEY", "override", true, "")
	check("DATABASE_URL", "env_file", false, "postgres://u:****@db:5432/x")
	if d := byName(web.Vars, "ENABLE_GUEST_ACCESS"); !d.HasDefault || d.Default == nil || *d.Default != "true" {
		t.Errorf("default of an override: %+v", d)
	}
	if d := byName(web.Vars, "NEW_FLAG"); d.HasDefault {
		t.Errorf("a new name has no default: %+v", d)
	}
	// the environment of the snapshot wins over env_file
	b, _ := json.Marshal(svcs)
	for _, leak := range []string{"file-secret", "sk-live-0123456789", "literal-key", "hunter2"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("%q leaked in the read model: %s", leak, b)
		}
	}
}

func TestEnvViewHostAppShowsOnlyItsOverrides(t *testing.T) {
	dir := envInstance(t)
	SetEnvOverrides(dir, "app", map[string]string{"VITE_ENABLE_X": "true"}, nil, false)
	svcs, _ := EnvView(dir)
	for _, s := range svcs {
		if s.Name == "app" {
			if len(s.Vars) != 1 || s.Vars[0].Origin != "override" || s.Vars[0].Name != "VITE_ENABLE_X" {
				t.Errorf("vars %+v", s.Vars)
			}
			return
		}
	}
	t.Fatal("no app")
}

// A service with a build key is flagged, so the panel can say a VITE_* value applies at the next build.
func TestEnvViewFlagsBuiltServices(t *testing.T) {
	dir := envInstance(t)
	p := filepath.Join(dir, "compose.json")
	var snap map[string]map[string]map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &snap)
	snap["services"]["web"]["build"] = map[string]any{"context": "."}
	b, _ = json.Marshal(snap)
	os.WriteFile(p, b, 0o600)
	view, err := EnvView(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range view {
		if want := s.Name == "web"; s.Build != want {
			t.Errorf("%s: build=%v, want %v", s.Name, s.Build, want)
		}
	}
}

// A name whose project value interpolates a secret of the instance is not
// overridable, even with --allow-secret (add-env-overrides D2).
func TestSetEnvOverridesRefusesVaultInterpolatedName(t *testing.T) {
	dir := envInstance(t)
	for _, allow := range []bool{false, true} {
		err := SetEnvOverrides(dir, "web", map[string]string{"AUTH_LINE": "x"}, nil, allow)
		if err == nil || !strings.Contains(err.Error(), "Settings → Secrets") {
			t.Errorf("allow=%v: %v", allow, err)
		}
	}
	if o, _ := state.ReadEnvOverrides(dir); o.Count() != 0 {
		t.Error("written")
	}
}

func TestMaskURLPassword(t *testing.T) {
	for in, want := range map[string]string{
		"postgres://u:pa/ss@h/db":      "postgres://u:****@h/db",
		"postgres://u:p@ss@h":          "postgres://u:****@h",
		"postgres://u:hunter2@db:5432": "postgres://u:****@db:5432",
		"http://h:80/path":             "http://h:80/path",
		"plain":                        "plain",
	} {
		if got := maskURLPassword(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

// A host step of the plan is a target before it ever started (D1).
func TestEnvTargetsIncludePlanHostSteps(t *testing.T) {
	dir := envInstance(t)
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	k := &knowledge.Knowledge{Schema: knowledge.CurrentSchema, Status: knowledge.StatusVerified,
		Plan: knowledge.Plan{Status: knowledge.PlanRecorded, Steps: []knowledge.Step{
			{ID: "app", Kind: knowledge.KindHost, Modes: []string{"hybrid"}, Name: "fresh", Cmd: "npm run dev"}}}}
	if err := k.Save(filepath.Join(dir, ".stackfork", "knowledge.json")); err != nil {
		t.Fatal(err)
	}
	_, hosts := EnvTargets(dir, m)
	if !strings.Contains(strings.Join(hosts, ","), "fresh") {
		t.Errorf("hosts %v", hosts)
	}
	if err := SetEnvOverrides(dir, "fresh", map[string]string{"A": "1"}, nil, false); err != nil {
		t.Error(err)
	}
}

// Overrides stored with --allow-secret are redacted in every log path.
func TestLogRedactionPathsCoverOverrideSecrets(t *testing.T) {
	dir := envInstance(t)
	const secret = "sk-live-0123456789"
	if err := SetEnvOverrides(dir, "web", map[string]string{"STRIPE_KEY": secret}, nil, true); err != nil {
		t.Fatal(err)
	}
	m, _ := state.ReadManifest(filepath.Join(dir, "manifest.json"))
	m.Secrets = nil // only the override is secret here
	text := "token " + secret + " end"
	redactWith := func(name string, w func(io.Writer) io.WriteCloser) {
		var b strings.Builder
		c := w(&b)
		c.Write([]byte(text))
		c.Close()
		if strings.Contains(b.String(), secret) || !strings.Contains(b.String(), "[redacted:") {
			t.Errorf("%s: %q", name, b.String())
		}
	}
	redactWith("NewRedactedWriter", func(w io.Writer) io.WriteCloser { return NewRedactedWriter(w, dir, m) })
	secrets, err := LogSecrets(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	redactWith("LogSecrets", func(w io.Writer) io.WriteCloser { return redact.NewWriter(w, secrets) })
	redactWith("SecretsForRedaction", func(w io.Writer) io.WriteCloser { return redact.NewWriter(w, SecretsForRedaction(dir, m)) })
}
