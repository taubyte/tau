package service

import (
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"

	"github.com/taubyte/tau/pkg/spore-drive/config"
)

func (s *Service) doHosts(in *pb.Hosts, p config.Parser) (*connect.Response[pb.Return], error) {
	// get
	if in.GetList() {
		return returnStringSlice(p.Hosts().List()), nil
	}

	if x := in.GetSelect(); x != nil {
		name := x.GetName()

		// address
		if n := x.GetAddresses(); n != nil {
			// get
			if n.GetList() {
				return returnStringSlice(p.Hosts().Host(name).Addresses().List()), nil
			}

			// set
			if l := n.GetSet(); l != nil {
				return returnEmpty(p.Hosts().Host(name).Addresses().Set(l.GetValue()...))
			}

			if l := n.GetAdd(); l != nil {
				return returnEmpty(p.Hosts().Host(name).Addresses().Append(l.GetValue()...))
			}

			if l := n.GetDelete(); l != nil {
				for _, li := range l.GetValue() {
					if err := p.Hosts().Host(name).Addresses().Delete(li); err != nil {
						return nil, err
					}
				}

				return returnEmpty(nil)
			}

			if n.GetClear() {
				for _, sh := range p.Hosts().Host(name).Addresses().List() {
					if err := p.Hosts().Host(name).Addresses().Delete(sh); err != nil {
						return nil, err
					}
				}

				return returnEmpty(nil)
			}
		}

		// ssh
		if j := x.GetSsh(); j != nil {
			if n := j.GetAddress(); n != nil {
				if n.GetGet() {
					addr := p.Hosts().Host(name).SSH().Address()
					if addr != "" {
						return returnString(fmt.Sprintf(
							"%s:%d",
							p.Hosts().Host(name).SSH().Address(),
							p.Hosts().Host(name).SSH().Port(),
						)), nil
					}
					return nil, errors.New("host not found")
				}

				if y := n.GetSet(); y != "" {
					return returnEmpty(p.Hosts().Host(name).SSH().SetFullAddress(y))
				}
			}

			if n := j.GetAuth(); n != nil {
				// get
				if n.GetList() {
					return returnStringSlice(p.Hosts().Host(name).SSH().Auth().List()), nil
				}

				// set
				if l := n.GetSet(); l != nil {
					return returnEmpty(p.Hosts().Host(name).SSH().Auth().Set(l.GetValue()...))
				}

				if l := n.GetAdd(); l != nil {
					return returnEmpty(p.Hosts().Host(name).SSH().Auth().Append(l.GetValue()...))
				}

				if l := n.GetDelete(); l != nil {
					for _, li := range l.GetValue() {
						if err := p.Hosts().Host(name).SSH().Auth().Delete(li); err != nil {
							return nil, err
						}
					}

					return returnEmpty(nil)
				}

				if n.GetClear() {
					for _, sh := range p.Hosts().Host(name).Addresses().List() {
						if err := p.Hosts().Host(name).Addresses().Delete(sh); err != nil {
							return nil, err
						}
					}

					return returnEmpty(nil)
				}
			}
		}

		// location
		if j := x.GetLocation(); j != nil {
			if j.GetGet() {
				lat, lng := p.Hosts().Host(name).Location()
				return returnString(fmt.Sprintf("%f,%f", lat, lng)), nil
			}

			if j.GetSet() != "" {
				var lat, lng float32

				if pn, err := fmt.Sscanf(strings.TrimSpace(j.GetSet()), "%f,%f", &lat, &lng); err != nil || pn != 2 {
					return nil, errors.New("invalid location format: expected `latitude,longitude`")
				}

				return returnEmpty(p.Hosts().Host(name).SetLocation(lat, lng))
			}
		}

		// shapes
		if j := x.GetShapes(); j != nil {
			if j.GetList() {
				return returnStringSlice(p.Hosts().Host(name).Shapes().List()), nil
			}

			if n := j.GetSelect(); n != nil {
				shape := n.GetName()

				if y := n.GetSelect(); y != nil {
					// get
					if y.GetId() {
						return returnString(p.Hosts().Host(name).Shapes().Instance(shape).Id()), nil
					}

					if l := y.GetKey(); l != nil && l.GetGet() {
						return returnString(p.Hosts().Host(name).Shapes().Instance(shape).Key()), nil
					}

					// set
					if l := y.GetKey(); l != nil && (!l.GetGet() || l.GetSet() != "") {
						return returnEmpty(p.Hosts().Host(name).Shapes().Instance(shape).SetKey(l.GetSet()))
					}

					// generate
					if y.GetGenerate() {
						return returnEmpty(p.Hosts().Host(name).Shapes().Instance(shape).GenerateKey())
					}
				}

				if n.GetDelete() {
					return returnEmpty(p.Hosts().Host(name).Shapes().Delete(shape))
				}
			}
		}

		// delete
		if x.GetDelete() {
			return returnEmpty(p.Hosts().Delete(name))
		}

	}

	return nil, errors.New("invalid host operation")
}
