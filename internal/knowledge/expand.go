package knowledge

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ServiceAddr is a Compose service's host-published address: the URL and
// port `env` and `urls` show.
type ServiceAddr struct {
	URL  string
	Port int
}

// Scope is what the placeholders of a host command expand to, for one
// instance. Self is the host app the command starts ("" for an exec step).
// Apps maps every host app of the plan to its port (0: none allocated).
type Scope struct {
	Self     string
	Hostname string
	Apps     map[string]int
	Services map[string]ServiceAddr
}

var (
	// placeholderRE is the whole grammar. Anything else in braces (a Go
	// template, Helm, docker --format) is not ours and stays as written.
	placeholderRE = regexp.MustCompile(`\{\{\s*(port|url|hostname|service\.[A-Za-z0-9_-]+\.(?:port|url)|[a-z0-9][a-z0-9-]*\.(?:port|url))\s*\}\}`)
	// anyNameRE is a bare word in braces: a typo of the grammar, or not ours.
	anyNameRE  = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_][A-Za-z0-9_.-]*)\s*\}\}`)
	hostnameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.localhost$`)
)

// Expand replaces the placeholders in each argument: {{port}}, {{url}},
// {{hostname}}, {{APP.port}}, {{APP.url}}, {{service.NAME.port}} and
// {{service.NAME.url}}. It runs on an argument vector, after SplitArgv, and
// no shell sees the result. One pass: a substituted value is never scanned
// again, and a value stays one argument whatever characters it has. The
// hostname is checked against the .localhost label charset. An unknown placeholder, or an app with no
// port, is an error that names it; then nothing is returned.
func (s Scope) Expand(argv []string) ([]string, error) {
	out := make([]string, len(argv))
	var err error
	for i, a := range argv {
		out[i] = placeholderRE.ReplaceAllStringFunc(a, func(m string) string {
			v, e := s.value(placeholderRE.FindStringSubmatch(m)[1])
			if e == nil && (strings.IndexFunc(v, unsafeRune) >= 0 || strings.HasPrefix(v, "-")) {
				e = errors.New("the value has whitespace, a control character or a leading '-', which a command argument must not get from a placeholder")
			}
			if e != nil {
				if err == nil {
					err = fmt.Errorf("%s: %w", m, e)
				}
				return m
			}
			return v
		})
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func unsafeRune(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }

func (s Scope) value(name string) (string, error) {
	host := func() (string, error) {
		if !hostnameRE.MatchString(s.Hostname) {
			return "", fmt.Errorf("the instance hostname %q is not a valid .localhost name", s.Hostname)
		}
		return s.Hostname, nil
	}
	appValue := func(app, field string) (string, error) {
		port, known := s.Apps[app]
		switch {
		case app == "":
			return "", fmt.Errorf("there is no host app here; name one, as in {{APP.%s}}", field)
		case !known:
			return "", fmt.Errorf("unknown placeholder: %q is not a host app of this plan", app)
		case port == 0:
			return "", fmt.Errorf("host app %q has no allocated port", app)
		case field == "port":
			return strconv.Itoa(port), nil
		}
		h, err := host()
		return "http://" + h + ":" + strconv.Itoa(port), err
	}
	switch name {
	case "hostname":
		return host()
	case "port", "url":
		return appValue(s.Self, name)
	}
	if rest, ok := strings.CutPrefix(name, "service."); ok {
		i := strings.LastIndex(rest, ".")
		if i > 0 {
			svc, field := rest[:i], rest[i+1:]
			a, known := s.Services[svc]
			switch {
			case !known || (a.Port == 0 && a.URL == ""):
				return "", fmt.Errorf("unknown placeholder: service %q has no published address", svc)
			case field == "port":
				return strconv.Itoa(a.Port), nil
			case field == "url":
				return a.URL, nil
			}
		}
		return "", fmt.Errorf("unknown placeholder: expected service.NAME.port or service.NAME.url")
	}
	if app, field, ok := strings.Cut(name, "."); ok && (field == "port" || field == "url") && app != "" {
		return appValue(app, field)
	}
	return "", fmt.Errorf("unknown placeholder")
}

// nearMisses lists the bare-word placeholders in cmd the grammar does not
// know ({{prot}}, {{app.prot}}): they stay literal, so a typo is worth a warning.
func nearMisses(cmd string) []string {
	var out []string
	for _, m := range anyNameRE.FindAllString(cmd, -1) {
		if !placeholderRE.MatchString(m) {
			out = append(out, m)
		}
	}
	return out
}

// HasPlaceholder reports whether cmd has a {{...}} placeholder.
func HasPlaceholder(cmd string) bool { return placeholderRE.MatchString(cmd) }

var literalPortRE = regexp.MustCompile(`(?:--port[= ]|:)(\d+)\b`)

// HostLiteralWarnings lists what in a host step or host exec step fixes a
// value the placeholders give per instance: a literal port:, a port in cmd
// (--port N, :N) or the instance hostname. At most one warning per step. It
// warns, never refuses: a fixed port is legal.
func HostLiteralWarnings(s Step, hostname string) []string {
	if s.Kind != KindHost && !s.IsHostExec() {
		return nil
	}
	var found []string
	if s.Port != 0 {
		found = append(found, "port: "+strconv.Itoa(s.Port))
	}
	for _, m := range literalPortRE.FindAllStringSubmatch(s.Cmd, -1) {
		found = append(found, "port "+m[1]+" in cmd")
	}
	if hostname != "" && strings.Contains(s.Cmd, hostname) {
		found = append(found, "the hostname "+hostname+" in cmd")
	}
	var out []string
	for _, m := range nearMisses(s.Cmd) {
		out = append(out, fmt.Sprintf("step %s: %s does not expand (not a placeholder: port, url, hostname, APP.port, APP.url, service.NAME.port, service.NAME.url); it stays as written", s.ID, m))
	}
	if len(found) == 0 {
		return out
	}
	return append(out, fmt.Sprintf("step %s fixes %s; another worktree would collide or differ. Use {{port}}, {{url}}, {{hostname}}, {{APP.port}} or {{APP.url}} (see references/learning.md)", s.ID, strings.Join(found, ", ")))
}

// UsesSelfPort reports whether argv uses {{port}} or {{url}}: the app is
// given a port to listen on, so a start that never listens fails.
func UsesSelfPort(argv []string) bool {
	for _, a := range argv {
		for _, m := range placeholderRE.FindAllStringSubmatch(a, -1) {
			if m[1] == "port" || m[1] == "url" {
				return true
			}
		}
	}
	return false
}
