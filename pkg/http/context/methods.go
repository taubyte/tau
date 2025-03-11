package context

import (
	"net/http"

	service "github.com/taubyte/tau/pkg/http"
)

func (c *Context) RawResponse() bool {
	return c.rawResponse
}

func (c *Context) Variables() map[string]interface{} {
	return c.variables
}

func (c *Context) Body() []byte {
	return c.body
}

func (c *Context) Request() *http.Request {
	return c.req.HttpRequest
}

func (c *Context) Writer() http.ResponseWriter {
	return c.req.ResponseWriter
}

func (c *Context) HandleWith(handler service.Handler) error {
	if handler == nil {
		panic("Nil handler!")
	}

	ret, err := handler(c)
	if err != nil {
		c.returnError(http.StatusBadRequest, err)
		return err
	}

	request := c.req.HttpRequest

	switch redirect := ret.(type) {
	case service.TemporaryRedirect:
		http.Redirect(c.req.ResponseWriter, request, string(redirect), http.StatusTemporaryRedirect)
		return nil
	case service.PermanentRedirect:
		http.Redirect(c.req.ResponseWriter, request, string(redirect), http.StatusPermanentRedirect)
		return nil
	}

	return c.returnData(http.StatusOK, ret)
}

func (c *Context) HandleAuth(handler service.Handler) error {
	if handler == nil {
		return nil
	}

	_, err := handler(c)
	if err != nil {
		c.returnError(http.StatusUnauthorized, err)
		return err
	}

	return nil
}

func (c *Context) HandleCleanup(handler service.Handler) error {
	if handler == nil {
		return nil
	}

	_, err := handler(c)
	return err
}
