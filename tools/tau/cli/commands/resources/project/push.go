package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/tools/tau/cli/common"
	patrickClient "github.com/taubyte/tau/tools/tau/clients/patrick_client"
	"github.com/taubyte/tau/tools/tau/flags"
	projectFlags "github.com/taubyte/tau/tools/tau/flags/project"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	"github.com/taubyte/tau/tools/tau/lib/follow"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/output"
	"github.com/taubyte/tau/tools/tau/prompts"
	projectPrompts "github.com/taubyte/tau/tools/tau/prompts/project"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/urfave/cli/v2"
)

func (link) Push() common.Command {
	return common.Create(&cli.Command{
		Flags: []cli.Flag{
			flags.CommitMessage,
			projectFlags.ConfigOnly,
			projectFlags.CodeOnly,
			&cli.BoolFlag{Name: "wait", Usage: "follow the builds this push starts; exit 1 if one fails, 2 on timeout"},
			&cli.BoolFlag{Name: "serve", Usage: "with --wait: then wait until every node serves the new functions"},
			&cli.DurationFlag{Name: "timeout", Value: 10 * time.Minute, Usage: "with --wait: how long to wait in all"},
		},
		Action: push,
	})
}

func push(ctx *cli.Context) error {
	project, err := projectPrompts.GetOrSelect(ctx, true)
	if err != nil {
		return err
	}

	repoHandler, err := projectLib.Repository(project.Name).Open()
	if err != nil {
		return err
	}

	commitMessage, err := prompts.GetOrRequireACommitMessage(ctx)
	if err != nil {
		return err
	}

	// --serve compares each node's answer after the build with its answer now
	wait, serve := ctx.Bool("wait"), ctx.Bool("serve") && ctx.Bool("wait")
	var targets []follow.Target
	before := map[follow.Target]string{}
	if serve {
		st, err := tcc.Open()
		if err != nil {
			return err
		}
		if targets, err = follow.Targets(st, false); err != nil {
			return err
		}
		for _, t := range targets {
			before[t] = follow.Hash(follow.Probe(t))
		}
	}

	var pushed []follow.Repo
	err = (&dualRepoHandler{
		ctx:         ctx,
		repository:  repoHandler,
		projectName: project.Name,
		errorFormat: projectI18n.PushingProjectFailed,
		action: func(r *git.Repository) error {
			if err := r.Commit(commitMessage, "."); err != nil {
				return err
			}

			if err := r.Push(); err != nil {
				return err
			}
			if head, err := r.Repo().Head(); err == nil {
				pushed = append(pushed, follow.Repo{Name: filepath.Base(r.Root()), Commit: head.Hash().String()})
			}
			return nil
		},
	}).Run()
	if err != nil {
		return err
	}

	projectI18n.PushedProject(project.Name)
	if !wait {
		return nil
	}
	return waitFor(ctx, pushed, targets, before)
}

func waitFor(ctx *cli.Context, pushed []follow.Repo, targets []follow.Target, before map[follow.Target]string) error {
	st, err := tcc.Open()
	if err != nil {
		return err
	}
	projectID, err := st.ProjectID()
	if err != nil {
		return err
	}
	p, err := patrickClient.Load()
	if err != nil {
		return err
	}

	c, cancel := context.WithTimeout(ctx.Context, ctx.Duration("timeout"))
	defer cancel()
	fmt.Fprintln(os.Stderr, "waiting for builds...")
	jobs, code := follow.Wait(c, p, projectID, pushed, 2*time.Second)
	result := struct {
		Jobs    []follow.Job    `json:"jobs"`
		Serving []follow.Served `json:"serving,omitempty"`
		Exit    int             `json:"exit"`
	}{Jobs: jobs}
	if code == 0 && len(targets) > 0 {
		fmt.Fprintln(os.Stderr, "waiting for nodes to serve the new build...")
		result.Serving, code = follow.Serve(c, targets, func(t follow.Target) string { return before[t] }, time.Second)
	}
	result.Exit = code
	if !output.Render(result) {
		follow.WriteJobs(os.Stdout, jobs)
		follow.WriteServed(os.Stdout, result.Serving)
	}
	if code != 0 {
		return cli.Exit("", code)
	}
	return nil
}
