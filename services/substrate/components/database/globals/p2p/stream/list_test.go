package api

import (
	"context"
	"testing"

	"github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/p2p/streams/command"
	mh "github.com/taubyte/utils/multihash"
	"golang.org/x/exp/slices"
)

func TestListHandler(t *testing.T) {
	testProjectID := "123456"

	mockHandler := &StreamHandler{
		srv: &mockService{
			databases: map[string]database.Database{
				mh.Hash(testProjectID): &mockDatabase{
					&mockKV{
						data: map[string][]byte{
							"/key1":     []byte("value1"),
							"/key2":     []byte("value2"),
							"/key3":     []byte("value3"),
							"/otherkey": []byte("value3"),
						},
					},
				},
			},
		},
	}

	resp, err := mockHandler.listHandler(context.TODO(), nil, command.Body{
		"projectID": testProjectID,
		"prefix":    "/key",
	})
	if err != nil {
		t.Error(err)
		return
	}

	expectedKeys := []string{"/key1", "/key2", "/key3"}
	for _, key := range expectedKeys {
		if !slices.Contains(resp["keys"].([]string), key) {
			t.Errorf("expected key %s to be in %s", key, resp["keys"].([]string))
			return
		}
	}
}
