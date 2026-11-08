// Package mock is cm for tests: the region's settings, which a test sets and changes,
// answered the way cm answers them. It runs on a test host or a dream node, so a
// service reads it through the same client it reads cm through.
package mock

import (
	"context"
	"net/http"
	"sync"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/services/cm/settings"
)

// CM answers the region's settings.
type CM struct {
	mu       sync.Mutex
	region   settings.Region
	failing  error
	listener *rpc.Server
}

var (
	_ service.Service = (*CM)(nil)
	_ settings.Source = (*CM)(nil)
)

// New is cm answering region.
func New(region settings.Region) *CM { return &CM{region: region} }

// Set changes the region's settings, as a cloud changing them at runtime would.
func (c *CM) Set(region settings.Region) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.region = region
}

// Fail has every ask answered with err, as a cm that cannot be reached; nil answers
// again.
func (c *CM) Fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failing = err
}

func (c *CM) Settings(context.Context) (settings.Region, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failing != nil {
		return settings.Region{}, c.failing
	}
	return c.region, nil
}

// Start answers on cm's protocol, for readers on other nodes.
func (c *CM) Start(ctx context.Context, h service.Host) error {
	mux := http.NewServeMux()
	settings.Serve(mux, c)
	listener, err := rpc.Listen(ctx, h.Peer().Host(), settings.Protocol, mux)
	if err != nil {
		return err
	}
	c.listener = listener
	h.Serve(settings.Protocol, c)
	return nil
}

func (c *CM) Close() error {
	if c.listener != nil {
		return c.listener.Close()
	}
	return nil
}
