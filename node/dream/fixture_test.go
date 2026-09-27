//go:build dreaming

package dream_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/node/dream"
)

// The smallest compiled project that exercises the whole path: one function,
// one gateway, one name.
const projectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"

// zoneID is the project's own zone, `example.com`. It is a constant because
// more than one fixture in this package puts a name in it.
const zoneID = "QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL"

// checkoutID is the one function compiled() declares. It is a constant because
// it is what the ARTIFACT SEAM is asked about for it: a resource id is what a
// build publishes under, and a test that stages a component has to say which
// resource runs it.
const checkoutID = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"

// anArtifact is a cid for a fixture whose tier never reads the bytes. A test
// that wants a component to actually RUN puts one in the asset store and stages
// the id it came back with instead.
const anArtifact = "bafyanartifactnothingreads"

// stagedOn hands every node's artifact seam what a build would have published:
// which component each resource runs, keyed by resource id.
//
// A UNIVERSE RUNS NO BUILD SERVICE, and this is where that shows. It is not
// written into the compiled document — see nodeDream.Built for the bug that came
// of doing it there — and it goes on EVERY node because placement chooses which
// one runs a workload.
func stagedOn(t *testing.T, pids []string, project string, byResource map[string]string) {
	t.Helper()

	for _, id := range pids {
		held, ok := nodeDream.Artifacts(id)
		require.True(t, ok, "no node on %s", id)
		held.Publish(project, "prod", byResource)
	}
}

func compiled() []byte {
	return []byte(`{
		"object": {
			"id": "` + projectID + `",
			"name": "shop",
			"function": {
				"` + checkoutID + `": {
					"name": "checkout",
					"execution": {"call": "handle", "memory": 33554432},
					"source": "services/checkout"
				}
			},
			"gateway": {
				"QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zk": {
					"name": "shop",
					"host": {"kind": "zone", "name": "shop", "zone": {"in": {"name": "` + zoneID + `"}}},
					"routing": "route",
					"route": {
						"path": "/", "match": "prefix", "target": "handler",
						"handler": {"kind": "function", "name": "checkout"}
					}
				}
			},
			"zone": {
				"` + zoneID + `": {
					"name": "example_com",
					"fqdn": "example.com"
				}
			}
		},
		"indexes": {
			"projects/` + projectID + `/function/checkout/links": [
				"branches/main/projects/` + projectID + `/function/` + checkoutID + `"],
			"projects/` + projectID + `/gateway/shop/links": [
				"branches/main/projects/` + projectID + `/gateway/QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zk"],
			"projects/` + projectID + `/zone/example_com/links": [
				"branches/main/projects/` + projectID + `/zone/` + zoneID + `"]
		}
	}`)
}
