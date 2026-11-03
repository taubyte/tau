package storage

import (
	"context"
	"errors"
	"io"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/reach"
)

// Reach is the store as the services on any instance ask it: this instance's storage
// service when it runs one, otherwise a storage node discovery names. Any storage
// node takes any request and forwards what it does not hold.
type Reach struct{ r *reach.Reach[Store] }

var _ Store = (*Reach)(nil)

// NewReach builds one over h. local is this instance's service when it runs one;
// peers is every node known to run storage.
func NewReach(h host.Host, local func() (Store, bool), peers func() []libp2ppeer.ID) *Reach {
	dial := func(id libp2ppeer.ID) (Store, error) { return NewClient(h, id) }
	return &Reach{r: reach.New(h.ID(), local, peers, dial, answered)}
}

// answered is an error that is the store's answer, which another storage node would
// give too.
func answered(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnderHeld) ||
		connect.CodeOf(err) == connect.CodeInvalidArgument
}

func (r *Reach) Close() error { return r.r.Close() }

// Put is asked of one node only once it has read any of content: the bytes it took
// cannot be offered to another.
func (r *Reach) Put(ctx context.Context, at Address, name string, o Object, content io.Reader) (Version, error) {
	counted := &countingReader{r: content}
	var v Version
	err := r.r.Do(ctx, func(s Store) error {
		var err error
		v, err = s.Put(ctx, at, name, o, counted)
		if err != nil && counted.n > 0 {
			return reach.Final(err)
		}
		return err
	})
	return v, err
}

func (r *Reach) Current(ctx context.Context, at Address, name string) (Version, error) {
	var v Version
	err := r.r.Do(ctx, func(s Store) error {
		var err error
		v, err = s.Current(ctx, at, name)
		return err
	})
	return v, err
}

func (r *Reach) Versions(ctx context.Context, at Address, name string, limit int) ([]Version, error) {
	var vs []Version
	err := r.r.Do(ctx, func(s Store) error {
		var err error
		vs, err = s.Versions(ctx, at, name, limit)
		return err
	})
	return vs, err
}

func (r *Reach) List(ctx context.Context, at Address, prefix, after string, limit int) ([]Version, error) {
	var vs []Version
	err := r.r.Do(ctx, func(s Store) error {
		var err error
		vs, err = s.List(ctx, at, prefix, after, limit)
		return err
	})
	return vs, err
}

func (r *Reach) Delete(ctx context.Context, at Address, name string) (Version, error) {
	var v Version
	err := r.r.Do(ctx, func(s Store) error {
		var err error
		v, err = s.Delete(ctx, at, name)
		return err
	})
	return v, err
}

func (r *Reach) Read(ctx context.Context, root string) (io.ReadCloser, error) {
	var out io.ReadCloser
	err := r.r.Do(ctx, func(s Store) error {
		var err error
		out, err = s.Read(ctx, root)
		return err
	})
	return out, err
}
