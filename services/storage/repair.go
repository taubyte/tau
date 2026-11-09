package storage

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ipfs/go-cid"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/placement"
)

// The repair loop. Each pass, for every root this node references, it proves that
// each holder placement names now holds the root (probe.go), and gives the root to
// one that cannot prove it, with every reference this node has to it so the
// holder's rows say what the bytes are for. A copy written before the region grew,
// which placement no longer puts here, and a holder that lost its copy are one
// repair. A holder placement names that lost its own copy takes it back first.
//
// Under-replicated first: the roots the last pass proved on the fewest holders go
// first, roots never proven before all. A root every holder proved, under this
// placement and these references, is proven again only after reprove. A pass proves
// at most repairBudget roots, so it ends, and every probe and hold has its own
// deadline, so a holder that stalls costs its own call, never the pass.
//
// A copy placement no longer puts on this node is dropped once every holder it names
// proved the root and took every reference this node has to it (gates, gc.go): the
// references go, and the sweep frees the bytes.

const (
	// reprove is how long a root every holder proved goes before it is proven
	// again: a holder that lost its copy meanwhile is found within it.
	reprove = time.Hour

	// repairBudget is how many roots one pass proves.
	repairBudget = 256

	// holdWait bounds giving one root to one holder, which fetches every block.
	holdWait = 10 * time.Minute
)

// proof is what the last pass established of one root: under which placement and
// how many references, when, how many holders proved it, whether that settles it —
// every holder proved it, or this node, holding no copy placement asks of it, has
// nothing to prove theirs against — and whether every holder took every reference.
type proof struct {
	view    string
	refs    int
	at      time.Time
	proven  int
	settled bool
	handed  bool

	// holders is how many holders placement names other than this node; named
	// is this node being one of them; bare is this node holding no copy.
	holders int
	named   bool
	bare    bool
}

// rooted is one root and this node's references to it.
type rooted struct {
	root cid.Cid
	refs []Reference
}

// repair is one pass of the loop.
func (s *server) repair(ctx context.Context) error {
	s.repairMu.Lock()
	defer s.repairMu.Unlock()
	replicas, _, err := s.promise(ctx)
	if err != nil {
		return err
	}
	members := s.placement()
	view := placementView(members, replicas)
	refs, err := s.local.References(ctx)
	if err != nil {
		return err
	}
	roots := byRoot(refs)
	if s.proofs == nil {
		s.proofs = map[string]proof{}
	}
	s.forgetUnreferenced(roots)

	now := s.now()
	var errs []error
	for _, r := range s.due(roots, view, now) {
		last, known := s.proofs[r.root.KeyString()]
		handed := known && last.view == view && last.refs == len(r.refs) && last.handed
		p, err := s.repairRoot(ctx, r, members, replicas, handed)
		p.view, p.refs, p.at = view, len(r.refs), now
		s.proofs[r.root.KeyString()] = p
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.root, err))
			continue
		}
		g := Gates{ProvenHolders: p.proven, Handed: p.handed, Bare: p.bare}
		if p.named || g.Open(Misplaced, now, 0, p.holders) != nil {
			continue
		}
		if err := s.dropRoot(ctx, r); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.root, err))
		}
	}
	return errors.Join(errs...)
}

// dropRoot drops every reference this node has to r's root, a copy placement does
// not name, and owes a sweep.
func (s *server) dropRoot(ctx context.Context, r rooted) error {
	for _, ref := range r.refs {
		if err := s.local.Dereference(ctx, r.root, ref.Address, ref.Name, ref.Stamp); err != nil {
			return err
		}
	}
	delete(s.proofs, r.root.KeyString())
	s.owed.Store(true)
	return nil
}

// byRoot groups references by root, in the order the rows list them.
func byRoot(refs []Reference) []rooted {
	var out []rooted
	for _, ref := range refs {
		if n := len(out); n > 0 && out[n-1].root.Equals(ref.Root) {
			out[n-1].refs = append(out[n-1].refs, ref)
			continue
		}
		out = append(out, rooted{root: ref.Root, refs: []Reference{ref}})
	}
	return out
}

// forgetUnreferenced drops what was proven of roots this node no longer references.
func (s *server) forgetUnreferenced(roots []rooted) {
	live := make(map[string]struct{}, len(roots))
	for _, r := range roots {
		live[r.root.KeyString()] = struct{}{}
	}
	for k := range s.proofs {
		if _, ok := live[k]; !ok {
			delete(s.proofs, k)
		}
	}
}

// due is the roots this pass proves: every one not settled under this placement and
// these references, or settled longer than reprove ago. The fewest proven go first,
// roots never proven before all, then the longest ago; at most repairBudget.
func (s *server) due(roots []rooted, view string, now time.Time) []rooted {
	var out []rooted
	for _, r := range roots {
		p, ok := s.proofs[r.root.KeyString()]
		if ok && p.settled && p.view == view && p.refs == len(r.refs) && now.Sub(p.at) < reprove {
			continue
		}
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b rooted) int {
		pa, okA := s.proofs[a.root.KeyString()]
		pb, okB := s.proofs[b.root.KeyString()]
		if okA != okB {
			if !okA {
				return -1
			}
			return 1
		}
		if c := cmp.Compare(pa.proven, pb.proven); c != 0 {
			return c
		}
		return pa.at.Compare(pb.at)
	})
	return out[:min(len(out), repairBudget)]
}

// repairRoot proves r on each holder placement names, giving it to one that cannot
// prove it; unless handed, every holder is given this node's references to it whether
// it proved the bytes or not, since a placement or reference set it has not been
// handed may have rows it lacks. A holder that cannot be asked is an error: it
// proves nothing and is given nothing, and the next pass asks it again. A node that
// holds no copy of a root placement does not name gives every holder its references,
// all it has to give.
func (s *server) repairRoot(ctx context.Context, r rooted, members []placement.Member, replicas int, handed bool) (proof, error) {
	holders, err := blockHolders(r.root.String(), members, replicas)
	if err != nil {
		return proof{}, err
	}
	p := proof{handed: true, holders: len(holders), named: slices.Contains(holders, s.self)}
	if p.named {
		p.holders--
	}
	var errs []error
	for _, h := range holders {
		if h == s.self {
			continue
		}
		held, err := s.prove(ctx, h, r.root)
		if errors.Is(err, ErrNotHeld) {
			if !p.named {
				return s.handOver(ctx, r, holders, p)
			}
			if err := s.recover(ctx, r, holders); err != nil {
				return p, err
			}
			held, err = s.prove(ctx, h, r.root)
		}
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("proving it on %s: %w", h, err))
			p.handed = p.handed && handed
			continue
		case held:
			p.proven++
			if handed {
				continue
			}
		}
		if err := s.give(ctx, h, r); err != nil {
			errs = append(errs, err)
			p.handed = false
		}
	}
	p.settled = p.proven == p.holders
	return p, errors.Join(errs...)
}

// handOver gives every holder this node's references to r, which it holds no copy
// of to prove theirs against.
func (s *server) handOver(ctx context.Context, r rooted, holders []string, p proof) (proof, error) {
	p.settled, p.bare, p.proven = true, true, 0
	var errs []error
	for _, h := range holders {
		if err := s.give(ctx, h, r); err != nil {
			errs = append(errs, err)
			p.handed = false
		}
	}
	return p, errors.Join(errs...)
}

// give asks holder to hold r's root, once for each of this node's references to it.
func (s *server) give(ctx context.Context, holder string, r rooted) error {
	ctx, cancel := context.WithTimeout(ctx, holdWait)
	defer cancel()
	for _, ref := range r.refs {
		if err := s.askHold(ctx, holder, r.root, ref.Address, ref.Name, ref.Stamp); err != nil {
			return fmt.Errorf("giving it to %s: %w", holder, err)
		}
	}
	return nil
}

// recover takes r's root back onto this node, a holder placement names that lost its
// copy, from the first other holder that can give it.
func (s *server) recover(ctx context.Context, r rooted, holders []string) error {
	var errs []error
	for _, h := range holders {
		if h == s.self {
			continue
		}
		from, err := libp2ppeer.Decode(h)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := s.takeBack(ctx, from, r); err != nil {
			errs = append(errs, fmt.Errorf("from %s: %w", h, err))
			continue
		}
		return nil
	}
	return fmt.Errorf("taking back the copy this node lost: %w", errors.Join(errs...))
}

func (s *server) takeBack(ctx context.Context, from libp2ppeer.ID, r rooted) error {
	ctx, cancel := context.WithTimeout(ctx, holdWait)
	defer cancel()
	for _, ref := range r.refs {
		if err := s.hold(ctx, r.root, from, ref.Address, ref.Name, ref.Stamp); err != nil {
			return err
		}
	}
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
