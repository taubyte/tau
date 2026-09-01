package queue

import (
	"math"
	"time"
)

// maxDuration is the longest representable delay.
const maxDuration = time.Duration(math.MaxInt64)

// backoff is the delay before delivery n+1, given that delivery n just failed:
// RetryInterval * RetryFactor^(n-1), capped by RetryCeiling. A zero interval
// means the visibility timeout alone governs, which is what happened before
// these fields existed, so it returns zero.
//
// attempts is the number of deliveries so far, so it is at least one whenever a
// delivery has failed; a zero is treated as the first.
func backoff(cfg Config, attempts uint32) time.Duration {
	if cfg.RetryInterval == 0 {
		return 0
	}
	if attempts > 0 {
		attempts--
	}

	ceiling := maxDuration
	if cfg.RetryCeiling > 0 {
		ceiling = cfg.RetryCeiling
	}

	// math.Pow reaches +Inf long before a duration overflows, and converting
	// either +Inf or a float at or above 2^63 to an int64 is undefined, so the
	// comparison is made in float space and the cap is returned as itself.
	d := float64(cfg.RetryInterval) * math.Pow(cfg.RetryFactor, float64(attempts))
	if !(d < float64(ceiling)) { // also catches NaN and +Inf
		return ceiling
	}
	return time.Duration(d)
}

// addClamped is t+d without wrapping past the end of the representable range,
// which an uncapped exponential backoff will otherwise reach.
func addClamped(t time.Time, d time.Duration) time.Time {
	nanos := t.UnixNano()
	if d > 0 && nanos > math.MaxInt64-int64(d) {
		return time.Unix(0, math.MaxInt64)
	}
	return time.Unix(0, nanos+int64(d))
}
