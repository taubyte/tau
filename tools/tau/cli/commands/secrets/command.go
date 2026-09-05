// Package secrets is `tau secrets`, the one thing a developer writes to this
// cloud that is not a git push.
//
// # Why it is not a push
//
// Everything else v2 runs comes from a commit: the register's only writer is
// the build service, because a deploy from a laptop bypasses git, its
// authorization and its record. A secret inverts that exactly — putting the
// value in git IS the failure — so the value is written here, out of band, and
// a rotation is one call with no commit and no build behind it.
//
// The DECLARATION is still a push. `tau new secret` writes a `secret` resource
// into the config repo; this command fills it in. The two halves are separate
// on purpose and the error below says so when only one of them has happened.
//
// # Nothing here reads a value back
//
// `list` names what is set. There is no `get`, because the frontend has no
// route that returns a value — which is what keeps a leaked token to "they can
// overwrite my secrets" rather than "they have every secret this project
// holds". Every platform this competes with is write-only after the set.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/urfave/cli/v2"

	buildClient "github.com/taubyte/tau/tools/tau/clients/build"
	"github.com/taubyte/tau/tools/tau/output"
	"github.com/taubyte/tau/tools/tau/tcc"
)

var environmentFlag = &cli.StringFlag{
	Name:    "env",
	Aliases: []string{"e"},
	Usage:   "deployment to set this in; may be omitted when the project declares exactly one",
}

var Command = &cli.Command{
	Name:    "secrets",
	Aliases: []string{"secret-value"},
	Usage:   "Set, list and remove the values behind a project's declared secrets",
	Subcommands: []*cli.Command{
		{
			Name:      "set",
			Usage:     "Write a secret's value. Setting it again is a rotation: no commit, no build.",
			ArgsUsage: "<name>",
			Flags: []cli.Flag{
				environmentFlag,
				&cli.StringFlag{
					Name:  "value",
					Usage: "the value; prefer --from-file or stdin, because a value on the command line is in your shell history and in ps",
				},
				&cli.StringFlag{
					Name:    "from-file",
					Aliases: []string{"f"},
					Usage:   "read the value from this file; \"-\" reads stdin",
				},
			},
			Action: set,
		},
		{
			Name:   "list",
			Usage:  "Names of the secrets set in a deployment. Never values.",
			Flags:  []cli.Flag{environmentFlag},
			Action: list,
		},
		{
			Name:      "rm",
			Aliases:   []string{"unset", "delete"},
			Usage:     "Remove a secret's value. The declaration stays in the config.",
			ArgsUsage: "<name>",
			Flags:     []cli.Flag{environmentFlag},
			Action:    remove,
		},
	},
}

// THE THREE ACTIONS READ THE CONTEXT AND NOTHING ELSE. Everything that talks
// to a cloud is a method on scope below, so it is reachable in a test without a
// selected project, a clone or a token.

func set(ctx *cli.Context) error {
	scope, err := open(ctx)
	if err != nil {
		return err
	}
	// THE NAME AND THE VALUE ARE SETTLED BEFORE ANYTHING IS DIALLED. A typo or
	// a missing value is answered instantly and offline, which is the
	// difference between "no such secret" and a connection timeout followed by
	// "no such secret".
	if _, err := scope.secretID(ctx.Args().First()); err != nil {
		return err
	}
	value, err := readValue(ctx)
	if err != nil {
		return err
	}
	if err := scope.dial(); err != nil {
		return err
	}
	return scope.set(ctx.Context, ctx.Args().First(), value, os.Stdout)
}

func list(ctx *cli.Context) error {
	scope, err := open(ctx)
	if err != nil {
		return err
	}
	if err := scope.dial(); err != nil {
		return err
	}
	return scope.list(ctx.Context, os.Stdout)
}

func remove(ctx *cli.Context) error {
	scope, err := open(ctx)
	if err != nil {
		return err
	}
	if _, err := scope.secretID(ctx.Args().First()); err != nil {
		return err
	}
	if err := scope.dial(); err != nil {
		return err
	}
	return scope.remove(ctx.Context, ctx.Args().First(), os.Stdout)
}

func (s *scope) set(ctx context.Context, name string, value []byte, out io.Writer) error {
	id, err := s.secretID(name)
	if err != nil {
		return err
	}
	if err := s.client.SetSecret(ctx, s.project, s.environment, id, value); err != nil {
		return err
	}
	// THE VALUE IS NOT ECHOED, not even truncated. A terminal is scrolled back
	// through, copied out of, and screen-shared.
	fmt.Fprintf(out, "%s is set in %s\n", name, s.environment)
	return nil
}

func (s *scope) remove(ctx context.Context, name string, out io.Writer) error {
	id, err := s.secretID(name)
	if err != nil {
		return err
	}
	if err := s.client.DeleteSecret(ctx, s.project, s.environment, id); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s is unset in %s\n", name, s.environment)
	return nil
}

func (s *scope) list(ctx context.Context, out io.Writer) error {
	ids, err := s.client.Secrets(ctx, s.project, s.environment)
	if err != nil {
		return err
	}

	// THE CLOUD ANSWERS IN IDS AND A PERSON READS NAMES. The id is what a
	// reference compiles to, so it is what the vault is keyed by; turning it
	// back is a read of the config repo already open here.
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, ok := s.names[id]; ok {
			names = append(names, name)
			continue
		}
		// A value set under an id this config no longer declares. It is shown
		// rather than hidden: it is holding a value, and somebody has to be
		// able to see it in order to remove it.
		names = append(names, id+" (not declared in this config)")
	}
	sort.Strings(names)

	if output.Render(names) {
		return nil
	}
	if len(names) == 0 {
		fmt.Fprintf(out, "nothing is set in %s\n", s.environment)
		return nil
	}
	for _, name := range names {
		fmt.Fprintln(out, name)
	}
	return nil
}

// scope is everything the three subcommands share: which project, which
// deployment, and the declarations that turn a name into the id the cloud
// keys by.
type scope struct {
	client      buildClient.Client
	project     string
	environment string

	// names maps a declared secret's id to the name it was written as, and
	// byName is the other direction.
	names  map[string]string
	byName map[string]string
}

// open reads what the config repo knows: which project, which deployment, and
// the declarations that turn a name into the id the cloud keys by. It dials
// nothing — see dial below.
func open(ctx *cli.Context) (*scope, error) {
	store, err := tcc.Open()
	if err != nil {
		return nil, err
	}

	project, err := store.ProjectID()
	if err != nil {
		return nil, err
	}

	environment, err := environmentOf(ctx, store)
	if err != nil {
		return nil, err
	}

	s := &scope{
		project: project, environment: environment,
		names: map[string]string{}, byName: map[string]string{},
	}

	declared, err := store.List("secret")
	if err != nil {
		return nil, err
	}
	for _, name := range declared {
		doc, err := store.Doc("secret", name)
		if err != nil {
			continue
		}
		id, _ := doc["id"].(string)
		if id == "" {
			continue
		}
		s.names[id] = name
		s.byName[name] = id
	}
	return s, nil
}

// dial resolves the cloud this session has selected. It is separate from open
// so that everything answerable from the config repo is answered first.
func (s *scope) dial() error {
	client, err := buildClient.Load()
	if err != nil {
		return err
	}
	s.client = client
	return nil
}

// environmentOf is the flag, or the project's one deployment when it has one.
//
// GUESSING BETWEEN TWO WOULD BE THE WRONG KIND OF CONVENIENCE. Writing
// production's database password into staging is silent, and it is discovered
// when production stops working.
func environmentOf(ctx *cli.Context, store *tcc.Store) (string, error) {
	if name := ctx.String("env"); name != "" {
		return name, nil
	}

	declared := store.Environments()
	switch len(declared) {
	case 1:
		return declared[0], nil
	case 0:
		return "", errors.New("this project declares no deployments; add one to project.yaml, or pass --env")
	default:
		return "", fmt.Errorf("this project has more than one deployment (%s); say which with --env",
			strings.Join(declared, ", "))
	}
}

// secretID turns the name a person types into the id the vault is keyed by.
//
// A NAME THIS CONFIG DOES NOT DECLARE IS REFUSED. Setting a value under a name
// nothing references would succeed at the cloud and do nothing at all, and the
// failure would surface as a workload that never got its variable.
func (s *scope) secretID(name string) (string, error) {
	if name == "" {
		return "", errors.New("which secret? pass its name, as declared in the config")
	}
	id, ok := s.byName[name]
	if !ok {
		return "", fmt.Errorf("this project declares no secret called %q — `tau new secret` declares one, and this sets its value", name)
	}
	return id, nil
}

// readValue takes the value from --value, from a file, or from stdin.
//
// STDIN IS THE DEFAULT WHEN IT IS NOT A TERMINAL, so `... | tau secrets set X`
// works and nothing is asked. A value passed as --value is in the shell's
// history and in the process table, which is why the flag says so.
func readValue(ctx *cli.Context) ([]byte, error) {
	given := 0
	for _, set := range []bool{ctx.IsSet("value"), ctx.IsSet("from-file")} {
		if set {
			given++
		}
	}
	if given > 1 {
		return nil, errors.New("--value and --from-file are two answers to one question; pass one")
	}

	if ctx.IsSet("value") {
		return []byte(ctx.String("value")), nil
	}

	if path := ctx.String("from-file"); path != "" && path != "-" {
		return os.ReadFile(path)
	}
	if ctx.IsSet("from-file") || !isTerminal(os.Stdin) {
		return io.ReadAll(os.Stdin)
	}
	return nil, errors.New("no value: pass --from-file, or pipe it in")
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
