package register

import (
	"context"
	"errors"
	"fmt"
)

// fleet.go is the one pointer that is not a project's.
//
// A FLEET IS A SINGLETON, so its key is not a prefix: there is one cloud, and
// the row is the cloud's own document. Everything else about it is the project
// pointer's rule, because the reason is the same one — a value every node must
// compute with identically needs a revision so that a node which is behind is
// behind by something it can see.

const fleetKey = "f"

// PublishFleet moves the cloud's own document forward.
//
// THE SAME MONOTONE RULE AS A PROJECT'S, and for a sharper reason: a floor that
// went backwards on one node and not another would be two nodes computing
// different placements with no way to tell which was stale.
func (r *Register) PublishFleet(ctx context.Context, revision uint64, document []byte) (ObjectID, error) {
	if len(document) == 0 {
		return "", ErrEmptyObject
	}
	id := IDOf(document)

	r.mu.Lock()
	defer r.mu.Unlock()

	current, ok, err := r.fleet(ctx)
	if err != nil && !errors.Is(err, ErrMalformedPointer) {
		return "", err
	}
	if ok && revision <= current.Revision {
		return "", fmt.Errorf("%w: the fleet holds %d, was offered %d",
			ErrStaleRevision, current.Revision, revision)
	}

	if err := r.store.Put(ctx, objectKey(id), document); err != nil {
		return "", fmt.Errorf("register: storing the fleet document: %w", err)
	}
	if err := r.store.Put(ctx, fleetKey, encodePointer(Pointer{Object: id, Revision: revision})); err != nil {
		return "", fmt.Errorf("register: moving the fleet: %w", err)
	}
	return id, nil
}

// ResolveFleet is what the cloud currently declares about itself.
//
// NOT PUBLISHED IS ErrNotPublished AND NEVER AN EMPTY DOCUMENT, so a reader can
// tell "this cloud has said nothing, use your default" from "the store cannot
// be reached, change nothing" — which are opposite instructions, and getting
// them the wrong way round means either every node picking its own floor or
// every node freezing on a transient error.
func (r *Register) ResolveFleet(ctx context.Context) (Pointer, error) {
	p, ok, err := r.fleet(ctx)
	if err != nil {
		return Pointer{}, err
	}
	if !ok {
		return Pointer{}, fmt.Errorf("%w: the fleet", ErrNotPublished)
	}
	return p, nil
}

func (r *Register) fleet(ctx context.Context) (Pointer, bool, error) {
	b, ok, err := r.store.Get(ctx, fleetKey)
	if err != nil {
		return Pointer{}, false, fmt.Errorf("register: reading the fleet: %w", err)
	}
	if !ok {
		return Pointer{}, false, nil
	}
	p, err := decodePointer(b)
	if err != nil {
		return Pointer{}, false, err
	}
	return p, true, nil
}
