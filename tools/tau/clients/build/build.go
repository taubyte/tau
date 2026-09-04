// Package build is tau-cli's client for the build service's public role.
//
// # It talks to the frontend, and only ever to the frontend
//
// The build service is split in two on purpose: the process a stranger's POST
// reaches holds no queue, no clone credentials and no register writer
// (services/build/boundary_test.go). A developer's CLI is on the stranger side
// of that line — it arrives over the internet with a provider token and
// nothing else — so it speaks the frontend's five HTTP routes and never the
// backend's RPC.
//
// # A log is followed, not downloaded
//
// v1's patrick could hand over a build log only once the build had ended and
// the whole of it had reached IPFS, so `tau logs` on a running build printed
// nothing. Here the log is followed from a byte offset while it is written,
// which is what makes watching a deploy possible at all — and a connection
// that drops resumes at the offset it reached rather than starting over.
package build

import (
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

	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
)

// Client is what tau-cli asks of a cloud's build service.
type Client interface {
	// Jobs are the ids of a project's builds, newest first.
	Jobs(ctx context.Context, projectID string) ([]string, error)
	// Job is one build's record.
	Job(ctx context.Context, jobID string) (*buildv1.Job, error)
	// Follow writes a running build's output to w from `from`, and returns the
	// offset it reached — including when it returns an error, so a caller that
	// reconnects resumes rather than repeats.
	Follow(ctx context.Context, jobID string, from int64, w io.Writer) (int64, error)
	Cancel(ctx context.Context, jobID string) error
	Retry(ctx context.Context, jobID string) error
}

// HTTP is a Client over the frontend's routes.
type HTTP struct {
	base  string
	token string
	http  *http.Client
}

var _ Client = (*HTTP)(nil)

// Option configures a client.
type Option func(*HTTP)

// WithHTTPClient replaces the transport. A follow is a long-lived response, so
// a client given here must not have a whole-request Timeout — see New.
func WithHTTPClient(c *http.Client) Option {
	return func(h *HTTP) {
		if c != nil {
			h.http = c
		}
	}
}

// New builds a client against a cloud's build frontend.
//
// THE DEFAULT TRANSPORT HAS NO Timeout, ON PURPOSE. http.Client.Timeout bounds
// the whole exchange including the body, and the body here is a build log that
// is written for as long as the build runs — a timeout would cut off exactly
// the case this client exists for. What is bounded is the part that can hang
// without producing anything: the connection, the TLS handshake and the
// response headers.
func New(base, token string, opts ...Option) (*HTTP, error) {
	if base == "" {
		return nil, errors.New("build: no address for the build service")
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("build: %q is not an address: %w", base, err)
	}

	h := &HTTP{
		base:  strings.TrimSuffix(base, "/"),
		token: token,
		http: &http.Client{Transport: &http.Transport{
			ResponseHeaderTimeout: 30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
		}},
	}
	for _, apply := range opts {
		apply(h)
	}
	return h, nil
}

func (h *HTTP) Jobs(ctx context.Context, projectID string) ([]string, error) {
	if projectID == "" {
		return nil, errors.New("build: no project")
	}

	body, err := h.get(ctx, "/jobs/"+url.PathEscape(projectID))
	if err != nil {
		return nil, err
	}
	defer body.Close()

	// THE ARRAY CAN BE LEFT OPEN, and that is not a client bug to paper over:
	// the frontend streams ids and, if the listing breaks after it has
	// committed to a 200, it stops mid-array so the truncation is VISIBLE. A
	// decode failure here is that, and it is reported rather than silently
	// returning the prefix as if it were the whole.
	var ids []string
	if err := json.NewDecoder(body).Decode(&ids); err != nil {
		return nil, fmt.Errorf("build: the job listing of %s was cut short: %w", projectID, err)
	}
	return ids, nil
}

func (h *HTTP) Job(ctx context.Context, jobID string) (*buildv1.Job, error) {
	if jobID == "" {
		return nil, errors.New("build: no job")
	}

	body, err := h.get(ctx, "/job/"+url.PathEscape(jobID))
	if err != nil {
		return nil, err
	}
	defer body.Close()

	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	// protojson over the generated message, because the route's body is
	// protojson over the same one — so a field added to build.proto arrives
	// here without an edit and cannot be spelled differently on the two sides.
	var job buildv1.Job
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, &job); err != nil {
		return nil, fmt.Errorf("build: reading job %s: %w", jobID, err)
	}
	return &job, nil
}

func (h *HTTP) Follow(ctx context.Context, jobID string, from int64, w io.Writer) (int64, error) {
	if jobID == "" {
		return from, errors.New("build: no job")
	}
	if from < 0 {
		return from, fmt.Errorf("build: offset %d is before the start of the log", from)
	}

	body, err := h.get(ctx, "/job/"+url.PathEscape(jobID)+"/logs?offset="+strconv.FormatInt(from, 10))
	if err != nil {
		return from, err
	}
	defer body.Close()

	// THE OFFSET IS COUNTED FROM WHAT WAS WRITTEN, not from what was read. A
	// caller resuming has to know where ITS OUTPUT ends, and bytes this client
	// read but could not hand on are bytes the caller does not have.
	at := from
	buf := make([]byte, 32<<10)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			written, writeErr := w.Write(buf[:n])
			at += int64(written)
			if writeErr != nil {
				return at, writeErr
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return at, nil
			}
			return at, readErr
		}
	}
}

func (h *HTTP) Cancel(ctx context.Context, jobID string) error {
	return h.post(ctx, "/cancel/", jobID)
}

func (h *HTTP) Retry(ctx context.Context, jobID string) error {
	return h.post(ctx, "/retry/", jobID)
}

func (h *HTTP) post(ctx context.Context, route, jobID string) error {
	if jobID == "" {
		return errors.New("build: no job")
	}

	body, err := h.do(ctx, http.MethodPost, route+url.PathEscape(jobID))
	if err != nil {
		return err
	}
	// The response carries nothing a caller did not already know; draining it
	// is what lets the connection be reused.
	io.Copy(io.Discard, body)
	return body.Close()
}

func (h *HTTP) get(ctx context.Context, route string) (io.ReadCloser, error) {
	return h.do(ctx, http.MethodGet, route)
}

// do sends one request and hands back a body the caller closes.
//
// AN ERROR STATUS IS READ AND CLOSED HERE, because the caller of a failed call
// has no body to stream and would have to remember to close one anyway.
func (h *HTTP) do(ctx context.Context, method, route string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, method, h.base+route, nil)
	if err != nil {
		return nil, err
	}
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}

	resp, err := h.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp.Body, nil
	}

	defer resp.Body.Close()
	return nil, statusError(resp)
}

// statusError turns the frontend's problem body into an error, and keeps the
// code so a caller can tell what to do about it.
func statusError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))

	var problem struct {
		Error string `json:"error"`
	}
	detail := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &problem) == nil && problem.Error != "" {
		detail = problem.Error
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrUnauthorized, detail)
	case http.StatusNotFound:
		// THE FRONTEND ANSWERS 404 FOR A JOB THE CALLER MAY NOT TOUCH as well
		// as for one that does not exist — telling a stranger "you may not read
		// this" confirms it exists — so the message says both rather than
		// asserting the one this client cannot distinguish.
		return fmt.Errorf("%w: %s", ErrNotFound, detail)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", ErrRateLimited, detail)
	}
	return fmt.Errorf("build: %s: %s", resp.Status, detail)
}

var (
	ErrUnauthorized = errors.New("build: this cloud did not accept the token; `tau login` again")
	ErrNotFound     = errors.New("build: no such job, or not one this token may see")
	ErrRateLimited  = errors.New("build: too many requests")
)
