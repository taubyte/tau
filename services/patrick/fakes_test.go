package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/kvdb"
	commonIface "github.com/taubyte/tau/core/services/patrick"
)

// fakeJobs is a job store holding only what a test puts in it.
type fakeJobs struct {
	kvdb.KVDB
	lock sync.Mutex
	keys map[string][]byte
}

func (d *fakeJobs) Get(_ context.Context, key string) ([]byte, error) {
	d.lock.Lock()
	defer d.lock.Unlock()
	if v, ok := d.keys[key]; ok {
		return v, nil
	}
	return nil, errors.New("not found")
}

func (d *fakeJobs) put(t *testing.T, key string) {
	t.Helper()
	data, err := cbor.Marshal(&commonIface.Job{Id: "j"})
	if err != nil {
		t.Fatal(err)
	}
	d.lock.Lock()
	defer d.lock.Unlock()
	d.keys[key] = data
}
