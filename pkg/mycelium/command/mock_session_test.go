package command

import (
	"io"

	"github.com/stretchr/testify/mock"
	"golang.org/x/crypto/ssh"
)

// mockWriteCloser implements io.WriteCloser for testing purposes.
type mockWriteCloser struct {
	mock.Mock
}

func (mwc *mockWriteCloser) Write(p []byte) (n int, err error) {
	args := mwc.Called(p)
	return args.Int(0), args.Error(1)
}

func (mwc *mockWriteCloser) Close() error {
	args := mwc.Called()
	return args.Error(0)
}

type MockRemoteSession struct {
	mock.Mock
}

func (m *MockRemoteSession) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockRemoteSession) CombinedOutput(cmd string) ([]byte, error) {
	args := m.Called(cmd)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockRemoteSession) Output(cmd string) ([]byte, error) {
	args := m.Called(cmd)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockRemoteSession) Run(cmd string) error {
	args := m.Called(cmd)
	return args.Error(0)
}

func (m *MockRemoteSession) Setenv(name string, value string) error {
	args := m.Called(name, value)
	return args.Error(0)
}

func (m *MockRemoteSession) Shell() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockRemoteSession) Signal(sig ssh.Signal) error {
	args := m.Called(sig)
	return args.Error(0)
}

func (m *MockRemoteSession) Start(cmd string) error {
	args := m.Called(cmd)
	return args.Error(0)
}

func (m *MockRemoteSession) StderrPipe() (io.Reader, error) {
	args := m.Called()
	if args.Get(0) != nil {
		return args.Get(0).(io.Reader), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockRemoteSession) StdinPipe() (io.WriteCloser, error) {
	args := m.Called()
	if args.Get(0) != nil {
		return args.Get(0).(io.WriteCloser), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockRemoteSession) StdoutPipe() (io.Reader, error) {
	args := m.Called()
	if args.Get(0) != nil {
		return args.Get(0).(io.Reader), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockRemoteSession) Wait() error {
	args := m.Called()
	return args.Error(0)
}
