package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/pedropinheiro75/stackfork/internal/config"
)

// cmdConfig is `stackfork config [get|add-root|remove-root|add-exclude|
// remove-exclude|set]`: the same edits the dashboard's Settings panel makes.
func cmdConfig(stdout io.Writer, subArgs []string) (int, error) {
	if isHelpRequest(subArgs) {
		printFlaglessUsage(stdout, "config", "config [get | add-root PATH | remove-root PATH | add-exclude NAME | remove-exclude NAME | set max-depth N | set roots PATH... | set exclude NAME...]")
		return 0, nil
	}
	c, err := config.Load()
	if err != nil {
		return 0, err
	}
	verb := "get"
	if len(subArgs) > 0 {
		verb, subArgs = subArgs[0], subArgs[1:]
	}
	one := func() (string, error) {
		if len(subArgs) != 1 {
			return "", fmt.Errorf("config %s takes exactly one argument", verb)
		}
		return subArgs[0], nil
	}
	switch verb {
	case "get":
		path, _ := config.Path()
		return 0, printJSON(stdout, map[string]interface{}{
			"path": path, "roots": nonNilStrings(c.Roots), "exclude": nonNilStrings(c.Exclude), "maxDepth": c.Depth(),
		})
	case "add-root", "remove-root", "add-exclude", "remove-exclude":
		arg, err := one()
		if err != nil {
			return 2, err
		}
		switch verb {
		case "add-root":
			_, err = c.AddRoot(arg)
		case "remove-root":
			err = c.RemoveRoot(arg)
		case "add-exclude":
			err = c.AddExclude(arg)
		default:
			err = c.RemoveExclude(arg)
		}
		if err != nil {
			return 0, err
		}
	case "set":
		if len(subArgs) < 1 {
			return 2, errors.New("config set KEY VALUE...")
		}
		key, vals := subArgs[0], subArgs[1:]
		switch key {
		case "max-depth":
			n := 0
			if len(vals) == 1 {
				n, _ = strconv.Atoi(vals[0])
			}
			if err := c.SetMaxDepth(n); err != nil {
				return 0, err
			}
		case "roots":
			c.Roots = nil
			for _, v := range vals {
				if _, err := c.AddRoot(v); err != nil {
					return 0, err
				}
			}
		case "exclude":
			c.Exclude = nil
			for _, v := range vals {
				if err := c.AddExclude(v); err != nil {
					return 0, err
				}
			}
		default:
			return 2, fmt.Errorf("unknown config key %q (roots, exclude, max-depth)", key)
		}
	default:
		return 2, fmt.Errorf("unknown config command %q", verb)
	}
	if err := c.Save(); err != nil {
		return 0, err
	}
	fmt.Fprintln(stdout, "saved")
	return 0, nil
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
