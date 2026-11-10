package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"

	storagev2 "github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2"
)

// Store is the store as a caller asks it: *Service answers in this process, *Client
// over the wire, and *Reach picks one of them.
type Store interface {
	// Put stores content as the next version of name, and returns once the
	// cloud's barrier of nodes hold it.
	Put(ctx context.Context, at Address, name string, o Object, content io.Reader) (Version, error)

	// Current is the newest version of name.
	Current(ctx context.Context, at Address, name string) (Version, error)

	// Versions is name's versions, newest first; a limit of zero is all of them.
	Versions(ctx context.Context, at Address, name string, limit int) ([]Version, error)

	// List is the current version of every live name beginning with prefix that
	// sorts after after, in name order; a limit of zero is all of them.
	List(ctx context.Context, at Address, prefix, after string, limit int) ([]Version, error)

	// Delete writes a tombstone over name.
	Delete(ctx context.Context, at Address, name string) (Version, error)

	// Size is the bytes the instance holds: the current version of every name, and
	// every older one its bucket keeps, none expired.
	Size(ctx context.Context, at Address) (uint64, error)

	// Read is the bytes under root, from whichever node holds them.
	Read(ctx context.Context, root string) (io.ReadCloser, error)
}

var (
	_ Store = (*server)(nil)
	_ Store = (*Client)(nil)
)

// Object is what a put says about its bytes.
type Object struct {
	ContentType string
	// Metadata is the caller's own name/value pairs.
	Metadata map[string]string
	// Versioned is a bucket that keeps every version: this one is kept once a
	// later one supersedes it, rather than collected.
	Versioned bool
	// Expire is how long the version lives, its bucket's lifecycle.expire, kept
	// or not; zero is until it is superseded or deleted.
	Expire time.Duration
}

func (s *server) Put(ctx context.Context, at Address, name string, o Object, content io.Reader) (Version, error) {
	w, err := s.put(ctx, at, name, o, content)
	return w.version, err
}

func (s *server) Current(ctx context.Context, at Address, name string) (Version, error) {
	return s.current(ctx, at, name, false, false)
}

func (s *server) Versions(ctx context.Context, at Address, name string, limit int) ([]Version, error) {
	return s.versions(ctx, at, name, limit, false)
}

func (s *server) List(ctx context.Context, at Address, prefix, after string, limit int) ([]Version, error) {
	return s.list(ctx, at, prefix, after, limit, false, false)
}

func (s *server) Delete(ctx context.Context, at Address, name string) (Version, error) {
	return s.remove(ctx, at, name)
}

func (s *server) Size(ctx context.Context, at Address) (uint64, error) {
	return s.size(ctx, at, false)
}

// size is the bytes an instance holds: the most any copy a read hears says, since a
// copy behind holds less. Here, it is this node's copy's alone.
func (s *server) size(ctx context.Context, a Address, here bool) (uint64, error) {
	if here {
		var out uint64
		err := s.copyHere(ctx, a, func(i index) error {
			n, err := i.Size(ctx, a)
			out = n
			return err
		})
		return out, err
	}
	sizes, err := fromCopies(ctx, s, a, func(i index) (uint64, error) {
		return i.Size(ctx, a)
	}, func(svc storagev2Client) (uint64, error) {
		res, err := svc.Size(ctx, connect.NewRequest(&storagev2.SizeRequest{At: wireAddress(a), Here: true}))
		if err != nil {
			return 0, err
		}
		return res.Msg.GetBytes(), nil
	})
	if errors.Is(err, ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return slices.Max(sizes), nil
}

func (s *server) Read(ctx context.Context, root string) (io.ReadCloser, error) {
	c, err := cid.Decode(root)
	if err != nil {
		return nil, fmt.Errorf("%w: %q is not a root: %w", ErrNotFound, root, err)
	}
	return s.open(ctx, c, false)
}

// list is the live names of an instance beginning with prefix and sorting after
// after, in order, the newest each the copies a read hears hold (quorum.go). Here, it
// is this node's copy's alone; with newest too, the newest row of each name,
// tombstones and expired versions included.
func (s *server) list(ctx context.Context, a Address, prefix, after string, limit int, here, newest bool) ([]Version, error) {
	if here {
		var out []Version
		err := s.copyHere(ctx, a, func(i index) error {
			var err error
			if newest {
				out, err = i.NewestRows(ctx, a, prefix, after, limit)
			} else {
				out, err = i.List(ctx, a, prefix, after, limit)
			}
			return err
		})
		return out, err
	}
	var out []Version
	for {
		pages, err := fromCopies(ctx, s, a, func(i index) ([]Version, error) {
			return i.NewestRows(ctx, a, prefix, after, limit)
		}, func(svc storagev2Client) ([]Version, error) {
			res, err := svc.List(ctx, connect.NewRequest(&storagev2.ListRequest{
				At: wireAddress(a), Prefix: []byte(prefix), After: []byte(after), Limit: uint32(limit), Here: true, Newest: true,
			}))
			if err != nil {
				return nil, err
			}
			return versionsFromWire(a, "", res.Msg.GetVersions())
		})
		if errors.Is(err, ErrNotFound) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		live, bound, more := mergePages(pages, limit, s.now())
		s.observe(live...)
		out = append(out, live...)
		if limit > 0 && len(out) >= limit {
			return out[:limit], nil
		}
		if !more {
			return out, nil
		}
		after = bound
	}
}

// open is the bytes under root: this node's when it has them, otherwise a holder's
// streamed through. A request that says here never leaves this node.
func (s *server) open(ctx context.Context, root cid.Cid, here bool) (io.ReadCloser, error) {
	held, err := s.bytes.Has(ctx, root)
	if err != nil {
		return nil, err
	}
	if held {
		r, err := s.bytes.Read(ctx, root)
		if err == nil || here {
			return r, err
		}
		// A COPY HERE THAT CANNOT BE READ IS NOT THE ANSWER while a holder's can be.
		s.logf("storage: reading %s here: %v; asking its holders", root, err)
	}
	if here {
		return nil, fmt.Errorf("%w: %s is not held here", ErrNotFound, root)
	}

	replicas, _, err := s.promise(ctx)
	if err != nil {
		return nil, err
	}
	holders, err := blockHolders(root.String(), s.placement(), replicas)
	if err != nil {
		return nil, err
	}
	var errs []error
	missing := 0
	for _, holder := range holders {
		if holder == s.self {
			continue
		}
		svc, err := s.peer(holder)
		if err == nil {
			var r io.ReadCloser
			if r, err = readFrom(ctx, svc, root.String(), true); err == nil {
				return r, nil
			}
		}
		if errors.Is(err, ErrNotFound) {
			missing++
		}
		errs = append(errs, fmt.Errorf("%s: %w", holder, err))
	}
	if missing == len(errs) {
		return nil, fmt.Errorf("%w: no holder has %s", ErrNotFound, root)
	}
	return nil, fmt.Errorf("storage: no holder of %s answered: %w", root, errors.Join(errs...))
}

// readFrom opens a node's stream of root and waits for its first answer, so a node
// that does not have it is told apart from one that does.
func readFrom(ctx context.Context, svc storagev2Client, root string, here bool) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	stream, err := svc.Read(ctx, connect.NewRequest(&storagev2.ReadRequest{RootCid: root, Here: here}))
	if err != nil {
		cancel()
		return nil, unwire(err)
	}
	r := &streamed{stream: stream, cancel: cancel}
	if !stream.Receive() {
		err := stream.Err()
		r.Close()
		if err != nil {
			return nil, unwire(err)
		}
		r.done = true
		return r, nil
	}
	r.buf = stream.Msg().GetChunk()
	return r, nil
}

// streamed is a Read's chunks as one reader.
type streamed struct {
	stream *connect.ServerStreamForClient[storagev2.ReadResponse]
	cancel context.CancelFunc
	buf    []byte
	done   bool
}

func (r *streamed) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.done || !r.stream.Receive() {
			r.done = true
			if err := r.stream.Err(); err != nil {
				return 0, unwire(err)
			}
			return 0, io.EOF
		}
		r.buf = r.stream.Msg().GetChunk()
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func (r *streamed) Close() error {
	r.cancel()
	return r.stream.Close()
}
