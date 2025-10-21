package vm

import (
	"io"
	"time"
)

// Logger provides logging capabilities for VM function calls and executions
type Logger interface {
	// New creates a new call logger instance using the provided vm.Context
	// Writes to memory buffer, commits to file on close
	New(ctx Context) (io.WriteCloser, error)

	// Open returns a ReadCloser for reading logs at specific timestamp, fails if file doesn't exist
	Open(ctx Context, timestamp time.Time) (io.ReadCloser, error)

	// List returns all timestamps for a specific context within the specified range
	// If start is zero, uses first timestamp. If end is zero, uses last timestamp.
	List(ctx Context, start, end time.Time) ([]time.Time, error)

	// First returns the first (earliest) timestamp for a specific context
	First(ctx Context) (time.Time, error)

	// Last returns the last (most recent) timestamp for a specific context
	Last(ctx Context) (time.Time, error)

	// Close closes the logger and all open files
	Close() error
}
