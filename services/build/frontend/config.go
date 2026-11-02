package frontend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/register"
)

// Defaults, each a Config field set through New, never a shared package global.
const (
	// DefaultMaxBodyBytes bounds a webhook body; GitHub's push payloads stay well
	// under a mebibyte, and a stranger must not decide how much this process reads.
	DefaultMaxBodyBytes = 1 << 20
	// DefaultReadHeaderTimeout bounds slow headers; net/http has no default.
	DefaultReadHeaderTimeout = 10 * time.Second
	// DefaultIdleTimeout closes a kept-alive connection nobody is using.
	DefaultIdleTimeout = 60 * time.Second
	// DefaultShutdownTimeout bounds how long Close waits for in-flight requests.
	DefaultShutdownTimeout = 10 * time.Second
	// DefaultBackendTimeout bounds one call into the backend or auth, so a silent
	// backend costs bounded memory on internet-facing connections.
	DefaultBackendTimeout = 30 * time.Second
	// DefaultSecretCacheTTL is how long a hook's registration is believed, bounding
	// how long a replaced secret or an unlinked project's deliveries still verify.
	DefaultSecretCacheTTL = time.Minute
	// DefaultSecretCacheMax bounds the cache and the per-hook rate limiter
	// alike. Both are keyed by a string a stranger chooses, so both need a
	// ceiling; see ratelimit.go for what that ceiling costs.
	DefaultSecretCacheMax = 1024
	// DefaultHookSecretGrace is how long a replaced hook secret still verifies, for
	// in-flight deliveries and forges not yet updated; the secrets service keeps a
	// replaced version an hour.
	DefaultHookSecretGrace = time.Hour

	// DefaultStreamBackoff and DefaultStreamBackoffMax bound how soon a forge
	// event stream that broke is opened again: the first wait, doubled per
	// failure up to the ceiling, and back to the first once one connects.
	DefaultStreamBackoff    = time.Second
	DefaultStreamBackoffMax = time.Minute
	// DefaultFollowBackoff and DefaultFollowBackoffMax pace a follow asking
	// again for a log no node is writing yet: the first wait, doubled per ask
	// up to the ceiling. Each ask walks the backends, the leader's answer
	// behind a barrier, so the ceiling is what a long queue costs per viewer.
	DefaultFollowBackoff    = 500 * time.Millisecond
	DefaultFollowBackoffMax = 2 * time.Second
	// DefaultStreamResync is how often a connected stream's linked repositories have
	// their heads compared anyway, since a stream has no replay (streams.go).
	DefaultStreamResync = 5 * time.Minute
)

// The four limits, two per public surface, so neither surface's flood spends the
// other's budget (ratelimit.go). Defaults, so an unconfigured Config is limited.
// The two webhook limits also bound hook lookups: an admitted request makes at
// most one.
var (
	DefaultHookRate    = Rate{Burst: 30, Per: time.Minute}
	DefaultWebhookRate = Rate{Burst: 300, Per: time.Minute}
	// DefaultCallerRate is one credential's share of the job API: about one request a
	// second, with a minute of burst.
	DefaultCallerRate = Rate{Burst: 60, Per: time.Minute}
	// DefaultJobRate is the job API's ceiling on one node, the bound that holds since
	// a new bearer token gets a fresh per-credential bucket.
	DefaultJobRate = Rate{Burst: 600, Per: time.Minute}
)

// DefaultEligibleRefs is restated rather than imported from v1's frozen specs.
func DefaultEligibleRefs() []string { return []string{"refs/heads/main", "refs/heads/master"} }

// Rate is a token bucket: Burst requests may arrive at once, and the bucket
// refills at Burst tokens per Per.
type Rate struct {
	Burst int           `yaml:"burst"`
	Per   time.Duration `yaml:"per"`
}

func (r Rate) valid() error {
	if r.Burst < 0 {
		return fmt.Errorf("build: rate burst must not be negative, got %d", r.Burst)
	}
	if r.Per < 0 {
		return fmt.Errorf("build: rate period must not be negative, got %s", r.Per)
	}
	if r.Burst > 0 && r.Per == 0 {
		return fmt.Errorf("build: a rate of %d has no period", r.Burst)
	}
	return nil
}

// Config is what an operator declared. A plain value: no live objects, no
// setters, and it round-trips through yaml.
type Config struct {
	// Listen is the address Start binds; no default. Tests use Handler() and bind
	// nothing.
	Listen string `yaml:"listen"`

	// MaxBodyBytes bounds one webhook body. Zero means DefaultMaxBodyBytes.
	MaxBodyBytes int64 `yaml:"maxBodyBytes"`

	// ReadHeaderTimeout, IdleTimeout and ShutdownTimeout are the http.Server's.
	// Zero means the matching default.
	ReadHeaderTimeout time.Duration `yaml:"readHeaderTimeout"`
	IdleTimeout       time.Duration `yaml:"idleTimeout"`
	ShutdownTimeout   time.Duration `yaml:"shutdownTimeout"`

	// BackendTimeout bounds one call out of this process. Zero means
	// DefaultBackendTimeout.
	BackendTimeout time.Duration `yaml:"backendTimeout"`

	// EligibleRefs are the refs a push is built for; nil means DefaultEligibleRefs.
	// The one place that decides, before Enqueue.
	EligibleRefs []string `yaml:"eligibleRefs"`

	// Origins are the browser origins (scheme://host[:port]) whose pages may call this
	// ingress; no cookie is ever accepted (cors.go).
	Origins []string `yaml:"origins"`

	// HookRate limits one hook id, WebhookRate the webhook route, CallerRate one
	// credential, JobRate the job routes. Each surface has its own pair so a flood
	// on one cannot lock out the other. A zero Rate means the default; turning one
	// off takes a period with a burst of zero.
	HookRate    Rate `yaml:"hookRate"`
	WebhookRate Rate `yaml:"webhookRate"`
	CallerRate  Rate `yaml:"callerRate"`
	JobRate     Rate `yaml:"jobRate"`

	// SecretCacheTTL and SecretCacheMax bound how long and how much of auth's
	// answer this process keeps. Zero means the matching default.
	SecretCacheTTL time.Duration `yaml:"secretCacheTTL"`
	SecretCacheMax int           `yaml:"secretCacheMax"`

	// HookSecretGrace is how long a rotated hook secret's previous value is
	// still accepted. Zero means the default.
	HookSecretGrace time.Duration `yaml:"hookSecretGrace"`

	// StreamBackoff, StreamBackoffMax and StreamResync pace the forge event
	// streams (streams.go). Zero means the matching default.
	StreamBackoff    time.Duration `yaml:"streamBackoff"`
	StreamBackoffMax time.Duration `yaml:"streamBackoffMax"`
	StreamResync     time.Duration `yaml:"streamResync"`

	// FollowBackoff and FollowBackoffMax pace a follow of a job whose log no
	// node is writing yet (jobapi.go). Zero means the matching default.
	FollowBackoff    time.Duration `yaml:"followBackoff"`
	FollowBackoffMax time.Duration `yaml:"followBackoffMax"`
}

func (c Config) withDefaults() (Config, error) {
	type namedDuration struct {
		name  string
		value time.Duration
	}
	for _, d := range []namedDuration{
		{"readHeaderTimeout", c.ReadHeaderTimeout},
		{"idleTimeout", c.IdleTimeout},
		{"shutdownTimeout", c.ShutdownTimeout},
		{"backendTimeout", c.BackendTimeout},
		{"secretCacheTTL", c.SecretCacheTTL},
		{"hookSecretGrace", c.HookSecretGrace},
		{"streamBackoff", c.StreamBackoff},
		{"streamBackoffMax", c.StreamBackoffMax},
		{"streamResync", c.StreamResync},
		{"followBackoff", c.FollowBackoff},
		{"followBackoffMax", c.FollowBackoffMax},
	} {
		if d.value < 0 {
			return c, fmt.Errorf("build: %s must not be negative", d.name)
		}
	}
	if c.MaxBodyBytes < 0 {
		return c, fmt.Errorf("build: maxBodyBytes must not be negative")
	}
	if c.SecretCacheMax < 0 {
		return c, fmt.Errorf("build: secretCacheMax must not be negative")
	}
	if err := c.HookRate.valid(); err != nil {
		return c, fmt.Errorf("build: hookRate: %w", err)
	}
	if err := c.WebhookRate.valid(); err != nil {
		return c, fmt.Errorf("build: webhookRate: %w", err)
	}
	if err := c.CallerRate.valid(); err != nil {
		return c, fmt.Errorf("build: callerRate: %w", err)
	}
	if err := c.JobRate.valid(); err != nil {
		return c, fmt.Errorf("build: jobRate: %w", err)
	}
	for _, ref := range c.EligibleRefs {
		if ref == "" {
			return c, fmt.Errorf("build: eligibleRefs holds an empty ref")
		}
	}

	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if c.ReadHeaderTimeout == 0 {
		c.ReadHeaderTimeout = DefaultReadHeaderTimeout
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = DefaultIdleTimeout
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = DefaultShutdownTimeout
	}
	if c.BackendTimeout == 0 {
		c.BackendTimeout = DefaultBackendTimeout
	}
	if c.SecretCacheTTL == 0 {
		c.SecretCacheTTL = DefaultSecretCacheTTL
	}
	if c.SecretCacheMax == 0 {
		c.SecretCacheMax = DefaultSecretCacheMax
	}
	if c.HookSecretGrace == 0 {
		c.HookSecretGrace = DefaultHookSecretGrace
	}
	if c.EligibleRefs == nil {
		c.EligibleRefs = DefaultEligibleRefs()
	}
	if c.StreamBackoff == 0 {
		c.StreamBackoff = DefaultStreamBackoff
	}
	if c.StreamBackoffMax == 0 {
		c.StreamBackoffMax = DefaultStreamBackoffMax
	}
	if c.StreamBackoffMax < c.StreamBackoff {
		c.StreamBackoffMax = c.StreamBackoff
	}
	if c.StreamResync == 0 {
		c.StreamResync = DefaultStreamResync
	}
	if c.FollowBackoff == 0 {
		c.FollowBackoff = DefaultFollowBackoff
	}
	if c.FollowBackoffMax == 0 {
		c.FollowBackoffMax = DefaultFollowBackoffMax
	}
	if c.FollowBackoffMax < c.FollowBackoff {
		c.FollowBackoffMax = c.FollowBackoff
	}
	if c.HookRate == (Rate{}) {
		c.HookRate = DefaultHookRate
	}
	if c.WebhookRate == (Rate{}) {
		c.WebhookRate = DefaultWebhookRate
	}
	if c.CallerRate == (Rate{}) {
		c.CallerRate = DefaultCallerRate
	}
	if c.JobRate == (Rate{}) {
		c.JobRate = DefaultJobRate
	}
	return c, nil
}

// Hook verifies a delivery addressed to one project: each repository with its own
// webhook secret, so a signature is good for its own repository only.
type Hook struct {
	Project      string
	Repositories []HookRepository
}

// HookRepository is one linked repository and its webhook secret, with the
// secret that one replaced and when, if the vault still holds it.
type HookRepository struct {
	Key    string
	Secret []byte

	Previous []byte
	Replaced time.Time
}

// ErrNoHook is a project this cloud cannot verify a push for; an answer the route
// denies, unlike a failure to ask (503).
var ErrNoHook = errors.New("build: no hook for this project")

// Hooks verifies a webhook addressed to a project, from the register and the
// vault.
type Hooks interface {
	// Hook returns project's hook, or ErrNoHook.
	Hook(ctx context.Context, project string) (Hook, error)
}

// Deliveries authenticates a webhook delivery and says what it means; an interface
// since the forge is the host's answer (forgeauth).
type Deliveries interface {
	// Verify checks one delivery against its hook under the repository's own scheme
	// and secret and returns the push; a delivery naming another repository does not
	// verify. ErrNotAPush and ErrRefDeleted are genuine non-builds, ErrMalformed a
	// genuine delivery with no usable push, ErrUnverified not genuine; anything else
	// is a hook that cannot be checked.
	Verify(hook Hook, header http.Header, body []byte) (Push, error)
}

// Push is what a verified delivery means: build.proto's PushEvent, nothing else.
type Push struct {
	Repository string
	Ref        string
	Commit     string
}

var (
	// ErrNotAPush is a genuine delivery of another event, such as a ping.
	ErrNotAPush = errors.New("build: not a push event")

	// ErrRefDeleted is a genuine push that deletes its ref.
	ErrRefDeleted = errors.New("build: the push deletes its ref")

	// ErrUnverified is a delivery that does not prove it came from the hook.
	ErrUnverified = errors.New("build: the delivery does not verify")

	// ErrMalformed is a delivery that verified and names no usable push.
	ErrMalformed = errors.New("build: the delivery names no usable push")
)

// Projects is the register's project table, as authorization reads it.
type Projects interface {
	// Role is user's role on a project; an unknown project is NotFound.
	Role(ctx context.Context, projectID, user string) (register.Role, error)
	// Project is the project a repository belongs to; one no project owns
	// is NotFound.
	Project(ctx context.Context, repositoryKey string) (string, error)
}

// Callers resolves a tau token into the user auth issued it to. A token auth
// refuses is ErrBadCredential or an Unauthenticated connect error.
type Callers interface {
	Caller(ctx context.Context, token string) (string, error)
}

// Vault is the secrets service as this role sees it: set and list, never get, so a
// stolen token can overwrite secrets but never read them. Optional; without it
// every route answers the same 404 a denial gets.
type Vault interface {
	Set(ctx context.Context, project, environment, name string, value []byte) error
	Delete(ctx context.Context, project, environment, name string) error
	List(ctx context.Context, project, environment string) ([]string, error)
}

// Linker is the register's project table as this role writes it: linking, the
// caller's index and grants, only after authorization.
type Linker interface {
	LinkProject(ctx context.Context, l register.Link) (register.ProjectRecord, error)
	ProjectsOf(ctx context.Context, user string) ([]register.ProjectRecord, error)
	// ProjectRecord is one linked project; an unknown id is
	// register.ErrNotLinked.
	ProjectRecord(ctx context.Context, projectID string) (register.ProjectRecord, error)
	GrantRole(ctx context.Context, project, grantee string, role register.Role, by string) (register.Grant, error)
	Revoke(ctx context.Context, project, id, by string) error
	Grants(ctx context.Context, project string) ([]register.Grant, error)
}

// Streams is a forge's own push feed, for forges with no signed webhook (Gerrit).
// A streamed push goes the way a verified delivery does.
type Streams interface {
	// Hosts is every forge host with a feed.
	Hosts() []string
	// Stream holds one subscription: ready once accepted, then emit per push (an error
	// for a claim the forge layer refused). It returns when ctx ends or the feed
	// breaks, and never reconnects.
	Stream(ctx context.Context, host string, ready func(), emit func(Push, error)) error
	// Heads is one repository's branch heads, as pushes.
	Heads(ctx context.Context, repository string) ([]Push, error)
}

// Linked is every repository linked to a project on this cloud, as spelled
// when linked: the set a reconnected stream compares heads for.
type Linked interface {
	LinkedRepositories(ctx context.Context) ([]string, error)
}

// Backend is build's private half as this role sees it: build.proto's five RPCs
// and nothing else, an interface so routes test against a fake.
type Backend interface {
	Enqueue(ctx context.Context, repositoryKey, ref, commitSHA string) (jobID string, err error)
	Get(ctx context.Context, jobID string) (*buildv2.Job, error)
	// List hands each id to emit as it arrives, so neither this process nor the
	// backend has to hold a project's whole job history to answer.
	List(ctx context.Context, projectID string, emit func(jobID string) error) error
	Cancel(ctx context.Context, jobID string) error
	Retry(ctx context.Context, jobID string) error
	// Follow streams one run's output from `from` and returns the offset reached; only
	// the writer answers. Unavailable from all is a log not yet written;
	// FailedPrecondition is an ended run.
	Follow(ctx context.Context, jobID string, attempt int32, from int64, emit func(data []byte, next int64) error) (int64, error)
	// Snapshot is Follow up to what the log holds when it is asked: it ends
	// there while the build goes on.
	Snapshot(ctx context.Context, jobID string, attempt int32, from int64, emit func(data []byte, next int64) error) (int64, error)
}

// ErrBadCredential is a token auth refused outright.
var ErrBadCredential = errors.New("build: the credential is not accepted")

// Assets reads what a build stored, by the content id a job records.
type Assets interface {
	Get(ctx context.Context, cid string) (io.ReadSeekCloser, error)
}

// accepting is the hook as a delivery is verified against now: each
// repository under its secret, and under its previous one while the grace since
// the replacement lasts.
func (h Hook) accepting(now time.Time, grace time.Duration) Hook {
	out := Hook{Project: h.Project, Repositories: make([]HookRepository, 0, len(h.Repositories))}
	for _, r := range h.Repositories {
		out.Repositories = append(out.Repositories, HookRepository{Key: r.Key, Secret: r.Secret})
	}
	for _, r := range h.Repositories {
		if len(r.Previous) > 0 && now.Before(r.Replaced.Add(grace)) {
			out.Repositories = append(out.Repositories, HookRepository{Key: r.Key, Secret: r.Previous})
		}
	}
	return out
}

// signed reports whether any of the hook's repositories has a secret to
// verify a delivery with.
func (h Hook) signed() bool {
	for _, r := range h.Repositories {
		if len(r.Secret) > 0 {
			return true
		}
	}
	return false
}
