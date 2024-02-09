//go:build mock

package app

import (
	"github.com/taubyte/go-interfaces/services/seer"
)

func estimateGPSLocation() (*seer.Location, error) {
	return &seer.Location{
		Latitude:  32.78306,
		Longitude: -96.80667,
	}, nil
}
