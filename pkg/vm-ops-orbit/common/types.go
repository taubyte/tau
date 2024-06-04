package common

import (
	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/services/substrate/smartops"
	"github.com/taubyte/tau/pkg/vm-low-orbit/event"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory interface {
	helpers.Methods
	GetEvent(resourceId uint32) (*event.Event, errno.Error)
	GetResource(resourceId uint32) (*Resource, errno.Error)
}

type Resource struct {
	Id     uint32
	Caller smartops.EventCaller
}
