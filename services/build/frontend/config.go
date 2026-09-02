package frontend

import (
	"context"
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/git/repokey"
	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/auth/v1"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
)

// Defaults. Fields on Config set through New, never mutable package globals —
// services/patrick/service.go's BootstrapTime, DefaultReAnnounceJobTime and
// MaxReAnnounceJobs are those, shared by every instance in a process, and
// services/patrick/types.go already records that as a fixed race.
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
	// is the window in which a rotated secret still verifies, so it is short.
	DefaultSecretCacheTTL = time.Minute
	// DefaultSecretCacheMax bounds the cache and the per-hook rate limiter
	// alike. Both are keyed by a string a stranger chooses, so both need a
	// ceiling; see ratelimit.go for what that ceiling costs.
	DefaultSecretCacheMax = 1024
)

// DefaultHookRate and DefaultWebhookRate are the two limits, and they are two
// because they defend different things. See ratelimit.go.
var (
	DefaultHookRate    = Rate{Burst: 30, Per: time.Minute}
	DefaultWebhookRate = Rate{Burst: 300, Per: time.Minute}
)

// DefaultEligibleRefs is RESTATED, not imported. legacy/specs/common's
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

	// HookRate limits one hook id; WebhookRate limits the route. The ZERO Rate
	// means the matching default, so a config that names neither is limited
	// rather than open. Turning a limit off takes a period with a burst of
	// zero — deliberately not the shape a half-filled config falls into.
	HookRate    Rate `yaml:"hookRate"`
	WebhookRate Rate `yaml:"webhookRate"`

	// SecretCacheTTL and SecretCacheMax bound how long and how much of auth's
	// answer this process keeps. Zero means the matching default.
	SecretCacheTTL time.Duration `yaml:"secretCacheTTL"`
	SecretCacheMax int           `yaml:"secretCacheMax"`
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
	if c.HookRate == (Rate{}) {
		c.HookRate = DefaultHookRate
	}
	if c.WebhookRate == (Rate{}) {
		c.WebhookRate = DefaultWebhookRate
	}
	return c, nil
}

// Hooks is what auth knows about one webhook.
type Hooks interface {
	// Get returns the GithubHook registered under id.
	//
	// It hands back the generated message rather than two bare strings because
	// THE PAIR DECIDES AN AUTHENTICATION: `secret` verifies the body, and
	// `repository_key` says which repository that signature is good for. Two
	// same-typed returns are transposable, and v1 fetched only the first of
	// them — services/patrick/github.go's githubCheckHookAndExtractSecret sets
	// exactly one context variable — which is the whole of how a signature
	// under one hook came to authorize a job for any repository.
	Get(ctx context.Context, hookID string) (*authv1.GithubHook, error)
}

// Projects is what auth knows about who owns a project.
type Projects interface {
	// Repositories returns the repository keys that gate a project: write
	// access to any one of them is write access to the project.
	Repositories(ctx context.Context, projectID string) ([]string, error)
}

// Backend is build's private half, as this role sees it: build.proto's five
// RPCs and nothing else. It is an interface here rather than
// services/build/clients' concrete client so that every route in this package
// is testable with a fake and no libp2p host — and so that this package cannot
// reach past the five RPCs into anything else the backend holds.
type Backend interface {
	Enqueue(ctx context.Context, repositoryKey, ref, commitSHA string) (jobID string, err error)
	Get(ctx context.Context, jobID string) (*buildv1.Job, error)
	// List hands each id to emit as it arrives, so neither this process nor the
	// backend has to hold a project's whole job history to answer.
	List(ctx context.Context, projectID string, emit func(jobID string) error) error
	Cancel(ctx context.Context, jobID string) error
	Retry(ctx context.Context, jobID string) error
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
}
