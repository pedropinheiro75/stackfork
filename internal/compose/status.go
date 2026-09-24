package compose

import (
	"encoding/json"
	"fmt"
	"strings"
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

// EndpointLines mirrors Python's endpoint_lines(rows, hostname).
func EndpointLines(rows []StatusRow, hostname string) []string {
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

func isTarget(v interface{}, n int64) bool {
	num, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := num.Int64()
	return err == nil && i == n
}
