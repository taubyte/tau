package app

import (
	"errors"
	"testing"

	"github.com/taubyte/tau/pkg/config"
	authService "github.com/taubyte/tau/services/auth"
)

type startCase struct {
	config.Config
	dev      bool
	services []string
	tenancy  config.Tenancy
}

func (c startCase) DevMode() bool           { return c.dev }
func (c startCase) Services() []string      { return c.services }
func (c startCase) Tenancy() config.Tenancy { return c.tenancy }

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

func TestConfigErrorIsFoundThroughWrapping(t *testing.T) {
	err := errors.Join(errors.New("starting"), ConfigError{authService.ErrNoTenancy})
	var ce ConfigError
	if !errors.As(err, &ce) || !errors.Is(err, authService.ErrNoTenancy) {
		t.Fatalf("lost the config error in %v", err)
	}
}
