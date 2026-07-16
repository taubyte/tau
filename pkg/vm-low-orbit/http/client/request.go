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

func (f *Factory) newHttpRequest(ctx context.Context, module common.Module,
	clientId uint32,
	reqIdPtr uint32,
) uint32 {
	client, err := f.getClient(clientId)
	if err != 0 {
		return uint32(err)
	}

	reqId := client.generateReqId()

	_r, err0 := http.NewRequest("", "", nil)
	if err0 != nil {
		return uint32(errno.ErrorNewRequestFailed)
	}
	r := &Request{
		Id:      reqId,
		Request: _r,
	}

	err = client.setRequest(r)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteUint32Le(module, reqIdPtr, reqId))
}

func (f *Factory) setHttpRequestURL(ctx context.Context, module common.Module,
	clientId uint32,
	requestId uint32,
	urlPtr uint32, urlLen uint32,
) uint32 {
	client, req, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	url, err := f.ReadString(module, urlPtr, urlLen)
	if err != 0 {
		return uint32(err)
	}

	var _err error
	req.URL, _err = urlpkg.Parse(url)
	if _err != nil {
		return uint32(errno.ErrorParseUrlFailed)
	}

	return uint32(client.setRequest(req))
}

func (f *Factory) doHttpRequest(ctx context.Context, module common.Module,
	clientId,
	requestId uint32,
) uint32 {
	client, req, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	var _err error
	resp, _err := client.Do(req.Request)
	if _err != nil {
		return uint32(errno.ErrorHttpRequestFailed)
	}

	req.Response = resp
	return uint32(client.setRequest(req))
}
