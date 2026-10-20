package clients

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/p2p/rpc"
)

// AuthAPI carries a Connect call the public frontend received to auth, over
// p2p, without reading it: the frontend already chose which calls pass, and
// auth answers them. It satisfies frontend.AuthAPI.
type AuthAPI struct{ pool *Pool }

// maxAuthAnswer bounds what is read back from auth.
const maxAuthAnswer = 1 << 20

// AuthCalls carries calls to the auth service p reaches.
func AuthCalls(p *Pool) (*AuthAPI, error) {
	if err := requireProtocol(p, AuthProtocol, "auth calls"); err != nil {
		return nil, err
	}
	return &AuthAPI{pool: p}, nil
}

// Call posts body to procedure on one auth peer and returns its answer. A
// peer that cannot be reached is the next one's to answer; any answer at all,
// a Connect error included, is the answer.
func (a *AuthAPI) Call(ctx context.Context, procedure string, header http.Header, body []byte) (int, http.Header, []byte, error) {
	var (
		status int
		rh     http.Header
		rb     []byte
	)
	err := a.pool.Do(ctx, func(c *rpc.Client) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL()+procedure, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header = header.Clone()
		resp, err := c.HTTP().Do(req)
		if err != nil {
			return connect.NewError(connect.CodeUnavailable, err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxAuthAnswer+1))
		if err != nil {
			return connect.NewError(connect.CodeUnavailable, err)
		}
		if len(b) > maxAuthAnswer {
			return fmt.Errorf("clients: auth answered %s with more than %d bytes", procedure, maxAuthAnswer)
		}
		status, rh, rb = resp.StatusCode, resp.Header, b
		return nil
	})
	return status, rh, rb, err
}
