package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/tools/tau/cli/common"
	dreamClient "github.com/taubyte/tau/tools/tau/clients/dream"
	patrickClient "github.com/taubyte/tau/tools/tau/clients/patrick_client"
	tauCommon "github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectFlags "github.com/taubyte/tau/tools/tau/flags/project"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	"github.com/taubyte/tau/tools/tau/lib/follow"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/output"
	"github.com/taubyte/tau/tools/tau/prompts"
	projectPrompts "github.com/taubyte/tau/tools/tau/prompts/project"
	"github.com/taubyte/tau/tools/tau/session"
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
		var skipped []string
		targets, before, skipped = follow.Baseline(targets)
		for _, s := range skipped {
			fmt.Fprintln(os.Stderr, s)
		}
	}

	var pushed []follow.Repo
	err = (&dualRepoHandler{
		ctx:         ctx,
		repository:  repoHandler,
		projectName: project.Name,
		errorFormat: projectI18n.PushingProjectFailed,
		action: func(r *git.Repository) error {
			p, err := pushRepo(r, commitMessage)
			if err == nil {
				pushed = append(pushed, p)
			}
			return err
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

// pushRepo commits what changed and pushes; with nothing to commit it pushes
// what is already committed. It returns what --wait matches the build on.
func pushRepo(r *git.Repository, message string) (follow.Repo, error) {
	name := filepath.Base(r.Root())
	if err := r.Commit(message, "."); errors.Is(err, gogit.ErrEmptyCommit) {
		fmt.Fprintf(os.Stderr, "nothing to commit in %s; pushing what is already committed\n", name)
	} else if err != nil {
		return follow.Repo{}, err
	}
	since := time.Now().Unix()
	if err := r.Push(); err != nil {
		return follow.Repo{}, err
	}
	out := follow.Repo{Name: name, Since: since}
	if head, err := r.Repo().Head(); err == nil {
		out.Commit, out.Branch = head.Hash().String(), head.Name().Short()
	}
	if o, err := r.Repo().Remote("origin"); err == nil && len(o.Config().URLs) > 0 {
		out.Remote = follow.RepoName(o.Config().URLs[0])
	}
	return out, nil
}

// injectDream is what --wait runs on Dream, which gets no GitHub webhook.
var injectDream = func(ctx context.Context, projectID, branch string) error {
	universe, _ := session.GetCustomCloudUrl()
	return dreamClient.PushAll(ctx, dreamClient.DefaultURL(), universe, projectID, branch)
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
	if profile, err := loginLib.GetSelectedProfile(); err == nil && profile.CloudType == tauCommon.DreamCloud && len(pushed) > 0 {
		if err := injectDream(c, projectID, pushed[0].Branch); err != nil {
			return cli.Exit(fmt.Sprintf("Dream gets no GitHub webhook: run `dream inject push-all` (the inject failed: %v)", err), 2)
		}
	}
	fmt.Fprintln(os.Stderr, "waiting for builds...")
	jobs, code := follow.Wait(c, p, projectID, pushed, 2*time.Second, os.Stderr)
	result := struct {
		Jobs    []follow.Job    `json:"jobs"`
		Serving []follow.Served `json:"serving,omitempty"`
		Exit    int             `json:"exit"`
	}{Jobs: jobs}
	if code == 0 && len(targets) > 0 {
		fmt.Fprintln(os.Stderr, "waiting for nodes to serve the new build...")
		since := time.Now()
		if len(pushed) > 0 {
			since = time.Unix(pushed[0].Since, 0)
		}
		result.Serving, code = follow.Serve(c, targets, func(t follow.Target) string { return before[t] }, time.Second, since)
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
