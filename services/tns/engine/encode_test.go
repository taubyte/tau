package engine

import (
	"context"
	"fmt"

	"testing"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/tns/flat"
	"gotest.tools/v3/assert"
)

func TestEncode(t *testing.T) {
	logger := log.Logger("tau.tns.service.testing")
	testCtx, testCtxC := context.WithCancel(context.Background())
	defer func() {
		s := (3 * time.Second)
		go func() {
			time.Sleep(s)
			testCtxC()
		}()
		time.Sleep(s)
	}()

	peerC, err := peer.New(
		testCtx,
		nil,
		keypair.NewRaw(),
		servicesCommon.SwarmKey(),
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11002)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Errorf("New node failed with err: %v", err)
		return
	}

	dbFactory := kvdb.New(peerC)
	db, err := dbFactory.New(logger, servicesCommon.Tns, 5)
	if err != nil {
		t.Errorf("New db failed with err: %v", err)
		return
	}

	engine, err := New(db, Prefix...)
	if err != nil {
		t.Errorf("New engine failed with err: %v", err)
		return
	}

	data := map[string]interface{}{
		"a": uint64(1),
		"b": "string",
	}

	object, err := flat.New([]string{"t1"}, data)
	if err != nil {
		t.Errorf("New Flat failed with err: %v", err)
		return
	}

	err = engine.Merge(testCtx, object)
	if err != nil {
		t.Errorf("Merge failed with err: %v", err)
		return
	}

	object2, err := engine.Get(testCtx, "t1")
	if err != nil {
		t.Errorf("List failed with err: %v", err)
		return
	}

	assert.DeepEqual(t, object.Data, object2.Data)
}
