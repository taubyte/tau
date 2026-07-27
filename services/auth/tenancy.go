package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-github/v71/github"
	"github.com/jellydator/ttlcache/v3"
	tauConfig "github.com/taubyte/tau/pkg/config"
	"golang.org/x/oauth2"
)

const (
	// MembershipPositiveTTL is the revocation window: a user removed from the
	// namespace keeps registering for this long. Registration is a rare,
	// interactive operation, so a short window costs little.
	MembershipPositiveTTL = 5 * time.Minute
	// MembershipNegativeTTL bounds how often a refused caller re-asks.
	MembershipNegativeTTL = time.Minute

	// appJWTTTL is under GitHub's 10 minute ceiling for app JWTs.
	appJWTTTL = 9 * time.Minute
	// installTokenTTL is under GitHub's 1 hour installation token lifetime.
	installTokenTTL = 55 * time.Minute
)

// ErrNoTenancy is returned when a registration is attempted on a cloud with no
// owning namespace configured. Refusing beats falling open: an unconfigured
// cloud would otherwise accept a registration from anyone holding a valid
// provider token.
var ErrNoTenancy = errors.New("no tenancy configured: this cloud registers repositories for a configured namespace only")

// MembershipVerifier answers whether a user belongs to a namespace. A
// (false, nil) result is a definitive non-member; everything else -- missing
// permission, rate limit, transport failure -- is an error, so an outage can
// never read as a refusal.
type MembershipVerifier interface {
	IsActiveMember(ctx context.Context, namespace, username string) (bool, error)
}

// githubAppVerifier answers membership with an installation token minted from
// the namespace's own app credential. The caller's token cannot be used: it
// carries whatever scopes the caller happened to grant, and GitHub returns 404
// for both "not a member" and "you may not look", which are not the same answer.
type githubAppVerifier struct {
	clientID string
	key      any

	// baseURL is nil in production; tests point it at a stub.
	baseURL *url.URL

	mu             sync.Mutex
	installationID map[string]int64
	tokens         map[string]tokenEntry

	cache *ttlcache.Cache[string, bool]
}

type tokenEntry struct {
	token   string
	expires time.Time
}

// tenancyVerifier returns the verifier a tenancy requires: none when no
// namespace is configured, and otherwise one built from the app credential.
//
// A configured tenancy has no ownership-only fallback. Membership is
// unanswerable without the credential, so a missing or unusable one is an error
// here — at startup — rather than a quietly narrower gate discovered at
// whichever registration first needs it.
func tenancyVerifier(t tauConfig.Tenancy) (MembershipVerifier, error) {
	if !t.Configured() {
		return nil, nil
	}
	v, err := newGitHubAppVerifier(t.App.ClientId, t.App.Key)
	if err != nil {
		return nil, fmt.Errorf("tenancy `%s` needs a usable app credential: %w", t.Owner, err)
	}
	return v, nil
}

// newGitHubAppVerifier builds a verifier from a PEM private key. It fails on a
// malformed key rather than deferring the error to the first registration.
func newGitHubAppVerifier(clientID, pem string) (*githubAppVerifier, error) {
	if clientID == "" {
		return nil, errors.New("tenancy app client-id is empty")
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pem))
	if err != nil {
		return nil, fmt.Errorf("parsing tenancy app key failed with %w", err)
	}

	// Sliding expiration is off: with it, a user who keeps registering would
	// refresh their own cached membership and never fall out of the cache.
	cache := ttlcache.New(
		ttlcache.WithTTL[string, bool](MembershipPositiveTTL),
		ttlcache.WithDisableTouchOnHit[string, bool](),
	)
	go cache.Start()

	return &githubAppVerifier{
		clientID:       clientID,
		key:            key,
		installationID: make(map[string]int64),
		tokens:         make(map[string]tokenEntry),
		cache:          cache,
	}, nil
}

func (v *githubAppVerifier) Close() {
	v.cache.Stop()
}

func (v *githubAppVerifier) IsActiveMember(ctx context.Context, namespace, username string) (bool, error) {
	if namespace == "" || username == "" {
		return false, errors.New("namespace and username are required")
	}

	key := strings.ToLower(namespace + "/" + username)
	if item := v.cache.Get(key); item != nil {
		return item.Value(), nil
	}

	client, err := v.installationClient(ctx, namespace)
	if err != nil {
		return false, err
	}

	membership, resp, err := client.Organizations.GetOrgMembership(ctx, username, namespace)
	if err != nil {
		// Only a 404 is a definitive non-member. A 403 can mean the app was
		// blocked by the namespace, which is an operator problem, not a verdict
		// on this user.
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			v.cache.Set(key, false, MembershipNegativeTTL)
			return false, nil
		}
		return false, fmt.Errorf("membership check for %s/%s failed with %w", namespace, username, err)
	}

	// "pending" is an unaccepted invitation, not membership.
	active := membership.GetState() == "active"
	ttl := MembershipPositiveTTL
	if !active {
		ttl = MembershipNegativeTTL
	}
	v.cache.Set(key, active, ttl)
	return active, nil
}

// installationClient returns a client authenticated as the app's installation
// on namespace, minting and caching a token as needed.
func (v *githubAppVerifier) installationClient(ctx context.Context, namespace string) (*github.Client, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if entry, ok := v.tokens[namespace]; ok && time.Now().Before(entry.expires) {
		return v.clientWithToken(entry.token), nil
	}

	appClient, err := v.appClient(ctx)
	if err != nil {
		return nil, err
	}

	id, ok := v.installationID[namespace]
	if !ok {
		install, _, ierr := appClient.Apps.FindOrganizationInstallation(ctx, namespace)
		if ierr != nil {
			return nil, fmt.Errorf("app is not installed on %s: %w", namespace, ierr)
		}
		id = install.GetID()
		v.installationID[namespace] = id
	}

	// Ask for the narrowest token the check needs, not everything the app holds.
	token, _, err := appClient.Apps.CreateInstallationToken(ctx, id, &github.InstallationTokenOptions{
		Permissions: &github.InstallationPermissions{
			Members: github.Ptr("read"),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("minting installation token for %s failed with %w", namespace, err)
	}

	v.tokens[namespace] = tokenEntry{
		token:   token.GetToken(),
		expires: time.Now().Add(installTokenTTL),
	}

	return v.clientWithToken(token.GetToken()), nil
}

// appClient authenticates as the app itself, which is only good for finding
// installations and minting their tokens.
func (v *githubAppVerifier) appClient(ctx context.Context) (*github.Client, error) {
	now := time.Now()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(appJWTTTL)),
		Issuer:    v.clientID,
	}).SignedString(v.key)
	if err != nil {
		return nil, fmt.Errorf("signing app jwt failed with %w", err)
	}
	return v.clientWithToken(signed), nil
}

func (v *githubAppVerifier) clientWithToken(token string) *github.Client {
	client := github.NewClient(oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)))
	if v.baseURL != nil {
		client.BaseURL = v.baseURL
	}
	return client
}
