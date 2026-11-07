package hoarder

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/taubyte/tau/p2p/streams/command/response"
)

// A peer can stream a stored file's bytes under a false CID. Rejecting the push
// must leave that file intact: the blocks it hashes to belong to someone else.
func TestStashReceive_MismatchKeepsExistingBlocks(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()

	data := bytes.Repeat([]byte("owned by another project "), 1000)
	stored, err := srv.node.AddFileForCid(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	writeStashHeader(t, &buf, "bafyDeclaredButWrong", 1, false)
	buf.Write(data)
	srv.stashReceive(ctx, &buf)

	resp, err := response.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["error"].(string); !ok {
		t.Fatalf("expected an error ack on cid mismatch, got %v", resp)
	}

	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	r, err := srv.node.GetFileFromCid(readCtx, stored)
	if err != nil {
		t.Fatalf("stored file no longer readable: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("stored file no longer readable: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("stored file changed")
	}
}
