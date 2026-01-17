package logger

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"github.com/taubyte/tau/core/vm"
)

// New creates a new call logger instance using the provided vm.Context
// Writes to memory buffer, commits to file on close
func (lm *logManager) New(ctx vm.Context) (io.WriteCloser, error) {
	return &callWriter{
		ctx:     ctx,
		manager: lm,
		buffer:  &bytes.Buffer{},
	}, nil
}

// Write implements io.Writer for callWriter
func (cw *callWriter) Write(p []byte) (n int, err error) {
	if cw.closed {
		return 0, io.ErrClosedPipe
	}

	return cw.buffer.Write(p)
}

// Close implements io.Closer for callWriter - commits buffer to file
func (cw *callWriter) Close() error {
	if cw.closed {
		return nil
	}

	cw.closed = true

	logPath := cw.manager.getLogPath(cw.ctx)

	if cw.buffer.Len() == 0 {
		// Still need to decrement reference count
		return cw.manager.closeFile(logPath)
	}

	// Get file through manager
	file, err := cw.manager.getOrCreateFile(logPath)
	if err != nil {
		return fmt.Errorf("failed to get file for writing: %w", err)
	}

	// Encode buffer to base64
	encoded := base64.StdEncoding.EncodeToString(cw.buffer.Bytes())

	// Lock the file for the entire atomic write operation
	file.mu.Lock()
	// Seek to end of file for append behavior
	file.file.Seek(0, 2)
	timestamp := time.Now()
	_, err = fmt.Fprintf(file.file, "%d,%s\n", timestamp.UnixNano(), encoded)
	file.mu.Unlock()

	// Decrement reference count while still holding the file lock
	// This ensures the entire operation is atomic
	closeErr := cw.manager.closeFile(logPath)

	if err != nil {
		return err
	}
	return closeErr
}
