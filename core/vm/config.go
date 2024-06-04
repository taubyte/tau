package vm

// Config sets configuration of the VM service
type Config struct {
	MemoryLimitPages uint32 // should default to MemoryLimitPages
	Output           OutputType
}

type OutputType uint32

const (
	Pipe OutputType = iota
	Buffer
)
