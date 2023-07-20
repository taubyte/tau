package api

import (
	"context"
	"fmt"
	"strings"

	moody "bitbucket.org/taubyte/go-moody-blues"
	moodyIface "github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/services/substrate"
	"github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/services/substrate/components/database"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/go-interfaces/vm"
	structureSpec "github.com/taubyte/go-specs/structure"
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
	mh "github.com/taubyte/utils/multihash"
)

// TODO: Needs to create real mocks

var _ database.Service = &mockService{}

type mockService struct {
	databases map[string]database.Database
}

func (s *mockService) SmartOps() substrate.SmartOpsService {
	return nil
}

func (s *mockService) Branch() string {
	return "master"
}

func (s *mockService) Cache() components.Cache {
	return nil
}

func (s *mockService) Orbitals() []vm.Plugin {
	return nil
}

func (s *mockService) Tns() tns.Client {
	return nil
}

func (s *mockService) Vm() vm.Service {
	return nil
}

func (s *mockService) CheckTns(components.MatchDefinition) ([]components.Serviceable, error) {
	return nil, nil
}

func (s *mockService) Counter() substrate.CounterService {
	return nil
}

func (s *mockService) Http() http.Service {
	return nil
}

func (s *mockService) Dev() bool {
	return true
}

func (s *mockService) Database(context database.Context) (database.Database, error) {
	return nil, nil
}

func (s *mockService) Context() context.Context {
	return context.Background()
}

func (s *mockService) Logger() moodyIface.Logger {
	logger, _ := moody.New("test")
	return logger

}

func (s *mockService) Databases() map[string]database.Database {
	if s.databases == nil {
		s.databases = make(map[string]database.Database)
	}

	return s.databases
}

func (s *mockService) List(context database.Context) ([]string, error) {
	return []string{"1", "2", "3"}, nil
}

func (s *mockService) Global(projectID string) (database.Database, error) {
	s.Databases()

	hash := mh.Hash(projectID)
	if db, ok := s.databases[hash]; ok == true {
		return db, nil
	}

	db := &mockDatabase{
		kv: &mockKV{},
	}
	s.databases[hash] = db

	return db, nil
}

func (s *mockService) Node() peer.Node {
	return nil
}

func (s *mockService) Close() error {
	s.databases = nil
	return nil
}

var _ database.Database = &mockDatabase{}

type mockDatabase struct {
	kv *mockKV
}

func (d *mockDatabase) KV() database.KV {
	return d.kv
}

func (d *mockDatabase) Close() {}

func (d *mockDatabase) DBContext() database.Context {
	return database.Context{}
}

func (d *mockDatabase) SetConfig(config *structureSpec.Database) {
}

func (d *mockDatabase) Config() *structureSpec.Database {
	return nil
}

var _ database.KV = &mockKV{}

type mockKV struct {
	data map[string][]byte
	size uint64
}

func (k *mockKV) Get(ctx context.Context, key string) ([]byte, error) {
	v, ok := k.data[key]
	if ok == false {
		return nil, fmt.Errorf("not found")
	}

	return v, nil
}

func (k *mockKV) Put(ctx context.Context, key string, v []byte) error {
	k.data[key] = v
	return nil
}

func (k *mockKV) Delete(ctx context.Context, key string) error {
	delete(k.data, key)
	return nil
}

func (k *mockKV) UpdateSize(size uint64) {
	k.size = size
}

func (k *mockKV) Size(ctx context.Context) (uint64, error) {
	return k.size, nil
}

func (k *mockKV) List(ctx context.Context, prefix string) ([]string, error) {
	keys := []string{}
	for k := range k.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}

	return keys, nil
}

func (k *mockKV) Close() {
	k.data = nil
}
