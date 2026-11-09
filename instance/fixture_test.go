package instance

import (
	"github.com/taubyte/tau/pkg/specs"
)

// The smallest compiled project exercising the whole path, hand-written so the
// wiring is readable in one screen. Its workload is a service on an image, which
// somebody else built, so nothing has to build it for a node to resolve it.

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
				"` + imagePackageID + `": {"name": "image", "type": "oci",
					"source": {"kind": "image", "image": "ghcr.io/acme/checkout:1"}}
			},
			"service": {
				"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY": {
					"name": "checkout",
					"package": {"name": "` + imagePackageID + `"},
					"ports": {"http": {"number": 8080}}
				}
			},
			"gateway": {
				"QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zk": {
					"name": "shop",
					"host": {"kind": "zone", "name": "shop", "zone": {"in": {"name": "QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL"}}},
					"routing": "route",
					"route": {
						"path": "/", "match": "prefix", "target": "handler",
						"handler": {"kind": "service", "name": "checkout", "port": "http"}
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
			"projects/` + projectID + `/package/image/links": [
				"branches/main/projects/` + projectID + `/package/` + imagePackageID + `"],
			"projects/` + projectID + `/service/checkout/links": [
				"branches/main/projects/` + projectID + `/service/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"],
			"projects/` + projectID + `/gateway/shop/links": [
				"branches/main/projects/` + projectID + `/gateway/QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zk"],
			"projects/` + projectID + `/zone/example_com/links": [
				"branches/main/projects/` + projectID + `/zone/QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL"]
		}
	}`)
}

func workerTarget() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "service", Name: "checkout"}
}

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
