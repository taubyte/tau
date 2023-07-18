package p2p

import (
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

func (c *Client) Simple() tns.SimpleIface {
	return structure.Simple(c)
}

func (c *Client) Database() tns.StructureIface[*structureSpec.Database] {
	return structure.New[*structureSpec.Database](c, databaseSpec.PathVariable)
}

func (c *Client) Domain() tns.StructureIface[*structureSpec.Domain] {
	return structure.New[*structureSpec.Domain](c, domainSpec.PathVariable)
}

func (c *Client) Function() tns.StructureIface[*structureSpec.Function] {
	return structure.New[*structureSpec.Function](c, functionSpec.PathVariable)
}

func (c *Client) Library() tns.StructureIface[*structureSpec.Library] {
	return structure.New[*structureSpec.Library](c, librarySpec.PathVariable)
}

func (c *Client) Messaging() tns.StructureIface[*structureSpec.Messaging] {
	return structure.New[*structureSpec.Messaging](c, messagingSpec.PathVariable)
}

func (c *Client) Service() tns.StructureIface[*structureSpec.Service] {
	return structure.New[*structureSpec.Service](c, serviceSpec.PathVariable)
}

func (c *Client) SmartOp() tns.StructureIface[*structureSpec.SmartOp] {
	return structure.New[*structureSpec.SmartOp](c, smartOpSpec.PathVariable)
}

func (c *Client) Storage() tns.StructureIface[*structureSpec.Storage] {
	return structure.New[*structureSpec.Storage](c, storageSpec.PathVariable)
}

func (c *Client) Website() tns.StructureIface[*structureSpec.Website] {
	return structure.New[*structureSpec.Website](c, websiteSpec.PathVariable)
}
