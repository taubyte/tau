package app

import (
	"fmt"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/afero"
	"github.com/urfave/cli/v2"

	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/publish"
	registerService "github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/v2/node"
	"github.com/taubyte/tau/services/v2/node/boot"
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
			Usage:   "publish only this deployment; the branch decides when unset",
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
			fs := afero.NewOsFs()
			branch := ctx.String(branchFlag.Name)
			say := func(format string, args ...any) { fmt.Fprintf(ctx.App.Writer, format+"\n", args...) }

			// WHICH DEPLOYMENTS, BEFORE WHAT THEY CONTAIN. A project says which
			// branches feed which of its environments, so a push is already an
			// answer to "deploy where" and nobody should be retyping it.
			environments, err := publish.Environments(fs, dir, branch, ctx.String(environmentFlag.Name))
			if err != nil {
				return err
			}

			// EVERY ONE COMPILES BEFORE ANY ONE PUBLISHES, and they are separate
			// compiles: a deployment's own limits, toolchain and ceilings are
			// resolved for it and nothing else. Doing them all first is what
			// keeps a project that compiles for production and not for preview
			// from going half live.
			type deployment struct {
				name     string
				compiled []byte
				project  *project.Project
			}
			plan := make([]deployment, 0, len(environments))
			for _, name := range environments {
				compiled, p, err := publish.Compile(fs, dir, branch, name)
				if err != nil {
					return err
				}
				plan = append(plan, deployment{name: name, compiled: compiled, project: p})
			}

			fqdn := ctx.String(fqdnFlag.Name)
			if ctx.Bool(dryRunFlag.Name) {
				for _, d := range plan {
					hosts, incomplete := publish.Hosts(d.project, fqdn)
					say("%s compiles for %s", dir, d.name)
					report(say, hosts, incomplete)
				}
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

			name := ctx.String(projectFlag.Name)
			for _, d := range plan {
				result, err := publish.Object(ctx.Context, client, d.compiled, d.project, publish.Options{
					Project:     name,
					Environment: d.name,
					Branch:      branch,
					CloudFqdn:   fqdn,
					Revision:    ctx.Uint64(revisionFlag.Name),
				})
				if err != nil {
					return err
				}

				if result.Unchanged {
					say("unchanged: %s/%s already runs %s at revision %d",
						name, d.name, result.Object, result.Revision)
				} else {
					say("published %s/%s revision %d as %s",
						name, d.name, result.Revision, result.Object)
				}
				report(say, result.Hosts, result.Incomplete)
			}
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
