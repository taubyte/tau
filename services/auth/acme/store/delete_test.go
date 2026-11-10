package store

import (
	"context"
	"errors"
	"testing"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"golang.org/x/crypto/acme/autocert"
	"gotest.tools/v3/assert"
)

// fakeAuth keeps certificates by fqdn and cache entries by key, like the auth acme handler.
type fakeAuth struct {
	certs, cache map[string][]byte
	actions      []string
	old          bool // answers like an auth node without the "delete" action
}

func (f *fakeAuth) Send(cmd string, body command.Body, peers ...peerCore.ID) (cr.Response, error) {
	action := body["action"].(string)
	f.actions = append(f.actions, action)
	switch action {
	case "set":
		f.certs[body["fqdn"].(string)] = body["certificate"].([]byte)
	case "cache-set":
		f.cache[body["key"].(string)] = body["data"].([]byte)
	case "get":
		if c, ok := f.certs[body["fqdn"].(string)]; ok {
			return cr.Response{"certificate": c}, nil
		}
		return nil, autocert.ErrCacheMiss
	case "cache-delete":
		delete(f.cache, body["key"].(string))
	case "delete":
		if f.old {
			return nil, errors.New("Acme action `delete` not recognized")
		}
		delete(f.certs, body["fqdn"].(string))
	default:
		return nil, autocert.ErrCacheMiss
	}
	return nil, nil
}

func newFakeStore(t *testing.T) (*Store, *fakeAuth) {
	ctx := context.Background()
	s, err := New(ctx, peer.Mock(ctx), t.TempDir())
	assert.NilError(t, err)
	t.Cleanup(func() { s.Close() })
	f := &fakeAuth{certs: map[string][]byte{}, cache: map[string][]byte{}}
	s.client = f
	return s, f
}

func TestStore_Delete_RemovesStoredWildcard(t *testing.T) {
	defer func(d time.Duration) { CertRetryDuration = d }(CertRetryDuration)
	CertRetryDuration = 0

	ctx := context.Background()
	s, _ := newFakeStore(t)

	assert.NilError(t, s.Put(ctx, "*.gen.example.com", []byte("staging-cert")))
	got, err := s.Get(ctx, "a.gen.example.com")
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []byte("staging-cert"))

	assert.NilError(t, s.Delete(ctx, "*.gen.example.com"))
	_, err = s.Get(ctx, "b.gen.example.com")
	assert.Equal(t, err, autocert.ErrCacheMiss)
}

func TestStore_Delete_MissingCertIsNotError(t *testing.T) {
	s, _ := newFakeStore(t)
	assert.NilError(t, s.Delete(context.Background(), "nothing.example.com"))
}

func TestStore_Delete_OldAuthNodeErrors(t *testing.T) {
	s, f := newFakeStore(t)
	f.old = true
	assert.ErrorContains(t, s.Delete(context.Background(), "*.gen.example.com"), "not recognized")
}

func TestStore_Delete_KeysUnchanged(t *testing.T) {
	ctx := context.Background()
	s, f := newFakeStore(t)
	assert.NilError(t, s.Delete(ctx, "example.com"))
	assert.DeepEqual(t, f.actions, []string{"cache-delete", "delete"})

	f.actions = nil
	assert.NilError(t, s.Delete(ctx, "example.com+token"))
	assert.Equal(t, len(f.actions), 0) // keys/tokens were never deleted remotely; unchanged
}
