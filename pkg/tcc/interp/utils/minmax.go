package utils

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
)

// ValidateMinMax checks that the value at maxKey is >= the value at minKey.
// If either key is missing or not an int, no error is returned (validation is skipped).
// Returns an error if both values are present and max < min.
func ValidateMinMax(sel object.Selector[object.Refrence], minKey, maxKey string) error {
	minVal, err := sel.GetInt(minKey)
	if err != nil {
		return nil
	}
	maxVal, err := sel.GetInt(maxKey)
	if err != nil {
		return nil
	}
	if maxVal < minVal {
		return fmt.Errorf("%s (%d) must be >= %s (%d)", maxKey, maxVal, minKey, minVal)
	}
	return nil
}
