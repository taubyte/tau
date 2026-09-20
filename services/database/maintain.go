package database

import (
	"bytes"
	"context"

	"github.com/taubyte/tau/pkg/placement"
)

// maintain.go is what a node does about a database whose placement moved.
//
// # Two decisions, and only one of them can lose anything
//
// ACQUIRE is opening the database. On a store whose writes converge that is the
// whole of it: opening subscribes to that database's own topic and the rows
// arrive. There is nothing to get wrong, and nothing to gate.
//
// RELEASE deletes the local copy, and it is the one that can cost the data. So
// it is gated on a fact and never on placement alone — see release.
//
// # Acquire is on demand, deliberately
//
// This loop does not go looking for databases to pick up. A node acquires one
// when something asks it for one, which is what already happens, and the reason
// that is safe is the gate below: nobody lets go until somebody else has it, so
// a database is never held by nobody while it waits to be asked for.
//
// Acquiring ahead of demand is an optimisation — it shortens the first request
// after a move — and it is not correctness. It is not here.

// digester asks one node what it holds for a database. It is the Digest call in
// services/database's own client; the loop takes it as a function so a test can
// answer for a fleet that does not exist.
type digester func(ctx context.Context, peer string, at address) (uint64, []byte, error)

// release reports whether this node may stop holding a database.
//
// IT IS THE ONLY THING THAT MAY AUTHORISE A DELETE ON A MOVE, and the argument
// for it is a failure it prevents rather than a property it has. Placement says
// who SHOULD hold a database. "Another node is alive and should hold this" is
// not the same statement as "another node HAS this", and the gap between them is
// where the data goes:
//
//	eu goes dark; an operator repins to us to restore service; us nodes are
//	alive and are now the desired holders, but their source is down so they
//	receive nothing; eu comes back, reads the new placement, and drops the
//	only copies that ever existed.
//
// So this asks. Equal digests mean that node holds the same keys with the same
// values, which for a store whose writes converge is a proof and not a sample.
//
// A NODE THAT CANNOT BE REACHED HAS NOT CONVERGED. An error is not a maybe: the
// answer to "may I delete the data" defaults to no, every time, for every reason.
//
// THE BAR IS THE FLOOR, OR THE REGION IF IT IS SMALLER. Requiring `replication`
// where the region holds fewer nodes than that would be a bar nothing can clear,
// and this node would hold the copy forever. Requiring all of a smaller set is
// the same promise the operator actually made: as many copies as there are nodes
// to put them on.
func release(ctx context.Context, at address, keys uint64, sum []byte, desired []string, replication int, ask digester) bool {
	if len(desired) == 0 {
		// NOBODY IS SUPPOSED TO HOLD THIS, which is not a move — it is a
		// database whose region no node serves, and placement.go returns that
		// empty rather than filling it from elsewhere. Letting go here would
		// delete a copy that nothing is replacing.
		return false
	}

	want := replication
	if len(desired) < want {
		want = len(desired)
	}

	converged := 0
	for _, peer := range desired {
		theirKeys, theirSum, err := ask(ctx, peer, at)
		if err != nil {
			continue
		}
		if theirKeys == keys && bytes.Equal(theirSum, sum) {
			converged++
		}
	}
	return converged >= want
}

// moved reports whether this node should still hold a database, and who should.
//
// The desired set EXCLUDES this node, because it is the set release asks: a node
// comparing its digest against its own would converge with itself and let go.
func moved(at address, byRegion map[string][]placement.Member, regions []string, replication int, me, region string) (desired []string, keep bool) {
	if holds(at, byRegion, regions, replication, me, region) {
		return nil, true
	}

	for _, pinned := range placementRegions(regions) {
		for _, id := range placement.Desired(resourceOf(at), membersIn(byRegion, pinned), replication) {
			if id != me {
				desired = append(desired, id)
			}
		}
	}
	return desired, false
}
