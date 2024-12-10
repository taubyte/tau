package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/mycelium/command/mocks"
	"gotest.tools/v3/assert"
)

func TestNewCommand(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()

	mockSession.On("Setenv", mock.Anything, mock.Anything).Return(nil).Once()
	_, err := New(ctx, mockSession, "ls", Env("fake", "var"))
	assert.NilError(t, err)

	mockSession.On("Setenv", mock.Anything, mock.Anything).Return(errors.New("failed to set env")).Once()
	_, err = New(ctx, mockSession, "ls", Env("fake", "var"))
	assert.Assert(t, err != nil)
}

func TestCommandRun(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()

	mockSession.On("Setenv", mock.Anything, mock.Anything).Return(nil).Once()
	cmd, _ := New(ctx, mockSession, "echo", Args("hello"))

	mockSession.On("Run", mock.Anything).Return(nil).Once()
	mockSession.On("Close").Return(nil).Twice() // Expect Close to be called twice
	err := cmd.Run()
	assert.NilError(t, err)

	mockSession.On("Run", mock.Anything).Return(errors.New("command failed")).Once()
	err = cmd.Run()
	assert.Assert(t, err != nil)
}

func TestCommandCombinedOutput(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "ls")

	output := []byte("success output")
	mockSession.On("CombinedOutput", mock.Anything).Return(output, nil).Once()
	mockSession.On("Close").Return(nil).Once() // Set expectation for Close method here
	data, err := cmd.CombinedOutput()
	assert.NilError(t, err)
	assert.DeepEqual(t, output, data)

	mockSession.On("CombinedOutput", mock.Anything).Return(nil, errors.New("command failed")).Once()
	mockSession.On("Close").Return(nil).Once() // Expect Close to be called on error as well
	_, err = cmd.CombinedOutput()
	assert.Assert(t, err != nil)
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
	assert.NilError(t, err)
	assert.Equal(t, stdout != nil, true)

	mockSession.On("StderrPipe").Return(io.NopCloser(nil), nil).Once()
	mockSession.On("Close").Return(nil).Once()
	stderr, err := cmd.StderrPipe()
	assert.NilError(t, err)
	assert.Equal(t, stderr != nil, true)

	mockSession.On("StdinPipe").Return(mockWriteCloser, nil).Once()
	mockSession.On("Close").Return(nil).Once()
	stdin, err := cmd.StdinPipe()
	assert.NilError(t, err)
	assert.Equal(t, stdin != nil, true)
}

func TestCommandSessionClosed(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "ls")

	cmd.sessClosed = true

	mockSession.On("Run", mock.Anything).Return(errors.New("session closed")).Once()
	err := cmd.Run()
	assert.Assert(t, err != nil)

	mockSession.On("Start", mock.Anything).Return(errors.New("session closed")).Once()
	err = cmd.Start()
	assert.Assert(t, err != nil)
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
	assert.Assert(t, err != nil)
	assert.ErrorContains(t, err, "command failed", "Error should propagate from Run")
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
		assert.ErrorContains(t, err, "context deadline exceeded", "Expected a timeout error")
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
	assert.NilError(t, err)

	err = cmd.Wait()
	assert.NilError(t, err)
}

func TestCommandPipeStdout(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "pipe-test")

	dummyReader := io.NopCloser(bytes.NewBufferString(""))

	mockSession.On("StdoutPipe").Return(dummyReader, nil).Once()
	stdout, err := cmd.StdoutPipe()
	assert.NilError(t, err)
	assert.Equal(t, stdout, dummyReader)
}

func TestCommandPipeStderr(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "pipe-test")

	dummyReader := io.NopCloser(bytes.NewBufferString(""))

	mockSession.On("StderrPipe").Return(dummyReader, nil).Once()
	stdout, err := cmd.StderrPipe()
	assert.NilError(t, err)
	assert.Equal(t, stdout, dummyReader)
}

func TestCommandSingleExecution(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()

	cmd, _ := New(ctx, mockSession, "echo", Args("hello"))

	mockSession.On("Run", mock.Anything).Return(nil).Once()
	mockSession.On("Close").Return(nil).Once()
	err := cmd.Run()
	assert.NilError(t, err)

	err = cmd.Run()
	assert.ErrorContains(t, err, "session closed")
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
	assert.ErrorContains(t, err, "context done")
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
	assert.ErrorContains(t, err, "context done")
}

func TestCommandStartAndWaitErrorHandling(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "long-process")

	mockSession.On("Start", mock.Anything).Return(errors.New("start failed")).Once()
	mockSession.On("Close").Return(nil).Once()

	err := cmd.Start()
	assert.Assert(t, err != nil)

	mockSession.On("Wait").Return(errors.New("wait failed")).Once()
	err = cmd.Wait()
	assert.Assert(t, err != nil)
}

func TestCommandStdinPipesErrorHandling(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "pipe-test")

	mockSession.On("StdinPipe").Return(nil, errors.New("stdin error")).Once()
	stdin, err := cmd.StdinPipe()
	assert.Equal(t, stdin, nil)
	assert.Assert(t, err != nil)
}

func TestCommandStdoutPipesErrorHandling(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "pipe-test")

	mockSession.On("StdoutPipe").Return(nil, errors.New("stdin error")).Once()
	stdin, err := cmd.StdoutPipe()
	assert.Equal(t, stdin, nil)
	assert.Assert(t, err != nil)
}

func TestCommandStderrPipesErrorHandling(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()
	cmd, _ := New(ctx, mockSession, "pipe-test")

	mockSession.On("StderrPipe").Return(nil, errors.New("stdin error")).Once()
	stdin, err := cmd.StderrPipe()
	assert.Equal(t, stdin, nil)
	assert.Assert(t, err != nil)
}

func TestCommand_ArgumentsHandling(t *testing.T) {
	mockSession := new(mocks.RemoteSession)
	ctx := context.Background()

	args := []string{"a rg1", "arg2"}
	cmd, _ := New(ctx, mockSession, "echo", Args(args...))

	assert.Equal(t, `echo "a rg1" "arg2"`, cmd.String(), "Arguments should be correctly formatted and stored")
}
