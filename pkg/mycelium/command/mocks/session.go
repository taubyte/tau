package mocks

import (
	"io"

	"github.com/stretchr/testify/mock"
	"golang.org/x/crypto/ssh"
)

type RemoteSession struct {
	mock.Mock
}

func (m *RemoteSession) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *RemoteSession) CombinedOutput(cmd string) ([]byte, error) {
	args := m.Called(cmd)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *RemoteSession) Output(cmd string) ([]byte, error) {
	args := m.Called(cmd)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *RemoteSession) Run(cmd string) error {
	args := m.Called(cmd)
	return args.Error(0)
}

func (m *RemoteSession) Setenv(name string, value string) error {
	args := m.Called(name, value)
	return args.Error(0)
}

func (m *RemoteSession) Shell() error {
	args := m.Called()
	return args.Error(0)
}

func (m *RemoteSession) Signal(sig ssh.Signal) error {
	args := m.Called(sig)
	return args.Error(0)
}

func (m *RemoteSession) Start(cmd string) error {
	args := m.Called(cmd)
	return args.Error(0)
}

func (m *RemoteSession) StderrPipe() (io.Reader, error) {
	args := m.Called()
	if args.Get(0) != nil {
		return args.Get(0).(io.Reader), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *RemoteSession) StdinPipe() (io.WriteCloser, error) {
	args := m.Called()
	if args.Get(0) != nil {
		return args.Get(0).(io.WriteCloser), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *RemoteSession) StdoutPipe() (io.Reader, error) {
	args := m.Called()
	if args.Get(0) != nil {
		return args.Get(0).(io.Reader), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *RemoteSession) Wait() error {
	args := m.Called()
	return args.Error(0)
}
