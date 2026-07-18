package engine

import (
	"fmt"
	"time"

	"github.com/alecthomas/units"
	"github.com/taubyte/tau/pkg/tcc/object"
)

// ScalarSpec is the whole meaning of a scalar-typed DSL term (Duration, Bytes):
// the codec that maps its authored string to/from the typed wire value, plus the
// Go struct-field type a generator emits. Carrying both on the term is what lets
// the driver read Parse/Format and tcc-gen read GoType without either restating a
// per-scalar switch — the term's meaning lives in exactly one place. Generic: the
// codecs touch only object.Selector, time and units, so engine keeps no taubyte
// dependency.
type ScalarSpec struct {
	ID     string
	GoType string
	Parse  func(object.Selector[object.Refrence], string) error // authored -> wire
	Format func(object.Selector[object.Refrence], string) error // wire -> authored
}

// parseDuration parses a duration string from field and sets it as nanoseconds.
// A missing or nil field is a no-op (nil error); a non-string or unparsable value
// is a wrapped error.
func parseDuration(sel object.Selector[object.Refrence], field string) error {
	timeout, err := sel.Get(field)
	if err != nil {
		// Field doesn't exist, which is fine
		return nil
	}

	// Field exists but is nil, which is also fine
	if timeout == nil {
		return nil
	}

	timeoutStr, ok := timeout.(string)
	if !ok {
		return fmt.Errorf("%s is not a string", field)
	}

	timeoutDur, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return fmt.Errorf("parsing %s failed with %w", field, err)
	}

	return sel.Set(field, timeoutDur.Nanoseconds())
}

// parseBytes parses a memory-size string from field and sets it as bytes (int64).
// A missing or nil field is a no-op (nil error); a non-string or unparsable value
// is a wrapped error.
func parseBytes(sel object.Selector[object.Refrence], field string) error {
	memory, err := sel.Get(field)
	if err != nil {
		// Field doesn't exist, which is fine
		return nil
	}

	// Field exists but is nil, which is also fine
	if memory == nil {
		return nil
	}

	memoryStr, ok := memory.(string)
	if !ok {
		return fmt.Errorf("%s is not a string", field)
	}

	memoryInt, err := units.ParseStrictBytes(memoryStr)
	if err != nil {
		return fmt.Errorf("parsing %s failed with %w", field, err)
	}

	return sel.Set(field, memoryInt)
}

// formatDuration converts nanoseconds at field back to a duration string. A
// missing or nil field is a no-op (nil error); a non-integer value is an error.
func formatDuration(sel object.Selector[object.Refrence], field string) error {
	timeout, err := sel.Get(field)
	if err != nil {
		// Field doesn't exist, which is fine
		return nil
	}

	// Field exists but is nil, which is also fine
	if timeout == nil {
		return nil
	}

	var timeoutNs int64
	switch v := timeout.(type) {
	case int64:
		timeoutNs = v
	case int:
		timeoutNs = int64(v)
	case int32:
		timeoutNs = int64(v)
	default:
		return fmt.Errorf("%s is not an integer", field)
	}

	timeoutDur := time.Duration(timeoutNs)
	return sel.Set(field, timeoutDur.String())
}

// formatBytes converts bytes (int64) at field back to a human-readable size
// string. A missing or nil field is a no-op (nil error); a non-integer value is
// an error.
func formatBytes(sel object.Selector[object.Refrence], field string) error {
	memory, err := sel.Get(field)
	if err != nil {
		// Field doesn't exist, which is fine
		return nil
	}

	// Field exists but is nil, which is also fine
	if memory == nil {
		return nil
	}

	var memoryBytes int64
	switch v := memory.(type) {
	case int64:
		memoryBytes = v
	case int:
		memoryBytes = int64(v)
	case int32:
		memoryBytes = int64(v)
	default:
		return fmt.Errorf("%s is not an integer", field)
	}

	memoryStr := units.MetricBytes(float64(memoryBytes)).String()
	return sel.Set(field, memoryStr)
}
