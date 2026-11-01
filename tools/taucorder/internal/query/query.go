// Package query is every read taucorder's commands and TUI show: the fleet
// from the bridge's discovery view, and projects, deployments and jobs from
// the services, through generated clients over the bridge's route.
package query

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"time"

	"connectrpc.com/connect"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v2/registerv2connect"
	"github.com/taubyte/tau/pkg/stats"
)

type Fleet interface {
	Nodes() []stats.Node
}

type Client struct {
	fleet    Fleet
	register registerv2connect.RegisterServiceClient
	jobs     buildv2connect.JobServiceClient
}

// New reads fleet state from f and calls services through hc at base.
func New(f Fleet, hc connect.HTTPClient, base string) *Client {
	return &Client{
		fleet:    f,
		register: registerv2connect.NewRegisterServiceClient(hc, base),
		jobs:     buildv2connect.NewJobServiceClient(hc, base),
	}
}

var _ connect.HTTPClient = (*http.Client)(nil)

type Node struct {
	Peer     string   `json:"peer"`
	Ready    bool     `json:"ready"`
	Load     float64  `json:"load"`
	Age      Duration `json:"age"`
	Serving  []string `json:"serving"`
	Address  []string `json:"addresses"`
	Pending  int      `json:"databases_pending"`
	Revision uint64   `json:"fleet_revision"`
}

// Duration marshals as a Go duration string.
type Duration time.Duration

func (d Duration) String() string { return time.Duration(d).Round(time.Second).String() }

// MarshalText is the duration as text, so JSON stays readable.
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText reads what MarshalText wrote, so the JSON output round-trips.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	*d = Duration(v)
	return err
}

func (c *Client) Nodes() []Node {
	var out []Node
	for _, n := range c.fleet.Nodes() {
		out = append(out, Node{
			Peer: n.Peer, Ready: n.Ready, Load: n.Load, Age: Duration(n.Age),
			Serving: slices.Clone(n.Serving), Address: slices.Clone(n.Addresses),
			Pending: n.DatabasesPending, Revision: n.FleetRevision,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Peer < out[j].Peer })
	return out
}

// Service is one name the fleet serves and who serves it.
type Service struct {
	Name  string   `json:"name"`
	Nodes []string `json:"nodes"`
	Ready int      `json:"ready"`
}

// Services is every name some node reports serving, by name.
func (c *Client) Services() []Service {
	by := map[string]*Service{}
	for _, n := range c.Nodes() {
		for _, s := range n.Serving {
			row, ok := by[s]
			if !ok {
				row = &Service{Name: s}
				by[s] = row
			}
			row.Nodes = append(row.Nodes, n.Peer)
			if n.Ready {
				row.Ready++
			}
		}
	}
	out := make([]Service, 0, len(by))
	for _, s := range by {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Project is one linked project, by id and configuration repository, and the
// environments it is deployed to.
type Project struct {
	ID           string   `json:"id"`
	Config       string   `json:"config_repository"`
	Environments []string `json:"environments"`
}

// Projects lists every project the register has linked, with its deployments.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	list, err := c.register.ListProjects(ctx, connect.NewRequest(&registerv2.ListProjectsRequest{}))
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(list.Msg.GetProjects()))
	for _, rec := range list.Msg.GetProjects() {
		p := Project{ID: rec.GetId(), Config: rec.GetConfigRepository()}
		envs, err := c.register.Environments(ctx, connect.NewRequest(&registerv2.ProjectRequest{Project: p.ID}))
		switch {
		case err == nil:
			p.Environments = envs.Msg.GetEnvironments()
		case connect.CodeOf(err) == connect.CodeNotFound:
			// Never deployed: a project, with no environments.
		default:
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

type Job struct {
	ID         string    `json:"id"`
	Project    string    `json:"project"`
	Status     string    `json:"status"`
	Repository string    `json:"repository"`
	Ref        string    `json:"ref"`
	Commit     string    `json:"commit"`
	Attempt    int32     `json:"attempt"`
	Time       time.Time `json:"time"`
}

// Jobs lists the jobs of project, or of every project when project is "".
func (c *Client) Jobs(ctx context.Context, project string) ([]Job, error) {
	projects := []string{project}
	if project == "" {
		var err error
		if projects, err = c.projectIDs(ctx); err != nil {
			return nil, err
		}
	}
	var out []Job
	for _, p := range projects {
		jobs, err := c.jobsOf(ctx, p)
		if err != nil {
			return nil, err
		}
		out = append(out, jobs...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Time.Equal(out[j].Time) {
			return out[i].Time.After(out[j].Time)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (c *Client) jobsOf(ctx context.Context, project string) ([]Job, error) {
	s, err := c.jobs.List(ctx, connect.NewRequest(&buildv2.ProjectRef{Id: project}))
	if err != nil {
		return nil, err
	}
	var ids []string
	for s.Receive() {
		ids = append(ids, s.Msg().GetId())
	}
	s.Close()
	if err := s.Err(); err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(ids))
	for _, id := range ids {
		j, err := c.Job(ctx, id)
		if err != nil {
			return nil, err
		}
		j.Project = project
		out = append(out, j)
	}
	return out, nil
}

// Job is one job by id.
func (c *Client) Job(ctx context.Context, id string) (Job, error) {
	if id == "" {
		return Job{}, errors.New("no job id")
	}
	got, err := c.jobs.Get(ctx, connect.NewRequest(&buildv2.JobRef{Id: id}))
	if err != nil {
		return Job{}, err
	}
	m := got.Msg
	return Job{
		ID: m.GetId(), Status: status(m.GetStatus()), Repository: m.GetRepositoryKey(),
		Ref: m.GetRef(), Commit: m.GetCommitSha(), Attempt: m.GetAttempt(),
		Time: time.Unix(m.GetTimestamp(), 0).UTC(),
	}, nil
}

func status(s buildv2.JobStatus) string {
	switch s {
	case buildv2.JobStatus_JobStatusOpen:
		return "open"
	case buildv2.JobStatus_JobStatusClaimed:
		return "running"
	case buildv2.JobStatus_JobStatusSuccess:
		return "success"
	case buildv2.JobStatus_JobStatusFailed:
		return "failed"
	case buildv2.JobStatus_JobStatusCancelled:
		return "cancelled"
	}
	return s.String()
}

func (c *Client) projectIDs(ctx context.Context) ([]string, error) {
	list, err := c.register.ListProjects(ctx, connect.NewRequest(&registerv2.ListProjectsRequest{}))
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list.Msg.GetProjects()))
	for _, rec := range list.Msg.GetProjects() {
		ids = append(ids, rec.GetId())
	}
	return ids, nil
}
