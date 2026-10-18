package node

import (
	"github.com/taubyte/tau/pkg/specs"
	gateway "github.com/taubyte/tau/services/gateway"
)

// fixture_test.go is the smallest compiled project that exercises the whole
// path: one function, one gateway, one name.
//
// IT IS HAND-WRITTEN AND THAT IS THE ONE PLACE IT IS RIGHT TO BE. Every other
// test of this shape compiles the vocabulary's own fixture; here the point is
// the wiring, and a project small enough to read in one screen makes what the
// node did with it obvious.

const (
	projectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	secretID  = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy"
)

func compiled() []byte {
	return []byte(`{
		"object": {
			"id": "` + projectID + `",
			"name": "shop",
			"function": {
				"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY": {
					"name": "checkout",
					"execution": {"call": "handle", "memory": 33554432},
					"source": "services/checkout"
				}
			},
			"gateway": {
				"QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zk": {
					"name": "shop",
					"host": {"kind": "zone", "name": "shop", "zone": {"in": {"name": "QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL"}}},
					"routing": "route",
					"route": {
						"path": "/", "match": "prefix", "target": "handler",
						"handler": {"kind": "function", "name": "checkout"}
					}
				}
			},
			"zone": {
				"QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL": {
					"name": "example_com",
					"fqdn": "example.com"
				}
			}
		},
		"indexes": {
			"projects/` + projectID + `/function/checkout/links": [
				"branches/main/projects/` + projectID + `/function/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"],
			"projects/` + projectID + `/gateway/shop/links": [
				"branches/main/projects/` + projectID + `/gateway/QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zk"],
			"projects/` + projectID + `/zone/example_com/links": [
				"branches/main/projects/` + projectID + `/zone/QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL"]
		}
	}`)
}

func workerTarget() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"}
}

func gatewayConfig() gateway.Config { return gateway.Config{} }

// needsASecret is a project whose one function names a secret and nothing else.
// It is the ordinary shape of anything imported from a platform that had
// environment variables, and a test using it hands the node prebuilt() so that
// the one seam left to refuse is the secrets one.
func needsASecret() []byte {
	return []byte(`{
		"object": {
			"id": "` + projectID + `",
			"name": "shop",
			"function": {
				"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY": {
					"name": "checkout",
					"execution": {"call": "handle", "memory": 33554432},
					"source": "services/checkout",
					"variables": {
						"TOKEN": {"as": "env", "at": "run", "secret": {"name": "` + secretID + `"}}
					}
				}
			}
		},
		"indexes": {
			"projects/` + projectID + `/function/checkout/links": [
				"branches/main/projects/` + projectID + `/function/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"]
		}
	}`)
}

// needsBothSeams is a project whose one function is built by the platform and
// wants a secret — the two things a worker refuses to invent.
//
// IT IS NO LONGER THE ONLY FIXTURE THAT NEEDS THE ARTIFACT SEAM: every workload
// here does, because every source is a path in a repository and what came out of
// building one is nothing a compiled document holds. What this one still says on
// its own is BOTH refusals at once.
func needsBothSeams() []byte {
	return []byte(`{
		"object": {
			"id": "` + projectID + `",
			"name": "shop",
			"function": {
				"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY": {
					"name": "built",
					"execution": {"call": "handle", "memory": 33554432},
					"source": "services/built",
					"variables": {
						"TOKEN": {"as": "env", "at": "run", "secret": {"name": "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy"}}
					}
				}
			}
		},
		"indexes": {
			"projects/` + projectID + `/function/built/links": [
				"branches/main/projects/` + projectID + `/function/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"]
		}
	}`)
}
