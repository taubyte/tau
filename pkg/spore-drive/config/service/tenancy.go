package service

import (
	"errors"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"

	"github.com/taubyte/tau/pkg/spore-drive/config"
)

func (s *Service) doTenancy(in *pb.Tenancy, p config.Parser) (*connect.Response[pb.Return], error) {
	if x := in.GetProvider(); x != nil {
		if x.GetGet() {
			return returnString(p.Tenancy().Provider()), nil
		}
		return returnEmpty(p.Tenancy().SetProvider(x.GetSet()))
	}

	if x := in.GetOwner(); x != nil {
		if x.GetGet() {
			return returnString(p.Tenancy().Owner()), nil
		}
		return returnEmpty(p.Tenancy().SetOwner(x.GetSet()))
	}

	if a := in.GetApp(); a != nil {
		return s.doTenancyApp(a, p)
	}

	return nil, errors.New("invalid tenancy operation")
}

func (s *Service) doTenancyApp(in *pb.TenancyApp, p config.Parser) (*connect.Response[pb.Return], error) {
	app := p.Tenancy().App()

	if x := in.GetClientId(); x != nil {
		if x.GetGet() {
			return returnString(app.ClientId()), nil
		}
		return returnEmpty(app.SetClientId(x.GetSet()))
	}

	if x := in.GetKey(); x != nil {
		if x.GetGet() {
			return returnString(app.Key()), nil
		}
		return returnEmpty(app.SetKey(x.GetSet()))
	}

	return nil, errors.New("invalid tenancy.app operation")
}
