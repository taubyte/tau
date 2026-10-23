package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/public"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/tools/tau/internal/api"
	"github.com/taubyte/tau/tools/tau/internal/config"
	"github.com/taubyte/tau/tools/tau/internal/project"
	"github.com/taubyte/tau/tools/tau/internal/ui"
)

// repoKey is a repository key from whatever a developer pastes: a clone URL,
// an scp-style git address, or the key itself. It is keyed through the forge
// registry, so every address a forge hands out for one repository is that one
// repository's key.
func repoKey(forges *forge.Registry, s string) (repokey.Key, error) {
	spelled := s
	if !strings.Contains(s, "://") && !strings.Contains(s, "@") {
		spelled = "https://" + s
	}
	k, err := forges.Key(spelled)
	if err != nil {
		return repokey.Key{}, fmt.Errorf("%q: %w", s, err)
	}
	return k, nil
}

// keysFor is the registry repository addresses are keyed through: the
// injected one, or the public forges. Keying asks nothing of a policy.
func keysFor(env Env) (*forge.Registry, error) {
	if env.Forges != nil {
		return env.Forges, nil
	}
	return public.Registry(forge.Policy{})
}

// originOf is the directory's git origin, which is what a config repository
// usually is.
func originOf(ctx context.Context, dir string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// projectsCommand lists the projects the caller owns or holds a role on.
func projectsCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "the projects you own or were granted a role on",
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			ps, err := cl.Projects(cmd.Context())
			if err != nil {
				return err
			}
			t := theme(env)
			var b strings.Builder
			if len(ps) == 0 {
				b.WriteString(t.Muted.Render("no projects linked yet: run `tau link` in a project") + "\n")
			}
			for _, p := range ps {
				fmt.Fprintf(&b, "%s  %s\n", t.Accent.Render(p.ID), p.Config)
				for _, c := range p.Code {
					b.WriteString(t.Muted.Render("    code "+c) + "\n")
				}
			}
			if ps == nil {
				ps = []api.Project{}
			}
			return emit(cmd, env, ps, b.String())
		},
	}
	return cmd
}

// jobsOf reads up to limit of a project's jobs in full.
func jobsOf(ctx context.Context, cl *api.Client, projectID string, limit int) ([]*api.Job, error) {
	ids, err := cl.Jobs(ctx, projectID)
	if err != nil {
		return nil, err
	}
	jobs := make([]*api.Job, 0, len(ids))
	for _, id := range ids {
		j, err := cl.Job(ctx, id)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	slices.SortStableFunc(jobs, func(a, b *api.Job) int { return int(b.GetTimestamp() - a.GetTimestamp()) })
	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

func jobsCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "list the project's build jobs, newest first",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := projectID(cmd)
			if err != nil {
				return err
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			jobs, err := jobsOf(cmd.Context(), cl, id, flagInt(cmd, "limit"))
			if err != nil {
				return err
			}
			return emit(cmd, env, jobJSON(jobs), theme(env).Jobs(jobs))
		},
	}
	addDir(cmd)
	addProject(cmd)
	cmd.Flags().Int("limit", 20, "at most this many (0 for all)")
	return cmd
}

// jobView is a job as --output json prints it: the status by name, not number.
type jobView struct {
	ID         string            `json:"id"`
	Status     string            `json:"status"`
	Repository string            `json:"repository_key"`
	Ref        string            `json:"ref"`
	Commit     string            `json:"commit_sha"`
	Timestamp  int64             `json:"timestamp"`
	Attempt    int32             `json:"attempt"`
	Built      map[string]string `json:"asset_cid,omitempty"`
}

func viewOf(j *api.Job) jobView {
	return jobView{ID: j.GetId(), Status: ui.Status(j.GetStatus()), Repository: j.GetRepositoryKey(), Ref: j.GetRef(),
		Commit: j.GetCommitSha(), Timestamp: j.GetTimestamp(), Attempt: j.GetAttempt(), Built: j.GetAssetCid()}
}

func jobJSON(jobs []*api.Job) []jobView {
	out := make([]jobView, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, viewOf(j))
	}
	return out
}

func jobArg(args []string) (string, error) {
	id := arg(args, 0)
	if id == "" {
		return "", errors.New("a job id is required (see `tau jobs`)")
	}
	return id, nil
}

func jobCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job <job-id>",
		Short: "show one job",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := jobArg(args)
			if err != nil {
				return err
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			j, err := cl.Job(cmd.Context(), id)
			if err != nil {
				return err
			}
			return emit(cmd, env, viewOf(j), theme(env).Job(j))
		},
	}
	return cmd
}

func logsCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <job-id>",
		Short: "print a job's log; --follow keeps reading while it runs, resuming across drops",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := jobArg(args)
			if err != nil {
				return err
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			return showLog(cmd, env, cl, id, flagInt64(cmd, "offset"), flagBool(cmd, "follow"))
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "follow while the job runs, reconnecting at the offset reached")
	cmd.Flags().Int64("offset", 0, "start at this byte of the log")
	return cmd
}

// showLog prints a job's log. At a terminal, following draws the live view;
// anywhere else the bytes go to stdout as they arrive.
func showLog(cmd *cobra.Command, env Env, cl *api.Client, id string, from int64, follow bool) error {
	// A FINISHED JOB'S LOG IS THE ONE IT STORED. Following asks the node
	// writing the log, and once the build ends no node is: the stream would be
	// empty, and an empty log reads as a build that printed nothing.
	if j, err := cl.Job(cmd.Context(), id); err == nil && terminal(j) {
		var all bytes.Buffer
		if err := storedLogOf(cmd, cl, j, &all); err != nil {
			return err
		}
		if from > int64(all.Len()) {
			from = int64(all.Len())
		}
		_, err := env.Stdout.Write(all.Bytes()[from:])
		return err
	}
	stream := cl.Stream(cmd.Context(), id, from)
	if !follow {
		stream.Retries = 0
	}
	if follow && env.Interactive && !jsonOut(cmd) && env.Program != nil {
		m := ui.NewFollow("job "+id, stream, theme(env))
		if _, err := env.Program(m, true); err != nil {
			return err
		}
		defer stream.Close()
		if m.Err != nil {
			return m.Err
		}
		return nil
	}
	_, err := stream.CopyTo(env.Stdout)
	return err
}

func cancelCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "cancel a job",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := jobArg(args)
			if err != nil {
				return err
			}
			if err := confirm(cmd, env, "Cancel job "+id+"?"); err != nil {
				return err
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			if err := cl.Cancel(cmd.Context(), id); err != nil {
				return err
			}
			return emit(cmd, env, map[string]string{"cancelled": id}, theme(env).Done("cancelled "+id))
		},
	}
	return cmd
}

func retryCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retry <job-id>",
		Short: "queue a job again",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := jobArg(args)
			if err != nil {
				return err
			}
			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			if err := cl.Retry(cmd.Context(), id); err != nil {
				return err
			}
			return emit(cmd, env, map[string]string{"retried": id}, theme(env).Done("queued "+id+" again"))
		},
	}
	return cmd
}

// deployCommand is push-driven, because that is the only way a v2 cloud builds:
// the frontend has no manual trigger, only the forge's webhook. So deploying is
// pushing; this says what to push and, with --wait, waits for the job the push
// starts and follows its log.
func deployCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "deploy by pushing: says what to push, and --wait follows the build it starts",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := flagString(cmd, "dir")
			id, err := projectID(cmd)
			if err != nil {
				return err
			}
			t := theme(env)
			var b strings.Builder
			if link, err := config.ReadLink(dir); err == nil {
				fmt.Fprintf(&b, "Deploys are push-driven. Push the configuration repository to deploy it:\n  %s\n", t.Accent.Render(link.Config))
				for _, code := range link.Code {
					fmt.Fprintf(&b, "and the code repository to build it:\n  %s\n", t.Accent.Render(code))
				}
			} else {
				b.WriteString("Deploys are push-driven: push the project's repositories.\n")
			}
			if !flagBool(cmd, "wait") {
				b.WriteString(t.Muted.Render("run `tau deploy --wait` to follow the build the push starts") + "\n")
				return emit(cmd, env, map[string]string{"project": id, "trigger": "push"}, b.String())
			}
			if !jsonOut(cmd) {
				fmt.Fprint(env.Stdout, b.String())
			}

			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			seen, err := cl.Jobs(cmd.Context(), id)
			if err != nil {
				return err
			}
			deadline := time.Now().Add(flagDuration(cmd, "timeout"))
			var next string
			poll := func() (string, bool, error) {
				ids, err := cl.Jobs(cmd.Context(), id)
				if err != nil {
					return "", false, err
				}
				for _, j := range ids {
					if !slices.Contains(seen, j) {
						next = j
						return "job " + j + " started", true, nil
					}
				}
				if time.Now().After(deadline) {
					return "", false, errors.New("no job started before --timeout")
				}
				return "waiting for a push…", false, nil
			}

			if env.Interactive && !jsonOut(cmd) && env.Program != nil {
				m := ui.NewWait("waiting for the build", flagDuration(cmd, "interval"), poll, t)
				if _, err := env.Program(m, false); err != nil {
					return err
				}
				if m.Canceled {
					return errors.New("stopped waiting")
				}
				if m.Err != nil {
					return m.Err
				}
			} else {
				for {
					_, done, err := poll()
					if err != nil {
						return err
					}
					if done {
						break
					}
					select {
					case <-cmd.Context().Done():
						return cmd.Context().Err()
					case <-time.After(flagDuration(cmd, "interval")):
					}
				}
			}
			if jsonOut(cmd) {
				return emit(cmd, env, map[string]string{"project": id, "job": next}, "")
			}
			fmt.Fprint(env.Stdout, t.Done("job "+next+" started"))
			return showLog(cmd, env, cl, next, 0, true)
		},
	}
	addDir(cmd)
	addProject(cmd)
	cmd.Flags().BoolP("wait", "w", false, "wait for the next job and follow its log")
	cmd.Flags().Duration("timeout", 10*time.Minute, "how long --wait waits for a job to appear")
	cmd.Flags().Duration("interval", 2*time.Second, "how often --wait looks")
	return cmd
}

func statusCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "the profile, the linked project, and its latest jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			t := theme(env)
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			profile, p, perr := cfg.Resolve(flagString(cmd, "profile"), flagString(cmd, "cloud"))
			out := map[string]any{"profile": profile, "cloud": p.Cloud, "logged_in": p.Token != ""}
			var b strings.Builder
			field := func(k, v string) { fmt.Fprintf(&b, "%s %s\n", t.Muted.Render(fmt.Sprintf("%-8s", k)), v) }
			field("profile", profile)
			if perr != nil {
				field("cloud", t.Warn.Render(perr.Error()))
			} else {
				field("cloud", p.Cloud)
			}

			if dir := flagString(cmd, "dir"); dir != "" {
				if pr, err := project.Open(dir); err == nil && pr.IsProject() {
					field("project", pr.Name())
					out["name"] = pr.Name()
				}
			}
			id, err := projectID(cmd)
			if err != nil {
				field("linked", t.Warn.Render("no — run `tau link`"))
				return emit(cmd, env, out, b.String())
			}
			field("linked", id)
			out["project_id"] = id
			if perr != nil || p.Token == "" {
				return emit(cmd, env, out, b.String())
			}
			cl, err := profileClient(env, cfg, profile, p)
			if err != nil {
				return err
			}
			jobs, err := jobsOf(cmd.Context(), cl, id, 5)
			if err != nil {
				field("jobs", t.Bad.Render(err.Error()))
				return emit(cmd, env, out, b.String())
			}
			out["jobs"] = jobJSON(jobs)
			b.WriteString(t.Jobs(jobs))
			return emit(cmd, env, out, b.String())
		},
	}
	addDir(cmd)
	addProject(cmd)
	return cmd
}
