package context

import (
	"net/http"

	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/http/request"
)

func New(req *request.Request, vars *service.Variables, options ...Option) (service.Context, error) {
	c := &Context{
		req: req,
	}

	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	c.body = req.Body()

	var err error
	if c.variables, err = c.extractVariables(vars.Required, vars.Optional); err != nil {
		c.returnError(http.StatusNotAcceptable, err)
		return nil, err
	}

	if !c.rawResponse {
		c.req.ResponseWriter.Header().Set("Content-Type", "application/json")
	}

	return c, nil
}
