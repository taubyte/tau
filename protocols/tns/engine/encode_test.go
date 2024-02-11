package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"testing"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/p2p/keypair"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/pkgs/kvdb"
	protocolsCommon "github.com/taubyte/tau/protocols/common"
	"github.com/taubyte/tau/protocols/tns/flat"
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
		protocolsCommon.SwarmKey(),
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
	db, err := dbFactory.New(logger, protocolsCommon.Tns, 5)
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
		"a": 1,
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

	// Convert to json and compare sent and received objects
	jsonData, err := json.Marshal(object.Data)
	if err != nil {
		t.Errorf("Marshal failed with err: %v", err)
		return
	}

	jsonData2, err := json.Marshal(object2.Data)
	if err != nil {
		t.Errorf("Marshal failed with err: %v", err)
		return
	}

	if string(jsonData) != string(jsonData2) {
		t.Error("JSON data not equal")
		return
	}

}
