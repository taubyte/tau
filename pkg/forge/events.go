package forge

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/git/repokey"
)

const CapPushEvents Capability = "push-events"

// PushEvents is a forge's own event feed, for one that cannot sign a webhook,
// read over a channel the deployment opened and authenticated (Gerrit's
// stream-events over ssh to a pinned host key). It hangs off the driver: the
// credential is the deployment's. StreamPushes still checks each claim as
// VerifyPush checks a delivery.
type PushEvents interface {
	// StreamPushes holds ONE subscription: it calls ready once the forge has
	// accepted it, then emit for each push it reports, and returns when ctx
	// ends or the feed breaks. It never reconnects; reconnecting, and
	// recovering what was missed while it was down, is the caller's.
	StreamPushes(ctx context.Context, ready func(), emit func(PushClaim)) error

	// Heads is the branch heads of one repository as the deployment sees them
	// — what a caller compares against after a reconnect, because a feed with
	// no replay loses whatever happened while it was down.
	Heads(ctx context.Context, key repokey.Key) ([]PushClaim, error)
}

func (r *Registry) EventHosts() []string {
	var out []string
	for _, h := range r.Hosts() {
		if _, ok := r.drivers[h].(PushEvents); ok {
			out = append(out, h)
		}
	}
	return out
}

// Each claim is checked before it reaches emit: a valid one arrives as a Push,
// an invalid one as a zero Push and the reason, a ref deletion not at all.
func (r *Registry) StreamPushes(ctx context.Context, host string, ready func(), emit func(Push, error)) error {
	ev, err := r.events(host)
	if err != nil {
		return err
	}
	return ev.StreamPushes(ctx, ready, func(c PushClaim) {
		p, err := r.checkClaim(host, c)
		if errors.Is(err, ErrRefDeleted) {
			return
		}
		emit(p, err)
	})
}

// Heads is one repository's branch heads, each checked as a streamed claim is.
// A head that does not check is left out rather than failing the rest.
func (r *Registry) Heads(ctx context.Context, key repokey.Key) ([]Push, error) {
	ev, err := r.events(key.Host)
	if err != nil {
		return nil, err
	}
	claims, err := ev.Heads(ctx, key)
	if err != nil {
		return nil, err
	}
	out := make([]Push, 0, len(claims))
	for _, c := range claims {
		if c.Repository != key {
			continue
		}
		if p, err := r.checkClaim(key.Host, c); err == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *Registry) events(host string) (PushEvents, error) {
	d, err := r.Driver(host)
	if err != nil {
		return nil, err
	}
	ev, ok := d.(PushEvents)
	if !ok {
		return nil, &UnsupportedError{Host: host, Capability: CapPushEvents}
	}
	return ev, nil
}

// checkClaim is VerifyPush's steps 9 and 10 for a claim with no hook: the
// repository must be on the feed's own host and addressable by its driver, the
// ref a refname, the commit a whole object id.
func (r *Registry) checkClaim(host string, c PushClaim) (Push, error) {
	if c.Repository.Host != host {
		return Push{}, fmt.Errorf("forge: the feed of %s reported a push to %q", host, c.Repository)
	}
	if err := r.drivers[host].Addressable(c.Repository); err != nil {
		return Push{}, &UnaddressableError{Host: host, Key: c.Repository, Reason: err.Error()}
	}
	if reason := refReason(c.Ref); reason != "" {
		return Push{}, fmt.Errorf("forge: the feed of %s: %s", host, reason)
	}
	if isZeroOID(c.Commit) {
		return Push{}, ErrRefDeleted
	}
	if !isObjectID(c.Commit) {
		return Push{}, fmt.Errorf("forge: the feed of %s reported a push naming no commit", host)
	}
	return Push{Key: c.Repository, Ref: c.Ref, Commit: c.Commit}, nil
}
