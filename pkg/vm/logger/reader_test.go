package logger

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReader_Open_ZeroTimestamp(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	_, err = l.Open(ctx, time.Time{})
	if err == nil {
		t.Fatal("expected error for zero timestamp")
	}
}

func TestReader_Open_FutureTimestamp(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	futureTime := time.Now().Add(1 * time.Hour)
	_, err = l.Open(ctx, futureTime)
	if err == nil {
		t.Fatal("expected error for future timestamp")
	}
}

func TestReader_Open_NonExistentFile(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	_, err = l.Open(ctx, time.Now())
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestReader_Read_ClosedReader(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	// Create a log entry first
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}
	w.Write([]byte("test data"))
	w.Close()

	ts, err := l.Last(ctx)
	if err != nil {
		t.Fatalf("Last failed: %v", err)
	}

	r, err := l.Open(ctx, ts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	r.Close()

	// Try to read from closed reader
	buf := make([]byte, 10)
	_, err = r.Read(buf)
	if err != io.ErrClosedPipe {
		t.Fatalf("expected ErrClosedPipe, got %v", err)
	}
}

func TestReader_Close_AlreadyClosed(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	// Create a log entry first
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}
	w.Write([]byte("test data"))
	w.Close()

	ts, err := l.Last(ctx)
	if err != nil {
		t.Fatalf("Last failed: %v", err)
	}

	r, err := l.Open(ctx, ts)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	r.Close()

	// Close again should not error
	err = r.Close()
	if err != nil {
		t.Fatalf("Close on already closed reader should not error: %v", err)
	}
}

func TestReader_List_EmptyFile(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	tsList, err := l.List(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tsList) != 0 {
		t.Fatalf("expected 0 timestamps, got %d", len(tsList))
	}
}

func TestReader_List_WithTimeRange(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	// Create multiple entries with time separation
	startTime := time.Now()

	w1, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer 1 failed: %v", err)
	}
	w1.Write([]byte("first"))
	w1.Close()

	time.Sleep(2 * time.Millisecond)

	w2, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer 2 failed: %v", err)
	}
	w2.Write([]byte("second"))
	w2.Close()

	time.Sleep(2 * time.Millisecond)
	endTime := time.Now()

	time.Sleep(2 * time.Millisecond)

	w3, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer 3 failed: %v", err)
	}
	w3.Write([]byte("third"))
	w3.Close()

	// Test with time range
	tsList, err := l.List(ctx, startTime, endTime)
	if err != nil {
		t.Fatalf("List with range failed: %v", err)
	}
	if len(tsList) != 2 {
		t.Fatalf("expected 2 timestamps in range, got %d", len(tsList))
	}
}

func TestReader_First_EmptyFile(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	_, err = l.First(ctx)
	if err == nil {
		t.Fatal("expected error for empty file")
	}
}

func TestReader_First_NonExistentFile(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	_, err = l.First(ctx)
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestReader_First_WithData(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	// Create first entry
	w1, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer 1 failed: %v", err)
	}
	w1.Write([]byte("first"))
	w1.Close()

	time.Sleep(1 * time.Millisecond)

	// Create second entry
	w2, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer 2 failed: %v", err)
	}
	w2.Write([]byte("second"))
	w2.Close()

	first, err := l.First(ctx)
	if err != nil {
		t.Fatalf("First failed: %v", err)
	}

	last, err := l.Last(ctx)
	if err != nil {
		t.Fatalf("Last failed: %v", err)
	}

	if !first.Before(last) && !first.Equal(last) {
		t.Fatalf("first timestamp should be before or equal to last")
	}
}

func TestReader_Last_EmptyFile(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	_, err = l.Last(ctx)
	if err == nil {
		t.Fatal("expected error for empty file")
	}
}

func TestReader_Last_NonExistentFile(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()
	_, err = l.Last(ctx)
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestReader_SeekAndReadToBuffer_InvalidBase64(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	// Manually create a log file with invalid base64
	logPath := l.(*logManager).getLogPath(ctx)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer file.Close()

	// Write invalid base64 content
	_, err = file.WriteString("invalid_base64_content\n")
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}

	// Try to open the log
	_, err = l.Open(ctx, time.Now())
	if err == nil {
		t.Fatal("expected error for invalid base64 content")
	}
}

func TestReader_SeekAndReadToBuffer_TimestampNotFound(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	// Create a log entry
	w, err := l.New(ctx)
	if err != nil {
		t.Fatalf("New writer failed: %v", err)
	}
	w.Write([]byte("test data"))
	w.Close()

	// Try to open with a timestamp that doesn't exist
	nonExistentTime := time.Now().Add(-1 * time.Hour)
	_, err = l.Open(ctx, nonExistentTime)
	if err == nil {
		t.Fatal("expected error for non-existent timestamp")
	}
}

func TestReader_List_InvalidLines(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	// Manually create a log file with invalid lines
	logPath := l.(*logManager).getLogPath(ctx)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer file.Close()

	// Write various invalid lines
	_, err = file.WriteString("invalid_line\n")
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}
	_, err = file.WriteString("123,valid_base64\n")
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}
	_, err = file.WriteString("invalid_timestamp,data\n")
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}

	// List should still work and filter out invalid lines
	tsList, err := l.List(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tsList) != 1 {
		t.Fatalf("expected 1 valid timestamp, got %d", len(tsList))
	}
}

func TestReader_First_NoValidTimestamps(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	// Manually create a log file with only invalid timestamps
	logPath := l.(*logManager).getLogPath(ctx)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer file.Close()

	// Write only invalid lines
	_, err = file.WriteString("invalid_timestamp,data\n")
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}
	_, err = file.WriteString("not_a_number,data\n")
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}

	// First should return error
	_, err = l.First(ctx)
	if err == nil {
		t.Fatal("expected error for no valid timestamps")
	}
	if !strings.Contains(err.Error(), "no timestamps found") {
		t.Fatalf("expected 'no timestamps found' error, got: %v", err)
	}
}

func TestReader_Last_NoValidTimestamps(t *testing.T) {
	logDir := t.TempDir()
	l, err := New(logDir)
	if err != nil {
		t.Fatalf("New logger failed: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	ctx := newMockContext()

	// Manually create a log file with only invalid timestamps
	logPath := l.(*logManager).getLogPath(ctx)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer file.Close()

	// Write only invalid lines
	_, err = file.WriteString("invalid_timestamp,data\n")
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}
	_, err = file.WriteString("not_a_number,data\n")
	if err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}

	// Last should return error
	_, err = l.Last(ctx)
	if err == nil {
		t.Fatal("expected error for no valid timestamps")
	}
	if !strings.Contains(err.Error(), "no timestamps found") && !strings.Contains(err.Error(), "invalid timestamp") {
		t.Fatalf("expected 'no timestamps found' or 'invalid timestamp' error, got: %v", err)
	}
}
