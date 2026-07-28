package engine

import (
	"strconv"
	"strings"
)

// Notation tolerance. YAML infers a scalar's type from how it was written, but
// the DSL declares what a field MEANS — and the two disagree over quoting all
// the time. A repository id is a string field routinely authored unquoted
// (`id: 485476045`), and a numeric field is just as routinely authored quoted.
// Rejecting either would be pedantry about notation, not a real disagreement
// about the value, so a scalar is converted to its declared type wherever the
// conversion is unambiguous.
//
// Booleans deliberately do NOT take part. `true` where a string is declared, or
// 1 where a bool is, is a mistake about the field rather than a notation
// choice — and a numeric field given a non-numeric string is a genuine error,
// which is exactly what it stays.

// coerceScalar converts an authored scalar to the declared type, reporting
// whether the conversion applied. A value already of the right type never
// reaches here — this is the fallback after a typed read fails.
func coerceScalar(raw any, t Type) (any, bool) {
	switch t {
	case TypeString:
		switch n := raw.(type) {
		case int:
			return strconv.Itoa(n), true
		case int64:
			return strconv.FormatInt(n, 10), true
		case uint64:
			return strconv.FormatUint(n, 10), true
		case float64:
			return strconv.FormatFloat(n, 'f', -1, 64), true
		}
	case TypeInt:
		if s, ok := raw.(string); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
				return n, true
			}
		}
	case TypeFloat:
		if s, ok := raw.(string); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
				return f, true
			}
		}
	}
	return nil, false
}

// convertible reports whether a value of the wrong Go type is nonetheless an
// acceptable notation for the declared type — the check-side twin of
// coerceScalar, used by partial validation so it agrees with what will compile.
func convertible(value any, t Type) bool {
	_, ok := coerceScalar(value, t)
	return ok
}
