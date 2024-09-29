package service

import (
	"sync"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/spore-drive/config"
	pb "github.com/taubyte/tau/pkg/spore-drive/config/proto/go"
)

//go:generate protoc --proto_path=. --go_out=go --go-grpc_out=go config.proto

// server is used to implement pb.ConfigServiceServer.
type Service struct {
	pb.UnimplementedConfigServiceServer

	lock    sync.RWMutex
	configs map[string]*configInstance
}

type configInstance struct {
	lock   sync.Mutex
	path   string // only for local = root + base
	id     string
	fs     afero.Fs
	parser config.Parser
}
