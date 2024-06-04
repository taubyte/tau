package services

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (g getter) Struct() (srv *structureSpec.Service, err error) {
	srv = &structureSpec.Service{
		Id:          g.Id(),
		Name:        g.Name(),
		Description: g.Description(),
		Tags:        g.Tags(),
		Protocol:    g.Protocol(),
	}

	return
}
