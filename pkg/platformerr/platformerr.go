// Package platformerr gives platform errors a code that survives wrapping and
// the p2p wire, and turns it into a public HTTP answer that never shows the
// internal error text.
package platformerr

import (
	"encoding/json"
	"errors"
	"net/http"
)

type Code string

const (
	NotFound          Code = "not_found"
	NodeBusy          Code = "node_busy"
	ModuleUnavailable Code = "module_unavailable"
	FunctionError     Code = "function_error"
	FunctionTimeout   Code = "function_timeout"
	Internal          Code = "internal"
)

type Error struct {
	Code Code
	Err  error
}

// Error returns the inner text so wrappers and log lines read as before.
func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// New tags err with c, unless err already carries a code: the innermost code wins.
func New(c Code, err error) error {
	if CodeOf(err) != "" {
		return err
	}
	return &Error{Code: c, Err: err}
}

// CodeOf returns the innermost code in err's chain, or "" if there is none.
func CodeOf(err error) (c Code) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if pe, ok := e.(*Error); ok {
			c = pe.Code
		}
	}
	return
}

var statuses = map[Code]int{
	NotFound:          http.StatusNotFound,
	NodeBusy:          http.StatusServiceUnavailable,
	ModuleUnavailable: http.StatusBadGateway,
	FunctionError:     http.StatusInternalServerError,
	FunctionTimeout:   http.StatusGatewayTimeout,
	Internal:          http.StatusInternalServerError,
}

var messages = map[Code]string{
	NotFound:          "Nothing at this address",
	NodeBusy:          "The node is busy, try again",
	ModuleUnavailable: "The code for this address could not be loaded",
	FunctionError:     "The function failed",
	FunctionTimeout:   "The function took too long",
	Internal:          "Internal error",
}

// Status is the HTTP status for c; unknown and empty codes are 500.
func Status(c Code) int {
	if s, ok := statuses[c]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Write answers with err's code as JSON. err's text is never written.
func Write(w http.ResponseWriter, err error, requestID string) {
	c := CodeOf(err)
	if _, ok := statuses[c]; !ok {
		c = Internal
	}
	if c == NodeBusy {
		w.Header().Set("Retry-After", "1")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(Status(c))
	json.NewEncoder(w).Encode(map[string]any{"code": Status(c), "error": c, "message": messages[c], "request_id": requestID})
}
