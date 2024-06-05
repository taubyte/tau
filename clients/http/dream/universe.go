package http

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/clients/http/dream/inject"
)

func (u *Universe) Inject(ops ...inject.Injectable) error {
	for _, op := range ops {
		err := u.client.runInjection(u.Name, op)
		if err != nil {
			return fmt.Errorf("injection `%s` failed with: %w", op.Name, err)
		}
	}

	return nil
}

func (c *Client) runInjection(universe string, op inject.Injectable) (err error) {
	if op.Params == nil {
		op.Params = []interface{}{}
	}
	ret := map[string]interface{}{"params": op.Params}

	if op.Config != nil {
		ret["config"] = op.Config
	}

	switch op.Method {
	case inject.POST:
		err = c.post(op.Run(universe), ret, nil)

	default:
		err = errors.New("method not supported " + op.Method.String())
	}

	return
}
