package utils

import "time"

// ParseDuration parses a config duration string (e.g. "168h"): "" → 0, malformed
// → error. Canonical config duration parser — pkg/schema/common.StringToTime and
// the ee enterprise config both delegate here so parsing stays consistent
// regardless of how the schema layer evolves.
func ParseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}
