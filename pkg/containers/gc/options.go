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

// Reference scopes the sweep to a single image reference. Without it the
// collector removes every image on the host old enough to qualify, including
// ones that have nothing to do with tau.
func Reference(ref string) Option {
	return func(o *config) error {
		o.reference = ref
		return nil
	}
}
