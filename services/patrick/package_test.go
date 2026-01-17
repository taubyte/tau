package service

import (
	"context"
	"testing"

	"github.com/taubyte/tau/config"
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

	config := &config.Node{
		Root: "",
	}
	_, err = iface.New(ctx, config)
	assert.ErrorContains(t, err, "building config failed")
}
