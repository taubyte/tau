package engine

import (
	"reflect"
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
//
// Numbers are matched by KIND, not by a list of Go types. The conversion side
// and the check side have to agree about what counts as a number, and two
// hand-written lists drift: they already disagreed about int32, so a value a
// numeric field accepted was rejected by a string one.

// numberString renders any numeric value as its decimal text, reporting false
// for anything that is not a number.
func numberString(v any) (string, bool) {
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(r.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(r.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(r.Float(), 'f', -1, 64), true
	}
	return "", false
}

// isInteger reports a whole-number value. A float is NOT one: `5.0` was written
// as a float and a field declared integer should say so.
func isInteger(v any) bool {
	switch reflect.ValueOf(v).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

// coerceScalar converts an authored scalar to the declared type, reporting
// whether the conversion applied. A value already of the right type never
// reaches here — this is the fallback after a typed read fails.
func coerceScalar(raw any, t Type) (any, bool) {
	switch t {
	case TypeString:
		if s, ok := numberString(raw); ok {
			return s, true
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
