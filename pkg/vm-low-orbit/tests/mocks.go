package tests

import (
	logging "github.com/ipfs/go-log/v2"
	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	kvdbMock "github.com/taubyte/tau/pkg/kvdb/mock"
	dbkv "github.com/taubyte/tau/services/substrate/components/database/kv"
)

// Backend mocks. Each embeds the real interface (nil) and overrides only the
// methods the plugin factories call.

// mockDBService backs the database + globals plugins with the real
// size-tracking KV layer over an in-memory kvdb mock, so the guests see genuine
// backend semantics. A fresh store per Database() call keeps each guest run
// isolated.
type mockDBService struct {
	dbIface.Service
}

func (m *mockDBService) newDatabase(name string) (dbIface.Database, error) {
	store, err := kvdbMock.New().New(logging.Logger("vm-test"), name, 0)
	if err != nil {
		return nil, err
	}
	return &mockDB{kv: dbkv.New(1<<30, name, store)}, nil
}

func (m *mockDBService) Database(dbIface.Context) (dbIface.Database, error) {
	return m.newDatabase("test")
}

func (m *mockDBService) Global(projectID string) (dbIface.Database, error) {
	return m.newDatabase("global-" + projectID)
}

type mockDB struct {
	dbIface.Database
	kv dbIface.KV
}

func (m *mockDB) KV() dbIface.KV { return m.kv }
func (m *mockDB) Close()         {}
