package vm

import "io"

// Config sets configuration of the VM service
type Config struct {
	MemoryLimitPages uint32 // should default to MemoryLimitPages
	Output           OutputType

	// Stdin, when set, is wired to the module's WASI standard input. It is used
	// by the WASI-stdio handler ABI to feed a serialized request to a
	// JavaScript (or any stdin/stdout) server bundle. When nil the module has
	// no stdin.
	Stdin io.Reader
}

type OutputType uint32

const (
	Pipe OutputType = iota
	Buffer
	Stdio
)
