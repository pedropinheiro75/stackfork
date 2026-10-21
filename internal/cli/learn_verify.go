package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// probeTimeout is how long `learn verify` waits for the entry to answer.
var probeTimeout = 10 * time.Second

// linkTimeout bounds each link probe, so the worst case of verify stays small.
var linkTimeout = 3 * time.Second

// probeEntryAndLinks is the entry-links D8 gate of `learn verify`: it
// returns what verification records (the probed target, one result per
// link) and the link warnings. An absent or proposed entry, a stopped
// instance and an entry that does not answer are refusals.
func probeEntryAndLinks(root, newStateBase, legacyStateBase string, k *knowledge.Knowledge) (entry string, links []knowledge.LinkCheck, warnings []string, err error) {
	if k.Entry == nil || k.Entry.Proposed {
		return "", nil, nil, errors.New("record the app's entry first: stackfork learn record entry --service S --port N (or --none)")
	}
	if k.Entry.None {
		return "none", nil, nil, nil
	}
	info, model, rows, hostname, err := verifyInstance(root, newStateBase, legacyStateBase)
	if err != nil {
		return "", nil, nil, err
	}
	r := knowledge.ResolveEntry(k, model, info.Routes)
	where := knowledge.Where{Hostname: hostname, Rows: rows}
	label := fmt.Sprintf("%s:%d", k.Entry.Service, k.Entry.Port)
	if k.Entry.Path != "" && k.Entry.Path != "/" {
		label += k.Entry.Path
	}
	if r.Source == knowledge.SourceHostRoute {
		label = "host-route " + label
	}
	if !r.OK {
		return "", nil, nil, errors.New(r.Warning)
	}
	appURL, _ := r.AppURL(where)
	if appURL == "" {
		return "", nil, nil, fmt.Errorf("the entry %s has no published port yet; start the instance with stackfork up", label)
	}
	status, perr := probe(appURL, probeTimeout)
	if perr != nil || status >= 500 {
		return "", nil, nil, fmt.Errorf("the entry %s did not answer (%s); check the service, or record the right entry", label, probeWhy(status, perr))
	}
	for _, l := range r.Links {
		check := knowledge.LinkCheck{Name: l.Name}
		linkURL, _ := r.LinkURL(l, where)
		switch {
		case !l.OK:
			warnings = append(warnings, l.Warning)
		case linkURL == "":
			warnings = append(warnings, fmt.Sprintf("link %q has no published port yet", l.Name))
		default:
			st, lerr := probe(linkURL, linkTimeout)
			check.Status = st
			if check.Answered = lerr == nil && st < 500; !check.Answered {
				warnings = append(warnings, fmt.Sprintf("link %q did not answer (%s)", l.Name, probeWhy(st, lerr)))
			}
		}
		links = append(links, check)
	}
	return label, links, warnings, nil
}

func probeWhy(status int, err error) string {
	if err != nil {
		return "no connection"
	}
	return fmt.Sprintf("status %d", status)
}

// verifyInstance finds the checkout's instance and requires it to run.
func verifyInstance(root, newStateBase, legacyStateBase string) (info *state.Manifest, model interface{}, rows []compose.StatusRow, hostname string, err error) {
	noStack := errors.New("no prepared stack for this checkout; run stackfork prepare and stackfork up, then verify")
	toplevel, err := gitToplevel(root)
	if err != nil {
		return nil, nil, nil, "", noStack
	}
	top, err := identity.ResolvePath(toplevel)
	if err != nil {
		return nil, nil, nil, "", err
	}
	branch, err := currentBranch(top)
	if err != nil {
		return nil, nil, nil, "", err
	}
	project, instanceDir, err := checkoutInstance(top, branch, newStateBase, legacyStateBase)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if info, err = state.ReadManifest(filepath.Join(instanceDir, "manifest.json")); err != nil || info == nil {
		return nil, nil, nil, "", noStack
	}
	data, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		return nil, nil, nil, "", noStack
	}
	if model, err = compose.DecodeJSON(data); err != nil {
		return nil, nil, nil, "", err
	}
	if err := lifecycle.LocalDocker(); err != nil {
		return nil, nil, nil, "", err
	}
	if rows, err = composePs(top, project, filepath.Join(instanceDir, "compose.json")); err != nil {
		return nil, nil, nil, "", err
	}
	up := false
	for _, row := range rows {
		up = up || strings.EqualFold(row.State, "running")
	}
	if !up {
		return nil, nil, nil, "", errors.New("the instance is not running; start it with stackfork up, then verify")
	}
	if hostname, err = identity.BrowserHostname(project, info.Hostname); err != nil {
		return nil, nil, nil, "", err
	}
	return info, model, rows, hostname, nil
}

// composePs lists an instance's containers, running or not.
func composePs(root, project, configPath string) ([]compose.StatusRow, error) {
	cmd := exec.Command("docker", "compose", "-p", project, "--project-directory", root,
		"--env-file", os.DevNull, "-f", configPath, "--profile", "*", "ps", "--all", "--format", "json")
	cmd.Env = compose.DockerEnv(nil)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, errors.New("docker compose ps failed; is Docker running?")
	}
	return compose.PublicStatus(out.String())
}

// probe sends one GET to rawURL. The connection always goes to 127.0.0.1 on
// the URL's port, with the instance hostname in Host, so it does not depend
// on how the system resolves *.localhost. Redirects are not followed and
// certificates are not checked: the target is a loopback port of the
// instance (entry-links D8).
func probe(rawURL string, timeout time.Duration) (int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, err
	}
	loopback := net.JoinHostPort("127.0.0.1", u.Port())
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, loopback)
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: u.Hostname()},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
