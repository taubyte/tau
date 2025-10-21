package logger

import (
	"bytes"
	"os"
	"sync"

	"github.com/taubyte/tau/core/vm"
)

// managedFile represents a file with reference counting for thread-safe management
type managedFile struct {
	file     *os.File
	mu       sync.RWMutex
	refCount int
}

// logManager manages log files and provides the main logging API
type logManager struct {
	logDir    string
	mu        sync.RWMutex
	openFiles map[string]*managedFile
}

// callReader implements io.ReadCloser for reading logs at specific timestamps
type callReader struct {
	ctx     vm.Context
	manager *logManager
	logPath string
	buffer  *bytes.Buffer
	pos     int
	closed  bool
}

// callWriter implements io.WriteCloser for buffered logging
type callWriter struct {
	ctx     vm.Context
	manager *logManager
	buffer  *bytes.Buffer
	closed  bool
}
