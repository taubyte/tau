package storage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"

	"github.com/taubyte/tau/pkg/placement"
	storagev2 "github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2"
)

// Collection. Each pass, for references this node holds, it asks the holders of the
// version's index shard what their copies hold of the version (Standing), and drops
// the reference once the version is superseded or abandoned past the grace window
// (gc.go); the sweep then frees what no reference reaches. A version found live is
// asked about again after reask, at most collectBudget of them a pass, the longest
// unasked first. A version found superseded is never asked about again: rows are never
// removed, so it stays superseded, and its reference waits out the grace window here.

const (
	// reask is how long a version found live goes before its holders are asked
	// about it again.
	reask = time.Hour

	// collectBudget is how many versions one pass asks about.
	collectBudget = 256

	// standingWait bounds asking one index holder.
	standingWait = 10 * time.Second

	// resweep is how often a node sweeps having dropped nothing: what a crash
	// left indexed and unreferenced goes within it.
	resweep = time.Hour
)

// collect is one pass: it drops every reference whose gates open, and owes a sweep
// when it dropped one.
func (s *server) collect(ctx context.Context) error {
	s.collectMu.Lock()
	defer s.collectMu.Unlock()
	replicas, barrier, err := s.promise(ctx)
	if err != nil {
		return err
	}
	refs, err := s.local.References(ctx)
	if err != nil {
		return err
	}
	members := s.placement()
	now := s.now()
	if s.asked == nil {
		s.asked = map[string]time.Time{}
	}
	live := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		live[r.key] = struct{}{}
	}
	for k := range s.asked {
		if _, ok := live[k]; !ok {
			delete(s.asked, k)
		}
	}

	asks := 0
	var errs []error
	for _, r := range s.unaskedFirst(refs) {
		superseded, absent, err := s.local.Observed(ctx, r.Address, r.Name, r.Stamp)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		g := Gates{SupersededAt: superseded, AbsentAt: absent}
		purpose := Superseded
		if superseded.IsZero() {
			if at, ok := s.asked[r.key]; (ok && now.Sub(at) < reask) || asks == collectBudget {
				continue
			}
			asks++
			s.asked[r.key] = now
			if g, err = s.ask(ctx, r, members, replicas, barrier, g); err != nil {
				errs = append(errs, fmt.Errorf("%s %q at %d: %w", r.Address, r.Name, r.Stamp, err))
				continue
			}
			if g.SupersededAt.IsZero() {
				purpose = Abandoned
			}
		}
		if g.Open(purpose, now, s.cfg.GraceGC, 0) != nil {
			continue
		}
		if err := s.dropReference(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// unaskedFirst orders references never asked about first, then the longest unasked.
func (s *server) unaskedFirst(refs []Reference) []Reference {
	out := slices.Clone(refs)
	slices.SortStableFunc(out, func(a, b Reference) int { return s.asked[a.key].Compare(s.asked[b.key]) })
	return out
}

// ask has the holders of r's shard say what they hold of its version, and records
// what that settles: superseded once as many as a write is acknowledged on hold a
// newer row, or say it expired, and none says it is kept and live; absent while every
// holder answers and none holds its row; present, forgetting absent, once one holds
// it.
func (s *server) ask(ctx context.Context, r Reference, members []placement.Member, replicas, barrier int, g Gates) (Gates, error) {
	shard, err := shardOf(r.Address.Bucket())
	if err != nil {
		return g, err
	}
	holders := indexHolders(shard, members, replicas)
	answers := s.standings(ctx, r, holders)
	need := max(1, min(barrier, len(holders)))
	superseded, held, answered := 0, false, 0
	for _, a := range answers {
		if a == nil {
			continue
		}
		answered++
		if !a.held {
			continue
		}
		held = true
		switch {
		case a.expired:
			superseded++
		case a.kept:
			return g, s.local.Present(ctx, r.Address, r.Name, r.Stamp)
		case a.superseded:
			superseded++
		}
	}
	switch {
	case superseded >= need:
		g.SupersededAt, err = s.local.Superseded(ctx, r.Address, r.Name, r.Stamp)
	case held:
		err = s.local.Present(ctx, r.Address, r.Name, r.Stamp)
	case answered == len(holders) && answered > 0:
		g.AbsentNow = true
		g.AbsentAt, err = s.local.Absent(ctx, r.Address, r.Name, r.Stamp)
	}
	return g, err
}

// standings asks every holder at once, each within standingWait; a holder that
// cannot answer is nil.
func (s *server) standings(ctx context.Context, r Reference, holders []string) []*standing {
	out := make([]*standing, len(holders))
	done := make(chan struct{}, len(holders))
	for i, h := range holders {
		go func() {
			defer func() { done <- struct{}{} }()
			actx, cancel := context.WithTimeout(ctx, standingWait)
			defer cancel()
			st, err := s.standingOn(actx, h, r)
			if err != nil {
				s.logf("storage: asking %s what it holds of %s %q: %v", h, r.Address, r.Name, err)
				return
			}
			out[i] = &st
		}()
	}
	for range holders {
		<-done
	}
	return out
}

func (s *server) standingOn(ctx context.Context, holder string, r Reference) (standing, error) {
	if holder == s.self {
		return s.answerSuperseded(ctx, r.Address, r.Name, r.Stamp, r.Root)
	}
	svc, err := s.peer(holder)
	if err != nil {
		return standing{}, err
	}
	res, err := svc.Superseded(ctx, connect.NewRequest(&storagev2.SupersededRequest{
		At: wireAddress(r.Address), Name: []byte(r.Name), Stamp: r.Stamp, RootCid: r.Root.String(),
	}))
	if err != nil {
		return standing{}, err
	}
	return standing{held: res.Msg.GetHeld(), superseded: res.Msg.GetSuperseded(), kept: res.Msg.GetKept(), expired: res.Msg.GetExpired()}, nil
}

// answerSuperseded is what this node's copy of the instance's index holds of a
// version. Only a shard already open answers: one opened for the question would hold
// nothing yet, and say so.
func (s *server) answerSuperseded(ctx context.Context, a Address, name string, stamp uint64, root cid.Cid) (standing, error) {
	i, release, ok, err := s.shards.hold(ctx, a.Bucket(), false)
	if err != nil {
		return standing{}, err
	}
	if !ok {
		return standing{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("storage: this node does not hold %s's index open", a))
	}
	defer release()
	i.at = s.now()
	return i.Standing(ctx, a, name, stamp, root.String())
}

// dropReference drops one reference and what was found of its version, and owes a
// sweep.
func (s *server) dropReference(ctx context.Context, r Reference) error {
	if err := s.local.Dereference(ctx, r.Root, r.Address, r.Name, r.Stamp); err != nil {
		return err
	}
	s.owed.Store(true)
	delete(s.asked, r.key)
	return s.local.Collected(ctx, r.Address, r.Name, r.Stamp)
}

// sweepIfDue sweeps when a reference was dropped since the last sweep, or resweep
// has passed.
func (s *server) sweepIfDue(ctx context.Context) error {
	s.collectMu.Lock()
	defer s.collectMu.Unlock()
	if !s.owed.Load() && s.now().Sub(s.swept) < resweep {
		return nil
	}
	s.owed.Store(false)
	res, err := s.sweeper.Sweep(ctx)
	if err != nil {
		s.owed.Store(true)
		return err
	}
	s.swept = s.now()
	if res.Deleted > 0 || len(res.Incomplete) > 0 {
		s.logf("storage: swept %d blocks of %d indexed; %d roots could not be walked here", res.Deleted, res.Indexed, len(res.Incomplete))
	}
	return nil
}
