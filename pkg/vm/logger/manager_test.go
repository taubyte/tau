package logger

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestManager_New_InvalidDirectory(t *testing.T) {
	// Create a file where directory is expected
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "not_a_dir")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	_, err := New(filePath)
	if err == nil {
		t.Fatal("expected error when creating logger with file path")
	}
}

func TestManager_New_NonExistentDirectory(t *testing.T) {
	tmp := t.TempDir()
	nonExistentDir := filepath.Join(tmp, "non_existent")

	// This should succeed as MkdirAll creates the directory
	_, err := New(nonExistentDir)
	if err != nil {
		t.Fatalf("New with non-existent directory should succeed: %v", err)
	}
}

func TestManager_GetLogPath(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	expectedPath := l.(*logManager).getLogPath(ctx)

	// Should contain the log directory and a deterministic filename
	if !filepath.IsAbs(expectedPath) {
		t.Fatalf("expected absolute path, got %s", expectedPath)
	}
	if !filepath.HasPrefix(expectedPath, logDir) {
		t.Fatalf("expected path to be in log directory, got %s", expectedPath)
	}
}

func TestManager_GetOrCreateFile_NewFile(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	logPath := l.(*logManager).getLogPath(ctx)

	mf, err := l.(*logManager).getOrCreateFile(logPath)
	if err != nil {
		t.Fatalf("getOrCreateFile failed: %v", err)
	}
	if mf == nil {
		t.Fatal("expected managedFile, got nil")
	}
	if mf.refCount != 1 {
		t.Fatalf("expected refCount 1, got %d", mf.refCount)
	}
}

func TestManager_GetOrCreateFile_ExistingFile(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	logPath := l.(*logManager).getLogPath(ctx)

	// Create file first time
	mf1, err := l.(*logManager).getOrCreateFile(logPath)
	if err != nil {
		t.Fatalf("getOrCreateFile 1 failed: %v", err)
	}

	// Get same file second time
	mf2, err := l.(*logManager).getOrCreateFile(logPath)
	if err != nil {
		t.Fatalf("getOrCreateFile 2 failed: %v", err)
	}

	if mf1 != mf2 {
		t.Fatal("expected same managedFile instance")
	}
	if mf2.refCount != 2 {
		t.Fatalf("expected refCount 2, got %d", mf2.refCount)
	}
}

func TestManager_CloseFile_DecrementRefCount(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	logPath := l.(*logManager).getLogPath(ctx)

	// Create file with refCount 2
	mf, err := l.(*logManager).getOrCreateFile(logPath)
	if err != nil {
		t.Fatalf("getOrCreateFile 1 failed: %v", err)
	}
	_, err = l.(*logManager).getOrCreateFile(logPath)
	if err != nil {
		t.Fatalf("getOrCreateFile 2 failed: %v", err)
	}

	if mf.refCount != 2 {
		t.Fatalf("expected refCount 2, got %d", mf.refCount)
	}

	// Close once - should not close file
	err = l.(*logManager).closeFile(logPath)
	if err != nil {
		t.Fatalf("closeFile 1 failed: %v", err)
	}

	// File should still exist in manager
	lm := l.(*logManager)
	lm.mu.RLock()
	_, exists := lm.openFiles[logPath]
	lm.mu.RUnlock()
	if !exists {
		t.Fatal("file should still exist after first close")
	}

	// Close second time - should close file
	err = l.(*logManager).closeFile(logPath)
	if err != nil {
		t.Fatalf("closeFile 2 failed: %v", err)
	}

	// File should be removed from manager
	lm.mu.RLock()
	_, exists = lm.openFiles[logPath]
	lm.mu.RUnlock()
	if exists {
		t.Fatal("file should be removed after second close")
	}
}

func TestManager_CloseFile_NonExistentFile(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	logPath := l.(*logManager).getLogPath(ctx)

	// Close non-existent file should not error
	err = l.(*logManager).closeFile(logPath)
	if err != nil {
		t.Fatalf("closeFile non-existent should not error: %v", err)
	}
}

func TestManager_Close_WithOpenFiles(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}

	ctx := newMockContext()

	// Create some log entries to have open files
	w1, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer 1 failed: %v", err)
	}
	w1.Write([]byte("data1"))
	w1.Close()

	// Create another context to get a different log file
	ctx2 := &mockContext{
		ctx:      context.Background(),
		project:  "proj-def",
		app:      "app-abc",
		resource: "res-456",
	}

	w2, err := l.New(ctx2)
	if err != nil {
		t.Fatalf("New writer 2 failed: %v", err)
	}
	w2.Write([]byte("data2"))
	w2.Close()

	// Close manager - should close all files
	err = l.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify no files are open
	lm := l.(*logManager)
	lm.mu.RLock()
	fileCount := len(lm.openFiles)
	lm.mu.RUnlock()
	if fileCount != 0 {
		t.Fatalf("expected 0 open files after Close, got %d", fileCount)
	}
}

func TestManager_Close_WithErrors(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}

	ctx := newMockContext()

	// Create a log entry
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}
	w.Write([]byte("data"))
	w.Close()

	// Manually close the file to simulate an error
	logPath := l.(*logManager).getLogPath(ctx)
	lm := l.(*logManager)
	lm.mu.Lock()
	if mf, exists := lm.openFiles[logPath]; exists {
		mf.file.Close() // Close the file manually
	}
	lm.mu.Unlock()

	// Close manager - should handle the error gracefully
	err = l.Close()
	// Close should not fail even if individual files fail to close
	if err != nil {
		t.Fatalf("Close should handle file close errors gracefully: %v", err)
	}
}

func TestManager_ConcurrentAccess(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	logPath := l.(*logManager).getLogPath(ctx)

	// Test concurrent access to getOrCreateFile
	done := make(chan error, 10)

	for i := 0; i < 10; i++ {
		go func() {
			_, err := l.(*logManager).getOrCreateFile(logPath)
			done <- err
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Fatalf("Concurrent getOrCreateFile failed: %v", err)
		}
	}

	// Verify refCount is correct
	lm := l.(*logManager)
	lm.mu.RLock()
	mf, exists := lm.openFiles[logPath]
	lm.mu.RUnlock()

	if !exists {
		t.Fatal("file should exist after concurrent access")
	}
	if mf.refCount != 10 {
		t.Fatalf("expected refCount 10, got %d", mf.refCount)
	}
}

func TestManager_FileOperations_ErrorHandling(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	// Test with a context that would create a very long path
	// This might cause issues on some systems
	longCtx := &mockContext{
		ctx:      context.Background(),
		project:  string(make([]byte, 1000)), // Very long project name
		app:      string(make([]byte, 1000)), // Very long app name
		resource: string(make([]byte, 1000)), // Very long resource name
	}

	// This should still work as the path generation should handle it
	logPath := l.(*logManager).getLogPath(longCtx)
	if logPath == "" {
		t.Fatal("expected non-empty log path")
	}
}

func TestManager_ReferenceCounting_EdgeCases(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	logPath := l.(*logManager).getLogPath(ctx)

	// Test multiple close operations
	_, err = l.(*logManager).getOrCreateFile(logPath)
	if err != nil {
		t.Fatalf("getOrCreateFile failed: %v", err)
	}

	// Close multiple times
	for i := 0; i < 5; i++ {
		err = l.(*logManager).closeFile(logPath)
		if err != nil {
			t.Fatalf("closeFile %d failed: %v", i, err)
		}
	}

	// File should be removed after first close (refCount was 1)
	lm := l.(*logManager)
	lm.mu.RLock()
	_, exists := lm.openFiles[logPath]
	lm.mu.RUnlock()
	if exists {
		t.Fatal("file should be removed after close")
	}

	// Additional closes should not error
	err = l.(*logManager).closeFile(logPath)
	if err != nil {
		t.Fatalf("closeFile on non-existent file should not error: %v", err)
	}
}
