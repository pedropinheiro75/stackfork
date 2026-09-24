package cli

import "strings"

// stringList is a repeatable flag value, mirroring argparse's action='append'.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
