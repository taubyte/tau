package service

import (
	"errors"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"

	"github.com/taubyte/tau/pkg/spore-drive/config"
)

func (s *Service) doShapes(in *pb.Shapes, p config.Parser) (*connect.Response[pb.Return], error) {
	if in.GetList() {
		return returnStringSlice(p.Shapes().List()), nil
	}

	if x := in.GetSelect(); x != nil {
		name := x.GetName()
		if name == "" {
			return nil, errors.New("shape must have a name")
		}

		// services
		if n := x.GetServices(); n != nil {
			// get
			if n.GetList() {
				return returnStringSlice(p.Shapes().Shape(name).Services().List()), nil
			}

			// set
			if l := n.GetSet(); l != nil {
				return returnEmpty(p.Shapes().Shape(name).Services().Set(l.GetValue()...))
			}

			if l := n.GetAdd(); l != nil {
				return returnEmpty(p.Shapes().Shape(name).Services().Append(l.GetValue()...))
			}

			if l := n.GetDelete(); l != nil {
				for _, li := range l.GetValue() {
					if err := p.Shapes().Shape(name).Services().Delete(li); err != nil {
						return nil, err
					}
				}

				return returnEmpty(nil)
			}

			if n.GetClear() {
				for _, sh := range p.Shapes().Shape(name).Services().List() {
					if err := p.Shapes().Shape(name).Services().Delete(sh); err != nil {
						return nil, err
					}
				}

				return returnEmpty(nil)
			}
		}

		// ports
		if y := x.GetPorts(); y != nil {
			if y.GetList() {
				return returnStringSlice(p.Shapes().Shape(name).Ports().List()), nil
			}

			if k := y.GetSelect(); k != nil {
				portName := k.GetName()
				if k.GetGet() {
					return returnUint(uint64(p.Shapes().Shape(name).Ports().Get(portName))), nil
				}

				if k.GetDelete() {
					return returnEmpty(p.Shapes().Shape(name).Ports().Delete(portName))
				}

				if pval := k.GetSet(); pval != 0 {
					return returnEmpty(p.Shapes().Shape(name).Ports().Set(portName, uint16(pval)))
				}
			}
		}

		// plugins
		if n := x.GetPlugins(); n != nil {
			// get
			if n.GetList() {
				return returnStringSlice(p.Shapes().Shape(name).Plugins().List()), nil
			}

			// set
			if l := n.GetSet(); l != nil {
				return returnEmpty(p.Shapes().Shape(name).Plugins().Set(l.GetValue()...))
			}

			if l := n.GetAdd(); l != nil {
				return returnEmpty(p.Shapes().Shape(name).Plugins().Append(l.GetValue()...))
			}

			if l := n.GetDelete(); l != nil {
				for _, li := range l.GetValue() {
					if err := p.Shapes().Shape(name).Plugins().Delete(li); err != nil {
						return nil, err
					}
				}

				return returnEmpty(nil)
			}

			if n.GetClear() {
				for _, sh := range p.Shapes().Shape(name).Plugins().List() {
					if err := p.Shapes().Shape(name).Plugins().Delete(sh); err != nil {
						return nil, err
					}
				}

				return returnEmpty(nil)
			}
		}

		// delete
		if x.GetDelete() {
			return returnEmpty(p.Shapes().Delete(name))
		}
	}

	return nil, errors.New("invalid shapes operation")
}
