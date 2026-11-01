package register

import (
	"context"
	"errors"
	"fmt"
)

// A fleet is a singleton: one cloud, so its key is not a prefix. It carries a
// revision like a project pointer, so a node that is behind can see it.

const fleetKey = "f"

// PublishFleet moves the cloud's own document forward, with the same monotone rule
// as a project: a floor that went backwards on one node would give two nodes
// different placements.
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

// ResolveFleet is what the cloud currently declares about itself. Nothing published
// is ErrNotPublished, never an empty document, so a reader can tell "use your
// default" from "the store cannot be reached, change nothing".
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
