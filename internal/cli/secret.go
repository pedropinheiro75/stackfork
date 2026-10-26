package cli

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/config"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/lifecycle"
	"github.com/pedropinheiro75/stackfork/internal/repos"
	"github.com/pedropinheiro75/stackfork/internal/secretstore"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

func init() {
	// The store tools must not live in a scanned repository (design D2). The
	// secretstore package cannot import config, so the CLI wires the roots.
	secretstore.Default.ScannedRoots = func() []string {
		if cfg, err := config.Load(); err == nil {
			return cfg.ResolvedRoots()
		}
		return nil
	}
}

const secretUsage = "secret ls [REPO] [--json] | secret check [REPO] [--json] | secret set ITEM [--store keychain] | secret map REPO|FOLDER NAME=REF | secret unmap REPO|FOLDER NAME | secret forget NAME [CHECKOUT]"

// secretStdin is where `secret set` reads a value from; tests replace it.
var secretStdin io.Reader = os.Stdin

// secretIsTerminal reports whether r is a terminal; tests replace it.
var secretIsTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// secretSetEcho switches the terminal echo with stty; tests replace it.
var secretSetEcho = func(on bool) error {
	if runtime.GOOS == "windows" {
		return winEcho(on)
	}
	arg := "-echo"
	if on {
		arg = "echo"
	}
	for _, stty := range []string{"/bin/stty", "/usr/bin/stty"} { // fixed paths, as for the store tools
		if _, err := os.Stat(stty); err != nil {
			continue
		}
		cmd := exec.Command(stty, arg)
		cmd.Stdin = os.Stdin
		return cmd.Run()
	}
	return errors.New("stty not found; pipe the value in: printenv NAME | stackfork secret set ITEM")
}

// winRestore undoes WithoutEcho on the Windows console.
var winRestore func()

// winEcho is secretSetEcho on Windows: SetConsoleMode through secretstore.
func winEcho(on bool) error {
	if on {
		if winRestore != nil {
			winRestore()
			winRestore = nil
		}
		return nil
	}
	restore, err := secretstore.WithoutEcho(os.Stdin)
	if err != nil {
		return fmt.Errorf("%v; pipe the value in: printenv NAME | stackfork secret set ITEM", err)
	}
	winRestore = restore
	return nil
}

// cmdSecret is `stackfork secret ...`. It never prints a value and has no
// flag that takes one (design D10).
func cmdSecret(stdout, stderr io.Writer, subArgs []string, rootArg, newStateBase, legacyStateBase string, global []string) (int, error) {
	if len(subArgs) == 0 || isHelpRequest(subArgs) {
		printFlaglessUsage(stdout, "secret", secretUsage)
		if len(subArgs) == 0 {
			return 2, errors.New("secret needs a subcommand (ls, check, set, map, unmap or forget)")
		}
		return 0, nil
	}
	verb, rest := subArgs[0], subArgs[1:]
	switch verb {
	case "ls", "check":
		return secretList(stdout, verb, rest, rootArg, newStateBase, legacyStateBase, global)
	case "set":
		return secretSet(stdout, stderr, rest, rootArg)
	case "forget":
		return secretForget(stdout, rest, rootArg, newStateBase, legacyStateBase)
	case "map", "unmap":
		return secretMap(stdout, verb, rest, newStateBase, legacyStateBase)
	}
	return 2, fmt.Errorf("unknown secret subcommand %q (expected ls, check, set, map, unmap or forget)", verb)
}

// parseMixed parses fs over args, allowing flags after positionals.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, parseFailureErr(fs, err)
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// mainRepo is the main checkout of the repository path is in.
func mainRepo(path string) string {
	if m := repos.MainRepoOf(path); m != "" {
		return m
	}
	return path
}

// --- ls / check -----------------------------------------------------------

func secretList(stdout io.Writer, verb string, args []string, rootArg, newStateBase, legacyStateBase string, global []string) (int, error) {
	if err := checkSecretFlags(global); err != nil {
		return 2, err
	}
	fs := newFlagSet(stdout, "secret "+verb)
	asJSON := fs.Bool("json", false, "")
	pos, err := parseMixed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return 0, nil
	}
	if err != nil {
		return 2, err
	}
	if len(pos) > 1 {
		return 2, fmt.Errorf("usage: stackfork secret %s [REPO] [--json]", verb)
	}
	var repo string
	if len(pos) == 1 {
		repo, err = resolveRepoArg(pos[0], newStateBase, legacyStateBase)
	} else {
		var top string
		if top, err = gitToplevel(rootArg); err == nil {
			repo, err = identity.ResolvePath(top)
		}
	}
	if err != nil {
		return 0, err
	}
	root := repo
	repo = mainRepo(repo)
	rows, err := lifecycle.SecretRows(root, repo, verb == "check", nil, flagSecrets(global), newStateBase, legacyStateBase)
	if err != nil {
		return 0, err
	}
	if *asJSON {
		if err := printJSON(stdout, rows); err != nil {
			return 0, err
		}
	} else {
		printSecretRows(stdout, rows)
	}
	if verb == "check" {
		for _, r := range rows {
			if r.Status != string(secretstore.Resolves) {
				return 1, nil
			}
		}
	}
	return 0, nil
}

func printSecretRows(w io.Writer, rows []lifecycle.SecretRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "no secrets: none mapped on this machine, none declared by the project")
		return
	}
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tREFERENCE\tSOURCE\tSTATUS\tHINT")
	for _, r := range rows {
		ref := r.Reference
		if ref == "" {
			ref = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, ref, r.Source, r.Status, r.Hint)
	}
	tw.Flush()
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
}

// --- set ------------------------------------------------------------------

func secretSet(stdout, stderr io.Writer, args []string, rootArg string) (int, error) {
	fs := newFlagSet(stdout, "secret set")
	store := fs.String("store", "keychain", "")
	pos, err := parseMixed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return 0, nil
	}
	if err != nil {
		return 2, err
	}
	if *store != "keychain" {
		return 2, errors.New("--store: only keychain is supported (the OS credential store)")
	}
	if len(pos) != 1 {
		return 2, errors.New("usage: printenv NAME | stackfork secret set ITEM [--store keychain]; there is no value argument")
	}
	item := pos[0]
	if !secretstore.ValidItem(item) {
		return 2, errors.New("ITEM must be 1 to 128 characters: letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	value, err := readSecretValue(stderr, item)
	if err != nil {
		return 1, err
	}
	if err := secretstore.Store(item, value); err != nil {
		return 1, err
	}
	ref := "keychain:" + item
	fmt.Fprintf(stdout, "stored %s\n", ref)
	if !refMapped(ref) {
		repo := "REPO"
		if top, err := gitToplevel(rootArg); err == nil {
			if p, err := identity.ResolvePath(top); err == nil {
				repo = shellQuoteArg(mainRepo(p))
			}
		}
		fmt.Fprintf(stdout, "to use it: stackfork secret map %s NAME=%s\n", repo, ref)
	}
	return 0, nil
}

// refMapped reports whether any repository's machine map uses ref.
func refMapped(ref string) bool {
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	for _, names := range cfg.Secrets {
		for _, r := range names {
			if r == ref {
				return true
			}
		}
	}
	return false
}

func shellQuoteArg(s string) string {
	if strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+:@%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// readSecretValue reads the value from stdin when it is not a terminal, else
// from a prompt asked twice with echo off. The error never holds the value.
func readSecretValue(prompt io.Writer, item string) (string, error) {
	if !secretIsTerminal(secretStdin) {
		b, err := io.ReadAll(io.LimitReader(secretStdin, secretstore.MaxValueSize+3))
		if err != nil {
			return "", fmt.Errorf("reading the value: %w", err)
		}
		value := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r") // printenv and echo end with a newline
		return value, secretstore.ValidateValue(value)
	}
	if err := secretSetEcho(false); err != nil {
		return "", err
	}
	done := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { // a Ctrl-C at the prompt must not leave the terminal silent
		select {
		case <-sig:
			secretSetEcho(true)
			os.Exit(130)
		case <-done:
		}
	}()
	defer func() {
		close(done)
		signal.Stop(sig)
		secretSetEcho(true)
	}()
	in := bufio.NewReader(secretStdin)
	ask := func(text string) (string, error) {
		fmt.Fprint(prompt, text)
		line, err := in.ReadString('\n')
		fmt.Fprintln(prompt)
		if err != nil && line == "" {
			return "", errors.New("no value was entered")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	first, err := ask("Value for keychain:" + item + " (hidden): ")
	if err != nil {
		return "", err
	}
	second, err := ask("Again: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("the two entries differ; nothing was stored")
	}
	return first, secretstore.ValidateValue(first)
}

// --- forget ---------------------------------------------------------------

// secretForget drops a recorded reference from one instance (its manifest and
// kept references). A person action: the guard blocks it for agents.
func secretForget(stdout io.Writer, args []string, rootArg, newBase, legacyBase string) (int, error) {
	if len(args) < 1 || len(args) > 2 {
		return 2, errors.New("usage: stackfork secret forget NAME [CHECKOUT]")
	}
	root := rootArg
	if len(args) == 2 {
		root = args[1]
	}
	_, _, instanceDir, _, err := checkoutManifest(root, newBase, legacyBase)
	if err != nil {
		return 0, err
	}
	ok, err := state.ForgetSecret(instanceDir, args[0])
	if err != nil {
		return 0, err
	}
	if !ok {
		return 1, fmt.Errorf("no recorded reference %s for this checkout (see: stackfork secret ls)", args[0])
	}
	fmt.Fprintf(stdout, "forgot %s for this checkout\n", args[0])
	return 0, nil
}

// --- map / unmap ----------------------------------------------------------

func secretMap(stdout io.Writer, verb string, args []string, newBase, legacyBase string) (int, error) {
	if len(args) != 2 {
		if verb == "map" {
			return 2, errors.New("usage: stackfork secret map REPO|FOLDER NAME=REF")
		}
		return 2, errors.New("usage: stackfork secret unmap REPO|FOLDER NAME")
	}
	repo, err := resolveRepoArg(args[0], newBase, legacyBase)
	if err != nil {
		return 0, err
	}
	repo = mainRepo(repo)
	target := "for " + repo
	if repos.MainRepoOf(repo) == "" {
		target = "for folder " + repo
	}
	load := config.Load
	if verb == "unmap" {
		load = config.LoadForUnmap
	}
	cfg, err := load()
	if err != nil {
		return 0, err
	}
	if verb == "unmap" {
		if err := cfg.Unmap(repo, args[1]); err != nil {
			return 1, err
		}
		if err := cfg.Save(); err != nil {
			return 0, err
		}
		fmt.Fprintf(stdout, "unmapped %s %s\n", args[1], target)
		return 0, nil
	}
	name, ref, err := compose.ParseSecretFlag(args[1])
	if err != nil {
		return 2, errors.New(strings.Replace(err.Error(), "--secret", "secret map", 1))
	}
	if err := cfg.Map(repo, name, ref); err != nil {
		return 1, err
	}
	if err := cfg.Save(); err != nil {
		return 0, err
	}
	st, _ := compose.ExistsSecret(ref) // never prompts
	if repos.MainRepoOf(repo) == "" {
		target += " (applies to the repositories under it that use " + name + ")"
	}
	fmt.Fprintf(stdout, "mapped %s to %s %s (status: %s)\n", name, ref, target, st)
	if st == secretstore.Missing && strings.HasPrefix(ref, "keychain:") {
		fmt.Fprintf(stdout, "store its value: printenv VAR | stackfork secret set %s\n", strings.TrimPrefix(ref, "keychain:"))
	}
	return 0, nil
}
