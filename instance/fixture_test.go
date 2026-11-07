package instance

import (
	"github.com/taubyte/tau/pkg/specs"
	gateway "github.com/taubyte/tau/services/gateway"
)

// The smallest compiled project exercising the whole path, hand-written so the
// wiring is readable in one screen.

const (
	projectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	secretID  = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Xy"

	// codePackageID is the package every function here runs, and imagePackageID the
	// image every service does.
	codePackageID  = "QmUAdPDpqX8h9iMcn2ionxAkptmvD5bGqzMrRWHAcpU9fg"
	imagePackageID = "QmUAdPDpqX8h9iMcn2ionxAkptmvD5bGqzMrRWHAcpU9fh"
)

func compiled() []byte {
	return []byte(`{
		"object": {
			"id": "` + projectID + `",
			"name": "shop",
			"package": {
				"` + codePackageID + `": {"name": "code", "type": "wasm"}
			},
			"function": {
				"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY": {
					"name": "checkout",
					"execution": {"call": "handle", "memory": 33554432},
					"package": {"name": "` + codePackageID + `"}
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
			"projects/` + projectID + `/package/code/links": [
				"branches/main/projects/` + projectID + `/package/` + codePackageID + `"],
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

// builtByThePlatform is a project whose one function runs a package the platform
// builds, so the only seam it needs is the plm service's.
func builtByThePlatform() []byte {
	return []byte(`{
		"object": {
			"id": "` + projectID + `",
			"name": "shop",
			"package": {
				"` + codePackageID + `": {"name": "code", "type": "wasm"}
			},
			"function": {
				"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY": {
					"name": "built",
					"execution": {"call": "handle", "memory": 33554432},
					"package": {"name": "` + codePackageID + `"}
				}
			}
		},
		"indexes": {
			"projects/` + projectID + `/package/code/links": [
				"branches/main/projects/` + projectID + `/package/` + codePackageID + `"],
			"projects/` + projectID + `/function/built/links": [
				"branches/main/projects/` + projectID + `/function/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"]
		}
	}`)
}
