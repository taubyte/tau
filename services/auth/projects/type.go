package projects

import (
	"github.com/taubyte/tau/core/kvdb"
)

type Data map[string]interface{}

type Project interface {
	Register() error
	Delete() error
	Serialize() Data
}

type ProjectObject struct {
	KV       kvdb.KVDB
	Id       string
	Name     string
	Provider string
	Config   int
	Code     int
}
