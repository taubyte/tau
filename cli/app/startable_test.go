//go:build !ee

package app

import (
	"errors"
	"testing"

	"github.com/taubyte/tau/pkg/config"
	authService "github.com/taubyte/tau/services/auth"
)

// A node with auth and no tenancy failed only when auth started, after the
// other services, and systemd restarted it every second. It is refused up front.
func TestStartRefusesAuthWithoutTenancy(t *testing.T) {
	owned := config.Tenancy{Owner: "ZaouiAmine"}
	for _, c := range []struct {
		name string
		cnf  startCase
		want error
	}{
		{"auth, no tenancy", startCase{services: []string{"seer", "auth"}}, authService.ErrNoTenancy},
		{"auth, tenancy", startCase{services: []string{"auth"}, tenancy: owned}, nil},
		{"dev, no tenancy", startCase{dev: true, services: []string{"auth"}}, nil},
		{"no auth", startCase{services: []string{"seer", "monkey"}}, nil},
	} {
		if got := checkStartable(c.cnf); !errors.Is(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
