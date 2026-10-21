package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

// The host exec tests run this test binary as the "project command": with
// STACKFORK_TEST_HELPER=1 it prints its argv, working directory and a few
// environment variables, reports whether stdin is closed, and exits with
// STACKFORK_TEST_HELPER_EXIT. It never touches anything else.
func init() {
	if os.Getenv("STACKFORK_TEST_HELPER") != "1" {
		return
	}
	argv, _ := json.Marshal(os.Args[1:])
	wd, _ := os.Getwd()
	fmt.Printf("argv=%s\ncwd=%s\n", argv, wd)
	for _, k := range []string{"DATABASE_URL", "STACKFORK_HOST_RUN", "PORT", "HELPER_PRINT"} {
		fmt.Printf("%s=%s\n", k, os.Getenv(k))
	}
	if v := os.Getenv("HELPER_PRINT"); v != "" {
		fmt.Fprintln(os.Stderr, "stderr "+v)
	}
	_, err := os.Stdin.Read(make([]byte, 1))
	fmt.Printf("stdin=%v\n", err == io.EOF || err != nil)
	code, _ := strconv.Atoi(os.Getenv("STACKFORK_TEST_HELPER_EXIT"))
	os.Exit(code)
}
