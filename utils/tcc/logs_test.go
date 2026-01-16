package tccUtils

import (
	"errors"
	"io"
	"testing"

	"gotest.tools/v3/assert"
)

func TestLogs_NilError(t *testing.T) {
	reader := Logs(nil)
	assert.Assert(t, reader != nil)

	// Should return empty bytes (EOF on read)
	data := make([]byte, 100)
	n, err := reader.Read(data)
	assert.Equal(t, err.Error(), "EOF")
	assert.Equal(t, n, 0)
}

func TestLogs_WithError(t *testing.T) {
	testError := errors.New("test error message")
	reader := Logs(testError)
	assert.Assert(t, reader != nil)

	// Read the error message
	data := make([]byte, 100)
	n, err := reader.Read(data)
	assert.NilError(t, err)
	assert.Equal(t, string(data[:n]), "test error message\n")
}

func TestLogs_ReadSeeker_Seek(t *testing.T) {
	testError := errors.New("test error message")
	reader := Logs(testError)
	assert.Assert(t, reader != nil)

	seeker, ok := reader.(io.Seeker)
	assert.Assert(t, ok, "Logs should return io.Seeker")

	// Seek to start
	pos, err := seeker.Seek(0, io.SeekStart)
	assert.NilError(t, err)
	assert.Equal(t, pos, int64(0))

	// Read some data
	data := make([]byte, 5)
	n, err := reader.Read(data)
	assert.NilError(t, err)
	assert.Equal(t, n, 5)
	assert.Equal(t, string(data), "test ")

	// Seek back to start
	pos, err = seeker.Seek(0, io.SeekStart)
	assert.NilError(t, err)
	assert.Equal(t, pos, int64(0))

	// Read again from start
	data = make([]byte, 5)
	n, err = reader.Read(data)
	assert.NilError(t, err)
	assert.Equal(t, n, 5)
	assert.Equal(t, string(data), "test ")
}

func TestLogs_ReadSeeker_SeekEnd(t *testing.T) {
	testError := errors.New("test error")
	reader := Logs(testError)
	seeker := reader.(io.Seeker)

	// Seek to end
	pos, err := seeker.Seek(0, io.SeekEnd)
	assert.NilError(t, err)
	// Should be at the end (length of "test error\n")
	assert.Equal(t, pos, int64(11))

	// Reading from end should return EOF
	data := make([]byte, 10)
	n, err := reader.Read(data)
	assert.Equal(t, err, io.EOF)
	assert.Equal(t, n, 0)
}

func TestLogs_ReadSeeker_SeekCurrent(t *testing.T) {
	testError := errors.New("test error message")
	reader := Logs(testError)
	seeker := reader.(io.Seeker)

	// Read some bytes
	data := make([]byte, 5)
	n, err := reader.Read(data)
	assert.NilError(t, err)
	assert.Equal(t, n, 5)

	// Seek relative to current position
	pos, err := seeker.Seek(-2, io.SeekCurrent)
	assert.NilError(t, err)
	assert.Equal(t, pos, int64(3))

	// Read from new position
	data = make([]byte, 5)
	n, err = reader.Read(data)
	assert.NilError(t, err)
	assert.Equal(t, n, 5)
	assert.Equal(t, string(data), "t err")
}

func TestLogs_EmptyError(t *testing.T) {
	testError := errors.New("")
	reader := Logs(testError)
	assert.Assert(t, reader != nil)

	// Should return just newline
	data := make([]byte, 10)
	n, err := reader.Read(data)
	assert.NilError(t, err)
	assert.Equal(t, n, 1)
	assert.Equal(t, string(data[:n]), "\n")
}
