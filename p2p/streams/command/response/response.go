package response

import (
	"fmt"
	"io"

	"github.com/taubyte/tau/p2p/streams/command/framer"

	"github.com/taubyte/tau/p2p/streams/command"
)

type Response map[string]interface{}

func (r Response) Encode(s io.Writer) error {
	return framer.Send(command.Magic, command.Version, s, r)
}

func (r Response) Get(value string) (interface{}, error) {
	val, ok := r[value]
	if !ok {
		return nil, fmt.Errorf("`%s` does not exist", value)
	}

	return val, nil
}

func (r Response) Set(key string, value interface{}) {
	r[key] = value
}

func Decode(s io.Reader) (Response, error) {
	var r Response
	if err := framer.Read(command.Magic, command.Version, s, &r); err != nil {
		return nil, err
	}

	return r, nil
}
