package globals

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	pkgcache "github.com/taubyte/tau/pkg/cache"
)

// challenges.go holds DNS-01 values as cache entries, not records: each lives
// ChallengeTTL unless the order withdraws it first, and expires on its own
// when the order dies with the node that placed it.
//
// One entry per value, keyed <fqdn>/<digest of the value>, so two orders
// proving one name at once — a wildcard and its zone share
// `_acme-challenge.<zone>` — write different keys and neither loses.

const challengePrefix = "/challenge"

// Challenges reads and writes DNS-01 values over a cache store. A globals node
// writes through one; a dns node reads through its own over the same
// replicated store.
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

func challengeKey(fqdn, value string) string {
	sum := sha256.Sum256([]byte(value))
	return normal(fqdn) + "/" + hex.EncodeToString(sum[:8])
}

// Present is acme.Presenter's.
func (c *Challenges) Present(ctx context.Context, fqdn, value string) error {
	return c.cache.Put(ctx, challengeKey(fqdn, value), []byte(value))
}

// CleanUp is acme.Presenter's.
func (c *Challenges) CleanUp(ctx context.Context, fqdn, value string) error {
	return c.cache.Delete(ctx, challengeKey(fqdn, value))
}

// Challenges are the live values for fqdn, from whatever the store holds —
// listed from the store and not the cache's own index, which counts only
// what this node wrote.
func (c *Challenges) Challenges(ctx context.Context, fqdn string) ([]string, error) {
	prefix := challengePrefix + "/" + normal(fqdn) + "/"
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
