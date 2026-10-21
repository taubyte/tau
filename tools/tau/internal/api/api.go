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

	"google.golang.org/protobuf/encoding/protojson"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// Job is the frontend's job record.
type Job = buildv2.Job

// Error is a refusal from the frontend: its status and the message it chose to
// give. A 404 is also what a denial looks like — the frontend does not say
// whether a project exists to someone who may not touch it.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		// A refusal the caller can act on — a forge they have not connected —
		// says so; anything else is the token.
		if e.Message != "" && e.Message != "unauthorized" {
			return "unauthorized: " + e.Message + " (see `tau forge connect`)"
		}
		return "unauthorized: the cloud did not accept this token (run `tau login`)"
	case http.StatusNotFound:
		return "not found, or not yours: the token cannot write to a repository this belongs to"
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
		return nil, &Error{Status: resp.StatusCode, Message: refusal.Error}
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

// Logs opens a job's log stream at byte offset from. The frontend keeps it open
// while the job runs; the caller reads until EOF and, if the connection drops
// first, reopens at the offset it had reached.
func (c *Client) Logs(ctx context.Context, jobID string, from int64) (io.ReadCloser, error) {
	q := url.Values{}
	if from > 0 {
		q.Set("offset", strconv.FormatInt(from, 10))
	}
	resp, err := c.do(ctx, http.MethodGet, []string{"job", jobID, "logs"}, q, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
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

// Inspection is a repository as a link sees it before claiming anything: the
// forge's spelling of it, whether the caller's connection may install on it,
// and whether this cloud's webhook for the project is already there.
type Inspection struct {
	Repository string `json:"repository"`
	Admin      bool   `json:"admin"`
	Hooked     bool   `json:"hooked"`
	Callback   string `json:"callback"`
}

// InspectRepository asks the cloud to read repository at its forge, under the
// caller's connection, for project id.
func (c *Client) InspectRepository(ctx context.Context, id, repository string) (*Inspection, error) {
	body, err := json.Marshal(map[string]string{"repository": repository})
	if err != nil {
		return nil, err
	}
	var out Inspection
	if err := c.json(ctx, http.MethodPost, []string{"link", id, "inspect"}, bytes.NewReader(body), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InstallLink asks the cloud to install project id's webhook, with the
// secret it stores, and deployKey (an authorized_keys line) on repository,
// under the caller's connection. replace removes this cloud's previous ones.
func (c *Client) InstallLink(ctx context.Context, id, repository, deployKey string, replace bool) error {
	body, err := json.Marshal(map[string]any{"repository": repository, "deploy_key": deployKey, "replace": replace})
	if err != nil {
		return err
	}
	return c.json(ctx, http.MethodPost, []string{"link", id, "install"}, bytes.NewReader(body), nil)
}

// Projects is every project the token's holder linked, as the forge serving
// host knows them.
func (c *Client) Projects(ctx context.Context, host string) ([]Project, error) {
	var out []Project
	err := c.json(ctx, http.MethodGet, []string{"forges", host, "projects"}, nil, &out)
	return out, err
}
