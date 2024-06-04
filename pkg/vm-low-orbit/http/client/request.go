package client

import (
	"context"
	"net/http"
	urlpkg "net/url"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (client *Client) getRequest(reqId uint32) (*Request, errno.Error) {
	client.reqLock.RLock()
	defer client.reqLock.RUnlock()

	req, ok := client.reqs[reqId]
	if !ok {
		return nil, errno.ErrorAddressOutOfMemory
	}

	return req, 0
}

func (client *Client) setRequest(req *Request) errno.Error {
	client.reqLock.Lock()
	defer client.reqLock.Unlock()

	client.reqs[req.Id] = req

	return 0
}

func (f *Factory) W_newHttpRequest(ctx context.Context, module common.Module,
	clientId uint32,
	reqIdPtr uint32,
) (err errno.Error) {
	client, err := f.getClient(clientId)
	if err != 0 {
		return err
	}

	reqId := client.generateReqId()

	_r, err0 := http.NewRequest("", "", nil)
	if err0 != nil {
		return errno.ErrorNewRequestFailed
	}
	r := &Request{
		Id:      reqId,
		Request: _r,
	}

	err = client.setRequest(r)
	if err != 0 {
		return err
	}

	return f.WriteUint32Le(module, reqIdPtr, reqId)
}

func (f *Factory) W_setHttpRequestURL(ctx context.Context, module common.Module,
	clientId uint32,
	requestId uint32,
	urlPtr uint32, urlLen uint32,
) (err errno.Error) {
	client, req, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return err
	}

	url, err := f.ReadString(module, urlPtr, urlLen)
	if err != 0 {
		return err
	}

	var _err error
	req.URL, _err = urlpkg.Parse(url)
	if _err != nil {
		return errno.ErrorParseUrlFailed
	}

	return client.setRequest(req)
}

func (f *Factory) W_doHttpRequest(ctx context.Context, module common.Module,
	clientId,
	requestId uint32,
) errno.Error {
	client, req, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return err
	}

	var _err error
	resp, _err := client.Do(req.Request)
	if _err != nil {
		return errno.ErrorHttpRequestFailed
	}

	req.Response = resp
	return client.setRequest(req)
}
