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

func TestConfigErrorIsFoundThroughWrapping(t *testing.T) {
	err := errors.Join(errors.New("starting"), ConfigError{authService.ErrNoTenancy})
	var ce ConfigError
	if !errors.As(err, &ce) || !errors.Is(err, authService.ErrNoTenancy) {
		t.Fatalf("lost the config error in %v", err)
	}
}
