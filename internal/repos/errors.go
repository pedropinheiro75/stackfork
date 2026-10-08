package repos

import "fmt"

func errNotFound(arg string) error {
	return fmt.Errorf("no repo %q under the configured roots (see `stackfork repos`)", arg)
}
func errAmbiguous(arg, paths string) error {
	return fmt.Errorf("%q matches several repos; use the full path: %s", arg, paths)
}
