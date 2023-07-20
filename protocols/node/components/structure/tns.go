package structure

import (
	"github.com/mitchellh/mapstructure"
	"github.com/taubyte/go-interfaces/services/tns"
	databaseSpec "github.com/taubyte/go-specs/database"
	domainSpec "github.com/taubyte/go-specs/domain"
	functionSpec "github.com/taubyte/go-specs/function"
	librarySpec "github.com/taubyte/go-specs/library"
	messagingSpec "github.com/taubyte/go-specs/messaging"
	serviceSpec "github.com/taubyte/go-specs/service"
	smartOpSpec "github.com/taubyte/go-specs/smartops"
	storageSpec "github.com/taubyte/go-specs/storage"
	structureSpec "github.com/taubyte/go-specs/structure"
	websiteSpec "github.com/taubyte/go-specs/website"
	"github.com/taubyte/odo/clients/p2p/tns/structure"
)

var FakeFetchMethod = func(tns.Path) (tns.Object, error) {
	return nil, nil
}

var FakeCurrentMethod = func(r tns.Object, branch string) ([]tns.Path, error) {
	return nil, nil
}

type TestClient struct {
	tns.Client
}

func (tc TestClient) Fetch(path tns.Path) (tns.Object, error) {
	return FakeFetchMethod(path)
}

type ResponseObject struct {
	Object    interface{}
	InnerPath tns.Path
	Tns       tns.Client
}

func (f ResponseObject) Bind(binder interface{}) error {
	return mapstructure.Decode(f.Object, &binder)
}

func (r ResponseObject) Interface() interface{} {
	return r.Object
}

func (f ResponseObject) Path() tns.Path {
	return f.InnerPath
}

func (f ResponseObject) Current(branch string) ([]tns.Path, error) {
	return FakeCurrentMethod(f, branch)
}

func (tc TestClient) Simple() tns.SimpleIface {
	return structure.Simple(tc)
}

func (c TestClient) Database() tns.StructureIface[*structureSpec.Database] {
	return structure.New[*structureSpec.Database](c, databaseSpec.PathVariable)
}

func (c TestClient) Domain() tns.StructureIface[*structureSpec.Domain] {
	return structure.New[*structureSpec.Domain](c, domainSpec.PathVariable)
}

func (c TestClient) Function() tns.StructureIface[*structureSpec.Function] {
	return structure.New[*structureSpec.Function](c, functionSpec.PathVariable)
}

func (c TestClient) Library() tns.StructureIface[*structureSpec.Library] {
	return structure.New[*structureSpec.Library](c, librarySpec.PathVariable)
}

func (c TestClient) Messaging() tns.StructureIface[*structureSpec.Messaging] {
	return structure.New[*structureSpec.Messaging](c, messagingSpec.PathVariable)
}

func (c TestClient) Service() tns.StructureIface[*structureSpec.Service] {
	return structure.New[*structureSpec.Service](c, serviceSpec.PathVariable)
}

func (c TestClient) SmartOp() tns.StructureIface[*structureSpec.SmartOp] {
	return structure.New[*structureSpec.SmartOp](c, smartOpSpec.PathVariable)
}

func (c TestClient) Storage() tns.StructureIface[*structureSpec.Storage] {
	return structure.New[*structureSpec.Storage](c, storageSpec.PathVariable)
}

func (c TestClient) Website() tns.StructureIface[*structureSpec.Website] {
	return structure.New[*structureSpec.Website](c, websiteSpec.PathVariable)
}
