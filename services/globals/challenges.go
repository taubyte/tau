package globals

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	pkgcache "github.com/taubyte/tau/pkg/cache"
)

// challenges.go holds DNS-01 values as cache entries, not records: each lives
// ChallengeTTL unless its order withdraws it first, and expires on its own
// when the order dies with the node that placed it.
//
// One entry per presentation, keyed dns-01/<fqdn>/<presentation id>, so two
// orders proving one name at once — a wildcard and its zone share
// `_acme-challenge.<zone>`, and two nodes may order one name — write
// different keys, and each withdraws only its own.

const challengePrefix = "/challenge"

// ChallengeWait bounds how long a reader holding no value waits for one to
// replicate to it.
const ChallengeWait = 5 * time.Second

const challengePoll = 100 * time.Millisecond

// ErrNoValue is a name with no value presented within the wait.
var ErrNoValue = errors.New("globals: no DNS-01 value is presented for that name")

// Challenges reads and writes DNS-01 values over this node's copy of the
// replicated challenge store.
type Challenges struct {
	store pkgcache.Store
	cache *pkgcache.Cache
	wait  time.Duration

	mu      sync.Mutex
	present map[string]string
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
	return &Challenges{store: st, cache: c, wait: ChallengeWait, present: map[string]string{}}, nil
}

func namePrefix(fqdn string) string { return ChallengeDNS + "/" + normal(fqdn) + "/" }

// Present is acme.Presenter's.
func (c *Challenges) Present(ctx context.Context, fqdn, value string) error {
	b := make([]byte, 8)
	rand.Read(b)
	key := namePrefix(fqdn) + hex.EncodeToString(b)
	if err := c.cache.Put(ctx, key, []byte(value)); err != nil {
		return err
	}
	c.mu.Lock()
	c.present[normal(fqdn)+"\x00"+value] = key
	c.mu.Unlock()
	return nil
}

// CleanUp is acme.Presenter's: it withdraws this node's presentation only.
func (c *Challenges) CleanUp(ctx context.Context, fqdn, value string) error {
	c.mu.Lock()
	id := normal(fqdn) + "\x00" + value
	key, ok := c.present[id]
	delete(c.present, id)
	c.mu.Unlock()
	if !ok {
		return nil
	}
	return c.cache.Delete(ctx, key)
}

// values are the live values for fqdn in this copy — listed from the store
// and not the cache's own index, which counts only what this node wrote.
func (c *Challenges) values(ctx context.Context, fqdn string) ([]string, error) {
	keys, err := c.store.List(ctx, challengePrefix+"/"+namePrefix(fqdn))
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

// Challenges are the live DNS-01 values for fqdn. Holding none, it waits up
// to the challenge wait for one to replicate here, and then is ErrNoValue:
// a CA asks only while an order proves the name, so nothing here yet is not
// its absence.
func (c *Challenges) Challenges(ctx context.Context, fqdn string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.wait)
	defer cancel()
	for {
		values, err := c.values(ctx, fqdn)
		if err != nil || len(values) > 0 {
			return values, err
		}
		select {
		case <-ctx.Done():
			return nil, ErrNoValue
		case <-time.After(challengePoll):
		}
	}
}
