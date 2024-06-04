package basic

import (
	"github.com/taubyte/go-seer"
)

// Resource contains the default methods, they can be overridden on the resource itself or
type Resource struct {
	ResourceIface
	seer   *seer.Seer
	Root   RootMethod
	Config RootMethod
}

// TODO make this an option so that project and application which override these are not wasting
func (r *Resource) initMethods() {
	r.Root = r.root
	r.Config = r.config
}

// New returns a Basic which is embedded into a resource for generic methods
func New(seer *seer.Seer, iface ResourceIface, name string) (*Resource, error) {
	if seer == nil {
		return nil, iface.WrapError("seer is nil")
	}

	if len(name) == 0 {
		return nil, iface.WrapError("name is empty")
	}

	res := &Resource{
		ResourceIface: iface,
		seer:          seer,
	}

	res.initMethods()
	return res, nil
}

// NewNoName like new returns a Basic which is embedded into a resource
// the only difference is no name is taken in the function or checked
func NewNoName(seer *seer.Seer, iface ResourceIface) (*Resource, error) {
	if seer == nil {
		return nil, iface.WrapError("seer is nil")
	}

	res := &Resource{
		ResourceIface: iface,
		seer:          seer,
	}

	res.initMethods()

	return res, nil
}
