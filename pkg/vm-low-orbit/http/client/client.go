package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/netguard"
)

// restrictedHTTPClient builds the http.Client handed to a guest function. Its
// dialer enforces the egress policy (netguard) on every connection — including
// redirects, which re-dial through the same transport — so a function cannot
// reach node-local services or the cloud metadata endpoint. The timeout bounds
// a hung request so it can't pin the pooled instance (which is freed only after
// the synchronous call returns).
func restrictedHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext:           netguard.RestrictedDialer(10 * time.Second).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          8,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
		// The dial guard already blocks a redirect to a denied IP (each redirect
		// re-dials through the transport). This is belt-and-suspenders on the
		// literal target host, and caps redirect chains.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			if ip := net.ParseIP(req.URL.Hostname()); ip != nil && netguard.IsDenied(ip) {
				return fmt.Errorf("netguard: redirect to %s not permitted", req.URL.Host)
			}
			return nil
		},
	}
}

func (f *Factory) getClient(clientId uint32) (*Client, errno.Error) {
	f.clientsLock.RLock()
	defer f.clientsLock.RUnlock()
	if client, ok := f.clients[clientId]; ok {
		return client, 0
	}

	return nil, errno.ErrorClientNotFound
}

func (f *Factory) newHttpClient(ctx context.Context, module common.Module,
	clientIdPtr uint32,
) uint32 {
	c := &Client{
		Id:     f.generateClientId(),
		Client: restrictedHTTPClient(),
		reqs:   make(map[uint32]*Request),
	}

	f.clientsLock.Lock()
	defer f.clientsLock.Unlock()
	f.clients[c.Id] = c

	return uint32(f.WriteUint32Le(module, clientIdPtr, c.Id))
}
