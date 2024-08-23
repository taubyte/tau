package registry

import (
	"context"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/opencontainers/go-digest"

	//lint:ignore ST1001 ignore
	. "github.com/taubyte/tau/pkg/spin"
)

type pullRequest struct {
	ctx        context.Context
	image      string
	registries []string
	ret        chan error
	progress   chan<- PullProgress
}

type registry struct {
	ctx  context.Context
	ctxC context.CancelFunc

	registries []string

	root string

	imageToDigestCache *ttlcache.Cache[string, digest.Digest]

	lock sync.RWMutex

	pullRequest chan *pullRequest

	wg sync.WaitGroup
}

var DigestResolvCacheTTL = 120 * time.Second

func New(ctx context.Context, root string, options ...Option[Registry]) (Registry, error) {
	r := &registry{
		registries:  []string{"registry.hub.docker.com", "registry.hub.docker.com/library"},
		root:        root,
		pullRequest: make(chan *pullRequest, 16),
		imageToDigestCache: ttlcache.New(
			ttlcache.WithTTL[string, digest.Digest](DigestResolvCacheTTL),
		),
	}

	for _, opt := range options {
		if err := opt(r); err != nil {
			return nil, err
		}
	}

	if err := os.MkdirAll(path.Join(root, "images"), 0750); err != nil {
		return nil, fmt.Errorf("populating root folder failed with %w", err)
	}

	r.ctx, r.ctxC = context.WithCancel(ctx)

	r.wg.Add(1)
	go r.pullRequestHandler()

	return r, nil
}

func (r *registry) Close() {
	r.ctxC()
	r.wg.Wait()
}
