package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/taubyte/tau/tools/tau/internal/api"
	"github.com/taubyte/tau/tools/tau/internal/config"
	"github.com/taubyte/tau/tools/tau/internal/ui"
)

// ci.go is `tau ci run`: a build of a pushed commit, asked for by hand.
//
// THE COMMIT IS RESOLVED HERE. The cloud never asks a forge anything with a
// person's credential, so "the branch's head" is this checkout's to say; the
// cloud then reads the ref at the forge over the repository's deploy key, and a
// commit that was never pushed is refused by name.

func ciCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "the cloud's build, run by hand",
	}
	cmd.AddCommand(ciRunCommand(env))
	return cmd
}

type ciOutput struct {
	Job    string `json:"job"`
	Status string `json:"status,omitempty"`
}

func ciRunCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "build a pushed commit now, and follow its log",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			dir := flagString(cmd, "dir")
			id, err := projectID(cmd)
			if err != nil {
				return err
			}

			repo := flagString(cmd, "repo")
			if repo == "" {
				l, err := config.ReadLink(dir)
				if err != nil {
					return fmt.Errorf("--repo is required outside a linked directory: %w", err)
				}
				repo = l.Config
			}

			branch := flagString(cmd, "branch")
			if branch == "" {
				branch = branchOf(ctx, dir)
			}
			commit := flagString(cmd, "commit")
			if commit == "" {
				out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--verify", "-q", "refs/heads/"+branch+"^{commit}").Output()
				if err != nil {
					return fmt.Errorf("no commit named: this checkout has no branch %q; pass --commit", branch)
				}
				commit = strings.TrimSpace(string(out))
			}

			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			job, err := cl.Build(ctx, id, repo, "refs/heads/"+branch, commit)
			if err != nil {
				return err
			}

			out := ciOutput{Job: job}
			log := env.Stderr
			if jsonOut(cmd) {
				log = io.Discard
			}
			fmt.Fprintf(log, "queued job %s: %s at %s\n", job, repo, short(commit))
			if flagBool(cmd, "detach") {
				return emit(cmd, env, out, theme(env).Done("job "+job+" queued"))
			}

			final, err := awaitJob(cmd, cl, job, log)
			if err != nil {
				return err
			}
			out.Status = jobStatus(final)
			if err := emit(cmd, env, out, fmt.Sprintf("job %s: %s\n", job, out.Status)); err != nil {
				return err
			}
			if final.GetStatus() != api.JobSuccess {
				return &exitError{code: 1, msg: "job " + job + " " + out.Status}
			}
			return nil
		},
	}
	addDir(cmd)
	addProject(cmd)
	cmd.Flags().String("repo", "", "the repository to build (default: the linked configuration repository)")
	cmd.Flags().String("branch", "", "the branch it was pushed to (default: the checked-out one)")
	cmd.Flags().String("commit", "", "the commit to build (default: the branch's head in this checkout)")
	cmd.Flags().Bool("detach", false, "queue it and return, without following")
	cmd.Flags().Duration("interval", 2*time.Second, "how often a queued job is looked at")
	return cmd
}

// awaitJob follows a job to its end: the log of the run it is on while it runs,
// what is left of that run's log once it has ended, and its record at the end.
//
// THE RUN IS THE RECORD'S, and a read resumes only within it. A job whose node
// went away runs again with a log of its own, which is read from its start
// after a note saying where the last one stopped.
func awaitJob(cmd *cobra.Command, cl *api.Client, job string, log io.Writer) (*api.Job, error) {
	out := &lines{w: log}
	j, err := awaitOne(cmd, cl, job, out)
	if err != nil || !isPlan(j) {
		return j, err
	}

	// A PUSH IS A PLAN, then a job per environment it feeds: the push has ended
	// when they have, and it is the first that did not succeed.
	if j.GetStatus() != api.JobSuccess {
		if reason := j.GetReason(); reason != "" {
			out.note("job %s: %s", job, reason)
		}
		return j, nil
	}
	final := j
	for _, child := range j.GetChildren() {
		out.note("job %s: building as job %s", job, child)
		if final, err = awaitOne(cmd, cl, child, out); err != nil {
			return nil, err
		}
		if final.GetStatus() != api.JobSuccess {
			return final, nil
		}
	}
	return final, nil
}

// isPlan is a push's first job, which builds no environment and keeps no log.
func isPlan(j *api.Job) bool { return j.GetEnvironment() == "" }

// awaitOne follows one job to its end: each run's events until the status it left
// the job in, which is its log's last, so nothing waits on a clock while a run goes
// on. The record is read between runs, for what comes next. Only a log that cannot
// be read at all is asked for again after the interval.
func awaitOne(cmd *cobra.Command, cl *api.Client, job string, out *lines) (*api.Job, error) {
	ctx := cmd.Context()
	j, err := cl.Job(ctx, job)
	if err != nil {
		return nil, err
	}
	// The run it follows is named, as the frontend would name it, so a note about one
	// it could not read can say which.
	at := j.GetAttempt()
	if at == 0 || j.GetStatus() == api.JobQueued {
		at++
	}
	for {
		run := cl.Stream(ctx, job, at, 0)
		_, err := run.CopyTo(out)
		var lost *api.LostLog
		var refusal *api.Error
		unreadable := false
		switch {
		case errors.As(err, &lost):
			// A plan's log is live only, so one that ended before it was followed
			// left nothing to say so about.
			if !isPlan(j) {
				out.note("%v", lost)
			}
		case errors.As(err, &refusal):
			// A FOLLOW REFUSED IS A LOG NO NODE CAN SERVE NOW: asked for again,
			// unless the job has ended, when it is the last word on its log.
			unreadable = true
		case err != nil:
			return nil, err
		}

		if j, err = cl.Job(ctx, job); err != nil {
			return nil, err
		}
		if terminal(j) {
			if unreadable {
				out.note("the job's log cannot be read: %v", refusal)
			}
			return j, nil
		}
		next := j.GetAttempt()
		if j.GetStatus() == api.JobQueued {
			next++
		}
		if next != at && lost == nil {
			out.note("job %s: the log of attempt %d was read to byte %d; attempt %d follows", job, at, run.Offset(), next)
		}
		at = next
		if unreadable {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(interval(cmd)):
			}
		}
	}
}

// lines is a log with notes between its runs, each on a line of its own.
type lines struct {
	w   io.Writer
	mid bool
}

func (l *lines) Write(p []byte) (int, error) {
	n, err := l.w.Write(p)
	if n > 0 {
		l.mid = p[n-1] != '\n'
	}
	return n, err
}

func (l *lines) note(format string, args ...any) {
	if l.mid {
		fmt.Fprintln(l.w)
		l.mid = false
	}
	fmt.Fprintf(l.w, format+"\n", args...)
}

func interval(cmd *cobra.Command) time.Duration {
	if d := flagDuration(cmd, "interval"); d > 0 {
		return d
	}
	return 2 * time.Second
}

func terminal(j *api.Job) bool {
	switch j.GetStatus() {
	case api.JobSuccess, api.JobFailed, api.JobCancelled:
		return true
	}
	return false
}

func jobStatus(j *api.Job) string { return ui.Status(j.GetStatus()) }

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
