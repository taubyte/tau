// Package api is the build frontend's public HTTP API, as the CLI calls it
// (services/build/frontend/ingress.go is the other side). Jobs are decoded into
// the generated tau.build.v2 types with protojson — the frontend encodes them
// that way — so nothing here restates the schema.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// Job is the frontend's job record.
type Job = buildv2.Job

// Error is a refusal from the frontend: its status, the message it chose to
// give, and the headers it came with. A 404 is also what a denial looks like —
// the frontend does not say whether a project exists to someone who may not
// touch it.
type Error struct {
	Status  int
	Message string
	Header  http.Header
}

func (e *Error) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "unauthorized: the cloud did not accept this token (run `tau login`)"
	case http.StatusNotFound:
		return "not found, or not yours: you hold no role on this project"
	}
	if e.Message == "" {
		return fmt.Sprintf("the cloud answered %d", e.Status)
	}
	return fmt.Sprintf("the cloud answered %d: %s", e.Status, e.Message)
}

// IsStatus reports whether err is a refusal with status.
func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

// Client calls one cloud's frontend with one token: the tau token.
type Client struct {
	base   *url.URL
	tokens Tokens
	// http is the caller's client; authed is it with the tau token added.
	http   *http.Client
	authed *http.Client
}

// New builds a client for the frontend at base. A nil tokens is a client with
// no credential, for signing in.
func New(base string, tokens Tokens, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%q is not a cloud URL (want https://host[:port])", base)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	if tokens == nil {
		tokens = Static("")
	}
	authed := *hc
	authed.Transport = &bearer{next: transport(hc), tokens: tokens}
	return &Client{base: u, tokens: tokens, http: hc, authed: &authed}, nil
}

// Base is the frontend's URL.
func (c *Client) Base() string { return c.base.String() }

func (c *Client) do(ctx context.Context, method string, path []string, query url.Values, body io.Reader) (*http.Response, error) {
	u := *c.base
	for _, seg := range path {
		u.Path += "/" + seg
	}
	raw := c.base.EscapedPath()
	for _, seg := range path {
		raw += "/" + url.PathEscape(seg)
	}
	u.RawPath = raw
	if query != nil {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	token, err := c.tokens.Access(ctx)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, errors.New("no token: run `tau login`, or set TAU_TOKEN")
	}
	resp, err := c.authed.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var refusal struct {
			Error string `json:"error"`
		}
		json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&refusal)
		return nil, &Error{Status: resp.StatusCode, Message: refusal.Error, Header: resp.Header}
	}
	return resp, nil
}

func (c *Client) json(ctx context.Context, method string, path []string, body io.Reader, out any) error {
	resp, err := c.do(ctx, method, path, nil, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Jobs lists a project's job ids.
func (c *Client) Jobs(ctx context.Context, projectID string) ([]string, error) {
	var ids []string
	err := c.json(ctx, http.MethodGet, []string{"jobs", projectID}, nil, &ids)
	return ids, err
}

// Job reads one job.
func (c *Client) Job(ctx context.Context, jobID string) (*Job, error) {
	resp, err := c.do(ctx, http.MethodGet, []string{"job", jobID}, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	job := &Job{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, job); err != nil {
		return nil, fmt.Errorf("reading job %s: %w", jobID, err)
	}
	return job, nil
}

// Cancel cancels a job.
func (c *Client) Cancel(ctx context.Context, jobID string) error {
	return c.json(ctx, http.MethodPost, []string{"cancel", jobID}, nil, nil)
}

// Retry queues a job again.
func (c *Client) Retry(ctx context.Context, jobID string) error {
	return c.json(ctx, http.MethodPost, []string{"retry", jobID}, nil, nil)
}

// attemptHeader names the run a log response is about.
const attemptHeader = "X-Job-Attempt"

// errUnnamedRun is a log response that does not say which run it is, which no
// read can resume.
var errUnnamedRun = errors.New("the cloud did not say which run the log is")

// logs opens one run of a job's log at byte offset from — the run attempt
// names, or with 0 the job's current one — and returns the run the response is
// about. The frontend keeps it open while the run is to come and while it
// runs, or with snapshot ends it at what the log holds now; the caller reads
// until EOF and, if the connection drops first, reopens that run at the offset
// it had reached.
func (c *Client) logs(ctx context.Context, jobID string, attempt int32, from int64, snapshot bool) (io.ReadCloser, int32, error) {
	q := url.Values{}
	if attempt > 0 {
		q.Set("attempt", strconv.FormatInt(int64(attempt), 10))
	}
	if from > 0 {
		q.Set("offset", strconv.FormatInt(from, 10))
	}
	if snapshot {
		q.Set("snapshot", "true")
	}
	resp, err := c.do(ctx, http.MethodGet, []string{"job", jobID, "logs"}, q, nil)
	if err != nil {
		return nil, 0, err
	}
	run := runOf(resp.Header)
	if run == 0 {
		resp.Body.Close()
		return nil, 0, errUnnamedRun
	}
	return resp.Body, run, nil
}

// runOf is the run a log response names, or 0 for none.
func runOf(h http.Header) int32 {
	run, err := strconv.ParseInt(h.Get(attemptHeader), 10, 32)
	if err != nil || run < 1 {
		return 0
	}
	return int32(run)
}

// Secrets lists the names set in a project's environment. Values never come
// back: the frontend has no route that reads one.
func (c *Client) Secrets(ctx context.Context, projectID, environment string) ([]string, error) {
	var names []string
	err := c.json(ctx, http.MethodGet, []string{"secrets", projectID, environment}, nil, &names)
	return names, err
}

// SetSecret sets one secret's value.
func (c *Client) SetSecret(ctx context.Context, projectID, environment, name string, value []byte) error {
	return c.json(ctx, http.MethodPut, []string{"secrets", projectID, environment, name}, bytes.NewReader(value), nil)
}

// UnsetSecret removes one secret.
func (c *Client) UnsetSecret(ctx context.Context, projectID, environment, name string) error {
	return c.json(ctx, http.MethodDelete, []string{"secrets", projectID, environment, name}, nil, nil)
}

// Project is a project linked on a cloud: project.yaml's id, the
// configuration repository that claimed it, and the code repositories it
// attaches.
type Project struct {
	ID     string   `json:"id"`
	Config string   `json:"config_repository_key"`
	Code   []string `json:"code_repository_keys"`
}

// LinkProject claims project.yaml's id on the cloud for a configuration
// repository and attaches code repositories. An id another repository claimed
// is a 409 whose message says so.
func (c *Client) LinkProject(ctx context.Context, id, configKey string, codeKeys []string) (*Project, error) {
	if codeKeys == nil {
		codeKeys = []string{}
	}
	body, err := json.Marshal(map[string]any{
		"config_repository_key": configKey,
		"code_repository_keys":  codeKeys,
	})
	if err != nil {
		return nil, err
	}
	var p Project
	if err := c.json(ctx, http.MethodPost, []string{"projects", id}, bytes.NewReader(body), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Projects is every project the caller owns or holds a role on.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var out []Project
	err := c.json(ctx, http.MethodGet, []string{"projects"}, nil, &out)
	return out, err
}

// Fqdn is the name the cloud serves under; its build frontend is
// build-frontend.<fqdn>.
func (c *Client) Fqdn(ctx context.Context) (string, error) {
	var out struct {
		Fqdn string `json:"fqdn"`
	}
	if err := c.json(ctx, http.MethodGet, []string{"cloud"}, nil, &out); err != nil {
		return "", err
	}
	if out.Fqdn == "" {
		return "", errors.New("the cloud named no fqdn")
	}
	return out.Fqdn, nil
}

// Grant is one role an owner gave a tau user on a project.
type Grant struct {
	ID   string    `json:"id"`
	User string    `json:"user"`
	Role string    `json:"role"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
}

// Grants lists a project's grants; owners only.
func (c *Client) Grants(ctx context.Context, projectID string) ([]Grant, error) {
	var out []Grant
	err := c.json(ctx, http.MethodGet, []string{"projects", projectID, "grants"}, nil, &out)
	return out, err
}

// GrantRole gives user role on a project and returns the grant's id.
func (c *Client) GrantRole(ctx context.Context, projectID, user, role string) (string, error) {
	body, err := json.Marshal(map[string]string{"user": user, "role": role})
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	err = c.json(ctx, http.MethodPost, []string{"projects", projectID, "grants"}, bytes.NewReader(body), &out)
	return out.ID, err
}

// Revoke withdraws one grant.
func (c *Client) Revoke(ctx context.Context, projectID, grantID string) error {
	return c.json(ctx, http.MethodDelete, []string{"projects", projectID, "grants", grantID}, nil, nil)
}

// DomainClaim is the TXT record that proves a custom domain is a project's.
type DomainClaim struct {
	Entry string `json:"entry"`
	Type  string `json:"type"`
	Token string `json:"token"`
}

// ClaimDomain is the record to set for fqdn; the project's owner only.
func (c *Client) ClaimDomain(ctx context.Context, projectID, fqdn string) (*DomainClaim, error) {
	var out DomainClaim
	if err := c.json(ctx, http.MethodPost, []string{"domain", fqdn, "for", projectID}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Cloud is what a cloud says about itself.
type Cloud struct {
	Fqdn string `json:"fqdn"`
	// Uploads is a development cloud that takes a developer's build straight
	// in; no cloud does.
	Uploads bool `json:"uploads"`
}

// Cloud asks the cloud what it is.
func (c *Client) Cloud(ctx context.Context) (Cloud, error) {
	var out Cloud
	err := c.json(ctx, http.MethodGet, []string{"cloud"}, nil, &out)
	return out, err
}

type queued struct {
	JobID string `json:"job_id"`
}

// Build queues a build of repositoryKey at commit, for ref, by hand.
func (c *Client) Build(ctx context.Context, projectID, repositoryKey, ref, commit string) (string, error) {
	body, err := json.Marshal(map[string]string{"repository_key": repositoryKey, "ref": ref, "commit_sha": commit})
	if err != nil {
		return "", err
	}
	var out queued
	err = c.json(ctx, http.MethodPost, []string{"build", projectID}, bytes.NewReader(body), &out)
	return out.JobID, err
}

// Upload carries a developer's build into a development cloud, which queues it
// as a commit, for ref, of repository — the project's configuration when "".
func (c *Client) Upload(ctx context.Context, projectID, repository, ref string, bundle io.Reader) (string, error) {
	q := url.Values{"ref": {ref}}
	if repository != "" {
		q.Set("repository", repository)
	}
	resp, err := c.do(ctx, http.MethodPost, []string{"upload", projectID}, q, bundle)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out queued
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out.JobID, err
}

// Deployments are the environments a project has published.
func (c *Client) Deployments(ctx context.Context, projectID string) ([]string, error) {
	var out []string
	err := c.json(ctx, http.MethodGet, []string{"projects", projectID, "deployments"}, nil, &out)
	return out, err
}

// Published is the compiled object a deployment runs.
func (c *Client) Published(ctx context.Context, projectID, environment string) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, []string{"projects", projectID, "deployments", environment}, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// The job statuses a caller waits through.
const (
	JobQueued    = buildv2.JobStatus_JobStatusOpen
	JobRunning   = buildv2.JobStatus_JobStatusClaimed
	JobSuccess   = buildv2.JobStatus_JobStatusSuccess
	JobFailed    = buildv2.JobStatus_JobStatusFailed
	JobCancelled = buildv2.JobStatus_JobStatusCancelled
)
