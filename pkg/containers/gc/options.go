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

func Filter(key, value string) Option {
	return func(o *config) error {
		o.filters.Add(key, value)
		return nil
	}
}
