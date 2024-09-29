package progress

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestReader_WithReader(t *testing.T) {
	data := "This is some test data."
	r := bytes.NewReader([]byte(data))
	interval := 10 * time.Millisecond
	pr, err := New(interval, WithReader(r, len(data)), Percentage())
	assert.NilError(t, err)

	done := make(chan struct{})
	go func() {
		for progress := range pr.ProgressChan() {
			fmt.Printf("Progress: %d%%\n", progress)
		}
		close(done)
	}()

	readBuf := make([]byte, len(data))
	totalRead := 0

	for {
		n, readErr := pr.Read(readBuf[totalRead:])
		if n > 0 {
			totalRead += n
		}
		if readErr == io.EOF {
			break
		}
		if n == 0 && readErr == nil {
			// No more data to read and no error; break the loop.
			break
		}
		assert.NilError(t, readErr)
	}

	assert.Equal(t, totalRead, len(data))
	assert.DeepEqual(t, readBuf, []byte(data))

	<-done
}

func TestReader_WithDifferentIntervals(t *testing.T) {
	data := "Some different interval test data."
	interval := 5 * time.Millisecond
	r := bytes.NewReader([]byte(data))
	pr, err := New(interval, WithReader(r, len(data)), Percentage())
	assert.NilError(t, err)

	done := make(chan struct{})
	go func() {
		for progress := range pr.ProgressChan() {
			fmt.Printf("Progress with interval: %d%%\n", progress)
		}
		close(done)
	}()

	readBuf := make([]byte, len(data))
	totalRead := 0

	for {
		n, readErr := pr.Read(readBuf[totalRead:])
		totalRead += n
		if readErr == io.EOF {
			break
		}
		assert.NilError(t, readErr)
	}

	assert.Equal(t, totalRead, len(data))
	assert.DeepEqual(t, readBuf, []byte(data))

	<-done
}

func TestReader_WithCanceledContext(t *testing.T) {
	data := "Data for cancellation test."
	ctx, cancel := context.WithCancel(context.Background())
	r := bytes.NewReader([]byte(data))
	pr, err := New(10*time.Millisecond, WithReader(r, len(data)), WithContext(ctx), Percentage())
	assert.NilError(t, err)

	done := make(chan struct{})
	go func() {
		for progress := range pr.ProgressChan() {
			fmt.Printf("Progress before cancel: %d%%\n", progress)
		}
		close(done)
	}()

	cancel()
	readBuf := make([]byte, len(data))
	_, err = pr.Read(readBuf)
	assert.ErrorContains(t, err, context.Canceled.Error())

	<-done
}

func TestReader_BufferHandling(t *testing.T) {
	_, err := New(10*time.Millisecond, WithBuffer(nil))
	assert.ErrorContains(t, err, "buffer cannot be nil")

	buf := []byte("Buffered test data.")
	pr, err := New(10*time.Millisecond, WithBuffer(buf), Percentage())
	assert.NilError(t, err)

	done := make(chan struct{})
	go func() {
		for progress := range pr.ProgressChan() {
			fmt.Printf("Buffered progress: %d%%\n", progress)
		}
		close(done)
	}()

	readBuf := make([]byte, len(buf))
	totalRead := 0

	for {
		n, readErr := pr.Read(readBuf[totalRead:])
		totalRead += n

		if readErr == io.EOF {
			break
		}
		assert.NilError(t, readErr)
	}

	assert.Equal(t, totalRead, len(buf))
	assert.DeepEqual(t, readBuf, buf)

	<-done
}

func TestReader_WithoutPercentage(t *testing.T) {
	data := "Test data without percentage."
	r := bytes.NewReader([]byte(data))
	pr, err := New(10*time.Millisecond, WithReader(r, len(data)))
	assert.NilError(t, err)

	done := make(chan struct{})
	go func() {
		for progress := range pr.ProgressChan() {
			fmt.Printf("Bytes read: %d\n", progress)
		}
		close(done)
	}()

	readBuf := make([]byte, len(data))
	totalRead := 0

	for {
		n, readErr := pr.Read(readBuf[totalRead:])
		totalRead += n
		if readErr == io.EOF {
			break
		}
		assert.NilError(t, readErr)
	}

	assert.Equal(t, totalRead, len(data))
	assert.DeepEqual(t, readBuf, []byte(data))

	<-done
}
