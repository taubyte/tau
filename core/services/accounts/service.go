package accounts

import "github.com/taubyte/tau/core/services"

// Service is what the accounts process implements. It satisfies the standard
// services.DBService for KV access and exposes the accounts Client surface
// in-process.
type Service interface {
	services.DBService
	Client() Client
}
