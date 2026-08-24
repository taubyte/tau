package schema

import (
	"errors"
	"fmt"

	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/parser"
	"github.com/taubyte/tau/pkg/tcc/engine"
)

// An EXPRESSION FIELD holds an expression, and nothing else.
//
// There is no `${...}` wrapper, and that is a decision rather than an omission. A marker
// is what a language needs when one slot takes EITHER a literal or something computed —
// Google Cloud Workflows carries `${}` for exactly that reason. No field here is
// ambiguous: `when` and `predicate` are always predicates, `input` and `result` are
// always selectors, and a field that names a resource is always a name. With nothing to
// tell apart, a wrapper is punctuation the author has to remember and a whole class of
// faults (opened and never closed, text outside the braces) that then has to be checked
// for.
//
// WHY expr RATHER THAN CEL. Both are non-Turing-complete embedded expression languages
// with Go implementations, and either would do. expr's parser is hand-written rather than
// ANTLR-generated, which is most of a megabyte of compressed wasm — tcc is compiled to
// wasm so the explorer can compile config in the browser, and that is a download. More
// importantly its faults arrive as a *file.Error carrying a line and a column, so the
// position below is READ rather than scraped out of a message whose format is not a
// contract.
//
// What that costs, said plainly: expr has one implementation, in Go, where CEL already
// has Java, C++, Rust and TS. It is a smaller cost than it sounds — the module declares
// no third-party dependency at all, the syntax half this package uses is about 2,700
// lines (parser, lexer, ast, file) and a whole evaluator is about 9,400, over a
// conventional precedence-climbing grammar with no macro layer to reimplement. So a
// second-language consumer that only VALIDATES config ports a weekend of code, and one
// that wants to RUN workflows would be calling the Go runtime anyway. Revisit this if a
// non-Go evaluator ever becomes real; nothing else about it is a lock.
//
// This is what keeps §5 satisfied. "Do not smuggle structure into strings" is about
// giving up per-field validation and per-field error locations; an expression parsed at
// compile gives up neither. The accepted precedent is `zones.records`, allowed because a
// zone file is a real interchange format rather than an invented grammar — an expression
// language with a parser qualifies on the same ground.
//
// WHAT IS CHECKED HERE AND WHAT IS NOT. Syntax, and only syntax. Deciding that
// `order.lines` exists and is a list needs a declared environment — what a step can see,
// and of what type — and that environment is the runtime's contract, not something this
// package can invent. When it exists, `expr.Compile(src, expr.Env(...))` is the same call
// with the declarations filled in, and it returns the same positioned error.

// ExpressionError is a syntax fault inside an expression, carrying where in the value it
// was found so a caller can point at the character rather than at the field.
//
// The engine already wraps a validator's error in an engine.LocatedError holding the
// file, line and column the VALUE starts at, and the parser reports a position within the
// expression. Neither is the answer alone, and printing both reads as two coordinate
// systems. Offset is measured from the first character of the value, so the two compose
// by addition and nothing has to parse a message.
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
