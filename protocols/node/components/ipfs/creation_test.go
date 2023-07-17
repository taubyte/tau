package ipfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestIpfs(t *testing.T) {
	testString := fmt.Sprintf("GOING TO IPFS!!%d", time.Now().Unix())
	ctx, ctxC := context.WithTimeout(context.Background(), (5 * time.Minute))
	defer ctxC()

	testNode, err := New(ctx, Public(), Listen([]string{"/ip4/0.0.0.0/tcp/8890"}))
	if err != nil {
		t.Error(err)
		return
	}

	err = testNode.WaitForSwarm(time.Second)
	if err != nil {
		t.Error(err)
		return
	}

	testNode2, err := New(ctx, Public(), Listen([]string{"/ip4/0.0.0.0/tcp/8899"}))
	if err != nil {
		t.Error(err)
		return
	}

	err = testNode2.WaitForSwarm(time.Second)
	if err != nil {
		t.Error(err)
		return
	}

	err = testNode.Peer().Connect(ctx, peer.AddrInfo{
		ID:    testNode2.ID(),
		Addrs: testNode2.Peer().Addrs(),
	})
	if err != nil {
		t.Error(err)
		return
	}

	var file bytes.Buffer
	file.Write([]byte(testString))

	id, err := testNode.AddFile(&file)
	if err != nil {
		t.Error(err)
		return
	}

	data, err := testNode2.GetFile(ctx, id)
	if err != nil {
		t.Error(err)
		return
	}
	defer data.Close()

	_data, err := io.ReadAll(data)
	if err != nil {
		t.Error(err)
		return
	}

	if string(_data) != (testString) {
		t.Errorf("Wrong file. Got %s, Expecting %s", string(_data), testString)
		return

	}
}
