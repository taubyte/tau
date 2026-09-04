package storage

import (
	"fmt"

	"github.com/taubyte/tau/pkg/v2/placement"
)

// placement.go binds pkg/v2/placement to this service's two questions. There
// are TWO, they run over ONE member set, and their independence is the single
// structural decision that lets a bucket be larger than one node.
//
// The HRW itself is not here — it is pkg/v2/placement, because database and
// cache want the identical computation and because service-conventions.md
// names hand-rolled in-service placement as the thing not to port. What is
// here is the part that is storage's: which resource strings to name, and
// which membership timescale to ask.

// indexResource is the HRW resource string for a shard's index, and
// blocksResource is the HRW resource string for one object's bytes.
//
// THEY ARE TWO DIFFERENT NAMESPACES AND THAT IS THE POINT. Index placement and
// byte placement are separate HRW computations over one member set, so the
// nodes holding a bucket's index are unrelated to the nodes holding its
// objects. THAT IS THE ONLY REASON A BUCKET CAN BE LARGER THAN ONE NODE: the
// index for a bucket sits on R nodes, and the bytes spread over the whole
// fleet by their own CIDs.
//
// Granularity on the byte side is the OBJECT, not the block: one computation
// covers a four-thousand-block object, and a reader dials the placement set and
// lets bitswap walk the DAG from there.
func indexResource(shard uint16) string { return fmt.Sprintf("index/s%03d", shard) }

func blocksResource(rootCID string) (string, error) {
	if rootCID == "" {
		return "", fmt.Errorf("%w: an object with no root cid has no placement", ErrNotThisServicesRow)
	}
	return "blocks/" + rootCID, nil
}

// indexHolders is which nodes hold a shard's index.
//
// IT TAKES THE STABLE MEMBERSHIP, and every caller must pass the stable set
// rather than the live one. Placement on the live set is placement on a
// five-second timescale, which is what made v1 thrash: a node that misses four
// heartbeats would hand its share of the fleet's bytes to somebody else and
// take them back a second later.
func indexHolders(shard uint16, stable []placement.Member, r int) []string {
	return placement.Desired(indexResource(shard), stable, r)
}

// blockHolders is which nodes hold an object's bytes.
func blockHolders(rootCID string, stable []placement.Member, r int) ([]string, error) {
	res, err := blocksResource(rootCID)
	if err != nil {
		return nil, err
	}
	return placement.Desired(res, stable, r), nil
}
