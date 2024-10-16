package service

import (
	"context"
	"net/http"
	"sync"

	"github.com/taubyte/tau/pkg/spore-drive/config"
	"github.com/taubyte/tau/pkg/spore-drive/course"
	"github.com/taubyte/tau/pkg/spore-drive/drive"
	pbconnect "github.com/taubyte/tau/pkg/spore-drive/proto/gen/drive/v1/drivev1connect"
)

type Service struct {
	pbconnect.UnimplementedDriveServiceHandler

	ctx context.Context

	driveLock sync.RWMutex
	drives    map[string]*driveInstance

	courseLock sync.RWMutex
	courses    map[string]*courseInstance

	resolver ConfigResolver

	path    string
	handler http.Handler
}

type driveInstance struct {
	id string
	drive.Spore
}

type courseInstance struct {
	ctx  context.Context
	ctxC context.CancelFunc

	course.Course

	lock    sync.Mutex
	id      string
	drive   *driveInstance
	service *Service

	pch <-chan drive.Progress

	mockDisplace func() <-chan drive.Progress
}

type ConfigResolver interface {
	Lookup(id string) (config.Parser, error)
}
