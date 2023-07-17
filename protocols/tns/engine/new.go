package engine

import (
	"github.com/taubyte/go-interfaces/kvdb"
)

func New(db kvdb.KVDB, prefix ...string) (*Engine, error) {
	e := &Engine{
		db:     db,
		prefix: prefix,
	}

	err := e.validateVersion()
	if err != nil {
		return nil, err
	}

	return e, nil
}
