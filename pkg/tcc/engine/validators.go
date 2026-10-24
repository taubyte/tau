package engine

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ipfs/go-cid"
)

// NextValidation represents a validation that needs to be performed externally.
// The compiler emits these during compilation, and it's up to the caller to
// implement and process these validations.
type NextValidation struct {
	Key       string         `json:"key"`       // identifier for the validation (e.g., "domain", "fqdn")
	Value     any            `json:"value"`     // value to validate
	Validator string         `json:"validator"` // validator name (e.g., "dns", "cid")
	Context   map[string]any `json:"context"`   // extra context for validation
}

// NewNextValidation creates a new NextValidation instance.
func NewNextValidation(key string, value any, validator string, context map[string]any) NextValidation {
	return NextValidation{
		Key:       key,
		Value:     value,
		Validator: validator,
		Context:   context,
	}
}

var varNameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func IsVariableName() Option {
	return func(a *Attribute) {
		Validator(func(s string) error {
			if varNameRegex.MatchString(s) {
				return nil
			}
			return errors.New("invalid variable name")
		})(a)
		// The check IS a regex, so record it as an introspectable pattern a
		// generator can emit directly (JSON Schema `pattern`). No runtime effect.
		Annotate("pattern", varNameRegex.String())(a)
	}
}

func IsCID() Option {
	return func(a *Attribute) {
		Validator(func(s string) error {
			_, err := cid.Parse(s)
			if err != nil {
				return fmt.Errorf("failed parsing `%s` with %w", s, err)
			}
			return nil
		})(a)
		// CID parsing is genuinely code, not data — but name the check so a
		// generator can emit it as a (custom) JSON Schema `format`. No runtime effect.
		Annotate("format", "cid")(a)
	}
}

func InSet[T string | int | float64](elms ...T) Option {
	return func(a *Attribute) {
		vals := make([]string, len(elms))
		for i, e := range elms {
			vals[i] = fmt.Sprint(e)
		}
		// A refusal names what would have been accepted, as strict mode's
		// unknown-key one does: a closed set the author cannot see is a search
		// through the schema.
		Validator(func(s T) error {
			for _, _s := range elms {
				if s == _s {
					return nil
				}
			}
			shown := fmt.Sprint(s)
			if str, ok := any(s).(string); ok {
				shown = strconv.Quote(str)
			}
			return fmt.Errorf("invalid value %s; valid values here: %s", shown, strings.Join(vals, ", "))
		})(a)
		// Record the permitted values so a code generator can emit an enum /
		// union type. Opaque to the engine (see Annotate) — no runtime effect.
		Annotate("enum", vals)(a)
		var zero T
		if _, ok := any(zero).(string); ok {
			Annotate("enumString", true)(a)
		}
	}
}

func IsFqdn() Option {
	return func(a *Attribute) {
		Validator(func(s string) error {
			if !isDomainName(s) {
				return errors.New("invalid fqdn")
			}
			return nil
		})(a)
		Annotate("format", "hostname")(a)
	}
}

// HttpMethods is the closed set of HTTP methods, canonical uppercase.
var HttpMethods = []string{"GET", "HEAD", "POST", "PUT", "DELETE", "CONNECT", "OPTIONS", "TRACE", "PATCH"}

// MinInt returns an Option that validates an Int attribute is >= min.
func MinInt(min int) Option {
	return func(a *Attribute) {
		Validator(func(v int) error {
			if v < min {
				return fmt.Errorf("value must be >= %d", min)
			}
			return nil
		})(a)
		Annotate("minimum", min)(a)
	}
}

// source: https://github.com/golang/go/blob/go1.20.5/src/net/dnsclient.go#L72-L75
func isDomainName(s string) bool {
	// The root domain name is valid. See golang.org/issue/45715.
	if s == "." {
		return true
	}

	// See RFC 1035, RFC 3696.
	// Presentation format has dots before every label except the first, and the
	// terminal empty label is optional here because we assume fully-qualified
	// (absolute) input. We must therefore reserve space for the first and last
	// labels' length octets in wire format, where they are necessary and the
	// maximum total length is 255.
	// So our _effective_ maximum is 253, but 254 is not rejected if the last
	// character is a dot.
	l := len(s)
	if l == 0 || l > 254 || l == 254 && s[l-1] != '.' {
		return false
	}

	last := byte('.')
	nonNumeric := false // true once we've seen a letter or hyphen
	partlen := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		default:
			return false
		case 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '_':
			nonNumeric = true
			partlen++
		case '0' <= c && c <= '9':
			// fine
			partlen++
		case c == '-':
			// Byte before dash cannot be dot.
			if last == '.' {
				return false
			}
			partlen++
			nonNumeric = true
		case c == '.':
			// Byte before dot cannot be dot, dash.
			if last == '.' || last == '-' {
				return false
			}
			if partlen > 63 || partlen == 0 {
				return false
			}
			partlen = 0
		}
		last = c
	}
	if last == '-' || partlen > 63 {
		return false
	}

	return nonNumeric
}
