package service

import (
	"context"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"gotest.tools/v3/assert"
)

func TestRefreshHandlerOwnerOnly(t *testing.T) {
	ctx := context.Background()
	service := createTestService()
	old, _ := cbor.Marshal(Assignment{MonkeyPID: peer.ID("monkey-1").String(), Timestamp: 1})
	assert.NilError(t, service.db.Put(ctx, "/assigned/job-1", old))

	stamp := func() int64 {
		data, err := service.db.Get(ctx, "/assigned/job-1")
		assert.NilError(t, err)
		var a Assignment
		assert.NilError(t, cbor.Unmarshal(data, &a))
		assert.Equal(t, peer.ID("monkey-1").String(), a.MonkeyPID)
		return a.Timestamp
	}

	resp, err := service.refreshHandler(ctx, "job-1", &mockConnection{remotePeer: peer.ID("monkey-2")})
	assert.NilError(t, err)
	assert.Equal(t, false, resp["assigned"])
	assert.Equal(t, int64(1), stamp())

	resp, err = service.refreshHandler(ctx, "job-1", &mockConnection{remotePeer: peer.ID("monkey-1")})
	assert.NilError(t, err)
	assert.Equal(t, true, resp["assigned"])
	assert.Assert(t, stamp() > 1)

	resp, err = service.refreshHandler(ctx, "job-2", &mockConnection{remotePeer: peer.ID("monkey-1")})
	assert.NilError(t, err)
	assert.Equal(t, false, resp["assigned"])
}
