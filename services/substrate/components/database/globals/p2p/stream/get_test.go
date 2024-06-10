package api

import (
	"context"
	"testing"

	"github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/p2p/streams/command"
	mh "github.com/taubyte/utils/multihash"
)

func TestGetHandler(t *testing.T) {
	testProjectID := "123456"

	mockHandler := &StreamHandler{
		srv: &mockService{
			databases: map[string]database.Database{
				mh.Hash(testProjectID): &mockDatabase{
					&mockKV{
						data: map[string][]byte{
							"/key1": []byte("Hello, world!"),
							"/key2": {0x0, 0x0, 0x2, 0x15},
						},
					},
				},
			},
		},
	}

	resp, err := mockHandler.getHandler(context.TODO(), nil, command.Body{
		"projectID": testProjectID,
		"key":       "/key1",
		"type":      "string",
	})
	if err != nil {
		t.Error(err)
		return
	}

	if resp["value"].(string) != "Hello, world!" {
		t.Errorf("expected %s, got %s", "Hello, world!", resp["value"].(string))
		return
	}

	resp, err = mockHandler.getHandler(context.TODO(), nil, command.Body{
		"projectID": testProjectID,
		"key":       "/key2",
		"type":      "uint32",
	})
	if err != nil {
		t.Error(err)
		return
	}

	if resp["value"].(uint32) != 533 {
		t.Errorf("expected %d, got %d", 533, resp["value"].(uint32))
		return
	}
}
