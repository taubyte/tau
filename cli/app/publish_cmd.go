package app

import (
	"fmt"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/afero"
	"github.com/urfave/cli/v2"

	registerService "github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/v2/node"
	"github.com/taubyte/tau/services/v2/node/boot"
	"github.com/taubyte/tau/services/v2/node/publish"
)

// publish_cmd.go is the step between a config repository and a fleet that
// serves it.
//
// ONE COMMAND, AND NOTHING TO TRACK. It compiles, works out the names the
// project answers on, finds a register in the fleet and publishes under the
// next revision. Nobody types a revision, nobody looks up which node holds the
// register, and nobody assembles a hostname out of a label and a zone.

func publishCommand() *cli.Command {
	var (
		configFlag = &cli.PathFlag{
			Name:     "config",
			Aliases:  []string{"c"},
			Usage:    "the node config naming the swarm to publish into",
			Required: true,
		}
		projectFlag = &cli.StringFlag{
			Name:     "project",
			Aliases:  []string{"p"},
			Required: true,
		}
		environmentFlag = &cli.StringFlag{
			Name:    "environment",
			Aliases: []string{"env", "e"},
			Value:   "prod",
		}
		branchFlag = &cli.StringFlag{
			Name:  "branch",
			Usage: "branch the compile reads; the compiler's default when unset",
		}
		fqdnFlag = &cli.StringFlag{
			Name:  "cloud-fqdn",
			Usage: "the zone this cloud serves, which completes generated names",
		}
		revisionFlag = &cli.Uint64Flag{
			Name:  "revision",
			Usage: "publish under this revision; derived from what is there when unset",
		}
		waitFlag = &cli.DurationFlag{
			Name:  "wait",
			Usage: "how long to wait for a register to answer",
			Value: 30 * time.Second,
		}
		dryRunFlag = &cli.BoolFlag{
			Name:  "dry-run",
			Usage: "compile and report, publish nothing",
		}
	)

	return &cli.Command{
		Name:      "publish",
		Usage:     "compile a config repository and publish it",
		ArgsUsage: "<config directory>",
		Flags: []cli.Flag{
			configFlag, projectFlag, environmentFlag,
			branchFlag, fqdnFlag, revisionFlag, waitFlag, dryRunFlag,
		},
		Action: func(ctx *cli.Context) error {
			if ctx.NArg() != 1 {
				return fmt.Errorf("name one config directory")
			}
			dir := ctx.Args().First()

			options := publish.Options{
				Project:     ctx.String(projectFlag.Name),
				Environment: ctx.String(environmentFlag.Name),
				Branch:      ctx.String(branchFlag.Name),
				CloudFqdn:   ctx.String(fqdnFlag.Name),
				Revision:    ctx.Uint64(revisionFlag.Name),
			}

			// COMPILING COMES FIRST, ALWAYS. A config that does not compile is
			// a config that does not compile whether or not a fleet is
			// reachable, and finding that out after a swarm join is a slower
			// way to be told the same thing.
			compiled, project, err := publish.Compile(afero.NewOsFs(), dir, options.Branch)
			if err != nil {
				return err
			}

			hosts, incomplete := publish.Hosts(project, options.CloudFqdn)
			say := func(format string, args ...any) { fmt.Fprintf(ctx.App.Writer, format+"\n", args...) }

			if ctx.Bool(dryRunFlag.Name) {
				say("%s compiles", dir)
				report(say, hosts, incomplete)
				return nil
			}

			file, err := boot.Read(ctx.Path(configFlag.Name))
			if err != nil {
				return err
			}

			running, err := boot.Reach(ctx.Context, file, node.Register, ctx.Duration(waitFlag.Name))
			if err != nil {
				return err
			}
			defer running.Close()

			id, err := libp2ppeer.Decode(running.Found[0].Peer)
			if err != nil {
				return fmt.Errorf("publish: %q is not a peer: %w", running.Found[0].Peer, err)
			}

			client, err := registerService.NewClient(running.Host(), id)
			if err != nil {
				return err
			}
			defer client.Close()

			result, err := publish.Object(ctx.Context, client, compiled, project, options)
			if err != nil {
				return err
			}

			if result.Unchanged {
				say("unchanged: %s/%s already runs %s at revision %d",
					options.Project, options.Environment, result.Object, result.Revision)
			} else {
				say("published %s/%s revision %d as %s",
					options.Project, options.Environment, result.Revision, result.Object)
			}
			report(say, result.Hosts, result.Incomplete)
			return nil
		},
	}
}

// report says what a project answers on, and what it would answer on if the
// cloud's own name were given.
func report(say func(string, ...any), hosts, incomplete []string) {
	if len(hosts) > 0 {
		say("")
		say("answers on (%d)", len(hosts))
		for _, host := range hosts {
			say("    %s", host)
		}
	}

	if len(incomplete) > 0 {
		say("")
		say("not published (%d) — these are names in the cloud's own zone, and --cloud-fqdn says what it is",
			len(incomplete))
		for _, host := range incomplete {
			say("    %s.<cloud fqdn>", host)
		}
	}
}
