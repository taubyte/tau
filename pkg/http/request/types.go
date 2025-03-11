package request

import "net/http"

type Request struct {
	ResponseWriter http.ResponseWriter
	HttpRequest    *http.Request
}
