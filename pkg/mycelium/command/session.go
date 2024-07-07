package command

import (
	"io"

	"golang.org/x/crypto/ssh"
)

type RemoteSession interface {
	Close() error
	CombinedOutput(cmd string) ([]byte, error)
	Output(cmd string) ([]byte, error)
	Run(cmd string) error
	Setenv(name string, value string) error
	Shell() error
	Signal(sig ssh.Signal) error
	Start(cmd string) error
	StderrPipe() (io.Reader, error)
	StdinPipe() (io.WriteCloser, error)
	StdoutPipe() (io.Reader, error)
	Wait() error
}
