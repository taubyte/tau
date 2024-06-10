package command

import (
	"errors"
	"fmt"
	"io"

	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command/framer"
)

func New(command string, body Body) *Command {
	return &Command{
		Command: command,
		Body:    body,
	}
}

func (c *Command) Encode(s io.Writer) error {
	return framer.Send(Magic, Version, s, c)
}

func (c *Command) Connection() (streams.Connection, error) {
	if c.conn != nil {
		return c.conn, nil
	}
	return nil, errors.New("no connection found")
}

func (c *Command) Get(key string) (interface{}, bool) {
	val, ok := c.Body[key]
	return val, ok
}

func (c *Command) Name() string {
	return c.Command
}

func (c *Command) Set(key, value string) {
	c.Body[key] = value
}

func (c *Command) SetName(value interface{}) error {
	var ok bool
	c.Command, ok = value.(string)
	if !ok {
		return fmt.Errorf("`%v` cannot convert to string", value)
	}

	return nil
}

func (c *Command) Delete(key string) {
	delete(c.Body, key)
}

func (c *Command) Raw() map[string]interface{} {
	return c.Body
}

func Decode(conn streams.Connection, r io.Reader) (*Command, error) {
	c := Command{conn: conn}

	if err := framer.Read(Magic, Version, r, &c); err != nil {
		return nil, err
	}

	return &c, nil
}
