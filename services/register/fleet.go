package register

import (
	"context"
	"errors"
	"time"

	"github.com/taubyte/tau/pkg/kvdb"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// Fleet is the slice of a register that reads the cloud's own document.
type Fleet interface {
	ResolveFleet(ctx context.Context) (pkgregister.Pointer, error)
	Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error)
}

var (
	_ Fleet = (*pkgregister.Register)(nil)
	_ Fleet = (*Client)(nil)
)

// Retention is the T the cloud's fleet document gives its replicated databases: 0,
// the default, while nothing is published.
func Retention(ctx context.Context, f Fleet) (time.Duration, error) {
	p, err := f.ResolveFleet(ctx)
	if errors.Is(err, pkgregister.ErrNotPublished) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	doc, err := f.Object(ctx, p.Object)
	if err != nil {
		return 0, err
	}
	return kvdb.RetentionOf(doc, true)
}
