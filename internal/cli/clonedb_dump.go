// clonedb_dump.go implements `clone-db --from-dump FILE` (add-env-and-seed,
// capability data-seeding): seeds this instance's own database service
// from a local dump instead of another instance's volume. Same guards as
// the volume-copy form (target empty unless --replace, this instance
// only), but the opposite running requirement: the dump is restored
// through the live database process (`docker compose exec`), so the
// instance must be up, not stopped.
package cli

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/identity"
	"github.com/pedropinheiro75/stackfork/internal/inventory"
)

// pgCustomFormatMagic is pg_dump -Fc's fixed 5-byte file signature, used to
// tell a custom-format dump from plain SQL without trusting the file
// extension (verified against pg_dump's own source/format docs: "PGDMP").
const pgCustomFormatMagic = "PGDMP"

func cmdCloneDBFromDump(stdout io.Writer, dumpArg, serviceFlag, databaseFlag string, replace bool, root, project, instanceDir string) (int, error) {
	dumpPath, err := identity.ResolvePath(dumpArg)
	if err != nil {
		return 0, err
	}
	if fi, err := os.Stat(dumpPath); err != nil {
		return 0, fmt.Errorf("--from-dump %q: %w", dumpArg, err)
	} else if fi.IsDir() {
		return 0, fmt.Errorf("--from-dump %q is a directory, not a file", dumpArg)
	}

	if err := requirePrepared(instanceDir); err != nil {
		return 0, err
	}

	running, err := inventory.RunningProjects()
	if err != nil {
		return 0, err
	}
	if !running[project] {
		return 0, errors.New("this instance is not running; run `stackfork up` first (a dump restores through the live database process)")
	}

	model, err := readComposeModel(instanceDir)
	if err != nil {
		return 0, err
	}
	service, kind, err := resolveDBService(model, serviceFlag)
	if err != nil {
		return 0, err
	}
	svcRaw, _ := asMap(mustServices(model)[service])
	database, err := resolveDBName(kind, svcRaw, databaseFlag)
	if err != nil {
		return 0, err
	}
	user := defaultDBUser(kind, svcRaw)

	content, isCustom, cleanup, err := openDumpContent(dumpPath)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	if isCustom && kind != "postgres" {
		return 0, fmt.Errorf("%q looks like a Postgres custom-format dump; the target service %q is %s, not postgres", dumpArg, service, kind)
	}

	ce := &composeExec{root: root, project: project, configPath: filepath.Join(instanceDir, "compose.json"), service: service}

	count, err := tableCount(ce, kind, user, database)
	if err != nil {
		return 0, err
	}
	if count > 0 {
		if !replace {
			return 0, fmt.Errorf("database %q on service %q is not empty (%d table(s)); pass --replace to overwrite it", database, service, count)
		}
		if err := resetDatabase(ce, kind, user, database); err != nil {
			return 0, err
		}
	}

	if err := restore(ce, kind, isCustom, user, database, content); err != nil {
		return 0, err
	}
	fmt.Fprintf(stdout, "seeded %s (service %s, database %s) from %s\n", project, service, database, dumpArg)
	setSeeded(instanceDir, boolPtr(true)) // data came from elsewhere: never seed it
	return 0, nil
}

func requirePrepared(instanceDir string) error {
	if _, err := os.Stat(filepath.Join(instanceDir, "manifest.json")); err != nil {
		return errors.New("No prepared stack for this checkout/branch. Run prepare first.")
	}
	return nil
}

func mustServices(model map[string]interface{}) map[string]interface{} {
	services, _ := model["services"].(map[string]interface{})
	return services
}

func asMap(v interface{}) (map[string]interface{}, bool) {
	m, ok := v.(map[string]interface{})
	return m, ok
}

// resolveDBService picks the target database service: the given --service
// name if any (any kind - the caller still checks it's a supported one via
// resolveDBName/restore's own refusals), else the single postgres/mysql/
// mariadb service declared, else a clear refusal naming every candidate
// (more than one) or none at all.
func resolveDBService(model map[string]interface{}, serviceFlag string) (name, kind string, err error) {
	services := mustServices(model)
	if serviceFlag != "" {
		raw, ok := asMap(services[serviceFlag])
		if !ok {
			return "", "", fmt.Errorf("no service named %q in this instance", serviceFlag)
		}
		image, _ := raw["image"].(string)
		kind := inventory.ImageKind(image)
		if kind != "postgres" && kind != "mysql" && kind != "mariadb" {
			return "", "", fmt.Errorf("service %q (image %q) isn't a supported database (postgres/mysql/mariadb)", serviceFlag, image)
		}
		return serviceFlag, kind, nil
	}

	var candidates []string
	kinds := map[string]string{}
	for name, raw := range services {
		svc, ok := asMap(raw)
		if !ok {
			continue
		}
		image, _ := svc["image"].(string)
		k := inventory.ImageKind(image)
		if k == "postgres" || k == "mysql" || k == "mariadb" {
			candidates = append(candidates, name)
			kinds[name] = k
		}
	}
	sort.Strings(candidates)
	switch len(candidates) {
	case 1:
		return candidates[0], kinds[candidates[0]], nil
	case 0:
		return "", "", errors.New("no postgres/mysql/mariadb service found; pass --service explicitly")
	default:
		return "", "", fmt.Errorf("multiple candidate database services (%s); pass --service explicitly", strings.Join(candidates, ", "))
	}
}

// resolveDBName picks the target database name: --database if given, else
// the service's own POSTGRES_DB/MYSQL_DATABASE/MARIADB_DATABASE - the same
// env keys and fallback inventory.ConnectionURL builds a connection string
// from (postgres defaults to the user name when POSTGRES_DB is unset,
// matching the official image's own default database name); MySQL/MariaDB
// have no such convention, so an unset MYSQL_DATABASE/MARIADB_DATABASE is a
// clear refusal instead of a guess.
func resolveDBName(kind string, svcRaw map[string]interface{}, databaseFlag string) (string, error) {
	if databaseFlag != "" {
		return databaseFlag, nil
	}
	env := compose.NormalizeEnvironment(svcRaw["environment"])
	switch kind {
	case "postgres":
		if db := env["POSTGRES_DB"]; db != "" {
			return db, nil
		}
		if user := env["POSTGRES_USER"]; user != "" {
			return user, nil
		}
		return "postgres", nil
	case "mysql":
		if db := env["MYSQL_DATABASE"]; db != "" {
			return db, nil
		}
	case "mariadb":
		if db := env["MARIADB_DATABASE"]; db != "" {
			return db, nil
		}
	}
	return "", fmt.Errorf("service has no MYSQL_DATABASE/MARIADB_DATABASE set; pass --database explicitly")
}

func defaultDBUser(kind string, svcRaw map[string]interface{}) string {
	env := compose.NormalizeEnvironment(svcRaw["environment"])
	switch kind {
	case "postgres":
		if u := env["POSTGRES_USER"]; u != "" {
			return u
		}
		return "postgres"
	case "mysql":
		if u := env["MYSQL_USER"]; u != "" {
			return u
		}
	case "mariadb":
		if u := env["MARIADB_USER"]; u != "" {
			return u
		}
	}
	return "root"
}

// openDumpContent opens the dump for streaming, transparently
// decompressing .gz, and reports whether it's a Postgres custom-format
// dump (pg_dump -Fc) by sniffing the magic header - not the file
// extension, so a misnamed file is still handled correctly. The returned
// reader is never written to a temp file; cleanup closes whatever was
// opened.
func openDumpContent(path string) (content io.Reader, isCustom bool, cleanup func(), err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, func() {}, err
	}
	var raw io.Reader = f
	closers := []io.Closer{f}
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, false, func() {}, fmt.Errorf("%s: not a valid gzip file: %w", path, err)
		}
		raw = gz
		closers = append(closers, gz)
	}
	br := bufio.NewReader(raw)
	magic, _ := br.Peek(len(pgCustomFormatMagic))
	cleanup = func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i].Close()
		}
	}
	return br, bytes.Equal(magic, []byte(pgCustomFormatMagic)), cleanup, nil
}

// composeExec runs a command inside this instance's own running service
// container via `docker compose exec -T`, the same resolution
// (project/compose.json/service, never a hardcoded container name)
// lifecycle.Execute's own exec/run actions already use.
type composeExec struct {
	root, project, configPath, service string
}

func (e *composeExec) run(stdin io.Reader, args ...string) ([]byte, error) {
	full := append([]string{"compose", "-p", e.project, "--project-directory", e.root,
		"--env-file", os.DevNull, "-f", e.configPath, "exec", "-T", e.service}, args...)
	cmd := exec.Command("docker", full...)
	cmd.Env = compose.DockerEnv(nil)
	cmd.Stdin = stdin
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return nil, fmt.Errorf("%s: %s", strings.Join(args, " "), msg)
	}
	return out.Bytes(), nil
}

// tableCount is the "target must be empty unless --replace" check: any
// user table at all in the target database refuses without --replace.
func tableCount(e *composeExec, kind, user, database string) (int, error) {
	var out []byte
	var err error
	switch kind {
	case "postgres":
		out, err = e.run(nil, "psql", "-U", user, "-d", database, "-tAc",
			"select count(*) from pg_catalog.pg_tables where schemaname not in ('pg_catalog','information_schema')")
	case "mysql", "mariadb":
		out, err = e.run(nil, "mysql", "-u", user, database, "-N", "-e",
			"select count(*) from information_schema.tables where table_schema=database()")
	default:
		return 0, fmt.Errorf("clone-db --from-dump only supports postgres/mysql/mariadb, got %q", kind)
	}
	if err != nil {
		return 0, fmt.Errorf("checking target database is empty: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("checking target database is empty: unexpected output %q", string(out))
	}
	return n, nil
}

// resetDatabase clears every table --replace must overwrite: drop and
// recreate the schema (Postgres) or the whole database (MySQL/MariaDB),
// leaving the target genuinely empty for restore to fill.
func resetDatabase(e *composeExec, kind, user, database string) error {
	switch kind {
	case "postgres":
		_, err := e.run(nil, "psql", "-U", user, "-d", database, "-v", "ON_ERROR_STOP=1",
			"-c", "DROP SCHEMA public CASCADE; CREATE SCHEMA public;")
		return err
	case "mysql", "mariadb":
		_, err := e.run(nil, "mysql", "-u", user, "-e",
			fmt.Sprintf("DROP DATABASE IF EXISTS `%s`; CREATE DATABASE `%s`;", database, database))
		return err
	default:
		return fmt.Errorf("clone-db --from-dump only supports postgres/mysql/mariadb, got %q", kind)
	}
}

// restore streams content into the right restore program for kind/format -
// psql for plain SQL, pg_restore for a Postgres custom-format dump, mysql
// for MySQL/MariaDB (plain SQL only; a custom-format dump for a
// MySQL/MariaDB target already refused earlier).
func restore(e *composeExec, kind string, isCustom bool, user, database string, content io.Reader) error {
	switch {
	case kind == "postgres" && isCustom:
		_, err := e.run(content, "pg_restore", "-U", user, "-d", database, "--no-owner", "-1")
		return err
	case kind == "postgres":
		_, err := e.run(content, "psql", "-U", user, "-d", database, "-v", "ON_ERROR_STOP=1")
		return err
	case kind == "mysql" || kind == "mariadb":
		_, err := e.run(content, "mysql", "-u", user, database)
		return err
	default:
		return fmt.Errorf("clone-db --from-dump only supports postgres/mysql/mariadb, got %q", kind)
	}
}
