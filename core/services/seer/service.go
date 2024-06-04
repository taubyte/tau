package seer

import (
	"context"

	"github.com/taubyte/tau/core/services"
)

type Service interface {
	services.DBService
	services.GitHubAuth
	Resolver() Resolver
}

type Resolver interface {
	LookupTXT(context.Context, string) ([]string, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
}
