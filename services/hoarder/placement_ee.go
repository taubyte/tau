//go:build ee

package hoarder

import (
	"github.com/taubyte/tau/ee/services/hoarder/placement"
)

// placementDesired for the ee build: deterministic HRW over live members (the
// same contract as placement.go). Implementation lives in the ee submodule.
func placementDesired(instanceHash string, members []string, target int) []string {
	return placement.Desired(instanceHash, members, target)
}
