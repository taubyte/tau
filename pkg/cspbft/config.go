package cspbft

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"time"
)

// Signer produces this replica's signature over a preimage. Everything about
// what is behind it — the key, its rotation, an HSM, a worker pool — is the
// caller's, which is why it is a func and not an interface.
type Signer func(preimage []byte) ([]byte, error)

// Verifier checks another REPLICA's signature. A caller that wants the verify
// cost off its hot path puts the cache and the parallelism inside this func;
// this package neither caches nor parallelises, because a cache that is wrong
// about a key is a fault model that is wrong about everything.
type Verifier func(from ReplicaID, preimage, sig []byte) error

// ClientVerifier checks a CLIENT's signature on a request.
//
// IT IS A SEPARATE TYPE FROM Verifier ON PURPOSE. A client identity can then
// never reach code that counts quorum members, and the confusion that would
// let a client's signature stand in for a replica's vote is a compile error
// rather than a review comment.
type ClientVerifier func(from ClientID, preimage, sig []byte) error

// Hasher digests a proposal or a preimage. Default sha256.
type Hasher func(b []byte) Digest

const (
	// DefaultCheckpointInterval is K, the paper's own example. It is the
	// cheapest steady state — a checkpoint makes the application digest its
	// whole state — but it multiplies view-change cost quadratically in f, so a
	// deployment near the top of the supported n range should lower it.
	DefaultCheckpointInterval = 100

	// DefaultBaseTimeout is T: how long a backup waits for its request to
	// execute before it gives up on the primary.
	DefaultBaseTimeout = 4 * time.Second

	// MaxCheckpointInterval is the ceiling on K. The log holds 2K entries and
	// onCheckpoint reaches 4K, so past a quarter of the sequence space the high water
	// mark would wrap below the low one.
	MaxCheckpointInterval = 1 << 20

	// DefaultMaxTimeout is the ceiling on the doubling. Without one, a
	// partition long enough drives the timeout past any useful horizon and the
	// cluster takes hours to notice it healed.
	DefaultMaxTimeout = 5 * time.Minute
)

// Option configures a Replica. The replica set and self stay positional because they
// cannot be defaulted; the rest are validated in the option.
type Option func(*options) error

type options struct {
	sign     Signer
	verify   Verifier
	verifyCl ClientVerifier
	hash     Hasher
	scorer   Scorer
	interval uint64
	base     time.Duration
	max      time.Duration
}

// WithSigner installs the signing function. Required.
func WithSigner(s Signer) Option {
	return func(o *options) error {
		if s == nil {
			return ErrNoSigner
		}
		o.sign = s
		return nil
	}
}

// WithVerifier installs replica signature verification. Required: there is no
// default, because a default that accepts everything is a Byzantine protocol
// with no adversary.
func WithVerifier(v Verifier) Option {
	return func(o *options) error {
		if v == nil {
			return ErrNoVerifier
		}
		o.verify = v
		return nil
	}
}

// WithClientVerifier installs client signature verification. Required.
func WithClientVerifier(v ClientVerifier) Option {
	return func(o *options) error {
		if v == nil {
			return ErrNoClientVerifier
		}
		o.verifyCl = v
		return nil
	}
}

// WithHasher replaces sha256. A caller only needs this to match a digest
// function its application already commits to.
func WithHasher(h Hasher) Option {
	return func(o *options) error {
		if h == nil {
			return fmt.Errorf("cspbft: a nil hasher")
		}
		o.hash = h
		return nil
	}
}

// WithScorer installs the leader-selection heuristic. Default RoundRobin,
// which is textbook PBFT.
func WithScorer(s Scorer) Option {
	return func(o *options) error {
		if s == nil {
			return fmt.Errorf("cspbft: a nil scorer: the default is RoundRobin, not nothing")
		}
		o.scorer = s
		return nil
	}
}

// WithCheckpointInterval sets K; the log holds L = 2K sequence numbers.
func WithCheckpointInterval(k uint64) Option {
	return func(o *options) error {
		if k == 0 {
			return fmt.Errorf("%w: checkpoint interval 0: a log that is never truncated grows without bound", ErrInvalidInterval)
		}
		if k > MaxCheckpointInterval {
			return fmt.Errorf("%w: checkpoint interval %d is above %d, at which the log is already larger than any deployment wants and 4K wraps the water marks",
				ErrInvalidInterval, k, uint64(MaxCheckpointInterval))
		}
		o.interval = k
		return nil
	}
}

// WithBaseTimeout sets T, the first request timeout. It doubles on every view
// change that does not make progress.
func WithBaseTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("%w: base timeout %s: every request would time out before the primary could see it", ErrInvalidTimeout, d)
		}
		o.base = d
		return nil
	}
}

// WithMaxTimeout caps the doubling.
func WithMaxTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return fmt.Errorf("%w: max timeout %s", ErrInvalidTimeout, d)
		}
		o.max = d
		return nil
	}
}

func (o *options) withDefaults() error {
	if o.hash == nil {
		o.hash = func(b []byte) Digest { return sha256.Sum256(b) }
	}
	if o.scorer == nil {
		o.scorer = RoundRobin{}
	}
	if o.interval == 0 {
		o.interval = DefaultCheckpointInterval
	}
	if o.base == 0 {
		o.base = DefaultBaseTimeout
	}
	if o.max == 0 {
		o.max = DefaultMaxTimeout
	}
	if o.sign == nil {
		return ErrNoSigner
	}
	if o.verify == nil {
		return ErrNoVerifier
	}
	if o.verifyCl == nil {
		return ErrNoClientVerifier
	}
	if o.max < o.base {
		return fmt.Errorf("%w: max timeout %s is below the base %s, so the first doubling would shorten the wait", ErrInvalidTimeout, o.max, o.base)
	}
	return nil
}

// New builds a replica. The replica set is deduped and sorted here, so replicas
// given the same set in different orders compute the same primary. The index is
// never exported.
func New(replicas []ReplicaID, self ReplicaID, opts ...Option) (*Replica, error) {
	o := &options{}
	for _, opt := range opts {
		if err := opt(o); err != nil {
			return nil, err
		}
	}
	if err := o.withDefaults(); err != nil {
		return nil, err
	}

	set := append([]ReplicaID(nil), replicas...)
	sort.Slice(set, func(i, j int) bool { return set[i] < set[j] })
	for i := 1; i < len(set); i++ {
		if set[i] == set[i-1] {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateReplica, set[i])
		}
	}
	if len(set) < 4 {
		return nil, fmt.Errorf("%w: %d given", ErrTooFewReplicas, len(set))
	}
	if !contains(set, self) {
		return nil, fmt.Errorf("%w: %q", ErrSelfNotInSet, self)
	}

	r := &Replica{
		opts:           o,
		replicas:       set,
		self:           self,
		n:              len(set),
		f:              Faults(len(set)),
		q:              Quorum(len(set)),
		k:              o.interval,
		rot:            newRotation(o.scorer, o.hash, set),
		log:            newLog(),
		requests:       map[Digest]Message{},
		orphans:        map[Digest][]Message{},
		outstanding:    map[Digest]bool{},
		assigned:       map[Digest]bool{},
		executedAt:     map[Digest]Sequence{},
		checkpoints:    map[checkpointKey]map[ReplicaID]Message{},
		viewChanges:    map[View]map[ReplicaID]Message{},
		sentViewChange: map[View]bool{},
		sentNewView:    map[View]bool{},
		timerRunning:   map[Timer]bool{},
		timeout:        o.base,
	}
	return r, nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Faults is the most faulty replicas a configuration of n tolerates: (n-1)/3.
func Faults(n int) int {
	if n < 1 {
		return 0
	}
	return (n - 1) / 3
}

// Quorum is how many replicas must agree for two decisions never to
// contradict: ceil((n+f+1)/2). At n = 3f+1 it is 2f+1, the familiar number.
//
// The general form matters because n is not always 3f+1: at n = 6, f is still
// 1 and the quorum is 4 rather than 3, and a hard-coded 2f+1 would be a quorum
// two correct replicas could straddle.
func Quorum(n int) int {
	f := Faults(n)
	return (n + f + 2) / 2
}

// Advice is what a human should be told about a proposed n: replicas that buy no
// fault tolerance, and an n whose view change dominates the cost.
func Advice(n int) []string {
	var out []string
	if n < 4 {
		return []string{fmt.Sprintf("n=%d tolerates no byzantine faults: three replicas cannot distinguish a liar from a partition", n)}
	}
	f := Faults(n)
	q := Quorum(n)
	out = append(out, fmt.Sprintf("n=%d tolerates %s and needs a quorum of %d", n, plural(f, "faulty replica"), q))
	if n%3 != 1 {
		smaller := 3*f + 1
		out = append(out, fmt.Sprintf("n=%d tolerates the same %s as n=%d and needs a quorum of %d instead of %d",
			n, plural(f, "fault"), smaller, q, Quorum(smaller)))
	}
	if n > 16 {
		out = append(out, fmt.Sprintf("n=%d is above the supported range: view change is O(n^3) bits, so at this size it is the dominant cost and a linear-view-change protocol is the answer, not a tuning of this one", n))
	}
	return out
}
