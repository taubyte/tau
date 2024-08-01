package tns

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/tns/flat"
)

func TestPush(t *testing.T) {
	testCtx, testCtxC := context.WithCancel(context.Background())
	defer func() {
		s := (3 * time.Second)
		go func() {
			time.Sleep(s)
			testCtxC()
		}()
		time.Sleep(s)
	}()

	srvRoot := t.TempDir()

	srv, err := New(testCtx, &config.Node{
		Root:        srvRoot,
		P2PListen:   []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11001)},
		P2PAnnounce: []string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", 11001)},
		DevMode:     true,
		SwarmKey:    common.SwarmKey(),
	})

	if err != nil {
		t.Errorf("Error creating Service %s", err)
		return
	}

	defer srv.Close()

	sendMap := map[string]interface{}{"a": "apple"}
	sendData := map[string]interface{}{"path": []string{"/t1"}, "data": sendMap}

	_, err = srv.pushHandler(testCtx, nil, sendData)
	if err != nil {
		t.Errorf("push err: %v", err)
		return
	}

	resp, err := srv.fetchHandler(testCtx, nil, map[string]interface{}{"path": []string{"/t1"}})
	if err != nil {
		t.Errorf("fetch err: %v", err)
		return
	}

	old_obj, err := flat.New([]string{"/t1"}, sendMap)
	if err != nil {
		t.Errorf("new flat err: %v", err)
		return
	}

	if !reflect.DeepEqual(old_obj.Interface(), resp["object"]) {
		t.Error("Objects do not match")
	}

}
