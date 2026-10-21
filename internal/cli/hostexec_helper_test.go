package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// The host exec tests run this test binary as the "project command": with
// STACKFORK_TEST_HELPER=1 it prints its argv, working directory and a few
// environment variables, reports whether stdin is closed, and exits with
// STACKFORK_TEST_HELPER_EXIT. It never touches anything else.
func init() {
	if os.Getenv("STACKFORK_TEST_HELPER") != "1" {
		return
	}
	if os.Getenv("HELPER_SLEEPER") == "1" { // the grandchild: holds the inherited stdout for a while
		time.Sleep(4 * time.Second)
		os.Exit(0)
	}
	if os.Getenv("HELPER_BACKGROUND") == "1" { // starts a sleeper that keeps stdout open, then exits
		c := exec.Command(os.Args[0])
		c.Env = append(os.Environ(), "HELPER_SLEEPER=1")
		c.Stdout = os.Stdout
		c.Start()
		fmt.Println("parent done")
		os.Exit(0)
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
	if os.Getenv("HELPER_DUMP_ENV") == "1" {
		fmt.Printf("env=%q\n", os.Environ())
	}
	n, err := os.Stdin.Read(make([]byte, 1))
	fmt.Printf("stdin=%v\n", n == 0 && err == io.EOF) // closed: nothing to read, at once
	code, _ := strconv.Atoi(os.Getenv("STACKFORK_TEST_HELPER_EXIT"))
	os.Exit(code)
}
