package common

import (
	"github.com/alecthomas/units"
	"golang.org/x/exp/constraints"
)

// UnitsToString converts size in bytes to a unit in form 1.3KB OR 6.3GB
func UnitsToString[T constraints.Integer](size T) string {
	return units.MetricBytes(float64(size)).String()
}

// StringToUnits converts size in a unit in form 1.3KB OR 6.3GB to bytes
func StringToUnits(size string) (uint64, error) {
	if size == "" {
		return 0, nil
	}

	base, err := units.ParseMetricBytes(size)
	if err != nil {
		return 0, err
	}

	return uint64(base), nil
}
