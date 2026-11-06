package storage

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/taubyte/tau/p2p/peer"
)

// Each version of a file is its own record; deleting one must leave the others
// readable, including a version holding the very same bytes.
func TestStorage_DeleteVersionKeepsOthers(t *testing.T) {
	node := peer.Mock(t.Context())
	defer node.Close()
	s := newLocalStore(t, node, "projectA")
	s.context.Config.Versioning = true

	first := make([]byte, 600<<10)
	rand.Read(first)
	second := make([]byte, 600<<10)
	rand.Read(second)

	for _, data := range [][]byte{first, second, first} {
		if _, err := s.AddFile(t.Context(), bytes.NewReader(data), "doc.bin", false); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.DeleteFile(t.Context(), "doc.bin", 1); err != nil {
		t.Fatal(err)
	}

	readBack(t, s, "doc.bin", first) // version 3: same bytes as the deleted version 1
	if _, err := s.Meta(t.Context(), "doc.bin", 1); err == nil {
		t.Fatal("version 1 should be gone")
	}
	readVersion(t, s, "doc.bin", 2, second)
}
