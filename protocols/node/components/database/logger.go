package database

import (
	"github.com/taubyte/go-interfaces/moody"
)

var subLogger moody.Logger

func (s *Service) Logger() moody.Logger {
	if subLogger == nil {
		subLogger = s.Service.Logger().Sub("database")
	}

	return subLogger
}
