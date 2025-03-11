package context

import "github.com/taubyte/tau/pkg/http/request"

type Context struct {
	req         *request.Request
	variables   map[string]interface{}
	body        []byte
	rawResponse bool
}

type Option func(*Context) error
