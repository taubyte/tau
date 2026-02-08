package service

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/config"
	"gotest.tools/v3/assert"
)

func TestPackage(t *testing.T) {
	iface := Package()
	assert.Assert(t, iface != nil)

	_, ok := iface.(protoCommandIface)
	assert.Assert(t, ok, "Package should return protoCommandIface")
}

func TestProtoCommandIface_New(t *testing.T) {
	iface := Package()
	ctx := context.Background()

	_, err := iface.New(ctx, nil)
	assert.ErrorContains(t, err, "you must define p2p port")

	cfg, err := config.New(
		config.WithRoot(t.TempDir()),
		config.WithP2PListen([]string{"/ip4/0.0.0.0/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := iface.New(ctx, cfg)
	assert.NilError(t, err)
	if svc != nil {
		svc.Close()
	}
}
