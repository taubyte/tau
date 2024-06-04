package database

import structureSpec "github.com/taubyte/tau/pkg/specs/structure"

type Database interface {
	KV() KV
	DBContext() Context
	SetConfig(*structureSpec.Database)
	Close()
	Config() *structureSpec.Database
}
