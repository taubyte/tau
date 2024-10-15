package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"

	"github.com/taubyte/tau/pkg/spore-drive/config"
)

func (s *Service) doAuth(in *pb.Auth, p config.Parser) (*connect.Response[pb.Return], error) {
	if in.GetList() {
		return returnStringSlice(p.Auth().List()), nil
	}

	if a := in.GetSelect(); a != nil {
		name := a.GetName()
		if name == "" {
			return nil, errors.New("signer must have a name")
		}

		if a.GetDelete() {
			return returnEmpty(p.Auth().Delete(name))
		}

		// Get
		if x := a.GetUsername(); x != nil && x.GetGet() {
			return returnString(p.Auth().Get(name).Username()), nil
		}

		if x := a.GetPassword(); x != nil && x.GetGet() {
			return returnString(p.Auth().Get(name).Password()), nil
		}

		if x := a.GetKey(); x != nil && x.GetPath().GetGet() {
			return returnString(p.Auth().Get(name).Key()), nil
		}

		if x := a.GetKey(); x != nil && x.GetData().GetGet() {
			kr, err := p.Auth().Get(name).Open()
			if err != nil {
				return nil, fmt.Errorf("failed to open ssh key for %s: %w", name, err)
			}
			defer kr.Close()

			if kdata, err := io.ReadAll(kr); err != nil {
				return nil, fmt.Errorf("failed to read ssh key for %s: %w", name, err)
			} else {
				return returnBytes(kdata), nil
			}
		}

		// Set
		if x := a.GetUsername(); x != nil && (!x.GetGet() || x.GetSet() != "") {
			return returnEmpty(p.Auth().Get(name).SetUsername(x.GetSet()))
		}

		if x := a.GetPassword(); x != nil && (!x.GetGet() || x.GetSet() != "") {
			return returnEmpty(p.Auth().Get(name).SetPassword(x.GetSet()))
		}

		if x := a.GetKey(); x != nil {
			if z := x.GetPath(); z != nil && (!z.GetGet() || z.GetSet() != "") {
				return returnEmpty(p.Auth().Get(name).SetKey(z.GetSet()))
			} else if z := x.GetData(); z != nil && (!z.GetGet() || z.GetSet() != nil) {
				kw, err := p.Auth().Get(name).Create()
				if err != nil {
					return nil, fmt.Errorf("failed to set ssh key: %w", err)
				}
				defer kw.Close()

				_, err = io.Copy(kw, bytes.NewBuffer(z.GetSet()))
				if err != nil {
					return nil, fmt.Errorf("failed to write ssh key: %w", err)
				}

				return connect.NewResponse[pb.Return](nil), nil
			} else {
				return nil, errors.New("failed to set undefined ssh key")
			}
		}

		return nil, fmt.Errorf("invalid auth operation for %s", name)
	}

	return nil, errors.New("invalid auth operation")
}
