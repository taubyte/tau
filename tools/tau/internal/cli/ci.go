package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
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

// awaitJob follows a job to its end: its live log while it runs, the log it
// stored when it ended before a byte was followed, and its record at the end.
func awaitJob(cmd *cobra.Command, cl *api.Client, job string, log io.Writer) (*api.Job, error) {
	ctx := cmd.Context()
	var offset int64
	for {
		j, err := cl.Job(ctx, job)
		if err != nil {
			return nil, err
		}
		if terminal(j) {
			if offset == 0 {
				storedLog(cmd, cl, j, log)
			}
			return j, nil
		}
		if j.GetStatus() == api.JobRunning {
			stream := cl.Stream(ctx, job, offset)
			n, err := stream.CopyTo(log)
			offset = n
			// A FOLLOW REFUSED IS A BUILD NOT RUNNING THERE YET OR ANY MORE,
			// which the next look at its record says.
			var refusal *api.Error
			if err != nil && !errors.As(err, &refusal) {
				return nil, err
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval(cmd)):
		}
	}
}

func interval(cmd *cobra.Command) time.Duration {
	if d := flagDuration(cmd, "interval"); d > 0 {
		return d
	}
	return 2 * time.Second
}

// storedLog prints the last log a finished job stored.
func storedLog(cmd *cobra.Command, cl *api.Client, j *api.Job, log io.Writer) {
	keys := make([]string, 0, len(j.GetLogs()))
	for k := range j.GetLogs() {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)
	body, err := cl.StoredLog(cmd.Context(), j.GetId(), keys[len(keys)-1])
	if err != nil {
		fmt.Fprintf(log, "the job's log cannot be read: %v\n", err)
		return
	}
	defer body.Close()
	io.Copy(log, body)
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
