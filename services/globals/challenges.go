package globals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	pkgcache "github.com/taubyte/tau/pkg/cache"
	"github.com/taubyte/tau/pkg/certificate/acme"
)

// challenges.go holds the answers a CA looks for — DNS-01 TXT values and
// HTTP-01 key authorizations — as cache entries, not records: each lives
// ChallengeTTL unless the order withdraws it first, and expires on its own
// when the order dies with the node that placed it.
//
// One entry per answer, keyed <kind>/<name>/<digest of the value>, so two
// orders proving one name at once — a wildcard and its zone share
// `_acme-challenge.<zone>` — write different keys and neither loses, and an
// order withdrawing its answer removes only its own.

const challengePrefix = "/challenge"

// Challenges reads and writes the answers over a cache store. A globals node
// writes through one; a dns or gateway node reads through its own over the
// same replicated store.
type Challenges struct {
	store pkgcache.Store
	cache *pkgcache.Cache
}

// ChallengesIn opens the challenge cache over st.
func ChallengesIn(ctx context.Context, st pkgcache.Store, now func() time.Time) (*Challenges, error) {
	if st == nil {
		return nil, ErrNoChallenges
	}
	c, err := pkgcache.Open(ctx, st, pkgcache.Config{Prefix: challengePrefix, TTL: ChallengeTTL}, pkgcache.WithClock(now))
	if err != nil {
		return nil, err
	}
	return &Challenges{store: st, cache: c}, nil
}

func challengeName(kind, name string) string {
	if kind == acme.KindDNS {
		return normal(name)
	}
	return name
}

func challengeKey(kind, name, value string) string {
	sum := sha256.Sum256([]byte(value))
	return kind + "/" + challengeName(kind, name) + "/" + hex.EncodeToString(sum[:8])
}

// Present is acme.Presenter's.
func (c *Challenges) Present(ctx context.Context, fqdn, value string) error {
	return c.cache.Put(ctx, challengeKey(acme.KindDNS, fqdn, value), []byte(value))
}

// CleanUp is acme.Presenter's.
func (c *Challenges) CleanUp(ctx context.Context, fqdn, value string) error {
	return c.cache.Delete(ctx, challengeKey(acme.KindDNS, fqdn, value))
}

// PresentHTTP is acme.HTTPPresenter's.
func (c *Challenges) PresentHTTP(ctx context.Context, token, keyAuth string) error {
	return c.cache.Put(ctx, challengeKey(acme.KindHTTP, token, keyAuth), []byte(keyAuth))
}

// CleanUpHTTP is acme.HTTPPresenter's.
func (c *Challenges) CleanUpHTTP(ctx context.Context, token, keyAuth string) error {
	return c.cache.Delete(ctx, challengeKey(acme.KindHTTP, token, keyAuth))
}

// Values are the live answers of kind for name, from whatever the store
// holds — listed from the store and not the cache's own index, which counts
// only what this node wrote.
func (c *Challenges) Values(ctx context.Context, kind, name string) ([]string, error) {
	if kind != acme.KindDNS && kind != acme.KindHTTP {
		return nil, fmt.Errorf("globals: no challenge kind %q", kind)
	}
	prefix := challengePrefix + "/" + kind + "/" + challengeName(kind, name) + "/"
	keys, err := c.store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, k := range keys {
		value, err := c.cache.Get(ctx, strings.TrimPrefix(k, challengePrefix+"/"))
		if errors.Is(err, pkgcache.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, string(value))
	}
	return out, nil
}

// Challenges are the live DNS-01 values for fqdn.
func (c *Challenges) Challenges(ctx context.Context, fqdn string) ([]string, error) {
	return c.Values(ctx, acme.KindDNS, fqdn)
}

// Holds is whether this copy of the store holds value among kind's answers
// for name.
func (c *Challenges) Holds(ctx context.Context, kind, name, value string) (bool, error) {
	values, err := c.Values(ctx, kind, name)
	if err != nil {
		return false, err
	}
	return slices.Contains(values, value), nil
}

// Challenge is the HTTP-01 answer for token from this copy of the store.
func (c *Challenges) Challenge(token string) (string, bool) {
	values, err := c.Values(context.Background(), acme.KindHTTP, token)
	if err != nil || len(values) == 0 {
		return "", false
	}
	return values[0], true
}
