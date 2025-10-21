package logger

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/taubyte/tau/core/vm"
)

// Open returns a ReadCloser for reading logs at specific timestamp, fails if file doesn't exist
func (lm *logManager) Open(ctx vm.Context, timestamp time.Time) (io.ReadCloser, error) {
	if timestamp.IsZero() {
		return nil, fmt.Errorf("timestamp must be provided and not zero")
	}

	// Check if timestamp is in the future
	now := time.Now()
	if timestamp.After(now) {
		return nil, fmt.Errorf("timestamp cannot be in the future")
	}

	logPath := lm.getLogPath(ctx)

	// Check if file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return nil, err
	}

	reader := &callReader{
		ctx:     ctx,
		manager: lm,
		logPath: logPath,
		buffer:  &bytes.Buffer{},
		pos:     0,
	}

	// Seek to the required timestamp and buffer content
	if err := reader.seekAndReadToBuffer(timestamp); err != nil {
		// Close the reader if seeking fails
		reader.Close()
		return nil, err
	}

	return reader, nil
}

// List returns all timestamps for a specific context
func (lm *logManager) List(ctx vm.Context, start time.Time, end time.Time) ([]time.Time, error) {
	logPath := lm.getLogPath(ctx)

	// Check if file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return []time.Time{}, nil
	}

	// Get managed file
	mf, err := lm.getOrCreateFile(logPath)
	if err != nil {
		return nil, err
	}

	// Lock for reading
	mf.mu.RLock()
	defer mf.mu.RUnlock()

	// Reset file position to start
	mf.file.Seek(0, 0)
	scanner := bufio.NewScanner(mf.file)
	timestamps := make([]time.Time, 0)
	lineCount := 0
	validTimestampCount := 0

	for scanner.Scan() {
		lineCount++
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ",", 2)
		if len(parts) != 2 {
			continue
		}

		nanos, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		ts := time.Unix(0, nanos)

		if !start.IsZero() && ts.Before(start) {
			continue
		}
		if !end.IsZero() && ts.After(end) {
			continue
		}

		validTimestampCount++
		timestamps = append(timestamps, ts)
	}

	return timestamps, nil
}

// First returns the first timestamp for a specific context
func (lm *logManager) First(ctx vm.Context) (time.Time, error) {
	logPath := lm.getLogPath(ctx)

	// Check if file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return time.Time{}, fmt.Errorf("log file does not exist")
	}

	// Get managed file
	mf, err := lm.getOrCreateFile(logPath)
	if err != nil {
		return time.Time{}, err
	}

	// Lock for reading
	mf.mu.RLock()
	defer mf.mu.RUnlock()

	// Reset file position to start
	mf.file.Seek(0, 0)
	scanner := bufio.NewScanner(mf.file)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ",", 2)
		if len(parts) != 2 {
			continue
		}

		nanos, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		// Return first valid timestamp
		return time.Unix(0, nanos), nil
	}

	return time.Time{}, fmt.Errorf("no timestamps found")
}

// Last returns the last timestamp for a specific context
func (lm *logManager) Last(ctx vm.Context) (time.Time, error) {
	logPath := lm.getLogPath(ctx)

	// Check if file exists
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return time.Time{}, fmt.Errorf("log file does not exist")
	}

	// Get managed file
	mf, err := lm.getOrCreateFile(logPath)
	if err != nil {
		return time.Time{}, err
	}

	// Lock for reading
	mf.mu.RLock()
	defer mf.mu.RUnlock()

	// Start from end and read backwards until we hit \n or pos==0
	pos, err := mf.file.Seek(0, 2) // Seek to end
	if err != nil {
		return time.Time{}, err
	}

	if pos == 0 {
		return time.Time{}, fmt.Errorf("no timestamps found")
	}

	// Skip trailing newline, if any
	// Move to last byte and check if it's a newline
	if pos > 0 {
		if _, err := mf.file.Seek(pos-1, 0); err == nil {
			b := make([]byte, 1)
			if n, _ := mf.file.Read(b); n == 1 && b[0] == '\n' {
				pos--
			}
		}
	}

	// Read backwards until we find a newline
	var buffer []byte
	for pos > 0 {
		pos--
		mf.file.Seek(pos, 0)

		b := make([]byte, 1)
		n, err := mf.file.Read(b)
		if err != nil || n == 0 {
			break
		}

		if b[0] == '\n' {
			break
		}

		buffer = append([]byte{b[0]}, buffer...)
	}

	// If we reached the start of file without hitting a newline,
	// the buffer contains the first line; that's fine.

	// Parse the last line
	line := string(buffer)
	if line == "" {
		return time.Time{}, fmt.Errorf("no timestamps found")
	}

	parts := strings.SplitN(line, ",", 2)
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("invalid log format")
	}

	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp")
	}

	return time.Unix(0, nanos), nil
}

// Read implements io.Reader for callReader
func (cr *callReader) Read(p []byte) (n int, err error) {
	if cr.closed {
		return 0, io.ErrClosedPipe
	}

	// Read from buffered content
	if cr.pos < cr.buffer.Len() {
		buf := cr.buffer.Bytes()
		n = copy(p, buf[cr.pos:])
		cr.pos += n
		return n, nil
	}

	// No more data in buffer
	return 0, io.EOF
}

// Close implements io.Closer for callReader
func (cr *callReader) Close() error {
	if cr.closed {
		return nil
	}

	cr.closed = true

	// Close file through manager
	return cr.manager.closeFile(cr.logPath)
}

// seekAndReadToBuffer seeks to the line with the specified timestamp and buffers content
func (cr *callReader) seekAndReadToBuffer(timestamp time.Time) error {
	// Get file through manager
	mf, err := cr.manager.getOrCreateFile(cr.logPath)
	if err != nil {
		return fmt.Errorf("failed to get file for reading: %w", err)
	}

	// Lock the file for seeking
	mf.mu.RLock()
	defer mf.mu.RUnlock()

	// Reset file position
	mf.file.Seek(0, 0)

	scanner := bufio.NewScanner(mf.file)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ",", 2)
		if len(parts) != 2 {
			continue
		}

		nanos, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		lineTimestamp := time.Unix(0, nanos)

		if lineTimestamp.Equal(timestamp) {
			// Found the timestamp, decode and buffer the base64 content
			decoded, err := base64.StdEncoding.DecodeString(parts[1])
			if err != nil {
				return fmt.Errorf("invalid base64 content for timestamp %v", timestamp)
			}
			cr.buffer.Reset()
			cr.buffer.Write(decoded)
			cr.pos = 0
			return nil
		}

		// Since timestamps are chronological, if current line timestamp is greater,
		// we've passed our target and can stop searching
		if lineTimestamp.After(timestamp) {
			return fmt.Errorf("timestamp %v not found", timestamp)
		}
	}

	return fmt.Errorf("timestamp %v not found", timestamp)
}
