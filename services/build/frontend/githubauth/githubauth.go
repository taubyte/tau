// Package githubauth resolves a GitHub OAuth token into what its holder may do
// with a repository. It is the one production implementation of
// services/build/frontend's Identity, and the only package under
// services/build that talks to a provider.
//
// It is its own package so that the frontend's import list stays what the
// frontend is: go-github, its transport and its rate-limit types live here and
// nowhere else, and a second provider is a sibling package rather than a
// branch in a shared file.
//
// # It asks under the caller's own token
//
// One authenticated GET of the repository, with the CALLER's credential, and
// GitHub returns that caller's permissions on the repository as part of it — so
// the check costs no extra call and cannot be answered by a token this cloud
// holds instead. services/auth/github.go's repoAccess is the same call for the
// same reason; this restates it where build needs it rather than reaching into
// auth for it.
package githubauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/go-github/v71/github"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/frontend"
)

// Host is the provider this adapter speaks for. A repokey names its own host,
// so an adapter that answered for every host would be answering about
// repositories it has never heard of.
const Host = "github.com"

// Client resolves write access at github.com.
type Client struct {
	http    *http.Client
	baseURL *url.URL
}

// Option configures a Client.
type Option func(*Client) error

// WithHTTPClient replaces the transport — a timeout, a proxy, an instrumented
// round tripper. The per-call context still bounds the call; this is for
// everything a context cannot say.
func WithHTTPClient(c *http.Client) Option {
	return func(g *Client) error {
		if c == nil {
			return errors.New("build: http client must not be nil")
		}
		g.http = c
		return nil
	}
}

// WithBaseURL points this adapter at another API root: GitHub Enterprise, or a
// test server. The trailing slash go-github requires is added if it is missing,
// because forgetting it silently produces requests to the wrong path.
func WithBaseURL(raw string) Option {
	return func(g *Client) error {
		if !strings.HasSuffix(raw, "/") {
			raw += "/"
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("build: github base url: %w", err)
		}
		g.baseURL = parsed
		return nil
	}
}

// New returns a Client. It opens nothing: a github.Client is built per call,
// because the credential is per call.
func New(opts ...Option) (*Client, error) {
	g := &Client{}
	for _, opt := range opts {
		if err := opt(g); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// WriteAccess reports whether the holder of token may write to key.
//
// ADMIN, MAINTAIN OR PUSH — services/auth/github.go:116-132's own rule, and
// READ IS NOT ENOUGH: a public repository reads to everyone, which is what let
// an unrelated caller reach another project's repositories.
//
// The four outcomes GitHub can give, and what each becomes:
//
//   - 200 with permissions -> granted or denied, on those three flags.
//   - 401 -> AccessUnauthenticated. The token is not usable; the caller can fix
//     that, so it must not be flattened into a denial.
//   - 403 -> denied, EXCEPT a rate limit, which is not an answer about this
//     caller at all and comes back as an error so it cannot become a 404.
//   - 404 -> denied. A repository a token cannot see and one that does not
//     exist are the same fact from here, and conflating them is the right way
//     round: it is what keeps this from being an existence oracle.
func (g *Client) WriteAccess(ctx context.Context, token string, key repokey.Key) (frontend.Access, error) {
	if token == "" {
		return frontend.AccessUnauthenticated, nil
	}
	if key.Host != Host {
		// A key for another provider is not this adapter's to answer about, and
		// answering "denied" would be a lie that looks like a decision.
		return frontend.AccessDenied, fmt.Errorf("build: %s cannot answer for %q", Host, key.Host)
	}
	if key.Owner == "" || strings.Contains(key.Owner, "/") {
		// github.com repositories are owner/name, exactly two segments. A
		// nested owner is a forge that nests (repokey supports GitLab
		// subgroups) and is not this host.
		return frontend.AccessDenied, fmt.Errorf("build: %q is not a github.com repository", key)
	}

	client := github.NewClient(g.http).WithAuthToken(token)
	if g.baseURL != nil {
		client.BaseURL = g.baseURL
	}

	repo, _, err := client.Repositories.Get(ctx, key.Owner, key.Name)
	if err != nil {
		return classify(err)
	}

	perms := repo.GetPermissions()
	if perms["admin"] || perms["maintain"] || perms["push"] {
		return frontend.AccessGranted, nil
	}
	return frontend.AccessDenied, nil
}

// classify turns go-github's error into either a DECIDED access with a nil
// error, or a failure to decide.
//
// A FAILURE TO DECIDE IS NEVER A DENIAL. frontend/authz.go answers a denial
// with a 404 and a failure with a 502, and a provider outage that silently
// became "not yours" would be a support call rather than a bug report — which
// is why a rate limit, whose status code is also 403, is separated out here
// instead of falling into the denial case beside it.
func classify(err error) (frontend.Access, error) {
	var rate *github.RateLimitError
	if errors.As(err, &rate) {
		return frontend.AccessDenied, fmt.Errorf("build: github rate limit: %w", err)
	}
	var abuse *github.AbuseRateLimitError
	if errors.As(err, &abuse) {
		return frontend.AccessDenied, fmt.Errorf("build: github secondary rate limit: %w", err)
	}

	var response *github.ErrorResponse
	if !errors.As(err, &response) {
		// A transport failure: there was no answer at all.
		return frontend.AccessDenied, fmt.Errorf("build: reaching github: %w", err)
	}

	switch response.Response.StatusCode {
	case http.StatusUnauthorized:
		// The credential itself is not usable. The caller can fix that, so it
		// must not be flattened into a denial.
		return frontend.AccessUnauthenticated, nil
	case http.StatusForbidden, http.StatusNotFound:
		// Decided: denied. A repository a token cannot SEE and one that does
		// not exist are the same fact from here, and conflating them is the
		// right way round — it is what keeps this from being an existence
		// oracle.
		return frontend.AccessDenied, nil
	default:
		return frontend.AccessDenied, fmt.Errorf("build: github answered %d: %w", response.Response.StatusCode, err)
	}
}
