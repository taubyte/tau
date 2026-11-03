package queue

import (
	"fmt"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// Defaults are the schema's defaults for the queue kind, plus a store timeout
// this package needs and the schema does not describe.
const (
	DefaultVisibilityTimeout = 30 * time.Second
	DefaultMaxAttempts       = 5
	DefaultApplyTimeout      = 10 * time.Second
)

// DeadLetterSuffix is appended to a queue's name to name its dead-letter queue
// when the configuration does not name one.
const DeadLetterSuffix = "/dead"

// Config is what an operator declared. It is a plain value: no live objects, no
// setters, and it round-trips through yaml. The clock and the store arrive
// separately, through New and its options.
type Config struct {
	// Name identifies this queue within the store. It is one key segment, so it
	// may contain any character — it is encoded before it becomes one.
	Name string `yaml:"name"`

	// VisibilityTimeout is how long a claimed item stays invisible before it
	// becomes deliverable again. Zero means DefaultVisibilityTimeout.
	VisibilityTimeout time.Duration `yaml:"visibilityTimeout"`

	// Delay is how long a pushed item waits before it is deliverable at all.
	// Zero means immediately.
	Delay time.Duration `yaml:"delay"`

	// MaxAttempts is how many times an item may be delivered before it moves to
	// the dead-letter queue. Zero means DefaultMaxAttempts.
	MaxAttempts uint32 `yaml:"maxAttempts"`

	// RetryInterval is the delay before a failed delivery becomes deliverable
	// again. Zero means the visibility timeout alone governs, i.e. no extra wait.
	RetryInterval time.Duration `yaml:"retryInterval"`

	// RetryFactor multiplies the interval per further attempt: the delay after
	// attempt n is RetryInterval * RetryFactor^(n-1). Zero or one is a fixed
	// interval.
	RetryFactor float64 `yaml:"retryFactor"`

	// RetryCeiling caps that delay. Zero is uncapped.
	RetryCeiling time.Duration `yaml:"retryCeiling"`

	// DeadLetter is the name of the queue that receives items which exhausted
	// MaxAttempts. Empty means Name+DeadLetterSuffix, which is a queue like any
	// other: construct a Queue on that name to drain it.
	DeadLetter string `yaml:"deadLetter"`

	// ApplyTimeout bounds each store write and barrier. Zero means
	// DefaultApplyTimeout. It may not exceed raft.MaxApplyTimeout.
	ApplyTimeout time.Duration `yaml:"applyTimeout"`

	// MaxBytes is how much the queue's items may hold, ready and claimed together:
	// a push that would take it past this is refused with [ErrFull]. Zero is no
	// bound.
	MaxBytes int64 `yaml:"maxBytes"`
}

// withDefaults returns the configuration with zero values filled in, or an error
// for a value that has no sensible reading.
func (c Config) withDefaults() (Config, error) {
	if c.Name == "" {
		return c, ErrNoName
	}
	type namedDuration struct {
		name  string
		value time.Duration
	}
	for _, d := range []namedDuration{
		{"visibilityTimeout", c.VisibilityTimeout},
		{"delay", c.Delay},
		{"retryInterval", c.RetryInterval},
		{"retryCeiling", c.RetryCeiling},
		{"applyTimeout", c.ApplyTimeout},
	} {
		if d.value < 0 {
			return c, fmt.Errorf("%w: %s must not be negative", ErrInvalidConfig, d.name)
		}
	}
	if c.MaxBytes < 0 {
		return c, fmt.Errorf("%w: maxBytes must not be negative", ErrInvalidConfig)
	}
	if c.RetryFactor < 0 {
		return c, fmt.Errorf("%w: retryFactor must not be negative", ErrInvalidConfig)
	}
	if c.ApplyTimeout > raft.MaxApplyTimeout {
		return c, fmt.Errorf("%w: applyTimeout must not exceed %s", ErrInvalidConfig, raft.MaxApplyTimeout)
	}

	if c.VisibilityTimeout == 0 {
		c.VisibilityTimeout = DefaultVisibilityTimeout
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = DefaultMaxAttempts
	}
	if c.RetryFactor == 0 {
		c.RetryFactor = 1
	}
	if c.ApplyTimeout == 0 {
		c.ApplyTimeout = DefaultApplyTimeout
	}
	if c.DeadLetter == "" {
		c.DeadLetter = c.Name + DeadLetterSuffix
	}
	if c.DeadLetter == c.Name {
		return c, ErrSelfDeadLetter
	}
	return c, nil
}
