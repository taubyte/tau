package projects

import "github.com/taubyte/odo/pkgs/kvdb/database"

type Data map[string]interface{}

type Project interface {
	Register() error
	Delete() error
	Serialize() Data
}

type ProjectObject struct {
	KV     *database.KVDatabase
	Id     string
	Name   string
	Config int
	Code   int
}
