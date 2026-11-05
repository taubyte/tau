package gc

import (
	"time"
)

type Option func(o *config) error

func Interval(t time.Duration) Option {
	return func(o *config) error {
		o.interval = t
		return nil
	}
}

func MaxAge(t time.Duration) Option {
	return func(o *config) error {
		o.maxAge = t
		return nil
	}
}

// Reference sweeps every image matching ref that was built more than maxAge
// ago, whoever pulled it. Without it the collector only removes images this
// process used and then left idle for maxAge.
func Reference(ref string) Option {
	return func(o *config) error {
		o.reference = ref
		return nil
	}
}
