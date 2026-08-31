package helpers

import (
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/pkg/git/repokey"
)

type Repository struct {
	ID       int
	Name     string
	HookInfo patrick.Meta
	HookId   int64
	URL      string
}

// Key is how auth names this repository. It is derived through repokey rather
// than written out, so a fixture cannot spell an identity the service would not
// have produced from the same clone URL.
//
// ID is still here because the parts of the fixture that speak to patrick — the
// webhook payload and the TNS wait — are still on the numeric id patrick sends.
func (r Repository) Key() repokey.Key {
	key, err := repokey.Parse("https://github.com/" + GitUser + "/" + r.Name)
	if err != nil {
		panic(err) // a fixture with an unparseable name is a broken fixture
	}
	return key
}
