package learn

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/knowledge"
)

var (
	scriptPortFlag = regexp.MustCompile(`--port[= ]\s*(\d{2,5})\b`)
	scriptPortEnv  = regexp.MustCompile(`\bPORT=(\d{2,5})\b`)
	configPort     = regexp.MustCompile(`\bport\s*:\s*(\d{2,5})\b`)
)

// detectDevPort reads the dev server port from the dev script text and from
// nuxt/vite/next config files by regular expression; 0 when unknown.
// Nothing is executed.
func detectDevPort(root, devScript string) int {
	for _, re := range []*regexp.Regexp{scriptPortFlag, scriptPortEnv} {
		if m := re.FindStringSubmatch(devScript); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	for _, base := range []string{"nuxt", "vite", "next"} {
		matches, _ := filepath.Glob(filepath.Join(root, base+".config.*"))
		for _, f := range matches {
			data, ok := readFileNoSymlink(f)
			if !ok {
				continue
			}
			if m := configPort.FindSubmatch(data); m != nil {
				n, _ := strconv.Atoi(string(m[1]))
				return n
			}
		}
	}
	return 0
}

// appService runs compose.PrimaryHTTPService over the scanned services'
// ports (labels are not in the scan facts, so the port guess decides).
func appService(k *knowledge.Knowledge) string {
	services := map[string]interface{}{}
	for _, s := range k.Facts.Services {
		var ports []interface{}
		for _, p := range s.Ports {
			target := p[strings.LastIndex(p, ":")+1:]
			ports = append(ports, map[string]interface{}{"target": json.Number(target)})
		}
		services[s.Name] = map[string]interface{}{"ports": ports}
	}
	name, _, _, ok := compose.PrimaryHTTPService(map[string]interface{}{"services": services})
	if !ok {
		return ""
	}
	return name
}

// ProposePlan builds the proposed startup plan from the scan facts. The
// exec steps come from DetectSeed, so recording the proposal changes nothing
// about which migrate and seed commands run.
func ProposePlan(root string, k *knowledge.Knowledge) knowledge.Plan {
	p := knowledge.Plan{Modes: []string{knowledge.ModeContainer}, Status: knowledge.PlanProposed}

	for _, dir := range []string{".stackfork", ".agent-dev"} {
		if m, _ := filepath.Glob(filepath.Join(root, dir, "*.compose.yaml")); len(m) > 0 {
			p.Steps = append(p.Steps, knowledge.Step{ID: "prepare", Kind: knowledge.KindPrepare,
				Args: []string{"-f", dir + "/" + filepath.Base(m[0])}})
			break
		}
	}

	var devName, devCmd string
	for _, s := range k.Facts.Scripts {
		if s.Role == "dev" && s.Source == "package.json" && (devName == "" || s.Name == "dev") {
			devName, devCmd = s.Name, s.Command
		}
	}
	p.AppService = appService(k)

	up := knowledge.Step{ID: "up", Kind: knowledge.KindComposeUp}
	if devName != "" {
		up.Modes = []string{knowledge.ModeContainer}
	}
	p.Steps = append(p.Steps, up)
	if devName != "" {
		hy := knowledge.Step{ID: "up-hybrid", Kind: knowledge.KindComposeUp, Modes: []string{knowledge.ModeHybrid}}
		if p.AppService != "" {
			hy.Exclude = []string{p.AppService}
		}
		p.Steps = append(p.Steps, hy)
	}

	migrate, seed := DetectSeed(root)
	if migrate != "" {
		p.Steps = append(p.Steps, knowledge.Step{ID: "migrate", Kind: knowledge.KindExec, Cmd: migrate, Once: knowledge.OnceSchema})
	}
	if seed != "" {
		p.Steps = append(p.Steps, knowledge.Step{ID: "seed", Kind: knowledge.KindExec, Cmd: seed, Once: knowledge.OnceData})
	}

	if devName != "" {
		mgr, _, _ := detectPackageManager(root)
		if mgr == "" {
			mgr = "npm"
		}
		p.Modes = append(p.Modes, knowledge.ModeHybrid)
		p.Steps = append(p.Steps, knowledge.Step{ID: "app", Kind: knowledge.KindHost, Modes: []string{knowledge.ModeHybrid},
			Name: "app", Cmd: mgr + " run " + devName, PortEnv: []string{"PORT"}})
		if port := detectDevPort(root, devCmd); port > 0 {
			p.Steps = append(p.Steps, knowledge.Step{ID: "dev-port", Kind: knowledge.KindNote,
				Text: fmt.Sprintf("the dev script listens on port %d", port)})
		}
	}
	return p
}
