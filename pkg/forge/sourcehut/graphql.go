package sourcehut

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const (
	// maxResponseBody bounds what one answer may make this process hold.
	maxResponseBody = 8 << 20
	// maxHostString bounds any string a forge chose before an error carries it.
	maxHostString = 256
	// maxDrainOnError is how much of a refused answer is read to reuse the
	// connection.
	maxDrainOnError = 4 << 10
)

// client is one credential against one git.sr.ht.
type client struct {
	d    *driver
	http *http.Client
}

var (
	_ forge.Client         = (*client)(nil)
	_ forge.Lister         = (*client)(nil)
	_ forge.Creator        = (*client)(nil)
	_ forge.Deleter        = (*client)(nil)
	_ forge.DeployKeys     = (*client)(nil)
	_ forge.PushHooks      = (*client)(nil)
	_ forge.CloneAddresses = (*client)(nil)
)

func (d *driver) bind(token string) *client {
	return &client{
		d: d,
		http: &http.Client{
			// A redirect is never followed: the endpoint is one fixed URL, so
			// a hop anywhere — another host, or this host's another path — is
			// the forge choosing what the question was about.
			CheckRedirect: noRedirect,
			Transport: forge.PinnedTransport(d.host, d.rt, func(h http.Header) {
				if token != "" {
					h.Set("Authorization", "Bearer "+token)
				}
			}),
		},
	}
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (c *client) Host() string { return c.d.host }

// statusError is an answer that was not a 2xx.
type statusError struct {
	Operation string
	Status    int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("forge/sourcehut: %s answered %d %s", e.Operation, e.Status, http.StatusText(e.Status))
}

// graphqlError is a 2xx whose body carried GraphQL errors. The messages are
// the forge's and are bounded; nothing a caller sent is quoted back.
type graphqlError struct {
	Operation string
	Messages  []string
}

func (e *graphqlError) Error() string {
	return fmt.Sprintf("forge/sourcehut: %s was refused: %s", e.Operation, strings.Join(e.Messages, "; "))
}

// document is one fixed GraphQL operation. Its name is what a failure says and
// what the operationName field carries.
type document struct {
	name string
	text string
}

type gqlRequest struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName"`
	Variables     map[string]any `json:"variables,omitempty"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// send runs one document. out receives the response's data.
func (c *client) send(ctx context.Context, doc document, vars map[string]any, out any) error {
	enc, err := json.Marshal(gqlRequest{Query: doc.text, OperationName: doc.name, Variables: vars})
	if err != nil {
		return fmt.Errorf("forge/sourcehut: encoding %s: %w", doc.name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.d.endpoint.String(), bytes.NewReader(enc))
	if err != nil {
		return fmt.Errorf("forge/sourcehut: building %s: %w", doc.name, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("forge/sourcehut: %s: %w", doc.name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainOnError))
		return &statusError{Operation: doc.name, Status: resp.StatusCode}
	}
	var r gqlResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&r); err != nil {
		return fmt.Errorf("forge/sourcehut: %s answered a body this driver cannot read: %w", doc.name, err)
	}
	if len(r.Errors) > 0 {
		ge := &graphqlError{Operation: doc.name}
		for _, e := range r.Errors {
			ge.Messages = append(ge.Messages, bounded(e.Message))
		}
		return ge
	}
	if len(r.Data) == 0 || string(r.Data) == "null" {
		return fmt.Errorf("forge/sourcehut: %s answered no data", doc.name)
	}
	if err := json.Unmarshal(r.Data, out); err != nil {
		return fmt.Errorf("forge/sourcehut: %s answered data this driver cannot read: %w", doc.name, err)
	}
	return nil
}

// classify turns a refused credential into the error that says so. A 403 is
// one too: sourcehut answers a token lacking a grant with it, and that is the
// credential's answer and not a repository's.
func (c *client) classify(err error) error {
	var se *statusError
	if errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden) {
		return &forge.CredentialError{Host: c.d.host, Reason: "the forge refused this credential"}
	}
	return err
}

// bounded cuts a string somebody else chose to a length an error may carry.
func bounded(s string) string {
	if len(s) <= maxHostString {
		return s
	}
	return s[:maxHostString] + "…"
}

// ─────────────────────────────── repositories ───────────────────────────────

// repoFields is every field this driver reads off a Repository.
const repoFields = `id rid name visibility access owner { canonicalName } HEAD { name }`

type repoJSON struct {
	ID         int64  `json:"id"`
	RID        string `json:"rid"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	Access     string `json:"access"`
	Owner      struct {
		CanonicalName string `json:"canonicalName"`
	} `json:"owner"`
	HEAD *struct {
		Name string `json:"name"`
	} `json:"HEAD"`
}

// keyOf derives the key a repository answer DESCRIBES, from its owner and
// name, never from the key that was asked about.
func (c *client) keyOf(owner, name, asked string) (repokey.Key, error) {
	switch {
	case len(owner) < 2 || owner[0] != '~' || name == "":
		return repokey.Key{}, fmt.Errorf("forge/sourcehut: %s described %s with no ~owner and name, so there is nothing to derive a key from", c.d.host, asked)
	case len(owner)+len(name) > maxHostString:
		return repokey.Key{}, fmt.Errorf("forge/sourcehut: %s described %s with an implausibly long name", c.d.host, asked)
	case strings.ContainsAny(owner[1:], "/~?#@") || strings.ContainsAny(name, "/?#@"):
		return repokey.Key{}, fmt.Errorf("forge/sourcehut: %s described %s with a name that is not one path segment", c.d.host, asked)
	}
	// The reason repokey gives is not carried: it quotes its input, and the
	// input is a forge's bytes.
	key, err := repokey.Parse("https://" + c.d.host + "/" + owner + "/" + name)
	if err != nil {
		return repokey.Key{}, fmt.Errorf("forge/sourcehut: %s described %s with a name this layer cannot derive a key from", c.d.host, asked)
	}
	if key.Host != c.d.host || key.Owner != strings.ToLower(owner) {
		return repokey.Key{}, fmt.Errorf("forge/sourcehut: %s described %s with a name that does not stay a ~owner/name", c.d.host, asked)
	}
	return key, nil
}

func (c *client) repository(r repoJSON, asked string) (forge.Repository, error) {
	key, err := c.keyOf(r.Owner.CanonicalName, r.Name, asked)
	if err != nil {
		return forge.Repository{}, err
	}
	addr := c.addresses(key)
	repo := forge.Repository{
		Key:     key,
		HTTPS:   addr[forge.HTTPS].URL,
		SSH:     addr[forge.SSH].URL,
		Private: r.Visibility == "PRIVATE",
	}
	if r.HEAD != nil {
		repo.DefaultBranch = strings.TrimPrefix(r.HEAD.Name, "refs/heads/")
	}
	return repo, nil
}

// addresses are the clone addresses of a key, built from the key's own
// segments: sourcehut's GraphQL answers no clone URL, and the URL shape is the
// instance's, https://host/~owner/name and ssh://<ssh-user>@host/~owner/name.
func (c *client) addresses(key repokey.Key) map[forge.Protocol]forge.Address {
	path := "/" + key.Owner + "/" + key.Name
	return map[forge.Protocol]forge.Address{
		forge.HTTPS: {URL: c.d.scheme + "://" + c.d.host + path, HTTPUsername: "token"},
		forge.SSH:   {URL: "ssh://" + c.d.sshUser + "@" + c.d.host + path, SSHAccount: c.d.sshUser},
	}
}
