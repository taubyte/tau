package logger

import (
	"io"
	"testing"
	"time"
)

func TestWriter_Write_ClosedWriter(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}
	w.Close()

	// Try to write to closed writer
	_, err = w.Write([]byte("test"))
	if err != io.ErrClosedPipe {
		t.Fatalf("expected ErrClosedPipe, got %v", err)
	}
}

func TestWriter_Close_AlreadyClosed(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}
	w.Close()

	// Close again should not error
	err = w.Close()
	if err != nil {
		t.Fatalf("Close on already closed writer should not error: %v", err)
	}
}

func TestWriter_Write_MultipleWrites(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}

	// Write multiple times
	_, err = w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write 1 failed: %v", err)
	}
	_, err = w.Write([]byte(" "))
	if err != nil {
		t.Fatalf("Write 2 failed: %v", err)
	}
	_, err = w.Write([]byte("world"))
	if err != nil {
		t.Fatalf("Write 3 failed: %v", err)
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the data was written correctly
	ts, err := l.Last(ctx)
	if err != nil {
		t.Fatalf("Last failed: %v", err)
	}

	r, err := l.Open(ctx, ts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("unexpected content: got %q want %q", string(got), "hello world")
	}
}

func TestWriter_Write_EmptyBuffer(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}

	// Don't write anything, just close
	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify no log entry was created
	tsList, err := l.List(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tsList) != 0 {
		t.Fatalf("expected 0 timestamps for empty buffer, got %d", len(tsList))
	}
}

func TestWriter_Write_LargeData(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}

	// Write large data
	largeData := make([]byte, 10000)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	_, err = w.Write(largeData)
	if err != nil {
		t.Fatalf("Write large data failed: %v", err)
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the data was written correctly
	ts, err := l.Last(ctx)
	if err != nil {
		t.Fatalf("Last failed: %v", err)
	}

	r, err := l.Open(ctx, ts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if len(got) != len(largeData) {
		t.Fatalf("unexpected data length: got %d want %d", len(got), len(largeData))
	}
	if !bytesEqual(got, largeData) {
		t.Fatalf("data mismatch")
	}
}

func TestWriter_Write_BinaryData(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}

	// Write binary data with null bytes
	binaryData := []byte{0x00, 0x01, 0x02, 0xFF, 0x00, 0x7F, 0x80}
	_, err = w.Write(binaryData)
	if err != nil {
		t.Fatalf("Write binary data failed: %v", err)
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the data was written correctly
	ts, err := l.Last(ctx)
	if err != nil {
		t.Fatalf("Last failed: %v", err)
	}

	r, err := l.Open(ctx, ts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if !bytesEqual(got, binaryData) {
		t.Fatalf("binary data mismatch")
	}
}

func TestWriter_Write_UnicodeData(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}

	// Write unicode data
	unicodeData := []byte("Hello 世界 🌍 测试")
	_, err = w.Write(unicodeData)
	if err != nil {
		t.Fatalf("Write unicode data failed: %v", err)
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the data was written correctly
	ts, err := l.Last(ctx)
	if err != nil {
		t.Fatalf("Last failed: %v", err)
	}

	r, err := l.Open(ctx, ts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(got) != string(unicodeData) {
		t.Fatalf("unicode data mismatch: got %q want %q", string(got), string(unicodeData))
	}
}

func TestWriter_Write_ZeroLength(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}

	// Write zero-length data
	n, err := w.Write([]byte{})
	if err != nil {
		t.Fatalf("Write zero-length data failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 bytes written, got %d", n)
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify no log entry was created
	tsList, err := l.List(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tsList) != 0 {
		t.Fatalf("expected 0 timestamps for zero-length write, got %d", len(tsList))
	}
}

func TestWriter_Write_ConcurrentWrites(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	// Create multiple writers concurrently
	done := make(chan error, 3)

	for i := 0; i < 3; i++ {
		go func(id int) {
			w, err := l.New(ctx)
			if err != nil {
				done <- err
				return
			}

			_, err = w.Write([]byte("writer " + string(rune('0'+id))))
			if err != nil {
				done <- err
				return
			}

			err = w.Close()
			done <- err
		}(i)
	}

	// Wait for all writers to complete
	for i := 0; i < 3; i++ {
		if err := <-done; err != nil {
			t.Fatalf("Writer %d failed: %v", i, err)
		}
	}

	// Verify all entries were created
	tsList, err := l.List(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tsList) != 3 {
		t.Fatalf("expected 3 timestamps, got %d", len(tsList))
	}
}

// Helper function to compare byte slices
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
