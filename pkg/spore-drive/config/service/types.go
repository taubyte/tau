package service

import (
	"net/http"
	"sync"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/spore-drive/config"
	pbconnect "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1/configv1connect"
)

// server is used to implement pb.ConfigServiceServer.
type Service struct {
	pbconnect.UnimplementedConfigServiceHandler

	lock    sync.RWMutex
	configs map[string]*configInstance

	path    string
	handler http.Handler
}

type configInstance struct {
	lock   sync.Mutex
	path   string // only for local = root + base
	id     string
	fs     afero.Fs
	parser config.Parser
}
