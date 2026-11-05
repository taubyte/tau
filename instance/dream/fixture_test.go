//go:build dreaming

package dream_test

// The smallest compiled project that exercises the whole path: one function,
// one gateway, one name.
const projectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"

// zoneID is the project's own zone, `example.com`. It is a constant because
// more than one fixture in this package puts a name in it.
const zoneID = "QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL"

// checkoutID is the one function compiled() declares, and checkoutPackageID what it
// runs.
const (
	checkoutID        = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"
	checkoutPackageID = "QmPUnSYosVzMuhisQ5c5KSRS4yD9qyx7erZ8LhFoGUUJkN"
)

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
