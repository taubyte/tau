package helpers

import "github.com/taubyte/tau/core/services/patrick"

type Repository struct {
	ID       int
	Name     string
	HookInfo patrick.Meta
	HookId   int64
	URL      string
}
