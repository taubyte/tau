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

// checkoutID is the one function compiled() declares, and checkoutPackageID what it
// runs, the id the artifact seam is asked about.
const (
	checkoutID        = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"
	checkoutPackageID = "QmPUnSYosVzMuhisQ5c5KSRS4yD9qyx7erZ8LhFoGUUJkN"
)

// anArtifact is a cid for a fixture whose tier never reads the bytes. A test
// that wants a component to actually RUN puts one in the asset store and stages
// the id it came back with instead.
const anArtifact = "bafyanartifactnothingreads"

// stagedOn hands every node's artifact seam what a build would publish, keyed by
// resource id; every node, since placement chooses where a workload runs.
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
					"package": {"name": "` + checkoutPackageID + `"}
				}
			},
			"package": {
				"` + checkoutPackageID + `": {"name": "checkout_code", "type": "wasm", "source": "services/checkout"}
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
			"projects/` + projectID + `/package/checkout_code/links": [
				"branches/main/projects/` + projectID + `/package/` + checkoutPackageID + `"],
			"projects/` + projectID + `/function/checkout/links": [
				"branches/main/projects/` + projectID + `/function/` + checkoutID + `"],
			"projects/` + projectID + `/gateway/shop/links": [
				"branches/main/projects/` + projectID + `/gateway/QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zk"],
			"projects/` + projectID + `/zone/example_com/links": [
				"branches/main/projects/` + projectID + `/zone/` + zoneID + `"]
		}
	}`)
}

func TestAnUnstagedResourceIsRefusedByName(t *testing.T) {
	var held nodeDream.Built
	held.Publish("shop", "prod", map[string]string{"QmOne": "bafyone"})

	cid, err := held.Artifact(t.Context(), "shop", "prod", "function", "QmOne")
	require.NoError(t, err)
	require.Equal(t, "bafyone", cid)

	_, err = held.Artifact(t.Context(), "shop", "prod", "function", "QmOther")
	require.ErrorContains(t, err, "QmOther", "and it says which resource")

	// THE DEPLOYMENT IS PART OF THE KEY, the way it is in a cloud: two projects
	// in one universe are two sets of artifacts, and a resource id that happened
	// to be shared is not one artifact.
	_, err = held.Artifact(t.Context(), "market", "prod", "function", "QmOne")
	require.ErrorContains(t, err, "market")
}
