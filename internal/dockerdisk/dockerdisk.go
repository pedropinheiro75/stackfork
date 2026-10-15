// Package dockerdisk measures the disk that holds Docker's data (the VM's
// disk on macOS) and frees the space nothing can miss: build cache,
// dangling images and anonymous volumes no container uses. Named volumes,
// where databases live, are never touched.
package dockerdisk

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// WarnPercent is the usage at which doctor and the dashboard warn.
const WarnPercent = 85

// Usage is the Docker data filesystem as `df` sees it from a container.
type Usage struct {
	SizeKB, AvailKB int64
	Percent         int
}

func (u Usage) Full() bool { return u.Percent >= WarnPercent }

// String reads like "74% used, 15.2 GB free of 63.6 GB".
func (u Usage) String() string {
	return fmt.Sprintf("%d%% used, %s free of %s", u.Percent, gb(u.AvailKB), gb(u.SizeKB))
}

func gb(kb int64) string { return fmt.Sprintf("%.1f GB", float64(kb)/1e6) }

// Measure runs `df` on the root of a throwaway container: its overlay root
// reports the filesystem under /var/lib/docker.
// ponytail: one short container per call; callers cache the result.
func Measure(ctx context.Context) (Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none",
		"alpine:3.20", "df", "-Pk", "/").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "no space left") {
			return Usage{Percent: 100}, nil
		}
		return Usage{}, fmt.Errorf("docker run df: %s", msg)
	}
	return parseDF(string(out))
}

func parseDF(out string) (Usage, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 5 {
		return Usage{}, fmt.Errorf("unexpected df output: %q", out)
	}
	size, err1 := strconv.ParseInt(f[1], 10, 64)
	avail, err2 := strconv.ParseInt(f[3], 10, 64)
	pct, err3 := strconv.Atoi(strings.TrimSuffix(f[4], "%"))
	if err1 != nil || err2 != nil || err3 != nil {
		return Usage{}, fmt.Errorf("unexpected df output: %q", out)
	}
	return Usage{SizeKB: size, AvailKB: avail, Percent: pct}, nil
}

// anonymous matches the 64-hex names Docker gives anonymous volumes.
var anonymous = regexp.MustCompile(`^[0-9a-f]{64}$`)

// AnonymousVolumes keeps the dangling volumes with anonymous names, so a
// named volume (a database) is never in the list, whatever the Docker
// version's `volume prune` default is.
func AnonymousVolumes(dangling []string) []string {
	var out []string
	for _, v := range dangling {
		if anonymous.MatchString(v) {
			out = append(out, v)
		}
	}
	return out
}

// Free removes the build cache, dangling images and unused anonymous
// volumes, and returns one line per step.
func Free(ctx context.Context) ([]string, error) {
	var report []string
	run := func(args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("docker %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
		}
		return string(out), nil
	}
	reclaimed := func(out string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.ToLower(l), "total") {
				return strings.TrimSpace(l)
			}
		}
		return "nothing to reclaim"
	}
	out, err := run("builder", "prune", "-f")
	if err != nil {
		return report, err
	}
	report = append(report, "build cache: "+reclaimed(out))
	if out, err = run("image", "prune", "-f"); err != nil {
		return report, err
	}
	report = append(report, "dangling images: "+reclaimed(out))
	if out, err = run("volume", "ls", "-q", "--filter", "dangling=true"); err != nil {
		return report, err
	}
	vols := AnonymousVolumes(strings.Fields(out))
	if len(vols) > 0 {
		if _, err := run(append([]string{"volume", "rm"}, vols...)...); err != nil {
			return report, err
		}
	}
	report = append(report, fmt.Sprintf("anonymous volumes no container uses: %d removed (named volumes kept)", len(vols)))
	return report, nil
}
