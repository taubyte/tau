package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"testing"
	"time"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/p2p/peer"
	kvdbMock "github.com/taubyte/tau/pkg/kvdb/mock"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

// fakeSrv is the substrate service: AddFile/Meta/delete only use Node().
type fakeSrv struct {
	storageIface.Service
	node peer.Node
}

func (f fakeSrv) Node() peer.Node { return f.node }

// localHoarder is a hoarder running in the same process as substrate. Its
// Stash does what services/hoarder/stream.go stashReceive does with the
// bytes: AddFileForCid into its node and reject a CID mismatch. The node is
// the substrate node, as when all services share cfg.Node().
type localHoarder struct {
	hoarderIface.Client
	node peer.Node
}

func (h localHoarder) Stash(cid string, r io.Reader, _ ...hoarderIface.StashOption) error {
	got, err := h.node.AddFileForCid(r)
	if err != nil {
		return err
	}
	if got.String() != cid {
		return fmt.Errorf("cid mismatch: declared %s, imported %s", cid, got)
	}
	return nil
}

func newLocalStore(t *testing.T, node peer.Node, project string) *Store {
	db, err := kvdbMock.New().New(nil, project, 0)
	if err != nil {
		t.Fatal(err)
	}
	return &Store{
		KVDB:          db,
		srv:           fakeSrv{node: node},
		hoarderClient: localHoarder{node: node},
		id:            project,
		context: storageIface.Context{
			ProjectId: project,
			Matcher:   "files",
			Config:    &structureSpec.Storage{Size: 64 << 20},
		},
	}
}

// readBack reads name through Meta().Get(). A missing local block makes
// bitswap wait forever on a lone node, so a stuck read counts as unreadable.
func readBack(t *testing.T, s *Store, name string, want []byte) {
	t.Helper()
	readVersion(t, s, name, 0, want)
}

// readVersion is readBack for one version; 0 is the latest.
func readVersion(t *testing.T, s *Store, name string, version int, want []byte) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		meta, err := s.Meta(context.Background(), name, version)
		if err != nil {
			done <- err
			return
		}
		f, err := meta.Get()
		if err != nil {
			done <- err
			return
		}
		defer f.Close()
		got, err := io.ReadAll(f)
		if err == nil && !bytes.Equal(got, want) {
			err = fmt.Errorf("read %d bytes, content differs from the %d added", len(got), len(want))
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s/%s unreadable: %v", s.context.ProjectId, name, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s/%s unreadable: read still blocked after 5s (local block gone, no peer has it)", s.context.ProjectId, name)
	}
}

func TestStorage_AddFileKeepsLocalHoarderReplica(t *testing.T) {
	node := peer.Mock(t.Context())
	defer node.Close()
	s := newLocalStore(t, node, "projectA")

	data := make([]byte, 5<<20)
	rand.Read(data)
	if _, err := s.AddFile(t.Context(), bytes.NewReader(data), "big.bin", false); err != nil {
		t.Fatal(err)
	}

	readBack(t, s, "big.bin", data)
}

func TestStorage_TwoProjectsSameBytes(t *testing.T) {
	node := peer.Mock(t.Context())
	defer node.Close()
	a := newLocalStore(t, node, "projectA")
	b := newLocalStore(t, node, "projectB")

	data := make([]byte, 1<<20)
	rand.Read(data)
	for _, s := range []*Store{a, b} {
		if _, err := s.AddFile(t.Context(), bytes.NewReader(data), "same.bin", false); err != nil {
			t.Fatal(err)
		}
	}
	readBack(t, b, "same.bin", data) // fails today already: AddFile dropped the shared root

	if err := a.DeleteFile(t.Context(), "same.bin", -1); err != nil {
		t.Fatal(err)
	}
	readBack(t, b, "same.bin", data)
}
