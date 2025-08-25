package gateway

import (
	"github.com/taubyte/tau/p2p/streams/client"
)

type responseGetter struct {
	*client.Response
}

func (g *Gateway) Get(res *client.Response) responseGetter {
	return responseGetter{res}
}

func (r responseGetter) Cached() (cached bool) {
	cachedIface, err := r.Get("cached")
	if err != nil {
		logger.Errorf("failed to get cached value: %s", err.Error())
		return
	}

	var ok bool
	if cached, ok = cachedIface.(bool); !ok {
		logger.Errorf("cached value is not a boolean: %T", cachedIface)
	}

	return
}
