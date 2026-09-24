package compose

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/identity"
)

// StatusRow mirrors the public subset of a `docker compose ps` row that
// Python's public_status() keeps.
type StatusRow struct {
	ID         string                   `json:"ID"`
	Name       string                   `json:"Name"`
	Service    string                   `json:"Service"`
	State      string                   `json:"State"`
	Health     string                   `json:"Health"`
	Publishers []map[string]interface{} `json:"Publishers"`
}

// PublicStatus mirrors Python's public_status(output): parses `docker
// compose ps --format json` output (a JSON array or JSON Lines, Compose has
// used both across versions) and strips everything except a public subset
// of fields, dropping unpublished ports.
func PublicStatus(output string) ([]StatusRow, error) {
	var raw []interface{}
	trimmed := strings.TrimLeft(output, " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		v, err := DecodeJSON([]byte(output))
		if err != nil {
			return nil, err
		}
		list, _ := asList(v)
		raw = list
	} else {
		for _, line := range strings.Split(output, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			v, err := DecodeJSON([]byte(line))
			if err != nil {
				return nil, err
			}
			raw = append(raw, v)
		}
	}

	rows := make([]StatusRow, 0, len(raw))
	for _, item := range raw {
		row, ok := asMap(item)
		if !ok {
			continue
		}
		out := StatusRow{
			ID:      stringField(row, "ID"),
			Name:    stringField(row, "Name"),
			Service: stringField(row, "Service"),
			State:   stringField(row, "State"),
			Health:  stringField(row, "Health"),
		}
		if publishers, ok := asList(row["Publishers"]); ok {
			for _, p := range publishers {
				pub, ok := asMap(p)
				if !ok {
					continue
				}
				if truthy(pub["PublishedPort"]) {
					out.Publishers = append(out.Publishers, pub)
				}
			}
		}
		rows = append(rows, out)
	}
	return rows, nil
}

func stringField(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// EndpointLines mirrors Python's endpoint_lines(rows, hostname), extended by
// task 7.2 with the HTTP/HTTPS target ports a service declared at prepare
// (design.md D7): a declared port wins over the fixed-port guess below,
// which stays as the fallback for services that declare nothing.
func EndpointLines(rows []StatusRow, hostname string, httpPorts, httpsPorts []int) []string {
	var lines []string
	for _, row := range rows {
		for _, port := range row.Publishers {
			target := port["TargetPort"]
			protocol, _ := port["Protocol"].(string)
			if protocol == "" {
				protocol = "tcp"
			}
			var scheme string
			switch {
			case isTargetIn(target, httpsPorts):
				scheme = "https"
			case isTargetIn(target, httpPorts):
				scheme = "http"
			case isTarget(target, 443):
				scheme = "https"
			case isTarget(target, 80), isTarget(target, 3000), isTarget(target, 8080), isTarget(target, 5050):
				scheme = "http"
			default:
				scheme = protocol
			}
			if protocol != "tcp" {
				scheme = protocol
			}
			host := "127.0.0.1"
			if scheme == "http" || scheme == "https" {
				host = hostname
			}
			lines = append(lines, fmt.Sprintf("%s: %s://%s:%v (%v/%s)",
				row.Service, scheme, host, port["PublishedPort"], target, protocol))
		}
	}
	return lines
}

// DeclaredHTTPPorts scans an isolated model's services for the
// io.stackfork.http / io.stackfork.https labels (task 7.2, design.md D7)
// and returns the union of declared target ports, sorted. Call it at
// prepare, on the model Isolate returned, and store the result in the
// manifest - urls reads it back rather than re-parsing the snapshot.
func DeclaredHTTPPorts(model interface{}) (httpPorts, httpsPorts []int) {
	m, ok := asMap(model)
	if !ok {
		return nil, nil
	}
	services, _ := asMap(m["services"])
	httpSet := map[int]bool{}
	httpsSet := map[int]bool{}
	for _, raw := range services {
		svc, ok := asMap(raw)
		if !ok {
			continue
		}
		labels, ok := asMap(svc["labels"])
		if !ok {
			continue
		}
		collectPorts(labels[identity.LabelHTTP], httpSet)
		collectPorts(labels[identity.LabelHTTPS], httpsSet)
	}
	return sortedPortSet(httpSet), sortedPortSet(httpsSet)
}

func collectPorts(v interface{}, into map[int]bool) {
	s, ok := v.(string)
	if !ok || s == "" {
		return
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		into[n] = true
	}
}

func sortedPortSet(set map[int]bool) []int {
	if len(set) == 0 {
		return nil
	}
	out := make([]int, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

func isTarget(v interface{}, n int64) bool {
	num, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := num.Int64()
	return err == nil && i == n
}

// isTargetIn reports whether v (a TargetPort) is one of ports.
func isTargetIn(v interface{}, ports []int) bool {
	num, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := num.Int64()
	if err != nil {
		return false
	}
	for _, p := range ports {
		if int64(p) == i {
			return true
		}
	}
	return false
}
