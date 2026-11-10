//go:build ee

package app

import "testing"

func TestStartAllowsAuthWithoutTenancy(t *testing.T) {
	if err := checkStartable(startCase{services: []string{"auth"}}); err != nil {
		t.Fatalf("EE auth does not require a tenancy: %v", err)
	}
}
