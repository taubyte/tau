package storage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/placement"
)

// A put is held by the holders its writer's placement named, and a read asks the
// holders the reader's placement names. The two differ whenever the cloud grew
// between them, which is every write made before a node had heard of the others:
// placement is then the writer alone, and once the rest are known the holders a
// reader asks may not include it. Nothing would ever ask the one node that has the
// bytes.
//
// So each node hands every root it references to the holders placement gives it
// now, once per placement. A holder that has the root already keeps it with no
// fetch; the copies left where placement no longer puts them are the sweep's to
// drop, behind gate 5.

// rehome asks every holder of every root this node references to hold it, unless
// that was done in full under this placement. A holder that could not be asked
// leaves the placement unrecorded, so the next turn asks again.
func (s *server) rehome(ctx context.Context) error {
	s.homeMu.Lock()
	defer s.homeMu.Unlock()
	replicas, _, err := s.promise(ctx)
	if err != nil {
		return err
	}
	members := s.placement()
	view := placementView(members, replicas)
	if view == s.homed {
		return nil
	}
	refs, err := s.local.References(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range refs {
		holders, err := blockHolders(r.Root.String(), members, replicas)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, holder := range holders {
			if holder == s.self {
				continue
			}
			if err := s.askHold(ctx, holder, r.Root, r.Address, r.Name, r.Stamp); err != nil {
				errs = append(errs, fmt.Errorf("%s to %s: %w", r.Root, holder, err))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	s.homed = view
	return nil
}

// placementView names a placement: the members that accept, in order, and how many
// copies each root gets.
func placementView(members []placement.Member, replicas int) string {
	ids := make([]string, 0, len(members))
	for _, m := range members {
		if m.Accepts {
			ids = append(ids, m.ID)
		}
	}
	slices.Sort(ids)
	return strconv.Itoa(replicas) + "/" + strings.Join(ids, "/")
}
