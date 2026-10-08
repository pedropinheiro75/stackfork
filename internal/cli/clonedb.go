package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// cmdCloneDB is design.md D8's clone-db: copies a named Docker volume from
// another prepared instance into this one, with a throwaway alpine
// container (`tar` pipe) - for starting a new branch's instance from an
// existing one's data instead of an empty database.
func cmdCloneDB(stdout io.Writer, subArgs []string, root, project, instanceDir, newStateBase, legacyStateBase string) (int, error) {
	fs := newFlagSet(stdout, "clone-db")
	from := fs.String("from", "", "")
	fromDump := fs.String("from-dump", "", "")
	volumeFlag := fs.String("volume", "", "")
	serviceFlag := fs.String("service", "", "")
	databaseFlag := fs.String("database", "", "")
	replace := fs.Bool("replace", false, "")
	allowRunning := fs.Bool("allow-running", false, "")
	if err := fs.Parse(subArgs); err != nil {
		return parseFailure(fs, err)
	}
	if *from != "" && *fromDump != "" {
		return 2, errors.New("--from and --from-dump are mutually exclusive")
	}
	if *fromDump != "" {
		return cmdCloneDBFromDump(stdout, *fromDump, *serviceFlag, *databaseFlag, *replace, root, project, instanceDir)
	}
	if *from == "" {
		return 2, errors.New("--from or --from-dump is required")
	}
	if !identity.InstancePattern.MatchString(*from) {
		return 0, errors.New("Invalid instance name")
	}
	if *from == project {
		return 0, errors.New("--from must name a different instance")
	}

	targetInfo, err := state.ReadManifest(filepath.Join(instanceDir, "manifest.json"))
	if err != nil {
		return 0, err
	}
	if targetInfo == nil {
		return 0, errors.New("No prepared stack for this checkout/branch. Run prepare first.")
	}

	srcBase := newStateBase
	if identity.IsLegacyInstance(*from) {
		srcBase = legacyStateBase
	}
	srcDir := filepath.Join(srcBase, *from)
	srcInfo, err := state.ReadManifest(filepath.Join(srcDir, "manifest.json"))
	if err != nil {
		return 0, err
	}
	if srcInfo == nil {
		return 0, fmt.Errorf("no such instance %q", *from)
	}

	// Same project, by repo-relative file paths (design.md D8): the two
	// instances can be different checkouts/branches of the same repo (that
	// is the whole point - cloning data into a new branch's instance), so
	// their absolute Files/Root differ by design; what must match is which
	// Compose files they were prepared from, relative to each one's own
	// root.
	if !sameProjectFiles(targetInfo.Root, targetInfo.Files, srcInfo.Root, srcInfo.Files) {
		return 0, errors.New("--from names an instance of a different project (its Compose files don't match this one's, by repo-relative path)")
	}

	if !*allowRunning {
		running, err := inventory.RunningProjects()
		if err != nil {
			return 0, err
		}
		if running[*from] {
			return 0, errors.New("source instance is running; a copy of a running database is not consistent - pass --allow-running to override, or stop it first")
		}
	}

	targetModel, err := readComposeModel(instanceDir)
	if err != nil {
		return 0, err
	}
	srcModel, err := readComposeModel(srcDir)
	if err != nil {
		return 0, err
	}

	logicalName := *volumeFlag
	if logicalName == "" {
		logicalName, err = defaultDataVolume(srcModel)
		if err != nil {
			return 0, err
		}
	}

	srcVolume, ok := volumeDockerName(srcModel, logicalName)
	if !ok {
		return 0, fmt.Errorf("source instance has no volume named %q", logicalName)
	}
	dstVolume, ok := volumeDockerName(targetModel, logicalName)
	if !ok {
		return 0, fmt.Errorf("this instance has no volume named %q", logicalName)
	}

	empty, err := volumeIsEmpty(dstVolume)
	if err != nil {
		return 0, err
	}
	if !empty && !*replace {
		return 0, fmt.Errorf("target volume %q is not empty; pass --replace to overwrite it", dstVolume)
	}

	if err := copyVolume(srcVolume, dstVolume); err != nil {
		return 0, err
	}
	fmt.Fprintf(stdout, "copied %s (%s) into %s (%s)\n", *from, srcVolume, project, dstVolume)
	setSeeded(instanceDir, boolPtr(true)) // data came from elsewhere: never seed it
	return 0, nil
}

// sameProjectFiles compares each instance's Files relative to its own
// Root, so two different checkouts of the same repo (or two worktrees of
// it, on different branches) compare equal even though their absolute
// paths differ.
func sameProjectFiles(rootA string, filesA []string, rootB string, filesB []string) bool {
	rel := func(root string, files []string) []string {
		out := make([]string, 0, len(files))
		for _, f := range files {
			r, err := filepath.Rel(root, f)
			if err != nil {
				r = f
			}
			out = append(out, filepath.ToSlash(r))
		}
		sort.Strings(out)
		return out
	}
	a, b := rel(rootA, filesA), rel(rootB, filesB)
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readComposeModel(instanceDir string) (map[string]interface{}, error) {
	data, err := os.ReadFile(filepath.Join(instanceDir, "compose.json"))
	if err != nil {
		return nil, err
	}
	v, err := compose.DecodeJSON(data)
	if err != nil {
		return nil, err
	}
	m, _ := v.(map[string]interface{})
	if m == nil {
		return nil, errors.New("compose.json has an unexpected shape")
	}
	return m, nil
}

// dataMountPaths is D8's "known database data path" fallback, one target
// path per well-known engine.
var dataMountPaths = []string{
	"/var/lib/postgresql/data", "/var/lib/mysql", "/data/db", "/bitnami/postgresql",
}

// defaultDataVolume picks the volume clone-db copies when --volume isn't
// given: a service's io.stackfork.data label naming it, else the single
// volume mounted at a known database data path, else an error asking for
// --volume explicitly (design.md D8).
func defaultDataVolume(model map[string]interface{}) (string, error) {
	services, _ := model["services"].(map[string]interface{})
	for _, raw := range services {
		svc, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		labels, _ := svc["labels"].(map[string]interface{})
		if v, ok := labels["io.stackfork.data"].(string); ok && v != "" {
			return v, nil
		}
	}

	var candidates []string
	for _, raw := range services {
		svc, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		vols, _ := svc["volumes"].([]interface{})
		for _, rv := range vols {
			v, ok := rv.(map[string]interface{})
			if !ok {
				continue
			}
			target, _ := v["target"].(string)
			source, _ := v["source"].(string)
			if source == "" {
				continue
			}
			for _, known := range dataMountPaths {
				if target == known {
					candidates = append(candidates, source)
				}
			}
		}
	}
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return "", errors.New("no io.stackfork.data label and no volume at a known database data path; pass --volume explicitly")
	default:
		return "", fmt.Errorf("multiple candidate data volumes (%s); pass --volume explicitly", strings.Join(candidates, ", "))
	}
}

// volumeDockerName reads the actual Docker volume name `docker compose
// config` already normalized into every volume entry (it fills in "name"
// even when the source Compose file didn't set one), so this never has to
// re-derive Compose's own <project>_<logical> convention.
func volumeDockerName(model map[string]interface{}, logical string) (string, bool) {
	volumes, _ := model["volumes"].(map[string]interface{})
	v, ok := volumes[logical].(map[string]interface{})
	if !ok {
		return "", false
	}
	name, ok := v["name"].(string)
	return name, ok
}

// volumeIsEmpty and copyVolume both use a throwaway alpine container
// (design.md D8) rather than touching the volumes from the host - neither
// this process nor Docker Desktop's VM necessarily has a usable path into
// a named volume's data any other way.
func volumeIsEmpty(volume string) (bool, error) {
	out, err := exec.Command("docker", "run", "--rm", "-v", volume+":/v:ro", "alpine", "sh", "-c", "ls -A /v").Output()
	if err != nil {
		return false, fmt.Errorf("checking volume %q: %w", volume, err)
	}
	return len(strings.TrimSpace(string(out))) == 0, nil
}

func copyVolume(src, dst string) error {
	script := "rm -rf /to/* /to/.[!.]* /to/..?* 2>/dev/null; cd /from && tar cf - . | (cd /to && tar xf -)"
	cmd := exec.Command("docker", "run", "--rm",
		"-v", src+":/from:ro", "-v", dst+":/to", "alpine", "sh", "-c", script)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copying %s -> %s: %s", src, dst, strings.TrimSpace(out.String()))
	}
	return nil
}
