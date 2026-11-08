package storage

import (
	"fmt"

	"github.com/taubyte/tau/pkg/placement"
)

// Binds pkg/placement to storage's question over one member set, the storage nodes
// discovery finds: which resource strings to name.

// indexResource is the HRW resource string for a shard's index, and blocksResource
// for one object's bytes. Two namespaces, so a bucket's index holders are unrelated
// to its byte holders and a bucket can exceed one node. Byte placement is per
// object; bitswap walks the DAG from the placement set.
func indexResource(shard uint16) string { return fmt.Sprintf("index/s%03d", shard) }

func blocksResource(rootCID string) (string, error) {
	if rootCID == "" {
		return "", fmt.Errorf("%w: an object with no root cid has no placement", ErrNotThisServicesRow)
	}
	return "blocks/" + rootCID, nil
}

// indexHolders is which nodes hold a shard's index.
func indexHolders(shard uint16, members []placement.Member, r int) []string {
	return placement.Desired(indexResource(shard), members, r)
}

// blockHolders is which nodes hold an object's bytes.
func blockHolders(rootCID string, members []placement.Member, r int) ([]string, error) {
	res, err := blocksResource(rootCID)
	if err != nil {
		return nil, err
	}
	return placement.Desired(res, members, r), nil
}
