package schema

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
)

// A duration is Go's grammar with one more unit, d, a day of 24 hours (S6): thirty
// days is "30d", leading the rest as in "1d12h", and a whole number of days reads back
// as days. The codec is this vocabulary's own; the engine's, which v1 shares, keeps
// Go's grammar.

// durationPattern is the grammar for a JSON Schema validator, which then refuses what
// the load-time validator does.
const durationPattern = `^[-+]?(([0-9]+(\.[0-9]+)?d([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))*)|([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+|0)$`

const day = 24 * time.Hour

var leadingDays = regexp.MustCompile(`^([-+]?)([0-9]+(?:\.[0-9]+)?)d(.*)$`)

// Duration is a span of time in this vocabulary's grammar, compiled to nanoseconds.
func Duration[T string | AKey](name T, o ...engine.Option) *Attribute {
	return engine.String(name, append(o,
		engine.Annotate("scalar", engine.ScalarSpec{ID: "duration", GoType: "uint64", Parse: compileDuration, Format: decompileDuration}),
		engine.Annotate("pattern", durationPattern),
		engine.Validator(func(s string) error {
			if _, err := parseDuration(s); err != nil {
				return fmt.Errorf("invalid duration %q: %w", s, err)
			}
			return nil
		}),
	)...)
}

// parseDuration reads s: Go's grammar, led by days or not.
func parseDuration(s string) (time.Duration, error) {
	m := leadingDays.FindStringSubmatch(s)
	if m == nil {
		return time.ParseDuration(s)
	}
	d, err := daysOf(m[2])
	if err != nil {
		return 0, fmt.Errorf("%q: %w", s, err)
	}
	if rest := m[3]; rest != "" {
		if strings.ContainsAny(rest[:1], "+-") {
			return 0, errors.New("a sign leads the whole duration, not a part of it")
		}
		more, err := time.ParseDuration(rest)
		if err != nil {
			return 0, err
		}
		if d > math.MaxInt64-more {
			return 0, fmt.Errorf("%q: longer than a duration holds", s)
		}
		d += more
	}
	if m[1] == "-" {
		d = -d
	}
	return d, nil
}

// daysOf is n days, whole ones counted exactly.
func daysOf(n string) (time.Duration, error) {
	if !strings.Contains(n, ".") {
		whole, err := strconv.ParseInt(n, 10, 64)
		if err != nil || whole > math.MaxInt64/int64(day) {
			return 0, errors.New("longer than a duration holds")
		}
		return time.Duration(whole) * day, nil
	}
	days, err := strconv.ParseFloat(n, 64)
	if err != nil {
		return 0, err
	}
	if days*float64(day) >= math.MaxInt64 {
		return 0, errors.New("longer than a duration holds")
	}
	return time.Duration(days * float64(day)), nil
}

// formatDuration writes d as days when it is a whole number of them, and in Go's
// grammar otherwise.
func formatDuration(d time.Duration) string {
	if d != 0 && d%day == 0 {
		return strconv.FormatInt(int64(d/day), 10) + "d"
	}
	return d.String()
}

func compileDuration(sel object.Selector[object.Refrence], field string) error {
	v, err := sel.Get(field)
	if err != nil || v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("%s is not a string", field)
	}
	d, err := parseDuration(s)
	if err != nil {
		return fmt.Errorf("parsing %s failed with %w", field, err)
	}
	return sel.Set(field, d.Nanoseconds())
}

func decompileDuration(sel object.Selector[object.Refrence], field string) error {
	v, err := sel.Get(field)
	if err != nil || v == nil {
		return nil
	}
	var ns int64
	switch n := v.(type) {
	case int64:
		ns = n
	case int:
		ns = int64(n)
	case int32:
		ns = int64(n)
	default:
		return fmt.Errorf("%s is not an integer", field)
	}
	return sel.Set(field, formatDuration(time.Duration(ns)))
}
