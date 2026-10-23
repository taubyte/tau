// A function in Go, built by TinyGo: it answers the language, the method, the
// path and the body, so a caller can tell which component served it.
package main

import (
	"go.bytecodealliance.org/cm"

	"function/gen/taubyte/handler/handler"
)

func init() {
	handler.Exports.Handle = func(req handler.Request) handler.Response {
		body := []byte("tinygo " + req.Method + " " + req.Path + " ")
		body = append(body, req.Body.Slice()...)
		return handler.Response{Status: 200, Body: cm.ToList(body)}
	}
}

func main() {}
