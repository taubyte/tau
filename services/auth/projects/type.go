package projects

import (
	"github.com/taubyte/tau/core/kvdb"
)

type Data map[string]interface{}

type Project interface {
	Register() error
	Delete() error
	Serialize() Data
	Name() string
	Provider() string
	Config() string
	Code() string
}

type projectObject struct {
	kv       kvdb.KVDB
	id       string
	name     string
	provider string
	config   string
	code     string
}

func (r *projectObject) Name() string {
	return r.name
}

func (r *projectObject) Provider() string {
	return r.provider
}

func (r *projectObject) Config() string {
	return r.config
}

func (r *projectObject) Code() string {
	return r.code
}
