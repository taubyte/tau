package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

type Command struct {
	ctx        context.Context
	startDone  chan struct{}
	sess       RemoteSession
	sessClosed bool
	name       string
	args       []string
	env        map[string]string
}

// Create a command executor
// Note: this will close the session after execution
func New(ctx context.Context, sess RemoteSession, name string, options ...Option) (*Command, error) {
	c := &Command{
		ctx:       ctx,
		name:      name,
		sess:      sess,
		env:       make(map[string]string),
		startDone: make(chan struct{}),
	}

	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, fmt.Errorf("applying option: %w", err)
		}
	}

	// make sure session is setup and login shell (optional) requested first
	for k, v := range c.env {
		err := c.sess.Setenv(k, v)
		if err != nil {
			return nil, fmt.Errorf("setting environment variable %s: %w", k, err)
		}
	}

	// free env
	c.env = nil

	return c, nil
}

func (c *Command) Run() error {
	return c.sessionWrap(func() error {
		if err := c.sess.Run(c.String()); err != nil {
			return fmt.Errorf("running command %s: %w", c.String(), err)
		}
		return nil
	})
}

func (c *Command) CombinedOutput() ([]byte, error) {
	var b []byte
	err := c.sessionWrap(func() error {
		var err error
		b, err = c.sess.CombinedOutput(c.String())
		if err != nil {
			return fmt.Errorf("getting combined output of command %s: %w", c.String(), err)
		}
		return nil
	})
	return b, err
}

func (c *Command) Output() ([]byte, error) {
	var b []byte
	err := c.sessionWrap(func() error {
		var err error
		b, err = c.sess.Output(c.String())
		if err != nil {
			return fmt.Errorf("getting output of command %s: %w", c.String(), err)
		}
		return nil
	})
	return b, err
}

func (c *Command) Start() error {
	if c.sessClosed {
		return errors.New("session closed")
	}

	go func() {
		select {
		case <-c.ctx.Done():
			if err := c.sess.Signal(ssh.SIGINT); err != nil {
				c.sess.Signal(ssh.SIGKILL)
			}
		case <-c.startDone:
		}
		c.sess.Close()
		c.sessClosed = true
	}()

	if err := c.sess.Start(c.String()); err != nil {
		return fmt.Errorf("starting command %s: %w", c.String(), err)
	}

	return nil
}

func (c *Command) Wait() error {
	defer func() {
		c.startDone <- struct{}{}
	}()

	if err := c.sess.Wait(); err != nil {
		return fmt.Errorf("waiting for command %s to finish: %w", c.String(), err)
	}

	return nil
}

func (c *Command) StderrPipe() (io.Reader, error) {
	r, err := c.sess.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("getting stderr pipe: %w", err)
	}
	return r, nil
}

func (c *Command) StdinPipe() (io.WriteCloser, error) {
	w, err := c.sess.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("getting stdin pipe: %w", err)
	}
	return w, nil
}

func (c *Command) StdoutPipe() (io.Reader, error) {
	r, err := c.sess.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("getting stdout pipe: %w", err)
	}
	return r, nil
}

func (c *Command) String() string {
	quotedArgs := make([]string, len(c.args))
	for i, arg := range c.args {
		quotedArgs[i] = strconv.Quote(arg)
	}
	return fmt.Sprintf("%s %s", c.name, strings.Join(quotedArgs, " "))
}

func (c *Command) sessionWrap(method func() error) error {
	if c.sessClosed {
		return errors.New("session closed")
	}

	defer func() {
		c.sess.Close()
		c.sessClosed = true
	}()

	retChan := make(chan error)
	go func() {
		retChan <- method()
	}()

	select {
	case <-c.ctx.Done():
		if err := c.sess.Signal(ssh.SIGINT); err != nil {
			c.sess.Signal(ssh.SIGKILL)
		}
		return fmt.Errorf("context done: %w", c.ctx.Err())
	case ret := <-retChan:
		if ret != nil {
			return fmt.Errorf("error in session method: %w", ret)
		}
		return nil
	}
}
