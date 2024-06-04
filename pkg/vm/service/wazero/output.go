package service

import (
	"bytes"
	"errors"
	"io"
	"os"
)

type buffer struct {
	buffer *bytes.Buffer
	io.ReadWriteCloser
}

func (b *buffer) Close() error {
	b.buffer = nil
	return nil
}

func (b *buffer) Read(p []byte) (n int, err error) {
	if b.buffer != nil {
		return b.buffer.Read(p)
	}

	return 0, errors.New("buffer is closed")
}

func (b *buffer) Write(p []byte) (n int, err error) {
	if b.buffer != nil {
		b.buffer.Write(p)
	}

	return 0, errors.New("buffer is closed")
}

func newBuffer() io.ReadWriteCloser {
	return &buffer{
		buffer: bytes.NewBuffer(make([]byte, 0, MaxOutputCapacity)),
	}
}

type pipe struct {
	io.ReadCloser
	io.WriteCloser
	io.Closer
	filename string
}

func newPipe() (io.ReadWriteCloser, error) {

	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "vm")
	if err != nil {
		return nil, err
	}
	tmpFileName := tmpFile.Name()

	// Close the initial file descriptor as we'll open it separately for read and write
	tmpFile.Close()

	// Open the file twice: for reading and writing
	readFile, err := os.Open(tmpFileName)
	if err != nil {
		return nil, err
	}

	writeFile, err := os.OpenFile(tmpFileName, os.O_WRONLY, 0666)
	if err != nil {
		readFile.Close() // Make sure to close the readFile if opening writeFile fails
		return nil, err
	}

	return &pipe{
		ReadCloser:  readFile,
		WriteCloser: writeFile,
		filename:    tmpFileName,
	}, nil
}

func (p *pipe) Close() error {
	// Close the read file descriptor
	if err := p.ReadCloser.Close(); err != nil {
		p.WriteCloser.Close() // Attempt to close writeFile even if readFile.Close() fails
		return err
	}

	// Close the write file descriptor
	if err := p.WriteCloser.Close(); err != nil {
		return err
	}

	// Optionally, remove the temporary file to clean up
	os.Remove(p.filename)

	return nil
}

var MaxOutputCapacity = 10 * 1024
