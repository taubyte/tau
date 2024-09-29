package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/mycelium/command/mocks"
)

func TestNewCommand(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()

	mockSession.On("Setenv", mock.Anything, mock.Anything).Return(nil).Once()
	_, err := New(ctx, mockSession, "ls", Env("fake", "var"))
	assert.Nil(t, err, "Expected no error")

	mockSession.On("Setenv", mock.Anything, mock.Anything).Return(errors.New("failed to set env")).Once()
	_, err = New(ctx, mockSession, "ls", Env("fake", "var"))
	assert.Error(t, err, "Expected an error from setting environment variable")
}

func TestCommandRun(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()

	mockSession.On("Setenv", mock.Anything, mock.Anything).Return(nil).Once()
	cmd, _ := New(ctx, mockSession, "echo", Args("hello"))

	mockSession.On("Run", mock.Anything).Return(nil).Once()
	mockSession.On("Close").Return(nil).Twice() // Expect Close to be called twice
	err := cmd.Run()
	assert.Nil(t, err, "Expected no error when running command")

	mockSession.On("Run", mock.Anything).Return(errors.New("command failed")).Once()
	err = cmd.Run()
	assert.Error(t, err, "Expected an error when command fails")
}

func TestCommandCombinedOutput(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "ls")

	output := []byte("success output")
	mockSession.On("CombinedOutput", mock.Anything).Return(output, nil).Once()
	mockSession.On("Close").Return(nil).Once() // Set expectation for Close method here
	data, err := cmd.CombinedOutput()
	assert.Nil(t, err, "Expected no error from CombinedOutput")
	assert.Equal(t, output, data, "Expected output to match mock output")

	mockSession.On("CombinedOutput", mock.Anything).Return(nil, errors.New("command failed")).Once()
	mockSession.On("Close").Return(nil).Once() // Expect Close to be called on error as well
	_, err = cmd.CombinedOutput()
	assert.Error(t, err, "Expected an error from CombinedOutput")
}

func TestCommandPipes(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "ls")

	mockWriteCloser := new(mocks.WriteCloser)
	mockWriteCloser.On("Write", mock.Anything).Return(0, nil)
	mockWriteCloser.On("Close").Return(nil)

	mockSession.On("StdoutPipe").Return(io.NopCloser(nil), nil).Once()
	mockSession.On("Close").Return(nil).Once()
	stdout, err := cmd.StdoutPipe()
	assert.Nil(t, err, "Expected no error from StdoutPipe")
	assert.NotNil(t, stdout, "Expected stdout not to be nil")

	mockSession.On("StderrPipe").Return(io.NopCloser(nil), nil).Once()
	mockSession.On("Close").Return(nil).Once()
	stderr, err := cmd.StderrPipe()
	assert.Nil(t, err, "Expected no error from StderrPipe")
	assert.NotNil(t, stderr, "Expected stderr not to be nil")

	mockSession.On("StdinPipe").Return(mockWriteCloser, nil).Once()
	mockSession.On("Close").Return(nil).Once()
	stdin, err := cmd.StdinPipe()
	assert.Nil(t, err, "Expected no error from StdinPipe")
	assert.NotNil(t, stdin, "Expected stdin not to be nil")
}

func TestCommandSessionClosed(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "ls")

	cmd.sessClosed = true

	mockSession.On("Run", mock.Anything).Return(errors.New("session closed")).Once()
	err := cmd.Run()
	assert.Error(t, err, "Expected error when running with closed session")

	mockSession.On("Start", mock.Anything).Return(errors.New("session closed")).Once()
	err = cmd.Start()
	assert.Error(t, err, "Expected error when starting with closed session")
}

func TestCommandsessionWrap(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "ls")

	mockSession.On("Run", mock.Anything).Return(errors.New("command failed")).Once()
	mockSession.On("Close").Return(nil).Once()
	err := cmd.sessionWrap(func() error {
		return cmd.sess.Run("failing command")
	})
	assert.Error(t, err, "Expected an error from sessionWrap")
	assert.Contains(t, err.Error(), "command failed", "Error should propagate from Run")
}
func TestCommandContextTimeout(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	cmd, err := New(ctx, mockSession, "sleep")
	if err != nil {
		t.Fatalf("Failed to create command: %v", err)
	}

	mockSession.On("Run", mock.Anything).Return(errors.New("context deadline exceeded")).Once()
	mockSession.On("Close").Return(nil).Once()

	err = cmd.Run()
	if err == nil {
		t.Error("Expected an error due to context timeout, but got nil")
	} else {
		assert.Contains(t, err.Error(), "context deadline exceeded", "Expected a timeout error")
	}
}

func TestCommandStartAndWait(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()

	mockSession.On("Start", mock.Anything).Return(nil).Once()
	mockSession.On("Wait").Return(nil).Once()
	mockSession.On("Close").Return(nil).Twice() // Called once after Start and once after Wait

	cmd, _ := New(ctx, mockSession, "long-running-process")
	err := cmd.Start()
	assert.Nil(t, err, "Failed to start the command")

	err = cmd.Wait()
	assert.Nil(t, err, "Failed to wait for the command to finish")
}

func TestCommandPipeStdout(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "pipe-test")

	dummyReader := io.NopCloser(bytes.NewBufferString(""))

	mockSession.On("StdoutPipe").Return(dummyReader, nil).Once()
	stdout, err := cmd.StdoutPipe()
	assert.Nil(t, err)
	assert.Equal(t, stdout, dummyReader)
}

func TestCommandPipeStderr(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "pipe-test")

	dummyReader := io.NopCloser(bytes.NewBufferString(""))

	mockSession.On("StderrPipe").Return(dummyReader, nil).Once()
	stdout, err := cmd.StderrPipe()
	assert.Nil(t, err)
	assert.Equal(t, stdout, dummyReader)
}

func TestCommandSingleExecution(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()

	cmd, _ := New(ctx, mockSession, "echo", Args("hello"))

	mockSession.On("Run", mock.Anything).Return(nil).Once()
	mockSession.On("Close").Return(nil).Once()
	err := cmd.Run()
	assert.Nil(t, err, "Expected no error on first run")

	err = cmd.Run()
	assert.Error(t, err, "Expected error on second run")
	assert.Equal(t, "session closed", err.Error(), "Error should indicate command was already executed")
}

func TestCommandsessionWrapContextCancellation(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx, cancel := context.WithCancel(context.Background())
	cmd, _ := New(ctx, mockSession, "ls")

	mockSession.On("Run", mock.Anything).Return(nil)
	mockSession.On("Close").Return(nil).Once()
	mockSession.On("Signal", mock.Anything).Return(nil).Once()

	go func() {
		cancel() // Cancel the context to trigger the cancellation path in sessionWrap
	}()

	err := cmd.sessionWrap(func() error {
		time.Sleep(100 * time.Millisecond) // Simulate some work
		return nil
	})
	assert.Error(t, err, "Expected context cancellation error")
	assert.Contains(t, err.Error(), "context done", "Expected error to mention context being done")
}

func TestCommandsessionWrapContextCancellationFailedInt(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx, cancel := context.WithCancel(context.Background())
	cmd, _ := New(ctx, mockSession, "ls")

	mockSession.On("Run", mock.Anything).Return(nil)
	mockSession.On("Close").Return(nil).Once()
	mockSession.On("Signal", ssh.SIGINT).Return(errors.ErrUnsupported).Once()
	mockSession.On("Signal", ssh.SIGKILL).Return(nil).Once()

	go func() {
		cancel() // Cancel the context to trigger the cancellation path in sessionWrap
	}()

	err := cmd.sessionWrap(func() error {
		time.Sleep(100 * time.Millisecond) // Simulate some work
		return nil
	})
	assert.Error(t, err, "Expected context cancellation error")
	assert.Contains(t, err.Error(), "context done", "Expected error to mention context being done")
}

func TestCommandStartAndWaitErrorHandling(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "long-process")

	mockSession.On("Start", mock.Anything).Return(errors.New("start failed")).Once()
	mockSession.On("Close").Return(nil).Once()

	err := cmd.Start()
	assert.Error(t, err, "Expected error on start failure")

	mockSession.On("Wait").Return(errors.New("wait failed")).Once()
	err = cmd.Wait()
	assert.Error(t, err, "Expected error on wait failure")
}

func TestCommandStdinPipesErrorHandling(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "pipe-test")

	mockSession.On("StdinPipe").Return(nil, errors.New("stdin error")).Once()
	stdin, err := cmd.StdinPipe()
	assert.Nil(t, stdin, "stdin should be nil on error")
	assert.Error(t, err, "Expected stdin pipe error")
}

func TestCommandStdoutPipesErrorHandling(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "pipe-test")

	mockSession.On("StdoutPipe").Return(nil, errors.New("stdin error")).Once()
	stdin, err := cmd.StdoutPipe()
	assert.Nil(t, stdin, "stdout should be nil on error")
	assert.Error(t, err, "Expected stdin pipe error")

}

func TestCommandStderrPipesErrorHandling(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "pipe-test")

	mockSession.On("StderrPipe").Return(nil, errors.New("stdin error")).Once()
	stdin, err := cmd.StderrPipe()
	assert.Nil(t, stdin, "stderr should be nil on error")
	assert.Error(t, err, "Expected stdin pipe error")

}

func TestCommand_ArgumentsHandling(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()

	args := []string{"a rg1", "arg2"}
	cmd, _ := New(ctx, mockSession, "echo", Args(args...))

	assert.Equal(t, `echo "a rg1" "arg2"`, cmd.String(), "Arguments should be correctly formatted and stored")
}
