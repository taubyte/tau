package engine

import "fmt"

// LocatedError is an error carrying the source position it was found at, so a
// consumer attributes it to a file/line/column by reading fields instead of
// parsing the message. Error() renders exactly the "file:line:col: msg" form
// these errors have always had, so message-matching callers are unaffected.
type LocatedError struct {
	File         string
	Line, Column int
	Err          error
}

func (e *LocatedError) Error() string {
	switch {
	case e.File == "":
		return e.Err.Error()
	case e.Line > 0 && e.Column > 0:
		return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Column, e.Err)
	case e.Line > 0:
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.File, e.Err)
}

func (e *LocatedError) Unwrap() error { return e.Err }

// located attaches a source position to err, or returns it unchanged when there
// is no file to attach.
func located(filePath string, line, column int, err error) error {
	if filePath == "" {
		return err
	}
	return &LocatedError{File: filePath, Line: line, Column: column, Err: err}
}
