package auth

import "github.com/taubyte/tau/core/services"

type Service interface {
	services.DBService
	services.GitHubAuth
}
