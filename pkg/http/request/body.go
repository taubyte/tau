package request

import "io"

func (r *Request) Body() []byte {
	d, _ := io.ReadAll(r.HttpRequest.Body)
	return d
}
