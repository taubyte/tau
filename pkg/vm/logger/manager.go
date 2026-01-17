package logger

import (
	"os"
	"path/filepath"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/utils/id"
)

// New creates a new log manager with the specified log directory
func New(logDir string) (vm.Logger, error) {
	// Ensure the log directory exists
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, err
	}

	return &logManager{
		logDir:    logDir,
		openFiles: make(map[string]*managedFile),
	}, nil
}

func (lm *logManager) getLogPath(ctx vm.Context) string {
	return filepath.Join(lm.logDir, "vm-debug-"+id.GenerateDeterministic(ctx.Project(), ctx.Application(), ctx.Resource())+".log")
}

// getOrCreateFile gets an existing open file or creates a new one
// Always opens files in RW mode since they are managed files
func (lm *logManager) getOrCreateFile(logPath string) (*managedFile, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if mf, exists := lm.openFiles[logPath]; exists {
		mf.refCount++
		return mf, nil
	}

	// Always open in RW mode for managed files
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	mf := &managedFile{
		file:     file,
		refCount: 1,
	}

	lm.openFiles[logPath] = mf
	return mf, nil
}

// closeFile decrements reference count and closes file if no more references
func (lm *logManager) closeFile(logPath string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	mf, exists := lm.openFiles[logPath]
	if !exists {
		return nil
	}

	mf.refCount--

	if mf.refCount <= 0 {
		delete(lm.openFiles, logPath)
		mf.file.Sync()
		return mf.file.Close()
	}

	return nil
}

// Close closes the log manager
func (lm *logManager) Close() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	var lastErr error
	for logPath, mf := range lm.openFiles {
		if err := mf.file.Close(); err != nil {
			lastErr = err
		}
		delete(lm.openFiles, logPath)
	}

	return lastErr
}
