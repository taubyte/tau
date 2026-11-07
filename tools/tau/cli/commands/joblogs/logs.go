// Package joblogs is `tau logs`: a build job's logs, by job id or as the
// last build of a repository of the selected project.
package joblogs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	patrickClient "github.com/taubyte/tau/tools/tau/clients/patrick_client"
	"github.com/taubyte/tau/tools/tau/lib/follow"
	"github.com/taubyte/tau/tools/tau/output"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/urfave/cli/v2"
)

var Command = &cli.Command{
	Name:      "logs",
	Usage:     "a build job's logs, by job id or the last build of a repository (config, code, website <name>, library <name>)",
	ArgsUsage: "<job-id> | --last <repository> | function <name>",
	Flags: []cli.Flag{
		&cli.BoolFlag{Name: "last", Usage: "the last build of the repository named by the arguments, e.g. --last website site1"},
		&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, Usage: "print the log as the build writes it, until the job is done"},
	},
	Action: run,
}

// ErrFunctionLogs is what `tau logs function <name>` answers until the SDK can log (WS.8).
var ErrFunctionLogs = errors.New("function runtime logs are not available yet: functions cannot log until the SDK logging work (WS.8) ships; for the build log use `tau logs --last code`")

// Client is the part of the Patrick client tau logs uses.
type Client interface {
	follow.Patrick
	StreamLogs(jid string, w io.Writer) error
}

// Result is a job's logs as --json prints them: resource id to log text.
type Result struct {
	Job    string            `json:"job"`
	Status string            `json:"status"`
	Logs   map[string]string `json:"logs"`
}

func run(ctx *cli.Context) error {
	args := ctx.Args().Slice()
	if len(args) > 0 && args[0] == "function" {
		return ErrFunctionLogs
	}
	if len(args) == 0 {
		return errors.New("give a job id, or --last and a repository: tau logs --last code")
	}
	p, err := patrickClient.Load()
	if err != nil {
		return err
	}
	jid := args[0]
	if ctx.Bool("last") {
		st, err := tcc.Open()
		if err != nil {
			return err
		}
		project, err := st.ProjectID()
		if err != nil {
			return err
		}
		j, err := follow.LastJob(p, project, follow.RepoNames(st), strings.Join(args, " "))
		if err != nil {
			return err
		}
		jid = j.Id
	}
	return Show(os.Stdout, p, jid, ctx.Bool("follow"))
}

// Show prints job jid's logs, or with followLog streams them until the job
// ends (Patrick's live log websocket; a finished job gets its stored log).
func Show(w io.Writer, p Client, jid string, followLog bool) error {
	if followLog {
		return p.StreamLogs(jid, w)
	}
	job, err := p.Job(jid)
	if err != nil {
		return err
	}
	res := Result{Job: jid, Status: strings.ToLower(job.Status.String()), Logs: map[string]string{}}
	for resource, cid := range job.Logs {
		rc, err := p.LogFile(jid, cid)
		if err != nil {
			return err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		res.Logs[resource] = string(b)
	}
	if output.Render(res) {
		return nil
	}
	if len(res.Logs) == 0 {
		fmt.Fprintf(w, "job %s (%s) has no logs yet; add --follow to watch a running build\n", jid, res.Status)
		return nil
	}
	resources := make([]string, 0, len(res.Logs))
	for r := range res.Logs {
		resources = append(resources, r)
	}
	sort.Strings(resources)
	for _, r := range resources {
		fmt.Fprintf(w, "--- job %s (%s), resource %s\n%s\n", jid, res.Status, r, strings.TrimRight(res.Logs[r], "\n"))
	}
	return nil
}
