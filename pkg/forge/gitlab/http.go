package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// maxBody bounds what one API response may cost this process. A listing page of
// a hundred projects is tens of kilobytes; anything at this size is either a
// misrouted request or an instance answering with something that is not its API,
// and reading it into memory to find that out is the wrong order.
const maxBody = 4 << 20

// maxHostString bounds a string the INSTANCE chose before it may reach an error.
// A host that picks how long a log line is picks how much of a disk it costs.
const maxHostString = 256

// response is one API answer, read whole before anything is decided about it, so
// the status, the headers and the body are all in hand when the failure taxonomy
// is applied.
//
// method and path are what THIS DRIVER SENT, and they are here because they are
// the whole of what an error may say about a failure: see fault.
type response struct {
	status int
	header http.Header
	body   []byte

	method string
	path   string
}

// call performs one request. It never decides what a status MEANS — fault does
// that, with the repository in hand, because whether a 404 is *NotFoundError
// depends on whether the request named a repository at all.
func (c *client) call(ctx context.Context, rq request, body any) (*response, error) {
	return do(ctx, c.api, c.cfg, rq, body)
}

// do IS THE ONLY PLACE IN THIS PACKAGE THAT BUILDS AN *http.Request, and it
// takes a ROUTE rather than a URL, so a path that reaches the wire is a path
// some declared route composed with every segment escaped by the one function
// that escapes segments. Both of this driver's http.Clients — the caller's and
// the deployment's — come through here.
func do(ctx context.Context, api *http.Client, cfg *config, rq request, body any) (*response, error) {
	host, method := cfg.host, rq.route.method
	u, err := rq.url(cfg.base)
	if err != nil {
		return nil, err
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("gitlab: encoding a %s request to %s: %w", method, host, err)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), payload)
	if err != nil {
		return nil, fmt.Errorf("gitlab: building a %s request to %s: %w", method, host, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := api.Do(req)
	if err != nil {
		// An outage, a refused connection, a request the pin refused to make.
		// It is a plain error and nothing in the taxonomy, so no consumer can
		// pattern-match it into a verdict.
		//
		// A REDIRECT IS NOT HERE. This client follows none, so a 3xx arrives
		// below as the response it is and fault names its status.
		return nil, fmt.Errorf("gitlab: %s %s: %w", method, host, err)
	}
	defer resp.Body.Close()

	read, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("gitlab: reading a %s answer from %s: %w", method, host, err)
	}
	return &response{
		status: resp.StatusCode,
		header: resp.Header,
		body:   read,
		method: method,
		path:   u.EscapedPath(),
	}, nil
}

// fault turns a non-2xx answer into the right member of the failure taxonomy, or
// nil. key is the repository the request named, or nil when it named none.
//
// THE MAPPING IS DELIBERATELY NARROW. Only 401 is a statement about the
// credential and only 404-on-a-named-repository is "no such repository";
// everything else is a plain error, because the alternative is a consumer
// reading an instance's 403 — which GitLab returns for an archived project, a
// blocked user, a missing scope and a permission the caller genuinely lacks —
// as a verdict it can act on.
//
// NOTHING THE INSTANCE WROTE IS IN THE ERROR. Not the body it composed, not the
// reason phrase on its status line: an error here names the method and the path
// THIS DRIVER built, the numeric status, and Go's own text for that number. The
// instance receives the caller's credential on every request, so a sentence it
// gets to choose is a sentence it can put that credential in — and an error
// string is the most-copied thing in a log. It is the same refusal the github
// driver's package comment states, and this driver used to render the body here.
//
// The two answers that do NOT come through here are safe for the same reason and
// not by exception: the 401 carries a fixed sentence, and the 404 carries the
// key TAU asked about. Neither reads a single byte the instance chose.
func (c *client) fault(r *response, key *repokey.Key) error {
	return fault(c.cfg.host, r, key)
}

func fault(host string, r *response, key *repokey.Key) error {
	switch {
	case r.status >= 200 && r.status < 300:
		return nil
	case r.status == http.StatusUnauthorized:
		// Never the credential itself, and never the instance's echo of it: an
		// error string is the most-copied thing in a log.
		return &forge.CredentialError{Host: host, Reason: "the instance refused the credential (401)"}
	case r.status == http.StatusNotFound && key != nil:
		return &forge.NotFoundError{Host: host, Key: *key}
	}
	return &statusError{host: host, method: r.method, path: r.path, status: r.status}
}

// statusError is a non-2xx answer the taxonomy does not name.
type statusError struct {
	host, method, path string
	status             int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("gitlab: %s answered %s %q with %s", e.host, e.method, e.path, statusLine(e.status))
}

// Refused is a 4xx: the instance's own answer refusing the request.
func (e *statusError) Refused() bool { return e.status >= 400 && e.status < 500 }

// decode reads a successful answer's body.
func decode(r *response, out any) error {
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(r.body, out); err != nil {
		return fmt.Errorf("gitlab: answer is not the JSON this endpoint documents: %w", err)
	}
	return nil
}

// get is the whole of a read: request, taxonomy, decode.
func (c *client) get(ctx context.Context, rq request, key *repokey.Key, out any) (*response, error) {
	r, err := c.call(ctx, rq, nil)
	if err != nil {
		return nil, err
	}
	if err := c.fault(r, key); err != nil {
		return nil, err
	}
	return r, decode(r, out)
}

// send is the whole of a write.
func (c *client) send(ctx context.Context, rq request, key *repokey.Key, body, out any) error {
	r, err := c.call(ctx, rq, body)
	if err != nil {
		return err
	}
	if err := c.fault(r, key); err != nil {
		return err
	}
	return decode(r, out)
}

// statusLine renders a status with Go's own text for it, which is a table keyed
// by the number rather than the reason phrase the instance sent.
func statusLine(status int) string {
	if text := http.StatusText(status); text != "" {
		return strconv.Itoa(status) + " " + text
	}
	return strconv.Itoa(status)
}
