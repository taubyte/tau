package register

import (
	"context"
	"errors"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/reach"
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

// Reach is a register as the services on any instance ask it: this instance's when
// it runs one, otherwise a register discovery names.
type Reach struct{ r *reach.Reach[Fleet] }

var _ Fleet = (*Reach)(nil)

// NewReach builds one over h. local is this instance's register service, nil when it
// runs none; peers is every node known to run one.
func NewReach(h host.Host, local func() *Service, peers func() []libp2ppeer.ID) *Reach {
	own := func() (Fleet, bool) {
		if s := local(); s != nil {
			return s.Local(), true
		}
		return nil, false
	}
	dial := func(id libp2ppeer.ID) (Fleet, error) { return NewClient(h, id) }
	return &Reach{r: reach.New(h.ID(), own, peers, dial, isAnswer)}
}

// isAnswer is an error the register answered with, which any other would too.
func isAnswer(err error) bool {
	return errors.Is(err, pkgregister.ErrNotPublished) || errors.Is(err, pkgregister.ErrNoObject)
}

func (r *Reach) Close() error { return r.r.Close() }

func (r *Reach) ResolveFleet(ctx context.Context) (pkgregister.Pointer, error) {
	var p pkgregister.Pointer
	err := r.r.Do(ctx, func(f Fleet) error {
		var err error
		p, err = f.ResolveFleet(ctx)
		return err
	})
	return p, err
}

func (r *Reach) Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error) {
	var out []byte
	err := r.r.Do(ctx, func(f Fleet) error {
		var err error
		out, err = f.Object(ctx, id)
		return err
	})
	return out, err
}
