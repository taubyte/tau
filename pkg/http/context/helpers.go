package context

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/mux"
	service "github.com/taubyte/tau/pkg/http"
)

func (c *Context) returnData(code int, interfaceData interface{}) error {
	if c.rawResponse {
		var err error

		switch data := interfaceData.(type) {
		case []byte:
			c.req.ResponseWriter.WriteHeader(code)
			_, err = c.req.ResponseWriter.Write(data)
		case string:
			c.req.ResponseWriter.WriteHeader(code)
			_, err = c.req.ResponseWriter.Write([]byte(data))
		case service.RawData:
			c.req.ResponseWriter.Header().Set("Content-Type", data.ContentType)
			c.req.ResponseWriter.WriteHeader(code)
			_, err = c.req.ResponseWriter.Write(data.Data)
		case service.RawStream:
			c.req.ResponseWriter.Header().Set("Content-Type", data.ContentType)
			c.req.ResponseWriter.WriteHeader(code)
			rbuf := make([]byte, 1024)
			n := 0
			for {
				n, err = data.Stream.Read(rbuf)
				if n > 0 {
					if _, err = c.req.ResponseWriter.Write(rbuf[:n]); err != nil {
						break
					}
				}
				if err != nil {
					if errors.Is(err, io.EOF) {
						err = nil
					}
					break
				}
			}
			data.Stream.Close()
		}
		if err != nil {
			return fmt.Errorf("writing raw response failed with: %w", err)
		}
	} else {
		var m string
		m, err := c.formatBody(interfaceData)
		if err != nil {
			c.returnError(http.StatusInternalServerError, err)
			return err
		}
		_, err = c.req.ResponseWriter.Write([]byte(m))
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Context) returnError(code int, err error) {
	m, _ := c.formatBody(
		map[string]interface{}{
			"code":  code,
			"error": err.Error(),
		},
	)

	// TODO log error here
	c.req.ResponseWriter.Write([]byte(m))
	c.req.ResponseWriter.WriteHeader(code)
}

func (c *Context) formatBody(m interface{}) (string, error) {
	out, err := json.Marshal(m)
	if err != nil {
		return "", err
	}

	return string(out), err
}

func (ctx *Context) extractVariables(required []string, optional []string) (map[string]interface{}, error) {
	if len(required)+len(optional) == 0 {
		return map[string]interface{}{}, nil
	}

	var body map[string]interface{}
	if len(ctx.body) > 0 {
		err := json.Unmarshal(ctx.body, &body)
		if err != nil {
			return nil, err
		}
	}

	request := ctx.Request()
	vars := mux.Vars(request)

	xVars := make(map[string]interface{})
	add := func(k string) bool {
		if q := request.URL.Query(); q != nil && q.Has(k) {
			xVars[k] = q.Get(k)
			return true
		} else if v, ok := vars[k]; ok {
			xVars[k] = v
			return true
		} else if v := request.Header.Get(k); v != "" {
			xVars[k] = v
			return true
		} else if v, ok := body[k]; ok {
			xVars[k] = v
			return true
		}

		return false
	}

	for _, k := range optional {
		add(k)
	}

	for _, k := range required {
		if !add(k) {
			return nil, fmt.Errorf("processing `%s`, key `%s` not found", request.URL, k)
		}
	}

	return xVars, nil
}
