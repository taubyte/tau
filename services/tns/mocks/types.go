package mocks

import (
	"sync"

	"github.com/taubyte/tau/core/services/tns"
)

type MockedTns interface {
	tns.Client
	Inject(interface{}, InjectConfig) error
	Delete(tns.Path)
}

type mockTns struct {
	mapDef map[string]interface{}
	lock   sync.RWMutex
	tns.Client
}

type mockedObject struct {
	tns.Object
	value interface{}
	path  tns.Path
}

type mockedPath struct {
	tns.Path
	path string
}

type InjectConfig struct {
	Branch      string
	Commit      string
	Project     string
	Application string
	Cid         string
}

type InjectObj struct {
	Path  tns.Path
	Value interface{}
}
