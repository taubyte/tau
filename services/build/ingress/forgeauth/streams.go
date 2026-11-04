package forgeauth

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/ingress"
)

var _ ingress.Streams = (*Forges)(nil)

// Hosts is every forge with an event feed.
func (i *Forges) Hosts() []string { return i.forges.EventHosts() }

// Stream holds one subscription to host's feed. Each push has already been
// checked by pkg/forge the way a verified delivery's is.
func (i *Forges) Stream(ctx context.Context, host string, ready func(), emit func(ingress.Push, error)) error {
	return i.forges.StreamPushes(ctx, host, ready, func(p forge.Push, err error) {
		emit(toPush(p), err)
	})
}

// Heads is one repository's branch heads.
func (i *Forges) Heads(ctx context.Context, repository string) ([]ingress.Push, error) {
	key, err := repokey.Parse("https://" + repository)
	if err != nil {
		return nil, fmt.Errorf("build: %q names no repository: %w", repository, err)
	}
	heads, err := i.forges.Heads(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("build: %w", err)
	}
	out := make([]ingress.Push, 0, len(heads))
	for _, p := range heads {
		out = append(out, toPush(p))
	}
	return out, nil
}

func toPush(p forge.Push) ingress.Push {
	if p.Key.Host == "" {
		return ingress.Push{}
	}
	return ingress.Push{Repository: p.Key.String(), Ref: p.Ref, Commit: p.Commit}
}
