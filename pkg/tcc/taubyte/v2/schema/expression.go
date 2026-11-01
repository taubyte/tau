package schema

import (
	"errors"
	"fmt"

	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/parser"
	"github.com/taubyte/tau/pkg/tcc/engine"
)

// An expression field holds an expression and nothing else, so no `${...}`
// wrapper is needed. expr over CEL: a hand-written parser keeps the browser wasm
// small, and faults carry a line and column. Only syntax is checked; checking
// names needs the runtime's declared environment, via expr.Env.

// ExpressionError is a syntax fault inside an expression. Offset is from the
// value's first character, so it adds to the engine.LocatedError position of the
// value.
type ExpressionError struct {
	// Offset is the 0-based character offset into the authored value.
	Offset int
	// Detail is the parser's own complaint, without its position.
	Detail string
}

func (e *ExpressionError) Error() string {
	return fmt.Sprintf("invalid expression at character %d: %s", e.Offset+1, e.Detail)
}

// ExpressionAt is where an expression fault is, in the FILE: the position the engine
// recorded for the value, moved along by the offset within it. A consumer asks this
// rather than adding the two itself, so the arithmetic lives in one place.
func ExpressionAt(err error) (path string, line, column int, ok bool) {
	var located *engine.LocatedError
	var fault *ExpressionError
	if !errors.As(err, &located) || !errors.As(err, &fault) {
		return "", 0, 0, false
	}
	return located.File, located.Line, located.Column + fault.Offset, true
}

// IsExpression accepts an expression, and reports a fault at the character it is at, in
// the yaml the author wrote. An empty value is absent rather than wrong: whether the
// field has to be there at all is Required()'s question, not this one.
func IsExpression() engine.Option {
	return func(a *engine.Attribute) {
		Validator(func(s string) error {
			if s == "" {
				return nil
			}
			if _, err := parser.Parse(s); err != nil {
				return expressionFault(err)
			}
			return nil
		})(a)
		// Named so a generator can say what the field takes without knowing the language.
		engine.Annotate("format", "expression")(a)
	}
}

// expressionFault turns the parser's positioned error into one positioned in the value.
// The parser counts columns from 0 at the start of what it was given, which IS the value.
func expressionFault(err error) error {
	var located *file.Error
	if !errors.As(err, &located) {
		return fmt.Errorf("invalid expression: %w", err)
	}
	return &ExpressionError{Offset: max(located.Column, 0), Detail: located.Message}
}
