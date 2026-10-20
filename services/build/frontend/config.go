package frontend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/taubyte/tau/pkg/git/repokey"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/register"
)

// Defaults. Fields on Config set through New, never mutable package globals —
// v1's services/patrick/service.go's BootstrapTime, DefaultReAnnounceJobTime and
// MaxReAnnounceJobs are those, shared by every instance in a process, and
// v1's services/patrick/types.go already records that as a fixed race.
const (
	// DefaultMaxBodyBytes bounds a webhook body. GitHub's own push payloads run
	// to a few hundred KiB at the truncation limits it documents (20 commits,
	// 3000 files), so a mebibyte is generous; the point is that a stranger
	// cannot decide how much this process reads. v1 io.ReadAll's the whole body
	// before any authentication happens and no tau service ever sets a cap.
	DefaultMaxBodyBytes = 1 << 20
	// DefaultReadHeaderTimeout is the slow-header bound. net/http has no
	// default for it, so without one a single idle connection holds a goroutine
	// for as long as it likes.
	DefaultReadHeaderTimeout = 10 * time.Second
	// DefaultIdleTimeout closes a kept-alive connection nobody is using.
	DefaultIdleTimeout = 60 * time.Second
	// DefaultShutdownTimeout bounds how long Close waits for in-flight requests.
	DefaultShutdownTimeout = 10 * time.Second
	// DefaultBackendTimeout bounds ONE call into the backend or into auth. It
	// exists because the goroutine waiting on that call is holding an
	// internet-facing connection: a backend that stops answering must cost this
	// process a bounded amount of memory, not an unbounded one.
	DefaultBackendTimeout = 30 * time.Second
	// DefaultSecretCacheTTL is how long a hook's registration is believed. It
	// bounds how long a replaced secret still verifies on a node that remembers
	// it — the first delivery under the new one ends that sooner, by re-asking
	// (webhook.go) — and how long an unlinked project's deliveries do; so it is
	// short.
	DefaultSecretCacheTTL = time.Minute
	// DefaultSecretCacheMax bounds the cache and the per-hook rate limiter
	// alike. Both are keyed by a string a stranger chooses, so both need a
	// ceiling; see ratelimit.go for what that ceiling costs.
	DefaultSecretCacheMax = 1024

	// DefaultStreamBackoff and DefaultStreamBackoffMax bound how soon a forge
	// event stream that broke is opened again: the first wait, doubled per
	// failure up to the ceiling, and back to the first once one connects.
	DefaultStreamBackoff    = time.Second
	DefaultStreamBackoffMax = time.Minute
	// DefaultStreamResync is how often a connected stream's linked
	// repositories have their branch heads compared anyway. A stream has no
	// replay, and a push it lost — to a disconnect, to the window before the
	// forge registered the subscription, to a backend that refused the
	// enqueue — is otherwise only found by the next push. See streams.go.
	DefaultStreamResync = 5 * time.Minute
)

// The four limits, two per public surface, and they are four because the two
// surfaces defend different things and neither's flood may spend the other's
// budget. See ratelimit.go.
//
// THE NUMBERS ARE POLICY and an operator overrides every one of them. What is
// not policy is that a Config naming none of them is limited rather than open,
// which is why they are defaults and not zero.
//
// THE TWO WEBHOOK LIMITS ARE ALSO THE LOOKUP BUDGET. Both are taken before
// every hook lookup, and an admitted request makes AT MOST ONE logical lookup:
// a cold one when nothing is remembered, or one refresh when a remembered hook
// fails to verify the delivery — never both, since a fresh answer is not
// re-asked (webhook.go). So the register and the vault see at most WebhookRate
// lookups per node and HookRate per project, whatever a stranger sends, and a
// refresh needs no cooldown of its own: it spends the token its request
// already paid for.
var (
	DefaultHookRate    = Rate{Burst: 30, Per: time.Minute}
	DefaultWebhookRate = Rate{Burst: 300, Per: time.Minute}
	// DefaultCallerRate is one credential's share of the job API. A CLI polling
	// one build's status wants about one request a second; sixty a minute is
	// that, with a minute's worth of burst in front of it.
	DefaultCallerRate = Rate{Burst: 60, Per: time.Minute}
	// DefaultJobRate is the whole job API's ceiling on one node, and it is the
	// bound that actually holds: a stranger gets a fresh per-credential bucket
	// by inventing a new bearer token, and cannot get a fresh one of these.
	DefaultJobRate = Rate{Burst: 600, Per: time.Minute}
)

// DefaultEligibleRefs is RESTATED, not imported. v1's legacy/specs/common's
// DefaultBranches is v1's frozen address space and nothing new imports it; the
// list is also policy, and policy an operator can change does not belong in a
// package v1 freezes.
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
	// Listen is the address Start binds. There is no default and no constant:
	// a port baked into a service is why services/auth/service_basic_test.go
	// hardcodes eight of them and why sibling worktrees collide. Tests bind
	// "127.0.0.1:0" or, more often, use Handler() and bind nothing at all.
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

	// EligibleRefs are the refs a push is built for. Nil means
	// DefaultEligibleRefs. THE POLICY LIVES HERE, before Enqueue is ever
	// called (build.proto: "a push to an ineligible ref is refused at the HTTP
	// handler and never reaches this RPC"), so the backend never re-filters a
	// ref and there is exactly one place that decides.
	EligibleRefs []string `yaml:"eligibleRefs"`

	// HookRate limits one hook id; WebhookRate limits the webhook route.
	// CallerRate limits one credential; JobRate limits the four job routes.
	// The ZERO Rate means the matching default, so a config that names none of
	// them is limited rather than open. Turning a limit off takes a period with
	// a burst of zero — deliberately not the shape a half-filled config falls
	// into.
	//
	// THE JOB ROUTES NEED THEIR OWN PAIR rather than sharing the webhook's.
	// One shared ceiling would let a flood on either surface lock the other
	// out, and the two are not interchangeable: a webhook refused is a delivery
	// a provider stops retrying, and a job route refused is a CLI that cannot
	// read its own build.
	// Origins are the browser origins (scheme://host[:port]) whose pages may
	// call this ingress — the cloud's consoles. Every other origin gets no
	// CORS headers, so a browser refuses it the answer. No cookie is ever
	// accepted, so no credentialed request is allowed. See cors.go.
	Origins []string `yaml:"origins"`

	HookRate    Rate `yaml:"hookRate"`
	WebhookRate Rate `yaml:"webhookRate"`
	CallerRate  Rate `yaml:"callerRate"`
	JobRate     Rate `yaml:"jobRate"`

	// SecretCacheTTL and SecretCacheMax bound how long and how much of auth's
	// answer this process keeps. Zero means the matching default.
	SecretCacheTTL time.Duration `yaml:"secretCacheTTL"`
	SecretCacheMax int           `yaml:"secretCacheMax"`

	// StreamBackoff, StreamBackoffMax and StreamResync pace the forge event
	// streams (streams.go). Zero means the matching default.
	StreamBackoff    time.Duration `yaml:"streamBackoff"`
	StreamBackoffMax time.Duration `yaml:"streamBackoffMax"`
	StreamResync     time.Duration `yaml:"streamResync"`
}

func (c Config) withDefaults() (Config, error) {
	for _, d := range []struct {
		name  string
		value time.Duration
	}{
		{"readHeaderTimeout", c.ReadHeaderTimeout},
		{"idleTimeout", c.IdleTimeout},
		{"shutdownTimeout", c.ShutdownTimeout},
		{"backendTimeout", c.BackendTimeout},
		{"secretCacheTTL", c.SecretCacheTTL},
		{"streamBackoff", c.StreamBackoff},
		{"streamBackoffMax", c.StreamBackoffMax},
		{"streamResync", c.StreamResync},
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

// Hook is what verifies a delivery addressed to one project: the
// repositories a push to it may be for, and the secret its signature is made
// with.
//
// THE FIELDS DECIDE AN AUTHENTICATION TOGETHER, which is why they travel as one
// value: the secret verifies the body, and the repositories say which
// repositories that signature is good for. v1 fetched only the secret and let
// the BODY name the repository, so a signature under one hook authorized a job
// for any repository the cloud had.
type Hook struct {
	Project      string
	Repositories []string
	Secret       []byte
}

// ErrNoHook is a project this cloud cannot verify a push for: nobody linked
// it, or no hook secret is set for it. It is an answer, not a failure: the
// webhook route denies it, where a failure to ask is a 503 (secrets.go).
var ErrNoHook = errors.New("build: no hook for this project")

// Hooks is what verifies a webhook addressed to a project: the register's
// record of the project, and the hook secret its link stored in the vault.
type Hooks interface {
	// Hook returns project's hook, or ErrNoHook.
	Hook(ctx context.Context, project string) (Hook, error)
}

// Deliveries authenticates a webhook delivery and says what it means.
//
// IT IS AN INTERFACE FOR THE REASON Identity IS: which forge signed a delivery,
// how, and where in the payload the push is, are the host's answers — and this
// package names no host. forgeauth answers it through pkg/forge.
type Deliveries interface {
	// Verify checks one delivery against the hook it was addressed to, under
	// the scheme the repository's forge signs with and the secret stored for
	// the project, and returns the push it proves. A push is always for one of
	// the hook's own repositories: a delivery naming any other does not verify.
	//
	// ErrNotAPush and ErrRefDeleted are genuine deliveries this cloud does not
	// build; ErrMalformed is a genuine delivery naming no usable push;
	// ErrUnverified is a delivery that is not genuine. Anything else is a hook
	// this cloud cannot check at all.
	Verify(hook Hook, header http.Header, body []byte) (Push, error)
}

// Push is what a verified delivery means: build.proto's PushEvent, and nothing
// else. Everything else a payload carries is a stranger's bytes a forge relayed.
type Push struct {
	Repository string
	Ref        string
	Commit     string
}

var (
	// ErrNotAPush is a genuine delivery of an event this cloud does not build
	// on — a ping when a hook is created, and a dozen others after.
	ErrNotAPush = errors.New("build: not a push event")

	// ErrRefDeleted is a genuine push that deletes its ref.
	ErrRefDeleted = errors.New("build: the push deletes its ref")

	// ErrUnverified is a delivery that does not prove it came from the hook.
	ErrUnverified = errors.New("build: the delivery does not verify")

	// ErrMalformed is a delivery that verified and names no usable push.
	ErrMalformed = errors.New("build: the delivery names no usable push")
)

// Projects is what auth knows about who owns a project.
type Projects interface {
	// Repositories returns the repository keys that gate a project: write
	// access to any one of them is write access to the project.
	Repositories(ctx context.Context, projectID string) ([]string, error)
}

// Vault is the secrets service as this role sees it — three calls, and
// deliberately NOT the fourth.
//
// THERE IS NO Get. A developer sets a secret and lists what is set; nothing on
// this ingress reads a value back. That is the difference between a stolen
// token being "they can overwrite my secrets" and "they have every secret this
// project holds" — and it costs a developer nothing, because the value is
// theirs already and rotating is setting again. Every platform this competes
// with is write-only after the set for the same reason.
//
// OPTIONAL. A frontend composed without one answers every route below with the
// same 404 a denial gets, because a cloud that holds no secrets should not
// distinguish itself from one that holds none of yours.
type Vault interface {
	Set(ctx context.Context, project, environment, name string, value []byte) error
	Delete(ctx context.Context, project, environment, name string) error
	List(ctx context.Context, project, environment string) ([]string, error)
}

// Linker is the register's project table, as this role sees it: the one write
// linking a project takes, and the caller's own index. The register's surface is
// p2p only; this is the one public door to it, and it opens only after the
// caller is shown to write to every repository the link names (projectapi.go).
type Linker interface {
	LinkProject(ctx context.Context, l register.Link) (register.ProjectRecord, error)
	ProjectsOf(ctx context.Context, claimant string) ([]register.ProjectRecord, error)
}

// Streams is a forge's own authenticated push feed, for the forges that have
// one and no webhook a signature covers (Gerrit). forgeauth answers it through
// pkg/forge. A push read from a stream is the same Push a verified delivery
// is, and goes the same way from here: eligibility, tenancy, Enqueue.
type Streams interface {
	// Hosts is every forge host with a feed.
	Hosts() []string
	// Stream holds one subscription to host's feed: ready once it is
	// accepted, then emit per push — a nil error for a push, the reason for a
	// claim the forge layer refused. It returns when ctx ends or the feed
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

// Backend is build's private half, as this role sees it: build.proto's five
// RPCs and nothing else. It is an interface here rather than
// services/build/clients' concrete client so that every route in this package
// is testable with a fake and no libp2p host — and so that this package cannot
// reach past the five RPCs into anything else the backend holds.
type Backend interface {
	Enqueue(ctx context.Context, repositoryKey, ref, commitSHA string) (jobID string, err error)
	Get(ctx context.Context, jobID string) (*buildv2.Job, error)
	// List hands each id to emit as it arrives, so neither this process nor the
	// backend has to hold a project's whole job history to answer.
	List(ctx context.Context, projectID string, emit func(jobID string) error) error
	Cancel(ctx context.Context, jobID string) error
	Retry(ctx context.Context, jobID string) error
	// Follow streams a running build's output to emit, from `from` to the end
	// of it, and returns the offset it reached. Only the backend node writing
	// the log can answer; the others say Unavailable, which is what makes a
	// pooled caller walk to the writer.
	Follow(ctx context.Context, jobID string, from int64, emit func(data []byte, next int64) error) (int64, error)
}

// Access is what an Identity concluded about one caller and one repository.
//
// THREE OUTCOMES, NOT A BOOL AND AN ERROR, because "the token is fine and
// grants nothing" and "that is not a usable token" are different answers to the
// caller — 404 and 401 — and a (bool, error) pair can only carry them by
// agreeing on a sentinel error across a package boundary. AccessDenied is the
// ZERO VALUE, so an implementation that returns nothing denies.
type Access int

const (
	// AccessDenied: the caller is who they say they are and may not write here.
	AccessDenied Access = iota
	// AccessGranted: the caller may write to this repository.
	AccessGranted
	// AccessUnauthenticated: the credential is not usable at all.
	AccessUnauthenticated
)

func (a Access) String() string {
	switch a {
	case AccessGranted:
		return "granted"
	case AccessUnauthenticated:
		return "unauthenticated"
	default:
		return "denied"
	}
}

// Identity resolves a caller's credential into what it may do.
type Identity interface {
	// WriteAccess reports whether the holder of token may write to key.
	//
	// WRITE, NOT READ, and that is services/auth/github.go:116-132's own rule
	// restated where this package needs it rather than invented here: read
	// access is not enough because a public repository reads to everyone, which
	// is exactly what let an unrelated caller reach another project's
	// repositories in v1.
	WriteAccess(ctx context.Context, token string, key repokey.Key) (Access, error)

	// Claimant is who the holder of token is on the forge serving host, as
	// "<host>/<subject>": the stable id, never the login, which can be renamed
	// and then claimed by somebody else.
	//
	// ErrUnknownForge is a host this cloud has no forge for; ErrBadCredential
	// is a token that forge does not accept.
	Claimant(ctx context.Context, token, host string) (string, error)
}

var (
	// ErrUnknownForge is a forge host this cloud does not serve.
	ErrUnknownForge = errors.New("build: no forge serves that host")

	// ErrBadCredential is a token the forge refused outright.
	ErrBadCredential = errors.New("build: the forge does not accept this token")
)

// Credentials are what a caller presented: the forge token every route is
// authorized with, and the tau token that says which of this cloud's users
// they are, when they sent one (TauTokenHeader).
type Credentials struct {
	Forge string
	Tau   string
}

// Admissions is the cloud's answer about a caller the forge has already
// granted: whether the person holding the credential may use this cloud's API
// at all. Optional; without it every caller a forge grants is admitted.
type Admissions interface {
	// Admit reports whether the caller, as the forge serving key knows the
	// holder of their forge token and as their tau token names them, is
	// admitted, and if not, why — in words the caller can act on.
	Admit(ctx context.Context, cred Credentials, key repokey.Key) (admitted bool, reason string, err error)
}
