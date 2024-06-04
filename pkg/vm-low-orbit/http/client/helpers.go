package client

import (
	"net/http"

	"github.com/taubyte/go-sdk/errno"
)

func (f *Factory) getClientAndRequest(clientId uint32, requestId uint32) (client *Client, request *Request, err errno.Error) {
	client, err = f.getClient(clientId)
	if err != 0 {
		return
	}

	request, err = client.getRequest(requestId)
	return
}

func (f *Factory) getResponse(clientId uint32, requestId uint32) (response *http.Response, err errno.Error) {
	client, err := f.getClient(clientId)
	if err != 0 {
		return
	}

	request, err := client.getRequest(requestId)

	return request.Response, err
}
