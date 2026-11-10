package peer

import (
	"bytes"
	"context"
	"crypto/rand"
	"testing"
)

func TestCidOfMatchesAddFile(t *testing.T) {
	ctx := context.Background()
	n := Mock(ctx)
	defer n.Close()
	for _, size := range []int{0, 1 << 10, 256 << 10, 256<<10 + 1, 5_000_000} {
		data := make([]byte, size)
		rand.Read(data)
		want, err := n.AddFile(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		got, err := CidOf(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}

		if got != want {
			t.Fatalf("size %d mismatch", size)
		}
	}
}
