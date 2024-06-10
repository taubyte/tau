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
		logger.Errorf("getting `cached` value from p2p response failed with: %s", err.Error())
		return
	}

	var ok bool
	if cached, ok = cachedIface.(bool); !ok {
		logger.Errorf("p2p response `cached` value is not a boolean; %T", cachedIface)
	}

	return
}
